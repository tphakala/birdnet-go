package middleware

import (
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestContextWithCookie creates an Echo context with a CSRF cookie pre-set.
func newTestContextWithCookie(t *testing.T, method, path, cookieValue string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, path, http.NoBody)
	if cookieValue != "" {
		req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: cookieValue})
	}
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}

func TestCSRFCookieRefresh_RefreshesCookieWhenPresent(t *testing.T) {
	t.Parallel()

	c, rec := newTestContextWithCookie(t, http.MethodGet, "/api/v2/detections/1", "test-token-value")

	handler := CSRFCookieRefresh(nil)(func(c echo.Context) error {
		return nil
	})

	err := handler(c)
	require.NoError(t, err)

	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "expected exactly one Set-Cookie header")
	assert.Equal(t, csrfCookieName, cookies[0].Name)
	assert.Equal(t, "test-token-value", cookies[0].Value)
	assert.Equal(t, csrfCookieMaxAge, cookies[0].MaxAge)
}

func TestCSRFCookieRefresh_NoCookiePresent(t *testing.T) {
	t.Parallel()

	c, rec := newTestContext(t, http.MethodGet, "/api/v2/detections/1")

	handler := CSRFCookieRefresh(nil)(func(c echo.Context) error {
		return nil
	})

	err := handler(c)
	require.NoError(t, err)

	cookies := rec.Result().Cookies()
	assert.Empty(t, cookies, "should not set cookie when none exists")
}

func TestCSRFCookieRefresh_SkipsStaticAssets(t *testing.T) {
	t.Parallel()

	skippedPaths := []string{
		"/assets/style.css",
		"/ui/assets/app.js",
		"/health",
		"/api/v2/media/clip.mp3",
		"/api/v2/spectrogram/123",
		"/api/v2/audio/456",
	}

	for _, path := range skippedPaths {
		t.Run(path, func(t *testing.T) {
			t.Parallel()

			c, rec := newTestContextWithCookie(t, http.MethodGet, path, "test-token")

			handler := CSRFCookieRefresh(nil)(func(c echo.Context) error {
				return nil
			})

			err := handler(c)
			require.NoError(t, err)

			cookies := rec.Result().Cookies()
			assert.Empty(t, cookies, "skipped path %s should not refresh cookie", path)
		})
	}
}

func TestCSRFCookieRefresh_EmptyCookieValue(t *testing.T) {
	t.Parallel()

	// AddCookie with empty value directly since helper skips empty values
	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/1", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: ""})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := CSRFCookieRefresh(nil)(func(c echo.Context) error {
		return nil
	})

	err := handler(c)
	require.NoError(t, err)

	cookies := rec.Result().Cookies()
	assert.Empty(t, cookies, "should not refresh cookie with empty value")
}

func TestCSRFCookieRefresh_DoesNotRefreshOnError(t *testing.T) {
	t.Parallel()

	c, rec := newTestContextWithCookie(t, http.MethodPost, "/api/v2/detections/1/review", "test-token")

	handlerErr := errors.New("handler failed")
	handler := CSRFCookieRefresh(nil)(func(c echo.Context) error {
		return handlerErr
	})

	err := handler(c)
	require.ErrorIs(t, err, handlerErr)

	cookies := rec.Result().Cookies()
	assert.Empty(t, cookies, "should not refresh cookie on handler error")
}

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

		// Regular API paths — never skipped
		{"GET auth logout", http.MethodGet, "/api/v2/auth/logout", false},
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
