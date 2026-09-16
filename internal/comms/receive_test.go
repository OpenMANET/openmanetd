package comms

import (
	"context"
	"errors"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/rs/zerolog"

	"github.com/openmanet/openmanetd/internal/comms/audiopool"
	"github.com/openmanet/openmanetd/internal/comms/control"
	"github.com/openmanet/openmanetd/internal/comms/rtp"
	"github.com/openmanet/openmanetd/internal/comms/webaudio"
)

// ─── helpers ──────────────────────────────────────────────────────────────────

func newReceiveRuntime() (*CommsRuntime, *PortChannel) {
	pc := &PortChannel{
		cfg: McastPortConfig{Send: true, Receive: true},
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 4)
	pc.Decoder = &mockDecoder{returnN: int(rtp.FrameSamples)}
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	return rt, pc
}

// isAllZero reports whether every sample in the slice is exactly zero.
// Used by playoutOneFrame tests to distinguish silence from decoded audio.
func isAllZero(out []int16) bool {
	for _, v := range out {
		if v != 0 {
			return false
		}
	}

	return true
}

// ─── playoutOneFrame tests ────────────────────────────────────────────────────

// driveOneFrame is a small helper for tests that need to call playoutOneFrame
// against a fresh PCM output buffer of the standard frame size.
func driveOneFrame(cfg *CommsConfig, pc *PortChannel, rt *CommsRuntime, jb *rtp.JitterBuffer) []int16 {
	out := make([]int16, audiopool.FrameSize)
	cfg.playoutOneFrame(pc, rt, jb, out)

	return out
}

func TestPlayoutOneFrame_UsesPerPortDecoder(t *testing.T) {
	// Concurrent receive on multiple talk groups is a designed capability
	// (any port can be receive-enabled at any time), and each playback
	// callback runs on its own audio thread. Sharing one stateful Opus
	// decoder across them is a CGO-level data race, so every port must own
	// its own decoder instance.
	cfg := &CommsConfig{Log: zerolog.Nop()}
	rt := &CommsRuntime{}

	makePort := func(fill int16) (*PortChannel, *rtp.JitterBuffer) {
		pc := &PortChannel{cfg: McastPortConfig{Receive: true}}
		pc.ReceiveEnabled.Store(true)
		pc.Decoder = &mockDecoder{fillValue: fill, returnN: audiopool.FrameSize}

		jb := rtp.NewJitterBuffer(1, 16)
		jb.Push(0, []byte{1})

		return pc, jb
	}

	pcA, jbA := makePort(11)
	pcB, jbB := makePort(22)
	rt.Ports = []*PortChannel{pcA, pcB}

	outA := driveOneFrame(cfg, pcA, rt, jbA)
	outB := driveOneFrame(cfg, pcB, rt, jbB)

	if outA[0] != 11 {
		t.Errorf("port A must decode through its own decoder; got sample %d, want 11", outA[0])
	}

	if outB[0] != 22 {
		t.Errorf("port B must decode through its own decoder; got sample %d, want 22", outB[0])
	}
}

func TestPlayoutOneFrame_DecodesPayload(t *testing.T) {
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{fillValue: 1234, returnN: audiopool.FrameSize}

	jb := rtp.NewJitterBuffer(1, 16) // prebuffer=1: first push triggers start
	jb.Push(0, []byte{0xAA, 0xBB})

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if isAllZero(out) {
		t.Fatal("expected decoded samples, got silence")
	}

	if pc.ConsecutivePLC != 0 {
		t.Errorf("consecutivePLC should be 0 after a decoded payload; got %d", pc.ConsecutivePLC)
	}
}

func TestPlayoutOneFrame_PLCOnSkippedMissing(t *testing.T) {
	// maxDepth=4 → skip threshold = maxDepth/2 = 2.
	// Push seq=0 first to set up expected=0 and pass the prebuffer=1 threshold,
	// then pop it. Now expected=1. Push seq=2 and seq=3 (missing seq=1 with
	// len >= maxDepth/2) so popOrConceal returns conceal=true → PLC must
	// be invoked via the decoder with nil payload.
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{fillValue: 99, returnN: audiopool.FrameSize}

	jb := rtp.NewJitterBuffer(1, 4)

	jb.Push(0, []byte{0})
	jb.PopReady() // consume seq=0; started=true, expected=1

	jb.Push(2, []byte{2})
	jb.Push(3, []byte{3}) // len=2 >= maxDepth/2=2, expected=1 missing → skipped

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if isAllZero(out) {
		t.Fatal("expected a PLC frame when jitter buffer skips missing seq, got silence")
	}

	if pc.ConsecutivePLC != 1 {
		t.Errorf("consecutivePLC should be 1 after one PLC frame; got %d", pc.ConsecutivePLC)
	}
}

