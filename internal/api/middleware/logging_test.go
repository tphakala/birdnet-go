package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

func TestNewRequestLogger_ScrubsHLSToken(t *testing.T) {
	buf := logtest.CaptureBuffer(t)

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

func TestNewRequestLogger_ScrubsURI(t *testing.T) {
	tests := []struct {
		name        string
		requestURI  string
		contains    []string
		notContains []string
	}{
		{
			name:        "percent-encoded query token is redacted after decoding",
			requestURI:  "/api/v2/media/audio?token=ab%2Bcd1234567890",
			contains:    []string{"/api/v2/media/audio", "[TOKEN]"},
			notContains: []string{"ab%2Bcd1234567890", "ab+cd1234567890", "1234567890"},
		},
		{
			name:        "plain media query token is redacted",
			requestURI:  "/api/v2/media/audio?token=SECRET_QUERY_TOKEN",
			contains:    []string{"/api/v2/media/audio", "[TOKEN]"},
			notContains: []string{"SECRET_QUERY_TOKEN"},
		},
		{
			name:        "ordinary request without secrets is preserved",
			requestURI:  "/api/v2/detections?limit=10&numResults=25",
			contains:    []string{"/api/v2/detections", "limit=10", "numResults=25"},
			notContains: []string{"[TOKEN]"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			buf := logtest.CaptureBuffer(t)

			e := echo.New()
			mw := NewRequestLogger()

			req := httptest.NewRequest(http.MethodGet, tt.requestURI, http.NoBody)
			req.RequestURI = tt.requestURI
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			handler := mw(func(c echo.Context) error {
				return c.String(http.StatusOK, "ok")
			})
			require.NoError(t, handler(c))

			logOutput := buf.String()
			for _, expected := range tt.contains {
				assert.Contains(t, logOutput, expected)
			}
			for _, unexpected := range tt.notContains {
				assert.NotContainsf(t, logOutput, unexpected, "secret %q must be scrubbed from the access log", unexpected)
			}
		})
	}
}
