package comms

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
)

func newSilentComms() *CommsConfig {
	return &CommsConfig{Log: zerolog.Nop()}
}

func newTestRuntime(stream BroadcastCapture) *CommsRuntime {
	pc := &PortChannel{
		cfg:     McastPortConfig{Send: true, Receive: true},
		RTPSess: &mockRTPSender{},
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 16)

	rt := &CommsRuntime{
		Ports:           []*PortChannel{pc},
		BeepBufferStart: []int16{100, 200},
		BeepBufferStop:  []int16{300, 400},
	}
	rt.SetBroadcast(stream)

	return rt
}

func TestBeginTransmission_StartsStream(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1", stream.txEnableCalls)
	}
}

func TestBeginTransmission_SetsBroadcasting(t *testing.T) {
	rt := newTestRuntime(&mockStream{})
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if !cfg.isBroadcasting(rt) {
		t.Error("should be broadcasting after beginTransmission")
	}
}

func TestBeginTransmission_QueuesStartBeep(t *testing.T) {
	rt := newTestRuntime(&mockStream{})
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	select {
	case frame := <-rt.Ports[0].PlaybackBuffer:
		if len(frame) != 2 {
			t.Errorf("beep frame len=%d want 2", len(frame))
		}
	default:
		t.Error("expected start beep in buffer")
	}
}

func TestBeginTransmission_DoublePressIgnored(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	for len(rt.Ports[0].PlaybackBuffer) > 0 {
		<-rt.Ports[0].PlaybackBuffer
	}

	cfg.beginTransmission(rt)

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1", stream.txEnableCalls)
	}
}

func TestEndTransmission_StopsStream(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	for len(rt.Ports[0].PlaybackBuffer) > 0 {
		<-rt.Ports[0].PlaybackBuffer
	}

	cfg.endTransmission(rt)

	if stream.txDisableCalls != 1 {
		t.Errorf("SetTxEnabled(false) called %d times, want 1", stream.txDisableCalls)
	}
}

func TestEndTransmission_ClearsBroadcasting(t *testing.T) {
	rt := newTestRuntime(&mockStream{})
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	for len(rt.Ports[0].PlaybackBuffer) > 0 {
		<-rt.Ports[0].PlaybackBuffer
	}

	cfg.endTransmission(rt)

	if cfg.isBroadcasting(rt) {
		t.Error("should not be broadcasting after endTransmission")
	}
}

func TestEndTransmission_WhenNotBroadcasting_Noop(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	cfg := newSilentComms()
	cfg.endTransmission(rt)

	if stream.txDisableCalls != 0 {
		t.Errorf("SetTxEnabled(false) called %d times, want 0", stream.txDisableCalls)
	}
}

func TestDrainPlaybackBuffer(t *testing.T) {
	rt := newTestRuntime(&mockStream{})
	cfg := newSilentComms()

	rt.Ports[0].PlaybackBuffer <- []int16{1}

	rt.Ports[0].PlaybackBuffer <- []int16{2}

	cfg.drainPlaybackBuffer(rt)

	if len(rt.Ports[0].PlaybackBuffer) != 0 {
		t.Errorf("expected empty buffer; got %d items", len(rt.Ports[0].PlaybackBuffer))
	}
}

func TestIsBroadcasting_InitiallyFalse(t *testing.T) {
	rt := newTestRuntime(&mockStream{})

	cfg := newSilentComms()
	if cfg.isBroadcasting(rt) {
		t.Error("should not be broadcasting initially")
	}
}

// TestBeginTransmission_DefaultStartDelay verifies that beginTransmission
// honors the default settle window (defaultPttStartDelayMs) so hardware that
// needs a brief warm-up before the first encoded frame still gets it.
func TestBeginTransmission_DefaultStartDelay(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	cfg := newSilentComms()

	want := defaultPttStartDelayMs * time.Millisecond

	start := time.Now()

	cfg.beginTransmission(rt)

	elapsed := time.Since(start)

	// Allow a tiny tolerance: scheduling, mock-stream Start cost, etc.
	const slop = 10 * time.Millisecond
	if elapsed+slop < want {
		t.Errorf("beginTransmission elapsed=%s, want at least %s (default settle)", elapsed, want)
	}
}

