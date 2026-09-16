package webaudio

import (
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

// recordingSink captures every payload handed to InjectTxFrame so tests can
// assert on the number of forwarded frames and their contents. It is
// mutex-protected so it can be exercised from parallel tests in the future.
type recordingSink struct {
	mu       sync.Mutex
	payloads [][]byte
}

func (s *recordingSink) send(p []byte) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cp := make([]byte, len(p))
	copy(cp, p)
	s.payloads = append(s.payloads, cp)
}

func (s *recordingSink) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return len(s.payloads)
}

func TestBridge_InjectTxFrame_ForwardsToSendFn(t *testing.T) {
	sink := &recordingSink{}
	bridge := NewBridge(zerolog.Nop(), sink.send)

	payload := []byte{0xDE, 0xAD, 0xBE, 0xEF}
	bridge.InjectTxFrame(payload)

	if got := sink.count(); got != 1 {
		t.Errorf("expected 1 forwarded payload, got %d", got)
	}
}

func TestBridge_InjectTxFrame_NilSendIsNoop(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), nil)

	// Must not panic.
	bridge.InjectTxFrame([]byte{0x01})
}

func TestBridge_PushRxFrame_Delivered(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	data := []byte{0xCA, 0xFE}
	bridge.PushRxFrame(1, data)

	select {
	case got := <-bridge.RxFrames():
		if d := got.Data(); len(d) != 2 || d[0] != 0xCA || d[1] != 0xFE {
			t.Errorf("unexpected frame data: %v", d)
		}

		got.Release()
	case <-time.After(200 * time.Millisecond):
		t.Error("timed out waiting for RX frame")
	}
}

// TestBridge_PushRxFrame_CarriesChannel pins the talk group identity
// contract: the channel byte handed to PushRxFrame must ride the frame to
// the consumer so the RPC layer can tell the browser which talk group the
// audio belongs to. The zero Frame reports channel 0 (unknown).
func TestBridge_PushRxFrame_CarriesChannel(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	bridge.PushRxFrame(2, []byte{0xCA})
	bridge.PushRxFrame(5, []byte{0xFE})

	for _, want := range []byte{2, 5} {
		select {
		case got := <-bridge.RxFrames():
			if ch := got.Channel(); ch != want {
				t.Errorf("frame channel: got %d, want %d", ch, want)
			}

			got.Release()
		case <-time.After(200 * time.Millisecond):
			t.Fatal("timed out waiting for RX frame")
		}
	}

	var zero Frame
	if ch := zero.Channel(); ch != 0 {
		t.Errorf("zero Frame channel: got %d, want 0", ch)
	}
}

// TestBridge_PushRxFrame_CopiesPayload pins the ownership contract: the
// caller's payload may be reused (it aliases a jitter-pool buffer) the
// moment PushRxFrame returns, so the frame in the channel must hold its
// own copy.
func TestBridge_PushRxFrame_CopiesPayload(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	data := []byte{0x11, 0x22}
	bridge.PushRxFrame(1, data)

	// Caller reuses its buffer immediately.
	data[0] = 0xEE
	data[1] = 0xFF

	got := <-bridge.RxFrames()
	defer got.Release()

	if d := got.Data(); d[0] != 0x11 || d[1] != 0x22 {
		t.Errorf("frame must not alias the caller's buffer; got %v", d)
	}
}

func TestBridge_PushRxFrame_DropsOnFull(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	// Fill the channel.
	for range cap(bridge.rxFrames) {
		bridge.PushRxFrame(1, []byte{0x00})
	}

	// This push must not block.
	done := make(chan struct{})

	go func() {
		bridge.PushRxFrame(1, []byte{0xFF})
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(200 * time.Millisecond):
		t.Error("PushRxFrame blocked on full channel")
	}
}

// TestBridge_PushRxFrame_OversizedCountsBoth pins the counter contract on
// the defensive oversize guard: RxPushIn counts every PushRxFrame
// invocation (as the snapshot doc states), so a rejected oversized payload
// must increment both counters — otherwise rx_push_drop / rx_push_in could
// exceed 1 and mislead triage.
func TestBridge_PushRxFrame_OversizedCountsBoth(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	bridge.PushRxFrame(1, make([]byte, maxFrameBytes+1))

	if got := bridge.RxPushIn.Load(); got != 1 {
		t.Errorf("RxPushIn: got %d, want 1 (every invocation counts)", got)
	}

	if got := bridge.RxPushDrop.Load(); got != 1 {
		t.Errorf("RxPushDrop: got %d, want 1 (oversized payload rejected)", got)
	}

	select {
	case f := <-bridge.rxFrames:
		t.Errorf("oversized payload must not be enqueued; got %v", f.Data())
	default:
	}
}

// TestBridge_ChannelDepth pins the RX buffer at ~200 ms of slack (10
// frames at 50 fps). The previous 1-second depth meant a stalled browser
// consumer resumed a full second behind live audio and stayed there for
// the rest of a continuous stream.
func TestBridge_ChannelDepth(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	if got := cap(bridge.rxFrames); got != 10 {
		t.Errorf("rxFrames depth: got %d, want 10 (~200 ms at 50 fps)", got)
	}
}

