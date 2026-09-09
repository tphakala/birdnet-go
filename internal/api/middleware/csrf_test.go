package middleware

import (
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDefaultCSRFSkipper(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		method   string
		path     string
		expected bool
	}{
		// Static assets — always skipped regardless of method
		{"GET static asset", http.MethodGet, "/assets/style.css", true},
		{"GET UI asset", http.MethodGet, "/ui/assets/app.js", true},

		// Health check — always skipped
		{"GET health", http.MethodGet, "/health", true},

		// Media/streaming paths — skipped only for safe methods
		{"GET media", http.MethodGet, "/api/v2/media/clip.mp3", true},
		{"HEAD media", http.MethodHead, "/api/v2/media/clip.mp3", true},
		{"OPTIONS media", http.MethodOptions, "/api/v2/media/clip.mp3", true},
		{"POST media", http.MethodPost, "/api/v2/media/clip.mp3", false},
		{"DELETE media", http.MethodDelete, "/api/v2/media/clip.mp3", false},
		{"PUT media", http.MethodPut, "/api/v2/media/clip.mp3", false},
		{"PATCH media", http.MethodPatch, "/api/v2/media/clip.mp3", false},

		{"GET streams", http.MethodGet, "/api/v2/streams/live", true},
		{"POST streams", http.MethodPost, "/api/v2/streams/live", false},

		{"GET spectrogram", http.MethodGet, "/api/v2/spectrogram/123", true},
		{"POST spectrogram", http.MethodPost, "/api/v2/spectrogram/123", false},

		{"GET audio", http.MethodGet, "/api/v2/audio/456", true},
		{"DELETE audio", http.MethodDelete, "/api/v2/audio/456", false},

		// Auth endpoints — always skipped (login/callback need to work before CSRF token exists)
		{"POST auth login", http.MethodPost, "/api/v2/auth/login", true},
		{"GET auth callback", http.MethodGet, "/api/v2/auth/callback/google", true},
		{"GET social OAuth", http.MethodGet, "/auth/google", true},

		// Logout — skipped (must work even with expired CSRF tokens)
		{"POST auth logout", http.MethodPost, "/api/v2/auth/logout", true},

		// pprof — always skipped. go tool pprof POSTs to /symbol with no CSRF
		// token, and the handlers are read-only and separately gated (auth
		// middleware, or a secret query token a cross-site request cannot supply).
		{"GET pprof index", http.MethodGet, "/debug/pprof", true},
		{"GET pprof heap", http.MethodGet, "/debug/pprof/heap", true},
		{"POST pprof symbol", http.MethodPost, "/debug/pprof/symbol", true},

		// A path that merely starts with the same characters is not pprof.
		{"POST debug lookalike", http.MethodPost, "/debug/pprofiler", false},

		// Regular API paths — never skipped
		{"GET detections", http.MethodGet, "/api/v2/detections/1", false},
		{"POST settings", http.MethodPost, "/api/v2/settings", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			c, _ := newTestContext(t, tt.method, tt.path)
			assert.Equal(t, tt.expected, DefaultCSRFSkipper(c), "method: %s, path: %s", tt.method, tt.path)
		})
	}
}

func TestIsSafeHTTPMethod(t *testing.T) {
	t.Parallel()

	tests := []struct {
		method   string
		expected bool
	}{
		{http.MethodGet, true},
		{http.MethodHead, true},
		{http.MethodOptions, true},
		{http.MethodPost, false},
		{http.MethodPut, false},
		{http.MethodPatch, false},
		{http.MethodDelete, false},
	}

	for _, tt := range tests {
		t.Run(tt.method, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.expected, isSafeHTTPMethod(tt.method))
		})
	}
}

func TestIsSecureRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		tls        bool
		header     string
		remoteAddr string // defaults to public IP 192.0.2.1:1234 if empty
		expected   bool
	}{
		{
			name:     "direct TLS connection",
			tls:      true,
			expected: true,
		},
		{
			name:       "X-Forwarded-Proto from loopback",
			header:     "https",
			remoteAddr: "127.0.0.1:54321",
			expected:   true,
		},
		{
			name:       "X-Forwarded-Proto from IPv6 loopback",
			header:     "https",
			remoteAddr: "[::1]:54321",
			expected:   true,
		},
		{
			name:       "X-Forwarded-Proto from private 192.168.x",
			header:     "https",
			remoteAddr: "192.168.1.1:54321",
			expected:   true,
		},
		{
			name:       "X-Forwarded-Proto from private 10.x",
			header:     "https",
			remoteAddr: "10.0.0.1:54321",
			expected:   true,
		},
		{
			name:       "X-Forwarded-Proto from private 172.16.x",
			header:     "https",
			remoteAddr: "172.16.0.1:54321",
			expected:   true,
		},
		{
			name:       "X-Forwarded-Proto from public IP ignored",
			header:     "https",
			remoteAddr: "203.0.113.50:54321",
			expected:   false,
		},
		{
			name:       "X-Forwarded-Proto from default httptest addr ignored",
			header:     "https",
			remoteAddr: "", // httptest default: 192.0.2.1:1234 (public TEST-NET)
			expected:   false,
		},
		{
			name:     "plain HTTP no header",
			expected: false,
		},
		{
			name:       "X-Forwarded-Proto http from loopback",
			header:     "http",
			remoteAddr: "127.0.0.1:54321",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			req := httptest.NewRequest(http.MethodGet, "/", http.NoBody)
			if tt.tls {
				req.TLS = &tls.ConnectionState{}
			}
			if tt.header != "" {
				req.Header.Set("X-Forwarded-Proto", tt.header)
			}
			if tt.remoteAddr != "" {
				req.RemoteAddr = tt.remoteAddr
			}

			assert.Equal(t, tt.expected, IsSecureRequest(req))
		})
	}
}

func TestIsTrustedRemote(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		remoteAddr string
		expected   bool
	}{
		{"loopback IPv4", "127.0.0.1:8080", true},
		{"loopback IPv6", "[::1]:8080", true},
		{"private 10.x", "10.0.0.5:1234", true},
		{"private 172.16.x", "172.16.5.1:1234", true},
		{"private 172.31.x", "172.31.255.255:1234", true},
		{"private 192.168.x", "192.168.0.100:1234", true},
		{"public IP", "8.8.8.8:1234", false},
		{"public 172.32.x not private", "172.32.0.1:1234", false},
		{"empty string", "", false},
		{"invalid address", "not-an-ip:1234", false},
		{"no port", "127.0.0.1", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isTrustedRemote(tt.remoteAddr))
		})
	}
}

// --- GHSA-9fhj-f35q-w532: Sec-Fetch-Site CSRF bypass ---------------------------

// newCSRFTestServer builds an Echo server wired with the production NewCSRF
// middleware plus representative routes, so tests can drive real requests
// through the middleware and Echo's HTTP error handler.
func newCSRFTestServer() *echo.Echo {
	e := echo.New()
	e.Use(NewCSRF(&CSRFConfig{}))

	// Non-skipped state-changing route (any method).
	e.Any("/api/v2/settings", func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})
	// Non-skipped token-provider route (mirrors /api/v2/app/config).
	e.GET("/api/v2/app/config", func(c echo.Context) error {
		token, err := EnsureCSRFToken(c)
		if err != nil {
			return err
		}
		return c.String(http.StatusOK, token)
	})
	// Skipper-exempt route (login must work before a CSRF token exists).
	e.POST("/api/v2/auth/login", func(c echo.Context) error {
		return c.String(http.StatusOK, "login")
	})
	return e
}

// TestNewCSRF_SecFetchSiteBypassBlocked verifies that a state-changing request
// without a CSRF token is rejected regardless of the Sec-Fetch-Site header.
// Before the fix, Sec-Fetch-Site: same-origin or none caused Echo v4.15 to skip
// token validation entirely, letting a non-browser client that forged the header
// call CSRF-protected routes without a token (GHSA-9fhj-f35q-w532).
func TestNewCSRF_SecFetchSiteBypassBlocked(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		secFetchSite string
	}{
		{"no Sec-Fetch-Site (control)", ""},
		{"same-origin", "same-origin"},
		{"none", "none"},
		{"cross-site", "cross-site"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			e := newCSRFTestServer()
			req := httptest.NewRequest(http.MethodPost, "/api/v2/settings", http.NoBody)
			if tt.secFetchSite != "" {
				req.Header.Set(echo.HeaderSecFetchSite, tt.secFetchSite)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"state-changing request without a token must be rejected for Sec-Fetch-Site=%q", tt.secFetchSite)
		})
	}
}

