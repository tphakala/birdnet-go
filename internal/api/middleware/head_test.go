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

func TestNewHeadToGet_LeavesOtherMethodsAlone(t *testing.T) {
	t.Parallel()

	for _, method := range []string{http.MethodGet, http.MethodPost, http.MethodOptions} {
		t.Run(method, func(t *testing.T) {
			t.Parallel()

			c, _ := newTestContext(t, method, "/api/v2/health")

			var seen string
			handler := NewHeadToGet()(func(c echo.Context) error {
				seen = c.Request().Method
				return nil
			})

			require.NoError(t, handler(c))
			assert.Equal(t, method, seen)
		})
	}
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