// TestBridge_PushRxFrame_DropsOldestOnFull pins the drop-oldest policy:
// when the channel is full the oldest frame is evicted so a stalled-then-
// resumed consumer hears the freshest audio, not second-old backlog. For
// PTT voice, fresh beats stale.
func TestBridge_PushRxFrame_DropsOldestOnFull(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	depth := cap(bridge.rxFrames)

	// Frames 0..depth fill the channel and then force one eviction.
	for i := range depth + 1 {
		bridge.PushRxFrame(1, []byte{byte(i)})
	}

	if got := bridge.RxPushDrop.Load(); got != 1 {
		t.Errorf("RxPushDrop: got %d, want 1 (the evicted oldest frame)", got)
	}

	// The survivor set must be frames 1..depth: oldest evicted, newest kept.
	for i := 1; i <= depth; i++ {
		select {
		case f := <-bridge.rxFrames:
			if d := f.Data(); len(d) != 1 || d[0] != byte(i) {
				t.Errorf("position %d: got frame %v, want [%d]", i, d, i)
			}

			f.Release()
		default:
			t.Fatalf("channel exhausted at position %d; want %d frames", i, depth)
		}
	}
}

// TestBridge_AddConsumer_FlushesStaleFrames pins the attach-time flush:
// frames queued when the last consumer detached (possibly hours ago) must
// not play to a newly attached browser. The 0→1 consumer transition
// discards and recycles whatever is queued.
func TestBridge_AddConsumer_FlushesStaleFrames(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	bridge.AddConsumer()
	bridge.PushRxFrame(1, []byte{0x01})
	bridge.PushRxFrame(1, []byte{0x02})
	bridge.RemoveConsumer() // consumer detached with frames still queued

	bridge.AddConsumer() // hours later, a new browser attaches

	select {
	case f := <-bridge.rxFrames:
		t.Errorf("stale frame survived consumer attach: %v", f.Data())
	default:
	}

	// A second consumer attaching while the first is still active must NOT
	// flush frames the active consumer is about to read.
	bridge.PushRxFrame(1, []byte{0x03})
	bridge.AddConsumer()

	select {
	case f := <-bridge.rxFrames:
		if d := f.Data(); len(d) != 1 || d[0] != 0x03 {
			t.Errorf("unexpected frame after second attach: %v", d)
		}

		f.Release()
	default:
		t.Error("frame flushed by a non-first consumer attach")
	}
}

// TestBridge_PushRxFrame_ZeroAlloc pins the pooled hand-off: a full
// push→receive→release cycle must not allocate once the pool is warm.
func TestBridge_PushRxFrame_ZeroAlloc(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	data := make([]byte, 100)

	// Warm the pool.
	bridge.PushRxFrame(1, data)
	f := <-bridge.RxFrames()
	f.Release()

	allocs := testing.AllocsPerRun(100, func() {
		bridge.PushRxFrame(1, data)

		got := <-bridge.RxFrames()
		got.Release()
	})

	if allocs != 0 {
		t.Errorf("push/receive/release cycle allocated %.1f/op; want 0", allocs)
	}
}

// TestBridge_PushRxFrame_DropRecyclesBuffer pins the drop branch's pool
// hygiene: a frame dropped because the channel is full must return its
// pooled buffer, so sustained drops do not allocate either.
func TestBridge_PushRxFrame_DropRecyclesBuffer(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	data := make([]byte, 100)

	for range cap(bridge.rxFrames) {
		bridge.PushRxFrame(1, data)
	}

	allocs := testing.AllocsPerRun(100, func() {
		bridge.PushRxFrame(1, data)
	})

	if allocs != 0 {
		t.Errorf("drop path allocated %.1f/op; want 0 (buffer must return to the pool)", allocs)
	}
}

// TestFrame_ZeroValueReleaseIsNoop keeps Release safe on the zero Frame,
// matching the nil-safety of the rest of the Bridge API.
func TestFrame_ZeroValueReleaseIsNoop(t *testing.T) {
	var f Frame

	f.Release()

	if d := f.Data(); len(d) != 0 {
		t.Errorf("zero Frame Data should be empty; got %v", d)
	}
}

func TestBridge_ConsumerCount(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	if bridge.HasConsumer() {
		t.Error("fresh bridge must report no consumer")
	}

	bridge.AddConsumer()

	if !bridge.HasConsumer() {
		t.Error("HasConsumer should be true after AddConsumer")
	}

	// A second concurrent stream keeps the bridge active until both detach.
	bridge.AddConsumer()
	bridge.RemoveConsumer()

	if !bridge.HasConsumer() {
		t.Error("HasConsumer should stay true while one consumer remains")
	}

	bridge.RemoveConsumer()

	if bridge.HasConsumer() {
		t.Error("HasConsumer should be false after the last RemoveConsumer")
	}
}

func TestBridge_ConsumerCount_NilBridge(t *testing.T) {
	var bridge *Bridge

	// All must be nil-safe no-ops, matching the rest of the Bridge API.
	bridge.AddConsumer()
	bridge.RemoveConsumer()

	if bridge.HasConsumer() {
		t.Error("nil bridge must report no consumer")
	}
}

func TestBridge_RxFrames_ReturnsReadOnlyChannel(t *testing.T) {
	bridge := NewBridge(zerolog.Nop(), func(_ []byte) {})

	ch := bridge.RxFrames()
	if ch == nil {
		t.Error("RxFrames() returned nil")
	}

	// Verify it is a receive-only channel (compile-time check via type).
	var _ <-chan Frame = ch
}
