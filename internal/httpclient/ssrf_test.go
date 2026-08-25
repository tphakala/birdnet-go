package httpclient

import (
	"net/http"
	"net/http/httptest"
	"net/netip"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func TestIsBlockedTargetIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		ip      string
		blocked bool
	}{
		// Blocked: link-local (covers 169.254.169.254 cloud metadata).
		{"aws/gcp/azure metadata", "169.254.169.254", true},
		{"link-local unicast v4", "169.254.10.5", true},
		{"link-local unicast v6", "fe80::1", true},
		{"ipv4-mapped metadata", "::ffff:169.254.169.254", true},
		{"alibaba metadata", "100.100.100.200", true},
		{"aws imds ipv6", "fd00:ec2::254", true},
		{"unspecified v4", "0.0.0.0", true},
		{"unspecified v6", "::", true},

		// Blocked: link-local metadata smuggled through IPv6 transition ranges.
		{"nat64 metadata", "64:ff9b::a9fe:a9fe", true},    // embeds 169.254.169.254
		{"6to4 metadata", "2002:a9fe:a9fe::", true},       // embeds 169.254.169.254
		{"teredo metadata", "2001::5601:5601", true},      // client IPv4 inverts to 169.254.169.254
		{"ipv4-compatible metadata", "::a9fe:a9fe", true}, // deprecated ::a.b.c.d embeds 169.254.169.254

		// Allowed: loopback and private ranges (on-LAN webhook targets).
		{"loopback v4", "127.0.0.1", false},
		{"loopback v6", "::1", false},
		{"rfc1918 10.x", "10.0.0.1", false},
		{"rfc1918 192.168.x", "192.168.1.10", false},
		{"rfc1918 172.16.x", "172.16.5.4", false},
		{"cgnat non-metadata", "100.64.0.1", false},
		{"ula non-metadata", "fd12:3456:789a::1", false},
		{"nat64 public", "64:ff9b::808:808", false},    // embeds 8.8.8.8, allowed
		{"ipv4-compatible public", "::808:808", false}, // ::8.8.8.8, allowed
		{"public v4", "8.8.8.8", false},
		{"public v6", "2606:4700:4700::1111", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			addr, err := netip.ParseAddr(tt.ip)
			require.NoError(t, err)
			assert.Equal(t, tt.blocked, isBlockedTargetIP(addr))
		})
	}
}

// TestGuardedClientAllowsLoopback verifies the guard permits loopback so a
// webhook can reach a service on the local host (the policy intentionally
// allows loopback and RFC1918).
func TestGuardedClientAllowsLoopback(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.BlockLinkLocalAndMetadata = true
	client := New(&cfg)
	t.Cleanup(client.Close)

	resp, err := client.Get(t.Context(), srv.URL)
	require.NoError(t, err)
	t.Cleanup(func() { _ = resp.Body.Close() })
	assert.Equal(t, http.StatusOK, resp.StatusCode)
}

// TestGuardedClientBlocksMetadataLiteral verifies a direct request to the cloud
// metadata address is refused at dial time with ErrBlockedTarget.
func TestGuardedClientBlocksMetadataLiteral(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	cfg.BlockLinkLocalAndMetadata = true
	client := New(&cfg)
	t.Cleanup(client.Close)

	resp, err := client.Get(t.Context(), "http://169.254.169.254/latest/meta-data/")
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedTarget), "expected ErrBlockedTarget, got %v", err)
}

// TestGuardedClientBlocksRedirectToMetadata verifies the guard re-applies on
// redirects: a loopback server that 302s to the metadata endpoint must not be
// followed to the blocked target.
func TestGuardedClientBlocksRedirectToMetadata(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://169.254.169.254/latest/meta-data/", http.StatusFound)
	}))
	t.Cleanup(srv.Close)

	cfg := DefaultConfig()
	cfg.BlockLinkLocalAndMetadata = true
	client := New(&cfg)
	t.Cleanup(client.Close)

	resp, err := client.Get(t.Context(), srv.URL)
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedTarget), "expected ErrBlockedTarget on redirect, got %v", err)
}

// TestGuardedClientIgnoresProxyForMetadata confirms a configured environment
// proxy cannot be used to bypass the guard: enabling the guard disables proxy
// support, so a metadata request is still blocked at dial time rather than
// tunneled through the proxy. Not parallel: it sets a process env var.
func TestGuardedClientIgnoresProxyForMetadata(t *testing.T) {
	t.Setenv("HTTPS_PROXY", "http://127.0.0.1:1/")
	t.Setenv("HTTP_PROXY", "http://127.0.0.1:1/")

	cfg := DefaultConfig()
	cfg.BlockLinkLocalAndMetadata = true
	cfg.DefaultTimeout = 2 * time.Second
	client := New(&cfg)
	t.Cleanup(client.Close)

	// Uses https so a live proxy would be reached via CONNECT; with the guard
	// enabled the proxy is disabled and the metadata IP is blocked directly.
	resp, err := client.Get(t.Context(), "https://169.254.169.254/latest/meta-data/")
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrBlockedTarget), "expected ErrBlockedTarget with proxy set, got %v", err)
}

// TestUnguardedClientDoesNotUseGuard confirms the guard is opt-in: without the
// flag the dialer applies no SSRF policy. It targets 192.0.2.1 (RFC 5737
// TEST-NET-1, guaranteed unrouted and never a live service) with a short
// timeout, so the dial fails locally rather than hanging. Crucially it does NOT
// target a real metadata address: on a cloud CI runner 169.254.169.254 would
// answer and make the assertion flaky. Whatever the outcome, the error must not
// be an SSRF-policy block.
func TestUnguardedClientDoesNotUseGuard(t *testing.T) {
	t.Parallel()

	cfg := DefaultConfig()
	// BlockLinkLocalAndMetadata left false.
	cfg.DefaultTimeout = 500 * time.Millisecond
	client := New(&cfg)
	t.Cleanup(client.Close)

	resp, err := client.Get(t.Context(), "http://192.0.2.1/")
	if resp != nil {
		t.Cleanup(func() { _ = resp.Body.Close() })
	}
	assert.False(t, errors.Is(err, ErrBlockedTarget), "unguarded client must not apply the SSRF policy")
}
