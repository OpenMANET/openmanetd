package frontend

import (
	"context"
	"encoding/json"
	"io"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"testing/fstest"
	"time"

	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/util/logger"
	ws "github.com/openmanet/openmanetd/internal/websocket"
	"github.com/rs/zerolog"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

func newTestServer(opts ...func(*Server)) *Server {
	hub := ws.NewHub(nil)
	go hub.Run(context.Background())

	cfg := config.NewWithoutWatch(nil)
	staticFS := fstest.MapFS{
		"index.html":       &fstest.MapFile{Data: []byte("<html><body>SPA</body></html>")},
		"assets/app.js":    &fstest.MapFile{Data: []byte("console.log('app')")},
		"assets/style.css": &fstest.MapFile{Data: []byte("body{}")},
		"pcm-worklet.js":   &fstest.MapFile{Data: []byte("// worklet")},
	}

	indexHTML, _ := fs.ReadFile(staticFS, "index.html")

	s := &Server{
		log:       logger.GetLogger("frontend.test"),
		staticFS:  staticFS,
		hub:       hub,
		cfg:       cfg,
		indexHTML: indexHTML,
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

func decodeJSON(t *testing.T, body io.Reader, v any) {
	t.Helper()

	if err := json.NewDecoder(body).Decode(v); err != nil {
		t.Fatalf("json decode: %v", err)
	}
}

// ---------------------------------------------------------------------------
// Helper method tests
// ---------------------------------------------------------------------------

func TestWriteJSON(t *testing.T) {
	srv := newTestServer()

	w := httptest.NewRecorder()
	srv.writeJSON(w, map[string]string{"key": "value"})

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var result map[string]string
	decodeJSON(t, w.Body, &result)

	if result["key"] != "value" {
		t.Errorf("key = %q, want %q", result["key"], "value")
	}
}

func TestWriteError(t *testing.T) {
	srv := newTestServer()

	w := httptest.NewRecorder()
	srv.writeError(w, "something failed")

	if w.Code != http.StatusBadGateway {
		t.Fatalf("status = %d, want 502", w.Code)
	}

	if ct := w.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("content-type = %q, want application/json", ct)
	}

	var result errorResponse
	decodeJSON(t, w.Body, &result)

	if result.Error != "something failed" {
		t.Errorf("error = %q, want %q", result.Error, "something failed")
	}
}

func TestWriteErrorStatus(t *testing.T) {
	srv := newTestServer()

	w := httptest.NewRecorder()
	srv.writeErrorStatus(w, http.StatusNotFound, "not found")

	if w.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", w.Code)
	}

	var result errorResponse
	decodeJSON(t, w.Body, &result)

	if result.Error != "not found" {
		t.Errorf("error = %q, want %q", result.Error, "not found")
	}
}

// ---------------------------------------------------------------------------
// Middleware tests
// ---------------------------------------------------------------------------

func TestCoiMiddleware(t *testing.T) {
	inner := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	handler := coiMiddleware(inner)

	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/", nil)
	handler.ServeHTTP(w, r)

	if v := w.Header().Get("Cross-Origin-Opener-Policy"); v != "same-origin" {
		t.Errorf("COOP = %q, want same-origin", v)
	}

	if v := w.Header().Get("Cross-Origin-Embedder-Policy"); v != "require-corp" {
		t.Errorf("COEP = %q, want require-corp", v)
	}

	if v := w.Header().Get("Permissions-Policy"); v != "microphone=*, speaker-selection=*" {
		t.Errorf("Permissions-Policy = %q, want microphone=*, speaker-selection=*", v)
	}
}

// ---------------------------------------------------------------------------
// SPA / static file server tests
// ---------------------------------------------------------------------------

func TestSPAHandler_ServesIndexAtRoot(t *testing.T) {
	srv := newTestServer()

	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatalf("GET / error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !containsStr(string(body), "SPA") {
		t.Errorf("body does not contain SPA: %s", body)
	}
}

func TestSPAHandler_ServesStaticAsset(t *testing.T) {
	srv := newTestServer()

	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/assets/app.js")
	if err != nil {
		t.Fatalf("GET /assets/app.js error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !containsStr(string(body), "console.log") {
		t.Errorf("body does not contain expected JS: %s", body)
	}
}

func TestSPAHandler_FallsBackToIndex(t *testing.T) {
	srv := newTestServer()

	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	// A client-side route that doesn't map to a static file should return index.html.
	resp, err := http.Get(ts.URL + "/settings")
	if err != nil {
		t.Fatalf("GET /settings error: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}

	body, _ := io.ReadAll(resp.Body)
	if !containsStr(string(body), "SPA") {
		t.Errorf("expected SPA fallback, got: %s", body)
	}
}

// ---------------------------------------------------------------------------
// Route registration test
// ---------------------------------------------------------------------------

func TestHandler_RegistersExpectedRoutes(t *testing.T) {
	srv := newTestServer()

	ts := httptest.NewServer(srv.handler())
	defer ts.Close()

	routes := []struct {
		path string
	}{
		{"/"},
		{"/settings"},
		{"/api/system/info"},
		{"/api/system/processes"},
		{"/api/network/interfaces"},
		{"/api/settings/config"},
		// /api/terminal/ws is intentionally omitted: it is registered, but with
		// term==nil (the test server does not construct a terminal.Manager) it
		// returns 404. The terminal route is exercised in terminal_test.go.
	}

	for _, tc := range routes {
		t.Run(tc.path, func(t *testing.T) {
			resp, err := http.Get(ts.URL + tc.path)
			if err != nil {
				t.Fatalf("GET %s error: %v", tc.path, err)
			}

			resp.Body.Close()
			// Just verify the route is registered (we get a response, not 404).
			if resp.StatusCode == http.StatusNotFound {
				t.Errorf("route %s returned 404, expected it to be registered", tc.path)
			}
		})
	}
}

func containsStr(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && containsSubstr(s, substr))
}

func containsSubstr(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}

	return false
}

// ---------------------------------------------------------------------------
// API reverse-proxy tests
// ---------------------------------------------------------------------------

// upstreamCapture records what the upstream API server saw, so the test can
// verify prefix stripping and pass-through behavior.
type upstreamCapture struct {
	path   atomic.Value // string
	method atomic.Value // string
	host   atomic.Value // string: Host header the upstream saw
	xff    atomic.Value // string: X-Forwarded-For the upstream saw
}

func newProxyUpstream(t *testing.T, body string, header http.Header) (*httptest.Server, *upstreamCapture) {
	t.Helper()

	cap := &upstreamCapture{}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cap.path.Store(r.URL.Path)
		cap.method.Store(r.Method)
		cap.host.Store(r.Host)
		cap.xff.Store(r.Header.Get("X-Forwarded-For"))

		for k, vs := range header {
			for _, v := range vs {
				w.Header().Add(k, v)
			}
		}

		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)

	return srv, cap
}

// The upstream must see its own host and the real client address; a
// browser-supplied X-Forwarded-For is discarded rather than trusted.
func TestBuildAPIProxies_RewritesHostAndXForwardedFor(t *testing.T) {
	upstream, capture := newProxyUpstream(t, `{"ok":true}`, nil)

	rpcProxy, _ := buildAPIProxies(upstream.URL, zerolog.Nop())
	require.NotNil(t, rpcProxy)

	mux := http.NewServeMux()
	mux.Handle("/rpc/", rpcProxy)

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost,
		front.URL+"/rpc/openmanet.foo.v1.FooService/Bar", strings.NewReader("{}"))
	require.NoError(t, err)
	req.Header.Set("X-Forwarded-For", "203.0.113.9")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	require.Equal(t, http.StatusOK, resp.StatusCode)

	upstreamURL, err := url.Parse(upstream.URL)
	require.NoError(t, err)
	assert.Equal(t, upstreamURL.Host, capture.host.Load(), "Host header must be rewritten to the upstream")

	xff, _ := capture.xff.Load().(string)
	assert.NotEmpty(t, xff, "client address must be forwarded")
	assert.NotContains(t, xff, "203.0.113.9", "inbound X-Forwarded-For must not be trusted")
}

