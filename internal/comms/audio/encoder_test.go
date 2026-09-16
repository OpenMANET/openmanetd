package audio

import (
	"errors"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
)

// newTestBroadcastEncoder builds a *BroadcastEncoder wired to an in-memory
// fake stream, encoder, and recording sink. It does NOT spawn the encode
// goroutine — tests opt in by calling go be.encodeLoop() when they need to
// exercise the consumer side.
func newTestBroadcastEncoder(t *testing.T, enc *mockEncoder) (*BroadcastEncoder, *recordingSink, *fakeCaptureStream) {
	t.Helper()

	sink := &recordingSink{}

	var tap atomic.Pointer[chan []float32]

	deps := Deps{
		Log:     zerolog.Nop(),
		Encoder: enc,
		Send:    sink.send,
		Tap:     &tap,
	}

	stream := &fakeCaptureStream{}

	be := &BroadcastEncoder{
		s:     stream,
		deps:  deps,
		encCh: make(chan *[]int16, broadcastEncoderChanDepth),
		done:  make(chan struct{}),
	}

	// Tests exercise the post-gate path (frame → encoder); under the
	// unified design this requires SetTxEnabled(true) before any frame
	// reaches the encode channel. Individual tests that want to
	// exercise the gate-closed behavior can call SetTxEnabled(false).
	be.txEnabled.Store(true)

	return be, sink, stream
}

// silentFrame returns an audiopool.FrameSize-long int16 slice filled with zeros.
func silentFrame() []int16 {
	return make([]int16, audiopool.FrameSize)
}

func TestBroadcastEncoder_HappyPath(t *testing.T) {
	enc := &mockEncoder{payloadN: 16}
	be, sink, _ := newTestBroadcastEncoder(t, enc)

	// Pump exactly broadcastEncoderChanDepth frames so they all fit in the
	// channel before we close it. This makes the test deterministic without
	// depending on the consumer goroutine racing the producer — once we
	// close encCh, encodeLoop will drain everything and then exit, and the
	// blocking <-be.done establishes a happens-before edge with all the
	// counter writes inside encodeOne.
	go be.encodeLoop()

	in := silentFrame()

	const frames = broadcastEncoderChanDepth

	for range frames {
		be.captureCallback(in)
	}

	close(be.encCh)
	<-be.done

	if got := be.framesCaptured.Load(); got != frames {
		t.Errorf("framesCaptured = %d, want %d", got, frames)
	}

	if got := be.framesEncoded.Load(); got != frames {
		t.Errorf("framesEncoded = %d, want %d", got, frames)
	}

	if got := be.framesDropped.Load(); got != 0 {
		t.Errorf("framesDropped = %d, want 0", got)
	}

	if got := be.encodeErrors.Load(); got != 0 {
		t.Errorf("encodeErrors = %d, want 0", got)
	}

	if got := enc.calls.Load(); got != frames {
		t.Errorf("encoder calls = %d, want %d", got, frames)
	}

	if got := sink.count(); got != frames {
		t.Errorf("sink payloads = %d, want %d", got, frames)
	}
}

func TestBroadcastEncoder_ChannelFullDropsAndCounts(t *testing.T) {
	// Do not spawn encodeLoop — leaving the consumer absent makes the
	// channel-full path fully deterministic. The producer fills encCh to
	// its capacity (broadcastEncoderChanDepth) and every additional frame
	// must be counted as dropped.
	enc := &mockEncoder{}
	be, _, _ := newTestBroadcastEncoder(t, enc)

	const extra = 5

	in := silentFrame()
	for range broadcastEncoderChanDepth + extra {
		be.captureCallback(in)
	}

	if got := be.framesCaptured.Load(); got != int64(broadcastEncoderChanDepth+extra) {
		t.Errorf("framesCaptured = %d, want %d", got, broadcastEncoderChanDepth+extra)
	}

	if got := be.framesDropped.Load(); got != int64(extra) {
		t.Errorf("framesDropped = %d, want %d", got, extra)
	}

	if got := len(be.encCh); got != broadcastEncoderChanDepth {
		t.Errorf("encCh len = %d, want %d", got, broadcastEncoderChanDepth)
	}

	if got := enc.calls.Load(); got != 0 {
		t.Errorf("encoder calls = %d, want 0 (no consumer)", got)
	}

	// Drain the channel so the pooled int16 slices are returned to the
	// pool rather than leaking. Each fp must be released the same way the
	// encode loop would.
	for range broadcastEncoderChanDepth {
		fp := <-be.encCh
		audiopool.Int16Pool.Put(fp)
	}
}

