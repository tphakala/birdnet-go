// scheme_extractor_test.go: tests for the trusted-proxy-gated scheme extractor.

package apicore

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"

	headerXFP = "X-Forwarded-Proto"
	headerXFS = "X-Forwarded-Ssl"

	// Peers used across the scheme tests.
	peerCGNAT       = "100.64.1.1:40000"
	peerCloudflare4 = "104.16.0.1:443"
	peerCloudflare6 = "[2606:4700::1]:443"
	peerLoopback    = "127.0.0.1:40000"
)

// newSchemeRequest builds a plain-HTTP request from remoteAddr with the given headers.
func newSchemeRequest(remoteAddr string, headers map[string]string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = remoteAddr
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	return req
}

func TestTrustedProxySchemeExtractor(t *testing.T) {
	t.Parallel()

	xfpHTTPS := map[string]string{headerXFP: "https"}
	cloudflare := []string{conf.TrustedProxyCloudflarePreset}

	tests := []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		headers        map[string]string
		tls            bool
		want           string
	}{
		{name: "direct TLS from public peer without headers is https", remoteAddr: testPublicPeerAddr, tls: true, want: schemeHTTPS},
		{name: "direct TLS beats XFP http from trusted peer", remoteAddr: peerLoopback, headers: map[string]string{headerXFP: "http"}, tls: true, want: schemeHTTPS},

		{name: "loopback v4 XFP https", remoteAddr: peerLoopback, headers: xfpHTTPS, want: schemeHTTPS},
		{name: "loopback v6 XFP https", remoteAddr: "[::1]:40000", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "private 192.168 XFP https", remoteAddr: "192.168.1.10:40000", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "private 10 XFP https", remoteAddr: "10.0.0.5:40000", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "private 172.16 XFP https", remoteAddr: "172.16.0.2:40000", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "ULA XFP https", remoteAddr: "[fd00::1]:40000", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "link-local v4 XFP https", remoteAddr: "169.254.1.1:40000", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "link-local v6 with zone XFP https", remoteAddr: "[fe80::1%eth0]:40000", headers: xfpHTTPS, want: schemeHTTPS},

		{name: "trusted peer XFP http", remoteAddr: peerLoopback, headers: map[string]string{headerXFP: "http"}, want: schemeHTTP},
		{name: "trusted peer XFP uppercase HTTPS", remoteAddr: peerLoopback, headers: map[string]string{headerXFP: "HTTPS"}, want: schemeHTTPS},
		{name: "trusted peer X-Forwarded-Ssl on", remoteAddr: peerLoopback, headers: map[string]string{headerXFS: "on"}, want: schemeHTTPS},
		{name: "trusted peer XFP list ending in http", remoteAddr: peerLoopback, headers: map[string]string{headerXFP: "https, http"}, want: schemeHTTP},
		{name: "trusted peer no headers", remoteAddr: peerLoopback, want: schemeHTTP},

		{name: "unix socket empty peer XFP https", remoteAddr: "", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "unix socket abstract peer XFP https", remoteAddr: "@", headers: xfpHTTPS, want: schemeHTTPS},
		{name: "unix socket path peer XFP https", remoteAddr: "/run/birdnet.sock", headers: xfpHTTPS, want: schemeHTTPS},

		{name: "public peer unconfigured XFP https is rejected", remoteAddr: testPublicPeerAddr, headers: xfpHTTPS, want: schemeHTTP},
		{name: "public peer unconfigured X-Forwarded-Ssl is rejected", remoteAddr: testPublicPeerAddr, headers: map[string]string{headerXFS: "on"}, want: schemeHTTP},
		{name: "public peer in configured CIDR", trustedProxies: []string{"203.0.113.0/24"}, remoteAddr: testPublicPeerAddr, headers: xfpHTTPS, want: schemeHTTPS},
		{name: "public peer as configured bare IP", trustedProxies: []string{"203.0.113.50"}, remoteAddr: testPublicPeerAddr, headers: xfpHTTPS, want: schemeHTTPS},
		{name: "public peer outside configured CIDR", trustedProxies: []string{"198.51.100.0/24"}, remoteAddr: testPublicPeerAddr, headers: xfpHTTPS, want: schemeHTTP},

		{name: "cloudflare preset v4 edge", trustedProxies: cloudflare, remoteAddr: peerCloudflare4, headers: xfpHTTPS, want: schemeHTTPS},
		{name: "cloudflare preset v6 edge", trustedProxies: cloudflare, remoteAddr: peerCloudflare6, headers: xfpHTTPS, want: schemeHTTPS},
		{name: "cloudflare preset is case insensitive", trustedProxies: []string{"CLOUDFLARE"}, remoteAddr: peerCloudflare4, headers: xfpHTTPS, want: schemeHTTPS},
		{name: "cloudflare edge without preset is rejected", remoteAddr: peerCloudflare4, headers: xfpHTTPS, want: schemeHTTP},

		{name: "CGNAT unconfigured is rejected", remoteAddr: peerCGNAT, headers: xfpHTTPS, want: schemeHTTP},
		{name: "CGNAT configured range", trustedProxies: []string{"100.64.0.0/10"}, remoteAddr: peerCGNAT, headers: xfpHTTPS, want: schemeHTTPS},

		{name: "unparseable peer is untrusted", remoteAddr: "garbage", headers: xfpHTTPS, want: schemeHTTP},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			extractor := newTrustedProxySchemeExtractor(settingsGetterWithProxies(tt.trustedProxies...))
			req := newSchemeRequest(tt.remoteAddr, tt.headers)
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			assert.Equal(t, tt.want, extractor(req))
		})
	}
}