func TestBuildAPIProxies_StripsRPCPrefix(t *testing.T) {
	upstream, capture := newProxyUpstream(t, `{"ok":true}`, nil)

	rpcProxy, _ := buildAPIProxies(upstream.URL, zerolog.Nop())
	require.NotNil(t, rpcProxy)

	mux := http.NewServeMux()
	mux.Handle("/rpc/", rpcProxy)

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	resp, err := http.Post(front.URL+"/rpc/openmanet.dashboard.v1.DashboardService/GetDashboardStatus",
		"application/json", strings.NewReader("{}"))
	require.NoError(t, err)

	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	require.NoError(t, err)

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, `{"ok":true}`, string(body))
	assert.Equal(t, "/openmanet.dashboard.v1.DashboardService/GetDashboardStatus", capture.path.Load())
	assert.Equal(t, http.MethodPost, capture.method.Load())
}

func TestBuildAPIProxies_AuthPassthroughWithSetCookie(t *testing.T) {
	upstream, capture := newProxyUpstream(t, `{"username":"root","token":"abc"}`,
		http.Header{"Set-Cookie": []string{"session=abc; Path=/; HttpOnly; SameSite=Lax"}})

	_, authProxy := buildAPIProxies(upstream.URL, zerolog.Nop())
	require.NotNil(t, authProxy)

	mux := http.NewServeMux()
	mux.Handle("/auth/", authProxy)

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	resp, err := http.Post(front.URL+"/auth/login", "application/json",
		strings.NewReader(`{"username":"root","password":""}`))
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/auth/login", capture.path.Load())

	// Set-Cookie from the upstream response must reach the browser
	// unchanged; otherwise the WebUI never establishes a session.
	cookies := resp.Cookies()
	require.Len(t, cookies, 1)
	assert.Equal(t, "session", cookies[0].Name)
	assert.Equal(t, "abc", cookies[0].Value)
	assert.True(t, cookies[0].HttpOnly)
}

