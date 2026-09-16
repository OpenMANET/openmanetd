// Package audio hosts the malgo (miniaudio) capture/playback wrappers and
// the Opus broadcast encoder used by the comms subsystem. The package owns
// the hardware-bound side of the audio pipeline (malgo stream lifecycle, the
// dedicated encode-and-send goroutine, and the per-port playback open path)
// while the parent comms package retains the orchestration layer
// (CommsConfig/Manager/Service) and the receive-side playback callback.
//
// The package never imports the parent. All dependencies flow in via the
// Deps struct (for the broadcast encoder) and the PortSlot slice (for
// per-port playback streams). The parent supplies callbacks at construction
// so the package neither knows about *PortChannel nor walks the runtime.
package audio

import (
	"fmt"
	"math"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/codec"
	"github.com/openmanet/openmanetd/internal/comms/device"
)

// frameDuration is the wall-clock length of one captured frame at the
// configured sample rate (20 ms at 48 kHz). Used as the per-frame budget
// against which we flag over-budget encodes.
const frameDuration = time.Duration(audiopool.FrameSize) * time.Second /
	time.Duration(audiopool.SampleRate)

// broadcastEncoderChanDepth bounds the queue between the malgo capture
// callback (producer, fires every 20 ms) and the encode-and-send goroutine
// (consumer). 10 frames = 200 ms of consumer slack before the producer
// starts dropping frames. The depth is sized to match the receive-side
// jitter buffer prebuffer (rtp.PrebufferPackets * 20 ms) so that any
// transient encoder spike the receiver can absorb downstream the producer
// can absorb upstream too. The previous depth of 3 (60 ms) was too tight
// for slow MIPS targets where Opus encoding plus GC pauses regularly
// crossed the per-frame budget; channel-full drops then manifested as
// stutter on the receive side. Channel-full drops are still counted in
// framesDropped and surfaced in the per-cycle Debug log so a regression
// is loud, not silent.
const broadcastEncoderChanDepth = 10

// TX mic gain is applied in Q8 fixed point: gainQ8 = round(gain * 256),
// so the resolution is 1/256 (~0.034 dB steps) and unity is exactly 256.
// Q8 keeps the per-sample loop integer-only, which matters on the mipsle
// softfloat production targets (mt7621/mt76x8 have no FPU).
const (
	unityGainQ8 int32 = 256
	gainQ8Shift int32 = 8

	// maxGainQ8 caps the Q8 gain at 256x, the largest value for which
	// int32(v)*q cannot wrap for any int16 sample: the worst case,
	// -32768 * 65536, is exactly math.MinInt32. Above this the encode
	// loop's int32 product would overflow before the soft knee runs,
	// turning a misconfigured comms.micGain into wrap-around noise
	// instead of a railed-but-intelligible signal.
	maxGainQ8 int32 = 65536
)

// Soft-knee limiter constants. Post-gain samples inside ±kneeStart pass
// through untouched; beyond it the rational curve
//
//	y = knee + r*d/(d+r), d = |s| - knee, r = rail - knee
//
// compresses the overshoot smoothly toward the rail instead of
// flat-topping. The curve is continuous with slope 1 at the knee (no
// step in level where compression begins) and approaches the rail
// asymptotically, so square-wave harmonics from hard clipping are gone;
// above the knee the transfer is still nonlinear — gentle compression,
// not transparency. Integer-only: the divide runs only on samples past
// the knee, which is the rare loud-talker case.
const (
	kneeStart int32 = 24576             // 0.75 * 32768
	kneePosR  int32 = 32767 - kneeStart // radius to the positive rail
	kneeNegR  int32 = 32768 - kneeStart // radius to the negative rail
)

// softKnee maps a post-gain int32 sample onto int16 range with the
// soft-knee curve above. The int64 intermediates keep r*d exact for any
// gain the config can express.
func softKnee(s int32) int16 {
	if s > kneeStart {
		d := int64(s - kneeStart)

		return int16(int64(kneeStart) + int64(kneePosR)*d/(d+int64(kneePosR)))
	}

	if s < -kneeStart {
		d := -int64(s) - int64(kneeStart)

		return int16(-(int64(kneeStart) + int64(kneeNegR)*d/(d+int64(kneeNegR))))
	}

	return int16(s)
}

