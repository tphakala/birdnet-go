package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
)

// newTestContext creates an Echo context with an HTTP request and response recorder.
// Returns the context and recorder for response inspection.
func newTestContext(t *testing.T, method, path string) (echo.Context, *httptest.ResponseRecorder) {
	t.Helper()
	e := echo.New()
	req := httptest.NewRequest(method, path, http.NoBody)
	rec := httptest.NewRecorder()
	return e.NewContext(req, rec), rec
}