func TestPlayoutOneFrame_PLCOnConceal(t *testing.T) {
	// Push+pop one frame to set started=true and record a recent lastPush.
	// The jitter buffer is then empty, so shouldConceal fires on the next
	// playoutOneFrame call and the decoder is invoked with nil payload.
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{fillValue: 99, returnN: audiopool.FrameSize}

	jb := rtp.NewJitterBuffer(1, 16)

	jb.Push(0, []byte{0})
	jb.PopReady() // started=true, expected=1, lastPush set to now

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if isAllZero(out) {
		t.Fatal("expected a PLC concealment frame while stream is active but empty, got silence")
	}

	if pc.ConsecutivePLC != 1 {
		t.Errorf("consecutivePLC should increment to 1 after concealment; got %d", pc.ConsecutivePLC)
	}
}

func TestPlayoutOneFrame_SilenceAfterMaxPLC(t *testing.T) {
	// After maxConsecutivePLC frames, playoutOneFrame should emit clean
	// silence rather than calling the (now degraded) decoder PLC.
	rt, pc := newReceiveRuntime()
	dec := &mockDecoder{fillValue: 99, returnN: audiopool.FrameSize}
	pc.Decoder = dec

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0})
	jb.PopReady() // started=true, lastPush set

	cfg := &CommsConfig{Log: zerolog.Nop()}

	// Drive playout until we exceed the PLC budget. The mock decoder
	// returns non-zero samples for both real and PLC frames so we can tell
	// PLC frames apart from silence by inspecting the samples.
	for i := 0; i < maxConsecutivePLC; i++ {
		out := driveOneFrame(cfg, pc, rt, jb)
		if isAllZero(out) {
			t.Fatalf("frame %d: expected a PLC frame, got silence", i)
		}
	}

	// The next call should be silence (consecutivePLC > maxConsecutivePLC).
	out := driveOneFrame(cfg, pc, rt, jb)
	if !isAllZero(out) {
		t.Errorf("expected silence after %d PLC frames; got non-zero samples", maxConsecutivePLC)
	}
}

func TestPlayoutOneFrame_SilenceWhenBroadcasting(t *testing.T) {
	rt, pc := newReceiveRuntime()
	rt.Broadcasting.Store(true) // isBroadcasting will return true; pc.SendEnabled=true → suppress

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA, 0xBB})

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if !isAllZero(out) {
		t.Errorf("playoutOneFrame should emit silence while broadcasting; got non-zero samples")
	}
}

func TestPlayoutOneFrame_SilenceWhenReceiveDisabled(t *testing.T) {
	rt, pc := newReceiveRuntime()
	pc.ReceiveEnabled.Store(false)

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA})

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if !isAllZero(out) {
		t.Errorf("playoutOneFrame should emit silence when receive is disabled; got non-zero samples")
	}
}

func TestPlayoutOneFrame_NilJitter(t *testing.T) {
	rt, pc := newReceiveRuntime()
	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, nil)
	if !isAllZero(out) {
		t.Errorf("playoutOneFrame with nil jitter should emit silence; got non-zero samples")
	}

	if pc.PlaybackUnderruns.Load() != 0 {
		t.Errorf("nil jitter should not count as underrun; got %d", pc.PlaybackUnderruns.Load())
	}
}

func TestPlayoutOneFrame_NoUnderrunOnIdleStream(t *testing.T) {
	// Stream that has never started (no packets yet) should write silence
	// without incrementing the underrun counter — silence != underrun.
	rt, pc := newReceiveRuntime()
	jb := rtp.NewJitterBuffer(1, 16)

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if !isAllZero(out) {
		t.Errorf("expected silence on idle stream; got non-zero samples")
	}

	if pc.PlaybackUnderruns.Load() != 0 {
		t.Errorf("idle stream should not increment playbackUnderruns; got %d", pc.PlaybackUnderruns.Load())
	}
}

func TestPlayoutOneFrame_UnderrunOnDecoderError(t *testing.T) {
	// Decoder returning an error AND PLC also failing → silence + underrun++.
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{decodeErr: errors.New("bad decode")}

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA, 0xBB})

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if !isAllZero(out) {
		t.Errorf("expected silence on decoder error; got non-zero samples")
	}

	if pc.PlaybackUnderruns.Load() != 1 {
		t.Errorf("expected playbackUnderruns=1 after decoder error; got %d", pc.PlaybackUnderruns.Load())
	}
}

