package comms

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/openmanet/openmanetd/internal/comms/codec"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/device"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
)

// ─── McastPortConfig / McastPortState ────────────────────────────────────────

// McastPortConfig describes a single multicast endpoint that the comms
// subsystem listens and/or transmits on. Ports with Send=false will not open
// an RTP/RTCP sender; ports with Receive=false will not open an RTP receiver
// socket.
//
// InitSendEnabled and InitReceiveEnabled seed the runtime atomic flags that
// EnableTalkGroupSend / EnableTalkGroupReceive toggle at runtime. When nil
// the values fall back to Send and Receive respectively, preserving backward
// compatibility for any caller that constructs McastPortConfig directly.
type McastPortConfig struct {
	InitSendEnabled    *bool
	InitReceiveEnabled *bool
	Address            string
	Port               int
	Send               bool
	Receive            bool
}

// McastPortState is a read-only snapshot of the runtime direction-toggle state
// for a single port. Returned by Service.TalkGroupStates.
type McastPortState struct {
	Address        string
	Port           int
	SendEnabled    bool
	ReceiveEnabled bool
}

// ─── PortChannel ─────────────────────────────────────────────────────────────

// PortChannel holds all live resources for one McastPortConfig entry.
// sendEnabled and receiveEnabled are atomic bools that can be toggled at
// runtime via EnableTalkGroupSend / EnableTalkGroupReceive without restarting any goroutine
// or socket.
//
// jitter is the per-port RTP jitter buffer. It is allocated in
// buildSinglePortChannel for ports with a Receive socket and shared between
// receiveLoop (producer) and the malgo playback callback (consumer). For
// portChannels constructed directly in tests, callers must allocate it
// explicitly.
//
// consecutivePLC is owned by the malgo playback callback for this port:
// each port has its own callback running on its own audio thread, so the
// field is single-writer and does not need atomic semantics. Tests that call
// playoutOneFrame directly are likewise single-threaded with respect to it.
//
// playbackBuffer is retained as a one-shot side channel for TX beep tones
// (see transmit.go beginTransmission/endTransmission); the malgo playback callback
// drains it before falling through to playoutOneFrame so beeps preempt one
// frame of jitter-buffered audio.
type PortChannel struct { //nolint:govet // fieldalignment: playbackMu must sit directly above the playback fields it guards (.claude/rules/concurrency.md); the pointer-scan-optimal layout would separate them.
	// Decoder is this port's private Opus decoder, allocated by
	// buildPortDecoders for every receive-capable port. It MUST NOT be
	// shared between ports: each port's malgo playback callback runs on its
	// own audio thread and any number of ports can be receive-enabled
	// concurrently, so a shared decoder would be a C-level data race inside
	// libopus — and even serialized, interleaving two RTP streams through
	// one stateful decoder corrupts the prediction state of both. Per-port
	// state also keeps PLC continuity correct per stream.
	Decoder codec.AudioDecoder
	RTPSess rtp.Sender

	// playbackMu protects the three playback lifecycle fields below. The
	// stream pointer and running flag are written by audio
	// init/recovery/teardown (Start goroutine, Run goroutine) and read by
	// the RX toggle path (RPC goroutines) and beep routing, so every
	// access goes through the lifecycle methods in this file. The mutex is
	// deliberately held across the device Start/Stop CGO calls — that
	// serialization is the point: malgo forbids concurrent start/stop on
	// one device, and the calls are millisecond-scale on human-driven
	// toggle/beep paths only, never on the audio or packet hot paths.
	playbackMu      sync.Mutex
	PlaybackStream  device.AudioStream
	playbackRunning bool
	// beepStopTimer re-sleeps a stream that queueBeep woke solely to make
	// a PTT beep audible (no port receive-enabled). Reset on every beep so
	// back-to-back beeps extend the window instead of truncating.
	beepStopTimer *time.Timer

	Sender            *rtp.SwappableSender
	RTCPSend          *rtp.SwappableSender
	Receiver          *rtp.SwappableReceiver
	Jitter            *rtp.JitterBuffer
	PlaybackBuffer    chan []int16
	cfg               McastPortConfig
	RxGate            control.HalfDuplexGate
	ConsecutivePLC    int
	PlaybackUnderruns atomic.Int64
	RxPkts            atomic.Int64
	RxParseErrs       atomic.Int64
	RxLoopback        atomic.Int64
	RxPushed          atomic.Int64
	RxPushRejected    atomic.Int64
	WebPoppedSkipped  atomic.Int64
	// QoSDSCP and QoSSOPriority record the QoS marking the kernel actually
	// holds on this port's RTP sender socket (read back after
	// setQoSMarking at build time; RTCP carries identical marking). Both
	// are 0 for unmarked ports (comms.dscp 0, Send=false, or marking
	// failure). Set once before the runtime is published; atomics keep the
	// instrumentation contract (snapshot reads are atomic-load-only).
	QoSDSCP        atomic.Int32
	QoSSOPriority  atomic.Int32
	SendEnabled    atomic.Bool
	ReceiveEnabled atomic.Bool
}