// micGainQ8 converts the float config gain to its Q8 representation.
// Non-positive gains read as unity — MicGain unset (zero value) means "no
// gain", matching the previous float implementation's gain > 0 guard. A
// positive gain small enough to round to 0 is clamped to 1 (the smallest
// non-silent Q8 step) so a configured near-zero gain attenuates instead
// of muting outright. Gains above 256x clamp to maxGainQ8 so the encode
// loop's int32 product cannot overflow; the comparison happens in
// float64 BEFORE the int32 conversion because converting an
// out-of-range float to int32 is implementation-dependent in Go.
func micGainQ8(gain float32) int32 {
	if gain <= 0 {
		return unityGainQ8
	}

	scaled := math.Round(float64(gain) * 256)
	if scaled >= float64(maxGainQ8) {
		return maxGainQ8
	}

	q := int32(scaled)
	if q < 1 {
		return 1
	}

	return q
}

// SendFn delivers an Opus payload to every send-enabled multicast port.
// The parent comms package binds it to its sendToAllPorts helper at
// BroadcastEncoder construction so this package never touches port lists.
type SendFn func(payload []byte)

// Deps is the dependency bundle BroadcastEncoder needs from the parent
// package. Construct it once at startup and pass to NewBroadcastEncoder.
type Deps struct {
	Log               zerolog.Logger
	Encoder           codec.AudioEncoder
	Send              SendFn
	Tap               *atomic.Pointer[chan []float32]
	InputDeviceSpec   string
	OutputDeviceSpec  string
	CaptureLatencyMs  int
	PlaybackLatencyMs int
	// CaptureFramesPerBuffer is the per-callback frame count passed to
	// malgo as DeviceConfig.PeriodSizeInFrames. A value of 0 means the
	// default audiopool.FrameSize; a negative value lets miniaudio pick
	// a period aligned with the native ALSA period. A positive value is
	// passed through verbatim. The default (audiopool.FrameSize = 960 @
	// 48 kHz mono = 20 ms) matches the Opus encoder frame so every
	// callback produces exactly one RTP packet with no accumulation
	// step.
	CaptureFramesPerBuffer int
	MicGain                float32
	Trace                  bool
	Debug                  bool
}

// streamInfo is a backend-neutral snapshot of a capture stream's negotiated
// buffering characteristics. miniaudio does not expose a runtime-reported
// "actual latency" so the values carried here reflect the period size we
// configured at open time, converted to a duration. It is enough to drive
// the stream-open diagnostic log line (period_frames, equivalent latency)
// without leaking *malgo.Device internals up to the parent package.
type streamInfo struct {
	InputLatency  time.Duration
	OutputLatency time.Duration
}

// captureStream is the minimal subset of an audio input device lifecycle
// that BroadcastEncoder depends on. It exists so unit tests can inject a
// fake stream without opening real audio hardware and so the encoder is
// not coupled to a particular backend (miniaudio / malgo today, PortAudio
// previously).
type captureStream interface {
	Start() error
	Stop() error
	Close() error
	Info() streamInfo
}

// captureOpener builds a concrete captureStream wired to the encoder's
// captureCallback. The closure is supplied at NewBroadcastEncoder time so
// the encoder itself never imports the backend binding — audio/init.go
// owns the malgo open and wraps it in a closure that matches this type.
type captureOpener func(onFrame func(samples []int16)) (captureStream, error)

