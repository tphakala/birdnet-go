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
		path     string
		expected bool
	}{
		// Should be skipped
		{"/assets/style.css", true},
		{"/ui/assets/app.js", true},
		{"/health", true},
		{"/api/v2/media/clip.mp3", true},
		{"/api/v2/streams/live", true},
		{"/api/v2/spectrogram/123", true},
		{"/api/v2/audio/456", true},
		{"/api/v2/auth/login", true},
		{"/api/v2/auth/callback/google", true},
		{"/auth/google", true},

		// Should NOT be skipped
		{"/api/v2/auth/logout", false},
		{"/api/v2/detections/1", false},
		{"/api/v2/settings", false},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			t.Parallel()

			c, _ := newTestContext(t, http.MethodGet, tt.path)
			assert.Equal(t, tt.expected, DefaultCSRFSkipper(c), "path: %s", tt.path)
		})
	}
}

func TestIsSecureRequest(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		tls      bool
		header   string
		expected bool
	}{
		{
			name:     "direct TLS connection",
			tls:      true,
			expected: true,
		},
		{
			name:     "X-Forwarded-Proto https",
			header:   "https",
			expected: true,
		},
		{
			name:     "plain HTTP",
			expected: false,
		},
		{
			name:     "X-Forwarded-Proto http",
			header:   "http",
			expected: false,
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

			assert.Equal(t, tt.expected, IsSecureRequest(req))
		})
	}
}
