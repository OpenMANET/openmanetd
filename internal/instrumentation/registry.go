package instrumentation

import (
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/rs/zerolog"
)

// SchemaVersion identifies the shape of the JSON Envelope document. Bump the
// minor component when adding fields, the major component when removing or
// renaming them. Consumers (including Claude) are expected to key their
// expectations off this value.
const SchemaVersion = "1.6.0"

// Snapshotter is implemented by producers that expose runtime state to the
// snapshot framework. Refresh atomically loads the producer's current
// counters into a producer-owned snapshot struct; Data returns a stable
// pointer to that struct.
//
// Refresh MUST NOT allocate (proven by testing.AllocsPerRun in producer
// tests) and MUST NOT hold any producer-side lock across blocking
// operations. The Registry serializes calls with its own mutex, so
// Refresh does not need to be safe against concurrent calls from the
// framework, but it must be safe against concurrent writes from the
// producer's hot path (typically via sync/atomic loads).
type Snapshotter interface {
	Refresh()
	Data() any
}

// DaemonInfo carries immutable process-level metadata included in every
// Envelope. Populated once at Registry construction.
type DaemonInfo struct {
	StartedAt time.Time `json:"started_at"`
	Version   string    `json:"version"`
	Hostname  string    `json:"hostname"`
	PID       int       `json:"pid"`
}

// NamedSection is one entry in an Envelope. Data is a pointer into a
// producer-owned struct — holding it across captures is safe because
// Refresh refreshes the same struct in place. Consumers that want to
// retain a snapshot across captures must copy or marshal Data.
type NamedSection struct {
	Data any    `json:"data"`
	Name string `json:"name"`
}

// Envelope is the top-level snapshot document. Callers own Envelope values
// and reuse them across captures so that the Sections slice capacity can
// be amortized. The framework never holds an Envelope pointer beyond a
// single Capture call.
type Envelope struct {
	Daemon          DaemonInfo     `json:"daemon"`
	CapturedAtStart time.Time      `json:"captured_at_start"`
	CapturedAtEnd   time.Time      `json:"captured_at_end"`
	SchemaVersion   string         `json:"schema_version"`
	Sections        []NamedSection `json:"sections"`
	Runtime         RuntimeStats   `json:"runtime"`
}

// Options configures a new Registry.
type Options struct {
	Log zerolog.Logger
	// Version, Hostname are the fields of the emitted DaemonInfo. StartedAt
	// and PID are captured from the current process automatically.
	Version  string
	Hostname string
}

// Registry holds the set of registered Snapshotters and produces Envelopes.
// The zero value is not usable — construct with NewRegistry.
type Registry struct {
	log      zerolog.Logger
	daemon   DaemonInfo
	sections []registration
	memStats preallocMemStats
	mu       sync.Mutex
}

// registration is the Registry's internal view of a registered Snapshotter.
type registration struct {
	Snap Snapshotter
	Name string
}

// NewRegistry constructs a Registry ready for Register / Capture calls.
func NewRegistry(opts Options) *Registry {
	return &Registry{
		log: opts.Log,
		daemon: DaemonInfo{
			Version:   opts.Version,
			Hostname:  opts.Hostname,
			PID:       processID(),
			StartedAt: time.Now().UTC(),
		},
		sections: make([]registration, 0, 8),
	}
}

// ErrDuplicateSection is returned by Register when the given name is already
// registered.
var ErrDuplicateSection = errors.New("instrumentation: section already registered")

// Register adds a Snapshotter under a stable section name. Names must be
// unique within the Registry. Register is safe to call from any goroutine;
// typical usage is to call it once per producer during daemon startup,
// before the Worker goroutine is spawned.
func (r *Registry) Register(name string, s Snapshotter) error {
	if s == nil {
		return fmt.Errorf("instrumentation: snapshotter for %q is nil", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	for i := range r.sections {
		if r.sections[i].Name == name {
			return fmt.Errorf("%w: %q", ErrDuplicateSection, name)
		}
	}

	r.sections = append(r.sections, registration{Name: name, Snap: s})

	return nil
}

// Capture fills env with a fresh snapshot. env is caller-owned and reused
// across calls; Capture resizes env.Sections in place so that after the
// first call the slice backing array is retained and no further allocation
// happens on the Sections field. Individual Snapshotter implementations
// are responsible for their own zero-alloc contract.
//
// Capture records CapturedAtStart before any producer is refreshed and
// CapturedAtEnd after all producers have been refreshed, so consumers can
// bound the per-capture skew window.
func (r *Registry) Capture(env *Envelope) {
	if env == nil {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	env.SchemaVersion = SchemaVersion
	env.Daemon = r.daemon
	env.CapturedAtStart = time.Now().UTC()

	// Reuse env.Sections capacity. Producers' Data() returns a pointer to a
	// struct that is refreshed in place, so the slice entries also survive
	// reuse (same Name + same pointer = same section).
	n := len(r.sections)
	if cap(env.Sections) < n {
		env.Sections = make([]NamedSection, n)
	} else {
		env.Sections = env.Sections[:n]
	}

	for i := range r.sections {
		reg := &r.sections[i]
		reg.Snap.Refresh()
		env.Sections[i].Name = reg.Name
		env.Sections[i].Data = reg.Snap.Data()
	}

	r.captureRuntime(&env.Runtime)

	env.CapturedAtEnd = time.Now().UTC()
}