// TestNewCSRF_BypassBlockedAcrossMethods confirms the strip is method-agnostic:
// every unsafe method is forced through token validation under a forged
// Sec-Fetch-Site: same-origin, not just POST.
func TestNewCSRF_BypassBlockedAcrossMethods(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodPut, http.MethodPatch, http.MethodDelete} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			e := newCSRFTestServer()
			req := httptest.NewRequest(method, "/api/v2/settings", http.NoBody)
			req.Header.Set(echo.HeaderSecFetchSite, "same-origin")
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			assert.Equal(t, http.StatusForbidden, rec.Code,
				"%s without a token must be rejected under Sec-Fetch-Site: same-origin", method)
		})
	}
}

// TestNewCSRF_MismatchedTokenRejected confirms a present-but-wrong token is
// rejected, proving the token is actually compared against the cookie rather
// than merely required to be present.
func TestNewCSRF_MismatchedTokenRejected(t *testing.T) {
	t.Parallel()

	e := newCSRFTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/settings", http.NoBody)
	req.Header.Set(echo.HeaderSecFetchSite, "same-origin")
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "cookie-token-value"})
	req.Header.Set("X-CSRF-Token", "a-different-token")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusForbidden, rec.Code,
		"a token not matching the cookie must be rejected")
}

// TestNewCSRF_LegitTokenFlow verifies the SPA flow still works: the config
// endpoint mints a real (non-sentinel) token backed by a cookie, and a
// subsequent state-changing request presenting that token succeeds even with
// Sec-Fetch-Site: same-origin.
func TestNewCSRF_LegitTokenFlow(t *testing.T) {
	t.Parallel()

	e := newCSRFTestServer()

	// 1. Fetch the token as a browser would (same-origin GET).
	getReq := httptest.NewRequest(http.MethodGet, "/api/v2/app/config", http.NoBody)
	getReq.Header.Set(echo.HeaderSecFetchSite, "same-origin")
	getRec := httptest.NewRecorder()
	e.ServeHTTP(getRec, getReq)

	require.Equal(t, http.StatusOK, getRec.Code)
	token := getRec.Body.String()
	require.NotEmpty(t, token)
	require.NotEqual(t, middleware.CSRFUsingSecFetchSite, token,
		"config endpoint must return a real token, not Echo's Sec-Fetch-Site sentinel")

	var csrfCookie *http.Cookie
	for _, ck := range getRec.Result().Cookies() {
		if ck.Name == csrfCookieName {
			csrfCookie = ck
		}
	}
	require.NotNil(t, csrfCookie, "config endpoint must set a CSRF cookie")
	require.Equal(t, token, csrfCookie.Value, "returned token must match the cookie value")

	// 2. Use that token on a state-changing request (same-origin).
	postReq := httptest.NewRequest(http.MethodPost, "/api/v2/settings", http.NoBody)
	postReq.Header.Set(echo.HeaderSecFetchSite, "same-origin")
	postReq.Header.Set("X-CSRF-Token", token)
	postReq.AddCookie(csrfCookie)
	postRec := httptest.NewRecorder()
	e.ServeHTTP(postRec, postReq)

	assert.Equal(t, http.StatusOK, postRec.Code, "valid token must be accepted")
}

// TestNewCSRF_SkippedRouteUnaffected verifies skipper-exempt routes still bypass
// CSRF (login must work before a token exists), even with Sec-Fetch-Site set.
func TestNewCSRF_SkippedRouteUnaffected(t *testing.T) {
	t.Parallel()

	e := newCSRFTestServer()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/auth/login", http.NoBody)
	req.Header.Set(echo.HeaderSecFetchSite, "same-origin")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code, "skipped login route must not require a CSRF token")
}

// TestEnsureCSRFToken_IgnoresSentinel verifies EnsureCSRFToken never returns
// Echo's Sec-Fetch-Site sentinel as if it were a usable token; it mints a real
// token and cookie instead (GHSA-9fhj-f35q-w532 defense in depth).
func TestEnsureCSRFToken_IgnoresSentinel(t *testing.T) {
	t.Parallel()

	c, rec := newTestContext(t, http.MethodGet, "/api/v2/app/config")
	c.Set(CSRFContextKey, middleware.CSRFUsingSecFetchSite)

	token, err := EnsureCSRFToken(c)
	require.NoError(t, err)
	assert.NotEqual(t, middleware.CSRFUsingSecFetchSite, token)
	assert.NotEmpty(t, token)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "should mint a fresh CSRF cookie")
	assert.Equal(t, token, cookies[0].Value)
}
