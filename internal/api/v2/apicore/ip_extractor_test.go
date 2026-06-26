// ip_extractor_test.go: tests for the trusted-proxy-gated client IP extractor.

package apicore

import (
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// testPublicPeerAddr is a public (untrusted-by-default) RemoteAddr reused across
// the spoofing and breadcrumb test cases.
const testPublicPeerAddr = "203.0.113.50:40000"

// settingsGetterWithProxies returns a getSettings function whose
// Security.TrustedProxies is set to the given entries.
func settingsGetterWithProxies(trustedProxies ...string) func() *conf.Settings {
	settings := &conf.Settings{}
	settings.Security.TrustedProxies = trustedProxies
	return func() *conf.Settings { return settings }
}

func TestTrustedProxyIPExtractor(t *testing.T) {
	t.Parallel()

	const (
		cfHeader  = "CF-Connecting-IP"
		xffHeader = "X-Forwarded-For"
		xriHeader = "X-Real-IP"
	)

	tests := []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		headers        map[string]string
		want           string
	}{
		{
			name:       "loopback peer honors CF-Connecting-IP",
			remoteAddr: "127.0.0.1:54321",
			headers:    map[string]string{cfHeader: "203.0.113.5"},
			want:       "203.0.113.5",
		},
		{
			// Single-hop XFF (the common reverse-proxy case): the sole entry is the
			// real client and is returned with no proxy config needed.
			name:       "trusted peer single-hop XFF returns client",
			remoteAddr: "192.168.1.10:40000",
			headers:    map[string]string{xffHeader: "203.0.113.7"},
			want:       "203.0.113.7",
		},
		{
			// XFF append-spoofing: an attacker prepends a forged value; the trusted
			// proxy appends the attacker's real address to the right. The walk
			// returns the real (rightmost non-proxy) IP, not the forged leftmost.
			name:       "trusted peer ignores forged leftmost XFF entry",
			remoteAddr: "127.0.0.1:40000",
			headers:    map[string]string{xffHeader: "127.0.0.1, 203.0.113.99"},
			want:       "203.0.113.99",
		},
		{
			// LAN-client spoof: a private real client prepends a forged public IP.
			// With no configured proxy CIDRs, the real private hop is NOT treated
			// as a proxy, so it is returned and the forged leftmost never wins.
			name:       "trusted peer does not let a LAN client spoof via XFF prepend",
			remoteAddr: "192.168.1.5:40000",
			headers:    map[string]string{xffHeader: "8.8.8.8, 192.168.1.99"},
			want:       "192.168.1.99",
		},
		{
			// Configured intermediate proxies are skipped to reach the real client.
			name:           "trusted peer skips configured proxy hops to reach client",
			trustedProxies: []string{"10.0.0.0/8"},
			remoteAddr:     "10.0.0.2:40000",
			headers:        map[string]string{xffHeader: "203.0.113.7, 10.0.0.1, 10.0.0.2"},
			want:           "203.0.113.7",
		},
		{
			// Every hop is a configured proxy (internal chain): the leftmost entry
			// is the original client.
			name:           "trusted peer all configured-proxy hops returns leftmost client",
			trustedProxies: []string{"10.0.0.0/8"},
			remoteAddr:     "10.0.0.5:40000",
			headers:        map[string]string{xffHeader: "10.0.0.50, 10.0.0.6"},
			want:           "10.0.0.50",
		},
		{
			name:       "private peer honors X-Real-IP",
			remoteAddr: "10.0.0.5:40000",
			headers:    map[string]string{xriHeader: "203.0.113.9"},
			want:       "203.0.113.9",
		},
		{
			name:       "CF-Connecting-IP takes precedence over XFF for trusted peer",
			remoteAddr: "127.0.0.1:40000",
			headers:    map[string]string{cfHeader: "203.0.113.5", xffHeader: "198.51.100.9"},
			want:       "203.0.113.5",
		},
		{
			// The security fix: a directly-exposed instance must not trust a
			// spoofed CF-Connecting-IP header from an untrusted public peer.
			name:       "public peer ignores spoofed CF-Connecting-IP",
			remoteAddr: testPublicPeerAddr,
			headers:    map[string]string{cfHeader: "1.2.3.4"},
			want:       "203.0.113.50",
		},
		{
			name:       "public peer ignores spoofed XFF",
			remoteAddr: "198.51.100.7:40000",
			headers:    map[string]string{xffHeader: "1.2.3.4"},
			want:       "198.51.100.7",
		},
		{
			name:       "public peer ignores spoofed X-Real-IP",
			remoteAddr: "198.51.100.7:40000",
			headers:    map[string]string{xriHeader: "1.2.3.4"},
			want:       "198.51.100.7",
		},
		{
			name:           "configured CIDR trusts public proxy",
			trustedProxies: []string{"198.51.100.0/24"},
			remoteAddr:     "198.51.100.7:40000",
			headers:        map[string]string{cfHeader: "203.0.113.9"},
			want:           "203.0.113.9",
		},
		{
			name:           "configured bare IPv4 trusts that single host",
			trustedProxies: []string{"198.51.100.7"},
			remoteAddr:     "198.51.100.7:40000",
			headers:        map[string]string{cfHeader: "203.0.113.9"},
			want:           "203.0.113.9",
		},
		{
			name:           "configured bare IPv4 does not trust a different host",
			trustedProxies: []string{"198.51.100.7"},
			remoteAddr:     "198.51.100.8:40000",
			headers:        map[string]string{cfHeader: "1.2.3.4"},
			want:           "198.51.100.8",
		},
		{
			name:           "configured bare IPv6 trusts that single host",
			trustedProxies: []string{"2001:db8::1"},
			remoteAddr:     "[2001:db8::1]:40000",
			headers:        map[string]string{cfHeader: "203.0.113.9"},
			want:           "203.0.113.9",
		},
		{
			name:       "IPv6 loopback peer bracketed without port honors header",
			remoteAddr: "[::1]",
			headers:    map[string]string{cfHeader: "203.0.113.5"},
			want:       "203.0.113.5",
		},
		{
			name:           "cloudflare preset trusts cloudflare edge range",
			trustedProxies: []string{conf.TrustedProxyCloudflarePreset},
			remoteAddr:     "173.245.48.10:40000", // within 173.245.48.0/20
			headers:        map[string]string{cfHeader: "203.0.113.11"},
			want:           "203.0.113.11",
		},
		{
			name:           "cloudflare preset does not trust non-cloudflare public peer",
			trustedProxies: []string{conf.TrustedProxyCloudflarePreset},
			remoteAddr:     testPublicPeerAddr,
			headers:        map[string]string{cfHeader: "1.2.3.4"},
			want:           "203.0.113.50",
		},
		{
			name:       "no forwarded headers returns peer address",
			remoteAddr: testPublicPeerAddr,
			want:       "203.0.113.50",
		},
		{
			name:       "trusted peer with no forwarded headers returns peer address",
			remoteAddr: "127.0.0.1:40000",
			want:       "127.0.0.1",
		},
		{
			name:       "remote addr without port is parsed",
			remoteAddr: "127.0.0.1",
			headers:    map[string]string{cfHeader: "203.0.113.5"},
			want:       "203.0.113.5",
		},
		{
			name:       "IPv6 private peer honors CF header",
			remoteAddr: "[fd00::1]:40000",
			headers:    map[string]string{cfHeader: "203.0.113.5"},
			want:       "203.0.113.5",
		},
		{
			name:       "IPv6 public peer ignores spoofed CF header",
			remoteAddr: "[2001:db8::1]:40000",
			headers:    map[string]string{cfHeader: "1.2.3.4"},
			want:       "2001:db8::1",
		},
		{
			name:       "invalid XFF entries fall through to peer address",
			remoteAddr: "192.168.1.10:40000",
			headers:    map[string]string{xffHeader: "not-an-ip"},
			want:       "192.168.1.10",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			extractor := newTrustedProxyIPExtractor(settingsGetterWithProxies(tt.trustedProxies...))

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			for k, v := range tt.headers {
				req.Header.Set(k, v)
			}

			assert.Equal(t, tt.want, extractor(req))
		})
	}
}