func TestBroadcastEncoder_EncoderErrorIncrementsCounter(t *testing.T) {
	enc := &mockEncoder{encodeErr: errors.New("boom")}
	be, sink, _ := newTestBroadcastEncoder(t, enc)

	go be.encodeLoop()

	in := silentFrame()

	const frames = 5

	// Push one frame at a time and wait for the encode worker to consume it
	// before queueing the next, so the test is independent of
	// broadcastEncoderChanDepth: a tighter channel depth still produces the
	// same total error count, just with more producer↔consumer interleaving.
	for i := int64(1); i <= frames; i++ {
		be.captureCallback(in)

		deadline := time.Now().Add(time.Second)
		for time.Now().Before(deadline) {
			if enc.calls.Load() >= i {
				break
			}

			time.Sleep(time.Millisecond)
		}

		if got := enc.calls.Load(); got < i {
			t.Fatalf("encoder calls = %d after frame %d, want at least %d", got, i, i)
		}
	}

	close(be.encCh)
	<-be.done

	if got := be.framesCaptured.Load(); got != frames {
		t.Errorf("framesCaptured = %d, want %d", got, frames)
	}

	if got := be.framesEncoded.Load(); got != 0 {
		t.Errorf("framesEncoded = %d, want 0", got)
	}

	if got := be.encodeErrors.Load(); got != frames {
		t.Errorf("encodeErrors = %d, want %d", got, frames)
	}

	if got := enc.calls.Load(); got != frames {
		t.Errorf("encoder calls = %d, want %d", got, frames)
	}

	if got := sink.count(); got != 0 {
		t.Errorf("sink payloads = %d, want 0 (encode failed)", got)
	}
}

// TestBroadcastEncoder_StartCallsStream verifies Start forwards to the
// underlying capture device. Under the unified design Start is called
// once per comms run (not per TX cycle), so it no longer resets the
// per-cycle counters — SetTxEnabled(true) owns that concern.
func TestBroadcastEncoder_StartCallsStream(t *testing.T) {
	be, _, stream := newTestBroadcastEncoder(t, &mockEncoder{})

	if err := be.Start(); err != nil {
		t.Fatalf("Start returned error: %v", err)
	}

	if stream.startCalls != 1 {
		t.Errorf("stream.startCalls = %d, want 1", stream.startCalls)
	}
}