func TestPlayoutOneFrame_DecoderErrorPLCFallback(t *testing.T) {
	// Real decode fails but PLC succeeds → playoutOneFrame should emit the
	// PLC samples, reset consecutivePLC, and NOT count an underrun.
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{
		decodeErr: errors.New("bad decode"),
		plcOK:     true,
		returnN:   audiopool.FrameSize,
		fillValue: 1234,
	}

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA, 0xBB})

	cfg := &CommsConfig{Log: zerolog.Nop()}

	out := driveOneFrame(cfg, pc, rt, jb)
	if isAllZero(out) {
		t.Errorf("expected PLC samples on decoder error fallback; got silence")
	}

	if pc.PlaybackUnderruns.Load() != 0 {
		t.Errorf("PLC fallback success should not count as underrun; got %d", pc.PlaybackUnderruns.Load())
	}
}

// ─── receiveLoop branch tests ─────────────────────────────────────────────────

func TestReceiveLoop_DropsOwnPackets(t *testing.T) {
	// All packets arrive from the local IP → all should be dropped.
	localIP := netip.MustParseAddr("192.168.1.1")
	localAddr := netip.AddrPortFrom(localIP, 5004)

	var pkts []mockPacket

	for i := 0; i < rtp.PrebufferPackets+1; i++ {
		raw := makeRTPBytes(t, uint16(i))
		pkts = append(pkts, mockPacket{data: raw, src: localAddr})
	}

	reader := newMockReader(pkts...)
	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 16)
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}
	s := localIP.String()
	rt.LocalIP.Store(&s)

	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: false}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	// Wait for all queued packets to be consumed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	time.Sleep(60 * time.Millisecond) // allow one playout tick

	cancel()
	pc.Receiver.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit")
	}

	if len(pc.PlaybackBuffer) != 0 {
		t.Errorf("own packets should be dropped; got %d frames in buffer",
			len(pc.PlaybackBuffer))
	}
}

// TestReceiveLoop_DropsOwn4in6Packets pins the Unmap in the loopback filter:
// a source address in 4-in-6 form (::ffff:a.b.c.d) must still match the
// cached v4 local address. Production sockets bind udp4 and return plain v4
// addresses, but the filter must not silently break if the socket family
// ever changes to dual-stack.
func TestReceiveLoop_DropsOwn4in6Packets(t *testing.T) {
	localIP := "192.168.1.1"
	mappedSrc := netip.AddrPortFrom(netip.MustParseAddr("::ffff:192.168.1.1"), 5004)

	var pkts []mockPacket

	for i := 0; i < rtp.PrebufferPackets+1; i++ {
		raw := makeRTPBytes(t, uint16(i))
		pkts = append(pkts, mockPacket{data: raw, src: mappedSrc})
	}

	reader := newMockReader(pkts...)
	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 16)
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}
	rt.LocalIP.Store(&localIP)

	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: false}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	pc.Receiver.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit")
	}

	if got := pc.RxLoopback.Load(); got != int64(rtp.PrebufferPackets+1) {
		t.Errorf("4-in-6 own packets should all be dropped as loopback; RxLoopback=%d, want %d",
			got, rtp.PrebufferPackets+1)
	}
}

// TestReceiveLoop_SkipsParseWhenReceiveDisabled pins the muted-port fast
// path: a receive-disabled port must not pay the RTP unmarshal for packets
// it will discard anyway, so parse errors are not counted while muted.
// RxPkts still advances (the read happened) and re-enabling resumes
// parsing.
func TestReceiveLoop_SkipsParseWhenReceiveDisabled(t *testing.T) {
	// Garbage packets that would fail RTP parsing if parsed.
	garbage := []byte{0xFF, 0x00, 0x01}

	var pkts []mockPacket
	for range 3 {
		pkts = append(pkts, mockPacket{data: garbage, src: netip.MustParseAddrPort("1.2.3.4:5004")})
	}

	reader := newMockReader(pkts...)
	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(false) // muted
	pc.PlaybackBuffer = make(chan []int16, 8)
	rt := &CommsRuntime{Ports: []*PortChannel{pc}}

	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	pc.Receiver.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit")
	}

	if got := pc.RxPkts.Load(); got != 3 {
		t.Errorf("RxPkts: got %d, want 3 (reads still counted while muted)", got)
	}

	if got := pc.RxParseErrs.Load(); got != 0 {
		t.Errorf("RxParseErrs: got %d, want 0 (muted ports must not parse)", got)
	}
}

