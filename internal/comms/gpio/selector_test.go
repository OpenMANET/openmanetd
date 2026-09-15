package gpio

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeLines is a hand-rolled lineGroup: tests set vals and fire the
// captured edge handler.
type fakeLines struct {
	mu      sync.Mutex
	vals    [5]int
	valsErr error
	closed  bool
	// valuesCalled, when non-nil, receives one token per Values call so
	// tests can serialize edge firings against the watch goroutine's
	// reads without sleeping. Buffered; send is non-blocking.
	valuesCalled chan struct{}
}

func (f *fakeLines) Values(out []int) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.valuesCalled != nil {
		select {
		case f.valuesCalled <- struct{}{}:
		default:
		}
	}

	if f.valsErr != nil {
		return f.valsErr
	}

	copy(out, f.vals[:])

	return nil
}

func (f *fakeLines) Close() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.closed = true

	return nil
}

func (f *fakeLines) set(vals [5]int) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.vals = vals
}

func (f *fakeLines) setErr(err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.valsErr = err
}

func newFakeSelector(initial [5]int) (*Selector, *fakeLines, *func()) {
	fl := &fakeLines{vals: initial}

	var handler func()

	s := &Selector{
		Log: zerolog.Nop(),
		openFn: func(h func()) (lineGroup, error) {
			handler = h

			return fl, nil
		},
	}

	return s, fl, &handler
}

func recvChannel(t *testing.T, ch <-chan int) int {
	t.Helper()

	select {
	case v := <-ch:
		return v
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for selector event")

		return 0
	}
}

func TestSelector_InitialStateApplied(t *testing.T) {
	// Position 3 active (line index 2 low); all others high (pull-up).
	s, _, _ := newFakeSelector([5]int{1, 1, 0, 1, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := s.Events(ctx)
	require.NoError(t, err)

	assert.Equal(t, 3, recvChannel(t, events))
}

func TestSelector_EdgeSelectsChannel(t *testing.T) {
	s, fl, handler := newFakeSelector([5]int{0, 1, 1, 1, 1})

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := s.Events(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recvChannel(t, events))

	fl.set([5]int{1, 1, 1, 1, 0})
	(*handler)()

	assert.Equal(t, 5, recvChannel(t, events))
}

func TestSelector_GlitchHoldsLastSelection(t *testing.T) {
	tests := []struct {
		name string
		vals [5]int
	}{
		{name: "rotary in transit, none active", vals: [5]int{1, 1, 1, 1, 1}},
		{name: "two active, wiring fault", vals: [5]int{0, 0, 1, 1, 1}},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s, fl, handler := newFakeSelector([5]int{1, 0, 1, 1, 1})

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			events, err := s.Events(ctx)
			require.NoError(t, err)
			assert.Equal(t, 2, recvChannel(t, events))

			fl.set(tc.vals)
			(*handler)()

			select {
			case v := <-events:
				t.Fatalf("glitch emitted %d; want hold", v)
			case <-time.After(50 * time.Millisecond):
			}

			var snap SelectorSnapshot

			s.Snapshot(&snap)
			assert.Equal(t, int64(1), snap.HeldGlitches)
		})
	}
}

func TestSelector_CtxCancelClosesAndReleasesLines(t *testing.T) {
	s, fl, _ := newFakeSelector([5]int{0, 1, 1, 1, 1})

	ctx, cancel := context.WithCancel(context.Background())

	events, err := s.Events(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recvChannel(t, events))

	cancel()

	for range events { //nolint:revive // drain until close
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	assert.True(t, fl.closed)
}

func TestSelector_SnapshotNilSafe(t *testing.T) {
	var s *Selector

	var snap SelectorSnapshot

	assert.NotPanics(t, func() { s.Snapshot(&snap) })
}

// TestSelector_ReadErrorBreakerClosesAndReleases pins the error breaker:
// after readErrorBreaker consecutive Values failures the watch goroutine
// closes the events channel and releases the lines. Each edge firing is
// serialized against the watcher's read via valuesCalled, so edges never
// coalesce and every firing produces exactly one failed read.
func TestSelector_ReadErrorBreakerClosesAndReleases(t *testing.T) {
	s, fl, handler := newFakeSelector([5]int{0, 1, 1, 1, 1})
	fl.valuesCalled = make(chan struct{}, readErrorBreaker+1)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := s.Events(ctx)
	require.NoError(t, err)
	assert.Equal(t, 1, recvChannel(t, events))
	waitValuesCall(t, fl) // the successful boot read

	fl.setErr(errors.New("ioctl: line request revoked"))

	for range readErrorBreaker {
		(*handler)()
		waitValuesCall(t, fl)
	}

	select {
	case v, ok := <-events:
		require.False(t, ok, "breaker must close the channel, got value %d", v)
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for events channel to close")
	}

	fl.mu.Lock()
	defer fl.mu.Unlock()

	assert.True(t, fl.closed, "lines released after breaker trip")
}

func waitValuesCall(t *testing.T, fl *fakeLines) {
	t.Helper()

	select {
	case <-fl.valuesCalled:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a Values read")
	}
}

// TestSelectorPins_RavenMapping pins the confirmed Raven wiring: BCM
// GPIO numbers (gpiochip0 line offsets on BCM2711), position i selecting
// talk group i+1. A regression to the pre-schematic placeholders or a
// duplicated line would silently break the switch in the field.
func TestSelectorPins_RavenMapping(t *testing.T) {
	assert.Equal(t, [5]int{17, 27, 22, 24, 10}, SelectorPins)

	seen := make(map[int]struct{}, len(SelectorPins))

	for i, pin := range SelectorPins {
		assert.GreaterOrEqual(t, pin, 0, "position %d", i+1)
		assert.LessOrEqual(t, pin, 27, "position %d: BCM2711 header exposes GPIO0-27", i+1)

		_, dup := seen[pin]
		assert.False(t, dup, "GPIO%d wired to two positions", pin)

		seen[pin] = struct{}{}
	}
}

// logCapture is a goroutine-safe zerolog sink. wrote receives one token
// per Write so tests can wait for a log line without sleeping.
type logCapture struct {
	mu    sync.Mutex
	buf   strings.Builder
	n     int
	wrote chan struct{}
}

func (l *logCapture) Write(p []byte) (int, error) {
	l.mu.Lock()
	l.buf.Write(p)
	l.n++
	l.mu.Unlock()

	select {
	case l.wrote <- struct{}{}:
	default:
	}

	return len(p), nil
}

func (l *logCapture) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.buf.String()
}

