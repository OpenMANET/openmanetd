package talkgroup

import (
	"sync"
	"sync/atomic"

	"github.com/rs/zerolog"
)

// Registry is an ID-keyed listener registry following the BLOS
// status-worker pattern: snapshot-under-lock, fire-unlocked, per-listener
// recover, drop accounting. Notify fires at human rate (selection and
// toggle changes), so the per-Notify snapshot allocation is acceptable;
// nothing here runs on an audio or packet hot path.
type Registry struct { //nolint:govet // fieldalignment: mu stays above the fields it guards per .claude/rules/concurrency.md; not worth 8 GC-scan bytes
	log zerolog.Logger

	mu        sync.Mutex // protects the fields below
	listeners map[uint64]func(Event)
	nextID    uint64

	dropped atomic.Uint64
}

// NewRegistry returns an empty registry. log is used only to report
// panicking listeners.
func NewRegistry(log zerolog.Logger) *Registry {
	return &Registry{log: log, listeners: make(map[uint64]func(Event), 4)}
}

// Add registers fn for every future event and returns an id for Remove.
// Callbacks must be non-blocking; bounded-buffer consumers drop and call
// NoteDropped instead of stalling the notifier.
func (r *Registry) Add(fn func(Event)) uint64 {
	r.mu.Lock()
	defer r.mu.Unlock()

	r.nextID++
	id := r.nextID
	r.listeners[id] = fn

	return id
}

// Remove deregisters a listener. No-op for unknown ids.
func (r *Registry) Remove(id uint64) {
	r.mu.Lock()
	defer r.mu.Unlock()

	delete(r.listeners, id)
}

// Notify delivers ev to every listener. Listeners are invoked outside the
// lock; a panicking listener is logged and does not affect the others.
// Nil-receiver safe so callers can fire unconditionally.
func (r *Registry) Notify(ev Event) {
	if r == nil {
		return
	}

	r.mu.Lock()

	var listeners []func(Event)

	if len(r.listeners) > 0 {
		listeners = make([]func(Event), 0, len(r.listeners))
		for _, fn := range r.listeners {
			listeners = append(listeners, fn)
		}
	}

	r.mu.Unlock()

	for _, fn := range listeners {
		func() {
			defer func() {
				if rec := recover(); rec != nil {
					r.log.Error().Interface("panic", rec).
						Msg("talkgroup: event listener panicked")
				}
			}()

			fn(ev)
		}()
	}
}

// NoteDropped increments the events-dropped counter. Called by listeners
// with bounded buffers when they shed an event; surfaces in the comms
// instrumentation snapshot as talkgroup_events_dropped.
func (r *Registry) NoteDropped() {
	if r == nil {
		return
	}

	r.dropped.Add(1)
}

// Dropped returns the cumulative dropped-event count. Nil-receiver safe.
func (r *Registry) Dropped() uint64 {
	if r == nil {
		return 0
	}

	return r.dropped.Load()
}
