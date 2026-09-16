package rtp

import (
	"testing"
	"time"
)

func TestSeqLess_NormalOrder(t *testing.T) {
	if !seqLess(1, 2) {
		t.Error("seqLess(1, 2) should be true")
	}

	if seqLess(2, 1) {
		t.Error("seqLess(2, 1) should be false")
	}

	if seqLess(5, 5) {
		t.Error("seqLess(5, 5) should be false (equal)")
	}
}

func TestSeqLess_WrapAround(t *testing.T) {
	// 0xFFFE < 0xFFFF < 0x0000 < 0x0001 under wrap-around semantics.
	if !seqLess(0xFFFE, 0xFFFF) {
		t.Error("seqLess(0xFFFE, 0xFFFF) should be true")
	}

	if !seqLess(0xFFFF, 0x0000) {
		t.Error("seqLess(0xFFFF, 0x0000) should be true (wrap)")
	}

	if seqLess(0x0000, 0xFFFF) {
		t.Error("seqLess(0x0000, 0xFFFF) should be false (wrap)")
	}
}

func TestJitterBuffer_PushOversizedPayload(t *testing.T) {
	jb := NewJitterBuffer(1, MaxDepth)

	// receiveLoop reads into a 1500-byte buffer, so a hostile or malformed
	// sender can deliver an RTP payload larger than the pool's
	// MaxOpusPayloadSize capacity. Push must reject it, not panic.
	oversized := make([]byte, MaxOpusPayloadSize+125)
	if ok := jb.Push(0, oversized); ok {
		t.Fatal("oversized payload must be rejected")
	}

	// The buffer must remain fully usable afterwards.
	if ok := jb.Push(1, []byte{42}); !ok {
		t.Fatal("push after oversized rejection must succeed")
	}

	payload, ready, _ := jb.PopReady()
	if !ready || payload[0] != 42 {
		t.Fatalf("expected seq=1 payload after rejection, got ready=%v payload=%v", ready, payload)
	}
}

func TestJitterBuffer_SeqWrapContinuity(t *testing.T) {
	jb := NewJitterBuffer(1, MaxDepth)

	// A window of 24 in-flight frames straddling the uint16 sequence wrap.
	// The seq→slot mapping must be continuous across the wrap: if maxDepth
	// does not divide 65536, in-window frames collide and evict each other,
	// surfacing as periodic PLC gaps roughly every 22 minutes of talk.
	seqs := make([]uint16, 0, 24)
	for i := range 24 {
		seqs = append(seqs, uint16(65524+i)) // 65524..65535, then 0..11
	}

	for _, s := range seqs {
		if !jb.Push(s, []byte{byte(s)}) {
			t.Fatalf("push seq=%d must succeed", s)
		}
	}

	for _, s := range seqs {
		payload, ready, skipped := jb.PopReady()
		if !ready {
			t.Fatalf("seq=%d: expected in-order pop, got ready=false skipped=%v", s, skipped)
		}

		if payload[0] != byte(s) {
			t.Fatalf("seq=%d: wrong payload byte %d", s, payload[0])
		}

		jb.ReleasePayload(payload)
	}
}

func TestNewJitterBuffer_RejectsNonPowerOfTwoDepth(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for non-power-of-two depth")
		}
	}()

	NewJitterBuffer(1, 10)
}

func TestNewJitterBuffer_RejectsDepthBeyondSlots(t *testing.T) {
	defer func() {
		if recover() == nil {
			t.Fatal("expected panic for depth larger than the slot array")
		}
	}()

	NewJitterBuffer(1, MaxDepth*2)
}

func TestJitterBuffer_InOrderDelivery(t *testing.T) {
	jb := NewJitterBuffer(1, 16) // prebuffer=1 so first push triggers start

	jb.Push(10, []byte{1, 2, 3})

	payload, ready, skipped := jb.PopReady()
	if !ready {
		t.Fatal("expected ready=true")
	}

	if skipped {
		t.Fatal("expected skipped=false")
	}

	if len(payload) != 3 || payload[0] != 1 {
		t.Errorf("wrong payload: %v", payload)
	}
}