func TestReceiveLoop_DropsMalformedRTP(t *testing.T) {
	// First packet is garbage; receiveLoop should log and continue rather than crash.
	garbled := mockPacket{data: []byte{0xFF, 0x00, 0x01}, src: netip.MustParseAddrPort("1.2.3.4:5004")}
	reader := newMockReader(garbled)

	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 8)
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	// Wait for the garbled packet to be consumed.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	// The loop survived; cancel and ensure clean shutdown.
	cancel()
	pc.Receiver.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit after malformed packet")
	}
}

// ─── isReceivingRemote tests ──────────────────────────────────────────────────

func TestIsReceivingRemote_FalseWhenNeverReceived(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	// rt has no ports → isReceivingRemote always returns false.
	rt := &CommsRuntime{}

	if cfg.isReceivingRemote(rt) {
		t.Error("expected false when no packet has ever been received")
	}
}

func TestIsReceivingRemote_TrueWhenRecent(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	pc.MarkRemoteRx(rt)

	if !cfg.isReceivingRemote(rt) {
		t.Error("expected true when a packet was just received")
	}
}

func TestIsReceivingRemote_FalseWhenStale(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}
	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	// Store a timestamp older than rxActiveThreshold.
	pc.RxGate.MarkAt(time.Now().Add(-(rxActiveThreshold + time.Second)))
	rt := &CommsRuntime{Ports: []*PortChannel{pc}}

	if cfg.isReceivingRemote(rt) {
		t.Error("expected false when last received packet is older than rxActiveThreshold")
	}
}

// TestHalfDuplexDecayLoop_ClearsCacheWhenAllGatesQuiet exercises the
// background decay loop: it must clear rt.RemoteRxActive once every gate has
// fallen outside its threshold window. The gate is stamped with a stale
// timestamp so the decay tick observes Active() == false on the very next
// pass and writes false back to the cache.
func TestHalfDuplexDecayLoop_ClearsCacheWhenAllGatesQuiet(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	// Stale timestamp — gate is no longer Active().
	pc.RxGate.MarkAt(time.Now().Add(-(rxActiveThreshold + time.Second)))

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	// Prime the cache as if the gate had just been marked.
	rt.RemoteRxActive.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.halfDuplexDecayLoop(ctx, rt)
	}()

	// Two decay ticks (~200 ms) should be more than enough to observe the
	// stale gate and flip the cache to false.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if !rt.RemoteRxActive.Load() {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	if rt.RemoteRxActive.Load() {
		t.Error("expected halfDuplexDecayLoop to clear the cache when all gates are stale")
	}

	cancel()
	<-done
}

// TestHalfDuplexDecayLoop_KeepsCacheWhileGateActive verifies that the decay
// loop does NOT clear the cache while at least one send-enabled port has an
// active gate.
func TestHalfDuplexDecayLoop_KeepsCacheWhileGateActive(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop()}

	pc := &PortChannel{cfg: McastPortConfig{Send: true, Receive: true}}
	pc.SendEnabled.Store(true)
	pc.RxGate.Mark() // fresh, well within threshold

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	rt.RemoteRxActive.Store(true)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.halfDuplexDecayLoop(ctx, rt)
	}()

	// Wait long enough for at least two decay ticks to fire.
	time.Sleep(halfDuplexDecayInterval*2 + 50*time.Millisecond)

	if !rt.RemoteRxActive.Load() {
		t.Error("expected halfDuplexDecayLoop to leave the cache set while a gate is still active")
	}

	cancel()
	<-done
}

func TestReceiveLoop_StampsLastRemoteRx(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}

	raw := makeRTPBytes(t, 0)
	reader := newMockReader(mockPacket{data: raw, src: netip.MustParseAddrPort("1.2.3.4:5004")})
	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 32)
	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	pc.Receiver.Close()

	<-done

	if pc.RxGate.LastUnixNano() == 0 {
		t.Error("expected rxGate to be marked after receiving a remote packet")
	}
}

// ─── receiveLoop failure-path tests ───────────────────────────────────────────

