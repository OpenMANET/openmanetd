package control

import (
	"context"
	"testing"
	"time"

	"github.com/rs/zerolog"
)

type heldFallbackSource struct{ events chan PTTEvent }

func (s *heldFallbackSource) Events(context.Context) <-chan PTTEvent { return s.events }

func TestFallbackJoinsAfterCanceledForward(t *testing.T) {
	events := make(chan PTTEvent)
	source := &bs22EventSource{xeventFactory: func(zerolog.Logger) EventSource { return &heldFallbackSource{events: events} }}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() { defer close(done); source.forwardBlueALSAFallback(ctx, make(chan PTTEvent)) }()
	t.Cleanup(func() { cancel(); close(events); <-done })
	events <- PTTDown // Forwarding is now blocked on the unconsumed output.
	cancel()
	select {
	case events <- PTTUp: // Cleanup must drain the source until it has stopped.
	case <-done:
		t.Fatal("returned before joining fallback")
	case <-time.After(time.Second):
		t.Fatal("fallback cleanup did not drain")
	}
}
