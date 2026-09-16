package instrumentation_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/openmanet/openmanetd/internal/instrumentation"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewWorker_MissingRegistry(t *testing.T) {
	t.Parallel()

	_, err := instrumentation.NewWorker(instrumentation.WorkerOptions{
		OutputDir: t.TempDir(),
	})
	require.Error(t, err)
}

func TestNewWorker_MissingOutputDir(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	_, err := instrumentation.NewWorker(instrumentation.WorkerOptions{
		Registry: reg,
	})
	require.Error(t, err)
}

func TestNewWorker_DefaultsApplied(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	w, err := instrumentation.NewWorker(instrumentation.WorkerOptions{
		Registry:  reg,
		OutputDir: t.TempDir(),
	})
	require.NoError(t, err)
	require.NotNil(t, w)
}

func TestWorker_RunWritesSnapshots(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	producer := &fakeSnapshotter{
		refreshFn: func(d *fakeCounterData) {
			d.Count++
		},
	}

	require.NoError(t, reg.Register("producer", producer))

	dir := t.TempDir()

	w, err := instrumentation.NewWorker(instrumentation.WorkerOptions{
		Registry:       reg,
		Interval:       20 * time.Millisecond,
		OutputDir:      dir,
		FilenamePrefix: "unit-snapshot",
		Log:            zerolog.Nop(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	var wg sync.WaitGroup

	wg.Add(1)

	go func() {
		defer wg.Done()

		w.Run(ctx)
	}()

	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		entries, err := os.ReadDir(dir)
		require.NoError(t, err)

		if len(entries) >= 2 {
			break
		}

		time.Sleep(5 * time.Millisecond)
	}

	cancel()
	wg.Wait()

	entries, err := os.ReadDir(dir)
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(entries), 2, "worker should produce at least 2 snapshot files")

	var sampled bool

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, "unit-snapshot-") || !strings.HasSuffix(name, ".json") {
			t.Fatalf("unexpected file in snapshot dir: %q", name)
		}

		if sampled {
			continue
		}

		sampled = true

		data, err := os.ReadFile(filepath.Join(dir, name))
		require.NoError(t, err)

		var decoded struct {
			SchemaVersion string `json:"schema_version"`
			Sections      []struct {
				Name string          `json:"name"`
				Data json.RawMessage `json:"data"`
			} `json:"sections"`
		}

		require.NoError(t, json.Unmarshal(data, &decoded))
		assert.Equal(t, "1.6.0", decoded.SchemaVersion)
		require.Len(t, decoded.Sections, 1)
		assert.Equal(t, "producer", decoded.Sections[0].Name)
	}
}

func TestWorker_RunExitsOnContextCancel(t *testing.T) {
	t.Parallel()

	reg := newTestRegistry(t)

	w, err := instrumentation.NewWorker(instrumentation.WorkerOptions{
		Registry:  reg,
		Interval:  10 * time.Millisecond,
		OutputDir: t.TempDir(),
		Log:       zerolog.Nop(),
	})
	require.NoError(t, err)

	ctx, cancel := context.WithCancel(context.Background())

	done := make(chan struct{})

	go func() {
		w.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("worker did not exit within one second of context cancel")
	}
}
