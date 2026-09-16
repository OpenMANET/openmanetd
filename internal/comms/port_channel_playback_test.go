package comms

import (
	"errors"
	"sync"
	"testing"
)

// fakeAudioStream satisfies device.AudioStream with mutex-protected call
// counters and per-method error injection.
type fakeAudioStream struct {
	mu         sync.Mutex
	startCalls int
	stopCalls  int
	closeCalls int
	startErr   error
	stopErr    error
}

func (f *fakeAudioStream) Start() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.startCalls++

	return f.startErr
}

func (f *fakeAudioStream) Stop() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.stopCalls++

	return f.stopErr
}

func (f *fakeAudioStream) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closeCalls++

	return nil
}

func (f *fakeAudioStream) counts() (start, stop int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.startCalls, f.stopCalls
}

// TestPortChannel_PlaybackLifecycle pins the per-port stream state machine:
// start/stop are idempotent (never a double ma_device_start/stop), a nil
// stream is a safe no-op, and clearing detaches the stream so a
// post-teardown toggle can never touch a freed malgo device.
func TestPortChannel_PlaybackLifecycle(t *testing.T) {
	fs := &fakeAudioStream{}
	pc := &PortChannel{}
	pc.setPlaybackStream(fs)

	if pc.playbackIsRunning() {
		t.Error("freshly installed stream must not be marked running")
	}

	if err := pc.startPlayback(); err != nil {
		t.Fatal(err)
	}

	if !pc.playbackIsRunning() {
		t.Error("startPlayback must mark the stream running")
	}

	// Idempotent: a second start must not reach the device.
	if err := pc.startPlayback(); err != nil {
		t.Fatal(err)
	}

	if s, _ := fs.counts(); s != 1 {
		t.Errorf("device Start called %d times, want 1", s)
	}

	if err := pc.stopPlayback(); err != nil {
		t.Fatal(err)
	}

	if pc.playbackIsRunning() {
		t.Error("stopPlayback must clear the running state")
	}

	// Idempotent: a second stop must not reach the device.
	if err := pc.stopPlayback(); err != nil {
		t.Fatal(err)
	}

	if _, st := fs.counts(); st != 1 {
		t.Errorf("device Stop called %d times, want 1", st)
	}
}

func TestPortChannel_PlaybackNilStream(t *testing.T) {
	pc := &PortChannel{}

	// All no-ops without a stream (web mode, audio-failed mode).
	if err := pc.startPlayback(); err != nil {
		t.Errorf("startPlayback with nil stream: %v", err)
	}

	if pc.playbackIsRunning() {
		t.Error("nil stream can never be running")
	}

	if err := pc.stopPlayback(); err != nil {
		t.Errorf("stopPlayback with nil stream: %v", err)
	}
}

func TestPortChannel_ClearPlaybackStream(t *testing.T) {
	fs := &fakeAudioStream{}
	pc := &PortChannel{}
	pc.setPlaybackStream(fs)
	pc.markPlaybackRunning()

	pc.clearPlaybackStream()

	if pc.playbackIsRunning() {
		t.Error("cleared stream must not report running")
	}

	// A start after teardown must never reach the (freed) device.
	if err := pc.startPlayback(); err != nil {
		t.Fatal(err)
	}

	if s, _ := fs.counts(); s != 0 {
		t.Errorf("device Start called %d times after clear, want 0", s)
	}
}

func TestPortChannel_MarkPlaybackRunning(t *testing.T) {
	fs := &fakeAudioStream{}
	pc := &PortChannel{}
	pc.setPlaybackStream(fs)

	// StartHardware starts every stream itself; markPlaybackRunning
	// records that fact without touching the device.
	pc.markPlaybackRunning()

	if !pc.playbackIsRunning() {
		t.Error("markPlaybackRunning must mark the stream running")
	}

	if s, _ := fs.counts(); s != 0 {
		t.Errorf("device Start called %d times, want 0", s)
	}
}

func TestPortChannel_StartPlaybackError(t *testing.T) {
	fs := &fakeAudioStream{startErr: errors.New("device gone")}
	pc := &PortChannel{}
	pc.setPlaybackStream(fs)

	if err := pc.startPlayback(); err == nil {
		t.Error("startPlayback must propagate the device error")
	}

	if pc.playbackIsRunning() {
		t.Error("failed start must not mark the stream running")
	}
}

// ─── queueBeep routing ───────────────────────────────────────────────────────

// twoPortBeepRuntime builds a runtime with two receive-capable ports whose
// playback streams are the supplied fakes (nil = no stream installed).
func twoPortBeepRuntime(s0, s1 *fakeAudioStream) (*CommsRuntime, *PortChannel, *PortChannel) {
	pc0 := &PortChannel{cfg: McastPortConfig{Receive: true}}
	pc0.PlaybackBuffer = make(chan []int16, 4)

	if s0 != nil {
		pc0.setPlaybackStream(s0)
	}

	pc1 := &PortChannel{cfg: McastPortConfig{Receive: true}}
	pc1.PlaybackBuffer = make(chan []int16, 4)

	if s1 != nil {
		pc1.setPlaybackStream(s1)
	}

	rt := &CommsRuntime{
		Ports:           []*PortChannel{pc0, pc1},
		BeepBufferStart: []int16{100, 200},
		BeepBufferStop:  []int16{300, 400},
	}

	return rt, pc0, pc1
}

