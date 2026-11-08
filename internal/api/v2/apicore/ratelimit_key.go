// ratelimit_key.go provides the per-client key the api/v2 rate limiters use.
package apicore

import (
	"net"
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
)

// RateLimitKey is an echo rate limiter IdentifierExtractor. It keys a client by
// the address the trusted-proxy extractor resolved (ctx.RealIP), reduced by
// rateLimitKeyFor so that rotating addresses within one IPv6 block does not give
// a client a fresh bucket each time.
func RateLimitKey(ctx echo.Context) (string, error) {
	return rateLimitKeyFor(ctx.RealIP()), nil
}

// PeerRateLimitKey is an echo rate limiter IdentifierExtractor that keys on the
// address the request arrived from, never on a forwarded header. It lets a
// limiter bound what a peer can do however many clients it names; the login
// route pairs it with RateLimitKey.
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
	if v4 := ip.To4(); v4 != nil {
		return v4.String()
	}
	return ip.Mask(net.CIDRMask(ipv6RateLimitPrefixBits, ipv6AddressBits)).String()
}