// ─── Playback stream lifecycle ───────────────────────────────────────────────
//
// Under the P4 idle-cost design, a port's malgo playback device stays OPEN
// for the comms run (ALSA/USB open is slow and fragile; instant RX toggling
// is a product requirement) but its stream only RUNS while the port is
// receive-enabled — ma_device_stop/start, no renegotiation. These methods
// are the only sanctioned access to PlaybackStream; see the field comment
// for the locking rationale.

// setPlaybackStream installs (or replaces) the port's playback stream in
// the not-running state. Called via the audio.PortSlot SetStream hook
// during init and recovery.
func (pc *PortChannel) setPlaybackStream(s device.AudioStream) {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	pc.PlaybackStream = s
	pc.playbackRunning = false
}

// markPlaybackRunning records that the stream was started externally
// (audio.StartHardware starts every stream itself) without touching the
// device.
func (pc *PortChannel) markPlaybackRunning() {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	pc.playbackRunning = true
}

// clearPlaybackStream detaches the stream at teardown so a late toggle or
// beep can never call into a freed malgo device. Also run on failed init,
// where the SetStream hook may have stored streams that BuildAudio's
// unwind already closed.
func (pc *PortChannel) clearPlaybackStream() {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	pc.PlaybackStream = nil
	pc.playbackRunning = false
}

// playbackStreamInstalled reports whether a playback stream is attached
// to this port (regardless of running state).
func (pc *PortChannel) playbackStreamInstalled() bool {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	return pc.PlaybackStream != nil
}

// playbackIsRunning reports whether the port's playback stream is
// currently started.
func (pc *PortChannel) playbackIsRunning() bool {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	return pc.PlaybackStream != nil && pc.playbackRunning
}

// startPlayback starts the port's playback stream. Idempotent; a nil
// stream (web mode, audio-failed mode, torn down) is a successful no-op.
func (pc *PortChannel) startPlayback() error {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	if pc.PlaybackStream == nil || pc.playbackRunning {
		return nil
	}

	if err := pc.PlaybackStream.Start(); err != nil {
		return fmt.Errorf("start playback stream: %w", err)
	}

	pc.playbackRunning = true

	return nil
}

// stopPlayback stops the port's playback stream. Idempotent; a nil stream
// is a successful no-op.
func (pc *PortChannel) stopPlayback() error {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	if pc.PlaybackStream == nil || !pc.playbackRunning {
		return nil
	}

	if err := pc.PlaybackStream.Stop(); err != nil {
		return fmt.Errorf("stop playback stream: %w", err)
	}

	pc.playbackRunning = false

	return nil
}

// stopPlaybackIfDisabled re-sleeps the stream unless the port has been
// receive-enabled in the meantime. Used as the beep-wake timer callback:
// the race where RX gets enabled right after the check simply leaves the
// stream running, which is the enabled port's correct state anyway.
func (pc *PortChannel) stopPlaybackIfDisabled() {
	if pc.ReceiveEnabled.Load() {
		return
	}

	// Best-effort: a stop failure here leaves an idle stream running,
	// which is the pre-P4 status quo, not a correctness problem.
	_ = pc.stopPlayback()
}

// armBeepSleep schedules (or extends) the re-sleep of a beep-woken
// stream. A single reusable timer per port: Reset on every beep so a
// stop-beep arriving late in a previous window gets a full window of its
// own instead of being truncated mid-tone.
func (pc *PortChannel) armBeepSleep(d time.Duration) {
	pc.playbackMu.Lock()
	defer pc.playbackMu.Unlock()

	if pc.beepStopTimer == nil {
		pc.beepStopTimer = time.AfterFunc(d, pc.stopPlaybackIfDisabled)

		return
	}

	pc.beepStopTimer.Reset(d)
}

// MarkRemoteRx records that a remote RTP packet has just been received on
// this port for half-duplex enforcement. It stamps the port's RxGate and,
// when this port is currently send-enabled, primes the runtime-wide
// RemoteRxActive cache so the PTT TX path observes a busy channel without
// waiting for the next halfDuplexDecayLoop tick. Receive-only ports never
// block our own transmissions, so the cache is left untouched in that case.
//
// Production callers reach this from receiveLoop after a successful RTP
// parse; tests use it as the single canonical way to express "a remote
// packet arrived" so the cache invariant is exercised the same way it is
// in production.
func (pc *PortChannel) MarkRemoteRx(rt *CommsRuntime) {
	pc.RxGate.Mark()

	if pc.SendEnabled.Load() {
		rt.RemoteRxActive.Store(true)
	}
}

// closePartial closes any sockets and the RTP session that this PortChannel
// has acquired so far. It is safe to call on a nil receiver and on a
// PortChannel where some fields are still nil — used both as the rollback
// path inside buildSinglePortChannel and as the bulk cleanup path in
// buildNetwork when a later port fails.
func (pc *PortChannel) closePartial() {
	if pc == nil {
		return
	}

	if pc.Receiver != nil {
		_ = pc.Receiver.Close()
	}

	if s, ok := pc.RTPSess.(*rtp.Session); ok && s != nil {
		_ = s.Close()
	}

	if pc.Sender != nil {
		_ = pc.Sender.Close()
	}

	if pc.RTCPSend != nil {
		_ = pc.RTCPSend.Close()
	}
}