// TestQueueBeep_SingleRunningStream pins the single-beep contract: the
// beep goes to exactly one port — the first with a running stream — never
// fanned out to every port (the pre-P4 behavior played N overlapping
// copies through dmix).
func TestQueueBeep_SingleRunningStream(t *testing.T) {
	s0, s1 := &fakeAudioStream{}, &fakeAudioStream{}
	rt, pc0, pc1 := twoPortBeepRuntime(s0, s1)
	pc0.markPlaybackRunning()
	pc1.markPlaybackRunning()

	cfg := newSilentComms()
	cfg.queueBeep(rt, rt.BeepBufferStart)

	if got := len(pc0.PlaybackBuffer); got != 1 {
		t.Errorf("port 0 beeps queued: got %d, want 1", got)
	}

	if got := len(pc1.PlaybackBuffer); got != 0 {
		t.Errorf("port 1 beeps queued: got %d, want 0 (single-beep contract)", got)
	}
}

// TestQueueBeep_SkipsStoppedStream pins the running-first preference: a
// stopped port is passed over in favor of a running one, and the stopped
// stream is not started.
func TestQueueBeep_SkipsStoppedStream(t *testing.T) {
	s0, s1 := &fakeAudioStream{}, &fakeAudioStream{}
	rt, pc0, pc1 := twoPortBeepRuntime(s0, s1)
	// pc0 stopped (RX disabled), pc1 running.
	pc1.markPlaybackRunning()

	cfg := newSilentComms()
	cfg.queueBeep(rt, rt.BeepBufferStart)

	if got := len(pc1.PlaybackBuffer); got != 1 {
		t.Errorf("running port 1 beeps: got %d, want 1", got)
	}

	if got := len(pc0.PlaybackBuffer); got != 0 {
		t.Errorf("stopped port 0 beeps: got %d, want 0", got)
	}

	if s, _ := s0.counts(); s != 0 {
		t.Errorf("stopped port 0 device started %d times, want 0 (a running port was available)", s)
	}
}

// TestQueueBeep_WakesStreamWhenAllStopped pins the wake-for-beep path: with
// zero receive-enabled ports (all streams stopped) the beep must still be
// audible, so the first stream is started, the beep queued to it, and the
// re-sleep timer armed.
func TestQueueBeep_WakesStreamWhenAllStopped(t *testing.T) {
	s0, s1 := &fakeAudioStream{}, &fakeAudioStream{}
	rt, pc0, pc1 := twoPortBeepRuntime(s0, s1)

	cfg := newSilentComms()
	cfg.queueBeep(rt, rt.BeepBufferStart)

	if !pc0.playbackIsRunning() {
		t.Error("port 0 stream should be woken for the beep")
	}

	if got := len(pc0.PlaybackBuffer); got != 1 {
		t.Errorf("port 0 beeps: got %d, want 1", got)
	}

	if pc1.playbackIsRunning() {
		t.Error("only one stream should wake for the beep")
	}

	pc0.playbackMu.Lock()
	armed := pc0.beepStopTimer != nil
	pc0.playbackMu.Unlock()

	if !armed {
		t.Error("beep-woken stream must have its re-sleep timer armed")
	}
}

// TestQueueBeep_WakeFallsThroughOnStartError pins degraded-device
// behavior: if waking the first stream fails, the next one is tried.
func TestQueueBeep_WakeFallsThroughOnStartError(t *testing.T) {
	s0 := &fakeAudioStream{startErr: errors.New("device gone")}
	s1 := &fakeAudioStream{}
	rt, _, pc1 := twoPortBeepRuntime(s0, s1)

	cfg := newSilentComms()
	cfg.queueBeep(rt, rt.BeepBufferStart)

	if !pc1.playbackIsRunning() {
		t.Error("port 1 should be woken when port 0's device fails")
	}

	if got := len(pc1.PlaybackBuffer); got != 1 {
		t.Errorf("port 1 beeps: got %d, want 1", got)
	}
}

// TestQueueBeep_NoStreamFallback pins the legacy no-local-audio path (web
// mode, audio-failed mode, unit tests): with no streams installed at all,
// the beep still queues to the first port's buffer — harmless with no
// consumer, and exactly what the pre-P4 code did.
func TestQueueBeep_NoStreamFallback(t *testing.T) {
	rt, pc0, pc1 := twoPortBeepRuntime(nil, nil)

	cfg := newSilentComms()
	cfg.queueBeep(rt, rt.BeepBufferStart)

	if got := len(pc0.PlaybackBuffer); got != 1 {
		t.Errorf("port 0 beeps: got %d, want 1", got)
	}

	if got := len(pc1.PlaybackBuffer); got != 0 {
		t.Errorf("port 1 beeps: got %d, want 0", got)
	}
}

