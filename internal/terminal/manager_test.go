package terminal_test

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openmanet/openmanetd/internal/terminal"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newManagerTestServer(t *testing.T, mgr *terminal.Manager) *httptest.Server {
	t.Helper()

	up := websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		conn, err := up.Upgrade(w, r, nil)
		if err != nil {
			return
		}
		defer conn.Close()

		_ = mgr.Run(r.Context(), conn)
	}))
	t.Cleanup(srv.Close)

	return srv
}

func TestManager_AllowsFirstSession(t *testing.T) {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })

	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)
}

func TestManager_RejectsSecondSessionWith1008(t *testing.T) {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	second, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })

	require.NoError(t, second.SetReadDeadline(time.Now().Add(2*time.Second)))
	_, _, err = second.ReadMessage()

	var ce *websocket.CloseError
	require.True(t, errors.As(err, &ce), "expected close error, got %T %v", err, err)
	assert.Equal(t, websocket.ClosePolicyViolation, ce.Code)
	assert.Contains(t, ce.Text, "in use")
}

func TestManager_ReleasesAfterClose(t *testing.T) {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	cfg.GraceShutdown = 200 * time.Millisecond
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	first, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	// Close cleanly; manager should release within the grace window.
	require.NoError(t, first.WriteMessage(websocket.CloseMessage,
		websocket.FormatCloseMessage(websocket.CloseNormalClosure, "")))
	_ = first.Close()

	require.Eventually(t, func() bool { return !mgr.InUse() }, 3*time.Second, 25*time.Millisecond)

	// Verify a second connection now succeeds.
	var wg sync.WaitGroup
	wg.Add(1)

	var second *websocket.Conn

	go func() {
		defer wg.Done()

		c, _, derr := websocket.DefaultDialer.Dial(url, nil)
		require.NoError(t, derr)

		second = c
	}()

	wg.Wait()
	t.Cleanup(func() { _ = second.Close() })

	// Second connection should not be closed with 1008.
	require.NoError(t, second.SetReadDeadline(time.Now().Add(200*time.Millisecond)))

	_, _, err = second.ReadMessage()
	if err != nil {
		var ce *websocket.CloseError
		if errors.As(err, &ce) {
			assert.NotEqual(t, websocket.ClosePolicyViolation, ce.Code)
		}
	}
}

// TestManager_NoLeakOnConcurrentDial guards the spec's "Single-session
// bypass via reload race" mitigation: two simultaneous dials must end up
// with exactly one accepted session.
func TestManager_NoLeakOnConcurrentDial(t *testing.T) {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	mgr := terminal.New(zerolog.Nop(), cfg)
	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")

	const n = 8

	var wg sync.WaitGroup
	wg.Add(n)
	rejected := make(chan struct{}, n)

	conns := make([]*websocket.Conn, n)
	for i := range n {
		go func() {
			defer wg.Done()

			c, _, err := websocket.DefaultDialer.Dial(url, nil)
			if err != nil {
				rejected <- struct{}{}

				return
			}

			conns[i] = c

			_ = c.SetReadDeadline(time.Now().Add(500 * time.Millisecond))
			if _, _, err := c.ReadMessage(); err != nil {
				var ce *websocket.CloseError
				if errors.As(err, &ce) && ce.Code == websocket.ClosePolicyViolation {
					rejected <- struct{}{}
				}
			}
		}()
	}

	wg.Wait()

	for _, c := range conns {
		if c != nil {
			_ = c.Close()
		}
	}
	// Exactly one wins; the rest are rejected.
	assert.Equal(t, n-1, len(rejected))
}

// keepaliveConfig returns a /bin/sh config with keepalive timings short
// enough for tests.
func keepaliveConfig() terminal.Config {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	cfg.GraceShutdown = 200 * time.Millisecond
	cfg.PongWait = 300 * time.Millisecond
	cfg.PingPeriod = 100 * time.Millisecond

	return cfg
}

func TestManager_closesSessionWhenPongsStop(t *testing.T) {
	mgr := terminal.New(zerolog.Nop(), keepaliveConfig())

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	// The client never reads, so it never answers the server's pings.
	assert.Eventually(t, func() bool { return !mgr.InUse() }, 3*time.Second, 20*time.Millisecond)
}

func TestManager_keepsSessionThatAnswersPings(t *testing.T) {
	cfg := keepaliveConfig()
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)

	// Reading lets gorilla's default ping handler answer each ping.
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
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	assert.Never(t, func() bool { return !mgr.InUse() }, 4*cfg.PongWait, 20*time.Millisecond)
}

func TestManager_closesSessionOnOversizedMessage(t *testing.T) {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	cfg.GraceShutdown = 200 * time.Millisecond
	cfg.MaxMessageSize = 1024
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	require.NoError(t, c.WriteMessage(websocket.BinaryMessage, []byte(strings.Repeat("a", 4096))))

	assert.Eventually(t, func() bool { return !mgr.InUse() }, 3*time.Second, 20*time.Millisecond)
}

func TestManager_zeroKeepaliveFieldsUseDefaults(t *testing.T) {
	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	cfg.PongWait = 0
	cfg.PingPeriod = 0
	cfg.MaxMessageSize = 0
	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	assert.Never(t, func() bool { return !mgr.InUse() }, 300*time.Millisecond, 20*time.Millisecond)
}

// TestManager_defaultKeepaliveToleratesDelayedPongs runs the default ping
// and pong timings scaled down 100x against a client whose pongs arrive a
// third of PongWait late, as when a ping waits behind queued shell output on
// a slow mesh link. The session must stay open.
func TestManager_defaultKeepaliveToleratesDelayedPongs(t *testing.T) {
	const scale = 100

	def := terminal.DefaultConfig()
	cfg := keepaliveConfig()
	cfg.PongWait = def.PongWait / scale
	cfg.PingPeriod = def.PingPeriod / scale
	pongDelay := cfg.PongWait / 3

	mgr := terminal.New(zerolog.Nop(), cfg)

	srv := newManagerTestServer(t, mgr)
	url := "ws" + strings.TrimPrefix(srv.URL, "http")
	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)

	var pongs sync.WaitGroup

	c.SetPingHandler(func(data string) error {
		pongs.Add(1)
		time.AfterFunc(pongDelay, func() {
			defer pongs.Done()

			_ = c.WriteControl(websocket.PongMessage, []byte(data), time.Now().Add(time.Second))
		})

		return nil
	})

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
		pongs.Wait()
	})
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	assert.Never(t, func() bool { return !mgr.InUse() }, 4*cfg.PongWait, 20*time.Millisecond)
}
