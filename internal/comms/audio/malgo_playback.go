package audio

import (
	"fmt"

	"github.com/gen2brain/malgo"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/device"
)

// playbackChunker re-aligns variable-length malgo output callback
// requests onto fixed audiopool.FrameSize chunks pulled from the
// playoutFrame closure (or the beep channel). It owns no
// synchronization because the malgo Data callback for a single device
// is always invoked from the same miniaudio worker thread.
//
// Why this exists: ALSA's USB audio class driver typically rounds
// requested period sizes up to a power of two (e.g. it gives us
// 1024-frame periods when we ask for 960). The downstream
// playoutFrame closure (cfg.playoutOneFrame) was written assuming
// exactly one Opus frame per call (audiopool.FrameSize == 960). The
// chunker stages a 960-sample buffer between the malgo callback and
// playoutFrame so the latter always sees a properly sized slice
// regardless of what miniaudio asks for.
type playbackChunker struct {
	beepBuf      <-chan []int16
	playoutFrame func(out []int16)
	chunk        []int16
	pos          int
	chunkSize    int
}

// newPlaybackChunker constructs a chunker in the "drained" state, so
// the very first drain() call triggers an immediate fill() rather than
// emitting silence from a half-initialized staging buffer.
//
// Chunk size is fixed at audiopool.FrameSize (960 samples = 20 ms @
// 48 kHz) — the same size the Opus codec / playoutFrame closure
// require. There is no use case for a different chunk size in this
// codebase, so it is not exposed as a parameter.
func newPlaybackChunker(
	beepBuf <-chan []int16,
	playoutFrame func(out []int16),
) *playbackChunker {
	return &playbackChunker{
		chunk:        make([]int16, audiopool.FrameSize),
		pos:          audiopool.FrameSize,
		chunkSize:    audiopool.FrameSize,
		beepBuf:      beepBuf,
		playoutFrame: playoutFrame,
	}
}

// fill produces the next chunk of audio into pc.chunk, preferring a
// pending start/stop beep over the regular playoutFrame source. Always
// leaves pc.pos = 0 and pc.chunk completely populated.
//
// Beep semantics: a queued beep replaces exactly one full chunk; the
// beep generator (lifecycle.go) produces frames sized to audiopool.
// FrameSize, so the copy fills the entire staging buffer. If a future
// producer ever sends a shorter beep, the trailing tail will simply
// contain stale samples from the previous fill — these are overwritten
// by the next fill before they would ever reach the speaker.
func (pc *playbackChunker) fill() {
	select {
	case data := <-pc.beepBuf:
		copy(pc.chunk, data)
	default:
		pc.playoutFrame(pc.chunk)
	}

	pc.pos = 0
}

// drain copies as many samples as fit into out, refilling the staging
// buffer via fill() whenever it is exhausted. Returns once out is
// completely filled.
//
// Single-producer: must only be called from the miniaudio worker
// thread that owns this chunker.
func (pc *playbackChunker) drain(out []int16) {
	for len(out) > 0 {
		if pc.pos == pc.chunkSize {
			pc.fill()
		}

		n := min(pc.chunkSize-pc.pos, len(out))
		copy(out, pc.chunk[pc.pos:pc.pos+n])
		pc.pos += n
		out = out[n:]
	}
}

// openMalgoPlayback configures and initializes a malgo playback device
// in FormatS16 bound to the supplied beepBuf / playoutFrame closures.
// The returned *malgo.Device has not been started yet — the caller
// must invoke Start() via device.NewMalgoStream to activate the
// callback thread.
//
// A playbackChunker is allocated per device and captured by the onData
// closure. The chunker hides any ALSA-imposed period rounding from the
// downstream playoutFrame closure, which always sees exactly one
// audiopool.FrameSize chunk per call.
func openMalgoPlayback(
	ctx *malgo.AllocatedContext,
	dev device.AudioDeviceInfo,
	sampleRate uint32,
	channels uint32,
	periodFrames uint32,
	beepBuf <-chan []int16,
	playoutFrame func(out []int16),
) (*malgo.Device, error) {
	if ctx == nil {
		return nil, fmt.Errorf("malgo playback: nil context")
	}

	cfg := malgo.DefaultDeviceConfig(malgo.Playback)
	cfg.Playback.Format = malgo.FormatS16
	cfg.Playback.Channels = channels
	cfg.Playback.DeviceID = dev.ID.Pointer()
	cfg.SampleRate = sampleRate
	cfg.PeriodSizeInFrames = periodFrames
	cfg.PerformanceProfile = malgo.LowLatency
	cfg.Alsa.NoMMap = 0
	applyBluetoothTiming(&cfg, dev.Name)

	chunker := newPlaybackChunker(beepBuf, playoutFrame)

	onData := func(out, _ []byte, frameCount uint32) {
		if len(out) == 0 || frameCount == 0 {
			return
		}

		samples := int16View(out, int(frameCount)*int(channels))
		chunker.drain(samples)
	}

	d, err := malgo.InitDevice(ctx.Context, cfg, malgo.DeviceCallbacks{Data: onData})
	if err != nil {
		return nil, fmt.Errorf("malgo init playback device: %w", err)
	}

	return d, nil
}