// TestBeginTransmission_SettleCoversPlaybackLatency verifies that when the
// runtime carries a non-zero PlaybackOutputLatency (mirroring what
// audio.Init.BuildAudio derives from the malgo playback period on real hardware),
// beginTransmission sleeps long enough to cover the start-beep's physical
// emission window before starting the mic stream. This is the regression
// guard against the start-beep leaking into the transmitted RTP stream via
// acoustic coupling between the speaker and the mic.
func TestBeginTransmission_SettleCoversPlaybackLatency(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	rt.PlaybackOutputLatency = 100 * time.Millisecond

	cfg := newSilentComms()

	// Expected floor: PlaybackOutputLatency + frameDuration (20 ms) +
	// beepSettleMargin (40 ms) = 160 ms.
	wantFloor := 100*time.Millisecond + frameDuration + beepSettleMargin

	start := time.Now()

	cfg.beginTransmission(rt)

	elapsed := time.Since(start)

	// Allow a small slop below the floor for clock granularity.
	const slop = 10 * time.Millisecond
	if elapsed+slop < wantFloor {
		t.Errorf("beginTransmission elapsed=%s, want at least %s (beep settle floor)", elapsed, wantFloor)
	}

	// Sanity upper bound — settle should not balloon.
	if elapsed > 400*time.Millisecond {
		t.Errorf("beginTransmission elapsed=%s, slept far longer than expected", elapsed)
	}

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times after settle, want 1", stream.txEnableCalls)
	}
}

// TestTransmitSettleWait_NegativeSkips verifies that an explicit negative
// PttStartDelayMs causes transmitSettleWait to return zero even when the
// runtime carries a large PlaybackOutputLatency. The negative value is the
// operator opt-out for low-latency PTT-to-ready and implies acceptance of
// the beep-leak risk.
func TestTransmitSettleWait_NegativeSkips(t *testing.T) {
	rt := newTestRuntime(&mockStream{})
	rt.PlaybackOutputLatency = 500 * time.Millisecond

	cfg := newSilentComms()
	cfg.PttStartDelayMs = -1

	if got := cfg.transmitSettleWait(rt); got != 0 {
		t.Errorf("transmitSettleWait = %s, want 0 when PttStartDelayMs<0", got)
	}
}

// TestTransmitSettleWait_PicksMaxOfWarmupAndBeepSettle verifies the max()
// behavior across the two contributing components: warmup (from
// pttStartDelay) and beep settle (from PlaybackOutputLatency).
func TestTransmitSettleWait_PicksMaxOfWarmupAndBeepSettle(t *testing.T) {
	t.Run("warmup_dominates", func(t *testing.T) {
		rt := newTestRuntime(&mockStream{})
		rt.PlaybackOutputLatency = 0 // beep settle = 0+20+40 = 60 ms

		cfg := newSilentComms()
		cfg.PttStartDelayMs = 100 // explicit warmup

		want := 100 * time.Millisecond
		if got := cfg.transmitSettleWait(rt); got != want {
			t.Errorf("transmitSettleWait = %s, want %s (warmup should win)", got, want)
		}
	})

	t.Run("beep_settle_dominates", func(t *testing.T) {
		rt := newTestRuntime(&mockStream{})
		rt.PlaybackOutputLatency = 80 * time.Millisecond // beep settle = 80+20+40 = 140 ms

		cfg := newSilentComms()
		cfg.PttStartDelayMs = 30 // small warmup

		want := 80*time.Millisecond + frameDuration + beepSettleMargin
		if got := cfg.transmitSettleWait(rt); got != want {
			t.Errorf("transmitSettleWait = %s, want %s (beep settle should win)", got, want)
		}
	})
}