// ─── startup sync + RX toggle wiring ─────────────────────────────────────────

// TestMarkAndSyncPlayback pins the boot/recovery contract: StartHardware
// starts every stream, then the comms side records that fact and re-sleeps
// the streams of ports that are not receive-enabled — so the default
// config (5 ports, 1 enabled) runs exactly one playback RT thread.
func TestMarkAndSyncPlayback(t *testing.T) {
	s0, s1 := &fakeAudioStream{}, &fakeAudioStream{}
	rt, pc0, pc1 := twoPortBeepRuntime(s0, s1)
	pc0.ReceiveEnabled.Store(true)
	pc1.ReceiveEnabled.Store(false)

	cfg := newSilentComms()
	cfg.markAndSyncPlayback(rt)

	if !pc0.playbackIsRunning() {
		t.Error("enabled port 0 must stay running")
	}

	if pc1.playbackIsRunning() {
		t.Error("disabled port 1 must be re-slept")
	}

	if _, st := s0.counts(); st != 0 {
		t.Errorf("enabled port 0 device Stop called %d times, want 0", st)
	}

	if _, st := s1.counts(); st != 1 {
		t.Errorf("disabled port 1 device Stop called %d times, want 1", st)
	}
}

// TestEnableTalkGroupReceive_TogglesPlaybackStream pins the RX toggle:
// enabling starts the port's stream, disabling stops it, and the atomic
// flag flips in both directions.
func TestEnableTalkGroupReceive_TogglesPlaybackStream(t *testing.T) {
	fs := &fakeAudioStream{}
	rt, pc0, _ := twoPortBeepRuntime(fs, nil)

	svc := &Service{Cfg: newSilentComms(), Rt: rt}

	if err := svc.EnableTalkGroupReceive(0, true); err != nil {
		t.Fatal(err)
	}

	if !pc0.ReceiveEnabled.Load() || !pc0.playbackIsRunning() {
		t.Error("enable must set the flag and start the stream")
	}

	if err := svc.EnableTalkGroupReceive(0, false); err != nil {
		t.Fatal(err)
	}

	if pc0.ReceiveEnabled.Load() || pc0.playbackIsRunning() {
		t.Error("disable must clear the flag and stop the stream")
	}

	s, st := fs.counts()
	if s != 1 || st != 1 {
		t.Errorf("device calls: start=%d stop=%d, want 1/1", s, st)
	}
}

// TestEnableTalkGroupReceive_StartErrorIsNonFatal pins degraded-audio
// behavior: a device failure on stream start is logged, not returned —
// the RTP-side enable must still take effect (web mode and audio-failed
// mode have no stream at all, and the audio recovery machinery owns
// device-level failures).
func TestEnableTalkGroupReceive_StartErrorIsNonFatal(t *testing.T) {
	fs := &fakeAudioStream{startErr: errors.New("device gone")}
	rt, pc0, _ := twoPortBeepRuntime(fs, nil)

	svc := &Service{Cfg: newSilentComms(), Rt: rt}

	if err := svc.EnableTalkGroupReceive(0, true); err != nil {
		t.Fatalf("stream failure must not fail the RPC: %v", err)
	}

	if !pc0.ReceiveEnabled.Load() {
		t.Error("RTP-side enable must take effect despite the stream failure")
	}
}

// TestEnableTalkGroupReceive_DrainsStaleBeeps pins the re-enable hygiene:
// a beep queued while the port's stream was stopped must not play when the
// port is enabled minutes later.
func TestEnableTalkGroupReceive_DrainsStaleBeeps(t *testing.T) {
	fs := &fakeAudioStream{}
	rt, pc0, _ := twoPortBeepRuntime(fs, nil)

	pc0.PlaybackBuffer <- []int16{9, 9} // stale beep from a stopped period

	svc := &Service{Cfg: newSilentComms(), Rt: rt}

	if err := svc.EnableTalkGroupReceive(0, true); err != nil {
		t.Fatal(err)
	}

	if got := len(pc0.PlaybackBuffer); got != 0 {
		t.Errorf("stale beeps in buffer after enable: %d, want 0", got)
	}
}

func TestPortChannel_StopPlaybackIfDisabled(t *testing.T) {
	fs := &fakeAudioStream{}
	pc := &PortChannel{}
	pc.setPlaybackStream(fs)
	pc.markPlaybackRunning()

	// RX enabled: the beep-wake sleep callback must leave it running.
	pc.ReceiveEnabled.Store(true)
	pc.stopPlaybackIfDisabled()

	if !pc.playbackIsRunning() {
		t.Error("stopPlaybackIfDisabled must not stop an RX-enabled port")
	}

	// RX disabled: now it stops.
	pc.ReceiveEnabled.Store(false)
	pc.stopPlaybackIfDisabled()

	if pc.playbackIsRunning() {
		t.Error("stopPlaybackIfDisabled must stop an RX-disabled port")
	}
}