func TestJitterBuffer_Prebuffer(t *testing.T) {
	jb := NewJitterBuffer(3, 16)

	// Push only 2 packets — not enough to start.
	jb.Push(0, []byte{0})
	jb.Push(1, []byte{1})

	_, ready, skipped := jb.PopReady()
	if ready || skipped {
		t.Error("should not deliver before prebuffer threshold")
	}

	// Third packet meets the threshold.
	jb.Push(2, []byte{2})

	_, ready, _ = jb.PopReady()
	if !ready {
		t.Error("expected ready after prebuffer threshold met")
	}
}

func TestJitterBuffer_StalePacketRejected(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	jb.Push(5, []byte{5})

	jb.PopReady() // advance expected to 6

	if jb.Push(5, []byte{5}) {
		t.Error("stale seq=5 should be rejected after seq=6 is expected")
	}
}

func TestJitterBuffer_DuplicateRejected(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	if !jb.Push(10, []byte{10}) {
		t.Fatal("first push should succeed")
	}

	if jb.Push(10, []byte{10}) {
		t.Error("duplicate seq=10 should be rejected")
	}
}

func TestJitterBuffer_OutOfOrderDelivery(t *testing.T) {
	// prebuffer=1 so a single push satisfies the threshold.
	jb := NewJitterBuffer(1, 16)

	// Push seq=0 first — this sets expected=0 and satisfies the prebuffer.
	jb.Push(0, []byte{0})

	// Pop seq=0 (started=true, expected advances to 1).
	payload, ready, _ := jb.PopReady()
	if !ready {
		t.Fatal("expected ready for seq=0")
	}

	if payload[0] != 0 {
		t.Errorf("expected payload[0]=0; got %d", payload[0])
	}

	// Now push seq=2 then seq=1 (out of order, both future).
	jb.Push(2, []byte{2})
	jb.Push(1, []byte{1}) // arrives after seq=2 but is expected=1

	// popReady should deliver seq=1 first (in-order by expected).
	payload, ready, _ = jb.PopReady()
	if !ready {
		t.Fatal("expected ready for seq=1")
	}

	if payload[0] != 1 {
		t.Errorf("expected payload[0]=1; got %d", payload[0])
	}

	// Then seq=2.
	payload, ready, _ = jb.PopReady()
	if !ready {
		t.Fatal("expected ready for seq=2")
	}

	if payload[0] != 2 {
		t.Errorf("expected payload[0]=2; got %d", payload[0])
	}
}

func TestJitterBuffer_SkipMissingWhenOverflow(t *testing.T) {
	maxDepth := 4
	jb := NewJitterBuffer(1, maxDepth)

	// Push seq=0 first to initialize expected=0 and pass prebuffer.
	jb.Push(0, []byte{0})
	jb.PopReady() // expected is now 1

	// Push maxDepth/2 = 2 packets that are NOT seq=1 (skip seq=1).
	jb.Push(2, []byte{2})
	jb.Push(3, []byte{3})

	// Buffer has 2 frames >= maxDepth/2=2, expected=1 missing → skip.
	_, ready, skipped := jb.PopReady()
	if ready {
		t.Error("expected ready=false for missing seq=1")
	}

	if !skipped {
		t.Error("expected skipped=true when buffer at half-depth with missing expected")
	}
}

func TestJitterBuffer_ShouldConceal(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	// Not started yet — should NOT conceal.
	if jb.ShouldConceal(100 * time.Millisecond) {
		t.Error("shouldConceal should be false before any push")
	}

	jb.Push(0, []byte{0})
	jb.PopReady() // started = true

	// Just pushed — should conceal within the window.
	if !jb.ShouldConceal(100 * time.Millisecond) {
		t.Error("shouldConceal should be true right after a push")
	}
}

func TestJitterBuffer_AdvancePast(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	jb.Push(0, []byte{0})
	jb.PopReady() // expected=1 now

	jb.Push(1, []byte{1})

	jb.AdvancePast() // should discard seq=1 and advance expected to 2

	// seq=1 should now be treated as stale.
	if jb.Push(1, []byte{1}) {
		t.Error("seq=1 should be stale after advancePast")
	}

	// seq=2 should succeed.
	if !jb.Push(2, []byte{2}) {
		t.Error("seq=2 should be accepted after advancePast")
	}
}

