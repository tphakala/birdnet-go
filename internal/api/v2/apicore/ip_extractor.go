// ip_extractor.go provides the client IP extractor for Echo.
//
// The extractor reports the client the rate limiters resolve (see
// proxytrust.Checker.RateLimitClient). Forwarded headers that disagree with each
// other, or that a configured proxy cannot verify, are not recorded, and an
// untrusted peer's headers are ignored. A client that an unlisted loopback,
// link-local or private peer names consistently (a lone CF-Connecting-IP,
// X-Forwarded-For or X-Real-IP with a usable address included) is recorded as
// named, so such a host can still choose the address that appears in logs. The trust primitives live in
// internal/security/proxytrust; authentication decisions use its stricter
// AuthClientIP rather than this extractor.
package apicore

import (
	"net/http"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

// Canonical map keys for the forwarded headers. http.Header stores keys in
// canonical MIME form, so direct map lookups (used on the per-request untrusted
// path) must use the canonical spelling. "CF-Connecting-IP" is not already
// canonical, so precomputing it avoids re-canonicalizing on every request.
var (
	canonicalCFConnectingIP = http.CanonicalHeaderKey(proxytrust.HeaderCFConnectingIP)
	canonicalXForwardedFor  = http.CanonicalHeaderKey(echo.HeaderXForwardedFor)
	canonicalXRealIP        = http.CanonicalHeaderKey(echo.HeaderXRealIP)
)

// resolveTrustedProxyChecker returns the checker for the current configuration
// from cache, rebuilding it only when the Security.TrustedProxies list changes.
func resolveTrustedProxyChecker(cache *proxytrust.Cache, getSettings func() *conf.Settings) *proxytrust.Checker {
	var settings *conf.Settings
	if getSettings != nil {
		settings = getSettings()
	}
	return cache.ResolveSettings(settings)
}

// attributionIP is the Echo IPExtractor behind echo.Context.RealIP: the client
// address that logs and the tunnel label record. It resolves the request the way
// the rate limiters do (see proxytrust.Checker.RateLimitClient), so forwarded
// headers that disagree or that a configured proxy cannot verify are not
// recorded:
//
//   - A direct request, one a configured proxy verified, and one an unconfigured
//     local proxy made consistent claims for (a lone CF-Connecting-IP, X-Forwarded-For or
//     X-Real-IP with a usable address included), yield the resolved client.
//   - A request whose forwarded headers do not verify yields the peer address.
//   - An untrusted peer yields its own address, and its forwarded headers are
//     ignored, leaving a DEBUG breadcrumb naming the peer.
//
// The address is for attribution only, never authentication or authorization
// (authentication uses proxytrust.Checker.AuthClientIP). The trusted-proxy list
// is read from the live settings on every call, so a settings save takes effect
// on the next request.
func (c *Core) attributionIP(req *http.Request) string {
	checker := c.ProxyChecker()
	res := checker.RateLimitClient(req)
	if res.Source == proxytrust.SourceDirect && !checker.TrustsPeer(res.Peer) {
		// Untrusted peer: forwarded headers are ignored. Leave a DEBUG
		// breadcrumb naming the peer so a misconfigured trusted proxy is
		// self-diagnosing, without flooding logs (a public instance gets
		// constant scanner header noise, so this stays at DEBUG).
		logIgnoredForwardedHeader(req, res.PeerHost)
	}
	if res.Client != nil {
		return res.Client.String()
	}
	return res.PeerHost
}

// logIgnoredForwardedHeader emits a DEBUG breadcrumb when forwarded client-IP
// headers arrive from an untrusted peer and are therefore ignored, naming the
// peer address an operator would add to security.trustedproxies. It is DEBUG
// (not WARN) on purpose: a directly-exposed instance receives constant scanner
// header noise, so a louder level would flood logs and alarm users. Fields are
// only built when a forwarded header is actually present, keeping the
// no-header untrusted path (ordinary direct clients) allocation-free.
func logIgnoredForwardedHeader(req *http.Request, peerHost string) {
	h := req.Header
	cfPresent := headerNonEmpty(h, canonicalCFConnectingIP)
	xffPresent := headerNonEmpty(h, canonicalXForwardedFor)
	xriPresent := headerNonEmpty(h, canonicalXRealIP)
	if !cfPresent && !xffPresent && !xriPresent {
		return // fast path: no forwarded headers, zero allocation
	}
	present := make([]string, 0, 3)
	if cfPresent {
		present = append(present, proxytrust.HeaderCFConnectingIP)
	}
	if xffPresent {
		present = append(present, echo.HeaderXForwardedFor)
	}
	if xriPresent {
		present = append(present, echo.HeaderXRealIP)
	}
	GetLogger().Debug("Ignoring forwarded client-IP header from untrusted peer; if this peer is your reverse proxy, add its CIDR (or \"cloudflare\") to security.trustedproxies",
		logger.String("peer", peerHost),
		logger.String("headers", strings.Join(present, ",")),
	)
}

// headerNonEmpty reports whether a canonical header key has a non-empty first
// value, via a direct map lookup that avoids the key canonicalization (and
// allocation) http.Header.Get performs for a non-canonical key.
func headerNonEmpty(h http.Header, canonicalKey string) bool {
	values := h[canonicalKey]
	return len(values) > 0 && values[0] != ""
}
