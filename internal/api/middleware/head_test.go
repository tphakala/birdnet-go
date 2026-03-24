package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewHeadToGet_RewritesHEADToGET(t *testing.T) {
	t.Parallel()

	c, _ := newTestContext(t, http.MethodHead, "/api/v2/health")

	mw := NewHeadToGet()
	handler := mw(func(c echo.Context) error {
		// By the time the handler runs, method should be GET
		assert.Equal(t, http.MethodGet, c.Request().Method)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
}

func TestNewHeadToGet_DoesNotRewriteGET(t *testing.T) {
	t.Parallel()

	c, _ := newTestContext(t, http.MethodGet, "/api/v2/health")

	mw := NewHeadToGet()
	handler := mw(func(c echo.Context) error {
		assert.Equal(t, http.MethodGet, c.Request().Method)
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)
}

func TestNewHeadToGet_DoesNotRewritePOST(t *testing.T) {
	t.Parallel()

	c, _ := newTestContext(t, http.MethodPost, "/api/v2/settings")

	mw := NewHeadToGet()
	handler := mw(func(c echo.Context) error {
		assert.Equal(t, http.MethodPost, c.Request().Method)
		return nil
	})

	err := handler(c)
	require.NoError(t, err)
}

func TestNewHeadToGet_IntegrationWithRouter(t *testing.T) {
	t.Parallel()

	e := echo.New()

	// Register the pre-middleware
	e.Pre(NewHeadToGet())

	// Register only a GET route
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "hello")
	})

	// Send a HEAD request — should match the GET route
	req := httptest.NewRequest(http.MethodHead, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusOK, rec.Code)
	// net/http suppresses the body for HEAD when using a real server,
	// but httptest.ResponseRecorder does not. The important check is
	// the status code.
}

func TestNewHeadToGet_IntegrationWithRouterWithoutMiddleware(t *testing.T) {
	t.Parallel()

	e := echo.New()

	// Register only a GET route — no pre-middleware
	e.GET("/test", func(c echo.Context) error {
		return c.String(http.StatusOK, "hello")
	})

	// Send a HEAD request — should NOT match (405 Method Not Allowed)
	req := httptest.NewRequest(http.MethodHead, "/test", http.NoBody)
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	assert.Equal(t, http.StatusMethodNotAllowed, rec.Code)
}