// TestTrustedProxyIPExtractor_HotReload verifies that changing
// Security.TrustedProxies between requests takes effect without rebuilding the
// extractor (hot-reload), and that the parsed-CIDR cache is refreshed on change.
func TestTrustedProxyIPExtractor_HotReload(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	extractor := newTrustedProxyIPExtractor(func() *conf.Settings { return settings })

	newReq := func() *http.Request {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = "198.51.100.7:40000" // public peer
		req.Header.Set("CF-Connecting-IP", "203.0.113.9")
		return req
	}

	// Initially no trusted proxies: the public peer is untrusted, so the spoofed
	// header is ignored and the real peer address is returned.
	assert.Equal(t, "198.51.100.7", extractor(newReq()), "untrusted peer should not honor forwarded header")

	// Hot-reload: trust the peer's subnet. The next request must honor the header.
	settings.Security.TrustedProxies = []string{"198.51.100.0/24"}
	assert.Equal(t, "203.0.113.9", extractor(newReq()), "newly trusted peer should honor forwarded header")

	// Hot-reload back to empty: the peer is untrusted again.
	settings.Security.TrustedProxies = nil
	assert.Equal(t, "198.51.100.7", extractor(newReq()), "removing trust should ignore forwarded header again")
}

// TestTrustedProxyIPExtractor_NilSettings verifies the extractor is safe when
// the settings getter is nil or returns nil (falls back to default trust set).
func TestTrustedProxyIPExtractor_NilSettings(t *testing.T) {
	t.Parallel()

	cases := map[string]func() *conf.Settings{
		"nil getter":         nil,
		"getter returns nil": func() *conf.Settings { return nil },
		"empty settings":     func() *conf.Settings { return &conf.Settings{} },
	}

	for name, getter := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			extractor := newTrustedProxyIPExtractor(getter)

			// Loopback is always trusted, so the header is honored.
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = "127.0.0.1:40000"
			req.Header.Set("CF-Connecting-IP", "203.0.113.5")
			assert.Equal(t, "203.0.113.5", extractor(req))

			// Public peer is untrusted by default.
			req2 := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req2.RemoteAddr = testPublicPeerAddr
			req2.Header.Set("CF-Connecting-IP", "1.2.3.4")
			assert.Equal(t, "203.0.113.50", extractor(req2))
		})
	}
}

