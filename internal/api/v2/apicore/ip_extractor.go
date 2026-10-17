// ip_extractor.go provides a trusted-proxy-gated client IP extractor for Echo.
//
// The extractor only honors proxy-supplied client-IP headers (CF-Connecting-IP,
// X-Forwarded-For, X-Real-IP) when the immediate connection peer is a trusted
// reverse proxy. On a directly-exposed instance this prevents a client from
// spoofing its source IP, which feeds rate limiting and logs. The trust
// primitives live in internal/security/proxytrust; authentication decisions use
// its stricter AuthClientIP rather than this extractor.
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

// newTrustedProxyIPExtractor returns an Echo IPExtractor that honors proxy
// client-IP headers (CF-Connecting-IP, then X-Forwarded-For, then X-Real-IP)
// only when the immediate peer is a trusted proxy, otherwise falling back to the
// real connection address. The trusted set is read from Security.TrustedProxies
// via getSettings on each request so changes take effect without a restart.
func newTrustedProxyIPExtractor(getSettings func() *conf.Settings) echo.IPExtractor {
	var cache proxytrust.Cache
	return func(req *http.Request) string {
		peerIP, peerHost := proxytrust.PeerAddr(req)

		// Only honor forwarded client-IP headers from a trusted proxy peer.
		if checker := resolveTrustedProxyChecker(&cache, getSettings); checker.TrustsPeer(peerIP) {
			if ip := proxytrust.ParseHeaderIP(req.Header.Get(proxytrust.HeaderCFConnectingIP)); ip != "" {
				return ip
			}
			if ip := checker.ClientIPFromXFF(req.Header.Get(echo.HeaderXForwardedFor)); ip != "" {
				return ip
			}
			if ip := proxytrust.ParseHeaderIP(req.Header.Get(echo.HeaderXRealIP)); ip != "" {
				return ip
			}
		} else {
			// Untrusted peer: forwarded headers are ignored. Leave a DEBUG
			// breadcrumb naming the peer so a misconfigured trusted proxy is
			// self-diagnosing, without flooding logs (a public instance gets
			// constant scanner header noise, so this stays at DEBUG).
			logIgnoredForwardedHeader(req, peerHost)
		}

		// Untrusted peer, or trusted peer with no forwarded headers: use the
		// real peer address.
		if peerIP != nil {
			return peerIP.String()
		}
		return peerHost
	}
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