func TestReceiveLoop_BacksOffOnPersistentReadErrors(t *testing.T) {
	// A permanently failing receiver (e.g. a socket closed by Start's
	// teardown while the loop's context is still live) must not busy-spin.
	// After a few consecutive errors the loop backs off between attempts.
	reader := &fakeErrReader{err: net.ErrClosed}
	pc := &PortChannel{
		cfg:      McastPortConfig{Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.ReceiveEnabled.Store(true)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	cfg := &CommsConfig{Log: zerolog.Nop()}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	// 100 ms of persistent errors must produce a bounded number of read
	// attempts. With the consecutive-error threshold and backoff the loop
	// makes on the order of a dozen attempts; a spinning loop makes
	// hundreds of thousands.
	time.Sleep(100 * time.Millisecond)
	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit after cancel")
	}

	if n := reader.reads.Load(); n > 100 {
		t.Fatalf("receiveLoop busy-spun on persistent read errors: %d reads in 100 ms", n)
	}
}

func TestRun_StopsReceiveLoopsWhenEventSourceCloses(t *testing.T) {
	// When the control source dies (e.g. PTT dongle unplugged), its events
	// channel closes and Run returns; Start's defers then close every
	// receiver while the manager's context is still live. The goroutines
	// Run spawned must terminate with it instead of erroring against
	// permanently closed sockets forever.
	reader := &fakeErrReader{err: net.ErrClosed}
	pc := &PortChannel{
		cfg:      McastPortConfig{Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader),
	}
	pc.ReceiveEnabled.Store(true)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}
	cfg := &CommsConfig{Log: zerolog.Nop()}

	src := &mockEventSource{ch: make(chan control.PTTEvent)}
	close(src.ch)

	cfg.Run(context.Background(), rt, src) // returns as soon as events closes

	// After Run returns, the spawned receiveLoop must wind down: poll until
	// the read count stops advancing.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		before := reader.reads.Load()

		time.Sleep(50 * time.Millisecond)

		if reader.reads.Load() == before {
			return // loop has stopped
		}
	}

	t.Fatal("receiveLoop still reading after Run returned; run-scoped cancellation missing")
}

// ─── receiveLoop socket-swap recovery tests ───────────────────────────────────

// netErrClosedReader wraps a mockReader and translates any read error to
// net.ErrClosed, matching the error that a real *net.UDPConn read returns
// after the connection is closed. This allows receiveLoop's socket-swap path
// (errors.Is(err, net.ErrClosed) → jitter.Reset()) to be exercised in tests.
type netErrClosedReader struct {
	*mockReader
}

func (r *netErrClosedReader) ReadFromUDPAddrPort(b []byte) (int, netip.AddrPort, error) {
	n, addr, err := r.mockReader.ReadFromUDPAddrPort(b)
	if err != nil {
		return 0, netip.AddrPort{}, net.ErrClosed
	}

	return n, addr, nil
}

// TestReceiveLoop_SocketSwapResetsJitter simulates an UpdateMulticastEndpoint
// mid-stream socket swap. The old reader is closed (returning net.ErrClosed),
// and a new reader with fresh packets is swapped in. The test verifies that the
// loop recovers and delivers frames from the new reader, proving the jitter
// buffer was reset and the loop continued correctly.
func TestReceiveLoop_SocketSwapResetsJitter(t *testing.T) {
	cfg := &CommsConfig{Log: zerolog.Nop(), Loopback: true}

	// reader1 holds a burst of packets that will fill the jitter buffer, then
	// it will block until explicitly closed (simulating the old socket).
	var pkts1 []mockPacket

	for i := 0; i < rtp.PrebufferPackets+2; i++ {
		raw := makeRTPBytes(t, uint16(i))
		pkts1 = append(pkts1, mockPacket{data: raw, src: netip.MustParseAddrPort("1.2.3.4:5004")})
	}

	reader1 := &netErrClosedReader{newMockReader(pkts1...)}

	// reader2 holds fresh packets that arrive after the swap.
	var pkts2 []mockPacket

	for i := 0; i < rtp.PrebufferPackets+2; i++ {
		raw := makeRTPBytes(t, uint16(i))
		pkts2 = append(pkts2, mockPacket{data: raw, src: netip.MustParseAddrPort("1.2.3.4:5004")})
	}

	reader2 := newMockReader(pkts2...)

	pc := &PortChannel{
		cfg:      McastPortConfig{Send: true, Receive: true},
		Receiver: rtp.NewSwappableReceiver(reader1),
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)
	pc.PlaybackBuffer = make(chan []int16, 64)

	rt := &CommsRuntime{
		Ports: []*PortChannel{pc},
	}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})

	go func() {
		defer close(done)

		cfg.receiveLoop(ctx, pc, rt)
	}()

	// Wait for reader1 to be exhausted.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader1.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if reader1.remaining() != 0 {
		t.Fatal("timed out waiting for reader1 to be exhausted")
	}

	// Drain any frames already queued from reader1.
	for len(pc.PlaybackBuffer) > 0 {
		<-pc.PlaybackBuffer
	}

	// Swap in reader2 then close reader1 to unblock the stale ReadFromUDP.
	// receiveLoop will get net.ErrClosed, call jitter.Reset(), then pick up
	// reader2 on the next iteration.
	pc.Receiver.Swap(reader2)
	reader1.Close()

	// Wait for reader2 packets to be delivered to receiveLoop.
	deadline = time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if reader2.remaining() == 0 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	pc.Receiver.Close()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("receiveLoop did not exit after context cancel")
	}

	// Verify reader2 was fully consumed, proving the loop recovered after the
	// socket swap and the jitter buffer was reset.
	if reader2.remaining() != 0 {
		t.Errorf("receiveLoop did not consume reader2 packets after socket swap; %d remaining",
			reader2.remaining())
	}
}