// TestBeginTransmission_ConfigurablePttStartDelay verifies that an explicit
// PttStartDelayMs overrides the default and that a negative value skips the
// wait entirely.
func TestBeginTransmission_ConfigurablePttStartDelay(t *testing.T) {
	t.Run("custom", func(t *testing.T) {
		stream := &mockStream{}
		rt := newTestRuntime(stream)
		cfg := newSilentComms()
		cfg.PttStartDelayMs = 20

		start := time.Now()

		cfg.beginTransmission(rt)

		elapsed := time.Since(start)

		if elapsed < 15*time.Millisecond {
			t.Errorf("expected ~20ms wait, got %s", elapsed)
		}

		if elapsed > 200*time.Millisecond {
			t.Errorf("expected ~20ms wait, got %s (slept too long)", elapsed)
		}
	})

	t.Run("disabled", func(t *testing.T) {
		stream := &mockStream{}
		rt := newTestRuntime(stream)
		cfg := newSilentComms()
		cfg.PttStartDelayMs = -1 // skip the wait entirely

		start := time.Now()

		cfg.beginTransmission(rt)

		elapsed := time.Since(start)

		if elapsed > 30*time.Millisecond {
			t.Errorf("expected near-zero wait when PttStartDelayMs<0, got %s", elapsed)
		}
	})
}

// ─── beginTransmission nil-stream test ───────────────────────────────────────

// newRunRuntime extends newTestRuntime with a receiver/sender so receiveLoop
// started inside Run does not panic.
func newRunRuntime(stream BroadcastCapture) *CommsRuntime {
	rt := newTestRuntime(stream)
	rt.Ports[0].Receiver = rtp.NewSwappableReceiver(newMockReader())
	rt.Ports[0].Sender = rtp.NewSwappableSender(&mockWriter{})

	return rt
}

// TestBeginTransmission_NilStreamClearsBroadcasting verifies that begin with
// a nil BroadcastStream fails cleanly (no panic) and leaves Broadcasting
// false. Under the unified always-on design the stream is opened once at
// StartHardware so a nil here indicates an initialization error, not a
// recoverable runtime condition — there is no reopen-on-demand path.
func TestBeginTransmission_NilStreamClearsBroadcasting(t *testing.T) {
	rt := newTestRuntime(nil)
	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if cfg.isBroadcasting(rt) {
		t.Error("should NOT be broadcasting when BroadcastStream is nil")
	}
}

// ─── Half-duplex tests ────────────────────────────────────────────────────────

func TestBeginTransmission_BlockedWhenReceivingRemote(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	// Simulate a packet that arrived just now from a remote peer. Use the
	// canonical helper so the half-duplex cache is primed the same way
	// receiveLoop does it in production.
	rt.Ports[0].MarkRemoteRx(rt)

	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if stream.txEnableCalls != 0 {
		t.Errorf("SetTxEnabled(true) called %d times, want 0 (channel busy)", stream.txEnableCalls)
	}

	if cfg.isBroadcasting(rt) {
		t.Error("should not be broadcasting while actively receiving remote audio")
	}
}

func TestBeginTransmission_AllowedWhenRxStale(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	// Store a timestamp well beyond rxActiveThreshold.
	rt.Ports[0].RxGate.MarkAt(time.Now().Add(-(rxActiveThreshold + time.Second)))

	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1 (rx is stale)", stream.txEnableCalls)
	}

	if !cfg.isBroadcasting(rt) {
		t.Error("should be broadcasting when last rx is older than rxActiveThreshold")
	}
}

func TestBeginTransmission_AllowedWhenNeverReceived(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	// rxGate is zero — never received a packet.

	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1 (never received)", stream.txEnableCalls)
	}
}

// ─── Run event-loop tests ─────────────────────────────────────────────────────

