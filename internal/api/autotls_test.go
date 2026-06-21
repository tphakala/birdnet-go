package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/testutil"
)

const testHost = "birdnet.example.com"

// TestConfigFromSettings_AutoTLS verifies that AutoTLS mode produces a config
// with TLSPort set (HTTPS listener) and Port preserved (HTTP/ACME listener).
func TestConfigFromSettings_AutoTLS(t *testing.T) {
	t.Parallel()

	settings := conftest.NewTestSettings().WithWebServer("8080", true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost

	cfg := ConfigFromSettings(settings)

	assert.True(t, cfg.AutoTLS, "AutoTLS flag should be set")
	assert.True(t, cfg.TLSEnabled, "TLSEnabled should be set")
	assert.Equal(t, "8080", cfg.Port, "HTTP port preserved for ACME challenges")
	assert.Equal(t, "8443", cfg.TLSPort, "TLS port should default to 8443")
	assert.Equal(t, ":8443", cfg.TLSAddress(), "TLS address should use TLSPort")
	assert.Equal(t, ":8080", cfg.Address(), "HTTP address should use Port")
}

// TestConfigFromSettings_AutoTLS_CustomTLSPort verifies that a user-configured
// TLS port is respected in AutoTLS mode.
func TestConfigFromSettings_AutoTLS_CustomTLSPort(t *testing.T) {
	t.Parallel()

	settings := conftest.NewTestSettings().WithWebServer("8080", true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.TLSPort = "9443"

	cfg := ConfigFromSettings(settings)

	assert.Equal(t, "9443", cfg.TLSPort)
	assert.Equal(t, ":9443", cfg.TLSAddress())
}

// TestConfigFromSettings_AutoTLS_PortConflict verifies that when TLSPort equals
// the HTTP port, the conflict is resolved.
func TestConfigFromSettings_AutoTLS_PortConflict(t *testing.T) {
	t.Parallel()

	settings := conftest.NewTestSettings().WithWebServer("8443", true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.TLSPort = "8443"

	cfg := ConfigFromSettings(settings)

	assert.NotEqual(t, cfg.Port, cfg.TLSPort,
		"TLS port must differ from HTTP port when there's a conflict")
}

// TestAutoTLS_DualListeners verifies that AutoTLS starts both an HTTP listener
// (for ACME HTTP-01 challenges) and a TLS listener (for HTTPS).
// This is a regression test for https://github.com/tphakala/birdnet-go/issues/3527
func TestAutoTLS_DualListeners(t *testing.T) {
	t.Parallel()

	httpPort := mustGetFreePort(t)
	tlsPort := mustGetFreePort(t)

	settings := conftest.NewTestSettings().WithWebServer(httpPort, true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.TLSPort = tlsPort
	conftest.SetTestSettings(settings)

	cfg := ConfigFromSettings(settings)
	require.Equal(t, tlsPort, cfg.TLSPort)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{
		config:   cfg,
		settings: settings,
		echo:     e,
		slogger:  GetLogger(),
	}

	go func() {
		_ = s.startBlocking()
	}()

	// Wait for listeners to bind
	requirePortOpen(t, httpPort)
	requirePortOpen(t, tlsPort)

	client := &http.Client{
		Timeout: testutil.ShortTestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// HTTP listener should handle ACME challenge paths (not 404)
	httpBase := fmt.Sprintf("http://127.0.0.1:%s", httpPort)
	resp, err := client.Get(httpBase + "/.well-known/acme-challenge/test-token")
	require.NoError(t, err, "HTTP listener should be active on port %s", httpPort)
	_ = resp.Body.Close()
	assert.NotEqual(t, http.StatusNotFound, resp.StatusCode,
		"ACME path should be handled by autocert, not return 404")

	// Non-ACME GET should be served by Echo (RedirectToHTTPS is not set,
	// so the fallback handler is the app itself, not a redirect).
	resp, err = client.Get(httpBase + "/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.NotEqual(t, http.StatusBadGateway, resp.StatusCode,
		"HTTP listener should serve requests, not fail")

	// TLS listener should accept TCP connections (full TLS handshake won't
	// complete without a real cert, but a successful TCP connect proves the
	// listener is bound to the TLS port, not the HTTP port).
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%s", tlsPort), testutil.ShortTestTimeout)
	require.NoError(t, err, "TLS listener should be active on port %s", tlsPort)
	_ = conn.Close()

	// Clean shutdown — use a real timeout so listeners fully drain.
	// t.Context() is cancelled immediately when the test returns, causing goroutine leaks.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second) //nolint:gocritic // intentional: t.Context() cancels too early
	defer cancel()
	_ = s.echo.Shutdown(ctx)
	if s.httpRedirectServer != nil {
		_ = s.httpRedirectServer.Shutdown(ctx)
	}
}

func mustGetFreePort(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	port := l.Addr().(*net.TCPAddr).Port
	_ = l.Close()
	return fmt.Sprintf("%d", port)
}

func requirePortOpen(t *testing.T, port string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", "127.0.0.1:"+port, 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatalf("port %s did not open within 2s", port)
}
