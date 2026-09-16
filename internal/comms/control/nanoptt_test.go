package control

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	evdev "github.com/gvalkov/golang-evdev"
	"github.com/rs/zerolog"
)

// fakeEvdev drives NanoPTTSource through its readOne/closeDev seams. Reads
// return queued events, then block until Close unblocks them with an error
// — mirroring a real evdev fd, where a blocked read(2) only returns once
// the file is closed.
type fakeEvdev struct {
	mu       sync.Mutex
	events   []*evdev.InputEvent
	closed   chan struct{}
	once     sync.Once
	reads    atomic.Int64
	closes   atomic.Int64
	readErr  error // when set, every read fails with this error
	blockErr error // error returned when Close unblocks a pending read
}

func newFakeEvdev(events ...*evdev.InputEvent) *fakeEvdev {
	return &fakeEvdev{
		events:   events,
		closed:   make(chan struct{}),
		blockErr: errors.New("file already closed"),
	}
}

func (f *fakeEvdev) readOne() (*evdev.InputEvent, error) {
	f.reads.Add(1)

	if f.readErr != nil {
		return nil, f.readErr
	}

	f.mu.Lock()
	if len(f.events) > 0 {
		ev := f.events[0]
		f.events = f.events[1:]
		f.mu.Unlock()

		return ev, nil
	}
	f.mu.Unlock()

	<-f.closed

	return nil, f.blockErr
}

func (f *fakeEvdev) close() error {
	f.closes.Add(1)
	f.once.Do(func() { close(f.closed) })

	return nil
}

func newTestNanoPTT(f *fakeEvdev, pttKey string) *NanoPTTSource {
	return &NanoPTTSource{
		log:      zerolog.Nop(),
		pttKey:   pttKey,
		readOne:  f.readOne,
		closeDev: f.close,
	}
}

func keyEvent(code uint16, value int32) *evdev.InputEvent {
	return &evdev.InputEvent{Type: evdev.EV_KEY, Code: code, Value: value}
}

// TestNanoPTTSource_EmitsToggleOnPress pins the existing key handling:
// a press (value 1) of the configured key emits PTTToggle; the release
// (value 0) emits nothing.
func TestNanoPTTSource_EmitsToggleOnPress(t *testing.T) {
	f := newFakeEvdev(keyEvent(42, 1), keyEvent(42, 0))
	s := newTestNanoPTT(f, "42")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := s.Events(ctx)

	select {
	case ev := <-ch:
		if ev != PTTToggle {
			t.Errorf("got event %v, want PTTToggle", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("no PTTToggle emitted for key press")
	}

	// The release must not emit; the channel stays quiet until cancel.
	select {
	case ev, ok := <-ch:
		if ok {
			t.Errorf("unexpected event %v after key release", ev)
		}
	case <-time.After(50 * time.Millisecond):
	}
}

// TestNanoPTTSource_IgnoresOtherKeys pins the key filter: presses of a
// non-matching code emit nothing, and "any" matches every code.
func TestNanoPTTSource_IgnoresOtherKeys(t *testing.T) {
	f := newFakeEvdev(keyEvent(7, 1))
	s := newTestNanoPTT(f, "42")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := s.Events(ctx)

	select {
	case ev, ok := <-ch:
		if ok {
			t.Errorf("unexpected event %v for non-matching key", ev)
		}
	case <-time.After(50 * time.Millisecond):
	}

	fAny := newFakeEvdev(keyEvent(7, 1))
	sAny := newTestNanoPTT(fAny, "any")

	chAny := sAny.Events(ctx)

	select {
	case ev := <-chAny:
		if ev != PTTToggle {
			t.Errorf("got event %v, want PTTToggle for pttKey=any", ev)
		}
	case <-time.After(time.Second):
		t.Fatal("pttKey=any did not match key press")
	}
}

// TestNanoPTTSource_CancelClosesDeviceAndChannel pins the leak fix: the
// read goroutine blocks in read(2) between key events, so cancellation
// must close the device (unblocking the read) and the event channel must
// close — otherwise every disable/enable cycle leaks a goroutine and an
// open evdev fd.
func TestNanoPTTSource_CancelClosesDeviceAndChannel(t *testing.T) {
	f := newFakeEvdev() // no events: first read blocks immediately
	s := newTestNanoPTT(f, "any")

	ctx, cancel := context.WithCancel(context.Background())
	ch := s.Events(ctx)

	// Let the goroutine reach the blocking read.
	time.Sleep(20 * time.Millisecond)

	cancel()

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel close, got an event")
		}
	case <-time.After(time.Second):
		t.Fatal("event channel did not close after cancel; blocked read was never unblocked")
	}

	if f.closes.Load() == 0 {
		t.Error("device was not closed on cancel (fd leak)")
	}
}

// TestNanoPTTSource_TerminalOnPersistentReadError pins the busy-spin fix:
// a permanently failing device (e.g. dongle unplugged, read returns
// ENODEV instantly forever) must terminate the source after a bounded
// number of attempts instead of spinning the goroutine at 100% CPU.
func TestNanoPTTSource_TerminalOnPersistentReadError(t *testing.T) {
	f := newFakeEvdev()
	f.readErr = errors.New("no such device")
	s := newTestNanoPTT(f, "any")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	ch := s.Events(ctx)

	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected channel close, got an event")
		}
	case <-time.After(time.Second):
		t.Fatal("source did not terminate on persistent read errors")
	}

	if got := f.reads.Load(); got > 10 {
		t.Errorf("read attempted %d times before terminating; want a small bounded count", got)
	}
}
