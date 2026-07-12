package apicore

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
)

func Test_SetSSEHeaders(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest("GET", "/test", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	SetSSEHeaders(ctx)

	expectedHeaders := map[string]string{
		"Content-Type":                 "text/event-stream",
		"Cache-Control":                "no-cache",
		"Connection":                   "keep-alive",
		"Access-Control-Allow-Origin":  "*",
		"Access-Control-Allow-Headers": "Cache-Control",
		"X-Accel-Buffering":            "no",
	}

	for key, expectedValue := range expectedHeaders {
		actualValue := rec.Header().Get(key)
		assert.Equal(t, expectedValue, actualValue, "SetSSEHeaders() header %q mismatch", key)
	}
}
