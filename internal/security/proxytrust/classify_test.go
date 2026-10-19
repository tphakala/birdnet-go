package proxytrust

import (
	"net/http"
	"net/http/httptest"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// classifyCase is one request shape for the Classify tests.
type classifyCase struct {
	name           string
	trustedProxies []string
	remoteAddr     string
	headers        map[string][]string
	wantStatus     ConnectionStatus
	wantPeer       string // "" means no parseable peer
	wantHeaders    bool
	wantListed     bool
	wantCFEdge     bool
	wantCFVerifies bool
}

const (
	testLocalProxyPeer   = "172.18.0.1"
	testClientAddress    = "198.51.100.9"
	testCloudflareEdgeIP = "173.245.48.10"
)

// classifyCases covers every status. All of the shared Classify tests run over
// this table.
func classifyCases() []classifyCase {
	xff := func(v string) map[string][]string { return map[string][]string{"X-Forwarded-For": {v}} }
	return []classifyCase{
		{name: "direct IPv4", remoteAddr: "192.168.1.20:5000", wantStatus: StatusDirect, wantPeer: "192.168.1.20"},
		{name: "direct IPv6", remoteAddr: "[2001:db8::7]:5000", wantStatus: StatusDirect, wantPeer: "2001:db8::7"},
		{name: "direct IPv4-mapped peer", remoteAddr: "[::ffff:172.18.0.1]:5000", wantStatus: StatusDirect, wantPeer: testLocalProxyPeer},
		{name: "unparseable peer without headers", remoteAddr: "@", wantStatus: StatusDirect},
		{name: "unparseable peer with headers", remoteAddr: "@", headers: xff(testClientAddress), wantStatus: StatusUntrustedProxyUnverifiable, wantHeaders: true},

		{name: "listed peer with consistent XFF", trustedProxies: []string{testLocalProxyPeer}, remoteAddr: testLocalProxyPeer + ":5000", headers: xff(testClientAddress), wantStatus: StatusTrustedProxy, wantPeer: testLocalProxyPeer, wantHeaders: true, wantListed: true},
		{name: "listed range that also holds the client", trustedProxies: []string{"10.0.0.0/24"}, remoteAddr: "10.0.0.2:5000", headers: xff("10.0.0.7"), wantStatus: StatusTrustedProxyUnverified, wantPeer: "10.0.0.2", wantHeaders: true, wantListed: true},
		{name: "listed peer with disagreeing X-Real-IP and XFF", trustedProxies: []string{testLocalProxyPeer}, remoteAddr: testLocalProxyPeer + ":5000", headers: map[string][]string{"X-Real-IP": {"203.0.113.7"}, "X-Forwarded-For": {testClientAddress}}, wantStatus: StatusTrustedProxyUnverified, wantPeer: testLocalProxyPeer, wantHeaders: true, wantListed: true},
		{name: "listed peer sending only Forwarded", trustedProxies: []string{testLocalProxyPeer}, remoteAddr: testLocalProxyPeer + ":5000", headers: map[string][]string{"Forwarded": {"for=198.51.100.9"}}, wantStatus: StatusTrustedProxyUnverified, wantPeer: testLocalProxyPeer, wantHeaders: true, wantListed: true},

		{name: "unlisted private peer", remoteAddr: testLocalProxyPeer + ":5000", headers: xff(testClientAddress), wantStatus: StatusUntrustedProxy, wantPeer: testLocalProxyPeer, wantHeaders: true},
		{name: "unlisted loopback peer", remoteAddr: "127.0.0.1:5000", headers: xff(testClientAddress), wantStatus: StatusUntrustedProxy, wantPeer: "127.0.0.1", wantHeaders: true},
		{name: "unlisted IPv6 link-local peer", remoteAddr: "[fe80::2]:5000", headers: xff(testClientAddress), wantStatus: StatusUntrustedProxy, wantPeer: "fe80::2", wantHeaders: true},
		{name: "unlisted CGNAT peer", remoteAddr: "100.64.1.2:5000", headers: xff(testClientAddress), wantStatus: StatusUntrustedProxy, wantPeer: "100.64.1.2", wantHeaders: true},
		{name: "unlisted private peer with an empty X-Real-IP", remoteAddr: testLocalProxyPeer + ":5000", headers: map[string][]string{"X-Real-IP": {""}}, wantStatus: StatusUntrustedProxyUnverifiable, wantPeer: testLocalProxyPeer, wantHeaders: true},
		{name: "unlisted private peer sending only Forwarded", remoteAddr: testLocalProxyPeer + ":5000", headers: map[string][]string{"Forwarded": {"for=198.51.100.9"}}, wantStatus: StatusUntrustedProxyUnverifiable, wantPeer: testLocalProxyPeer, wantHeaders: true},
		{name: "unlisted public peer", remoteAddr: "203.0.113.50:5000", headers: xff(testClientAddress), wantStatus: StatusUntrustedPublicProxy, wantPeer: "203.0.113.50", wantHeaders: true},

		{name: "Cloudflare edge peer without the preset", remoteAddr: testCloudflareEdgeIP + ":443", headers: map[string][]string{HeaderCFConnectingIP: {testClientAddress}}, wantStatus: StatusUntrustedPublicProxy, wantPeer: testCloudflareEdgeIP, wantHeaders: true, wantCFEdge: true, wantCFVerifies: true},
		{name: "Cloudflare edge peer with the preset", trustedProxies: []string{conf.TrustedProxyCloudflarePreset}, remoteAddr: testCloudflareEdgeIP + ":443", headers: map[string][]string{HeaderCFConnectingIP: {testClientAddress}}, wantStatus: StatusTrustedProxy, wantPeer: testCloudflareEdgeIP, wantHeaders: true, wantListed: true, wantCFEdge: true},
		{name: "local proxy behind Cloudflare, preset not listed", remoteAddr: "127.0.0.1:5000", headers: map[string][]string{HeaderCFConnectingIP: {testClientAddress}, "X-Forwarded-For": {testClientAddress + ", " + testCloudflareEdgeIP}, "X-Real-IP": {testCloudflareEdgeIP}}, wantStatus: StatusUntrustedProxyUnverifiable, wantPeer: "127.0.0.1", wantHeaders: true, wantCFVerifies: true},
		{name: "listed local proxy behind Cloudflare, preset not listed", trustedProxies: []string{"127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: map[string][]string{HeaderCFConnectingIP: {testClientAddress}, "X-Forwarded-For": {testClientAddress + ", " + testCloudflareEdgeIP}, "X-Real-IP": {testCloudflareEdgeIP}}, wantStatus: StatusTrustedProxyUnverified, wantPeer: "127.0.0.1", wantHeaders: true, wantListed: true, wantCFVerifies: true},
		{name: "local proxy behind Cloudflare, preset listed", trustedProxies: []string{conf.TrustedProxyCloudflarePreset, "127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: map[string][]string{HeaderCFConnectingIP: {testClientAddress}, "X-Forwarded-For": {testClientAddress + ", " + testCloudflareEdgeIP}, "X-Real-IP": {testCloudflareEdgeIP}}, wantStatus: StatusTrustedProxy, wantPeer: "127.0.0.1", wantHeaders: true, wantListed: true},
	}
}

// newClassifyRequest builds the request for tt.
func newClassifyRequest(tt *classifyCase) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = tt.remoteAddr
	for name, values := range tt.headers {
		for _, v := range values {
			req.Header.Add(name, v)
		}
	}
	return req
}

// TestCheckerClassify pins the status and every flag of Classify for each
// request shape.
func TestCheckerClassify(t *testing.T) {
	t.Parallel()

	for _, tt := range classifyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conn := NewChecker(tt.trustedProxies).Classify(newClassifyRequest(&tt))
			assert.Equal(t, tt.wantStatus, conn.Status)
			if tt.wantPeer == "" {
				assert.Nil(t, conn.Peer)
			} else {
				require.NotNil(t, conn.Peer)
				assert.Equal(t, tt.wantPeer, conn.Peer.String())
			}
			assert.Equal(t, tt.wantHeaders, conn.HasClientIPHeaders, "HasClientIPHeaders")
			assert.Equal(t, tt.wantListed, conn.PeerIsConfiguredProxy, "PeerIsConfiguredProxy")
			assert.Equal(t, tt.wantCFEdge, conn.PeerIsCloudflareEdge, "PeerIsCloudflareEdge")
			assert.Equal(t, tt.wantCFVerifies, conn.CloudflarePresetWouldVerify, "CloudflarePresetWouldVerify")
		})
	}
}

// TestCheckerClassifyNilRequest verifies a nil request is a direct connection
// without a peer or client.
func TestCheckerClassifyNilRequest(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		conn := NewChecker([]string{"10.0.0.2"}).Classify(nil)
		assert.Equal(t, Connection{Status: StatusDirect}, conn)
	})
}

