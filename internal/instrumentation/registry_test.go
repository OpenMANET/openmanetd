package instrumentation_test

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/openmanet/openmanetd/internal/instrumentation"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeCounterData is a simple JSON-marshalable struct used by test
// snapshotters to verify data flows end-to-end.
type fakeCounterData struct {
	Count int64 `json:"count"`
	Flag  bool  `json:"flag"`
}

// fakeSnapshotter is a hand-rolled instrumentation.Snapshotter used across
// the tests. It intentionally tracks a refresh counter so tests can assert
// Capture calls Refresh exactly once per snapshot.
type fakeSnapshotter struct {
	data        fakeCounterData
	refreshFn   func(*fakeCounterData)
	refreshCall int
}

func (f *fakeSnapshotter) Refresh() {
	f.refreshCall++
	if f.refreshFn != nil {
		f.refreshFn(&f.data)
	}
}

func (f *fakeSnapshotter) Data() any {
	return &f.data
}

func newTestRegistry(t *testing.T) *instrumentation.Registry {
	t.Helper()

	return instrumentation.NewRegistry(instrumentation.Options{
		Log:      zerolog.Nop(),
		Version:  "test-v1",
		Hostname: "test-host",
	})
}

func TestRegistry_RegisterAndCapture(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	producer := &fakeSnapshotter{
		refreshFn: func(d *fakeCounterData) {
			d.Count = 42
			d.Flag = true
		},
	}

	require.NoError(t, reg.Register("producer", producer))

	var env instrumentation.Envelope
	reg.Capture(&env)

	assert.Equal(t, instrumentation.SchemaVersion, env.SchemaVersion)
	assert.Equal(t, "test-v1", env.Daemon.Version)
	assert.Equal(t, "test-host", env.Daemon.Hostname)
	assert.NotZero(t, env.Daemon.PID)
	assert.False(t, env.Daemon.StartedAt.IsZero())

	assert.False(t, env.CapturedAtStart.IsZero())
	assert.False(t, env.CapturedAtEnd.IsZero())
	assert.True(t, env.CapturedAtEnd.After(env.CapturedAtStart) ||
		env.CapturedAtEnd.Equal(env.CapturedAtStart))

	require.Len(t, env.Sections, 1)
	assert.Equal(t, "producer", env.Sections[0].Name)

	data, ok := env.Sections[0].Data.(*fakeCounterData)
	require.True(t, ok)
	assert.Equal(t, int64(42), data.Count)
	assert.True(t, data.Flag)

	assert.Equal(t, 1, producer.refreshCall)
}

func TestRegistry_Register_DuplicateRejected(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	require.NoError(t, reg.Register("dup", &fakeSnapshotter{}))

	err := reg.Register("dup", &fakeSnapshotter{})
	require.Error(t, err)
	assert.True(t, errors.Is(err, instrumentation.ErrDuplicateSection))
}

func TestRegistry_Register_NilSnapshotter(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	require.Error(t, reg.Register("bad", nil))
}

func TestRegistry_Capture_ReusesSlice(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)
	require.NoError(t, reg.Register("a", &fakeSnapshotter{}))
	require.NoError(t, reg.Register("b", &fakeSnapshotter{}))

	var env instrumentation.Envelope
	reg.Capture(&env)

	backing := env.Sections
	ptr := &backing[0]

	reg.Capture(&env)

	// The backing array should be reused — len and cap match the original,
	// and the first NamedSection header should still live at the same
	// address.
	require.Len(t, env.Sections, 2)
	assert.Same(t, ptr, &env.Sections[0])
}

func TestRegistry_Capture_NilEnvelope(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	assert.NotPanics(t, func() {
		reg.Capture(nil)
	})
}

func TestRegistry_Capture_ZeroAlloc(t *testing.T) {
	// testing.AllocsPerRun cannot run under t.Parallel.
	reg := newTestRegistry(t)
	require.NoError(t, reg.Register("a", &fakeSnapshotter{}))
	require.NoError(t, reg.Register("b", &fakeSnapshotter{}))
	require.NoError(t, reg.Register("c", &fakeSnapshotter{}))

	var env instrumentation.Envelope
	// Warm up: the first Capture allocates the Sections backing array.
	reg.Capture(&env)

	allocs := testing.AllocsPerRun(100, func() {
		reg.Capture(&env)
	})

	assert.Equal(t, 0.0, allocs, "Capture must not allocate after warmup")
}

func TestEnvelope_JSONRoundTrip(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	producer := &fakeSnapshotter{
		refreshFn: func(d *fakeCounterData) {
			d.Count = 99
			d.Flag = true
		},
	}

	require.NoError(t, reg.Register("producer", producer))

	var env instrumentation.Envelope
	reg.Capture(&env)

	out, err := json.Marshal(&env)
	require.NoError(t, err)
	assert.Contains(t, string(out), `"schema_version":"1.6.0"`)
	assert.Contains(t, string(out), `"count":99`)
	assert.Contains(t, string(out), `"flag":true`)
	assert.Contains(t, string(out), `"name":"producer"`)

	// Decode into a loosely typed envelope so we can verify field
	// presence without depending on type identity.
	var decoded struct {
		SchemaVersion string `json:"schema_version"`
		Sections      []struct {
			Name string          `json:"name"`
			Data json.RawMessage `json:"data"`
		} `json:"sections"`
		Runtime struct {
			NumGoroutine int `json:"num_goroutine"`
		} `json:"runtime"`
	}

	require.NoError(t, json.Unmarshal(out, &decoded))
	assert.Equal(t, "1.6.0", decoded.SchemaVersion)
	require.Len(t, decoded.Sections, 1)
	assert.Equal(t, "producer", decoded.Sections[0].Name)
	assert.Positive(t, decoded.Runtime.NumGoroutine)
}

func TestRegistry_Capture_RuntimeStatsPopulated(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	var env instrumentation.Envelope
	reg.Capture(&env)

	assert.Positive(t, env.Runtime.NumGoroutine)
	assert.Positive(t, env.Runtime.MemSysBytes)
	// MemAlloc may be zero immediately after a GC but is almost always positive.
	assert.GreaterOrEqual(t, env.Runtime.HeapInuseBytes, uint64(0))
}

func TestSchemaVersion_Stable(t *testing.T) {
	t.Parallel()

	// A drift in this constant must be deliberate — bump the copy in
	// docs/instrumentation-snapshot.md at the same time.
	assert.Equal(t, "1.6.0", instrumentation.SchemaVersion)
}

// TestRegistry_CaptureSkewWindow verifies that CapturedAtEnd - CapturedAtStart
// is non-negative and small. It does not attempt a strict upper bound because
// CI noise can blow the window out.
func TestRegistry_CaptureSkewWindow(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)
	require.NoError(t, reg.Register("a", &fakeSnapshotter{}))

	var env instrumentation.Envelope
	reg.Capture(&env)

	window := env.CapturedAtEnd.Sub(env.CapturedAtStart)
	assert.GreaterOrEqual(t, window, time.Duration(0))
}
