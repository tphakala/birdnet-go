// ratelimit_key_test.go: tests for the per-client key the rate limiters use.

package apicore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestRateLimitKey verifies the echo extractor: it keys on the resolved client
// address, so two IPv6 peers in one /64 share a key and an IPv4 peer is
// unchanged.
func TestRateLimitKey(t *testing.T) {
	t.Parallel()

	keyFor := func(remoteAddr string) string {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = remoteAddr
		ctx := echo.New().NewContext(req, httptest.NewRecorder())
		key, err := RateLimitKey(ctx)
		require.NoError(t, err)
		return key
	}

	assert.Equal(t, "192.0.2.10", keyFor("192.0.2.10:40000"))
	assert.Equal(t, keyFor("[2001:db8:1:2::1]:40000"), keyFor("[2001:db8:1:2:dead:beef:0:7]:40001"),
		"addresses in one /64 are one client")
	assert.NotEqual(t, keyFor("[2001:db8:1:2::1]:40000"), keyFor("[2001:db8:1:3::1]:40000"),
		"another /64 is another client")
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
