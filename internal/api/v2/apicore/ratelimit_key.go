// ratelimit_key.go provides the per-client key the api/v2 rate limiters use.
package apicore

import (
	"net"
	"net/netip"
	"strings"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

const (
	// ipv6RateLimitPrefixBits is the IPv6 prefix length that counts as one client
	// for rate limiting: a /64 is one IPv6 subnet, so a client that changes the
	// host part of its address stays in one bucket.
	ipv6RateLimitPrefixBits = 64
	// ipv6AddressBits is the width of an IPv6 address in bits.
	ipv6AddressBits = 128
	// rateLimiterNamespaceIPv6PrefixBits is the IPv6 prefix length that shares one
	// limiter store namespace: the /64 keys of one /48 are one holder's subnets, so
	// they count against one per-namespace cap.
	rateLimiterNamespaceIPv6PrefixBits = 48
	// keyNamespaceSep separates the namespace of a limiter key from the rest of
	// it. IP address text never contains it. rateLimitNamespace groups keys by the
	// text before it, so a namespace's entries are capped together.
	keyNamespaceSep = "|"
	// unverifiedKeyMarker is the key part that stands for "the client could not
	// be verified". IP address text never contains it, so it cannot collide with
	// a client key.
	unverifiedKeyMarker = "?"
	// rateLimitResolutionContextKey is the echo context key RateLimitKey stores
	// the request's resolution under.
	rateLimitResolutionContextKey = "apicore.rateLimitResolution"
)

// rateLimitClient resolves, once per request, the client the rate limiters
// charge, and remembers the result in the echo context. It is for rate limiting
// and attribution only, never authentication. The trusted-proxy list is read
// from the live settings on every request.
func (c *Core) rateLimitClient(ctx echo.Context) proxytrust.RateLimitClient {
	if memo, ok := ctx.Get(rateLimitResolutionContextKey).(proxytrust.RateLimitClient); ok {
		return memo
	}
	res := c.ProxyChecker().RateLimitClient(ctx.Request())
	ctx.Set(rateLimitResolutionContextKey, res)
	return res
}

// RateLimitKey is an echo rate limiter IdentifierExtractor. It keys a request by
// the client rateLimitClient resolved, reduced so that rotating addresses within
// one IPv6 block does not give a client a fresh bucket each time:
//
//   - A direct request or one a configured proxy verified keys on the client
//     address alone, except that a verified client in ::/64 is keyed as
//     unverifiable so it cannot share the direct loopback client's key.
//   - A client an unconfigured local proxy claimed keys as peer, separator,
//     client. The claim is only as good as that proxy, so it is confined to the
//     proxy's own namespace and cannot name another client's bucket.
//   - A request that could not be verified keys as peer, separator, marker, one
//     shared bucket for everything unverifiable that peer sends.
func (c *Core) RateLimitKey(ctx echo.Context) (string, error) {
	res := c.rateLimitClient(ctx)
	switch {
	case res.Source == proxytrust.SourceConfiguredProxy && inIPv6LoopbackBlock(res.Client):
		// A verified client in ::/64 would fold to the same key as the direct
		// loopback client ::1. Only the loopback address itself is refused by
		// authentication, so key the rest as unverifiable instead.
		return rateLimitKeyForIP(res.Peer) + keyNamespaceSep + unverifiedKeyMarker, nil
	case res.Source == proxytrust.SourceImplicitProxy:
		return rateLimitKeyForIP(res.Peer) + keyNamespaceSep + rateLimitKeyForIP(res.Client), nil
	case res.Source == proxytrust.SourceUnverified:
		return rateLimitKeyForIP(res.Peer) + keyNamespaceSep + unverifiedKeyMarker, nil
	default:
		if res.Client == nil {
			return rateLimitKeyFor(res.PeerHost), nil
		}
		return rateLimitKeyForIP(res.Client), nil
	}
}

// SkipPeerRateLimit reports whether the peer limiter of a route that pairs it
// with RateLimitKey should skip this request. The peer limiter bounds how many
// clients a peer can name; it adds nothing, and is skipped, when:
//
//   - the request is direct: it names no client the limiter may believe, so the
//     client limiter already keys it on the peer, and a peer limiter shared with
//     the clients the same address proxies would only let that traffic lock the
//     peer's own direct requests out; or
//   - a proxy listed by its own address (a single host, not a range that may
//     hold clients) verified the client and no Cloudflare edge is involved, so
//     the operator vouches for each client it names.
//
// A client an unlisted proxy named, a request that could not be verified, a
// client a listed range verified, and a client a listed proxy verified behind a
// Cloudflare edge hop keep the peer limiter. A direct request skips it whatever
// its peer address is and keeps its own client bucket.
// The decision is made per request from the live trusted-proxy list.
func (c *Core) SkipPeerRateLimit(ctx echo.Context) bool {
	res := c.rateLimitClient(ctx)
	switch res.Source {
	case proxytrust.SourceDirect:
		return true
	case proxytrust.SourceConfiguredProxy:
		return !res.ViaCloudflareEdge && c.ProxyChecker().IsSingleHostProxy(res.Peer)
	default:
		return false
	}
}

// ipv6LoopbackBlock is the /64 that holds ::1. Folding any address in it to its
// /64 gives the key of the direct loopback client.
var ipv6LoopbackBlock = netip.MustParsePrefix("::/64")

// inIPv6LoopbackBlock reports whether ip is a native IPv6 address in ::/64.
func inIPv6LoopbackBlock(ip net.IP) bool {
	addr, ok := netip.AddrFromSlice(ip)
	return ok && !addr.Is4() && !addr.Is4In6() && ipv6LoopbackBlock.Contains(addr)
}

// PeerRateLimitKey is an echo rate limiter IdentifierExtractor that keys on the
// address the request arrived from, never on a forwarded header. It lets a
// limiter bound what a peer can do however many clients it names; the login and
// import elevation routes pair it with Core.RateLimitKey and skip it for the
// requests Core.SkipPeerRateLimit names.
func PeerRateLimitKey(ctx echo.Context) (string, error) {
	_, host := proxytrust.PeerAddr(ctx.Request())
	return rateLimitKeyFor(host), nil
}

// rateLimitKeyFor maps a client address to its limiter key. IPv4 addresses and
// IPv4-mapped IPv6 addresses key on the IPv4 address, other IPv6 addresses on
// their /64 prefix, ignoring any zone. A value that is not an IP address is
// returned unchanged.
func rateLimitKeyFor(addr string) string {
	// net.ParseIP rejects a zone, which the trusted-proxy extractor already strips;
	// ignoring it here keeps the key the same for callers that do not use it.
	host, _, _ := strings.Cut(addr, "%")
	ip := net.ParseIP(host)
	if ip == nil {
		return addr
	}
	return rateLimitKeyForIP(ip)
}

// rateLimitKeyForIP maps a client address to its limiter key like
// rateLimitKeyFor, without going through text.
func rateLimitKeyForIP(ip net.IP) string {
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(ipv6RateLimitPrefixBits, ipv6AddressBits)).String()
}

// rateLimitNamespace maps a limiter key to the namespace the limiter stores cap
// together. A composite key is grouped by the text before keyNamespaceSep, a
// plain IPv6 key by its /48, and any other key (IPv4 or a raw host) is its own
// namespace.
func rateLimitNamespace(key string) string {
	if name, _, composite := strings.Cut(key, keyNamespaceSep); composite {
		return name
	}
	ip := net.ParseIP(key)
	if ip == nil || ip.To4() != nil {
		return key
	}
	return ip.Mask(net.CIDRMask(rateLimiterNamespaceIPv6PrefixBits, ipv6AddressBits)).String()
}
