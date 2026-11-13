//go:build linux

package blos

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"tailscale.com/ipn"
)

// openFDs counts the process's open file descriptors via /proc.
func openFDs(t *testing.T) int {
	t.Helper()

	ents, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)

	return len(ents)
}

// fakeLocalAPI is a minimal tailscaled LocalAPI served over a unix socket.
// It implements the status and prefs endpoints the BLOS client uses and can
// be switched into a failing mode to exercise error paths.
type fakeLocalAPI struct {
	socket string
	fail   atomic.Bool

	mu      sync.Mutex // protects the fields below
	prefs   ipn.Prefs
	patches []ipn.MaskedPrefs
}

func startFakeLocalAPI(t *testing.T) *fakeLocalAPI {
	t.Helper()

	f := &fakeLocalAPI{socket: filepath.Join(t.TempDir(), "ts.sock")}
	f.prefs.Hostname = "fake-node"

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", f.socket)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/localapi/v0/status", f.handleStatus)
	mux.HandleFunc("/localapi/v0/prefs", f.handlePrefs)

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 0}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	return f
}

func (f *fakeLocalAPI) failing(w http.ResponseWriter) bool {
	if !f.fail.Load() {
		return false
	}

	http.Error(w, "fake tailscaled failure", http.StatusInternalServerError)

	return true
}

func (f *fakeLocalAPI) handleStatus(w http.ResponseWriter, _ *http.Request) {
	if f.failing(w) {
		return
	}

	w.Header().Set("Content-Type", "application/json")
	_, _ = w.Write([]byte(`{"BackendState":"Running"}`))
}

func (f *fakeLocalAPI) handlePrefs(w http.ResponseWriter, r *http.Request) {
	if f.failing(w) {
		return
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	if r.Method == http.MethodPatch {
		var mp ipn.MaskedPrefs
		if err := json.NewDecoder(r.Body).Decode(&mp); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)

			return
		}

		f.patches = append(f.patches, mp)
		f.prefs.ApplyEdits(&mp)
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(f.prefs)
}

func (f *fakeLocalAPI) receivedPatches() []ipn.MaskedPrefs {
	f.mu.Lock()
	defer f.mu.Unlock()

	return append([]ipn.MaskedPrefs(nil), f.patches...)
}

// newTestLocalTailscaleClient points a production LocalTailscaleClient at the
// fake socket. Only the socket path differs from the daemon's configuration.
func newTestLocalTailscaleClient(f *fakeLocalAPI) *LocalTailscaleClient {
	c := &LocalTailscaleClient{}
	c.lc.Socket = f.socket
	c.lc.UseSocketOnly = true

	return c
}

// TestLocalTailscaleClient_doesNotLeakFDs pins the regression where every
// GetPrefs/EditPrefs built a fresh local.Client whose private http.Transport
// parked an idle keep-alive unix socket forever. One shared client must hold
// at most one idle connection (plus the server's accepted side of it).
func TestLocalTailscaleClient_doesNotLeakFDs(t *testing.T) {
	f := startFakeLocalAPI(t)
	c := newTestLocalTailscaleClient(f)
	ctx := context.Background()

	const calls = 50

	// Warm up once so the single keep-alive pair exists before measuring.
	_, err := c.GetPrefs(ctx)
	require.NoError(t, err)

	runtime.GC()

	fdsBefore := openFDs(t)
	goroutinesBefore := runtime.NumGoroutine()

	for range calls {
		_, err := c.GetPrefs(ctx)
		require.NoError(t, err)

		_, err = c.Status(ctx)
		require.NoError(t, err)

		_, err = c.EditPrefs(ctx, &ipn.MaskedPrefs{Prefs: ipn.Prefs{NoSNAT: false}, NoSNATSet: true})
		require.NoError(t, err)
	}

	runtime.GC()

	// Allow a tiny amount of slack for the fake server's accept/serve
	// goroutines, but nothing proportional to the call count.
	assert.LessOrEqual(t, openFDs(t)-fdsBefore, 2, "file descriptors grew with call count")
	assert.LessOrEqual(t, runtime.NumGoroutine()-goroutinesBefore, 4, "goroutines grew with call count")
}

