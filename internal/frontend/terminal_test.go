package frontend

import (
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
	"github.com/openmanet/openmanetd/internal/auth"
	"github.com/openmanet/openmanetd/internal/terminal"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// terminalTestServer wires only the terminal handler — no auth — for
// behavior tests. Auth gating is exercised separately via the full
// frontend mux test in server_test.go.
func terminalTestServer(t *testing.T) (*httptest.Server, *terminal.Manager) {
	t.Helper()

	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	mgr := terminal.New(zerolog.Nop(), cfg)
	s := &Server{log: zerolog.Nop(), term: mgr}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/terminal/ws", s.handleTerminalWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	return srv, mgr
}

func TestHandleTerminalWS_HappyPath(t *testing.T) {
	srv, mgr := terminalTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"

	c, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)
}

func TestHandleTerminalWS_SecondSessionRejected(t *testing.T) {
	srv, mgr := terminalTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"

	first, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = first.Close() })
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	second, _, err := websocket.DefaultDialer.Dial(url, nil)
	require.NoError(t, err)
	t.Cleanup(func() { _ = second.Close() })
	require.NoError(t, second.SetReadDeadline(time.Now().Add(time.Second)))
	_, _, err = second.ReadMessage()

	var ce *websocket.CloseError
	require.True(t, errors.As(err, &ce))
	assert.Equal(t, websocket.ClosePolicyViolation, ce.Code)
}

func TestHandleTerminalWS_DisabledReturns404(t *testing.T) {
	s := &Server{log: zerolog.Nop()}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/terminal/ws", s.handleTerminalWS)
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)

	resp, err := http.Get(srv.URL + "/api/terminal/ws")
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusNotFound, resp.StatusCode)
}

// dialWS dials a WebSocket URL, sending origin as the Origin header when it is
// non-empty, and registers cleanup for whatever the dial returned.
func dialWS(t *testing.T, url, origin string) (*websocket.Conn, *http.Response, error) {
	t.Helper()

	header := http.Header{}
	if origin != "" {
		header.Set("Origin", origin)
	}

	c, resp, err := websocket.DefaultDialer.Dial(url, header)
	if c != nil {
		t.Cleanup(func() { _ = c.Close() })
	}

	if resp != nil && resp.Body != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}

	if err != nil {
		return c, resp, fmt.Errorf("dial %s: %w", url, err)
	}

	return c, resp, nil
}

func TestHandleTerminalWS_untrustedOriginRejected(t *testing.T) {
	srv, mgr := terminalTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"

	_, resp, err := dialWS(t, url, "https://example.com")

	require.ErrorIs(t, err, websocket.ErrBadHandshake)
	require.NotNil(t, resp)
	assert.Equal(t, http.StatusForbidden, resp.StatusCode)
	assert.False(t, mgr.InUse())
}

func TestHandleTerminalWS_lanOriginAccepted(t *testing.T) {
	srv, mgr := terminalTestServer(t)
	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"

	_, _, err := dialWS(t, url, "http://10.41.1.1:8081")

	require.NoError(t, err)
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)
}

// authedTerminalTestServer wires the terminal handler behind the frontend
// auth middleware backed by store, as the daemon does with auth enabled.
func authedTerminalTestServer(t *testing.T, store *auth.SessionStore) (*httptest.Server, *terminal.Manager) {
	t.Helper()

	cfg := terminal.DefaultConfig()
	cfg.Shell = "/bin/sh"
	cfg.GraceShutdown = 200 * time.Millisecond
	mgr := terminal.New(zerolog.Nop(), cfg)
	s := &Server{log: zerolog.Nop(), term: mgr, sessionStore: store, authEnabled: true}

	mux := http.NewServeMux()
	mux.HandleFunc("/api/terminal/ws", s.handleTerminalWS)
	srv := httptest.NewServer(auth.NewFrontendAuthMiddleware(store, true)(mux))
	t.Cleanup(srv.Close)

	return srv, mgr
}

// dialTerminalWithToken opens the terminal WebSocket with token as the
// session cookie.
func dialTerminalWithToken(t *testing.T, srv *httptest.Server, token string) {
	t.Helper()

	url := "ws" + strings.TrimPrefix(srv.URL, "http") + "/api/terminal/ws"
	header := http.Header{}
	header.Set("Cookie", auth.SessionCookieName+"="+token)

	c, _, err := websocket.DefaultDialer.Dial(url, header)
	require.NoError(t, err)
	t.Cleanup(func() { _ = c.Close() })
}

func TestHandleTerminalWS_logoutEndsSession(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 4)
	srv, mgr := authedTerminalTestServer(t, store)
	token := store.Create("root")

	dialTerminalWithToken(t, srv, token)
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	store.Delete(token)

	assert.Eventually(t, func() bool { return !mgr.InUse() }, 3*time.Second, 20*time.Millisecond)
}

func TestHandleTerminalWS_sessionExpiryEndsSession(t *testing.T) {
	store := auth.NewSessionStore(time.Second, 4)
	srv, mgr := authedTerminalTestServer(t, store)

	dialTerminalWithToken(t, srv, store.Create("root"))
	require.Eventually(t, mgr.InUse, 500*time.Millisecond, 10*time.Millisecond)

	assert.Eventually(t, func() bool { return !mgr.InUse() }, 4*time.Second, 20*time.Millisecond)
}

func TestHandleTerminalWS_otherLogoutKeepsSession(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 4)
	srv, mgr := authedTerminalTestServer(t, store)
	other := store.Create("root")

	dialTerminalWithToken(t, srv, store.Create("root"))
	require.Eventually(t, mgr.InUse, time.Second, 10*time.Millisecond)

	store.Delete(other)

	assert.Never(t, func() bool { return !mgr.InUse() }, 500*time.Millisecond, 20*time.Millisecond)
}