// BroadcastEncoder owns the always-on capture stream and a dedicated
// encode-and-send goroutine. It exists so that the audio callback thread
// never runs the Opus encoder or a blocking UDP write — both of those move
// to the goroutine, which has its own scheduling slack absorbed by encCh.
//
// The capture hot path is int16-native: the audio callback receives an
// int16 view over the buffer the backend handed it (via unsafe.Slice
// reinterpret in malgo_capture.go), the encode worker calls EncodeS16
// directly, and the float32↔int16 conversion is eliminated. The pooled
// []int16 frame is released via defer after each send.
//
// Under the unified design the underlying audio device is opened once at
// StartHardware and stays open for the lifetime of the comms run. The
// captureCallback always fires; txEnabled (an atomic bool toggled by
// beginTransmission / endTransmission) gates whether captured frames
// reach the Opus encoder. The VOX tap runs regardless of the gate so the
// ROIP control source can drive PTT off the same always-on capture.
type BroadcastEncoder struct {
	s                captureStream
	encCh            chan *[]int16
	done             chan struct{}
	deps             Deps
	framesDropped    atomic.Int64
	framesEncoded    atomic.Int64
	captureGapMaxNs  atomic.Int64
	encodeErrors     atomic.Int64
	framesCaptured   atomic.Int64
	encodeDurMaxNs   atomic.Int64
	encodeDurSumNs   atomic.Int64
	encodeDurCount   atomic.Int64
	captureLateCount atomic.Int64
	lastCaptureNs    atomic.Int64
	txEnabled        atomic.Bool
	overBudgetWarned atomic.Bool
}

// Compile-time assertion: BroadcastEncoder satisfies device.AudioStream so
// it can be installed via the parent's CommsRuntime.SetBroadcast accessor.
var _ device.AudioStream = (*BroadcastEncoder)(nil)

// NewBroadcastEncoder constructs the wrapper, opens the capture stream
// via the supplied opener closure with be.captureCallback as the
// per-frame hook, and spawns the encode goroutine. The opener is
// provided by audio/init.go (which owns the malgo context) so the
// encoder itself has no dependency on the audio backend.
func NewBroadcastEncoder(deps Deps, open captureOpener) (*BroadcastEncoder, error) {
	be := &BroadcastEncoder{
		deps:  deps,
		encCh: make(chan *[]int16, broadcastEncoderChanDepth),
		done:  make(chan struct{}),
	}

	s, err := open(be.captureCallback)
	if err != nil {
		return nil, fmt.Errorf("open broadcast stream: %w", err)
	}

	be.s = s

	go be.encodeLoop()

	return be, nil
}

// SetTxEnabled toggles the TX gate. When v is true, captured frames
// begin flowing into the Opus encoder + RTP send pipeline; when v is
// false the callback continues to run the VOX tap and advance
// framesCaptured but drops frames before the encCh hand-off. Safe to
// call from any goroutine — the underlying atomic transition is
// observed by the next captureCallback invocation from the audio thread.
//
// SetTxEnabled is the canonical per-PTT toggle. The comms package's
// transmit.beginTransmission / endTransmission drive it. Start/Stop
// on BroadcastEncoder control the underlying device lifecycle and are
// called once per StartHardware cycle, not per PTT.
func (be *BroadcastEncoder) SetTxEnabled(v bool) {
	prev := be.txEnabled.Swap(v)
	if prev == v {
		return
	}

	if v {
		// Fresh TX cycle — reset the per-cycle counters so the Stop
		// log reflects just the current cycle. recordCaptureArrival
		// resets lastCaptureNs on the "first callback" path via the
		// Swap idiom, so the first captured frame under the new gate
		// does not report a spurious gap derived from the pre-gate
		// callback.
		be.framesCaptured.Store(0)
		be.framesEncoded.Store(0)
		be.framesDropped.Store(0)
		be.encodeErrors.Store(0)
		be.encodeDurMaxNs.Store(0)
		be.encodeDurSumNs.Store(0)
		be.encodeDurCount.Store(0)
		be.overBudgetWarned.Store(false)
		be.lastCaptureNs.Store(0)
		be.captureGapMaxNs.Store(0)
		be.captureLateCount.Store(0)

		return
	}

	// Gate closed — log the cycle stats the same way Stop() used to.
	maxDur := time.Duration(be.encodeDurMaxNs.Load())

	var avgDur time.Duration
	if count := be.encodeDurCount.Load(); count > 0 {
		avgDur = time.Duration(be.encodeDurSumNs.Load() / count)
	}

	captureGapMax := time.Duration(be.captureGapMaxNs.Load())

	be.deps.Log.Debug().
		Int64("captured", be.framesCaptured.Load()).
		Int64("encoded", be.framesEncoded.Load()).
		Int64("dropped", be.framesDropped.Load()).
		Int64("encode_errors", be.encodeErrors.Load()).
		Dur("encode_dur_max", maxDur).
		Dur("encode_dur_avg", avgDur).
		Dur("frame_budget", frameDuration).
		Dur("capture_gap_max", captureGapMax).
		Int64("capture_late", be.captureLateCount.Load()).
		Msg("comms: broadcast cycle stats")
}