func TestRun_ExitsOnContextCancel(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	rt := newRunRuntime(&mockStream{})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // pre-cancel so Run returns without blocking

	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.Run(ctx, rt, &mockEventSource{ch: make(chan control.PTTEvent)})
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Run did not exit after context cancel")
	}

	rt.Ports[0].Receiver.Close() // unblock any lingering receiveLoop goroutine
}

func TestRun_ClosedEventChannelExits(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	rt := newRunRuntime(&mockStream{})

	ch := make(chan control.PTTEvent)
	close(ch) // closed before Run is called

	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.Run(context.Background(), rt, &mockEventSource{ch: ch})
	}()

	select {
	case <-done:
	case <-time.After(500 * time.Millisecond):
		t.Error("Run did not exit when event channel is closed")
	}

	rt.Ports[0].Receiver.Close()
}

func TestRun_PTTDownStartsTransmission(t *testing.T) {
	stream := &mockStream{}
	rt := newRunRuntime(stream)
	cfg := &CommsConfig{Log: zerolog.Nop()}

	evCh := make(chan control.PTTEvent, 1)
	evCh <- control.PTTDown

	close(evCh)

	cfg.Run(context.Background(), rt, &mockEventSource{ch: evCh})

	rt.Ports[0].Receiver.Close()

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1", stream.txEnableCalls)
	}
}

func TestRun_PTTUpStopsTransmission(t *testing.T) {
	stream := &mockStream{}
	rt := newRunRuntime(stream)
	cfg := &CommsConfig{Log: zerolog.Nop()}

	evCh := make(chan control.PTTEvent, 2)
	evCh <- control.PTTDown

	evCh <- control.PTTUp

	close(evCh)

	cfg.Run(context.Background(), rt, &mockEventSource{ch: evCh})

	rt.Ports[0].Receiver.Close()

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1", stream.txEnableCalls)
	}

	if stream.txDisableCalls != 1 {
		t.Errorf("SetTxEnabled(false) called %d times, want 1", stream.txDisableCalls)
	}
}

func TestRun_PTTToggleFlips(t *testing.T) {
	stream := &mockStream{}
	rt := newRunRuntime(stream)
	cfg := &CommsConfig{Log: zerolog.Nop()}

	evCh := make(chan control.PTTEvent, 2)
	evCh <- control.PTTToggle // → beginTransmission

	evCh <- control.PTTToggle // → endTransmission

	close(evCh)

	cfg.Run(context.Background(), rt, &mockEventSource{ch: evCh})

	rt.Ports[0].Receiver.Close()

	if stream.txEnableCalls != 1 {
		t.Errorf("SetTxEnabled(true) called %d times, want 1", stream.txEnableCalls)
	}

	if stream.txDisableCalls != 1 {
		t.Errorf("SetTxEnabled(false) called %d times, want 1", stream.txDisableCalls)
	}
}

// ─── Aux event dispatch ──────────────────────────────────────────────────────

// auxEventSource is a test source that satisfies both control.EventSource
// and control.AuxEventSource so the aux-pump path in Run can be exercised.
type auxEventSource struct {
	pttCh chan control.PTTEvent
	auxCh chan control.AuxEvent
}

func (s *auxEventSource) Events(_ context.Context) <-chan control.PTTEvent {
	return s.pttCh
}

func (s *auxEventSource) AuxEvents() <-chan control.AuxEvent { return s.auxCh }

// recordingAuxHandler records every event passed to Handle, mutex-protected
// so tests can inspect from outside the dispatch goroutine.
type recordingAuxHandler struct {
	mu     sync.Mutex
	events []control.AuxEvent
}

func (h *recordingAuxHandler) Handle(_ context.Context, ev control.AuxEvent) {
	h.mu.Lock()
	defer h.mu.Unlock()

	h.events = append(h.events, ev)
}

func (h *recordingAuxHandler) snapshot() []control.AuxEvent {
	h.mu.Lock()
	defer h.mu.Unlock()

	out := make([]control.AuxEvent, len(h.events))
	copy(out, h.events)

	return out
}