// TestBroadcastEncoder_SetTxEnabledResetsCounters verifies that opening
// the TX gate (the per-PTT transition) clears the per-cycle counters so
// the Stop-log for the cycle reflects just the current PTT cycle and
// not any stale state from a prior cycle.
func TestBroadcastEncoder_SetTxEnabledResetsCounters(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})

	// Start gate-closed so SetTxEnabled(true) is a real transition.
	be.txEnabled.Store(false)

	be.framesCaptured.Store(42)
	be.framesEncoded.Store(17)
	be.framesDropped.Store(3)
	be.encodeErrors.Store(9)
	be.encodeDurMaxNs.Store(123456)
	be.encodeDurSumNs.Store(987654)
	be.encodeDurCount.Store(11)
	be.overBudgetWarned.Store(true)
	be.lastCaptureNs.Store(42424242)
	be.captureGapMaxNs.Store(13579)
	be.captureLateCount.Store(7)

	be.SetTxEnabled(true)

	if got := be.framesCaptured.Load(); got != 0 {
		t.Errorf("framesCaptured = %d, want 0", got)
	}

	if got := be.framesEncoded.Load(); got != 0 {
		t.Errorf("framesEncoded = %d, want 0", got)
	}

	if got := be.framesDropped.Load(); got != 0 {
		t.Errorf("framesDropped = %d, want 0", got)
	}

	if got := be.encodeErrors.Load(); got != 0 {
		t.Errorf("encodeErrors = %d, want 0", got)
	}

	if got := be.encodeDurMaxNs.Load(); got != 0 {
		t.Errorf("encodeDurMaxNs = %d, want 0", got)
	}

	if got := be.encodeDurSumNs.Load(); got != 0 {
		t.Errorf("encodeDurSumNs = %d, want 0", got)
	}

	if got := be.encodeDurCount.Load(); got != 0 {
		t.Errorf("encodeDurCount = %d, want 0", got)
	}

	if got := be.overBudgetWarned.Load(); got {
		t.Errorf("overBudgetWarned = true, want false")
	}

	if got := be.lastCaptureNs.Load(); got != 0 {
		t.Errorf("lastCaptureNs = %d, want 0", got)
	}

	if got := be.captureGapMaxNs.Load(); got != 0 {
		t.Errorf("captureGapMaxNs = %d, want 0", got)
	}

	if got := be.captureLateCount.Load(); got != 0 {
		t.Errorf("captureLateCount = %d, want 0", got)
	}
}

func TestBroadcastEncoder_StartPropagatesError(t *testing.T) {
	startErr := errors.New("simulated start failure")
	be, _, stream := newTestBroadcastEncoder(t, &mockEncoder{})
	stream.startErr = startErr

	err := be.Start()
	if err == nil {
		t.Fatal("Start returned nil, want error")
	}

	if !errors.Is(err, startErr) {
		t.Errorf("Start error = %v, want it to wrap %v", err, startErr)
	}
}

func TestBroadcastEncoder_StopLogsAndPropagatesError(t *testing.T) {
	stopErr := errors.New("simulated stop failure")
	be, _, stream := newTestBroadcastEncoder(t, &mockEncoder{})
	stream.stopErr = stopErr

	err := be.Stop()
	if err == nil {
		t.Fatal("Stop returned nil, want error")
	}

	if !errors.Is(err, stopErr) {
		t.Errorf("Stop error = %v, want it to wrap %v", err, stopErr)
	}

	if stream.stopCalls != 1 {
		t.Errorf("stream.stopCalls = %d, want 1", stream.stopCalls)
	}
}

// ─── gain clipping ────────────────────────────────────────────────────────────

// gainCapturingEncoder records the first PCM frame it sees so tests can
// inspect post-gain sample values directly.
type gainCapturingEncoder struct {
	captured []int16
	calls    int
}

func (g *gainCapturingEncoder) Encode(pcm []int16, data []byte) (int, error) {
	return g.EncodeS16(pcm, data)
}

func (g *gainCapturingEncoder) EncodeS16(pcm []int16, data []byte) (int, error) {
	g.calls++

	if g.captured == nil {
		g.captured = make([]int16, len(pcm))
		copy(g.captured, pcm)
	}

	if len(data) > 0 {
		data[0] = 0xAA
	}

	return 1, nil
}

func (g *gainCapturingEncoder) SetPacketLossPerc(_ int) error { return nil }

func (g *gainCapturingEncoder) Close() error { return nil }

// newGainTestEncoder builds a BroadcastEncoder around the gain-capturing
// encoder with the supplied micGain. The encode loop is started so the
// captured frame is consumed via the channel.
func newGainTestEncoder(t *testing.T, gain float32) (*BroadcastEncoder, *gainCapturingEncoder) {
	t.Helper()

	enc := &gainCapturingEncoder{}

	var tap atomic.Pointer[chan []float32]

	deps := Deps{
		Log:     zerolog.Nop(),
		Encoder: enc,
		Send:    func(_ []byte) {},
		Tap:     &tap,
		MicGain: gain,
	}

	be := &BroadcastEncoder{
		s:     &fakeCaptureStream{},
		deps:  deps,
		encCh: make(chan *[]int16, broadcastEncoderChanDepth),
		done:  make(chan struct{}),
	}

	be.txEnabled.Store(true)

	return be, enc
}

