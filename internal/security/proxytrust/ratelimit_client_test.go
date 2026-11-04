// ratelimit_client_test.go: tests for RateLimitClient, the resolver behind the
// API rate limiter keys.
package proxytrust

import (
	"maps"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	// rlForgedClient is the address a forging client puts in a header.
	rlForgedClient = "203.0.113.99"
	// rlLANVisitor is a private client address behind a local proxy.
	rlLANVisitor = "192.168.1.20"
	// rlDockerHop is a private proxy hop in front of a proxy in docker.
	rlDockerHop = "172.18.0.2"
	// rlSecondEdge is a second Cloudflare edge address.
	rlSecondEdge = "173.245.48.11"
	// rlCloudflareCaller is an address inside Cloudflare's ranges that reaches an
	// origin without Cloudflare proxying it (for example a Cloudflare-hosted client).
	rlCloudflareCaller = "104.18.1.1"
)

// rateLimitClientCase is one request shape for TestCheckerRateLimitClient.
type rateLimitClientCase struct {
	name           string
	trustedProxies []string
	remoteAddr     string
	headers        map[string][]string
	wantSource     ClientSource
	wantClient     string // "" means a nil client
	wantEdge       bool
}

func rateLimitClientCases() []rateLimitClientCase {
	cf := func(v string) map[string][]string { return map[string][]string{HeaderCFConnectingIP: {v}} }
	xff := func(v string) map[string][]string { return map[string][]string{"X-Forwarded-For": {v}} }
	merge := func(parts ...map[string][]string) map[string][]string {
		out := map[string][]string{}
		for _, m := range parts {
			maps.Copy(out, m)
		}
		return out
	}
	realIP := func(v string) map[string][]string { return map[string][]string{"X-Real-IP": {v}} }
	behindCloudflare := merge(cf(testClientAddress), xff(testClientAddress+", "+testCloudflareEdgeIP), realIP(testCloudflareEdgeIP))

	return []rateLimitClientCase{
		{name: "direct, no headers", remoteAddr: "203.0.113.50:5000", wantSource: SourceDirect, wantClient: "203.0.113.50"},
		{name: "unparseable peer, with headers", remoteAddr: "@", headers: xff(testClientAddress), wantSource: SourceDirect},
		{name: "unlisted public peer, with headers", remoteAddr: "203.0.113.50:5000", headers: merge(cf(rlForgedClient), xff(rlForgedClient)), wantSource: SourceDirect, wantClient: "203.0.113.50"},
		{name: "unlisted public peer naming an edge hop is not flagged", remoteAddr: "203.0.113.50:5000", headers: xff(testCloudflareEdgeIP), wantSource: SourceDirect, wantClient: "203.0.113.50"},
		{name: "edge address only in X-Real-IP sets the flag", remoteAddr: "127.0.0.1:5000", headers: merge(cf(testClientAddress), realIP(testCloudflareEdgeIP)), wantSource: SourceImplicitProxy, wantClient: testClientAddress, wantEdge: true},
		{name: "edge hop in XFF without CF-Connecting-IP does not abstain", remoteAddr: "127.0.0.1:5000", headers: merge(realIP(rlForgedClient), xff(testCloudflareEdgeIP)), wantSource: SourceUnverified, wantClient: "127.0.0.1", wantEdge: true},
		{name: "caller in Cloudflare space without CF-Connecting-IP is the client, not an edge", remoteAddr: "127.0.0.1:5000", headers: merge(xff(testClientAddress+", "+rlCloudflareCaller), realIP(rlCloudflareCaller)), wantSource: SourceImplicitProxy, wantClient: rlCloudflareCaller, wantEdge: true},
		{name: "caller in Cloudflare space without CF-Connecting-IP, XFF only", remoteAddr: "127.0.0.1:5000", headers: xff(testClientAddress + ", " + rlCloudflareCaller), wantSource: SourceImplicitProxy, wantClient: rlCloudflareCaller, wantEdge: true},
		{name: "caller in Cloudflare space sending CF-Connecting-IP itself is still believed", remoteAddr: "127.0.0.1:5000", headers: merge(cf(testClientAddress), xff(testClientAddress+", "+rlCloudflareCaller)), wantSource: SourceImplicitProxy, wantClient: testClientAddress, wantEdge: true},
		{name: "unlisted CGNAT peer, with headers", remoteAddr: "100.64.1.2:5000", headers: cf(rlForgedClient), wantSource: SourceDirect, wantClient: "100.64.1.2"},
		{name: "configured peer, no headers", trustedProxies: []string{"127.0.0.1"}, remoteAddr: "127.0.0.1:5000", wantSource: SourceDirect, wantClient: "127.0.0.1"},
		{name: "cloudflared listed, CF plus a client XFF chain", trustedProxies: []string{"127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: merge(cf(testClientAddress), xff(testClientAddress)), wantSource: SourceConfiguredProxy, wantClient: testClientAddress},
		{name: "listed loopback, forged CF plus appended public client", trustedProxies: []string{"127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: merge(cf(rlForgedClient), xff(testClientAddress)), wantSource: SourceUnverified, wantClient: "127.0.0.1"},
		{name: "only loopback listed, nginx behind Cloudflare (no fall-through)", trustedProxies: []string{"127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: behindCloudflare, wantSource: SourceUnverified, wantClient: "127.0.0.1", wantEdge: true},
		{name: "unlisted private peer, CF only", remoteAddr: "10.0.0.5:5000", headers: cf(testClientAddress), wantSource: SourceImplicitProxy, wantClient: testClientAddress},
		{name: "unlisted loopback, forged CF plus appended public client", remoteAddr: "127.0.0.1:5000", headers: merge(cf(rlForgedClient), xff(testClientAddress)), wantSource: SourceUnverified, wantClient: "127.0.0.1"},
		{name: "unlisted loopback, LAN visitor, private XFF only", remoteAddr: "127.0.0.1:5000", headers: xff(rlLANVisitor), wantSource: SourceImplicitProxy, wantClient: rlLANVisitor},
		{name: "unlisted loopback, private XFF plus a forged CF", remoteAddr: "127.0.0.1:5000", headers: merge(xff(rlLANVisitor), cf(rlForgedClient)), wantSource: SourceUnverified, wantClient: "127.0.0.1"},
		{name: "unlisted loopback, XFF equal to the peer plus a forged CF", remoteAddr: "127.0.0.1:5000", headers: merge(xff("127.0.0.1"), cf(rlForgedClient)), wantSource: SourceUnverified, wantClient: "127.0.0.1"},
		{name: "cloudflared then nginx in docker", remoteAddr: "172.18.0.3:5000", headers: merge(cf(testClientAddress), realIP(rlDockerHop), xff(testClientAddress+", "+rlDockerHop)), wantSource: SourceImplicitProxy, wantClient: testClientAddress},
		{name: "unlisted loopback, nginx behind Cloudflare", remoteAddr: "127.0.0.1:5000", headers: behindCloudflare, wantSource: SourceImplicitProxy, wantClient: testClientAddress, wantEdge: true},
		{name: "unlisted Caddy behind Cloudflare, XFF is one edge hop", remoteAddr: "127.0.0.1:5000", headers: merge(cf(testClientAddress), xff(testCloudflareEdgeIP)), wantSource: SourceImplicitProxy, wantClient: testClientAddress, wantEdge: true},
		{name: "unlisted, XFF is two edge hops plus a forged CF", remoteAddr: "127.0.0.1:5000", headers: merge(cf(rlForgedClient), xff(testCloudflareEdgeIP+", "+rlSecondEdge)), wantSource: SourceUnverified, wantClient: "127.0.0.1", wantEdge: true},
		{name: "XFF client plus a disagreeing True-Client-IP, no CF", remoteAddr: "127.0.0.1:5000", headers: merge(xff(testClientAddress), map[string][]string{"True-Client-IP": {rlForgedClient}}), wantSource: SourceUnverified, wantClient: "127.0.0.1"},
		{name: "lone True-Client-IP from an unlisted private peer", remoteAddr: "10.0.0.5:5000", headers: map[string][]string{"True-Client-IP": {rlForgedClient}}, wantSource: SourceUnverified, wantClient: "10.0.0.5"},
		{name: "CF plus a disagreeing True-Client-IP", remoteAddr: "10.0.0.5:5000", headers: merge(cf(testClientAddress), map[string][]string{"True-Client-IP": {rlForgedClient}}), wantSource: SourceUnverified, wantClient: "10.0.0.5"},
		{name: "CF plus an agreeing True-Client-IP", remoteAddr: "10.0.0.5:5000", headers: merge(cf(testClientAddress), map[string][]string{"True-Client-IP": {testClientAddress}}), wantSource: SourceImplicitProxy, wantClient: testClientAddress},
		{name: "Forwarded plus XFF from an unlisted private peer", remoteAddr: "10.0.0.5:5000", headers: merge(xff(testClientAddress), map[string][]string{"Forwarded": {"for=" + rlForgedClient}}), wantSource: SourceImplicitProxy, wantClient: testClientAddress},
		{name: "header-derived loopback from an unlisted loopback peer", remoteAddr: "127.0.0.1:5000", headers: xff("127.0.0.1"), wantSource: SourceImplicitProxy, wantClient: "127.0.0.1"},
		{name: "edge peer, preset listed", trustedProxies: []string{conf.TrustedProxyCloudflarePreset}, remoteAddr: testCloudflareEdgeIP + ":443", headers: cf(testClientAddress), wantSource: SourceConfiguredProxy, wantClient: testClientAddress, wantEdge: true},
		{name: "edge peer, preset not listed", remoteAddr: testCloudflareEdgeIP + ":443", headers: cf(testClientAddress), wantSource: SourceDirect, wantClient: testCloudflareEdgeIP, wantEdge: true},
		{name: "listed peer, Caddy behind Cloudflare overwriting XFF", trustedProxies: []string{conf.TrustedProxyCloudflarePreset, "127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: merge(cf(testClientAddress), xff(testCloudflareEdgeIP)), wantSource: SourceUnverified, wantClient: "127.0.0.1", wantEdge: true},
	}
}

// TestCheckerRateLimitClient pins the source, client and edge flag of
// RateLimitClient for every branch of the resolver, including the forged-header
// shapes the limiter must not turn into a fresh bucket.
func TestCheckerRateLimitClient(t *testing.T) {
	t.Parallel()

	for _, tt := range rateLimitClientCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			for name, values := range tt.headers {
				for _, v := range values {
					req.Header.Add(name, v)
				}
			}
			got := NewChecker(tt.trustedProxies).RateLimitClient(req)
			assert.Equal(t, tt.wantSource, got.Source, "Source")
			if tt.wantClient == "" {
				assert.Nil(t, got.Client)
			} else {
				require.NotNil(t, got.Client)
				assert.Equal(t, tt.wantClient, got.Client.String(), "Client")
			}
			assert.Equal(t, tt.wantEdge, got.ViaCloudflareEdge, "ViaCloudflareEdge")
		})
	}
}

// TestCheckerRateLimitClientNilRequest verifies a nil request is a direct
// connection without a peer.
func TestCheckerRateLimitClientNilRequest(t *testing.T) {
	t.Parallel()

	assert.Equal(t, RateLimitClient{Source: SourceDirect}, NewChecker(nil).RateLimitClient(nil))
}

// TestRateLimitClientAgreesWithAuthClientIP pins invariant I5: for a configured
// peer the limiter and the authentication decision agree. With headers the
// source is SourceConfiguredProxy exactly when AuthClientIP verified a client,
// and the client is the same address; otherwise the request is unverified and
// the client is the peer. Without headers it is SourceDirect and the client is
// AuthClientIP's, the peer. It runs over the Classify table and the AuthClientIP
// table.
func TestRateLimitClientAgreesWithAuthClientIP(t *testing.T) {
	t.Parallel()

	type shape struct {
		name           string
		trustedProxies []string
		req            *http.Request
	}
	cases, authCases := classifyCases(), authClientIPCases()
	shapes := make([]shape, 0, len(cases)+len(authCases))
	for _, tt := range cases {
		shapes = append(shapes, shape{"classify/" + tt.name, tt.trustedProxies, newClassifyRequest(&tt)})
	}
	for _, tt := range authCases {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = tt.remoteAddr
		for name, values := range tt.headers {
			for _, v := range values {
				req.Header.Add(name, v)
			}
		}
		shapes = append(shapes, shape{"auth/" + tt.name, tt.trustedProxies, req})
	}

	var directConfigured int
	for _, tt := range shapes {
		checker := NewChecker(tt.trustedProxies)
		peer, _ := PeerAddr(tt.req)
		if peer == nil || !checker.IsConfiguredProxy(peer) {
			continue
		}
		if !HasClientIPHeader(tt.req.Header) {
			directConfigured++
		}
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := checker.RateLimitClient(tt.req)
			want := checker.AuthClientIP(tt.req)
			switch {
			case !HasClientIPHeader(tt.req.Header):
				assert.Equal(t, SourceDirect, got.Source)
			case want != nil:
				assert.Equal(t, SourceConfiguredProxy, got.Source)
			default:
				assert.Equal(t, SourceUnverified, got.Source)
			}
			require.NotNil(t, got.Client)
			if want != nil {
				assert.True(t, want.Equal(got.Client), "client %v, want %v", got.Client, want)
			} else {
				assert.True(t, got.Peer.Equal(got.Client), "an unverified client is the peer, got %v", got.Client)
			}
		})
	}
	assert.NotZero(t, directConfigured, "the tables must cover a configured peer without headers")
}

// TestCheckerIsSingleHostProxy verifies that a peer counts as a single-host
// proxy only when every configured entry that holds it names exactly that one
// address, so a range that could also hold clients never qualifies.
func TestCheckerIsSingleHostProxy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		trusted []string
		ip      string
		want    bool
	}{
		{"bare IPv4 entry", []string{"10.0.0.5"}, "10.0.0.5", true},
		{"IPv4 /32 entry", []string{"10.0.0.5/32"}, "10.0.0.5", true},
		{"bare IPv6 entry", []string{"2001:db8::5"}, "2001:db8::5", true},
		{"IPv6 /128 entry", []string{"2001:db8::5/128"}, "2001:db8::5", true},
		{"unlisted address", []string{"10.0.0.6"}, "10.0.0.5", false},
		{"empty list", nil, "10.0.0.5", false},
		{"range holding the address", []string{"10.0.0.0/24"}, "10.0.0.5", false},
		{"single host inside a listed range", []string{"10.0.0.0/24", "10.0.0.5"}, "10.0.0.5", false},
		{"cloudflare preset edge", []string{"cloudflare"}, "173.245.48.10", false},
		{"edge address listed by itself as well as by the preset", []string{"cloudflare", "173.245.48.10"}, "173.245.48.10", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, NewChecker(tt.trusted).IsSingleHostProxy(net.ParseIP(tt.ip)))
		})
	}

	t.Run("nil address", func(t *testing.T) {
		t.Parallel()
		assert.False(t, NewChecker([]string{"10.0.0.5"}).IsSingleHostProxy(nil))
	})
}
