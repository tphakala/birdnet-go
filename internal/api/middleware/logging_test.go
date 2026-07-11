package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger"
)

func captureAccessLogs(t *testing.T) *bytes.Buffer {
	t.Helper()

	var buf bytes.Buffer
	capture := slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	cl, err := logger.NewCentralLogger(
		&logger.LoggingConfig{
			Console:      &logger.ConsoleOutput{Enabled: false},
			FileOutput:   &logger.FileOutput{Enabled: false},
			DefaultLevel: "debug",
		},
		capture,
	)
	require.NoError(t, err)
	t.Cleanup(func() { _ = cl.Close() })

	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() { logger.SetGlobal(prev) })

	return &buf
}

func TestNewRequestLogger_ScrubsHLSToken(t *testing.T) {
	buf := captureAccessLogs(t)

	e := echo.New()
	mw := NewRequestLogger()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/hls/t/SECRET-TOKEN/playlist.m3u8?token=SECRET_QUERY_TOKEN", http.NoBody)
	// Echo middleware uses RequestURI directly from request
	req.RequestURI = "/api/v2/streams/hls/t/SECRET-TOKEN/playlist.m3u8?token=SECRET_QUERY_TOKEN"
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	handler := mw(func(c echo.Context) error {
		return c.String(http.StatusOK, "ok")
	})

	err := handler(c)
	require.NoError(t, err)

	logOutput := buf.String()
	// Check that the token in the path segment was replaced
	assert.Contains(t, logOutput, "/api/v2/streams/hls/t/:streamToken/playlist.m3u8")
	// Check that neither token was logged
	assert.NotContains(t, logOutput, "SECRET-TOKEN")
	assert.NotContains(t, logOutput, "SECRET_QUERY_TOKEN")
}