func TestBroadcastEncoder_SoftKneeCompressesPositiveOverflow(t *testing.T) {
	// gain * 10000 = 40000, past the 24576 knee. The rational knee maps
	// it to 24576 + 8191*15424/(15424+8191) = 29925 — compressed below
	// the rail instead of flat-topped at 32767.
	be, enc := newGainTestEncoder(t, 4.0)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = 10000
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != 29925 {
			t.Fatalf("captured[%d] = %d, want 29925 (soft knee)", i, v)
		}
	}
}

func TestBroadcastEncoder_SoftKneeCompressesNegativeOverflow(t *testing.T) {
	// gain * -10000 = -40000: same curve with the negative rail's radius
	// (8192, one step wider), landing at -29926, not -32768.
	be, enc := newGainTestEncoder(t, 4.0)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = -10000
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != -29926 {
			t.Fatalf("captured[%d] = %d, want -29926 (soft knee)", i, v)
		}
	}
}

func TestBroadcastEncoder_SoftKneePassesBelowKnee(t *testing.T) {
	// gain * 12000 = 24000, under the 24576 knee: bit-identical to plain
	// Q8 gain, no compression.
	be, enc := newGainTestEncoder(t, 2.0)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = 12000
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != 24000 {
			t.Fatalf("captured[%d] = %d, want 24000 (below knee, uncompressed)", i, v)
		}
	}
}

func TestBroadcastEncoder_SoftKneeNeverExceedsFullScale(t *testing.T) {
	// Full-scale input at 8x gain: 32767*8 = 262136 must compress to at
	// most 32767, and stay above the knee (monotonic).
	be, enc := newGainTestEncoder(t, 8.0)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = 32767
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v <= 24576 {
			t.Fatalf("captured[%d] = %d, want > 24576 (above knee)", i, v)
		}
	}
}

func TestBroadcastEncoder_GainScalesWithoutClipping(t *testing.T) {
	be, enc := newGainTestEncoder(t, 2.0) // 2.0 * 1000 = 2000, well within range
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = 1000
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != 2000 {
			t.Fatalf("captured[%d] = %d, want 2000 (2x gain)", i, v)
		}
	}
}

func TestBroadcastEncoder_UnityGainSkipsLoop(t *testing.T) {
	// gain == 1.0 takes the no-op fast path; the captured PCM equals the
	// input frame byte for byte.
	be, enc := newGainTestEncoder(t, 1.0)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = int16(i % 1000)
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != int16(i%1000) {
			t.Fatalf("captured[%d] = %d, want %d (unchanged)", i, v, i%1000)
		}
	}
}

func TestBroadcastEncoder_GainAppliesQ8Fraction(t *testing.T) {
	// 1.5 is exactly representable in Q8 (384/256); 1000 * 1.5 = 1500.
	be, enc := newGainTestEncoder(t, 1.5)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = 1000
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != 1500 {
			t.Fatalf("captured[%d] = %d, want 1500 (1.5x gain)", i, v)
		}
	}
}

func TestBroadcastEncoder_GainQuantizesToQ8(t *testing.T) {
	// The gain is fixed point with 1/256 resolution: 1.001 rounds to
	// 256/256 = unity, so samples pass through unchanged. The float
	// implementation would have produced 30030 here.
	be, enc := newGainTestEncoder(t, 1.001)
	go be.encodeLoop()

	frame := make([]int16, audiopool.FrameSize)
	for i := range frame {
		frame[i] = 30000
	}

	be.captureCallback(frame)

	close(be.encCh)
	<-be.done

	require.NotNil(t, enc.captured)

	for i, v := range enc.captured {
		if v != 30000 {
			t.Fatalf("captured[%d] = %d, want 30000 (1.001 quantizes to unity in Q8)", i, v)
		}
	}
}

// ─── VOX tap branches ─────────────────────────────────────────────────────────