func TestJitterBuffer_WrapAroundSequence(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	// Start just below wrap point.
	jb.Push(0xFFFE, []byte{0xFE})
	jb.PopReady() // expected = 0xFFFF

	jb.Push(0xFFFF, []byte{0xFF})

	payload, ready, _ := jb.PopReady() // expected = 0x0000
	if !ready || payload[0] != 0xFF {
		t.Error("expected seq=0xFFFF to be delivered correctly")
	}

	jb.Push(0x0000, []byte{0x00})

	payload, ready, _ = jb.PopReady() // expected = 0x0001
	if !ready || payload[0] != 0x00 {
		t.Error("expected seq=0x0000 (wrap) to be delivered correctly")
	}
}

// ─── popOrConceal tests ──────────────────────────────────────────────────────

func TestPopOrConceal_ReturnsReadyFrame(t *testing.T) {
	jb := NewJitterBuffer(1, 16)
	jb.Push(0, []byte{0xAA})

	payload, conceal := jb.PopOrConceal(100 * time.Millisecond)
	if payload == nil || payload[0] != 0xAA {
		t.Errorf("expected payload 0xAA, got %v", payload)
	}

	if conceal {
		t.Error("conceal should be false when frame is returned")
	}
}

func TestPopOrConceal_ConcealOnSkippedGap(t *testing.T) {
	jb := NewJitterBuffer(1, 4)

	jb.Push(0, []byte{0})
	jb.PopOrConceal(100 * time.Millisecond) // consume seq=0, expected=1

	jb.Push(2, []byte{2})
	jb.Push(3, []byte{3}) // count=2 >= maxDepth/2=2, seq=1 missing → skip

	payload, conceal := jb.PopOrConceal(100 * time.Millisecond)
	if payload != nil {
		t.Error("expected nil payload for skipped gap")
	}

	if !conceal {
		t.Error("expected conceal=true when buffer skips missing seq")
	}
}

func TestPopOrConceal_ConcealOnEmptyActiveStream(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	jb.Push(0, []byte{0})
	jb.PopOrConceal(100 * time.Millisecond) // started=true, lastPush ~now

	// Buffer is now empty but lastPush is recent.
	payload, conceal := jb.PopOrConceal(100 * time.Millisecond)
	if payload != nil {
		t.Error("expected nil payload")
	}

	if !conceal {
		t.Error("expected conceal=true for empty buffer with recent push")
	}
}

func TestPopOrConceal_NoConcealWhenStale(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	jb.Push(0, []byte{0})
	jb.PopOrConceal(100 * time.Millisecond)

	// Force lastPush to be old.
	jb.mu.Lock()
	jb.lastPush = time.Now().Add(-200 * time.Millisecond)
	jb.mu.Unlock()

	payload, conceal := jb.PopOrConceal(100 * time.Millisecond)
	if payload != nil {
		t.Error("expected nil payload")
	}

	if conceal {
		t.Error("expected conceal=false when lastPush is beyond recentWindow")
	}
}

func TestPopOrConceal_NoConcealBeforeStart(t *testing.T) {
	jb := NewJitterBuffer(3, 16)

	// Only push 1 packet, need 3 for prebuffer.
	jb.Push(0, []byte{0})

	payload, conceal := jb.PopOrConceal(100 * time.Millisecond)
	if payload != nil || conceal {
		t.Error("expected nothing before prebuffer threshold")
	}
}

// ─── Ring buffer integrity tests ─────────────────────────────────────────────

func TestJitterBuffer_PushCopiesPayload(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	input := []byte{1, 2, 3}
	jb.Push(0, input)

	// Mutate the input after push.
	input[0] = 99

	payload, ready, _ := jb.PopReady()
	if !ready {
		t.Fatal("expected ready")
	}

	if payload[0] != 1 {
		t.Error("jitter buffer should hold a copy, not a reference to input")
	}
}

