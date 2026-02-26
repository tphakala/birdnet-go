package middleware

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestCSRFCookieRefresh_RefreshesCookieWhenPresent(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/1", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-token-value"})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := CSRFCookieRefresh()(func(c echo.Context) error {
		return nil
	})

	err := handler(c)
	require.NoError(t, err)

	// The middleware should have set a refreshed cookie
	cookies := rec.Result().Cookies()
	require.Len(t, cookies, 1, "expected exactly one Set-Cookie header")
	assert.Equal(t, csrfCookieName, cookies[0].Name)
	assert.Equal(t, "test-token-value", cookies[0].Value)
	assert.Equal(t, csrfCookieMaxAge, cookies[0].MaxAge)
}

func TestCSRFCookieRefresh_NoCookiePresent(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/1", http.NoBody)
	// No CSRF cookie attached
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := CSRFCookieRefresh()(func(c echo.Context) error {
		return nil
	})

	err := handler(c)
	require.NoError(t, err)

	// No cookie should be set when none was present
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

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
			req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-token"})
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := CSRFCookieRefresh()(func(c echo.Context) error {
				return nil
			})

			err := handler(c)
			require.NoError(t, err)

			// Skipped paths should not refresh the cookie
			cookies := rec.Result().Cookies()
			assert.Empty(t, cookies, "skipped path %s should not refresh cookie", path)
		})
	}
}

func TestCSRFCookieRefresh_DoesNotRefreshOnError(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review", http.NoBody)
	req.AddCookie(&http.Cookie{Name: csrfCookieName, Value: "test-token"})
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handlerErr := errors.New("handler failed")
	handler := CSRFCookieRefresh()(func(c echo.Context) error {
		return handlerErr
	})

	err := handler(c)
	require.ErrorIs(t, err, handlerErr)

	// Should not refresh cookie when handler returned an error
	cookies := rec.Result().Cookies()
	assert.Empty(t, cookies, "should not refresh cookie on handler error")
}