// TestBroadcastEncoder_TapDeliversFrameWhenLoaded verifies that when a VOX
// tap channel pointer is published via Tap.Store, captureCallback hands off a
// float32-converted frame to the channel.
func TestBroadcastEncoder_TapDeliversFrameWhenLoaded(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})
	go be.encodeLoop()

	tapCh := make(chan []float32, 1)
	be.deps.Tap.Store(&tapCh)

	in := make([]int16, audiopool.FrameSize)
	for i := range in {
		in[i] = 16384 // 0.5 in float32
	}

	be.captureCallback(in)

	select {
	case f := <-tapCh:
		require.Equal(t, audiopool.FrameSize, len(f))
		// 16384 / 32768 = 0.5
		assert.InDelta(t, 0.5, f[0], 1e-3)
	case <-time.After(time.Second):
		t.Fatal("VOX tap did not receive a frame within the deadline")
	}

	close(be.encCh)
	<-be.done
}

// TestBroadcastEncoder_TapFullChannelDoesNotBlock verifies the channel-full
// branch in the VOX tap path: when the consumer is too slow, the captured
// frame is returned to the float32 pool and captureCallback continues
// without blocking. The test asserts the encoder still ships its frame
// even though the tap was dropped.
func TestBroadcastEncoder_TapFullChannelDoesNotBlock(t *testing.T) {
	be, sink, _ := newTestBroadcastEncoder(t, &mockEncoder{payloadN: 4})
	go be.encodeLoop()

	// Buffered channel of size 1 prefilled to capacity → next send
	// must drop into the default branch.
	tapCh := make(chan []float32, 1)
	tapCh <- make([]float32, audiopool.FrameSize)

	be.deps.Tap.Store(&tapCh)

	in := make([]int16, audiopool.FrameSize)
	be.captureCallback(in)

	close(be.encCh)
	<-be.done

	// Encoder still received the captured frame.
	if got := sink.count(); got != 1 {
		t.Fatalf("sink payloads = %d, want 1 (capture proceeds even when tap is full)", got)
	}

	// Tap channel still contains the original frame, untouched.
	require.Equal(t, 1, len(tapCh), "tap channel should still hold its prefilled frame")
}

// TestBroadcastEncoder_NilTapPointerSkipsConversion exercises the
// `Tap.Load() == nil` short-circuit so the float32 pool is never touched
// when no consumer is registered.
func TestBroadcastEncoder_NilTapPointerSkipsConversion(t *testing.T) {
	be, sink, _ := newTestBroadcastEncoder(t, &mockEncoder{payloadN: 4})
	go be.encodeLoop()

	// Tap field is non-nil (so the outer guard passes) but the pointer it
	// holds is nil (so the inner guard short-circuits without touching the
	// float32 pool).
	be.deps.Tap.Store(nil)

	in := make([]int16, audiopool.FrameSize)
	be.captureCallback(in)

	close(be.encCh)
	<-be.done

	if got := sink.count(); got != 1 {
		t.Errorf("sink payloads = %d, want 1", got)
	}
}

func TestBroadcastEncoder_CloseTerminatesGoroutine(t *testing.T) {
	be, _, stream := newTestBroadcastEncoder(t, &mockEncoder{})

	go be.encodeLoop()

	if err := be.Close(); err != nil {
		t.Fatalf("Close returned error: %v", err)
	}

	if stream.stopCalls != 1 {
		t.Errorf("stream.stopCalls = %d, want 1", stream.stopCalls)
	}

	if stream.closeCalls != 1 {
		t.Errorf("stream.closeCalls = %d, want 1", stream.closeCalls)
	}

	// done must be closed by encodeLoop's defer; if Close had not waited
	// the channel would still be open.
	select {
	case <-be.done:
	case <-time.After(time.Second):
		t.Fatal("Close did not wait for encode goroutine to exit")
	}
}

// ─── encCh depth + encode-duration tracking ──────────────────────────────────

