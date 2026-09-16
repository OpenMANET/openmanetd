package gpio

import (
	"fmt"

	"github.com/warthog618/go-gpiocdev"
)

// openHardware requests the five selector lines from the kernel with
// pull-up bias, both-edge events, and kernel-side debounce. handler is
// invoked from go-gpiocdev's event goroutine on every debounced edge;
// it only pokes a buffered channel, so it never blocks that goroutine.
func (s *Selector) openHardware(handler func()) (lineGroup, error) {
	lines, err := gpiocdev.RequestLines(SelectorChip, SelectorPins[:],
		gpiocdev.AsInput,
		gpiocdev.WithPullUp,
		gpiocdev.WithBothEdges,
		gpiocdev.WithDebounce(SelectorDebounce),
		gpiocdev.WithEventHandler(func(gpiocdev.LineEvent) { handler() }),
	)
	if err != nil {
		return nil, fmt.Errorf("gpio: gpiocdev.RequestLines: %w", err)
	}

	return lines, nil
}