func TestRun_AuxEvents_DispatchedToHandler(t *testing.T) {
	stream := &mockStream{}
	rt := newRunRuntime(stream)

	handler := &recordingAuxHandler{}
	cfg := &CommsConfig{Log: zerolog.Nop(), AuxHandler: handler}

	pttCh := make(chan control.PTTEvent)
	close(pttCh) // PTT loop exits immediately so Run returns

	auxCh := make(chan control.AuxEvent, 4)
	auxCh <- control.VolumeUpPressed

	auxCh <- control.VolumeUpReleased

	close(auxCh)

	src := &auxEventSource{pttCh: pttCh, auxCh: auxCh}

	cfg.Run(context.Background(), rt, src)

	// Run returned because PTT channel closed; the aux pump runs on its own
	// goroutine and may still be draining. Wait briefly for it to observe
	// the closed aux channel and exit.
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if len(handler.snapshot()) >= 2 {
			break
		}

		time.Sleep(10 * time.Millisecond)
	}

	got := handler.snapshot()

	want := []control.AuxEvent{control.VolumeUpPressed, control.VolumeUpReleased}
	if len(got) != len(want) {
		t.Fatalf("aux events: got %v, want %v", got, want)
	}

	for i, w := range want {
		if got[i] != w {
			t.Errorf("aux[%d]: got %v, want %v", i, got[i], w)
		}
	}

	rt.Ports[0].Receiver.Close()
}

func TestRun_AuxEvents_IgnoredWhenHandlerNil(t *testing.T) {
	stream := &mockStream{}
	rt := newRunRuntime(stream)

	cfg := &CommsConfig{Log: zerolog.Nop()} // AuxHandler intentionally nil

	pttCh := make(chan control.PTTEvent)
	close(pttCh)

	auxCh := make(chan control.AuxEvent, 1)
	auxCh <- control.VolumeUpPressed

	src := &auxEventSource{pttCh: pttCh, auxCh: auxCh}

	// Just must not panic and must return cleanly.
	cfg.Run(context.Background(), rt, src)

	rt.Ports[0].Receiver.Close()
}

func TestRun_AuxEvents_IgnoredWhenSourceLacksAuxInterface(t *testing.T) {
	stream := &mockStream{}
	rt := newRunRuntime(stream)

	handler := &recordingAuxHandler{}
	cfg := &CommsConfig{Log: zerolog.Nop(), AuxHandler: handler}

	ch := make(chan control.PTTEvent)
	close(ch)

	cfg.Run(context.Background(), rt, &mockEventSource{ch: ch})

	if got := handler.snapshot(); len(got) != 0 {
		t.Errorf("expected no aux events when source lacks AuxEventSource; got %v", got)
	}

	rt.Ports[0].Receiver.Close()
}

// ─── Additional beginTransmission / endTransmission edge cases ────────────────

// TestEndTransmission_QueuesStopBeepToOnePort verifies that endTransmission
// queues beepBufferStop to exactly one port, mirroring the single-beep
// start-beep contract tested by TestBeginTransmission_BeepSentToOnePort.
func TestEndTransmission_QueuesStopBeepToOnePort(t *testing.T) {
	pc0 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc0.PlaybackBuffer = make(chan []int16, 16)

	pc1 := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc1.PlaybackBuffer = make(chan []int16, 16)

	rt := &CommsRuntime{
		Ports:           []*PortChannel{pc0, pc1},
		BeepBufferStart: []int16{100, 200},
		BeepBufferStop:  []int16{300, 400},
	}
	rt.SetBroadcast(&mockStream{})

	cfg := newSilentComms()

	// Begin so broadcasting=true, then drain the start beeps before asserting.
	cfg.beginTransmission(rt)

	for len(pc0.PlaybackBuffer) > 0 {
		<-pc0.PlaybackBuffer
	}

	for len(pc1.PlaybackBuffer) > 0 {
		<-pc1.PlaybackBuffer
	}

	cfg.endTransmission(rt)

	// Exactly one port (the first) receives the stop beep.
	select {
	case frame := <-pc0.PlaybackBuffer:
		if len(frame) != 2 {
			t.Errorf("port 0: stop-beep frame len=%d, want 2", len(frame))
		}
	default:
		t.Error("port 0: expected stop beep in buffer")
	}

	if got := len(pc1.PlaybackBuffer); got != 0 {
		t.Errorf("port 1: beeps queued = %d, want 0 (single-beep contract)", got)
	}
}