// TestBroadcastEncoderChanDepth_AbsorbsHardwareSpike pins the channel depth
// at a value large enough to absorb a transient encoder stall on slow target
// hardware. The previous depth of 3 (60 ms) was too tight; sustained stutter
// on MIPS was traced back to encoder spikes that exceeded that window. The
// new floor (10 = 200 ms) matches the receive-side prebuffer so the producer
// and consumer agree on the spike envelope.
func TestBroadcastEncoderChanDepth_AbsorbsHardwareSpike(t *testing.T) {
	const minDepth = 10

	if broadcastEncoderChanDepth < minDepth {
		t.Fatalf("broadcastEncoderChanDepth = %d, want >= %d "+
			"(do not shrink without re-validating against MIPS targets)",
			broadcastEncoderChanDepth, minDepth)
	}
}

// TestBroadcastEncoder_EncodeDurationCountersAccumulate verifies that
// recordEncodeDuration updates the running max, sum, and count. The
// CAS-loop max must reflect the largest observed duration regardless of
// arrival order.
func TestBroadcastEncoder_EncodeDurationCountersAccumulate(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})

	durations := []time.Duration{
		2 * time.Millisecond,
		7 * time.Millisecond,
		1 * time.Millisecond,
		5 * time.Millisecond,
	}

	var totalNs int64

	for _, d := range durations {
		be.recordEncodeDuration(d)
		totalNs += d.Nanoseconds()
	}

	if got := be.encodeDurCount.Load(); got != int64(len(durations)) {
		t.Errorf("encodeDurCount = %d, want %d", got, len(durations))
	}

	if got := be.encodeDurSumNs.Load(); got != totalNs {
		t.Errorf("encodeDurSumNs = %d, want %d", got, totalNs)
	}

	wantMaxNs := (7 * time.Millisecond).Nanoseconds()
	if got := be.encodeDurMaxNs.Load(); got != wantMaxNs {
		t.Errorf("encodeDurMaxNs = %d, want %d", got, wantMaxNs)
	}

	if got := be.overBudgetWarned.Load(); got {
		t.Error("overBudgetWarned = true, want false (all durations under budget)")
	}
}

// TestBroadcastEncoder_OverBudgetWarnFiresOncePerCycle verifies that
// crossing the per-frame budget sets the latch exactly once per Start
// cycle. Repeated over-budget durations within the same cycle must NOT
// re-arm the warning, and Start must reset the latch so the next cycle
// can warn again.
func TestBroadcastEncoder_OverBudgetWarnFiresOncePerCycle(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})

	// Below-budget durations leave the latch alone.
	be.recordEncodeDuration(frameDuration - time.Millisecond)
	be.recordEncodeDuration(frameDuration / 2)

	if be.overBudgetWarned.Load() {
		t.Fatal("warn latch set by sub-budget durations")
	}

	// First over-budget duration arms the latch.
	be.recordEncodeDuration(frameDuration + time.Millisecond)

	if !be.overBudgetWarned.Load() {
		t.Fatal("warn latch should be set after first over-budget duration")
	}

	// Subsequent over-budget durations within the cycle do NOT clear or
	// re-fire the latch — it stays armed exactly once.
	for range 5 {
		be.recordEncodeDuration(frameDuration * 2)
	}

	if !be.overBudgetWarned.Load() {
		t.Fatal("warn latch lost across repeated over-budget durations")
	}

	// Counters still accumulate normally.
	const totalCalls = 2 + 1 + 5
	if got := be.encodeDurCount.Load(); got != totalCalls {
		t.Errorf("encodeDurCount = %d, want %d", got, totalCalls)
	}

	// SetTxEnabled(true) resets the latch so the next TX cycle can
	// warn again — under the unified design the TX gate is the
	// per-cycle boundary, not Start().
	be.txEnabled.Store(false)
	be.SetTxEnabled(true)

	if be.overBudgetWarned.Load() {
		t.Error("SetTxEnabled(true) did not reset overBudgetWarned")
	}

	be.recordEncodeDuration(frameDuration + time.Millisecond)

	if !be.overBudgetWarned.Load() {
		t.Error("warn latch should re-arm after SetTxEnabled(true)")
	}
}

