// proxytrust_test.go: tests for proxy trust and client address resolution.

package proxytrust

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestChecker_TrustsPeer(t *testing.T) {
	t.Parallel()

	checker := NewChecker([]string{"198.51.100.0/24", "  ", conf.TrustedProxyCloudflarePreset})

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
		assert.Truef(t, checker.TrustsPeer(mustParseIP(t, ip)), "expected %s to be trusted", ip)
	}

	untrusted := []string{
		"203.0.113.1", // public, not configured
		"8.8.8.8",     // public
		"2001:db8::1", // public IPv6
	}
	for _, ip := range untrusted {
		assert.Falsef(t, checker.TrustsPeer(mustParseIP(t, ip)), "expected %s to be untrusted", ip)
	}

	assert.False(t, checker.TrustsPeer(nil), "nil IP must never be trusted")
}

// TestChecker_IsConfiguredProxy verifies that X-Forwarded-For hop
// trust is narrower than peer trust: only explicitly configured CIDRs count as
// proxy hops, while loopback/private/link-local are trusted peers but NOT
// skippable forwarded hops (so a private real client can't be spoofed past).
func TestChecker_IsConfiguredProxy(t *testing.T) {
	t.Parallel()

	checker := NewChecker([]string{"203.0.113.0/24"})

	assert.True(t, checker.IsConfiguredProxy(mustParseIP(t, "203.0.113.9")), "configured CIDR is a trusted hop")

	// Default-trusted peer addresses must NOT be skippable forwarded hops.
	for _, ip := range []string{"127.0.0.1", "::1", "192.168.1.1", "10.0.0.1", "fe80::1"} {
		assert.Falsef(t, checker.IsConfiguredProxy(mustParseIP(t, ip)), "%s must not be a trusted forwarded hop", ip)
		assert.Truef(t, checker.TrustsPeer(mustParseIP(t, ip)), "%s should still be a trusted peer", ip)
	}

	assert.False(t, checker.IsConfiguredProxy(nil), "nil IP must never be a trusted hop")
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

// TestCache_Reuse verifies the checker is reused while the configuration is
// unchanged and rebuilt when it changes.
func TestCache_Reuse(t *testing.T) {
	t.Parallel()

	var cache Cache
	proxies := []string{"198.51.100.0/24"}

	first := cache.Resolve(proxies)
	second := cache.Resolve(proxies)
	assert.Same(t, first, second, "checker should be cached while config is unchanged")

	third := cache.Resolve([]string{"203.0.113.0/24"})
	assert.NotSame(t, second, third, "checker should be rebuilt when config changes")

	// An in-place edit of the caller's slice must also be seen as a change.
	proxies[0] = "192.0.2.0/24"
	fourth := cache.Resolve(proxies)
	assert.True(t, fourth.IsConfiguredProxy(mustParseIP(t, "192.0.2.1")), "in-place edit must rebuild the checker")
}

func mustParseIP(t *testing.T, s string) net.IP {
	t.Helper()
	ip := net.ParseIP(s)
	require.NotNil(t, ip, "test IP %q must parse", s)
	return ip
}

// TestParseHeaderIP tests IP parsing with zone ID stripping.
func TestParseHeaderIP(t *testing.T) {
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
			result := ParseHeaderIP(tt.input)
			assert.Equal(t, tt.expected, result)
		})
	}
}

const (
	// cloudflareEdgePeer is an address inside Cloudflare's published edge ranges.
	cloudflareEdgePeer = "173.245.48.10:443"
	// cloudflareWorkerEgress is the CF-Connecting-IP Cloudflare sets for a
	// Worker subrequest; it lies inside the preset's 2a06:98c0::/29 range.
	cloudflareWorkerEgress = "2a06:98c0:3600::103"
)

