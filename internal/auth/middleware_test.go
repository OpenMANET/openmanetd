package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/openmanet/openmanetd/internal/auth"
	"github.com/stretchr/testify/assert"
)

func TestAPIAuthMiddleware_Disabled(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewAPIAuthMiddleware(store, false)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/some/rpc", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestAPIAuthMiddleware_MissingCookie(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewAPIAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAPIAuthMiddleware_ValidCookie(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	token := store.Create("alice")

	mw := auth.NewAPIAuthMiddleware(store, true)

	var gotUsername string

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsername = auth.UsernameFromContext(r.Context())

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token, HttpOnly: true, SameSite: http.SameSiteLaxMode}) //nolint:gosec // test fixture; production cookie security is verified separately

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "alice", gotUsername)
}

func TestAPIAuthMiddleware_InvalidCookie(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewAPIAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: "bad-token", HttpOnly: true, SameSite: http.SameSiteLaxMode}) //nolint:gosec // test fixture; production cookie security is verified separately

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called)
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

func TestAPIAuthMiddleware_SkipPaths(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewAPIAuthMiddleware(store, true)

	skipPaths := []string{
		"/auth/login",
		"/auth/check",
		"/openmanet.dashboard.v1.DashboardService/GetDashboardStatus",
		"/openmanet.setup.v1.SetupService/GetSetupStatus",
		"/openmanet.setup.v1.SetupService/ApplySetup",
	}

	for _, path := range skipPaths {
		t.Run(path, func(t *testing.T) {
			called := false
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true

				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.True(t, called, "expected handler to be called for skip path %s", path)
			assert.Equal(t, http.StatusOK, rr.Code)
		})
	}
}

func TestFrontendAuthMiddleware_ProtectedPaths(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewFrontendAuthMiddleware(store, true)

	protectedPaths := []string{
		"/api/system/info",
		"/api/settings/config",
		"/ws",
	}

	for _, path := range protectedPaths {
		t.Run(path, func(t *testing.T) {
			called := false
			handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
				called = true
			}))

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.False(t, called, "expected handler NOT to be called for protected path %s", path)
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})
	}
}

func TestFrontendAuthMiddleware_UnprotectedPaths(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewFrontendAuthMiddleware(store, true)

	unprotectedPaths := []string{
		"/",
		"/login",
		"/settings",
		"/assets/main.js",
		"/favicon.ico",
	}

	for _, path := range unprotectedPaths {
		t.Run(path, func(t *testing.T) {
			called := false
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true

				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodGet, path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.True(t, called, "expected handler to be called for unprotected path %s", path)
		})
	}
}

func TestFrontendAuthMiddleware_ValidCookie(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	token := store.Create("bob")

	mw := auth.NewFrontendAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/system/info", nil)
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token, HttpOnly: true, SameSite: http.SameSiteLaxMode}) //nolint:gosec // test fixture; production cookie security is verified separately

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestAPIAuthMiddleware_BearerToken verifies a valid Bearer header is accepted
// by the API middleware and the username is injected into the context.
func TestAPIAuthMiddleware_BearerToken(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	token := store.Create("carol")

	mw := auth.NewAPIAuthMiddleware(store, true)

	var gotUsername string

	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotUsername = auth.UsernameFromContext(r.Context())

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.Equal(t, http.StatusOK, rr.Code)
	assert.Equal(t, "carol", gotUsername)
}

// TestAPIAuthMiddleware_BearerCaseInsensitive verifies the "Bearer " prefix
// match is case-insensitive, matching RFC 7235 §2.1.
func TestAPIAuthMiddleware_BearerCaseInsensitive(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	token := store.Create("carol")

	mw := auth.NewAPIAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	req.Header.Set("Authorization", "bearer "+token)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestAPIAuthMiddleware_InvalidBearer verifies a Bearer header with a bad
// token is rejected and the cookie is not silently substituted.
func TestAPIAuthMiddleware_InvalidBearer(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	validToken := store.Create("carol")

	mw := auth.NewAPIAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(_ http.ResponseWriter, _ *http.Request) {
		called = true
	}))

	// Bearer wins over the valid cookie, so the request must be rejected
	// even though the cookie alone would have succeeded.
	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	req.Header.Set("Authorization", "Bearer invalid-token")
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: validToken, HttpOnly: true, SameSite: http.SameSiteLaxMode}) //nolint:gosec // test fixture; production cookie security is verified separately

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.False(t, called, "Bearer must take precedence; invalid Bearer must fail even with a valid cookie")
	assert.Equal(t, http.StatusUnauthorized, rr.Code)
}

// TestAPIAuthMiddleware_MalformedAuthorizationHeader_FallsBackToCookie
// verifies a malformed Authorization header (missing "Bearer " prefix) is
// ignored, falling back to the session cookie.
func TestAPIAuthMiddleware_MalformedAuthorizationHeader_FallsBackToCookie(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	token := store.Create("carol")

	mw := auth.NewAPIAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodPost, "/some.Service/Method", nil)
	req.Header.Set("Authorization", "Basic YWxpY2U6c2VjcmV0")                                                               // not Bearer
	req.AddCookie(&http.Cookie{Name: auth.SessionCookieName, Value: token, HttpOnly: true, SameSite: http.SameSiteLaxMode}) //nolint:gosec // test fixture; production cookie security is verified separately

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

func TestFrontendAuthMiddleware_BearerToken(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	token := store.Create("dave")

	mw := auth.NewFrontendAuthMiddleware(store, true)

	called := false
	handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true

		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/system/info", nil)
	req.Header.Set("Authorization", "Bearer "+token)

	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	assert.True(t, called)
	assert.Equal(t, http.StatusOK, rr.Code)
}

// TestAPIAuthMiddleware_MeshJoinRequiresSession pins that the mesh join
// RPCs (which carry passphrases) are never on the skip list.
func TestAPIAuthMiddleware_MeshJoinRequiresSession(t *testing.T) {
	store := auth.NewSessionStore(time.Hour, 16)
	mw := auth.NewAPIAuthMiddleware(store, true)

	for _, path := range []string{
		"/openmanet.mesh_join.v1.MeshJoinService/GetMeshJoinQR",
		"/openmanet.mesh_join.v1.MeshJoinService/ApplyMeshJoin",
	} {
		t.Run(path, func(t *testing.T) {
			called := false
			handler := mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				called = true

				w.WriteHeader(http.StatusOK)
			}))

			req := httptest.NewRequest(http.MethodPost, path, nil)
			rr := httptest.NewRecorder()
			handler.ServeHTTP(rr, req)

			assert.False(t, called, "mesh join RPCs must not bypass auth")
			assert.Equal(t, http.StatusUnauthorized, rr.Code)
		})
	}
}