// captureCallback runs on the audio callback thread and MUST NOT block,
// allocate from the heap, or hold any mutex other than briefly. It does
// three things in order:
//
//  1. Run the VOX tap (always — not gated by TX). The ROIP control source
//     reads from this tap to drive PTT. When no tap is subscribed the
//     branch is a zero-overhead atomic.Load + nil check.
//  2. If the TX gate is closed, return early without copying into the
//     encode channel. framesCaptured and gap statistics still advance.
//  3. Copy the int16 frame into a pooled buffer and non-blockingly hand
//     it to the encode goroutine via encCh.
func (be *BroadcastEncoder) captureCallback(in []int16) {
	be.recordCaptureArrival(time.Now())

	// Optional VOX tap. The ROIP VOX consumer (control/roip.go) operates on
	// float32 frames for RMS energy. This is a boundary conversion off the
	// hot path — the tap is only active when the ROIP control source is
	// selected and VOX is currently monitoring. Regular TX (openvlm,
	// nanoptt, web) never enters this branch and pays nothing.
	if be.deps.Tap != nil {
		if tapPtr := be.deps.Tap.Load(); tapPtr != nil {
			fp := audiopool.Float32Pool.Get().(*[]float32) //nolint:forcetypeassert

			f := (*fp)[:audiopool.FrameSize]
			for i, v := range in {
				f[i] = float32(v) / 32768
			}

			select {
			case *tapPtr <- f:
			default:
				audiopool.ReturnFloat32(f)
			}
		}
	}

	be.framesCaptured.Add(1)

	// TX gate: when closed, the encoder pipeline is dormant. The capture
	// stream itself stays active so the VOX tap above keeps observing
	// mic audio, which is what lets the ROIP control source make PTT
	// decisions while otherwise "idle".
	if !be.txEnabled.Load() {
		return
	}

	fp := audiopool.Int16Pool.Get().(*[]int16) //nolint:forcetypeassert
	f := (*fp)[:audiopool.FrameSize]
	copy(f, in)
	*fp = f

	// Non-blocking hand-off. If the consumer is so far behind that
	// broadcastEncoderChanDepth frames of slack are exhausted, drop this
	// frame and count it. The audio callback MUST NOT block.
	select {
	case be.encCh <- fp:
	default:
		be.framesDropped.Add(1)
		audiopool.Int16Pool.Put(fp)
	}
}

// encodeLoop drains encCh, applying gain → Opus EncodeS16 → RTP send.
// Exits when encCh is closed by Close.
func (be *BroadcastEncoder) encodeLoop() {
	defer close(be.done)

	for fp := range be.encCh {
		be.encodeOne(fp)
	}
}

// encodeOne processes a single captured frame: applies mic gain in the
// integer domain (soft-knee limited to int16 range), encodes via EncodeS16, and
// ships the payload via Deps.Send. Pool buffers are released via defer so
// a panic in the encoder still returns them.
func (be *BroadcastEncoder) encodeOne(fp *[]int16) {
	defer audiopool.Int16Pool.Put(fp)

	pcm := *fp

	if q := micGainQ8(be.deps.MicGain); q != unityGainQ8 {
		// Apply gain in Q8 fixed point, limited by the soft knee. The
		// loop is integer-only: on mipsle softfloat targets the previous
		// float32 multiply emitted ~4 800 software-float runtime calls
		// per frame; the one float→Q8 conversion above is per frame,
		// not per sample.
		for i, v := range pcm {
			pcm[i] = softKnee((int32(v) * q) >> gainQ8Shift)
		}
	}

	bufPtr := audiopool.EncBufPool.Get().(*[]byte) //nolint:forcetypeassert

	buf := *bufPtr
	defer audiopool.EncBufPool.Put(bufPtr)

	encStart := time.Now()
	n, encErr := be.deps.Encoder.EncodeS16(pcm, buf)
	encDur := time.Since(encStart)

	be.recordEncodeDuration(encDur)

	if encErr != nil {
		be.encodeErrors.Add(1)
		be.deps.Log.Debug().Err(encErr).Msg("comms: opus encode failed")

		return
	}

	be.framesEncoded.Add(1)
	be.deps.Send(buf[:n])

	if be.deps.Trace {
		be.deps.Log.Trace().Int("encoded_bytes", n).Msg("comms: multicast packet sent")
	}
}