// ─── Web-mode playout tests ─────────────────────────────────────────────────

// In web mode the receiveLoop spawns webPlayoutLoop, which forwards raw
// Opus payloads from the jitter buffer to the WebAudioBridge for streaming
// to the browser. The malgo playback stream is not opened and playoutOneFrame is not used.

// TestWebPlayoutLoop_GatesWhenNoConsumer pins the idle-web-mode contract:
// with no RPC stream attached to the bridge, the drain must still empty the
// jitter buffer (so the cursor advances and pool buffers recycle) but must
// not copy or offer frames to the bridge channel.
func TestWebPlayoutLoop_GatesWhenNoConsumer(t *testing.T) {
	cfg := newSilentComms()
	rt, pc := newReceiveRuntime()

	bridge := webaudio.NewBridge(zerolog.Nop(), func(payload []byte) {
		cfg.sendToAllPorts(rt, payload)
	})
	rt.WebBridge = bridge
	// Deliberately no AddConsumer: the browser tab is closed.

	jb := rtp.NewJitterBuffer(1, 16)
	for i := range uint16(5) {
		jb.Push(i, []byte{byte(i)})
	}

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go cfg.webPlayoutLoop(ctx, pc, jb, rt)

	// Wait until the drain has consumed everything the jitter buffer holds.
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if bridge.RxGatedNoConsumer.Load() == 5 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if got := bridge.RxGatedNoConsumer.Load(); got != 5 {
		t.Fatalf("gated-frame counter: got %d, want 5", got)
	}

	if got := bridge.RxPushIn.Load(); got != 0 {
		t.Errorf("no frame should be offered to the bridge without a consumer; RxPushIn=%d", got)
	}

	select {
	case f := <-bridge.RxFrames():
		t.Errorf("bridge channel should stay empty without a consumer; got frame %v", f.Data())
		f.Release()
	default:
	}
}

func TestWebPlayoutLoop_ForwardsRawOpus(t *testing.T) {
	cfg := newSilentComms()
	rt, pc := newReceiveRuntime()

	bridge := webaudio.NewBridge(zerolog.Nop(), func(payload []byte) {
		cfg.sendToAllPorts(rt, payload)
	})
	rt.WebBridge = bridge
	bridge.AddConsumer() // this test reads RxFrames directly

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA, 0xBB, 0xCC})

	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()

	go cfg.webPlayoutLoop(ctx, pc, jb, rt)

	// The raw Opus bytes should arrive on the bridge's RX channel.
	select {
	case frame := <-bridge.RxFrames():
		if d := frame.Data(); len(d) != 3 || d[0] != 0xAA || d[1] != 0xBB || d[2] != 0xCC {
			t.Errorf("unexpected frame data: %v", d)
		}

		frame.Release()
	case <-time.After(500 * time.Millisecond):
		t.Error("timed out waiting for raw Opus frame on web bridge")
	}
}

// TestWebPlayoutLoop_TagsFramesWithTalkGroupChannel pins the talk group
// attribution contract: every frame handed to the web bridge carries the
// 1-based channel derived from the port's UDP port (38803 → channel 2), so
// the RPC layer can tell the browser which talk group the audio belongs
// to. A port outside the talk group plan tags frames with 0 (unknown).
func TestWebPlayoutLoop_TagsFramesWithTalkGroupChannel(t *testing.T) {
	tests := []struct {
		name string
		port int
		want byte
	}{
		{name: "talkgroup 2 port", port: 38803, want: 2},
		{name: "unmapped port", port: 40000, want: 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			cfg := newSilentComms()
			rt, pc := newReceiveRuntime()
			pc.cfg.Port = tc.port

			bridge := webaudio.NewBridge(zerolog.Nop(), func(payload []byte) {
				cfg.sendToAllPorts(rt, payload)
			})
			rt.WebBridge = bridge
			bridge.AddConsumer() // this test reads RxFrames directly

			jb := rtp.NewJitterBuffer(1, 16)
			jb.Push(0, []byte{0xAA})

			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()

			go cfg.webPlayoutLoop(ctx, pc, jb, rt)

			select {
			case frame := <-bridge.RxFrames():
				if got := frame.Channel(); got != tc.want {
					t.Errorf("frame channel: got %d, want %d", got, tc.want)
				}

				frame.Release()
			case <-time.After(500 * time.Millisecond):
				t.Error("timed out waiting for frame on web bridge")
			}
		})
	}
}

