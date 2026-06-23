package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"golang.org/x/crypto/acme/autocert"

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
	assert.True(t, cfg.RedirectToHTTPS, "RedirectToHTTPS should default to true for AutoTLS")
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

	// Echo.Shutdown returns early (skipping Server.Shutdown) if TLSServer.Shutdown
	// returns context.Canceled — which happens when ctx is already done. Use an
	// independent context so both servers are fully shut down.
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) //nolint:gocritic // Echo.Shutdown skips Server.Shutdown if TLSServer.Shutdown returns context.Canceled
		defer cancel()
		_ = s.echo.Shutdown(ctx)
		if s.httpRedirectServer != nil {
			_ = s.httpRedirectServer.Shutdown(ctx)
		}
	})

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

	// Non-ACME GET should redirect to HTTPS on the custom TLS port
	// (RedirectToHTTPS defaults to true for AutoTLS).
	resp, err = client.Get(httpBase + "/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode,
		"Non-ACME request should redirect to HTTPS")
	location := resp.Header.Get("Location")
	assert.Contains(t, location, ":"+tlsPort,
		"Redirect should include custom TLS port")

	// TLS listener should accept TCP connections (full TLS handshake won't
	// complete without a real cert, but a successful TCP connect proves the
	// listener is bound to the TLS port, not the HTTP port).
	conn, err := net.DialTimeout("tcp", fmt.Sprintf("127.0.0.1:%s", tlsPort), testutil.ShortTestTimeout)
	require.NoError(t, err, "TLS listener should be active on port %s", tlsPort)
	_ = conn.Close()
}

// TestAutoTLS_RedirectIPv6 verifies that the redirect handler produces valid
// Location URLs when the Host header contains an IPv6 address.
func TestAutoTLS_RedirectIPv6(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		host     string
		tlsPort  string
		wantHost string
	}{
		{
			name:     "IPv6 loopback with port",
			host:     "[::1]:8080",
			tlsPort:  "8443",
			wantHost: "[::1]:8443",
		},
		{
			name:     "IPv6 loopback without port",
			host:     "[::1]",
			tlsPort:  "8443",
			wantHost: "[::1]:8443",
		},
		{
			name:     "IPv6 full address",
			host:     "[2001:db8::1]:8080",
			tlsPort:  "8443",
			wantHost: "[2001:db8::1]:8443",
		},
		{
			name:     "IPv4 still works",
			host:     "192.168.1.1:8080",
			tlsPort:  "8443",
			wantHost: "192.168.1.1:8443",
		},
		{
			name:     "hostname still works",
			host:     "birdnet.example.com:8080",
			tlsPort:  "9443",
			wantHost: "birdnet.example.com:9443",
		},
		{
			name:     "IPv6 with port 443 omits port",
			host:     "[::1]:80",
			tlsPort:  "443",
			wantHost: "[::1]",
		},
		{
			name:     "hostname with port 443 omits port",
			host:     "birdnet.example.com:80",
			tlsPort:  "443",
			wantHost: "birdnet.example.com",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			s := &Server{
				config: &Config{
					TLSPort:         tc.tlsPort,
					RedirectToHTTPS: true,
					ReadTimeout:     5 * time.Second,
					WriteTimeout:    5 * time.Second,
				},
				slogger: GetLogger(),
			}

			srv := s.newHTTPRedirectServer(":0", tc.tlsPort)
			req := httptest.NewRequest(http.MethodGet, "/some/path?q=1", http.NoBody)
			req.Host = tc.host
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			resp := rec.Result()
			_ = resp.Body.Close()

			loc := resp.Header.Get("Location")
			require.NotEmpty(t, loc, "Location header must be set")

			parsed, err := url.Parse(loc)
			require.NoError(t, err, "Location must be a valid URL, got: %s", loc)
			assert.Equal(t, "https", parsed.Scheme)
			assert.Equal(t, tc.wantHost, parsed.Host,
				"Host portion of redirect URL should be well-formed")
			assert.Equal(t, "/some/path?q=1", parsed.RequestURI())
		})
	}
}

// TestAutoTLS_RedirectUsesStatusPermanentRedirect verifies that HTTP->HTTPS
// redirects use 308 (Permanent Redirect) to preserve request methods, not 301
// which converts POST to GET.
func TestAutoTLS_RedirectUsesStatusPermanentRedirect(t *testing.T) {
	t.Parallel()

	s := &Server{
		config: &Config{
			TLSPort:         "8443",
			RedirectToHTTPS: true,
			ReadTimeout:     5 * time.Second,
			WriteTimeout:    5 * time.Second,
		},
		slogger: GetLogger(),
	}

	srv := s.newHTTPRedirectServer(":0", "8443")

	methods := []string{http.MethodGet, http.MethodPost, http.MethodPut, http.MethodDelete}
	for _, method := range methods {
		t.Run(method, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(method, "/api/v2/data", http.NoBody)
			req.Host = "birdnet.example.com:8080"
			rec := httptest.NewRecorder()

			srv.Handler.ServeHTTP(rec, req)

			resp := rec.Result()
			_ = resp.Body.Close()
			assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode,
				"redirect should use 308 to preserve %s method", method)
		})
	}
}

