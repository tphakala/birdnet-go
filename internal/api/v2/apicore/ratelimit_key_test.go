// ratelimit_key_test.go: tests for the per-client key the rate limiters use.

package apicore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/security/proxytrust"
)

// TestRateLimitKeyFor verifies how a client address maps to its limiter key:
// IPv4 keeps its own key, an IPv4-mapped IPv6 address is the same client as the
// IPv4 address, and IPv6 addresses share a key with every other address in their
// /64.
func TestRateLimitKeyFor(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		addr string
		want string
	}{
		{"ipv4 keeps its address", "192.0.2.10", "192.0.2.10"},
		{"ipv4 mapped ipv6 is the ipv4 client", "::ffff:192.0.2.10", "192.0.2.10"},
		{"ipv6 reduces to its /64", "2001:db8:1:2:3:4:5:6", "2001:db8:1:2::"},
		{"ipv6 low interface id", "2001:db8:1:2::1", "2001:db8:1:2::"},
		{"ipv6 high interface id", "2001:db8:1:2:ffff:ffff:ffff:ffff", "2001:db8:1:2::"},
		{"ipv6 next /64 is a different client", "2001:db8:1:3::1", "2001:db8:1:3::"},
		{"ipv6 loopback", "::1", "::"},
		{"ipv6 zone is ignored", "fe80::1%eth0", "fe80::"},
		{"ipv6 on another zone is the same client", "fe80::2%eth1", "fe80::"},
		{"unparsable address is used as is", "not-an-ip", "not-an-ip"},
		{"unparsable address with a percent sign is used as is", "not-an-ip%eth0", "not-an-ip%eth0"},
		{"empty address is used as is", "", ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, rateLimitKeyFor(tt.addr))
		})
	}
}

// coreWithTrustedProxies returns a Core whose live settings list trustedProxies.
func coreWithTrustedProxies(trustedProxies ...string) *Core {
	core := &Core{}
	core.Settings.Store(&conf.Settings{Security: conf.Security{TrustedProxies: trustedProxies}})
	return core
}

// TestCoreRateLimitKey verifies the key each kind of request gets: a direct or
// verified client keys on its own address (IPv6 per /64), a claim from an
// unconfigured local proxy is namespaced under the proxy, and a request that
// cannot be verified shares one bucket per proxy that is distinct from the
// proxy's direct key.
func TestCoreRateLimitKey(t *testing.T) {
	t.Parallel()

	const (
		forged  = "203.0.113.99"
		visitor = "198.51.100.9"
		sep     = keyNamespaceSep
	)
	tests := []struct {
		name       string
		trusted    []string
		remoteAddr string
		headers    map[string]string
		want       string
	}{
		{name: "direct IPv4 is plain", remoteAddr: "192.0.2.10:40000", want: "192.0.2.10"},
		{name: "direct IPv6 folds to its /64", remoteAddr: "[2001:db8:1:2::1]:40000", want: "2001:db8:1:2::"},
		{name: "unparseable peer keys on its raw host", remoteAddr: "@", want: "@"},
		{name: "public peer with headers keys like its direct request", remoteAddr: "203.0.113.50:40000", headers: map[string]string{proxytrust.HeaderCFConnectingIP: forged}, want: "203.0.113.50"},
		{name: "implicit claim is namespaced under the peer", remoteAddr: "10.0.0.5:40000", headers: map[string]string{proxytrust.HeaderCFConnectingIP: visitor}, want: "10.0.0.5" + sep + visitor},
		{name: "implicit IPv6 claim folds to its /64", remoteAddr: "10.0.0.5:40000", headers: map[string]string{proxytrust.HeaderCFConnectingIP: "2001:db8:1:2::7"}, want: "10.0.0.5" + sep + "2001:db8:1:2::"},
		{name: "unverifiable request shares the peer's marker bucket", remoteAddr: "10.0.0.5:40000", headers: map[string]string{proxytrust.HeaderCFConnectingIP: forged, echo.HeaderXForwardedFor: visitor}, want: "10.0.0.5" + sep + unverifiedKeyMarker},
		{name: "configured peer verified by its headers keys plain", trusted: []string{"10.0.0.5"}, remoteAddr: "10.0.0.5:40000", headers: map[string]string{echo.HeaderXForwardedFor: visitor}, want: visitor},
		{name: "configured peer verifying a client in ::/64 shares the marker bucket, not the loopback key", trusted: []string{"cloudflare"}, remoteAddr: "173.245.48.10:443", headers: map[string]string{proxytrust.HeaderCFConnectingIP: "::2", echo.HeaderXForwardedFor: "::2"}, want: "173.245.48.10" + sep + unverifiedKeyMarker},
		{name: "direct IPv6 loopback keeps its key", remoteAddr: "[::1]:40000", want: "::"},
		{name: "configured peer with unverifiable headers shares the marker bucket", trusted: []string{"10.0.0.5"}, remoteAddr: "10.0.0.5:40000", headers: map[string]string{proxytrust.HeaderCFConnectingIP: forged, echo.HeaderXForwardedFor: visitor}, want: "10.0.0.5" + sep + unverifiedKeyMarker},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			ctx := newRateLimitKeyContext(tt.remoteAddr, tt.headers)
			key, err := coreWithTrustedProxies(tt.trusted...).RateLimitKey(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.want, key)
		})
	}

	t.Run("listing the peer moves the same request from the namespaced key to the plain key", func(t *testing.T) {
		t.Parallel()
		core := coreWithTrustedProxies()
		headers := map[string]string{echo.HeaderXForwardedFor: visitor}
		before, err := core.RateLimitKey(newRateLimitKeyContext("10.0.0.5:40000", headers))
		require.NoError(t, err)
		assert.Equal(t, "10.0.0.5"+sep+visitor, before)

		core.Settings.Store(&conf.Settings{Security: conf.Security{TrustedProxies: []string{"10.0.0.5"}}})
		after, err := core.RateLimitKey(newRateLimitKeyContext("10.0.0.5:40000", headers))
		require.NoError(t, err)
		assert.Equal(t, visitor, after)
	})

	t.Run("the resolution is made once per request", func(t *testing.T) {
		t.Parallel()
		core := coreWithTrustedProxies()
		ctx := newRateLimitKeyContext("10.0.0.5:40000", map[string]string{echo.HeaderXForwardedFor: visitor})
		first, err := core.RateLimitKey(ctx)
		require.NoError(t, err)

		core.Settings.Store(&conf.Settings{Security: conf.Security{TrustedProxies: []string{"10.0.0.5"}}})
		second, err := core.RateLimitKey(ctx)
		require.NoError(t, err)
		assert.Equal(t, first, second, "a second call on the same request reuses the resolution")
	})
}