// schemeTestPeers covers trusted-by-default, configurable and untrusted peers.
var schemeTestPeers = []string{
	peerLoopback, "[::1]:40000", "192.168.1.10:40000", "10.0.0.5:40000", "172.16.0.2:40000",
	"[fd00::1]:40000", "169.254.1.1:40000", "[fe80::1%eth0]:40000",
	"", "@", "/run/birdnet.sock",
	testPublicPeerAddr, peerCloudflare4, peerCloudflare6, peerCGNAT, "garbage",
}

var schemeTestHeaders = []map[string]string{
	nil,
	{headerXFP: "https"},
	{headerXFP: "http"},
	{headerXFP: "HTTPS"},
	{headerXFP: "https, http"},
	{headerXFP: "http, https"},
	{headerXFS: "on"},
	{"X-Forwarded-Protocol": "https"},
	{"X-Url-Scheme": "https"},
}

var schemeTestTrustedProxyLists = [][]string{
	nil,
	{conf.TrustedProxyCloudflarePreset},
	{"100.64.0.0/10"},
}

// TestTrustedProxySchemeExtractor_NeverTrustsLessThanEchoDefault pins that any
// request Echo's stock extractor reports as https is also https here.
func TestTrustedProxySchemeExtractor_NeverTrustsLessThanEchoDefault(t *testing.T) {
	t.Parallel()

	stock := echo.ExtractSchemeFromHeaders()
	for _, proxies := range schemeTestTrustedProxyLists {
		ours := newTrustedProxySchemeExtractor(settingsGetterWithProxies(proxies...))
		for _, peer := range schemeTestPeers {
			for _, headers := range schemeTestHeaders {
				if stock(newSchemeRequest(peer, headers)) == schemeHTTPS {
					assert.Equal(t, schemeHTTPS, ours(newSchemeRequest(peer, headers)),
						"peer %q headers %v proxies %v", peer, headers, proxies)
				}
			}
		}
	}
}

// TestTrustedProxySchemeExtractor_AgreesWithIPExtractor pins that, for TCP peers
// over plain HTTP, forwarded scheme headers are honored exactly when forwarded
// client-IP headers are.
func TestTrustedProxySchemeExtractor_AgreesWithIPExtractor(t *testing.T) {
	t.Parallel()

	const forwardedClient = "198.51.100.77"
	for _, proxies := range schemeTestTrustedProxyLists {
		getter := settingsGetterWithProxies(proxies...)
		schemeExtractor := newTrustedProxySchemeExtractor(getter)
		ipExtractor := newTrustedProxyIPExtractor(getter)
		for _, peer := range schemeTestPeers {
			if isUnixSocketPeer(peer) {
				continue // scheme trusts unix sockets like Echo; the IP extractor is TCP-only
			}
			headers := map[string]string{"X-Forwarded-For": forwardedClient, headerXFP: "https"}
			ipHonored := ipExtractor(newSchemeRequest(peer, headers)) == forwardedClient
			schemeHonored := schemeExtractor(newSchemeRequest(peer, headers)) == schemeHTTPS
			assert.Equal(t, ipHonored, schemeHonored, "peer %q proxies %v", peer, proxies)
		}
	}
}

