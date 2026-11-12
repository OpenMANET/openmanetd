package auth_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/openmanet/openmanetd/internal/auth"
	"github.com/stretchr/testify/assert"
)

func TestIsTrustedOrigin(t *testing.T) {
	// 192.0.2.1 (TEST-NET-1) is not a trusted address on its own, so rows
	// using the default host pass or fail on the origin alone.
	const defaultHost = "192.0.2.1:8087"

	tests := []struct {
		name   string
		origin string
		host   string
		want   bool
	}{
		{name: "no origin header", origin: "", want: true},
		{name: "rfc1918 10/8 with port", origin: "http://10.41.1.1:8081", want: true},
		{name: "rfc1918 192.168/16", origin: "http://192.168.1.20", want: true},
		{name: "rfc1918 172.16/12", origin: "https://172.16.0.5", want: true},
		{name: "outside 172.16/12", origin: "http://172.32.0.1", want: false},
		{name: "ipv4 loopback", origin: "http://127.0.0.1:8081", want: true},
		{name: "ipv6 loopback", origin: "http://[::1]:8081", want: true},
		{name: "ipv4 link-local", origin: "http://169.254.10.1", want: true},
		{name: "ipv6 link-local", origin: "http://[fe80::1]", want: true},
		{name: "ipv6 ula", origin: "http://[fd00::1]", want: true},
		{name: "ipv4-mapped private", origin: "http://[::ffff:10.0.0.1]", want: true},
		{name: "cgnat tailscale", origin: "http://100.101.102.103", want: true},
		{name: "above cgnat range", origin: "http://100.128.0.1", want: false},
		{name: "public ipv4", origin: "http://8.8.8.8", want: false},
		{name: "public ipv6", origin: "http://[2001:4860::8888]", want: false},
		{name: "unspecified ipv4", origin: "http://0.0.0.0:8081", want: false},
		{name: "lan suffix", origin: "http://openmanet.lan", want: true},
		{name: "lan suffix mixed case", origin: "http://OpenMANET.LAN:8081", want: true},
		{name: "lan suffix trailing dot", origin: "http://openmanet.lan.", want: true},
		{name: "local suffix", origin: "http://node-1.local:8081", want: true},
		{name: "home.arpa suffix", origin: "https://router.home.arpa", want: true},
		{name: "single label", origin: "http://openmanet:8081", want: true},
		{name: "localhost", origin: "http://localhost:5173", want: true},
		{name: "public hostname", origin: "https://example.com", want: false},
		{name: "lan as subdomain label", origin: "https://lan.example.com", want: false},
		{name: "single label ending in lan", origin: "https://evillan", want: true},
		{name: "lan lookalike suffix", origin: "https://example.evil-lan", want: false},
		{name: "null origin", origin: "null", want: false},
		{name: "file scheme", origin: "file://", want: false},
		{name: "extension scheme", origin: "chrome-extension://abcdef", want: false},
		{name: "not a url", origin: "%zz", want: false},
		{name: "same host as request", origin: "https://node.tail1234.ts.net", host: "node.tail1234.ts.net", want: true},
		{name: "same host and port as request", origin: "http://mesh.example.org:8081", host: "MESH.example.org:8081", want: true},
		{name: "same name different port", origin: "http://mesh.example.org:9999", host: "mesh.example.org:8081", want: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := httptest.NewRequest(http.MethodGet, "/", nil)

			r.Host = defaultHost
			if tt.host != "" {
				r.Host = tt.host
			}

			assert.Equal(t, tt.want, auth.IsTrustedOrigin(tt.origin, r))
		})
	}
}