// TestBroadcastEncoder_CaptureArrivalFirstCallSeedsOnly verifies that
// the very first captureCallback in a cycle only seeds lastCaptureNs
// and does not produce a synthetic large gap against the zero baseline.
func TestBroadcastEncoder_CaptureArrivalFirstCallSeedsOnly(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})

	t0 := time.Unix(0, int64(time.Hour))
	be.recordCaptureArrival(t0)

	if got := be.lastCaptureNs.Load(); got != t0.UnixNano() {
		t.Errorf("lastCaptureNs = %d, want %d", got, t0.UnixNano())
	}

	if got := be.captureGapMaxNs.Load(); got != 0 {
		t.Errorf("captureGapMaxNs = %d, want 0 (first call only seeds)", got)
	}

	if got := be.captureLateCount.Load(); got != 0 {
		t.Errorf("captureLateCount = %d, want 0", got)
	}
}

// TestBroadcastEncoder_CaptureArrivalTracksMaxAndLate verifies that the
// inter-arrival tracker updates the running max on each delta and counts
// callbacks whose delta meets or exceeds 2 * frameDuration.
func TestBroadcastEncoder_CaptureArrivalTracksMaxAndLate(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})

	base := time.Unix(0, int64(time.Hour))
	be.recordCaptureArrival(base)

	deltas := []time.Duration{
		frameDuration,                      // on-time (20 ms)
		frameDuration + 3*time.Millisecond, // slightly late (< threshold)
		2 * frameDuration,                  // exactly threshold — counts as late
		frameDuration / 2,                  // early / burst (not late)
		3 * frameDuration,                  // very late — new max
	}

	var (
		cursor  = base
		wantMax time.Duration
	)

	const lateThreshold = 2 * frameDuration

	wantLate := int64(0)

	for _, d := range deltas {
		cursor = cursor.Add(d)
		be.recordCaptureArrival(cursor)

		if d > wantMax {
			wantMax = d
		}

		if d >= lateThreshold {
			wantLate++
		}
	}

	if got := time.Duration(be.captureGapMaxNs.Load()); got != wantMax {
		t.Errorf("captureGapMaxNs = %v, want %v", got, wantMax)
	}

	if got := be.captureLateCount.Load(); got != wantLate {
		t.Errorf("captureLateCount = %d, want %d", got, wantLate)
	}
}

// TestBroadcastEncoder_CaptureArrivalRejectsNonMonotonic verifies that a
// non-monotonic clock reading (delta <= 0) does not corrupt the max.
// Should never happen with time.Now on Linux but we guard against it
// to keep the CAS loop robust.
func TestBroadcastEncoder_CaptureArrivalRejectsNonMonotonic(t *testing.T) {
	be, _, _ := newTestBroadcastEncoder(t, &mockEncoder{})

	t0 := time.Unix(0, int64(time.Hour))
	be.recordCaptureArrival(t0)

	// Same timestamp — delta is 0, must be ignored.
	be.recordCaptureArrival(t0)

	// Earlier timestamp — delta is negative, must be ignored.
	be.recordCaptureArrival(t0.Add(-time.Millisecond))

	if got := be.captureGapMaxNs.Load(); got != 0 {
		t.Errorf("captureGapMaxNs = %d, want 0 (non-monotonic deltas must not update max)", got)
	}
}

