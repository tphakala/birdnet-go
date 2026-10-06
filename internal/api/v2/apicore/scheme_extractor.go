// scheme_extractor.go provides a trusted-proxy-gated request scheme extractor
// for Echo.
//
// Since Echo v4.16, Context.Scheme() honors X-Forwarded-Proto and its siblings
// only when the direct peer is loopback, link-local, private or a unix socket.
// The Secure middleware relies on Scheme() to decide whether to send HSTS, so a
// reverse proxy on a public or 100.64.0.0/10 (CGNAT, Tailscale IPv4) address
// would silently lose HSTS. This extractor widens the peer trust to the same set
// the client-IP extractor uses (see ip_extractor.go), including the
// hot-reloadable Security.TrustedProxies list and its "cloudflare" preset.
package apicore

import (
	"net/http"
	"sync/atomic"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Leading bytes of a unix-socket RemoteAddr as reported by net/http: "@" for a
// Linux unnamed or abstract socket, "/" for the path of a bound socket.
const (
	unixSocketAbstractPrefix = '@'
	unixSocketPathPrefix     = '/'
)

// isUnixSocketPeer reports whether remoteAddr denotes a unix-socket peer. net/http
// reports those as "", "@" or "@name", or a socket path. Echo v4.16 trusts such
// peers by default (scheme.go isTrustedPeer), so this mirrors it to never trust
// less than stock Echo.
func isUnixSocketPeer(remoteAddr string) bool {
	return remoteAddr == "" ||
		remoteAddr[0] == unixSocketAbstractPrefix ||
		remoteAddr[0] == unixSocketPathPrefix
}

// newTrustedProxySchemeExtractor returns an Echo SchemeExtractor that honors
// proxy scheme headers (X-Forwarded-Proto, X-Forwarded-Protocol,
// X-Forwarded-Ssl, X-Url-Scheme) only when the immediate peer is trusted, and
// otherwise reports the connection scheme. A direct TLS connection is always
// https. Header parsing is delegated to Echo.
//
// The trusted set is loopback, link-local and private peers, unix-socket peers,
// and Security.TrustedProxies (including the cloudflare preset), read per request
// from getSettings so changes hot-reload. Apart from unix-socket peers, which
// only matter for the scheme, it must trust exactly the peers that
// newTrustedProxyIPExtractor trusts, so one list governs client IP and scheme.
func newTrustedProxySchemeExtractor(getSettings func() *conf.Settings) echo.SchemeExtractor {
	var cache atomic.Pointer[trustedProxyChecker]
	fromHeaders := echo.LegacySchemeExtractor()
	direct := echo.ExtractSchemeDirect()

	return func(req *http.Request) string {
		if req.TLS != nil {
			return direct(req)
		}
		if isUnixSocketPeer(req.RemoteAddr) {
			return fromHeaders(req)
		}
		peerIP, _ := peerAddrFromRequest(req)
		if resolveTrustedProxyChecker(&cache, getSettings).trust(peerIP) {
			return fromHeaders(req)
		}
		return direct(req)
	}
}