// newRateLimitKeyContext builds an echo context for a request from remoteAddr
// carrying headers.
func newRateLimitKeyContext(remoteAddr string, headers map[string]string) echo.Context {
	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.RemoteAddr = remoteAddr
	for name, value := range headers {
		req.Header.Set(name, value)
	}
	return echo.New().NewContext(req, httptest.NewRecorder())
}

// TestPeerRateLimitKey verifies that the peer key ignores forwarded client
// addresses: a request names another client in a header and still keys on the
// address it arrived from, with IPv6 peers reduced to their /64.
func TestPeerRateLimitKey(t *testing.T) {
	t.Parallel()

	keyFor := func(remoteAddr, forwarded string) string {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = remoteAddr
		req.Header.Set(echo.HeaderXForwardedFor, forwarded)
		req.Header.Set(proxytrust.HeaderCFConnectingIP, forwarded)
		ctx := echo.New().NewContext(req, httptest.NewRecorder())
		key, err := PeerRateLimitKey(ctx)
		require.NoError(t, err)
		return key
	}

	assert.Equal(t, "10.0.0.5", keyFor("10.0.0.5:40000", "203.0.113.7"), "a forwarded address does not change the key")
	assert.Equal(t, "10.0.0.5", keyFor("10.0.0.5:40001", "203.0.113.8"), "a new port or forwarded address is the same peer")
	assert.Equal(t, "2001:db8:1:2::", keyFor("[2001:db8:1:2::5]:40000", "203.0.113.7"), "an IPv6 peer is keyed per /64")
	assert.Equal(t, "fe80::", keyFor("[fe80::1%eth0]:40000", "203.0.113.7"), "a zone is not part of the key")
}

// TestRateLimitNamespace verifies how a limiter key maps to its store
// namespace: composite keys by their peer part, plain IPv6 keys by their /48,
// everything else by itself.
func TestRateLimitNamespace(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		key  string
		want string
	}{
		{"composite key is grouped by its peer", "192.0.2.1" + keyNamespaceSep + "203.0.113.5", "192.0.2.1"},
		{"composite key with an IPv6 peer", "2001:db8:1:2::" + keyNamespaceSep + "x", "2001:db8:1:2::"},
		{"plain IPv6 key is grouped by its /48", "2001:db8:1:2::", "2001:db8:1::"},
		{"another /64 of the /48 shares the namespace", "2001:db8:1:ffff::", "2001:db8:1::"},
		{"a different /48 is another namespace", "2001:db8:2:2::", "2001:db8:2::"},
		{"ipv4 is its own namespace", "192.0.2.10", "192.0.2.10"},
		{"a raw host is its own namespace", "proxy.example", "proxy.example"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, rateLimitNamespace(tt.key))
		})
	}
}
