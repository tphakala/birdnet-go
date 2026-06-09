package api

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestValidateStreamTestURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		url     string
		wantErr bool
		status  int
	}{
		{"valid rtsp", "rtsp://192.168.1.1/stream", false, 0},
		{"valid rtsps", "rtsps://host/stream", false, 0},
		{"valid http", "http://host/stream.m3u8", false, 0},
		{"valid https", "https://host/stream.m3u8", false, 0},
		{"valid rtmp", "rtmp://host/live/stream", false, 0},
		{"valid udp", "udp://224.1.1.1:1234", false, 0},
		{"private IP allowed", "rtsp://192.168.1.100/stream", false, 0},
		{"private IP 10.x allowed", "rtsp://10.0.0.1/stream", false, 0},
		{"blocked file scheme", "file:///etc/passwd", true, http.StatusBadRequest},
		{"blocked metadata IPv4", "rtsp://169.254.169.254/", true, http.StatusForbidden},
		{"blocked metadata hostname", "http://metadata.google.internal/", true, http.StatusForbidden},
		{"blocked link-local IP", "rtsp://169.254.1.1/stream", true, http.StatusForbidden},
		{"blocked localhost", "rtsp://localhost/stream", true, http.StatusForbidden},
		{"blocked loopback IPv4", "rtsp://127.0.0.1/stream", true, http.StatusForbidden},
		{"blocked loopback IPv6", "rtsp://[::1]/stream", true, http.StatusForbidden},
		{"blocked unspecified IPv4", "rtsp://0.0.0.0/stream", true, http.StatusForbidden},
		{"empty URL", "", true, http.StatusBadRequest},
		{"no scheme", "192.168.1.1/stream", true, http.StatusBadRequest},
		{"unknown scheme", "ftp://host/file", true, http.StatusBadRequest},
		{"typo rstp scheme", "rstp://192.168.1.1/stream", true, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vErr := validateStreamTestURL(tt.url)
			if !tt.wantErr {
				assert.Nil(t, vErr)
				return
			}
			require.NotNil(t, vErr)
			assert.Equal(t, tt.status, vErr.status)
			assert.NotEmpty(t, vErr.errorKey)
		})
	}
}

func TestTestStreamHandler_ValidationErrors(t *testing.T) {
	t.Parallel()

	e := echo.New()

	tests := []struct {
		name     string
		body     string
		status   int
		errorKey string
	}{
		{"malformed json", `{`, http.StatusBadRequest, "errors.streams.test.invalidBody"},
		{"empty url", `{"url":""}`, http.StatusBadRequest, "errors.streams.test.urlRequired"},
		{"file scheme", `{"url":"file:///etc/passwd"}`, http.StatusBadRequest, "errors.streams.test.unsupportedScheme"},
		{"metadata IP", `{"url":"rtsp://169.254.169.254/"}`, http.StatusForbidden, "errors.streams.test.blockedDestination"},
		{"unknown scheme", `{"url":"ftp://host/file"}`, http.StatusBadRequest, "errors.streams.test.unsupportedScheme"},
		{"typo rstp", `{"url":"rstp://192.168.1.1/stream"}`, http.StatusBadRequest, "errors.streams.test.unsupportedScheme"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v2/streams/test",
				strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			ctrl := &Controller{}
			ctrl.Settings.Store(&conf.Settings{})
			err := ctrl.TestStream(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.status, rec.Code)

			var resp ErrorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, tt.errorKey, resp.ErrorKey)
		})
	}
}

func TestTestStreamHandler_NoAudioStreamError(t *testing.T) {
	e := echo.New()

	originalProbeStreamInfo := probeStreamInfo
	probeStreamInfo = func(_ context.Context, _ string) (*ffmpeg.StreamInfo, error) {
		return nil, ffmpeg.ErrNoAudioStreamsFound
	}
	t.Cleanup(func() {
		probeStreamInfo = originalProbeStreamInfo
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/streams/test",
		strings.NewReader(`{"url":"rtsp://example.test/stream"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	ctrl := &Controller{}
	ctrl.Settings.Store(&conf.Settings{})
	err := ctrl.TestStream(ctx)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var resp ErrorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "errors.streams.test.connectionFailed", resp.ErrorKey)
	assert.Equal(t, "stream has no audio track", resp.Message)
}