// TestWebPlayoutLoop_MultipleFrames verifies that a sequence of frames is
// streamed through the web bridge in order.
func TestWebPlayoutLoop_MultipleFrames(t *testing.T) {
	cfg := newSilentComms()

	pc := &PortChannel{
		cfg: McastPortConfig{Send: true, Receive: true},
	}
	pc.SendEnabled.Store(true)
	pc.ReceiveEnabled.Store(true)

	rt := &CommsRuntime{Ports: []*PortChannel{pc}}

	bridge := webaudio.NewBridge(zerolog.Nop(), func(payload []byte) {
		cfg.sendToAllPorts(rt, payload)
	})
	rt.WebBridge = bridge
	bridge.AddConsumer() // this test reads RxFrames directly

	jb := rtp.NewJitterBuffer(1, 16)
	for i := 0; i < 5; i++ {
		jb.Push(uint16(i), []byte{byte(i)})
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go cfg.webPlayoutLoop(ctx, pc, jb, rt)

	for i := 0; i < 5; i++ {
		select {
		case frame := <-bridge.RxFrames():
			if d := frame.Data(); len(d) != 1 || d[0] != byte(i) {
				t.Errorf("frame %d: got %v, want [%d]", i, d, i)
			}

			frame.Release()
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("timed out waiting for frame %d", i)
		}
	}
}

// TestWebPlayoutLoop_DeliversWhileBroadcasting verifies that the web
// playout loop has no half-duplex suppression: frames flow to the bridge
// even while the local node is broadcasting.
func TestWebPlayoutLoop_DeliversWhileBroadcasting(t *testing.T) {
	cfg := newSilentComms()
	rt, pc := newReceiveRuntime()

	bridge := webaudio.NewBridge(zerolog.Nop(), func(payload []byte) {
		cfg.sendToAllPorts(rt, payload)
	})
	rt.WebBridge = bridge
	bridge.AddConsumer() // this test reads RxFrames directly
	rt.Broadcasting.Store(true)

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0x01})

	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	go cfg.webPlayoutLoop(ctx, pc, jb, rt)

	select {
	case f := <-bridge.RxFrames():
		// OK — frame delivered as expected.
		f.Release()
	case <-ctx.Done():
		t.Error("bridge should receive frames in web mode even while broadcasting")
	}
}

// ─── consecutive PLC limit tests ─────────────────────────────────────────────

func TestPlayoutOneFrame_ConsecutivePLCLimit(t *testing.T) {
	// Set up a jitter buffer where the stream is active (recent lastPush)
	// but all subsequent frames are missing. playoutOneFrame should emit
	// exactly maxConsecutivePLC PLC frames followed by silence.
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{fillValue: 99, returnN: audiopool.FrameSize}

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0})
	jb.PopReady() // started=true, expected=1, lastPush set

	cfg := &CommsConfig{Log: zerolog.Nop()}

	// First maxConsecutivePLC calls should produce non-zero PLC samples.
	for i := 0; i < maxConsecutivePLC; i++ {
		out := driveOneFrame(cfg, pc, rt, jb)
		if isAllZero(out) {
			t.Fatalf("frame %d: expected PLC samples; got silence", i)
		}
	}

	// All subsequent calls should return silence.
	for i := 0; i < 5; i++ {
		out := driveOneFrame(cfg, pc, rt, jb)
		if !isAllZero(out) {
			t.Errorf("post-cap frame %d: expected silence; got non-zero samples", i)
		}
	}
}

func TestPlayoutOneFrame_ConsecutivePLCResets(t *testing.T) {
	// After a burst of PLC, decoding a real frame should reset the
	// consecutivePLC counter so a subsequent gap produces PLC again.
	rt, pc := newReceiveRuntime()
	pc.Decoder = &mockDecoder{fillValue: 99, returnN: audiopool.FrameSize}

	jb := rtp.NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0})
	jb.PopReady() // started=true, expected=1, lastPush set

	cfg := &CommsConfig{Log: zerolog.Nop()}

	// Drive a few PLC frames. Each call advances expected via
	// advancePastLocked → expected goes 1→2→3→4 across these 3 calls.
	for i := 0; i < 3; i++ {
		_ = driveOneFrame(cfg, pc, rt, jb)
	}

	if pc.ConsecutivePLC == 0 {
		t.Fatal("expected consecutivePLC > 0 after PLC frames")
	}

	// Push a real packet at the current expected cursor (4) so the next
	// pop returns it directly. Anything ahead of expected is buffered but
	// not popped until the cursor advances to its slot.
	jb.Push(4, []byte{0xBB})

	out := driveOneFrame(cfg, pc, rt, jb)
	if isAllZero(out) {
		t.Fatal("expected decoded samples after pushing real frame")
	}

	if pc.ConsecutivePLC != 0 {
		t.Errorf("consecutivePLC should reset to 0 after a real frame; got %d", pc.ConsecutivePLC)
	}
}

