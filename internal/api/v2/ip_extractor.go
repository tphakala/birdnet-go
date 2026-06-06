// ip_extractor.go provides a trusted-proxy-gated client IP extractor for Echo.
//
// The extractor only honors proxy-supplied client-IP headers (CF-Connecting-IP,
// X-Forwarded-For, X-Real-IP) when the immediate connection peer is a trusted
// reverse proxy. On a directly-exposed instance this prevents a client from
// spoofing its source IP, which feeds rate limiting, ban/allow lists, and logs.
package api

import (
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// headerCFConnectingIP is Cloudflare's client-IP header. Echo has no constant
// for it (it only defines X-Forwarded-For and X-Real-IP), so it is named here.
const headerCFConnectingIP = "CF-Connecting-IP"

// cloudflareEdgeCIDRs lists Cloudflare's published proxy IP ranges
// (https://www.cloudflare.com/ips/). These ranges are stable and change rarely;
// kept in sync manually. Expanded from the Security.TrustedProxies "cloudflare"
// preset (conf.TrustedProxyCloudflarePreset).
var cloudflareEdgeCIDRs = []string{
	// IPv4
	"173.245.48.0/20",
	"103.21.244.0/22",
	"103.22.200.0/22",
	"103.31.4.0/22",
	"141.101.64.0/18",
	"108.162.192.0/18",
	"190.93.240.0/20",
	"188.114.96.0/20",
	"197.234.240.0/22",
	"198.41.128.0/17",
	"162.158.0.0/15",
	"104.16.0.0/13",
	"104.24.0.0/14",
	"172.64.0.0/13",
	"131.0.72.0/22",
	// IPv6
	"2400:cb00::/32",
	"2606:4700::/32",
	"2803:f800::/32",
	"2405:b500::/32",
	"2405:8100::/32",
	"2a06:98c0::/29",
	"2c0f:f248::/32",
}

// trustedProxyChecker decides whether an immediate peer address is a trusted
// reverse proxy whose forwarded client-IP headers may be honored. Loopback,
// link-local, and private (RFC1918/ULA) addresses are always trusted, matching
// Echo's default TrustOption behavior, plus any operator-configured CIDR ranges.
type trustedProxyChecker struct {
	// raw is the Security.TrustedProxies slice this checker was built from. It is
	// used to detect configuration changes for hot-reload so the CIDR list is not
	// re-parsed on every request.
	raw    []string
	ranges []*net.IPNet
}

// buildTrustedProxyChecker parses the configured trusted-proxy entries into a
// checker. Blank entries are skipped and the reserved "cloudflare" preset
// expands to Cloudflare's published ranges. Invalid CIDRs are silently skipped
// here (conf validation rejects them at load time; this is defense in depth).
func buildTrustedProxyChecker(trustedProxies []string) *trustedProxyChecker {
	tc := &trustedProxyChecker{raw: slices.Clone(trustedProxies)}
	for _, entry := range trustedProxies {
		trimmed := strings.TrimSpace(entry)
		switch {
		case trimmed == "":
			continue
		case strings.EqualFold(trimmed, conf.TrustedProxyCloudflarePreset):
			tc.appendCIDRs(cloudflareEdgeCIDRs)
		default:
			tc.appendCIDRs([]string{trimmed})
		}
	}
	return tc
}

// appendCIDRs parses and appends valid trusted-proxy entries, skipping any that
// fail to parse. Each entry may be a CIDR or a bare IP (a single host).
func (tc *trustedProxyChecker) appendCIDRs(entries []string) {
	for _, entry := range entries {
		if network, ok := parseProxyCIDR(entry); ok {
			tc.ranges = append(tc.ranges, network)
		}
	}
}

// parseProxyCIDR parses a trusted-proxy entry as either a CIDR or a bare IP
// (treated as a single-host /32 for IPv4 or /128 for IPv6). Returns false if the
// entry is neither. net.ParseCIDR normalizes the returned network for both forms.
func parseProxyCIDR(entry string) (*net.IPNet, bool) {
	entry = strings.TrimSpace(entry)
	if _, network, err := net.ParseCIDR(entry); err == nil {
		return network, true
	}
	if ip := net.ParseIP(entry); ip != nil {
		suffix := "/32"
		if ip.To4() == nil {
			suffix = "/128"
		}
		if _, network, err := net.ParseCIDR(entry + suffix); err == nil {
			return network, true
		}
	}
	return nil, false
}

// trust reports whether ip is a trusted proxy peer.
func (tc *trustedProxyChecker) trust(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	for _, network := range tc.ranges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// trustForwardedHop reports whether an X-Forwarded-For hop is an explicitly
// configured trusted proxy. It is deliberately NARROWER than trust(): it does
// not trust loopback/link-local/private by default, only operator-configured
// CIDRs (including the cloudflare preset, whose ranges are in tc.ranges).
//
// The asymmetry is intentional. The immediate peer (trust()) is the real TCP
// socket and cannot be forged, so trusting private/loopback peers by default is
// safe and keeps home-LAN setups zero-config. An X-Forwarded-For hop is
// attacker-supplied, and a real client is frequently on a private address; if
// private hops were skipped as proxies, a LAN client could prepend a forged
// entry and have its real (private) hop skipped, spoofing its attributed IP.
func (tc *trustedProxyChecker) trustForwardedHop(ip net.IP) bool {
	if ip == nil {
		return false
	}
	for _, network := range tc.ranges {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// clientIPFromXFF resolves the real client IP from an X-Forwarded-For header,
// assuming the immediate peer is already known to be a trusted proxy. It walks
// the hop chain from the rightmost (closest to this server) entry leftward,
// skipping hops that are explicitly configured proxies (trustForwardedHop), and
// returns the first non-proxy address: the real external client. If every hop
// is a configured proxy, the leftmost entry is returned as the original client.
//
// Walking from the right, and skipping only configured proxies, defeats
// spoofing: a client cannot forge a leftmost entry, because proxies append the
// address they actually saw, so the attacker's real address sits to the right
// of any value they injected and is never skipped. Multi-hop chains with
// internal proxies therefore require those proxies' CIDRs in
// security.trustedproxies for correct attribution. Returns "" for an empty or
// malformed header, letting the caller fall back to the next source.
func (tc *trustedProxyChecker) clientIPFromXFF(xff string) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	for i, part := range slices.Backward(parts) {
		ipStr := parseIPFromHeader(strings.TrimSpace(part))
		if ipStr == "" {
			// Malformed hop: nothing further left can be trusted; fall back.
			return ""
		}
		if !tc.trustForwardedHop(net.ParseIP(ipStr)) {
			return ipStr // nearest non-proxy hop = real client
		}
		if i == 0 {
			// All hops are configured proxies; the leftmost is the original client.
			return ipStr
		}
	}
	return ""
}

// resolveTrustedProxyChecker returns the checker for the current configuration,
// rebuilding and caching it only when the Security.TrustedProxies list changes.
// This keeps the trusted set hot-reloadable without re-parsing CIDRs per request.
func resolveTrustedProxyChecker(cache *atomic.Pointer[trustedProxyChecker], getSettings func() *conf.Settings) *trustedProxyChecker {
	var trustedProxies []string
	if getSettings != nil {
		if settings := getSettings(); settings != nil {
			trustedProxies = settings.Security.TrustedProxies
		}
	}
	if cached := cache.Load(); cached != nil && slices.Equal(cached.raw, trustedProxies) {
		return cached
	}
	checker := buildTrustedProxyChecker(trustedProxies)
	cache.Store(checker)
	return checker
}

// peerAddrFromRequest extracts the immediate peer address from req.RemoteAddr,
// returning the parsed IP (nil if unparseable) and the raw host string for
// fallback. An IPv6 zone identifier, if present, is stripped before parsing.
func peerAddrFromRequest(req *http.Request) (peerIP net.IP, host string) {
	var err error
	if host, _, err = net.SplitHostPort(req.RemoteAddr); err != nil {
		// No port (e.g. a bare "[::1]" or "127.0.0.1"). Strip IPv6 brackets so
		// net.ParseIP can parse it, otherwise a bracketed loopback/private peer
		// would fail to parse and be treated as untrusted.
		host = strings.TrimSuffix(strings.TrimPrefix(req.RemoteAddr, "["), "]")
	}
	if before, _, found := strings.Cut(host, "%"); found {
		host = before
	}
	peerIP = net.ParseIP(host)
	return peerIP, host
}

// newTrustedProxyIPExtractor returns an Echo IPExtractor that honors proxy
// client-IP headers (CF-Connecting-IP, then X-Forwarded-For, then X-Real-IP)
// only when the immediate peer is a trusted proxy, otherwise falling back to the
// real connection address. The trusted set is read from Security.TrustedProxies
// via getSettings on each request so changes take effect without a restart.
func newTrustedProxyIPExtractor(getSettings func() *conf.Settings) echo.IPExtractor {
	var cache atomic.Pointer[trustedProxyChecker]
	return func(req *http.Request) string {
		peerIP, peerHost := peerAddrFromRequest(req)

		// Only honor forwarded client-IP headers from a trusted proxy peer.
		if checker := resolveTrustedProxyChecker(&cache, getSettings); checker.trust(peerIP) {
			if ip := parseIPFromHeader(req.Header.Get(headerCFConnectingIP)); ip != "" {
				return ip
			}
			if ip := checker.clientIPFromXFF(req.Header.Get(echo.HeaderXForwardedFor)); ip != "" {
				return ip
			}
			if ip := parseIPFromHeader(req.Header.Get(echo.HeaderXRealIP)); ip != "" {
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
	var present []string
	if req.Header.Get(headerCFConnectingIP) != "" {
		present = append(present, headerCFConnectingIP)
	}
	if req.Header.Get(echo.HeaderXForwardedFor) != "" {
		present = append(present, echo.HeaderXForwardedFor)
	}
	if req.Header.Get(echo.HeaderXRealIP) != "" {
		present = append(present, echo.HeaderXRealIP)
	}
	if len(present) == 0 {
		return
	}
	GetLogger().Debug("Ignoring forwarded client-IP header from untrusted peer; if this peer is your reverse proxy, add its CIDR (or \"cloudflare\") to security.trustedproxies",
		logger.String("peer", peerHost),
		logger.String("headers", strings.Join(present, ",")),
	)
}
