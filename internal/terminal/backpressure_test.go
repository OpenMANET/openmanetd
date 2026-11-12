package terminal_test

import (
	"bytes"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openmanet/openmanetd/internal/terminal"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// shellScript writes an executable /bin/sh script with the given body and
// returns its path, for use as Config.Shell.
func shellScript(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "shell.sh")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+body+"\n"), 0o700)) //nolint:gosec // test script must be executable

	return path
}

// goroutineRunning reports whether any goroutine's stack includes fn.
func goroutineRunning(fn string) bool {
	buf := make([]byte, 1<<20)

	return strings.Contains(string(buf[:runtime.Stack(buf, true)]), fn)
}

// readUntil reads messages from c until their concatenation contains want.
func readUntil(t *testing.T, c *websocket.Conn, want string) {
	t.Helper()

	require.NoError(t, c.SetReadDeadline(time.Now().Add(3*time.Second)))

	var got strings.Builder

	for !strings.Contains(got.String(), want) {
		_, data, err := c.ReadMessage()
		require.NoError(t, err)
		got.Write(data)
	}

	require.NoError(t, c.SetReadDeadline(time.Time{}))
}

// TestManager_closesSessionWhenPongsStopDuringStalledInput covers a client
// that vanishes while its input is blocked on a raw-mode program that has
// stopped reading stdin. The pong deadline must still end the session, and
// teardown must release the goroutine blocked writing that input.
func TestManager_closesSessionWhenPongsStopDuringStalledInput(t *testing.T) {
	cfg := keepaliveConfig()
	cfg.Shell = shellScript(t, "stty raw -echo\nprintf ready\nexec sleep 30")
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	readUntil(t, c, "ready")

	// More input than the PTY accepts in raw mode before a write blocks.
	chunk := []byte(strings.Repeat("a", 16*1024))
	for range 4 {
		require.NoError(t, c.WriteMessage(websocket.BinaryMessage, chunk))
	}

	// The client stops reading, so it never answers another ping.
	require.Eventually(t, func() bool { return !mgr.InUse() }, 5*time.Second, 20*time.Millisecond)

	assert.Eventually(t, func() bool { return !goroutineRunning("terminal.(*Session).pumpWSToPTY") },
		3*time.Second, 20*time.Millisecond, "PTY input pump still blocked after teardown")
}

// TestManager_keepsSessionWhilePingWaitsBehindBlockedOutput covers a live
// client on a link that stalls: shell output fills the socket, so a ping
// waits behind a blocked frame. The session must survive any stall shorter
// than PongWait. The stall outlasts the 10s ping-write budget the session
// used to apply, so the test is slow.
func TestManager_keepsSessionWhilePingWaitsBehindBlockedOutput(t *testing.T) {
	if testing.Short() {
		t.Skip("stalls for 12s")
	}

	cfg := keepaliveConfig()
	cfg.Shell = shellScript(t, "exec yes")
	cfg.PongWait = 15 * time.Second
	cfg.PingPeriod = time.Second
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	// Not reading lets output back up until the server's frame writes block.
	require.Never(t, func() bool { return !mgr.InUse() }, 12*time.Second, 100*time.Millisecond)

	readerDone := make(chan struct{})

	go func() {
		defer close(readerDone)

		for {
			if _, _, rerr := c.ReadMessage(); rerr != nil {
				return
			}
		}
	}()

	t.Cleanup(func() {
		_ = c.Close()

		<-readerDone
	})

	// Reading again drains the backlog, lets the waiting ping through, and
	// answers it, so the session outlives the deadline set by its last pong
	// before the stall (PongWait after the session started).
	assert.Never(t, func() bool { return !mgr.InUse() }, 5*time.Second, 100*time.Millisecond)
}

// TestManager_deliversPasteSentInChunks covers the client's paste path: a
// paste larger than MaxMessageSize, sent as messages under it, reaches the
// shell intact and keeps the session.
func TestManager_deliversPasteSentInChunks(t *testing.T) {
	const pasteBytes = 100 * 1024

	out := filepath.Join(t.TempDir(), "paste")
	cfg := keepaliveConfig()
	cfg.Env = append(cfg.Env, "OUT="+out)
	cfg.Shell = shellScript(t, "stty raw -echo\nprintf ready\nhead -c "+strconv.Itoa(pasteBytes)+` > "$OUT"`+"\nprintf done\nexec sleep 30")
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	readUntil(t, c, "ready")

	paste := bytes.Repeat([]byte("0123456789abcdef"), pasteBytes/16)
	require.Greater(t, int64(len(paste)), cfg.MaxMessageSize)

	for chunk := range slices.Chunk(paste, 16*1024) {
		require.NoError(t, c.WriteMessage(websocket.BinaryMessage, chunk))
	}

	readUntil(t, c, "done")

	got, err := os.ReadFile(out)
	require.NoError(t, err)
	assert.Equal(t, paste, got)
	assert.True(t, mgr.InUse())
}
