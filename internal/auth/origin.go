package auth

import (
	"net/http"
	"net/netip"
	"net/url"
	"strings"
)

// IsTrustedOrigin reports whether a browser request carrying the given
// Origin header value may act on behalf of the operator: a credentialed CORS
// request to the API server or a WebSocket upgrade on the frontend server.
//
// The policy trusts LAN origins:
//   - an empty origin (non-browser clients such as curl send none);
//   - an origin whose host and port equal the request's Host header;
//   - IP literals in loopback, RFC 1918, link-local, IPv6 ULA, and the
//     RFC 6598 range Tailscale uses;
//   - single-label hostnames and names under .lan, .local, and .home.arpa.
//
// Everything else, including the opaque "null" origin, is rejected. This
// check does not defend against DNS rebinding, where a hostile name resolves
// to a LAN address and the request's Host matches the origin.
func IsTrustedOrigin(origin string, r *http.Request) bool {
	if origin == "" {
		return true
	}

	u, err := url.Parse(origin)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return false
	}

	if strings.EqualFold(u.Host, r.Host) {
		return true
	}

	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return false
	}

	if addr, err := netip.ParseAddr(host); err == nil {
		return isLANAddr(addr.Unmap())
	}

	// Single-label names and the suffixes used on local networks; OpenWrt's
	// dnsmasq serves ".lan" by default.
	return !strings.Contains(host, ".") ||
		strings.HasSuffix(host, ".lan") ||
		strings.HasSuffix(host, ".local") ||
		strings.HasSuffix(host, ".home.arpa")
}

// isLANAddr reports whether addr is a loopback, private (RFC 1918 or IPv6
// ULA), link-local, or RFC 6598 shared address. Tailscale assigns node
// addresses from the RFC 6598 range, so BLOS peers reach the WebUI from it.
func isLANAddr(addr netip.Addr) bool {
	cgnat := netip.PrefixFrom(netip.AddrFrom4([4]byte{100, 64, 0, 0}), 10)

	return addr.IsLoopback() ||
		addr.IsPrivate() ||
		addr.IsLinkLocalUnicast() ||
		cgnat.Contains(addr)
}
