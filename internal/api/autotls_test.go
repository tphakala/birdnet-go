package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/spf13/viper"
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
	assert.Equal(t, testHost, cfg.RedirectAuthority,
		"redirect authority should be the advertised host, not the internal TLS port")
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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) //nolint:gocritic // t.Context() is already cancelled in Cleanup
		defer cancel()
		_ = s.echo.Shutdown(ctx)
		if srv := s.httpRedirectServer.Load(); srv != nil {
			_ = srv.Shutdown(ctx)
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

	// Non-ACME GET should redirect to HTTPS on the advertised host, NOT the
	// internal TLS port (unreachable behind container port mappings).
	// RedirectToHTTPS defaults to true for AutoTLS.
	resp, err = client.Get(httpBase + "/")
	require.NoError(t, err)
	_ = resp.Body.Close()
	assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode,
		"Non-ACME request should redirect to HTTPS")
	location := resp.Header.Get("Location")
	parsedLoc, err := url.Parse(location)
	require.NoError(t, err, "Location must be a valid URL, got: %s", location)
	assert.Equal(t, "https", parsedLoc.Scheme)
	assert.Equal(t, testHost, parsedLoc.Host,
		"redirect should target the advertised host with no internal port")
	assert.NotContains(t, location, ":"+tlsPort,
		"redirect must not leak the internal TLS port")

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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) //nolint:gocritic // t.Context() is already cancelled in Cleanup
		defer cancel()
		_ = s.echo.Shutdown(ctx)
		if srv := s.httpRedirectServer.Load(); srv != nil {
			_ = srv.Shutdown(ctx)
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
	assert.Equal(t, testHost, parsed.Host,
		"redirect should target the advertised host, not the internal TLS port")
}

// TestHTTPSRedirectHandler verifies redirect target construction: an explicit
// external authority is used verbatim; otherwise the request host is reused with
// the port omitted for "" / "443" and appended (IPv6-safe) for a custom port.
func TestHTTPSRedirectHandler(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		externalAuthority string
		fallbackPort      string
		reqHost           string
		wantHost          string
	}{
		{"external authority wins", "birdnet.example.com", "", "internal:8080", "birdnet.example.com"},
		{"external authority wins over fallback port", "birdnet.example.com", "8443", "internal:8080", "birdnet.example.com"},
		{"external authority keeps its port", "birdnet.example.com:9443", "", "internal:8080", "birdnet.example.com:9443"},
		{"no authority omits empty port", "", "", "host.example.com:8080", "host.example.com"},
		{"no authority omits 443", "", "443", "host.example.com:80", "host.example.com"},
		{"no authority appends custom port", "", "8443", "host.example.com:8080", "host.example.com:8443"},
		{"IPv6 omits port", "", "", "[::1]:8080", "[::1]"},
		{"IPv6 appends custom port", "", "8443", "[2001:db8::1]:8080", "[2001:db8::1]:8443"},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			h := httpsRedirectHandler(tc.externalAuthority, tc.fallbackPort)
			req := httptest.NewRequest(http.MethodGet, "/some/path?q=1", http.NoBody)
			req.Host = tc.reqHost
			rec := httptest.NewRecorder()

			h.ServeHTTP(rec, req)

			resp := rec.Result()
			_ = resp.Body.Close()

			assert.Equal(t, http.StatusPermanentRedirect, resp.StatusCode,
				"redirect should use 308 to preserve the request method")
			parsed, err := url.Parse(resp.Header.Get("Location"))
			require.NoError(t, err)
			assert.Equal(t, "https", parsed.Scheme)
			assert.Equal(t, tc.wantHost, parsed.Host)
			assert.Equal(t, "/some/path?q=1", parsed.RequestURI())
		})
	}
}