// ─── Web-mode tests ────────────────────────────────────────────────────────

func TestBeginTransmission_WebMode_SkipsBroadcastStream(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	rt.WebBridge = &webaudio.Bridge{} // non-nil activates web mode

	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if !cfg.isBroadcasting(rt) {
		t.Error("should be broadcasting in web mode")
	}

	if stream.txEnableCalls != 0 {
		t.Errorf("SetTxEnabled(true) called %d times, want 0 in web mode", stream.txEnableCalls)
	}

	// No beep should be queued.
	select {
	case <-rt.Ports[0].PlaybackBuffer:
		t.Error("unexpected beep in playback buffer in web mode")
	default:
	}
}

func TestEndTransmission_WebMode_SkipsBroadcastStream(t *testing.T) {
	stream := &mockStream{}
	rt := newTestRuntime(stream)
	rt.WebBridge = &webaudio.Bridge{}

	cfg := newSilentComms()

	// Begin first so broadcasting is true.
	rt.Broadcasting.Store(true)
	cfg.endTransmission(rt)

	if cfg.isBroadcasting(rt) {
		t.Error("should not be broadcasting after endTransmission in web mode")
	}

	if stream.txDisableCalls != 0 {
		t.Errorf("SetTxEnabled(false) called %d times, want 0 in web mode", stream.txDisableCalls)
	}

	// No beep should be queued.
	select {
	case <-rt.Ports[0].PlaybackBuffer:
		t.Error("unexpected beep in playback buffer in web mode")
	default:
	}
}

func TestBeginTransmission_WebMode_HalfDuplexStillWorks(t *testing.T) {
	rt := newTestRuntime(&mockStream{})
	rt.WebBridge = &webaudio.Bridge{}
	// Simulate active remote reception via the canonical helper so the
	// half-duplex cache is primed exactly as receiveLoop would.
	rt.Ports[0].MarkRemoteRx(rt)

	cfg := newSilentComms()
	cfg.beginTransmission(rt)

	if cfg.isBroadcasting(rt) {
		t.Error("should not be broadcasting while receiving remote audio, even in web mode")
	}
}

// ─── In-run audio recovery ──────────────────────────────────────────────────

// countingStartHA is a mutex-protected fake for startHardwareAudioFn that
// fails failN times, then succeeds. Success installs a mockStream the same
// way the real startHardwareAudio does and signals succeeded (once).
type countingStartHA struct {
	mu        sync.Mutex
	calls     int
	failN     int
	cleanups  int
	succeeded chan struct{}
}

func (f *countingStartHA) fn(rt *CommsRuntime) (func(), error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.calls++
	if f.calls <= f.failN {
		return nil, errors.New("simulated: miniaudio: Broken pipe")
	}

	rt.SetBroadcast(&mockStream{})

	select {
	case <-f.succeeded:
	default:
		close(f.succeeded)
	}

	return func() { f.mu.Lock(); f.cleanups++; f.mu.Unlock() }, nil
}

func (f *countingStartHA) callCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.calls
}

