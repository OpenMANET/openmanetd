package talkgroup_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/openmanet/openmanetd/internal/comms/talkgroup"
)

func TestRegistry_AddNotifyRemove(t *testing.T) {
	r := talkgroup.NewRegistry(zerolog.Nop())

	var got []talkgroup.Event

	id := r.Add(func(ev talkgroup.Event) { got = append(got, ev) })
	require.NotZero(t, id)

	ev := talkgroup.Event{
		Kind: talkgroup.KindSelected, Channel: 3, Prev: 1,
		Send: true, Receive: true, Source: talkgroup.SourceRPC, At: time.Now(),
	}
	r.Notify(ev)
	require.Len(t, got, 1)
	assert.Equal(t, ev, got[0])

	r.Remove(id)
	r.Notify(ev)
	assert.Len(t, got, 1)
}

func TestRegistry_ListenerPanicIsolated(t *testing.T) {
	r := talkgroup.NewRegistry(zerolog.Nop())

	r.Add(func(talkgroup.Event) { panic("boom") })

	var called bool

	r.Add(func(talkgroup.Event) { called = true })

	assert.NotPanics(t, func() { r.Notify(talkgroup.Event{Kind: talkgroup.KindSelected}) })
	assert.True(t, called, "panicking listener must not block later listeners")
}

func TestRegistry_DropAccounting(t *testing.T) {
	r := talkgroup.NewRegistry(zerolog.Nop())
	assert.Zero(t, r.Dropped())
	r.NoteDropped()
	r.NoteDropped()
	assert.Equal(t, uint64(2), r.Dropped())
}

func TestRegistry_NilReceiverSafe(t *testing.T) {
	var r *talkgroup.Registry

	assert.NotPanics(t, func() { r.Notify(talkgroup.Event{}) })
	assert.Zero(t, r.Dropped())
}

// TestRegistry_ConcurrentAddRemoveNotify stress-tests the
// snapshot-under-lock contract: concurrent Add/Remove churn, Notify
// fan-out, and NoteDropped must not race or deadlock (-race gates it),
// and both counters must stay exact — the persistent listener sees every
// Notify because it registers before any fires, and Dropped is a plain
// atomic sum.
func TestRegistry_ConcurrentAddRemoveNotify(t *testing.T) {
	r := talkgroup.NewRegistry(zerolog.Nop())

	const (
		workers = 8
		iters   = 200
	)

	var persistentCalls atomic.Int64

	r.Add(func(talkgroup.Event) { persistentCalls.Add(1) })

	var wg sync.WaitGroup

	wg.Add(workers * 3)

	for range workers {
		go func() {
			defer wg.Done()

			for range iters {
				id := r.Add(func(talkgroup.Event) {})
				r.Remove(id)
			}
		}()

		go func() {
			defer wg.Done()

			for i := range iters {
				r.Notify(talkgroup.Event{Kind: talkgroup.KindSelected, Channel: i%5 + 1})
			}
		}()

		go func() {
			defer wg.Done()

			for range iters {
				r.NoteDropped()
			}
		}()
	}

	wg.Wait()

	assert.Equal(t, int64(workers*iters), persistentCalls.Load(),
		"persistent listener sees every Notify")
	assert.Equal(t, uint64(workers*iters), r.Dropped(),
		"drop counter is an exact concurrent sum")
}