func (l *logCapture) writes() int {
	l.mu.Lock()
	defer l.mu.Unlock()

	return l.n
}

func (l *logCapture) wait(t *testing.T) {
	t.Helper()

	select {
	case <-l.wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for a log line")
	}
}

// TestSelector_BootGlitchWarnsOnce pins the boot-time diagnostic: when the
// first read finds no single low line (switch between detents, harness
// unplugged) the watcher warns once with the raw values and emits nothing,
// so the daemon keeps its configured channel. Later in-transit glitches
// stay silent at warn level (counter only), and a subsequent clean position
// still flows.
func TestSelector_BootGlitchWarnsOnce(t *testing.T) {
	s, fl, handler := newFakeSelector([5]int{1, 1, 1, 1, 1})
	fl.valuesCalled = make(chan struct{}, 8)

	logs := &logCapture{wrote: make(chan struct{}, 8)}
	// Warn level: the contract under test is the warn-only diagnostic,
	// not the debug trace emitted on every read.
	s.Log = zerolog.New(logs).Level(zerolog.WarnLevel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := s.Events(ctx)
	require.NoError(t, err)
	waitValuesCall(t, fl)

	logs.wait(t)
	assert.Contains(t, logs.String(), `"level":"warn"`)
	assert.Contains(t, logs.String(), "at boot")
	assert.Contains(t, logs.String(), `"values":[1,1,1,1,1]`)

	select {
	case v := <-events:
		t.Fatalf("boot glitch emitted %d; want no selection", v)
	case <-time.After(50 * time.Millisecond):
	}

	// A later wiring glitch is counted, not logged.
	fl.set([5]int{0, 0, 1, 1, 1})
	(*handler)()
	waitValuesCall(t, fl)

	select {
	case <-logs.wrote:
		t.Fatalf("edge glitch logged: %s", logs.String())
	case <-time.After(50 * time.Millisecond):
	}

	// A clean position still selects.
	fl.set([5]int{1, 1, 0, 1, 1})
	(*handler)()

	assert.Equal(t, 3, recvChannel(t, events))
	assert.Equal(t, 1, logs.writes(), "only the boot read warns")

	var snap SelectorSnapshot

	s.Snapshot(&snap)
	assert.Equal(t, int64(2), snap.HeldGlitches)
}

// TestSelector_DebugTracesReads pins the field-triage trace: at debug level
// every read reports the raw line values and the decoded channel, and a
// position change reports the from/to transition. Without this an operator
// cannot tell a mis-wired rotary switch from a decode bug.
func TestSelector_DebugTracesReads(t *testing.T) {
	s, fl, handler := newFakeSelector([5]int{0, 1, 1, 1, 1})
	fl.valuesCalled = make(chan struct{}, 8)

	logs := &logCapture{wrote: make(chan struct{}, 16)}
	s.Log = zerolog.New(logs).Level(zerolog.DebugLevel)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	events, err := s.Events(ctx)
	require.NoError(t, err)

	assert.Equal(t, 1, recvChannel(t, events))
	waitValuesCall(t, fl)

	fl.set([5]int{1, 1, 0, 1, 1})
	(*handler)()

	assert.Equal(t, 3, recvChannel(t, events))

	out := logs.String()
	assert.Contains(t, out, `"pins":[17,27,22,24,10]`)
	assert.Contains(t, out, `"values":[0,1,1,1,1]`)
	assert.Contains(t, out, `"values":[1,1,0,1,1]`)
	assert.Contains(t, out, `"decoded":3`)
	assert.Contains(t, out, `"from":1,"to":3`)
	assert.Contains(t, out, "gpio: selector edge wakeup")
}