// TestRun_AudioRecovery_RetriesUntilSuccess verifies that when comms starts
// without hardware audio (e.g. OpenVLM unplugged or dmix EPIPE at boot),
// the Run loop re-attempts init on the recovery ticker and installs the
// stream on success, without a daemon restart.
func TestRun_AudioRecovery_RetriesUntilSuccess(t *testing.T) {
	// Gate off real ALSA card detection: ControlSource is defaultCtrlSrc,
	// which would otherwise make tryAudioRecovery walk /sys and
	// /proc/asound and potentially os.Setenv("ALSA_CARD", ...) for real on
	// a dev machine with a CM108-class device attached.
	t.Setenv("ALSA_CARD", "0")

	fake := &countingStartHA{failN: 2, succeeded: make(chan struct{})}

	cfg := &CommsConfig{
		Log:                   zerolog.Nop(),
		ControlSource:         defaultCtrlSrc,
		audioRecoveryInterval: 5 * time.Millisecond,
		startHardwareAudioFn:  fake.fn,
	}

	rt := &CommsRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		cfg.Run(ctx, rt, &mockEventSource{ch: make(chan control.PTTEvent)})
		close(done)
	}()

	select {
	case <-fake.succeeded:
	case <-time.After(2 * time.Second):
		t.Fatal("recovery never succeeded")
	}

	cancel()
	<-done

	assert.Equal(t, 3, fake.callCount(), "two failures then one success")
	assert.NotNil(t, rt.Broadcast(), "recovered stream must be installed")
	assert.NotNil(t, rt.audioCleanup, "recovery must stash the cleanup for Start's defer")
}

// TestRun_AudioRecovery_StopsAfterSuccess verifies the ticker is disarmed
// once audio is up: no further init attempts occur.
func TestRun_AudioRecovery_StopsAfterSuccess(t *testing.T) {
	// Gate off real ALSA card detection — see RetriesUntilSuccess above.
	t.Setenv("ALSA_CARD", "0")

	fake := &countingStartHA{failN: 0, succeeded: make(chan struct{})}

	cfg := &CommsConfig{
		Log:                   zerolog.Nop(),
		ControlSource:         defaultCtrlSrc,
		audioRecoveryInterval: time.Millisecond,
		startHardwareAudioFn:  fake.fn,
	}

	rt := &CommsRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		cfg.Run(ctx, rt, &mockEventSource{ch: make(chan control.PTTEvent)})
		close(done)
	}()

	<-fake.succeeded

	// Give the ticker room to misfire if the disarm were broken, without
	// asserting on wall-clock behavior: poll until call count is stable
	// across two observations 20 ticker-periods apart.
	deadline := time.After(2 * time.Second)

	for {
		before := fake.callCount()

		select {
		case <-deadline:
			t.Fatal("call count never stabilized")
		case <-time.After(20 * time.Millisecond):
		}

		if fake.callCount() == before {
			break
		}
	}

	cancel()
	<-done

	assert.Equal(t, 1, fake.callCount(), "no attempts after success")
}

// TestRun_AudioRecovery_DisabledWhenHealthy verifies no recovery attempts
// happen when startup already produced a stream.
func TestRun_AudioRecovery_DisabledWhenHealthy(t *testing.T) {
	cfg := &CommsConfig{
		Log:                   zerolog.Nop(),
		ControlSource:         defaultCtrlSrc,
		audioRecoveryInterval: time.Millisecond,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			t.Error("recovery must not run when audio is already up")

			return nil, nil
		},
	}

	rt := &CommsRuntime{}
	rt.SetBroadcast(&mockStream{})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		cfg.Run(ctx, rt, &mockEventSource{ch: make(chan control.PTTEvent)})
		close(done)
	}()

	time.AfterFunc(50*time.Millisecond, cancel)
	<-done
}

// TestRun_AudioRecovery_DisabledInWebMode verifies web mode never attempts
// hardware recovery — the browser owns audio I/O.
func TestRun_AudioRecovery_DisabledInWebMode(t *testing.T) {
	cfg := &CommsConfig{
		Log:                   zerolog.Nop(),
		ControlSource:         controlSourceWeb,
		audioRecoveryInterval: time.Millisecond,
		startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
			t.Error("recovery must not run in web mode")

			return nil, nil
		},
	}

	rt := &CommsRuntime{}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		cfg.Run(ctx, rt, &mockEventSource{ch: make(chan control.PTTEvent)})
		close(done)
	}()

	time.AfterFunc(50*time.Millisecond, cancel)
	<-done
}