func TestTrustedProxyChecker_Trust(t *testing.T) {
	t.Parallel()

	checker := buildTrustedProxyChecker([]string{"198.51.100.0/24", "  ", conf.TrustedProxyCloudflarePreset})

	trusted := []string{
		"127.0.0.1",     // loopback
		"::1",           // IPv6 loopback
		"10.1.2.3",      // private
		"192.168.0.5",   // private
		"fd00::1",       // IPv6 ULA (private)
		"fe80::1",       // link-local
		"198.51.100.42", // configured CIDR
		"173.245.48.10", // cloudflare range
	}
	for _, ip := range trusted {
		assert.Truef(t, checker.trust(mustParseIP(t, ip)), "expected %s to be trusted", ip)
	}

	untrusted := []string{
		"203.0.113.1", // public, not configured
		"8.8.8.8",     // public
		"2001:db8::1", // public IPv6
	}
	for _, ip := range untrusted {
		assert.Falsef(t, checker.trust(mustParseIP(t, ip)), "expected %s to be untrusted", ip)
	}

	assert.False(t, checker.trust(nil), "nil IP must never be trusted")
}

// TestTrustedProxyChecker_TrustForwardedHop verifies that X-Forwarded-For hop
// trust is narrower than peer trust: only explicitly configured CIDRs count as
// proxy hops, while loopback/private/link-local are trusted peers but NOT
// skippable forwarded hops (so a private real client can't be spoofed past).
func TestTrustedProxyChecker_TrustForwardedHop(t *testing.T) {
	t.Parallel()

	checker := buildTrustedProxyChecker([]string{"203.0.113.0/24"})

	assert.True(t, checker.trustForwardedHop(mustParseIP(t, "203.0.113.9")), "configured CIDR is a trusted hop")

	// Default-trusted peer addresses must NOT be skippable forwarded hops.
	for _, ip := range []string{"127.0.0.1", "::1", "192.168.1.1", "10.0.0.1", "fe80::1"} {
		assert.Falsef(t, checker.trustForwardedHop(mustParseIP(t, ip)), "%s must not be a trusted forwarded hop", ip)
		assert.Truef(t, checker.trust(mustParseIP(t, ip)), "%s should still be a trusted peer", ip)
	}

	assert.False(t, checker.trustForwardedHop(nil), "nil IP must never be a trusted hop")
}

// TestParseProxyCIDR verifies bare IPs become single-host networks, including
// the IPv4-mapped IPv6 case that must not widen into a /32 over 128 bits.
func TestParseProxyCIDR(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		entry    string
		ok       bool
		wantOnes int
		wantBits int
		contains string
		excludes string
	}{
		{name: "IPv4 CIDR", entry: "203.0.113.0/24", ok: true, wantOnes: 24, wantBits: 32, contains: "203.0.113.9", excludes: "203.0.114.1"},
		{name: "bare IPv4", entry: "203.0.113.5", ok: true, wantOnes: 32, wantBits: 32, contains: "203.0.113.5", excludes: "203.0.113.6"},
		{name: "bare IPv6", entry: "2001:db8::1", ok: true, wantOnes: 128, wantBits: 128, contains: "2001:db8::1", excludes: "2001:db8::2"},
		{name: "IPv4-mapped IPv6 stays single host", entry: "::ffff:198.51.100.7", ok: true, wantOnes: 32, wantBits: 32, contains: "198.51.100.7", excludes: "198.51.100.8"},
		{name: "garbage", entry: "not-an-ip", ok: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			network, ok := parseProxyCIDR(tt.entry)
			require.Equal(t, tt.ok, ok)
			if !tt.ok {
				return
			}
			ones, bits := network.Mask.Size()
			assert.Equal(t, tt.wantOnes, ones, "mask ones")
			assert.Equal(t, tt.wantBits, bits, "mask bits")
			assert.True(t, network.Contains(mustParseIP(t, tt.contains)), "should contain %s", tt.contains)
			assert.False(t, network.Contains(mustParseIP(t, tt.excludes)), "should not contain %s", tt.excludes)
		})
	}
}