func TestLocalTailscaleClient_Status(t *testing.T) {
	f := startFakeLocalAPI(t)
	c := newTestLocalTailscaleClient(f)

	status, err := c.Status(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "Running", status.BackendState)
}

func TestLocalTailscaleClient_GetPrefs(t *testing.T) {
	f := startFakeLocalAPI(t)
	c := newTestLocalTailscaleClient(f)

	prefs, err := c.GetPrefs(context.Background())
	require.NoError(t, err)
	assert.Equal(t, "fake-node", prefs.Hostname)
}

// TestLocalTailscaleClient_EditPrefs_roundTrip checks the masked edit reaches
// the daemon as a PATCH and that the daemon's updated prefs come back.
func TestLocalTailscaleClient_EditPrefs_roundTrip(t *testing.T) {
	f := startFakeLocalAPI(t)
	c := newTestLocalTailscaleClient(f)

	edit := &ipn.MaskedPrefs{
		Prefs:       ipn.Prefs{RouteAll: true, NoSNAT: true},
		RouteAllSet: true,
		NoSNATSet:   true,
	}

	updated, err := c.EditPrefs(context.Background(), edit)
	require.NoError(t, err)
	assert.True(t, updated.RouteAll)
	assert.True(t, updated.NoSNAT)
	assert.Equal(t, "fake-node", updated.Hostname, "unmasked fields must be left untouched")

	patches := f.receivedPatches()
	require.Len(t, patches, 1)
	assert.True(t, patches[0].RouteAllSet)
	assert.True(t, patches[0].NoSNATSet)
	assert.False(t, patches[0].HostnameSet)

	// The edit is visible to a subsequent read through the same client.
	prefs, err := c.GetPrefs(context.Background())
	require.NoError(t, err)
	assert.True(t, prefs.RouteAll)
}

// TestLocalTailscaleClient_wrapsErrors verifies every method surfaces a
// daemon failure as a non-nil error carrying an operation prefix, and that a
// failure does not poison the shared client for the next call.
func TestLocalTailscaleClient_wrapsErrors(t *testing.T) {
	f := startFakeLocalAPI(t)
	c := newTestLocalTailscaleClient(f)
	ctx := context.Background()

	tests := []struct {
		name   string
		call   func() error
		prefix string
	}{
		{
			name:   "Status",
			prefix: "tailscale status",
			call: func() error {
				_, err := c.Status(ctx)

				return err
			},
		},
		{
			name:   "GetPrefs",
			prefix: "tailscale get prefs",
			call: func() error {
				_, err := c.GetPrefs(ctx)

				return err
			},
		},
		{
			name:   "EditPrefs",
			prefix: "tailscale edit prefs",
			call: func() error {
				_, err := c.EditPrefs(ctx, &ipn.MaskedPrefs{NoSNATSet: true})

				return err
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f.fail.Store(true)

			err := tc.call()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tc.prefix)

			f.fail.Store(false)

			require.NoError(t, tc.call(), "client must recover once the daemon is healthy again")
		})
	}
}

// TestLocalTailscaleClient_daemonUnreachable covers the connect failure path
// (tailscaled not running) for all three methods.
func TestLocalTailscaleClient_daemonUnreachable(t *testing.T) {
	c := &LocalTailscaleClient{}
	c.lc.Socket = filepath.Join(t.TempDir(), "missing.sock")
	c.lc.UseSocketOnly = true

	ctx := context.Background()

	_, err := c.Status(ctx)
	assert.ErrorContains(t, err, "tailscale status")

	_, err = c.GetPrefs(ctx)
	assert.ErrorContains(t, err, "tailscale get prefs")

	_, err = c.EditPrefs(ctx, &ipn.MaskedPrefs{})
	assert.ErrorContains(t, err, "tailscale edit prefs")
}