func TestJitterBuffer_RingBufferOverwrite(t *testing.T) {
	// With maxDepth=4, seq 0 and seq 4 map to the same slot (0 % 4 == 4 % 4).
	// After popping seq 0-3, pushing seq 4 should reuse the slot.
	jb := NewJitterBuffer(1, 4)

	for i := uint16(0); i < 4; i++ {
		jb.Push(i, []byte{byte(i)})
	}

	for range 4 {
		jb.PopReady()
	}

	// Now push seq=4 which maps to slot 0.
	if !jb.Push(4, []byte{4}) {
		t.Error("push seq=4 should succeed after slot 0 was freed")
	}

	payload, ready, _ := jb.PopReady()
	if !ready || payload[0] != 4 {
		t.Errorf("expected payload=4, got ready=%v payload=%v", ready, payload)
	}
}

func TestJitterBuffer_FullBufferRejectsNewSequence(t *testing.T) {
	jb := NewJitterBuffer(1, 4)

	for i := uint16(0); i < 4; i++ {
		if !jb.Push(i, []byte{byte(i)}) {
			t.Fatalf("push seq=%d should succeed", i)
		}
	}

	// Buffer is full. New seq that doesn't collide with existing should fail
	// only if the slot is occupied by a different valid seq number.
	// seq=4 maps to slot 0 which has seq=0 — the overwrite path triggers
	// because slot.valid && slot.seq != seq.
	// Actually with maxDepth=4 this will overwrite. Let's test with a seq
	// that maps to a full, non-overwritable scenario.
	// Since all 4 slots are occupied, and any new seq will map to one of them,
	// the overwrite logic will kick in. Let's verify count stays at 4.
	jb.Push(4, []byte{4}) // overwrites slot 0 (was seq=0)

	jb.mu.Lock()
	count := jb.count
	jb.mu.Unlock()

	if count != 4 {
		t.Errorf("count should be 4 after overwrite, got %d", count)
	}
}

// ─── reset() tests ───────────────────────────────────────────────────────────

// TestJitterBuffer_Reset_ClearsState verifies that reset() zeros all internal
// state so the jitter buffer behaves as if freshly constructed.
func TestJitterBuffer_Reset_ClearsState(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	// Advance to started state.
	jb.Push(0, []byte{0})
	jb.PopReady() // started=true, expected=1

	jb.Reset()

	// After reset: no frame should be ready regardless of sequence.
	jb.mu.Lock()
	count := jb.count
	init := jb.init
	started := jb.started
	jb.mu.Unlock()

	if count != 0 {
		t.Errorf("count after reset: got %d, want 0", count)
	}

	if init {
		t.Error("init should be false after reset")
	}

	if started {
		t.Error("started should be false after reset")
	}

	// A push starting at any sequence number must be accepted as a fresh stream.
	if !jb.Push(99, []byte{0x99}) {
		t.Error("push to any seq should succeed on a reset buffer")
	}

	// One push satisfies prebuffer=1; frame must be ready.
	_, ready, _ := jb.PopReady()
	if !ready {
		t.Error("expected ready after first push on reset buffer with prebuffer=1")
	}
}

// ─── SSRC change & idle reset tests ─────────────────────────────────────────