// TestTryAudioRecovery_DetectionGate verifies tryAudioRecovery invokes the
// ALSA card detection seam only when ALSA_CARD is unset, and never when a
// card is already pinned (detected at startup or set manually) — the gate
// that keeps recovery from re-walking /sys and /proc/asound once a card is
// known.
func TestTryAudioRecovery_DetectionGate(t *testing.T) {
	tests := []struct {
		name       string
		alsaCard   string
		wantDetect bool
	}{
		{name: "unset triggers detection", alsaCard: "", wantDetect: true},
		{name: "set skips detection", alsaCard: "2", wantDetect: false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("ALSA_CARD", tc.alsaCard)

			detectCalls := 0
			cfg := &CommsConfig{
				Log:           zerolog.Nop(),
				ControlSource: defaultCtrlSrc,
				detectALSACardFn: func() {
					detectCalls++
				},
				startHardwareAudioFn: func(_ *CommsRuntime) (func(), error) {
					return nil, errors.New("simulated: no card available")
				},
			}

			rt := &CommsRuntime{}

			cfg.tryAudioRecovery(rt, 1)

			wantCalls := 0
			if tc.wantDetect {
				wantCalls = 1
			}

			assert.Equal(t, wantCalls, detectCalls, "detection seam call count")
		})
	}
}

// ─── queueLocalAudioFrame ────────────────────────────────────────────────────

func TestQueueLocalAudioFrame_PrefersActivePort(t *testing.T) {
	svc := newSelectTestService(t, 3)
	rt := svc.Rt

	for _, pc := range rt.Ports {
		pc.PlaybackBuffer = make(chan []int16, 4)
		pc.setPlaybackStream(&fakeAudioStream{}) // existing mocks_test fake
	}

	// Ports 0 and 2 running; active channel = 3 (port index 2).
	rt.Ports[0].markPlaybackRunning()
	rt.Ports[2].markPlaybackRunning()
	rt.ActiveChannel.Store(3)

	frame := make([]int16, audiopool.FrameSize)
	require.True(t, svc.Cfg.queueLocalAudioFrame(rt, frame))

	assert.Empty(t, rt.Ports[0].PlaybackBuffer, "active port preferred over first running port")
	assert.Len(t, rt.Ports[2].PlaybackBuffer, 1)
}

func TestQueueLocalAudioFrame_FallsBackToFirstRunning(t *testing.T) {
	svc := newSelectTestService(t, 2)
	rt := svc.Rt

	for _, pc := range rt.Ports {
		pc.PlaybackBuffer = make(chan []int16, 4)
		pc.setPlaybackStream(&fakeAudioStream{})
	}

	rt.Ports[1].markPlaybackRunning()
	// No active channel recorded.

	require.True(t, svc.Cfg.queueLocalAudioFrame(rt, make([]int16, audiopool.FrameSize)))
	assert.Len(t, rt.Ports[1].PlaybackBuffer, 1)
}

func TestQueueLocalAudioFrame_DropsWhenFull(t *testing.T) {
	svc := newSelectTestService(t, 1)
	rt := svc.Rt
	pc := rt.Ports[0]
	pc.PlaybackBuffer = make(chan []int16, 1)
	pc.setPlaybackStream(&fakeAudioStream{})
	pc.markPlaybackRunning()
	rt.ActiveChannel.Store(1)

	require.True(t, svc.Cfg.queueLocalAudioFrame(rt, make([]int16, audiopool.FrameSize)))
	assert.False(t, svc.Cfg.queueLocalAudioFrame(rt, make([]int16, audiopool.FrameSize)),
		"full buffer drops, never blocks")
}

func TestQueueLocalAudioFrame_NoBuffers(t *testing.T) {
	svc := newSelectTestService(t, 1)

	assert.False(t, svc.Cfg.queueLocalAudioFrame(svc.Rt, make([]int16, audiopool.FrameSize)))
}