// recordCaptureArrival tracks inter-arrival timing between consecutive
// malgo capture callbacks. The first callback in a cycle has no previous
// timestamp to compare against, so it only seeds lastCaptureNs and
// returns. Subsequent callbacks compute the delta from the previous
// arrival, update the running max via a CAS loop, and increment the
// late-callback counter if the gap ≥ 2 * frameDuration.
//
// captureCallback is single-producer (one malgo stream thread), so the
// Load/Store/CAS sequence races only against resets in Start, which is
// guaranteed to run with the stream stopped.
func (be *BroadcastEncoder) recordCaptureArrival(now time.Time) {
	nowNs := now.UnixNano()

	prevNs := be.lastCaptureNs.Swap(nowNs)
	if prevNs == 0 {
		return
	}

	deltaNs := nowNs - prevNs
	if deltaNs <= 0 {
		return
	}

	for {
		cur := be.captureGapMaxNs.Load()
		if deltaNs <= cur {
			break
		}

		if be.captureGapMaxNs.CompareAndSwap(cur, deltaNs) {
			break
		}
	}

	if deltaNs >= 2*frameDuration.Nanoseconds() {
		be.captureLateCount.Add(1)
	}
}

// recordEncodeDuration accumulates the encode-time stats and emits a
// one-shot Warn the first time a frame crosses the per-frame budget
// within a Start/Stop cycle. Lock-free: max is updated via a CAS loop,
// sum/count via plain Add, the warn flag via CompareAndSwap.
func (be *BroadcastEncoder) recordEncodeDuration(d time.Duration) {
	ns := d.Nanoseconds()

	be.encodeDurSumNs.Add(ns)
	be.encodeDurCount.Add(1)

	for {
		cur := be.encodeDurMaxNs.Load()
		if ns <= cur {
			break
		}

		if be.encodeDurMaxNs.CompareAndSwap(cur, ns) {
			break
		}
	}

	if d >= frameDuration && be.overBudgetWarned.CompareAndSwap(false, true) {
		be.deps.Log.Warn().
			Dur("encode_dur", d).
			Dur("frame_budget", frameDuration).
			Msg("comms: opus encode exceeded per-frame budget; expect TX frame drops " +
				"and RX stutter — lower cfg.EncoderComplexity")
	}
}

// Start activates the underlying capture device. Called once per comms
// run from audio.StartHardware, not per PTT cycle. Per-PTT state (frame
// counters, gap statistics, TX gate) is handled in SetTxEnabled. Under
// the unified design the capture callback runs continuously from Start
// to Stop; SetTxEnabled gates whether captured frames are forwarded to
// the Opus encoder.
func (be *BroadcastEncoder) Start() error {
	if err := be.s.Start(); err != nil {
		return fmt.Errorf("broadcast stream start: %w", err)
	}

	return nil
}

// Stop halts the underlying capture device. Called once per comms run
// from the StartHardware cleanup closure.
func (be *BroadcastEncoder) Stop() error {
	if err := be.s.Stop(); err != nil {
		return fmt.Errorf("broadcast stream stop: %w", err)
	}

	return nil
}

// Close stops the audio thread, terminates the encode goroutine, and
// releases the underlying device resources. Called once per comms run
// from the StartHardware cleanup closure.
func (be *BroadcastEncoder) Close() error {
	_ = be.s.Stop()
	close(be.encCh)
	<-be.done

	if err := be.s.Close(); err != nil {
		return fmt.Errorf("broadcast stream close: %w", err)
	}

	return nil
}