// TestAutoTLS_DualListeners_RedirectStatus verifies the AutoTLS inline redirect
// handler also uses 308 and produces valid IPv6 URLs.
func TestAutoTLS_DualListeners_RedirectStatus(t *testing.T) {
	t.Parallel()

	httpPort := mustGetFreePort(t)
	tlsPort := mustGetFreePort(t)

	settings := conftest.NewTestSettings().WithWebServer(httpPort, true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.TLSPort = tlsPort
	conftest.SetTestSettings(settings)

	cfg := ConfigFromSettings(settings)

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true

	s := &Server{
		config:   cfg,
		settings: settings,
		echo:     e,
		slogger:  GetLogger(),
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_ = s.echo.Shutdown(ctx)
		if s.httpRedirectServer != nil {
			_ = s.httpRedirectServer.Shutdown(ctx)
		}
	})

	go func() {
		_ = s.startBlocking()
	}()

	requirePortOpen(t, httpPort)

	client := &http.Client{
		Timeout: testutil.ShortTestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// POST should get a 308, not 301 (which would convert POST to GET)
	httpBase := fmt.Sprintf("http://127.0.0.1:%s", httpPort)
	req, err := http.NewRequest(http.MethodPost, httpBase+"/api/v2/data", http.NoBody)
	require.NoError(t, err)
	resp, err := client.Do(req)
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode,
		"AutoTLS redirect should use 308 to preserve POST method")

	loc := resp.Header.Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err, "Location must be a valid URL")
	assert.Equal(t, "https", parsed.Scheme)
	assert.Contains(t, parsed.Host, ":"+tlsPort)
}

// TestAutoTLS_Port443_AutocertRedirect verifies that when TLSPort is "443",
// the httpFallback is nil and autocert's built-in redirect handles non-ACME
// traffic (redirecting to https:// without an explicit port).
// This tests the handler directly rather than starting a full server, because
// binding port 443 requires root.
func TestAutoTLS_Port443_AutocertRedirect(t *testing.T) {
	t.Parallel()

	// When TLSPort == "443", our code passes nil as the fallback to
	// autocert.Manager.HTTPHandler(nil). autocert's default behavior for
	// non-ACME traffic is to redirect to HTTPS on the same host.
	mgr := &autocert.Manager{}
	handler := mgr.HTTPHandler(nil) // same as what our code does for port 443

	req := httptest.NewRequest(http.MethodGet, "/dashboard", http.NoBody)
	req.Host = "birdnet.example.com"
	rec := httptest.NewRecorder()

	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	_ = resp.Body.Close()

	// autocert uses 302 for its built-in redirect
	assert.Equal(t, http.StatusFound, resp.StatusCode,
		"autocert nil-fallback should redirect non-ACME traffic")
	loc := resp.Header.Get("Location")
	parsed, err := url.Parse(loc)
	require.NoError(t, err)
	assert.Equal(t, "https", parsed.Scheme)
	assert.Equal(t, "birdnet.example.com", parsed.Host,
		"redirect to port 443 should omit the port")
	assert.Equal(t, "/dashboard", parsed.Path)
}

// TestAutoTLS_RedirectDisabled_ServesApp verifies that when RedirectToHTTPS is
// false, the HTTP listener serves the application instead of redirecting.
func TestAutoTLS_RedirectDisabled_ServesApp(t *testing.T) {
	t.Parallel()

	httpPort := mustGetFreePort(t)
	tlsPort := mustGetFreePort(t)

	settings := conftest.NewTestSettings().WithWebServer(httpPort, true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.TLSPort = tlsPort
	settings.Security.RedirectToHTTPS = false
	conftest.SetTestSettings(settings)

	cfg := ConfigFromSettings(settings)
	cfg.RedirectToHTTPS = false // override the default-true logic

	e := echo.New()
	e.HideBanner = true
	e.HidePort = true
	e.GET("/health", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	s := &Server{
		config:   cfg,
		settings: settings,
		echo:     e,
		slogger:  GetLogger(),
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
		defer cancel()
		_ = s.echo.Shutdown(ctx)
		if s.httpRedirectServer != nil {
			_ = s.httpRedirectServer.Shutdown(ctx)
		}
	})

	go func() {
		_ = s.startBlocking()
	}()

	requirePortOpen(t, httpPort)

	client := &http.Client{
		Timeout: testutil.ShortTestTimeout,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	// With RedirectToHTTPS=false, the app should be served over plain HTTP
	httpBase := fmt.Sprintf("http://127.0.0.1:%s", httpPort)
	resp, err := client.Get(httpBase + "/health")
	require.NoError(t, err)
	_ = resp.Body.Close()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"with RedirectToHTTPS=false, HTTP should serve app content, not redirect")
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