// TestConfigFromSettings_AutoTLS_RedirectDefault checks the redirect-default
// POLICY: default true, overridden only by an explicit user value. It stubs
// redirectExplicitlySet, so it does not itself guard the viper.IsSet trap; that
// is TestConfigFromSettings_AutoTLS_RedirectDefault_RealDetection below.
func TestConfigFromSettings_AutoTLS_RedirectDefault(t *testing.T) {
	tests := []struct {
		name          string
		explicitlySet bool
		userValue     bool
		want          bool
	}{
		{"defaults to true when unset", false, false, true},
		{"explicit false is honored", true, false, false},
		{"explicit true is honored", true, true, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			orig := redirectExplicitlySet
			t.Cleanup(func() { redirectExplicitlySet = orig })
			redirectExplicitlySet = func() bool { return tc.explicitlySet }

			settings := conftest.NewTestSettings().WithWebServer("8080", true).Build()
			settings.Security.TLSMode = conf.TLSModeAutoTLS
			settings.Security.Host = testHost
			settings.Security.RedirectToHTTPS = tc.userValue

			cfg := ConfigFromSettings(settings)
			assert.Equal(t, tc.want, cfg.RedirectToHTTPS)
		})
	}
}

// TestConfigFromSettings_AutoTLS_RedirectEnvOverride exercises the real
// redirectExplicitlySet detection: an explicit BIRDNET_SECURITY_REDIRECTTOHTTPS
// env var disables the AutoTLS redirect default.
func TestConfigFromSettings_AutoTLS_RedirectEnvOverride(t *testing.T) {
	t.Setenv(conf.EnvVarSecurityRedirect, "false")

	settings := conftest.NewTestSettings().WithWebServer("8080", true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.RedirectToHTTPS = false

	cfg := ConfigFromSettings(settings)
	assert.False(t, cfg.RedirectToHTTPS,
		"an explicit env override should disable the AutoTLS redirect default")
}

// TestConfigFromSettings_AutoTLS_RedirectDefault_RealDetection reproduces the
// production trigger for the viper.IsSet trap: defaults.go registers a default
// for security.redirecttohttps, which made viper.IsSet always return true and
// collapse the AutoTLS redirect to false. It exercises the REAL detection (no
// stub), so it fails on any viper.IsSet-based implementation regardless of the
// test binary's viper state. Not parallel: it mutates the global viper.
func TestConfigFromSettings_AutoTLS_RedirectDefault_RealDetection(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	// Mirror production: defaults.go registers this default on every config load.
	viper.SetDefault(conf.ConfigKeySecurityRedirect, false)

	settings := conftest.NewTestSettings().WithWebServer("8080", true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.RedirectToHTTPS = false // the (default) stored value

	cfg := ConfigFromSettings(settings)
	assert.True(t, cfg.RedirectToHTTPS,
		"AutoTLS must default to redirect=true even when a viper default is registered (the viper.IsSet trap)")
}

// TestConfigFromSettings_AutoTLS_RedirectFromConfigFile exercises the real
// viper.InConfig half of redirectExplicitlySet: an explicit config-file value
// must be honored (and the config key must be correct). Not parallel: it mutates
// the global viper.
func TestConfigFromSettings_AutoTLS_RedirectFromConfigFile(t *testing.T) {
	viper.Reset()
	t.Cleanup(viper.Reset)
	viper.SetDefault(conf.ConfigKeySecurityRedirect, false)

	cfgFile := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfgFile, []byte("security:\n  redirecttohttps: false\n"), 0o600))
	viper.SetConfigFile(cfgFile)
	require.NoError(t, viper.ReadInConfig())

	settings := conftest.NewTestSettings().WithWebServer("8080", true).Build()
	settings.Security.TLSMode = conf.TLSModeAutoTLS
	settings.Security.Host = testHost
	settings.Security.RedirectToHTTPS = false // explicit false present in the config file

	cfg := ConfigFromSettings(settings)
	assert.False(t, cfg.RedirectToHTTPS,
		"an explicit config-file redirecttohttps:false must be honored for AutoTLS (viper.InConfig branch)")
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
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second) //nolint:gocritic // t.Context() is already cancelled in Cleanup
		defer cancel()
		_ = s.echo.Shutdown(ctx)
		if srv := s.httpRedirectServer.Load(); srv != nil {
			_ = srv.Shutdown(ctx)
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
