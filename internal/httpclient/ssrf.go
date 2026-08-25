package httpclient

import (
	"context"
	"fmt"
	"net"
	"net/netip"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// ErrBlockedTarget is returned by the guarded dialer when a resolved connection
// IP is refused by the SSRF policy (link-local, unspecified, or a known cloud
// metadata address). It is a sentinel so callers and tests can match it with
// errors.Is.
var ErrBlockedTarget = errors.Newf("connection target blocked by SSRF policy").
	Component("httpclient").
	Category(errors.CategoryNetwork).
	Build()

// extraBlockedMetadataIPs holds cloud metadata endpoints that are NOT already
// covered by the link-local / unspecified checks below. The canonical AWS, GCP
// and Azure endpoint 169.254.169.254 is link-local and caught there. The two
// entries here sit in ranges this guard otherwise allows: Alibaba Cloud
// publishes its metadata on 100.100.100.200 (CGNAT space 100.64.0.0/10), and
// AWS exposes the IMDS over IPv6 at fd00:ec2::254 (ULA space fc00::/7).
var extraBlockedMetadataIPs = map[netip.Addr]struct{}{
	netip.MustParseAddr("100.100.100.200"): {}, // Alibaba Cloud metadata service
	netip.MustParseAddr("fd00:ec2::254"):   {}, // AWS IMDS over IPv6
}

// nat64Prefix and teredoPrefix are IPv6 transition ranges that embed an IPv4
// address. An attacker can use them to smuggle a blocked IPv4 target (e.g.
// 169.254.169.254) past the IPv6 link-local checks when a translation gateway
// is present, so the embedded IPv4 is extracted and re-evaluated. 6to4
// (2002::/16) is matched inline by its leading bytes.
var (
	nat64Prefix  = netip.MustParsePrefix("64:ff9b::/96") // RFC 6052 well-known NAT64
	teredoPrefix = netip.MustParsePrefix("2001::/32")    // RFC 4380 Teredo
)

// isBlockedTargetIP reports whether ip must be refused under the webhook SSRF
// guard.
//
// Blocked: link-local unicast (169.254.0.0/16, fe80::/10, which includes the
// 169.254.169.254 cloud metadata address), link-local multicast, the
// unspecified address (0.0.0.0, ::), the explicit cloud metadata IPs above, and
// any of those reached through an IPv4-embedding IPv6 transition address
// (NAT64, 6to4, Teredo).
//
// Deliberately allowed: loopback (127.0.0.0/8, ::1) and private RFC1918 / ULA
// ranges. BirdNET-Go's primary webhook use is pointing at services on the
// user's own LAN (Home Assistant, a local ntfy, a NAS), so blocking those would
// break the common case. This guard closes the cloud-metadata exfiltration path
// without disturbing legitimate on-LAN delivery.
func isBlockedTargetIP(ip netip.Addr) bool {
	if !ip.IsValid() {
		// Fail closed: refuse anything that cannot be classified.
		return true
	}
	// Fold ::ffff:a.b.c.d to its IPv4 form and drop any IPv6 zone so mapped or
	// zoned encodings of a blocked address cannot slip past the checks below.
	ip = ip.Unmap().WithZone("")
	if isBlockedAddr(ip) {
		return true
	}
	// Re-check any IPv4 embedded in an IPv6 transition address so a blocked
	// IPv4 target cannot be laundered through a NAT64/6to4/Teredo gateway.
	if v4, ok := embeddedTransitionIPv4(ip); ok && isBlockedAddr(v4) {
		return true
	}
	return false
}

// isBlockedAddr applies the core policy to a single already-unmapped address:
// link-local, unspecified, or a known cloud metadata IP.
func isBlockedAddr(ip netip.Addr) bool {
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
		return true
	}
	_, blocked := extraBlockedMetadataIPs[ip]
	return blocked
}

// embeddedTransitionIPv4 returns the IPv4 address embedded in an IPv6 transition
// address (NAT64 64:ff9b::/96, 6to4 2002::/16, or Teredo 2001:0000::/32), and
// false when ip carries none. ip is expected to have been Unmap-ed already, so
// plain IPv4-mapped addresses are not handled here.
func embeddedTransitionIPv4(ip netip.Addr) (netip.Addr, bool) {
	if !ip.Is6() {
		return netip.Addr{}, false
	}
	b := ip.As16()
	switch {
	case nat64Prefix.Contains(ip):
		// Last 4 bytes carry the IPv4 address.
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	case b[0] == 0x20 && b[1] == 0x02:
		// 6to4 2002:AABB:CCDD::/16: bytes 2..5 carry the IPv4 address.
		return netip.AddrFrom4([4]byte{b[2], b[3], b[4], b[5]}), true
	case teredoPrefix.Contains(ip):
		// Teredo: the client IPv4 is the last 4 bytes, bit-inverted.
		return netip.AddrFrom4([4]byte{^b[12], ^b[13], ^b[14], ^b[15]}), true
	case [12]byte(b[:12]) == [12]byte{} && !ip.IsUnspecified() && !ip.IsLoopback():
		// ::a.b.c.d IPv4-compatible IPv6 (deprecated, RFC 4291). The leading 12
		// bytes are zero and the last 4 carry the IPv4 address. ::ffff:a.b.c.d
		// was already folded by Unmap, and ::/:: 1 are excluded above.
		return netip.AddrFrom4([4]byte{b[12], b[13], b[14], b[15]}), true
	}
	return netip.Addr{}, false
}

// newGuardedDialContext returns a DialContext that resolves the destination
// host, refuses any resolved IP blocked by isBlockedTargetIP, and dials a
// validated IP literal directly. Dialing the resolved IP (rather than the
// hostname) closes the DNS-rebinding window: the address that was validated is
// the exact address that is connected to.
//
// The guard is re-entered on every dial the transport makes, so HTTP redirects
// to a blocked target are refused as well.
func newGuardedDialContext(base *net.Dialer) func(ctx context.Context, network, addr string) (net.Conn, error) {
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		host, port, err := net.SplitHostPort(addr)
		if err != nil {
			return nil, fmt.Errorf("invalid dial address %q: %w", addr, err)
		}

		ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
		if err != nil {
			return nil, fmt.Errorf("DNS lookup failed for %q: %w", host, err)
		}

		var lastErr error
		for i := range ips {
			addrSlice, ok := netip.AddrFromSlice(ips[i].IP)
			if !ok {
				continue
			}
			// Honor a family-specific network so an address of the wrong family
			// is skipped rather than handed to the base dialer as a doomed dial.
			// http.Transport uses "tcp" today, so this is normally a no-op.
			switch network {
			case "tcp4":
				if !addrSlice.Unmap().Is4() {
					continue
				}
			case "tcp6":
				if addrSlice.Unmap().Is4() {
					continue
				}
			}
			if isBlockedTargetIP(addrSlice) {
				lastErr = fmt.Errorf("%w: %s", ErrBlockedTarget, ips[i].IP.String())
				continue
			}
			conn, dialErr := base.DialContext(ctx, network, net.JoinHostPort(ips[i].IP.String(), port))
			if dialErr != nil {
				lastErr = dialErr
				continue
			}
			return conn, nil
		}
		if lastErr != nil {
			return nil, lastErr
		}
		return nil, fmt.Errorf("no address to connect to for host %q", host)
	}
}
