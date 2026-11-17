// ratelimit_client.go resolves the client a rate limiter should charge.
package proxytrust

import (
	"net"
	"net/http"
	"strings"
)

// ClientSource names how RateLimitClient obtained the client of a request.
type ClientSource int

const (
	// SourceDirect means the peer is the client: the request carries no
	// client-IP header, or its peer is not a proxy the limiter may believe
	// (a public, CGNAT or unparseable address), so any header was ignored.
	SourceDirect ClientSource = iota
	// SourceConfiguredProxy means the peer is a configured proxy and
	// AuthClientIP verified the client address.
	SourceConfiguredProxy
	// SourceImplicitProxy means the peer is an unconfigured loopback,
	// link-local or private address whose client-IP headers agree on one
	// client. The claim is only as good as that peer, so a limiter must key it
	// under the peer.
	SourceImplicitProxy
	// SourceUnverified means a loopback, link-local, private or configured peer
	// sent client-IP headers that do not verify. Client is the peer, and a
	// limiter should charge every such request of that peer to one shared
	// bucket.
	SourceUnverified
)

// RateLimitClient is the client a rate limiter should charge for one request.
type RateLimitClient struct {
	// Client is the address to charge: the peer for SourceDirect and
	// SourceUnverified (nil when the peer address is unparseable), the verified
	// or claimed client otherwise.
	Client net.IP
	// Peer is the connection peer, nil when unparseable.
	Peer net.IP
	// PeerHost is the peer host text, also set when Peer is nil.
	PeerHost string
	// Source says how Client was obtained.
	Source ClientSource
	// ViaCloudflareEdge reports that the peer, an X-Real-IP value or an
	// X-Forwarded-For hop lies inside Cloudflare's edge ranges. It
	// over-approximates on purpose and is only read for the request that set it.
	ViaCloudflareEdge bool
}

// hopPolicy says which hops forwardedClientIP skips and how it treats the
// optional headers. The zero value is the authentication policy: only
// configured ranges are skipped, True-Client-IP supplies a client, and
// Forwarded makes the result nil.
type hopPolicy struct {
	// skipCloudflareEdge skips Cloudflare edge hops even when the preset is not
	// configured. implicitHopPolicy sets it only for a request that carries
	// CF-Connecting-IP.
	skipCloudflareEdge bool
	// skipLocalHops skips loopback, link-local and private hops, which a chain
	// such as cloudflared to a proxy in docker adds in front of the client.
	skipLocalHops bool
	// trueClientIPVetoOnly makes True-Client-IP unable to supply the client: it
	// must equal the resolved client or the result is nil.
	trueClientIPVetoOnly bool
	// singleEdgeHopXFFAbstains makes an X-Forwarded-For chain that is one
	// Cloudflare edge hop neither supply nor veto a client, as written by a
	// proxy that overwrites the header with the address it saw.
	singleEdgeHopXFFAbstains bool
	// ignoreForwarded makes the unparsed Forwarded header neither supply nor
	// veto a client.
	ignoreForwarded bool
}

// implicitHopPolicy is the policy for an unconfigured local proxy. Cloudflare
// edge hops, local hops and a single-edge-hop X-Forwarded-For are looked past
// only when the request carries CF-Connecting-IP, which is what puts such hops
// in front of the client. Without it an address inside Cloudflare's ranges is a
// caller (a Cloudflare-hosted client, for example), not an edge, unless the
// operator listed those ranges, which are then always skipped.
func implicitHopPolicy(h http.Header) hopPolicy {
	cfPresent := false
	for _, v := range h[canonicalCFConnectingIP] {
		if strings.TrimSpace(v) != "" {
			cfPresent = true
			break
		}
	}
	return hopPolicy{
		skipCloudflareEdge:       cfPresent,
		skipLocalHops:            cfPresent,
		trueClientIPVetoOnly:     true,
		singleEdgeHopXFFAbstains: cfPresent,
		ignoreForwarded:          true,
	}
}

// skips reports whether ip is a proxy hop to look past rather than a client.
func (p hopPolicy) skips(tc *Checker, ip net.IP) bool {
	return (p.skipLocalHops && isLocalAddr(ip)) ||
		tc.IsConfiguredProxy(ip) ||
		(p.skipCloudflareEdge && isCloudflareEdge(ip))
}

// RateLimitClient resolves the client a rate limiter should charge for req. It
// is for rate limiting and attribution only, never authentication: unlike
// AuthClientIP it also accepts a claim from an unconfigured local proxy, which
// the limiter keeps apart by keying it under the peer. Decisions, in order:
//
//  1. A peer that is unparseable or not trusted (see TrustsPeer) is
//     SourceDirect and its headers are ignored.
//  2. A request without a client-IP header is SourceDirect.
//  3. A configured peer is SourceConfiguredProxy when AuthClientIP verifies a
//     client, else SourceUnverified. It is never re-judged by the implicit
//     rules.
//  4. An unconfigured local peer whose headers agree on a client under
//     implicitHopPolicy is SourceImplicitProxy.
//  5. Anything else is SourceUnverified.
//
// A nil request is SourceDirect without a peer.
func (tc *Checker) RateLimitClient(req *http.Request) RateLimitClient {
	if req == nil {
		return RateLimitClient{Source: SourceDirect}
	}
	peer, host := PeerAddr(req)
	res := RateLimitClient{Client: peer, Peer: peer, PeerHost: host, Source: SourceDirect}
	res.ViaCloudflareEdge = peer != nil && isCloudflareEdge(peer)
	if peer == nil {
		return res
	}
	configured := tc.IsConfiguredProxy(peer)
	if (!configured && !isLocalAddr(peer)) || !HasClientIPHeader(req.Header) {
		return res
	}
	res.ViaCloudflareEdge = res.ViaCloudflareEdge || headersNameCloudflareEdge(req.Header)

	if configured {
		if client := tc.authClientIP(req, peer, true); client != nil {
			res.Client, res.Source = client, SourceConfiguredProxy
			return res
		}
		res.Source = SourceUnverified
		return res
	}
	if client := tc.forwardedClientIP(req.Header, implicitHopPolicy(req.Header)); client != nil {
		res.Client, res.Source = client, SourceImplicitProxy
		return res
	}
	res.Source = SourceUnverified
	return res
}

// headersNameCloudflareEdge reports whether any X-Real-IP value or
// X-Forwarded-For hop is a Cloudflare edge address. Malformed values are
// ignored.
func headersNameCloudflareEdge(h http.Header) bool {
	for _, v := range h[canonicalXRealIP] {
		if ip := parseStrictIP(strings.TrimSpace(v)); ip != nil && isCloudflareEdge(ip) {
			return true
		}
	}
	for _, line := range h[canonicalXForwardedFor] {
		for hop := range strings.SplitSeq(line, ",") {
			if ip := parseStrictIP(strings.TrimSpace(hop)); ip != nil && isCloudflareEdge(ip) {
				return true
			}
		}
	}
	return false
}