// TestBroadcastEncoder_EncodeOneRecordsDuration verifies that encodeOne
// observes a slow encoder via the duration counters and that the slow
// path triggers the over-budget warn. Uses a mockEncoder configured to
// sleep slightly longer than frameDuration so the assertion is robust
// against scheduling jitter.
func TestBroadcastEncoder_EncodeOneRecordsDuration(t *testing.T) {
	enc := &mockEncoder{
		payloadN: 4,
		sleepDur: frameDuration + 5*time.Millisecond,
	}
	be, sink, _ := newTestBroadcastEncoder(t, enc)

	go be.encodeLoop()

	be.captureCallback(silentFrame())

	// Wait for the encode goroutine to consume the frame.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sink.count() > 0 {
			break
		}

		time.Sleep(time.Millisecond)
	}

	close(be.encCh)
	<-be.done

	if got := be.encodeDurCount.Load(); got != 1 {
		t.Errorf("encodeDurCount = %d, want 1", got)
	}

	if got := time.Duration(be.encodeDurMaxNs.Load()); got < frameDuration {
		t.Errorf("encodeDurMaxNs = %v, want >= %v", got, frameDuration)
	}

	if !be.overBudgetWarned.Load() {
		t.Error("overBudgetWarned should be set after over-budget encode")
	}
}

// TestBroadcastEncoder_TxGateBlocksEncoderButNotTap exercises the unified
// capture path: when SetTxEnabled is false the captureCallback still
// runs the VOX tap (so the ROIP control source can drive PTT decisions
// off the always-on stream) but never enqueues frames into encCh, so
// the Opus encoder remains idle.
func TestBroadcastEncoder_TxGateBlocksEncoderButNotTap(t *testing.T) {
	enc := &mockEncoder{payloadN: 4}
	be, sink, _ := newTestBroadcastEncoder(t, enc)

	// Wire a tap channel and start gate-closed.
	tapCh := make(chan []float32, 4)
	be.deps.Tap.Store(&tapCh)
	be.txEnabled.Store(false)

	go be.encodeLoop()

	// Push three frames with the gate closed: tap should receive them
	// all, encoder should see none.
	for range 3 {
		be.captureCallback(silentFrame())
	}

	// Drain the tap to confirm frames flowed there.
	gotTap := 0

drainTap:
	for {
		select {
		case f := <-tapCh:
			audiopool.ReturnFloat32(f)

			gotTap++
		default:
			break drainTap
		}
	}

	if gotTap != 3 {
		t.Errorf("tap received %d frames, want 3 with gate closed", gotTap)
	}

	if got := be.framesCaptured.Load(); got != 3 {
		t.Errorf("framesCaptured = %d, want 3 (counter advances even when gated)", got)
	}

	// Open the gate and push three more frames; encoder should see them.
	be.SetTxEnabled(true)

	for range 3 {
		be.captureCallback(silentFrame())
	}

	close(be.encCh)
	<-be.done

	// SetTxEnabled(true) zeroed the counters so framesCaptured now
	// reflects only the post-gate cycle.
	if got := be.framesCaptured.Load(); got != 3 {
		t.Errorf("framesCaptured = %d, want 3 after re-enable", got)
	}

	if got := sink.count(); got != 3 {
		t.Errorf("sink received %d frames, want 3 with gate open", got)
	}
}

// TestMicGainQ8_HighSideClamp pins the overflow guard: q must never
// exceed maxGainQ8 (65536), the largest Q8 gain for which int32(v)*q
// cannot wrap for any int16 sample (worst case -32768 * 65536 ==
// math.MinInt32 exactly).
func TestMicGainQ8_HighSideClamp(t *testing.T) {
	tests := []struct {
		name string
		gain float32
		want int32
	}{
		{"at clamp boundary 256x", 256.0, 65536},
		{"just above boundary", 256.5, 65536},
		{"absurd gain", 1e6, 65536},
		{"float to int32 overflow gain", 1e30, 65536},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assert.Equal(t, tc.want, micGainQ8(tc.gain))
		})
	}
}

// TestMicGainQ8_ClampedGainNoOverflow proves the clamped worst case
// stays in int32 range end to end: the most negative sample at the
// maximum Q8 gain reaches the soft knee without wrapping.
func TestMicGainQ8_ClampedGainNoOverflow(t *testing.T) {
	q := micGainQ8(1e30)
	v := int16(-32768)
	scaled := (int32(v) * q) >> gainQ8Shift
	assert.Equal(t, int32(-8388608), scaled) // MinInt32 >> 8, no wrap
	assert.Equal(t, int16(-32759), softKnee(scaled))
}