func TestBuildAPIProxies_ForwardsAuthorizationHeader(t *testing.T) {
	var gotAuth atomic.Value

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth.Store(r.Header.Get("Authorization"))
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(upstream.Close)

	rpcProxy, _ := buildAPIProxies(upstream.URL, zerolog.Nop())

	mux := http.NewServeMux()
	mux.Handle("/rpc/", rpcProxy)

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	req, err := http.NewRequest(http.MethodPost, front.URL+"/rpc/foo.v1.Service/Method", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer test-token")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "Bearer test-token", gotAuth.Load())
}

func TestBuildAPIProxies_StreamingFlushesIncrementally(t *testing.T) {
	// Server streams two chunks with a wait between them. With FlushInterval=-1
	// the proxy must forward each chunk as it arrives — without flushing,
	// httputil.ReverseProxy buffers and the client never sees the first chunk
	// before the second is written, deadlocking long-lived ConnectRPC streams.
	chunkSent := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		flusher, ok := w.(http.Flusher)
		require.True(t, ok)

		w.Header().Set("Content-Type", "application/connect+json")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("first\n"))

		flusher.Flush()

		<-chunkSent

		_, _ = w.Write([]byte("second\n"))

		flusher.Flush()
	}))
	t.Cleanup(upstream.Close)

	rpcProxy, _ := buildAPIProxies(upstream.URL, zerolog.Nop())

	mux := http.NewServeMux()
	mux.Handle("/rpc/", rpcProxy)

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	resp, err := http.Get(front.URL + "/rpc/foo.v1.Service/Stream")
	require.NoError(t, err)

	defer resp.Body.Close()

	buf := make([]byte, 6)
	_, err = io.ReadFull(resp.Body, buf)
	require.NoError(t, err)
	assert.Equal(t, "first\n", string(buf))

	close(chunkSent)

	rest, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	assert.Equal(t, "second\n", string(rest))
}

func TestBuildAPIProxies_InvalidAddressDisablesProxies(t *testing.T) {
	rpcProxy, authProxy := buildAPIProxies("not-a-url", zerolog.Nop())
	assert.Nil(t, rpcProxy, "rpc proxy must be nil when API address is unparseable")
	assert.Nil(t, authProxy, "auth proxy must be nil when API address is unparseable")
}

func TestBuildAPIProxies_EmptyAddressDisablesProxies(t *testing.T) {
	rpcProxy, authProxy := buildAPIProxies("", zerolog.Nop())
	assert.Nil(t, rpcProxy)
	assert.Nil(t, authProxy)
}

// TestBuildAPIProxies_PreservesContextOnDisconnect verifies the proxy
// propagates client cancellation to the upstream — ensuring streaming
// goroutines on the API server exit when the browser closes the tab.
func TestBuildAPIProxies_PreservesContextOnDisconnect(t *testing.T) {
	upstreamSawDone := make(chan struct{})

	upstream := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
		close(upstreamSawDone)
	}))
	t.Cleanup(upstream.Close)

	rpcProxy, _ := buildAPIProxies(upstream.URL, zerolog.Nop())

	mux := http.NewServeMux()
	mux.Handle("/rpc/", rpcProxy)

	front := httptest.NewServer(mux)
	t.Cleanup(front.Close)

	client := &http.Client{Timeout: 100 * time.Millisecond}

	_, err := client.Get(front.URL + "/rpc/foo.v1.Service/Stream")
	require.Error(t, err) // client times out

	select {
	case <-upstreamSawDone:
	case <-time.After(2 * time.Second):
		t.Fatal("upstream did not observe context cancellation after client disconnect")
	}
}

// TestHandler_ProxyRoutesRegistered verifies the /rpc and /auth proxies
// are wired through the full handler chain (mux + auth middleware + COI
// middleware) and route to the upstream API rather than returning 404.
func TestHandler_ProxyRoutesRegistered(t *testing.T) {
	upstream, capture := newProxyUpstream(t, `{}`, nil)

	rpcProxy, authProxy := buildAPIProxies(upstream.URL, zerolog.Nop())

	srv := newTestServer(func(s *Server) {
		s.rpcProxy = rpcProxy
		s.authProxy = authProxy
	})

	ts := httptest.NewServer(srv.handler())
	t.Cleanup(ts.Close)

	// /auth/check must reach the upstream verbatim — it is the unauth probe
	// the WebUI fires on first paint, and it must not be gated by the
	// frontend auth middleware.
	resp, err := http.Get(ts.URL + "/auth/check")
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/auth/check", capture.path.Load())

	// /rpc/* hits the upstream with the prefix stripped.
	resp, err = http.Post(ts.URL+"/rpc/foo.v1.Service/Method", "application/json", strings.NewReader("{}"))
	require.NoError(t, err)
	resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode)
	assert.Equal(t, "/foo.v1.Service/Method", capture.path.Load())
}
