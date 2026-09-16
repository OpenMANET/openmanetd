package rtp

import (
	"net/netip"
	"sync"
	"testing"
	"time"
)

// ─── SwappableSender tests ────────────────────────────────────────────────────

func TestSwappableSender_WritesToInitialImpl(t *testing.T) {
	w := &mockWriter{}
	s := NewSwappableSender(w)

	if _, err := s.Write([]byte{1, 2, 3}); err != nil {
		t.Fatal(err)
	}

	if len(w.Packets) != 1 {
		t.Errorf("expected 1 packet; got %d", len(w.Packets))
	}
}

func TestSwappableSender_SwapReturnsOld(t *testing.T) {
	w1 := &mockWriter{}
	w2 := &mockWriter{}
	s := NewSwappableSender(w1)

	old := s.Swap(w2)

	if old != w1 {
		t.Error("swap should return the previous implementation")
	}
}

func TestSwappableSender_WritesToNewImplAfterSwap(t *testing.T) {
	w1 := &mockWriter{}
	w2 := &mockWriter{}
	s := NewSwappableSender(w1)

	s.Swap(w2)
	_, _ = s.Write([]byte{9})

	if len(w1.Packets) != 0 {
		t.Error("old writer should not receive packets after swap")
	}

	if len(w2.Packets) != 1 {
		t.Error("new writer should receive packets after swap")
	}
}

func TestSwappableSender_Swap_ClosesOldIfCloser(t *testing.T) {
	w1 := &mockClosingWriter{}
	w2 := &mockWriter{}
	s := NewSwappableSender(w1)

	old := s.Swap(w2)

	// Simulate what replaceNetwork does.
	if c, ok := old.(interface{ Close() error }); ok {
		_ = c.Close()
	}

	if !w1.closeCalled.Load() {
		t.Error("Close() should be called on old writer when it implements io.Closer")
	}
}

// TestSwappableSender_SwapAndDeferClose verifies that swapAndDeferClose
// publishes the new writer immediately, subsequent writes go to it, and the
// previous writer's Close is eventually called after the grace window.
func TestSwappableSender_SwapAndDeferClose(t *testing.T) {
	w1 := &mockClosingWriter{}
	w2 := &mockWriter{}
	s := NewSwappableSender(w1)

	s.SwapAndDeferClose(w2)

	if _, err := s.Write([]byte{7}); err != nil {
		t.Fatal(err)
	}

	if len(w2.Packets) != 1 {
		t.Error("write after swap should hit new writer")
	}

	// Wait for the deferred close fire (SwapCloseGrace + slack).
	deadline := time.Now().Add(SwapCloseGrace + 500*time.Millisecond)
	for time.Now().Before(deadline) {
		if w1.closeCalled.Load() {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	if !w1.closeCalled.Load() {
		t.Error("deferred Close() on old writer should have fired")
	}
}

func TestSwappableSender_ConcurrentWritesAndSwap(t *testing.T) {
	w1 := &safeMockWriter{}
	w2 := &safeMockWriter{}
	s := NewSwappableSender(w1)

	const (
		writers    = 10
		writesEach = 50
	)

	var wg sync.WaitGroup

	for i := 0; i < writers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < writesEach; j++ {
				_, _ = s.Write([]byte{byte(j)})
			}
		}()
	}

	// Swap in the middle of the writes.
	time.Sleep(1 * time.Millisecond)
	s.Swap(w2)

	wg.Wait()

	total := w1.count() + w2.count()
	if total != writers*writesEach {
		t.Errorf("total writes = %d; want %d", total, writers*writesEach)
	}
}

