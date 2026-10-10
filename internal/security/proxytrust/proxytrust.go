// Package proxytrust decides which reverse proxies may vouch for the client
// address of an HTTP request, and resolves that address.
//
// Two resolutions are offered. The lenient one, used for attribution (logs,
// tunnel detection), honors forwarded client-IP headers from any loopback,
// link-local or private peer so home-LAN proxies work without configuration.
// The strict one, AuthClientIP, is for authentication decisions such as the
// subnet bypass: it honors forwarded headers only from operator-configured
// proxies and refuses to guess when a request carries forwarded headers it
// cannot verify.
package proxytrust

import (
	"net"
	"net/http"
	"slices"
	"strings"
	"sync/atomic"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// HeaderCFConnectingIP is Cloudflare's client-IP header. Echo has no constant
// for it (it only defines X-Forwarded-For and X-Real-IP), so it is named here.
const HeaderCFConnectingIP = "CF-Connecting-IP"

// Client-IP header names that are not in Echo's constant set.
const (
	headerXForwardedFor = "X-Forwarded-For"
	headerXRealIP       = "X-Real-IP"
	headerForwarded     = "Forwarded"
	headerTrueClientIP  = "True-Client-IP"
)

// clientIPHeaders lists every header a proxy may use to carry the original
// client address. AuthClientIP treats the presence of any of them on a request
// from an unconfigured peer as "proxied or forged, client unknown".
var clientIPHeaders = []string{
	HeaderCFConnectingIP,
	headerXForwardedFor,
	headerXRealIP,
	headerForwarded,
	headerTrueClientIP,
}

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

// cloudflareEdgeNets is cloudflareEdgeCIDRs parsed once. AuthClientIP uses it to
// honor CF-Connecting-IP only from a real Cloudflare edge peer, which always
// overwrites that header.
var cloudflareEdgeNets = parseCIDRs(cloudflareEdgeCIDRs)

// Checker decides whether a peer address is a trusted reverse proxy whose
// forwarded client-IP headers may be honored. Build one with NewChecker.
type Checker struct {
	// raw is the Security.TrustedProxies slice this checker was built from. It is
	// used to detect configuration changes for hot-reload so the CIDR list is not
	// re-parsed on every request.
	raw    []string
	ranges []*net.IPNet
}

// NewChecker parses the configured trusted-proxy entries into a checker. Blank
// entries are skipped and the reserved "cloudflare" preset expands to
// Cloudflare's published ranges. Each entry may be a CIDR or a bare IP (a single
// host). Invalid entries are silently skipped (conf validation rejects them at
// load time; this is defense in depth).
func NewChecker(trustedProxies []string) *Checker {
	tc := &Checker{raw: slices.Clone(trustedProxies)}
	for _, entry := range trustedProxies {
		trimmed := strings.TrimSpace(entry)
		switch {
		case trimmed == "":
			continue
		case strings.EqualFold(trimmed, conf.TrustedProxyCloudflarePreset):
			tc.ranges = append(tc.ranges, cloudflareEdgeNets...)
		default:
			if network, ok := ParseProxyCIDR(trimmed); ok {
				tc.ranges = append(tc.ranges, network)
			}
		}
	}
	return tc
}

// parseCIDRs parses entries with ParseProxyCIDR, skipping any that fail.
func parseCIDRs(entries []string) []*net.IPNet {
	nets := make([]*net.IPNet, 0, len(entries))
	for _, entry := range entries {
		if network, ok := ParseProxyCIDR(entry); ok {
			nets = append(nets, network)
		}
	}
	return nets
}

// ParseProxyCIDR parses a trusted-proxy entry as either a CIDR or a bare IP
// (treated as a single host: /32 for IPv4, /128 for IPv6). Returns false if the
// entry is neither.
//
// The single-host network is built directly from the parsed IP rather than by
// appending a suffix and re-parsing: an IPv4-mapped IPv6 string (e.g.
// "::ffff:192.0.2.1") has a non-nil To4(), so a naive "/32" suffix would make
// net.ParseCIDR read it as a 128-bit address with a 32-bit mask, trusting a /32
// IPv6 range (2^96 hosts) instead of one host. Using To4() yields a true /32.
func ParseProxyCIDR(entry string) (*net.IPNet, bool) {
	entry = strings.TrimSpace(entry)
	if _, network, err := net.ParseCIDR(entry); err == nil {
		return network, true
	}
	if ip := net.ParseIP(entry); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return &net.IPNet{IP: v4, Mask: net.CIDRMask(32, 32)}, true
		}
		return &net.IPNet{IP: ip, Mask: net.CIDRMask(128, 128)}, true
	}
	return nil, false
}

