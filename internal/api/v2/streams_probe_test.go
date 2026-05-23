package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestValidateProbeURL(t *testing.T) {
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
		{"empty URL", "", true, http.StatusBadRequest},
		{"no scheme", "192.168.1.1/stream", true, http.StatusBadRequest},
		{"unknown scheme", "ftp://host/file", true, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := validateProbeURL(tt.url)
			if !tt.wantErr {
				assert.NoError(t, err)
				return
			}
			require.Error(t, err)
			var he *echo.HTTPError
			require.ErrorAs(t, err, &he)
			assert.Equal(t, tt.status, he.Code)
		})
	}
}

func TestProbeStreamHandler_ValidationErrors(t *testing.T) {
	t.Parallel()

	e := echo.New()

	tests := []struct {
		name   string
		body   string
		status int
	}{
		{"empty url", `{"url":""}`, http.StatusBadRequest},
		{"file scheme", `{"url":"file:///etc/passwd"}`, http.StatusBadRequest},
		{"metadata IP", `{"url":"rtsp://169.254.169.254/"}`, http.StatusForbidden},
		{"unknown scheme", `{"url":"ftp://host/file"}`, http.StatusBadRequest},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v2/streams/probe",
				strings.NewReader(tt.body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			ctrl := &Controller{}
			err := ctrl.ProbeStream(ctx)
			require.Error(t, err)
			var he *echo.HTTPError
			require.ErrorAs(t, err, &he)
			assert.Equal(t, tt.status, he.Code)
		})
	}
}