// TestResolveTrustedProxyChecker_CacheReuse verifies the checker is reused while
// the configuration is unchanged and rebuilt when it changes.
func TestResolveTrustedProxyChecker_CacheReuse(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.Security.TrustedProxies = []string{"198.51.100.0/24"}
	getSettings := func() *conf.Settings { return settings }

	var cache atomic.Pointer[trustedProxyChecker]

	first := resolveTrustedProxyChecker(&cache, getSettings)
	second := resolveTrustedProxyChecker(&cache, getSettings)
	assert.Same(t, first, second, "checker should be cached while config is unchanged")

	settings.Security.TrustedProxies = []string{"203.0.113.0/24"}
	third := resolveTrustedProxyChecker(&cache, getSettings)
	assert.NotSame(t, second, third, "checker should be rebuilt when config changes")
}

// TestLogIgnoredForwardedHeader exercises the DEBUG breadcrumb helper for both
// the header-present and header-absent (early-return) branches. It asserts the
// path is panic-safe; the log line itself is a side effect on the global logger
// and is not captured here.
func TestLogIgnoredForwardedHeader(t *testing.T) {
	t.Parallel()

	t.Run("with forwarded headers", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = testPublicPeerAddr
		req.Header.Set("CF-Connecting-IP", "1.2.3.4")
		req.Header.Set("X-Forwarded-For", "1.2.3.4")
		assert.NotPanics(t, func() { logIgnoredForwardedHeader(req, "203.0.113.50") })
	})

	t.Run("without forwarded headers", func(t *testing.T) {
		t.Parallel()
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = testPublicPeerAddr
		assert.NotPanics(t, func() { logIgnoredForwardedHeader(req, "203.0.113.50") })
	})
}

// TestHeaderNonEmpty_CanonicalKey verifies the direct-map header check uses the
// canonical stored key. "CF-Connecting-IP" is not in canonical MIME form, so a
// naive req.Header["CF-Connecting-IP"] lookup would miss the value http.Header
// stores under "Cf-Connecting-Ip".
func TestHeaderNonEmpty_CanonicalKey(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
	req.Header.Set("CF-Connecting-IP", "1.2.3.4")

	assert.True(t, headerNonEmpty(req.Header, canonicalCFConnectingIP), "CF-Connecting-IP must be detected via its canonical key")
	assert.False(t, headerNonEmpty(req.Header, canonicalXForwardedFor), "absent X-Forwarded-For must be false")
	assert.False(t, headerNonEmpty(req.Header, canonicalXRealIP), "absent X-Real-IP must be false")
}

func mustParseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	require.NotNil(t, ip, "test IP %q must parse", s)
	return ip
}

// TestParseIPFromHeader tests IP parsing with zone ID stripping.
func TestParseIPFromHeader(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{name: "empty string", input: "", expected: ""},
		{name: "valid IPv4", input: "192.168.1.1", expected: "192.168.1.1"},
		{name: "valid IPv6", input: "2001:db8::1", expected: "2001:db8::1"},
		{name: "IPv6 link-local with zone ID", input: "fe80::1%eth0", expected: "fe80::1"},
		{name: "IPv6 link-local with wlan zone", input: "fe80::1cb6:63bc:5462:71c5%wlan0", expected: "fe80::1cb6:63bc:5462:71c5"},
		{name: "IPv4 with spurious percent", input: "192.168.1.1%zone", expected: "192.168.1.1"},
		{name: "just a percent sign", input: "%", expected: ""},
		{name: "garbage with percent", input: "not_an_ip%zone", expected: ""},
		{name: "multiple percent signs", input: "fe80::1%wlan0%extra", expected: "fe80::1"},
		{name: "invalid IP", input: "999.999.999.999", expected: ""},
		{name: "IPv6 loopback", input: "::1", expected: "::1"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := parseIPFromHeader(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}