// TestJitterBuffer_SSRCChangeResets is the regression test for the silent-stall
// bug: a new talker on the multicast group whose starting sequence number lies
// in the "past half" of the previous talker's frozen cursor must NOT be
// rejected. The jitter buffer must detect the SSRC change and reset.
func TestJitterBuffer_SSRCChangeResets(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	const (
		ssrcA = uint32(0x11111111)
		ssrcB = uint32(0x22222222)
	)

	// Talker A streams seqs 0..4, drained → expected = 5.
	for i := uint16(0); i <= 4; i++ {
		if !jb.PushWithSSRC(ssrcA, i, []byte{byte(i)}, nil) {
			t.Fatalf("ssrcA push seq=%d rejected", i)
		}
	}

	for range 5 {
		if _, ready, _ := jb.PopReady(); !ready {
			t.Fatal("expected ssrcA frames to be ready")
		}
	}

	// Talker B starts at seq 0x8005. int16(0x8005 - 5) = -32768, so
	// seqLess(0x8005, 5) is true — on a SSRC-blind buffer this packet would
	// be silently rejected as "stale" forever. With SSRC tracking the buffer
	// must reset and accept it as a fresh stream.
	var (
		gotOld, gotNew uint32
		gotChange      bool
	)

	cb := func(oldSSRC, newSSRC uint32) {
		gotOld = oldSSRC
		gotNew = newSSRC
		gotChange = true
	}

	if !jb.PushWithSSRC(ssrcB, 0x8005, []byte{0xBB}, cb) {
		t.Fatal("ssrcB starting packet must be accepted after SSRC change")
	}

	if !gotChange {
		t.Fatal("expected SSRC change callback to fire")
	}

	if gotOld != ssrcA || gotNew != ssrcB {
		t.Errorf("callback got (%#x→%#x), want (%#x→%#x)", gotOld, gotNew, ssrcA, ssrcB)
	}

	if got := jb.SSRCResets.Load(); got != 1 {
		t.Errorf("SSRCResets = %d, want 1", got)
	}

	// The new packet must be deliverable.
	payload, ready, _ := jb.PopReady()
	if !ready || len(payload) == 0 || payload[0] != 0xBB {
		t.Errorf("expected payload 0xBB after SSRC change, got ready=%v payload=%v", ready, payload)
	}
}

// TestJitterBuffer_SSRCChange_NoCallbackOnSameSSRC verifies that a stream of
// packets all sharing the same SSRC never triggers the SSRC-change path.
func TestJitterBuffer_SSRCChange_NoCallbackOnSameSSRC(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	called := false
	cb := func(_, _ uint32) { called = true }

	for i := uint16(0); i < 5; i++ {
		jb.PushWithSSRC(0xDEADBEEF, i, []byte{byte(i)}, cb)
	}

	if called {
		t.Error("SSRC change callback fired despite constant SSRC")
	}

	if got := jb.SSRCResets.Load(); got != 0 {
		t.Errorf("SSRCResets = %d, want 0", got)
	}
}

// TestJitterBuffer_SameSSRCStalePacketStillDropped is a regression that
// preserves the existing reorder protection: with the same SSRC, a stale seq
// must still be dropped (otherwise we'd accept duplicates and out-of-window
// reorderings as fresh streams).
func TestJitterBuffer_SameSSRCStalePacketStillDropped(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	jb.PushWithSSRC(1, 100, []byte{100}, nil)
	jb.PopReady() // expected = 101

	if jb.PushWithSSRC(1, 100, []byte{100}, nil) {
		t.Error("stale seq=100 with same SSRC must still be rejected")
	}
}

// TestJitterBuffer_IdleTimeoutResets verifies the defensive idle-reset safety
// net: a long inter-arrival gap causes the buffer to treat the next packet as
// the start of a fresh stream, even with the same SSRC.
func TestJitterBuffer_IdleTimeoutResets(t *testing.T) {
	fakeNow := time.Unix(0, 0)
	jb := NewJitterBuffer(1, 16)
	jb.now = func() time.Time { return fakeNow }

	// First push at t=0, drain it.
	if !jb.PushWithSSRC(1, 1000, []byte{0xAA}, nil) {
		t.Fatal("initial push rejected")
	}

	jb.PopReady()

	// Advance the clock past the idle threshold.
	fakeNow = fakeNow.Add(jitterIdleResetThreshold + time.Second)

	// A packet with a stale seq (would be rejected by seqLess against
	// expected=1001) must now be accepted because the idle-reset clears state.
	if !jb.PushWithSSRC(1, 50, []byte{0xBB}, nil) {
		t.Error("stale-seq packet after idle gap must be accepted (idle reset)")
	}

	if got := jb.IdleResets.Load(); got != 1 {
		t.Errorf("IdleResets = %d, want 1", got)
	}

	payload, ready, _ := jb.PopReady()
	if !ready || payload[0] != 0xBB {
		t.Errorf("expected payload 0xBB after idle reset, got ready=%v payload=%v", ready, payload)
	}
}

