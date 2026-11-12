package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
)

// ctxKey is an unexported type for context keys in this package.
type ctxKey int

const (
	ctxKeyUsername ctxKey = iota
	ctxKeySession
)

// SessionCookieName is the name of the HTTP session cookie.
const SessionCookieName = "session"

// NewAPIAuthMiddleware returns HTTP middleware that enforces session
// authentication for the ConnectRPC API server. When enabled is false the
// middleware is a no-op. The session token may be supplied as the "session"
// cookie (browser clients) or an "Authorization: Bearer <token>" header
// (curl, scripts, ConnectRPC clients in other languages). When both are
// present the Authorization header wins. The following paths are always
// allowed without a valid session:
//   - POST /auth/login
//   - GET  /auth/check
//   - POST /openmanet.dashboard.v1.DashboardService/GetDashboardStatus
func NewAPIAuthMiddleware(store *SessionStore, enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled || isAPISkipPath(r) {
				next.ServeHTTP(w, r)

				return
			}

			token := extractSessionToken(r)
			if token == "" {
				writeUnauthorized(w)

				return
			}

			sess, ok := store.Validate(token)
			if !ok {
				writeUnauthorized(w)

				return
			}

			next.ServeHTTP(w, r.WithContext(contextWithSession(r.Context(), sess)))
		})
	}
}

// NewFrontendAuthMiddleware returns HTTP middleware that enforces session
// authentication for the frontend server. Only requests to /api/* and /ws
// require a valid session. All other paths (SPA shell, static assets) pass
// through. When enabled is false the middleware is a no-op. Accepts the
// session token via cookie or Authorization: Bearer header — same rules as
// NewAPIAuthMiddleware.
func NewFrontendAuthMiddleware(store *SessionStore, enabled bool) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if !enabled || !isFrontendProtectedPath(r) {
				next.ServeHTTP(w, r)

				return
			}

			token := extractSessionToken(r)
			if token == "" {
				writeUnauthorized(w)

				return
			}

			sess, ok := store.Validate(token)
			if !ok {
				writeUnauthorized(w)

				return
			}

			next.ServeHTTP(w, r.WithContext(contextWithSession(r.Context(), sess)))
		})
	}
}

// extractSessionToken pulls the session token from the request. Precedence:
//  1. Authorization: Bearer <token> header — wins when present so a stale
//     browser cookie cannot mask an explicit header.
//  2. "session" cookie.
//
// Returns "" when neither is present or the header is malformed.
func extractSessionToken(r *http.Request) string {
	if h := r.Header.Get("Authorization"); h != "" {
		const prefix = "Bearer "
		if len(h) > len(prefix) && strings.EqualFold(h[:len(prefix)], prefix) {
			if token := strings.TrimSpace(h[len(prefix):]); token != "" {
				return token
			}
		}
	}

	if cookie, err := r.Cookie(SessionCookieName); err == nil {
		return cookie.Value
	}

	return ""
}

// UsernameFromContext returns the authenticated username stored in ctx, or an
// empty string if no session is present (e.g. when auth is disabled).
func UsernameFromContext(ctx context.Context) string {
	v, _ := ctx.Value(ctxKeyUsername).(string)

	return v
}

// SessionFromContext returns the session the auth middleware validated for
// this request, or nil when there is none (auth disabled, or a path that
// skips authentication).
func SessionFromContext(ctx context.Context) *Session {
	sess, _ := ctx.Value(ctxKeySession).(*Session)

	return sess
}

// contextWithSession returns a copy of ctx carrying sess and its username.
func contextWithSession(ctx context.Context, sess *Session) context.Context {
	return ContextWithUsername(context.WithValue(ctx, ctxKeySession, sess), sess.Username)
}

// ContextWithUsername returns a copy of ctx with the authenticated username
// attached. Used by the auth middleware and by tests that exercise handlers
// requiring a session without running the full middleware chain.
func ContextWithUsername(ctx context.Context, username string) context.Context {
	return context.WithValue(ctx, ctxKeyUsername, username)
}

// isAPISkipPath returns true for paths that are always allowed on the API
// server without authentication. The setup wizard endpoints are listed
// here so the frontend can poll status and apply a profile before any
// admin password has been set; the SetupService handler enforces its own
// enabled/complete gate as defense-in-depth.
func isAPISkipPath(r *http.Request) bool {
	switch r.URL.Path {
	case "/auth/login", "/auth/check":
		return true
	case "/openmanet.dashboard.v1.DashboardService/GetDashboardStatus":
		return true
	case "/openmanet.setup.v1.SetupService/GetSetupStatus",
		"/openmanet.setup.v1.SetupService/ApplySetup":
		return true
	}

	return false
}

// isFrontendProtectedPath returns true for frontend server paths that require
// a valid session.
func isFrontendProtectedPath(r *http.Request) bool {
	p := r.URL.Path
	if len(p) >= 5 && p[:5] == "/api/" {
		return true
	}

	return p == "/ws"
}

// writeUnauthorized sends a 401 JSON response.
func writeUnauthorized(w http.ResponseWriter) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusUnauthorized)
	_ = json.NewEncoder(w).Encode(map[string]string{errorKey: "unauthorized"})
}