// ─── jitter buffer overflow counter test ─────────────────────────────────────

func TestJitterBuffer_OverflowCounter(t *testing.T) {
	jb := rtp.NewJitterBuffer(1, 4)

	// Fill buffer to maxDepth with seqs 0-3.
	for i := 0; i < 4; i++ {
		if !jb.Push(uint16(i), []byte{byte(i)}) {
			t.Fatalf("push(%d) failed unexpectedly", i)
		}
	}

	// Push a duplicate while the buffer is full. The duplicate is rejected
	// AND count >= maxDepth, so the overflow counter should increment.
	if jb.Push(0, []byte{0xFF}) {
		t.Error("duplicate push should have failed")
	}

	if got := jb.Overflows.Load(); got != 1 {
		t.Errorf("expected overflows=1; got %d", got)
	}

	// Push more duplicates — each should increment.
	jb.Push(1, []byte{0xFF})
	jb.Push(2, []byte{0xFF})
	jb.Push(3, []byte{0xFF})

	if got := jb.Overflows.Load(); got != 4 {
		t.Errorf("expected overflows=4; got %d", got)
	}
}

// TestWebPlayoutLoop_DoesNotAdvanceCursorOnSafetyPoll is a regression
// guard for the round-4 stutter fix. Before the fix, webPlayoutLoop used
// PopOrConceal which called advancePastLocked on every safety-poll tick
// while the buffer was empty and the stream was "recent". That one-way
// cursor advance caused late-but-correctly-sequenced arrivals to be
// rejected by pushLocked's seqLess(seq, expected) check.
//
// The fixed loop uses PopReady, which never advances the cursor without
// actually popping a frame. This test sets up the exact arrival pattern
// that triggered the bug and asserts:
//   - The late arrival is NOT rejected by the jitter buffer
//   - webPlayoutLoop delivers it to the bridge
//   - pc.RxPushRejected stays at 0
func TestWebPlayoutLoop_DoesNotAdvanceCursorOnSafetyPoll(t *testing.T) {
	cfg := newSilentComms()
	rt, pc := newReceiveRuntime()

	bridge := webaudio.NewBridge(zerolog.Nop(), func(payload []byte) {
		cfg.sendToAllPorts(rt, payload)
	})
	rt.WebBridge = bridge
	bridge.AddConsumer() // this test reads RxFrames directly

	jb := rtp.NewJitterBuffer(1, 16)

	// Seed the stream at seq=100 and drain it so expected=101. Using
	// PushWithSSRC with a stable SSRC matches the production path.
	const ssrc = uint32(0x12345678)
	if !jb.PushWithSSRC(ssrc, 100, []byte{0xAA}, nil) {
		t.Fatal("failed to push seed frame")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	go cfg.webPlayoutLoop(ctx, pc, jb, rt)

	// Drain the seed frame so the loop observes expected=101 with an
	// empty buffer.
	select {
	case f := <-bridge.RxFrames():
		f.Release()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("seed frame never reached bridge")
	}

	// Sleep 300 ms — at least 3 safety-poll (100 ms) ticks. Before the
	// fix, each tick would call advancePastLocked and bump expected from
	// 101 to 104. After the fix, PopReady never advances on empty.
	time.Sleep(300 * time.Millisecond)

	// Push the next sequentially-correct frame. The pre-fix version of
	// this loop would have advanced expected past 101 and rejected this
	// push; the fixed version accepts it.
	if !jb.PushWithSSRC(ssrc, 101, []byte{0xBB}, nil) {
		t.Fatal("seq=101 push was rejected by jitter buffer (regression)")
	}

	select {
	case frame := <-bridge.RxFrames():
		if d := frame.Data(); len(d) != 1 || d[0] != 0xBB {
			t.Errorf("unexpected frame bytes: %v", d)
		}

		frame.Release()
	case <-time.After(500 * time.Millisecond):
		t.Fatal("seq=101 frame never reached bridge (regression)")
	}

	if got := pc.RxPushRejected.Load(); got != 0 {
		t.Errorf("RxPushRejected must stay 0 in the fixed loop; got %d", got)
	}
}