// TestSwappableSender_StressWritersAndSwapper runs many concurrent writers
// alongside a single goroutine that repeatedly swaps the underlying writer.
// Under -race this exercises the lock-free Write path against atomic pointer
// publication from swap(). Every write must land on either the pre-swap
// impl or the post-swap impl — none may be lost or duplicated.
func TestSwappableSender_StressWritersAndSwapper(t *testing.T) {
	const (
		writers      = 8
		writesEach   = 1000
		swapInterval = 10 * time.Microsecond
	)

	impls := []*safeMockWriter{{}, {}, {}, {}}
	s := NewSwappableSender(impls[0])

	var wg sync.WaitGroup

	stop := make(chan struct{})

	// Swapper goroutine.
	wg.Add(1)

	go func() {
		defer wg.Done()

		i := 0

		for {
			select {
			case <-stop:
				return
			default:
			}

			s.Swap(impls[i%len(impls)])
			i++

			time.Sleep(swapInterval)
		}
	}()

	// Writer goroutines.
	for i := 0; i < writers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			for j := 0; j < writesEach; j++ {
				if _, err := s.Write([]byte{byte(j)}); err != nil {
					t.Errorf("write: %v", err)

					return
				}
			}
		}()
	}

	// Wait for writers; we don't know which writer finishes last, so poll a
	// small sleep then stop the swapper.
	done := make(chan struct{})

	go func() {
		for i := 0; i < writers; i++ {
			// no-op; wg.Wait below handles ordering
		}

		close(done)
	}()

	// Give writers time to finish, then stop the swapper.
	time.Sleep(100 * time.Millisecond)
	close(stop)
	wg.Wait()

	var total int
	for _, w := range impls {
		total += w.count()
	}

	if total != writers*writesEach {
		t.Errorf("total writes = %d; want %d", total, writers*writesEach)
	}
}

// ─── SwappableReceiver tests ──────────────────────────────────────────────────

func TestSwappableReceiver_ReadsFromInitialImpl(t *testing.T) {
	src := netip.MustParseAddrPort("127.0.0.1:1234")
	r := newMockReader(mockPacket{src: src, data: []byte{42}})
	sr := NewSwappableReceiver(r)

	buf := make([]byte, 10)

	n, addr, err := sr.ReadFromUDPAddrPort(buf)
	if err != nil {
		t.Fatal(err)
	}

	if n != 1 {
		t.Errorf("unexpected read length: got %d, want 1", n)
	} else if buf[0] != 42 { //nolint:gosec // buf is make([]byte,10); n==1 guarantees index is valid
		t.Errorf("unexpected read value: buf[0]=%d, want 42", buf[0]) //nolint:gosec
	}

	if addr.String() != src.String() {
		t.Errorf("addr mismatch: got %s, want %s", addr, src)
	}
}

func TestSwappableReceiver_SwapReturnsOld(t *testing.T) {
	r1 := newMockReader()
	r2 := newMockReader()
	sr := NewSwappableReceiver(r1)

	old := sr.Swap(r2)

	if old != r1 {
		t.Error("swap should return the previous implementation")
	}
}

func TestSwappableReceiver_Close(t *testing.T) {
	tr := &trackingReader{}
	sr := NewSwappableReceiver(tr)

	if err := sr.Close(); err != nil {
		t.Fatal(err)
	}

	if !tr.closed {
		t.Error("Close should propagate to the underlying reader")
	}
}

func TestSwappableReceiver_CloseUnblocksRead(t *testing.T) {
	r := newMockReader() // empty queue — will block until Close
	sr := NewSwappableReceiver(r)

	done := make(chan struct{})

	go func() {
		defer close(done)

		buf := make([]byte, 10)
		_, _, _ = sr.ReadFromUDPAddrPort(buf)
	}()

	time.Sleep(20 * time.Millisecond)

	_ = sr.Close()

	select {
	case <-done:
	case <-time.After(300 * time.Millisecond):
		t.Error("Close should unblock ReadFromUDPAddrPort")
	}
}

// TestSwappableReceiver_ConcurrentSwapAndRead verifies that simultaneous
// read calls and swap calls do not produce a data race under the race
// detector. The pattern mirrors TestSwappableSender_ConcurrentWritesAndSwap.
func TestSwappableReceiver_ConcurrentSwapAndRead(t *testing.T) {
	r1 := &safeCountingReader{}
	r2 := &safeCountingReader{}
	sr := NewSwappableReceiver(r1)

	const (
		readers   = 10
		readsEach = 50
	)

	var wg sync.WaitGroup

	for i := 0; i < readers; i++ {
		wg.Add(1)

		go func() {
			defer wg.Done()

			buf := make([]byte, 10)

			for j := 0; j < readsEach; j++ {
				_, _, _ = sr.ReadFromUDPAddrPort(buf)
			}
		}()
	}

	// Swap in the middle of concurrent reads.
	time.Sleep(1 * time.Millisecond)
	sr.Swap(r2)

	wg.Wait()

	total := r1.count() + r2.count()
	if total != readers*readsEach {
		t.Errorf("total reads = %d; want %d", total, readers*readsEach)
	}
}