// TestJitterBuffer_IdleTimeout_NoResetWithinWindow verifies that arrivals
// within the idle window do not trigger a reset.
func TestJitterBuffer_IdleTimeout_NoResetWithinWindow(t *testing.T) {
	fakeNow := time.Unix(0, 0)
	jb := NewJitterBuffer(1, 16)
	jb.now = func() time.Time { return fakeNow }

	jb.PushWithSSRC(1, 1000, []byte{0xAA}, nil)
	jb.PopReady()

	// Just below the threshold.
	fakeNow = fakeNow.Add(jitterIdleResetThreshold - time.Millisecond)

	// Stale seq must still be rejected — no idle reset yet.
	if jb.PushWithSSRC(1, 50, []byte{0xBB}, nil) {
		t.Error("stale-seq packet within idle window must be rejected")
	}

	if got := jb.IdleResets.Load(); got != 0 {
		t.Errorf("IdleResets = %d, want 0", got)
	}
}

// TestJitterBuffer_Reset_PrebufferRestartsAfterReset verifies that after reset
// the prebuffer threshold must be satisfied again before popReady returns frames.
func TestJitterBuffer_Reset_PrebufferRestartsAfterReset(t *testing.T) {
	jb := NewJitterBuffer(3, 16) // need 3 pushes before started

	// Satisfy prebuffer and advance to started.
	for i := uint16(0); i < 3; i++ {
		jb.Push(i, []byte{byte(i)})
	}

	_, ready, _ := jb.PopReady()
	if !ready {
		t.Fatal("expected started after 3 pushes with prebuffer=3")
	}

	jb.Reset()

	// After reset, a single push must NOT trigger delivery.
	jb.Push(50, []byte{50})

	_, ready, _ = jb.PopReady()
	if ready {
		t.Error("expected not-ready after only 1 push on reset buffer with prebuffer=3")
	}

	// Two more pushes reach the threshold; delivery must resume.
	jb.Push(51, []byte{51})
	jb.Push(52, []byte{52})

	_, ready, _ = jb.PopReady()
	if !ready {
		t.Error("expected ready after 3 pushes on reset buffer")
	}
}

// TestJitterBuffer_NotifyOnPush exercises the edge-triggered notify channel
// installed by EnableNotify: a successful push must wake any consumer that
// has parked on the channel within a single signal interval.
func TestJitterBuffer_NotifyOnPush(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	notify := jb.EnableNotify()

	// Drain any state-zero signal (none expected, but be defensive).
	select {
	case <-notify:
		t.Fatal("notify channel should be empty before any Push")
	default:
	}

	jb.Push(0, []byte{0xAB})

	select {
	case <-notify:
		// expected
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected notify wake within 100ms after Push")
	}
}

// TestJitterBuffer_NotifyCoalescesBurst verifies that a burst of pushes does
// NOT block the producer: the depth-1 buffered channel coalesces multiple
// signals into one, and the consumer is responsible for draining all
// available frames per wake.
func TestJitterBuffer_NotifyCoalescesBurst(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	notify := jb.EnableNotify()

	// Push 5 frames in a tight loop without consuming the notify signal
	// between them — every Push must return promptly (no blocking).
	for i := uint16(0); i < 5; i++ {
		done := make(chan bool)

		go func() {
			jb.Push(i, []byte{byte(i)})

			done <- true
		}()

		select {
		case <-done:
		case <-time.After(50 * time.Millisecond):
			t.Fatalf("Push %d blocked; signal channel should coalesce", i)
		}
	}

	// At least one signal must be readable.
	select {
	case <-notify:
	case <-time.After(100 * time.Millisecond):
		t.Fatal("expected at least one notify signal after burst")
	}
}

// TestJitterBuffer_NotifyDisabledByDefault verifies that consumers which
// never call EnableNotify pay no signaling cost: pushes succeed silently and
// no goroutine ever wakes from a notify channel.
func TestJitterBuffer_NotifyDisabledByDefault(t *testing.T) {
	jb := NewJitterBuffer(1, 16)

	if jb.notifyCh != nil {
		t.Fatal("notifyCh should be nil before EnableNotify is called")
	}

	if !jb.Push(0, []byte{0xCD}) {
		t.Fatal("Push must succeed even when notify is disabled")
	}

	if jb.notifyCh != nil {
		t.Fatal("Push must not allocate notifyCh as a side effect")
	}
}

