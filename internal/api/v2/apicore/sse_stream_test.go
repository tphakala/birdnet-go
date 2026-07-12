package apicore

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

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

func Test_DisableProxyBuffering(t *testing.T) {
	t.Parallel()

	e := echo.New()
	req := httptest.NewRequest("GET", "/test", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	DisableProxyBuffering(ctx)

	assert.Equal(t, "no", rec.Header().Get("X-Accel-Buffering"),
		"DisableProxyBuffering must set X-Accel-Buffering: no")
}

// deadlineRecorder is a ResponseWriter that records SetWriteDeadline calls so
// tests can assert SetStreamWriteDeadline wired the deadline through.
type deadlineRecorder struct {
	*httptest.ResponseRecorder
	deadlineSet bool
	deadline    time.Time
}

func (d *deadlineRecorder) SetWriteDeadline(t time.Time) error {
	d.deadlineSet = true
	d.deadline = t
	return nil
}

func Test_SetStreamWriteDeadline(t *testing.T) {
	t.Parallel()

	t.Run("sets deadline when writer supports it", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		req := httptest.NewRequest("GET", "/test", http.NoBody)
		writer := &deadlineRecorder{ResponseRecorder: httptest.NewRecorder()}
		ctx := e.NewContext(req, httptest.NewRecorder())
		ctx.Response().Writer = writer

		before := time.Now()
		SetStreamWriteDeadline(ctx)

		assert.True(t, writer.deadlineSet, "SetStreamWriteDeadline must set a write deadline when the writer supports it")
		assert.WithinDuration(t, before.Add(SSEWriteDeadline), writer.deadline, time.Second,
			"deadline should be roughly SSEWriteDeadline in the future")
	})

	t.Run("no-op when writer does not support deadlines", func(t *testing.T) {
		t.Parallel()
		e := echo.New()
		req := httptest.NewRequest("GET", "/test", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)

		// A plain recorder does not support write deadlines, so NewResponseController
		// returns http.ErrNotSupported; this must be handled without panicking.
		assert.NotPanics(t, func() { SetStreamWriteDeadline(ctx) })
	})
}