// TrustsPeer reports whether ip is a trusted proxy peer for attribution
// purposes. Loopback, link-local, and private (RFC1918/ULA) addresses are always
// trusted, matching Echo's default TrustOption behavior, plus any
// operator-configured CIDR ranges. Never use it for authentication decisions;
// use AuthClientIP.
func (tc *Checker) TrustsPeer(ip net.IP) bool {
	if ip == nil {
		return false
	}
	if ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsPrivate() {
		return true
	}
	return tc.IsConfiguredProxy(ip)
}

// IsConfiguredProxy reports whether ip is inside an operator-configured trusted
// proxy range (including the cloudflare preset). It is deliberately NARROWER
// than TrustsPeer: it does not trust loopback/link-local/private by default.
//
// It is used for X-Forwarded-For hops, which are attacker-supplied: a real
// client is frequently on a private address, and if private hops were skipped
// as proxies, a LAN client could prepend a forged entry and have its real
// (private) hop skipped, spoofing its attributed IP. It is also the only peer
// trust AuthClientIP accepts.
func (tc *Checker) IsConfiguredProxy(ip net.IP) bool {
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

// ClientIPFromXFF resolves the real client IP from an X-Forwarded-For header,
// assuming the immediate peer is already known to be a trusted proxy. It walks
// the hop chain from the rightmost (closest to this server) entry leftward,
// skipping hops that are explicitly configured proxies (IsConfiguredProxy), and
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
func (tc *Checker) ClientIPFromXFF(xff string) string {
	if xff == "" {
		return ""
	}
	parts := strings.Split(xff, ",")
	for i, part := range slices.Backward(parts) {
		ip := net.ParseIP(stripZone(strings.TrimSpace(part)))
		if ip == nil {
			// Malformed hop: nothing further left can be trusted; fall back.
			return ""
		}
		if !tc.IsConfiguredProxy(ip) {
			return ip.String() // nearest non-proxy hop = real client
		}
		if i == 0 {
			// All hops are configured proxies; the leftmost is the original client.
			return ip.String()
		}
	}
	return ""
}

// AuthClientIP resolves the client address to use for authentication
// decisions, such as the allowed-subnet bypass. It returns nil when the address
// cannot be verified, and callers must then grant no address-based access.
//
// Unlike the attribution path, it never takes a client address on the word of a
// peer the operator has not configured as a proxy:
//
//   - A request without any client-IP header comes from its peer, which is then
//     the client (a direct browser, or a browser on the proxy host itself).
//   - A peer that is not a configured proxy but sends a client-IP header was
//     either proxied (the peer is the proxy, not the client) or forged the
//     header, so the result is nil. This also keeps an unconfigured local
//     reverse proxy from turning every client it forwards into a loopback
//     client.
//   - From a configured proxy the client comes from CF-Connecting-IP when the
//     peer is a Cloudflare edge address (Cloudflare overwrites that header),
//     otherwise from X-Forwarded-For (every header line, walked from the right
//     so the hop the proxy appended wins), otherwise from X-Real-IP.
//     CF-Connecting-IP from any other proxy is ignored, because a proxy that
//     does not set it passes a client's forged copy through untouched.
//
// A header value is used only when it is a single, plain address, and a
// header-derived loopback address is refused: loopback describes the
// connection, so only a direct loopback peer may claim it.
func (tc *Checker) AuthClientIP(req *http.Request) net.IP {
	peerIP, _ := PeerAddr(req)
	if peerIP == nil {
		return nil
	}
	if !hasClientIPHeader(req.Header) {
		return peerIP
	}
	if !tc.IsConfiguredProxy(peerIP) {
		return nil
	}

	ip := tc.forwardedClientIP(req.Header, peerIP)
	if ip == nil || ip.IsLoopback() {
		return nil
	}
	return ip
}

// forwardedClientIP picks the client address a configured proxy reported, in
// the order AuthClientIP documents, or nil when none is usable.
func (tc *Checker) forwardedClientIP(h http.Header, peerIP net.IP) net.IP {
	if isCloudflareEdge(peerIP) {
		if ip := singleHeaderIP(h, HeaderCFConnectingIP); ip != nil {
			return ip
		}
	}
	if xff := h.Values(headerXForwardedFor); len(xff) > 0 {
		return tc.strictClientFromXFF(strings.Join(xff, ","))
	}
	return singleHeaderIP(h, headerXRealIP)
}

// strictClientFromXFF walks an X-Forwarded-For chain from the right, skipping
// configured proxies, and returns the first other hop. Unlike ClientIPFromXFF it
// returns nil for any malformed hop and when every hop is a configured proxy,
// because the leftmost entry is client-supplied and nothing vouches for it.
func (tc *Checker) strictClientFromXFF(xff string) net.IP {
	parts := strings.Split(xff, ",")
	for _, part := range slices.Backward(parts) {
		ip := parseStrictIP(strings.TrimSpace(part))
		if ip == nil {
			return nil
		}
		if !tc.IsConfiguredProxy(ip) {
			return ip
		}
	}
	return nil
}

// singleHeaderIP parses header name as one strict address, returning nil when
// it is absent, repeated, or not a plain address.
func singleHeaderIP(h http.Header, name string) net.IP {
	values := h.Values(name)
	if len(values) != 1 {
		return nil
	}
	return parseStrictIP(strings.TrimSpace(values[0]))
}

// parseStrictIP parses s as an IP address, accepting a zone identifier only on
// an IPv6 link-local address, where it is meaningful. A zone on anything else
// (for example "127.0.0.1%x") is treated as malformed rather than stripped.
func parseStrictIP(s string) net.IP {
	host, zone, hasZone := strings.Cut(s, "%")
	ip := net.ParseIP(host)
	if ip == nil {
		return nil
	}
	if hasZone && (zone == "" || ip.To4() != nil || !ip.IsLinkLocalUnicast()) {
		return nil
	}
	return ip
}

// hasClientIPHeader reports whether any client-IP header is present, even empty.
func hasClientIPHeader(h http.Header) bool {
	for _, name := range clientIPHeaders {
		if len(h.Values(name)) > 0 {
			return true
		}
	}
	return false
}

// isCloudflareEdge reports whether ip is inside Cloudflare's published ranges.
func isCloudflareEdge(ip net.IP) bool {
	for _, network := range cloudflareEdgeNets {
		if network.Contains(ip) {
			return true
		}
	}
	return false
}

// Cache holds the Checker for the current Security.TrustedProxies list and
// rebuilds it only when the list changes, keeping the trusted set
// hot-reloadable without re-parsing CIDRs per request. The zero value is ready
// to use and it is safe for concurrent use.
type Cache struct {
	current atomic.Pointer[Checker]
}

// Resolve returns the checker for trustedProxies, reusing the cached one when
// the list is unchanged.
func (c *Cache) Resolve(trustedProxies []string) *Checker {
	if cached := c.current.Load(); cached != nil && slices.Equal(cached.raw, trustedProxies) {
		return cached
	}
	checker := NewChecker(trustedProxies)
	c.current.Store(checker)
	return checker
}

// PeerAddr extracts the immediate peer address from req.RemoteAddr, returning
// the parsed IP (nil if unparseable) and the raw host string for fallback. An
// IPv6 zone identifier, if present, is stripped before parsing.
func PeerAddr(req *http.Request) (peerIP net.IP, host string) {
	var err error
	if host, _, err = net.SplitHostPort(req.RemoteAddr); err != nil {
		// No port (e.g. a bare "[::1]" or "127.0.0.1"). Strip IPv6 brackets so
		// net.ParseIP can parse it, otherwise a bracketed loopback/private peer
		// would fail to parse and be treated as untrusted.
		host = strings.TrimSuffix(strings.TrimPrefix(req.RemoteAddr, "["), "]")
	}
	host = stripZone(host)
	peerIP = net.ParseIP(host)
	return peerIP, host
}

// ParseHeaderIP attempts to parse a valid IP from a header value. Returns the
// IP string if valid, empty string otherwise.
func ParseHeaderIP(headerValue string) string {
	if headerValue == "" {
		return ""
	}
	// net.ParseIP does not handle zone identifiers, and iOS Safari commonly
	// connects via IPv6 link-local addresses with zone IDs.
	ip := net.ParseIP(stripZone(headerValue))
	if ip != nil {
		return ip.String()
	}
	return ""
}

// stripZone removes an IPv6 zone identifier (e.g. "%wlan0") from s.
func stripZone(s string) string {
	if before, _, found := strings.Cut(s, "%"); found {
		return before
	}
	return s
}
