//go:build linux

package blos

import (
	"context"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"runtime"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// openFDs counts the process's open file descriptors via /proc.
func openFDs(t *testing.T) int {
	t.Helper()

	ents, err := os.ReadDir("/proc/self/fd")
	require.NoError(t, err)

	return len(ents)
}

// startFakeLocalAPI serves a minimal tailscaled LocalAPI on a unix socket
// and returns the socket path. Only the prefs endpoint is implemented.
func startFakeLocalAPI(t *testing.T) string {
	t.Helper()

	sock := filepath.Join(t.TempDir(), "ts.sock")

	ln, err := (&net.ListenConfig{}).Listen(context.Background(), "unix", sock)
	require.NoError(t, err)

	mux := http.NewServeMux()
	mux.HandleFunc("/localapi/v0/prefs", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"RouteAll":false}`))
	})
	mux.HandleFunc("/localapi/v0/status", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"BackendState":"Running"}`))
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 0}

	go func() { _ = srv.Serve(ln) }()

	t.Cleanup(func() { _ = srv.Close() })

	return sock
}

// TestLocalTailscaleClient_doesNotLeakFDs pins the regression where every
// GetPrefs/EditPrefs built a fresh local.Client whose private http.Transport
// parked an idle keep-alive unix socket forever. One shared client must hold
// at most one idle connection (plus the server's accepted side of it).
func TestLocalTailscaleClient_doesNotLeakFDs(t *testing.T) {
	sock := startFakeLocalAPI(t)
	ctx := context.Background()

	c := &LocalTailscaleClient{}
	c.lc.Socket = sock
	c.lc.UseSocketOnly = true

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
	}

	runtime.GC()

	// Allow a tiny amount of slack for the fake server's accept/serve
	// goroutines, but nothing proportional to the call count.
	assert.LessOrEqual(t, openFDs(t)-fdsBefore, 2, "file descriptors grew with call count")
	assert.LessOrEqual(t, runtime.NumGoroutine()-goroutinesBefore, 4, "goroutines grew with call count")
}