// TestTrustedProxySchemeExtractor_HotReloadSequence pins that TrustedProxies
// changes take effect on the next request, for both CoW-published settings and
// reassignment on the same settings pointer, and that a reappearing list is not served stale.
func TestTrustedProxySchemeExtractor_HotReloadSequence(t *testing.T) {
	t.Parallel()

	steps := []struct {
		proxies []string
		want    map[string]string // peer -> scheme
	}{
		{nil, map[string]string{testPublicPeerAddr: schemeHTTP, peerCloudflare4: schemeHTTP, peerCGNAT: schemeHTTP}},
		{[]string{"203.0.113.0/24"}, map[string]string{testPublicPeerAddr: schemeHTTPS, peerCloudflare4: schemeHTTP, peerCGNAT: schemeHTTP}},
		{[]string{"203.0.113.0/24", conf.TrustedProxyCloudflarePreset}, map[string]string{testPublicPeerAddr: schemeHTTPS, peerCloudflare4: schemeHTTPS, peerCGNAT: schemeHTTP}},
		{[]string{conf.TrustedProxyCloudflarePreset}, map[string]string{testPublicPeerAddr: schemeHTTP, peerCloudflare4: schemeHTTPS, peerCGNAT: schemeHTTP}},
		{[]string{"100.64.0.0/10"}, map[string]string{testPublicPeerAddr: schemeHTTP, peerCloudflare4: schemeHTTP, peerCGNAT: schemeHTTPS}},
		{nil, map[string]string{testPublicPeerAddr: schemeHTTP, peerCloudflare4: schemeHTTP, peerCGNAT: schemeHTTP}},
	}
	xfpHTTPS := map[string]string{headerXFP: "https"}

	assertStep := func(t *testing.T, extractor echo.SchemeExtractor, want map[string]string) {
		t.Helper()
		assert.Equal(t, schemeHTTPS, extractor(newSchemeRequest(peerLoopback, xfpHTTPS)), "loopback is always trusted")
		for peer, scheme := range want {
			assert.Equal(t, scheme, extractor(newSchemeRequest(peer, xfpHTTPS)), "peer %s", peer)
		}
	}

	t.Run("publish new settings per step", func(t *testing.T) {
		t.Parallel()
		var current atomic.Pointer[conf.Settings]
		current.Store(&conf.Settings{})
		extractor := newTrustedProxySchemeExtractor(current.Load)
		for range 2 { // run twice so a reappearing list is pinned
			for _, step := range steps {
				next := conf.CloneSettings(current.Load())
				next.Security.TrustedProxies = step.proxies
				current.Store(next)
				assertStep(t, extractor, step.want)
			}
		}
	})

	t.Run("assign new slice on same settings", func(t *testing.T) {
		t.Parallel()
		settings := &conf.Settings{}
		extractor := newTrustedProxySchemeExtractor(func() *conf.Settings { return settings })
		for range 2 {
			for _, step := range steps {
				settings.Security.TrustedProxies = step.proxies
				assertStep(t, extractor, step.want)
			}
		}
	})
}

// TestTrustedProxySchemeExtractor_HotReloadEditsElementInPlace pins that editing
// an element of the already-published TrustedProxies slice, without assigning a
// new slice, still takes effect: the cached checker must not alias that slice.
func TestTrustedProxySchemeExtractor_HotReloadEditsElementInPlace(t *testing.T) {
	t.Parallel()

	list := []string{"203.0.113.0/24"}
	settings := &conf.Settings{}
	settings.Security.TrustedProxies = list
	extractor := newTrustedProxySchemeExtractor(func() *conf.Settings { return settings })
	xfpHTTPS := map[string]string{headerXFP: "https"}

	assert.Equal(t, schemeHTTPS, extractor(newSchemeRequest(testPublicPeerAddr, xfpHTTPS)))
	assert.Equal(t, schemeHTTP, extractor(newSchemeRequest(peerCGNAT, xfpHTTPS)))

	list[0] = "100.64.0.0/10"

	assert.Equal(t, schemeHTTP, extractor(newSchemeRequest(testPublicPeerAddr, xfpHTTPS)), "old range must stop being trusted")
	assert.Equal(t, schemeHTTPS, extractor(newSchemeRequest(peerCGNAT, xfpHTTPS)), "edited range must be trusted")
}

func TestTrustedProxySchemeExtractor_NilSettings(t *testing.T) {
	t.Parallel()

	cases := map[string]func() *conf.Settings{
		"nil getter":         nil,
		"getter returns nil": func() *conf.Settings { return nil },
		"empty settings":     func() *conf.Settings { return &conf.Settings{} },
	}
	for name, getter := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			extractor := newTrustedProxySchemeExtractor(getter)
			xfpHTTPS := map[string]string{headerXFP: "https"}
			require.Equal(t, schemeHTTPS, extractor(newSchemeRequest(peerLoopback, xfpHTTPS)))
			assert.Equal(t, schemeHTTP, extractor(newSchemeRequest(testPublicPeerAddr, xfpHTTPS)))
		})
	}
}