// TestClassifyAgreesWithAuthClientIP pins invariant I1: the verified client
// Classify reports is exactly what AuthClientIP returns, and a verified status
// is reported exactly when an address was verified.
func TestClassifyAgreesWithAuthClientIP(t *testing.T) {
	t.Parallel()

	for _, tt := range classifyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			checker := NewChecker(tt.trustedProxies)
			req := newClassifyRequest(&tt)
			conn := checker.Classify(req)
			want := checker.AuthClientIP(req)
			if want == nil {
				assert.Nil(t, conn.VerifiedClient)
			} else {
				require.NotNil(t, conn.VerifiedClient)
				assert.True(t, want.Equal(conn.VerifiedClient), "verified client %v, want %v", conn.VerifiedClient, want)
			}
			verifiedStatus := (conn.Status == StatusDirect && conn.Peer != nil) || conn.Status == StatusTrustedProxy
			assert.Equal(t, want != nil, verifiedStatus)
		})
	}
}

// TestClassifyTrustPromise pins invariant I3: where the panel offers to trust a
// peer, listing it produces the promised result.
func TestClassifyTrustPromise(t *testing.T) {
	t.Parallel()

	for _, tt := range classifyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			conn := NewChecker(tt.trustedProxies).Classify(newClassifyRequest(&tt))
			if conn.Peer == nil {
				return
			}
			req := newClassifyRequest(&tt)
			withPeer := append(slices.Clone(tt.trustedProxies), conn.Peer.String())
			if conn.Status == StatusUntrustedProxy {
				assert.Equal(t, StatusTrustedProxy, NewChecker(withPeer).Classify(req).Status)
			}
			if conn.Status == StatusUntrustedProxyUnverifiable {
				assert.Equal(t, StatusTrustedProxyUnverified, NewChecker(withPeer).Classify(req).Status,
					"a peer whose headers cannot be verified must not be promised green")
			}
			if conn.CloudflarePresetWouldVerify {
				withBoth := append(slices.Clone(withPeer), conf.TrustedProxyCloudflarePreset)
				assert.Equal(t, StatusTrustedProxy, NewChecker(withBoth).Classify(req).Status)
			}
		})
	}
}

// TestClassifyCachedCheckerMatchesNew verifies a checker resolved through the
// production Cache classifies exactly like a fresh one.
func TestClassifyCachedCheckerMatchesNew(t *testing.T) {
	t.Parallel()

	for _, tt := range classifyCases() {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var cache Cache
			settings := &conf.Settings{}
			settings.Security.TrustedProxies = tt.trustedProxies
			req := newClassifyRequest(&tt)
			assert.Equal(t, NewChecker(tt.trustedProxies).Classify(req), cache.ResolveSettings(settings).Classify(req))
		})
	}
}
