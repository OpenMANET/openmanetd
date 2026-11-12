//go:build integration

package server_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openmanet/openmanetd/internal/config"
	"github.com/openmanet/openmanetd/internal/openmanet/server"
	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const dashboardStatusPath = "/openmanet.dashboard.v1.DashboardService/GetDashboardStatus"

// newCORSTestServer serves the API server's real handler chain (CORS, auth,
// ConnectRPC mux) with auth disabled, the configuration where an origin
// check is the only cross-site barrier.
func newCORSTestServer(t *testing.T) *httptest.Server {
	t.Helper()

	api := server.NewAPIServer(server.APIServer{
		Cfg: config.NewWithoutWatch(viper.New()),
		Log: zerolog.Nop(),
	})

	srv := httptest.NewServer(api.ApiServer.Handler)
	t.Cleanup(srv.Close)

	return srv
}

func sendPreflight(t *testing.T, srv *httptest.Server, origin string) *http.Response {
	t.Helper()

	req, err := http.NewRequestWithContext(context.Background(), http.MethodOptions, srv.URL+dashboardStatusPath, nil)
	require.NoError(t, err)
	req.Header.Set("Origin", origin)
	req.Header.Set("Access-Control-Request-Method", http.MethodPost)
	req.Header.Set("Access-Control-Request-Headers", "content-type")
	req.Header.Set("Access-Control-Request-Private-Network", "true")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })

	return resp
}

func TestAPIServerCORS_untrustedOriginPreflightRejected(t *testing.T) {
	srv := newCORSTestServer(t)

	resp := sendPreflight(t, srv, "https://example.com")

	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Private-Network"))
}

func TestAPIServerCORS_lanOriginPreflightAllowed(t *testing.T) {
	srv := newCORSTestServer(t)

	const origin = "http://10.41.1.1:8081"

	resp := sendPreflight(t, srv, origin)

	assert.Equal(t, origin, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Credentials"))
	assert.Equal(t, "true", resp.Header.Get("Access-Control-Allow-Private-Network"))
}

func TestAPIServerCORS_untrustedOriginGetsNoCORSHeadersOnRequest(t *testing.T) {
	srv := newCORSTestServer(t)

	req, err := http.NewRequestWithContext(context.Background(), http.MethodPost, srv.URL+dashboardStatusPath, http.NoBody)
	require.NoError(t, err)
	req.Header.Set("Origin", "https://example.com")
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	require.NoError(t, err)

	defer resp.Body.Close()

	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Origin"))
	assert.Empty(t, resp.Header.Get("Access-Control-Allow-Credentials"))
}
