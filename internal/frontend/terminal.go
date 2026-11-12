package frontend

import (
	"context"
	"net/http"

	"github.com/openmanet/openmanetd/internal/auth"
)

// handleTerminalWS upgrades the request to a WebSocket and hands it to the
// terminal manager. When s.term is nil (feature disabled in config), the
// route is reported as not found so it does not appear in the API surface.
//
// Authentication is enforced by NewFrontendAuthMiddleware before this
// handler is reached: any /api/* path requires a valid session when auth
// is enabled. The terminal then runs under auth.WithSessionLifetime, so it
// ends when that session logs out, is evicted, or expires. The terminal
// package also ends sessions whose client stops answering pings.
func (s *Server) handleTerminalWS(w http.ResponseWriter, r *http.Request) {
	if s.term == nil {
		http.NotFound(w, r)

		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.log.Error().Err(err).Msg("terminal: websocket upgrade failed")

		return
	}
	defer conn.Close()

	ctx := r.Context()

	if sess := auth.SessionFromContext(ctx); sess != nil {
		var cancel context.CancelFunc

		ctx, cancel = auth.WithSessionLifetime(ctx, sess)
		defer cancel()
	}

	_ = s.term.Run(ctx, conn)
}