// TestChecker_AuthClientIP pins the strict client address used for
// authentication decisions. Every forged-header case must resolve to nil (no
// address-based access) or to the real peer, never to the forged value.
func TestChecker_AuthClientIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		trustedProxies []string
		remoteAddr     string
		headers        map[string][]string
		want           string // "" means nil
	}{
		{name: "direct LAN client without headers is the peer", remoteAddr: "192.168.1.20:5000", want: "192.168.1.20"},
		{name: "direct loopback client without headers is the peer", remoteAddr: "127.0.0.1:5000", want: "127.0.0.1"},
		{name: "direct IPv6 link-local client with zone", remoteAddr: "[fe80::1%eth0]:5000", want: "fe80::1"},
		{name: "unparseable peer", remoteAddr: "garbage", want: ""},

		// Forged headers from an unconfigured private peer: the request claims to
		// be proxied, so neither the claim nor the peer can be trusted.
		{name: "private peer forging X-Forwarded-For", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"X-Forwarded-For": {"203.0.113.7"}}, want: ""},
		{name: "private peer forging X-Real-IP", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"X-Real-IP": {"203.0.113.7"}}, want: ""},
		{name: "private peer forging CF-Connecting-IP", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"CF-Connecting-IP": {"203.0.113.7"}}, want: ""},
		{name: "private peer forging loopback via XFF", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"X-Forwarded-For": {"127.0.0.1"}}, want: ""},
		{name: "private peer forging Forwarded", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"Forwarded": {"for=203.0.113.7"}}, want: ""},
		{name: "private peer with empty X-Real-IP", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"X-Real-IP": {""}}, want: ""},
		{name: "unconfigured local proxy does not make clients loopback", remoteAddr: "127.0.0.1:5000", headers: map[string][]string{"X-Forwarded-For": {"198.51.100.9"}}, want: ""},
		{name: "public peer forging XFF", remoteAddr: "198.51.100.9:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20"}}, want: ""},

		{name: "private peer forging True-Client-IP", remoteAddr: "172.25.5.9:5000", headers: map[string][]string{"True-Client-IP": {"203.0.113.7"}}, want: ""},
		{name: "direct client on an IPv4-mapped address", remoteAddr: "[::ffff:192.168.1.20]:5000", want: "192.168.1.20"},
		{name: "unix socket peer", remoteAddr: "@", want: ""},
		{name: "empty RemoteAddr", remoteAddr: "", want: ""},
		{name: "unix socket path peer", remoteAddr: "/run/birdnet.sock", want: ""},

		// Configured proxies.
		{name: "configured proxy single-hop XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20"}}, want: "192.168.1.20"},
		{name: "configured proxy appended hop beats forged prefix", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20, 198.51.100.9"}}, want: "198.51.100.9"},
		{name: "configured proxy chain split across XFF lines", trustedProxies: []string{"10.0.0.0/24"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"198.51.100.9", "10.0.0.3"}}, want: "198.51.100.9"},
		{name: "configured proxy appending a separate XFF line", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20", "198.51.100.9"}}, want: "198.51.100.9"},
		{name: "configured proxy chain skips configured hops", trustedProxies: []string{"10.0.0.0/24"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"198.51.100.9, 10.0.0.3"}}, want: "198.51.100.9"},
		{name: "configured proxy chain of only configured hops", trustedProxies: []string{"10.0.0.0/24"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"10.0.0.7, 10.0.0.3"}}, want: ""},
		{name: "configured proxy with malformed XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"nonsense"}}, want: ""},
		{name: "configured proxy with zone on IPv4 hop", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20%x"}}, want: ""},
		{name: "configured proxy with link-local zone hop", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"fe80::1%eth0"}}, want: "fe80::1"},
		{name: "configured proxy on an IPv4-mapped address", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "[::ffff:10.0.0.2]:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20"}}, want: "192.168.1.20"},
		{name: "configured proxy with zone on a global IPv6 hop", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"2001:db8::1%eth0"}}, want: ""},
		{name: "configured proxy with an empty zone", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"fe80::1%"}}, want: ""},
		{name: "configured proxy forwarding IPv4-mapped loopback via X-Real-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"::ffff:127.0.0.1"}}, want: ""},
		{name: "configured proxy with an empty XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {""}}, want: ""},
		{name: "configured proxy with a trailing comma in XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"192.168.1.20,"}}, want: ""},
		{name: "configured proxy forwarding loopback via XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"127.0.0.1"}}, want: ""},
		{name: "configured proxy sending X-Real-IP only", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"192.168.1.20"}}, want: "192.168.1.20"},
		{name: "configured proxy with repeated X-Real-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"192.168.1.20", "198.51.100.9"}}, want: ""},
		// Agreement: every client-IP header from a configured proxy must name the
		// same client, so a forged copy of a header the proxy does not write
		// cannot override the one it does.
		{name: "X-Real-IP-only proxy passing a forged XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"198.51.100.9"}, "X-Forwarded-For": {"192.168.1.20"}}, want: ""},
		{name: "XFF-appending proxy passing a forged X-Real-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Forwarded-For": {"198.51.100.9"}, "X-Real-IP": {"192.168.1.20"}}, want: ""},
		{name: "XFF-appending proxy passing a forged CF-Connecting-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"CF-Connecting-IP": {"192.168.1.20"}, "X-Forwarded-For": {"198.51.100.9"}}, want: ""},
		{name: "XFF-appending proxy passing a forged True-Client-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"True-Client-IP": {"192.168.1.20"}, "X-Forwarded-For": {"198.51.100.9"}}, want: ""},
		{name: "proxy setting X-Real-IP and appending XFF in agreement", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"192.168.1.20"}, "X-Forwarded-For": {"203.0.113.1, 192.168.1.20"}}, want: "192.168.1.20"},
		{name: "configured proxy with only CF-Connecting-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"CF-Connecting-IP": {"192.168.1.20"}}, want: "192.168.1.20"},
		{name: "configured proxy with only True-Client-IP", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"True-Client-IP": {"192.168.1.20"}}, want: "192.168.1.20"},
		{name: "configured proxy sending Forwarded", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"Forwarded": {"for=192.168.1.20"}, "X-Real-IP": {"192.168.1.20"}}, want: ""},
		{name: "X-Real-IP naming a configured proxy is skipped", trustedProxies: []string{"10.0.0.2", "10.0.0.3"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"10.0.0.3"}, "X-Forwarded-For": {"198.51.100.9, 10.0.0.3"}}, want: "198.51.100.9"},
		{name: "only a header naming a configured proxy", trustedProxies: []string{"10.0.0.2", "10.0.0.3"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"10.0.0.3"}}, want: ""},
		{name: "browser on configured proxy host without headers", trustedProxies: []string{"127.0.0.1"}, remoteAddr: "127.0.0.1:5000", want: "127.0.0.1"},
		{name: "Cloudflare edge with CF-Connecting-IP matching the appended XFF hop", trustedProxies: []string{conf.TrustedProxyCloudflarePreset}, remoteAddr: cloudflareEdgePeer, headers: map[string][]string{"CF-Connecting-IP": {"198.51.100.9"}, "X-Forwarded-For": {"192.168.1.20, 198.51.100.9"}}, want: "198.51.100.9"},
		{name: "Cloudflare edge passing a forged X-Real-IP", trustedProxies: []string{conf.TrustedProxyCloudflarePreset}, remoteAddr: cloudflareEdgePeer, headers: map[string][]string{"CF-Connecting-IP": {"198.51.100.9"}, "X-Real-IP": {"192.168.1.20"}}, want: ""},
		{name: "Cloudflare edge with repeated CF-Connecting-IP", trustedProxies: []string{conf.TrustedProxyCloudflarePreset}, remoteAddr: cloudflareEdgePeer, headers: map[string][]string{"CF-Connecting-IP": {"192.168.1.20", "198.51.100.9"}, "X-Forwarded-For": {"198.51.100.10"}}, want: ""},
		{name: "local proxy behind Cloudflare: X-Real-IP naming the edge is skipped", trustedProxies: []string{conf.TrustedProxyCloudflarePreset, "127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: map[string][]string{"CF-Connecting-IP": {"198.51.100.9"}, "X-Forwarded-For": {"198.51.100.9, 173.245.48.10"}, "X-Real-IP": {"173.245.48.10"}}, want: "198.51.100.9"},
		{name: "local proxy behind Cloudflare passing a forged True-Client-IP", trustedProxies: []string{conf.TrustedProxyCloudflarePreset, "127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: map[string][]string{"CF-Connecting-IP": {"198.51.100.9"}, "X-Forwarded-For": {"198.51.100.9, 173.245.48.10"}, "True-Client-IP": {"192.168.1.20"}}, want: ""},
		{name: "Cloudflare Worker egress with a forged XFF entry", trustedProxies: []string{conf.TrustedProxyCloudflarePreset}, remoteAddr: cloudflareEdgePeer, headers: map[string][]string{"CF-Connecting-IP": {cloudflareWorkerEgress}, "X-Forwarded-For": {"192.168.1.20, " + cloudflareWorkerEgress}}, want: ""},
		{name: "local proxy behind Cloudflare with Worker egress and a forged XFF entry", trustedProxies: []string{conf.TrustedProxyCloudflarePreset, "127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: map[string][]string{"CF-Connecting-IP": {cloudflareWorkerEgress}, "X-Forwarded-For": {"192.168.1.20, " + cloudflareWorkerEgress + ", 173.245.48.10"}, "X-Real-IP": {"173.245.48.10"}}, want: ""},
		{name: "local proxy behind Cloudflare with Worker egress in True-Client-IP and a forged XFF entry", trustedProxies: []string{conf.TrustedProxyCloudflarePreset, "127.0.0.1"}, remoteAddr: "127.0.0.1:5000", headers: map[string][]string{"True-Client-IP": {cloudflareWorkerEgress}, "X-Forwarded-For": {"192.168.1.20, " + cloudflareWorkerEgress + ", 173.245.48.10"}}, want: ""},
		{name: "configured proxy with malformed X-Real-IP and agreeing XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"nonsense"}, "X-Forwarded-For": {"198.51.100.9"}}, want: ""},
		{name: "configured proxy with repeated X-Real-IP and XFF", trustedProxies: []string{"10.0.0.2"}, remoteAddr: "10.0.0.2:5000", headers: map[string][]string{"X-Real-IP": {"198.51.100.9", "198.51.100.9"}, "X-Forwarded-For": {"198.51.100.9"}}, want: ""},
		{name: "Cloudflare edge without preset is unconfigured", remoteAddr: cloudflareEdgePeer, headers: map[string][]string{"CF-Connecting-IP": {"192.168.1.20"}}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			req.RemoteAddr = tt.remoteAddr
			for name, values := range tt.headers {
				for _, v := range values {
					req.Header.Add(name, v)
				}
			}

			got := NewChecker(tt.trustedProxies).AuthClientIP(req)
			if tt.want == "" {
				assert.Nil(t, got, "expected no verifiable client address")
				return
			}
			require.NotNil(t, got)
			assert.Equal(t, tt.want, got.String())
		})
	}
}

// TestChecker_ClientIPFromXFF pins the attribution walk: rightmost first,
// skipping configured proxies, falling back to the leftmost when every hop is
// configured, and returning "" for an empty or malformed chain.
func TestChecker_ClientIPFromXFF(t *testing.T) {
	t.Parallel()

	checker := NewChecker([]string{"10.0.0.0/24"})
	tests := []struct {
		name string
		xff  string
		want string
	}{
		{name: "empty", xff: "", want: ""},
		{name: "single hop", xff: "198.51.100.9", want: "198.51.100.9"},
		{name: "rightmost unconfigured hop wins", xff: "192.0.2.1, 198.51.100.9", want: "198.51.100.9"},
		{name: "configured hops are skipped", xff: "198.51.100.9, 10.0.0.3", want: "198.51.100.9"},
		{name: "all configured falls back to leftmost", xff: "10.0.0.7, 10.0.0.3", want: "10.0.0.7"},
		{name: "zone is stripped", xff: "fe80::1%eth0", want: "fe80::1"},
		{name: "malformed hop", xff: "198.51.100.9, nonsense", want: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, checker.ClientIPFromXFF(tt.xff))
		})
	}
}

// FuzzChecker_AuthClientIP checks the strict resolver's invariants on arbitrary
// header values and peers: an unconfigured peer that sends a client-IP header
// never yields an address, a result is either the peer (no headers) or an
// address named in a header, a header-derived result is never loopback, an
// X-Real-IP naming an unconfigured address must equal any result, a Forwarded
// header always yields nil from a configured proxy, and nothing panics.
func FuzzChecker_AuthClientIP(f *testing.F) {
	f.Add("172.25.5.9:5000", "203.0.113.7", "", "", "", uint8(0b0001))
	f.Add("10.0.0.2:5000", "192.168.1.20, 198.51.100.9", "198.51.100.9", "", "", uint8(0b0011))
	f.Add("10.0.0.2:5000", "", "127.0.0.1", "", "", uint8(0b0010))
	f.Add(cloudflareEdgePeer, "198.51.100.9", "", "198.51.100.9", "", uint8(0b0101))
	f.Add("10.0.0.2:5000", "198.51.100.9", "", "", "192.168.1.20", uint8(0b1001))
	f.Add("10.0.0.2:5000", "198.51.100.9", "", "", "", uint8(0b10001))
	f.Add("[fe80::1%eth0]:5000", "fe80::2%eth0", "", "", "", uint8(0b0001))

	checker := NewChecker([]string{"10.0.0.2", conf.TrustedProxyCloudflarePreset})

	f.Fuzz(func(t *testing.T, remoteAddr, xff, realIP, cfIP, trueClientIP string, present uint8) {
		req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
		req.RemoteAddr = remoteAddr
		headers := []struct {
			name  string
			value string
		}{
			{headerXForwardedFor, xff},
			{headerXRealIP, realIP},
			{HeaderCFConnectingIP, cfIP},
			{headerTrueClientIP, trueClientIP},
			{headerForwarded, "for=" + xff},
		}
		var sent []string
		for i, h := range headers {
			if present&(1<<i) != 0 {
				req.Header.Set(h.name, h.value)
				sent = append(sent, h.value)
			}
		}

		got := checker.AuthClientIP(req)
		if got == nil {
			return
		}

		peerIP, _ := PeerAddr(req)
		if len(sent) == 0 {
			require.True(t, got.Equal(peerIP), "without headers the result must be the peer")
			return
		}
		require.True(t, checker.IsConfiguredProxy(peerIP), "an unconfigured peer with headers must yield nil")
		require.Zero(t, present&(1<<4), "a Forwarded header from a configured proxy must yield nil")
		assert.False(t, got.IsLoopback(), "a header-derived address must never be loopback")
		if present&(1<<1) != 0 {
			if ip := parseStrictIP(strings.TrimSpace(realIP)); ip != nil && !checker.IsConfiguredProxy(ip) {
				assert.True(t, got.Equal(ip), "the result must agree with an X-Real-IP naming a client")
			}
		}
		named := false
		for _, v := range sent {
			for part := range strings.SplitSeq(v, ",") {
				if ip := net.ParseIP(stripZone(strings.TrimSpace(part))); ip != nil && ip.Equal(got) {
					named = true
				}
			}
		}
		assert.True(t, named, "the result must be an address named in a header")
	})
}

// TestCache_ResolveSettings verifies the settings entry point reads
// Security.TrustedProxies and treats nil settings as an empty list.
func TestCache_ResolveSettings(t *testing.T) {
	t.Parallel()

	var cache Cache
	assert.False(t, cache.ResolveSettings(nil).IsConfiguredProxy(mustParseIP(t, "10.0.0.2")), "nil settings trust no proxy")

	settings := &conf.Settings{}
	settings.Security.TrustedProxies = []string{"10.0.0.2"}
	assert.True(t, cache.ResolveSettings(settings).IsConfiguredProxy(mustParseIP(t, "10.0.0.2")), "configured proxy is trusted")
}

// TestChecker_AuthClientIPNilRequest verifies a nil request yields no
// verifiable address instead of panicking.
func TestChecker_AuthClientIPNilRequest(t *testing.T) {
	t.Parallel()

	assert.NotPanics(t, func() {
		assert.Nil(t, NewChecker([]string{"10.0.0.2"}).AuthClientIP(nil))
	})
}