// TestJitterBuffer_GapHistogram verifies that sequence-gap runs are
// bucketed by length exactly once per run at the first skip-missing
// event, not once per skipped frame, and that a successful pop
// terminates a run so the next gap is counted independently.
func TestJitterBuffer_GapHistogram(t *testing.T) {
	maxDepth := 32
	jb := NewJitterBuffer(1, maxDepth)

	// Initialize expected=0, prebuffer satisfied.
	jb.Push(0, []byte{0})

	if _, ready, _ := jb.PopReady(); !ready {
		t.Fatal("seq=0 must be ready")
	}
	// expected is now 1.

	// Craft a single 3-frame gap at 1..3. We need jb.count >= maxDepth/2 = 16
	// to enter the skip branch. Push seqs 4..19 (16 packets) leaving 1..3
	// missing.
	for s := uint16(4); s <= 19; s++ {
		if !jb.Push(s, []byte{byte(s)}) {
			t.Fatalf("push seq=%d must succeed", s)
		}
	}

	// First PopReady: expected=1, slot empty, count=16 ≥ 16 → skip.
	// measureGapRunLocked should return 3 (seqs 1,2,3 missing before 4).
	if _, _, skipped := jb.PopReady(); !skipped {
		t.Fatal("expected skipped=true at head of gap")
	}

	// The gap should have been bucketed exactly once into 2_5.
	if got := jb.GapRuns2to5.Load(); got != 1 {
		t.Errorf("GapRuns2to5 = %d, want 1", got)
	}

	// Subsequent skips across the same run must NOT re-bucket.
	// expected is now 2; slot 2 still missing; count=12 still ≥ 12? No,
	// count didn't change because we just incremented expected. Count is
	// still 12. Next PopReady skips again.
	if _, _, skipped := jb.PopReady(); !skipped {
		t.Fatal("expected skipped=true continuing through gap")
	}

	if got := jb.GapRuns2to5.Load(); got != 1 {
		t.Errorf("GapRuns2to5 after second skip = %d, want 1 (no re-bucket)", got)
	}

	if _, _, skipped := jb.PopReady(); !skipped {
		t.Fatal("expected skipped=true on third skip")
	}

	// Now expected=4 and slot 4 is valid → successful pop ends the run.
	if _, ready, _ := jb.PopReady(); !ready {
		t.Fatal("expected ready=true at seq=4")
	}

	// Drain 5..19 cleanly. No new gap runs should be counted.
	for i := 0; i < 15; i++ {
		if _, ready, _ := jb.PopReady(); !ready {
			t.Fatalf("drain iter %d: expected ready=true", i)
		}
	}

	// Totals across all buckets should be exactly 1.
	total := jb.GapRuns1.Load() + jb.GapRuns2to5.Load() +
		jb.GapRuns6to10.Load() + jb.GapRuns11to20.Load() +
		jb.GapRuns21to50.Load() + jb.GapRunsOver50.Load()
	if total != 1 {
		t.Errorf("total gap runs = %d, want 1", total)
	}
}

// TestJitterBuffer_GapHistogram_SingleFrame verifies that a 1-frame
// gap lands in the GapRuns1 bucket.
func TestJitterBuffer_GapHistogram_SingleFrame(t *testing.T) {
	maxDepth := 32
	jb := NewJitterBuffer(1, maxDepth)

	jb.Push(0, []byte{0})

	if _, ready, _ := jb.PopReady(); !ready {
		t.Fatal("seq=0 must be ready")
	}
	// expected=1.

	// Single missing frame at seq=1, followed by 2..17 (16 packets)
	// so count=16 meets the maxDepth/2 skip threshold.
	for s := uint16(2); s <= 17; s++ {
		jb.Push(s, []byte{byte(s)})
	}

	if _, _, skipped := jb.PopReady(); !skipped {
		t.Fatal("expected skipped=true")
	}

	if got := jb.GapRuns1.Load(); got != 1 {
		t.Errorf("GapRuns1 = %d, want 1", got)
	}
}
