// Package gpio watches the Raven's 5-position talk group selector via
// the Linux GPIO character device (uAPI v2). Edge events arrive from
// the kernel with kernel-side debounce — no userspace polling loop.
package gpio

import (
	"context"
	"fmt"
	"sync/atomic"
	"time"

	"github.com/rs/zerolog"
)

const (
	// SelectorChip is the GPIO chip carrying the selector lines.
	SelectorChip = "gpiochip0"
	// SelectorDebounce is applied kernel-side via uAPI v2.
	SelectorDebounce = 10 * time.Millisecond
	// readErrorBreaker aborts the watch after this many consecutive
	// Values failures — mirrors the HID error-streak circuit breaker.
	readErrorBreaker = 10
)

// SelectorPins maps selector position → GPIO line offset on the Raven's
// BCM2711 pinctrl, confirmed against the Raven schematic 2026-09-05.
// Values are BCM GPIO numbers, which equal gpiochip0 line offsets on
// BCM2711. Position i selects talk group i+1: GPIO17 → 1, GPIO27 → 2,
// GPIO22 → 3, GPIO24 → 4, GPIO10 → 5.
//
// Lines are requested as inputs with pull-up bias and both-edge events.
// The switch common is tied to GND, so the selected position reads LOW
// (active) and every other position reads HIGH (inactive).
var SelectorPins = [5]int{10, 27, 22, 24, 17} //nolint:gochecknoglobals // hardware constant table

// lineGroup is the consumer-side view of the requested lines.
type lineGroup interface {
	Values(values []int) error
	Close() error
}

// Selector owns the watch goroutine for the 5 selector lines.
type Selector struct {
	Log zerolog.Logger
	// openFn overrides the hardware line request in tests. handler is
	// invoked on every debounced edge. Nil means real hardware.
	openFn func(handler func()) (lineGroup, error)

	transitions  atomic.Int64
	heldGlitches atomic.Int64
}

// Events requests the selector lines and returns a channel of 1-based
// talk group selections. The initial switch position is emitted first so
// the daemon boots onto it. The channel is latest-wins (depth 1, stale
// value evicted) and closes when ctx ends or the read breaker trips.
func (s *Selector) Events(ctx context.Context) (<-chan int, error) {
	open := s.openFn
	if open == nil {
		open = s.openHardware
	}

	// edge coalesces kernel edge callbacks: the watcher re-reads all
	// five lines per wakeup, so queued duplicates add nothing.
	edge := make(chan struct{}, 1)
	notify := func() {
		select {
		case edge <- struct{}{}:
		default:
		}
	}

	lines, err := open(notify)
	if err != nil {
		return nil, fmt.Errorf("gpio: request selector lines: %w", err)
	}

	s.Log.Debug().Str("chip", SelectorChip).Ints("pins", SelectorPins[:]).
		Dur("debounce", SelectorDebounce).
		Msg("gpio: selector lines requested (active-low, pull-up bias)")

	out := make(chan int, 1)

	go s.watch(ctx, lines, edge, out)

	return out, nil
}

func (s *Selector) watch(ctx context.Context, lines lineGroup, edge <-chan struct{}, out chan int) {
	defer close(out)
	defer func() {
		if err := lines.Close(); err != nil {
			s.Log.Warn().Err(err).Msg("gpio: close selector lines")
		}
	}()

	vals := make([]int, len(SelectorPins))

	var (
		last      int
		errStreak int
		booted    bool
	)

	read := func() bool {
		if err := lines.Values(vals); err != nil {
			errStreak++

			s.Log.Warn().Err(err).Int("streak", errStreak).Msg("gpio: read selector lines")

			return errStreak < readErrorBreaker
		}

		errStreak = 0

		ch := decodeChannel(vals)

		s.Log.Debug().Ints("pins", SelectorPins[:]).Ints("values", vals).
			Int("decoded", ch).Int("last", last).Bool("booted", booted).
			Msg("gpio: selector line read")

		if ch == 0 {
			// Rotary in transit or wiring fault: hold last selection.
			s.heldGlitches.Add(1)

			s.Log.Debug().Ints("values", vals).
				Msg("gpio: no single active selector line; holding last selection")

			if !booted {
				// Boot has no last selection to hold, so the daemon stays
				// on its configured channel. Say so once for field triage.
				s.Log.Warn().Ints("values", vals).
					Msg("gpio: no single active selector line at boot; keeping configured channel")
			}

			return true
		}

		if ch == last {
			s.Log.Debug().Int("channel", ch).
				Msg("gpio: selector unchanged; no event emitted")

			return true
		}

		s.Log.Debug().Int("from", last).Int("to", ch).Bool("boot", !booted).
			Msg("gpio: selector position changed; emitting talk group")

		last = ch

		s.transitions.Add(1)

		// Latest-wins delivery: never block on a slow consumer.
		for {
			select {
			case out <- ch:
				return true
			default:
				select {
				case <-out:
				default:
				}
			}
		}
	}

	if !read() { // boot onto the physical switch position
		return
	}

	booted = true

	for {
		select {
		case <-ctx.Done():
			return
		case <-edge:
			s.Log.Debug().Msg("gpio: selector edge wakeup")

			if !read() {
				s.Log.Error().Msg("gpio: selector read breaker tripped; selector disabled")

				return
			}
		}
	}
}

// decodeChannel returns the 1-based selected channel when exactly one
// line is active (low), else 0.
func decodeChannel(vals []int) int {
	sel, active := 0, 0

	for i, v := range vals {
		if v == 0 {
			sel = i + 1
			active++
		}
	}

	if active != 1 {
		return 0
	}

	return sel
}

// SelectorSnapshot is the selector's instrumentation section.
type SelectorSnapshot struct {
	// Transitions counts accepted selection changes.
	Transitions int64 `json:"transitions"`
	// HeldGlitches counts edge wakeups where zero or multiple pins were
	// active and the previous selection was held.
	HeldGlitches int64 `json:"held_glitches"`
}

// Snapshot fills dst. Nil-receiver safe, zero-alloc.
func (s *Selector) Snapshot(dst *SelectorSnapshot) {
	if dst == nil {
		return
	}

	if s == nil {
		*dst = SelectorSnapshot{}

		return
	}

	dst.Transitions = s.transitions.Load()
	dst.HeldGlitches = s.heldGlitches.Load()
}
