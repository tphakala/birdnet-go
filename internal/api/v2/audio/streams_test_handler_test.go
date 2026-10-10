package audio

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
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
			vErr := validateStreamTestURL(t.Context(), tt.url, stubLookup("192.168.1.50"))
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

			ctrl := &Handler{Core: &apicore.Core{}, lookupStreamHost: stubLookup("192.168.1.50")}
			ctrl.Settings.Store(&conf.Settings{})
			err := ctrl.TestStream(ctx)
			require.NoError(t, err)
			assert.Equal(t, tt.status, rec.Code)

			var resp apicore.ErrorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, tt.errorKey, resp.ErrorKey)
		})
	}
}

func TestTestStreamHandler_NoAudioStreamError(t *testing.T) {
	t.Parallel()

	e := echo.New()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/streams/test",
		strings.NewReader(`{"url":"rtsp://example.test/stream"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	ctrl := &Handler{Core: &apicore.Core{}, lookupStreamHost: stubLookup("192.168.1.50"), probeStreamInfo: func(_ context.Context, _ string) (*ffmpeg.StreamInfo, error) {
		return nil, ffmpeg.ErrNoAudioStreamsFound
	}}
	ctrl.Settings.Store(&conf.Settings{})
	err := ctrl.TestStream(ctx)
	require.NoError(t, err)

	assert.Equal(t, http.StatusUnprocessableEntity, rec.Code)

	var resp apicore.ErrorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "errors.streams.test.noAudioTrack", resp.ErrorKey)
	assert.Equal(t, "stream has no audio track", resp.Message)
}

// stubLookup returns a resolver that answers every host with the given
// addresses, so tests never touch real DNS.
func stubLookup(addrs ...string) lookupHostFunc {
	return func(context.Context, string) ([]netip.Addr, error) {
		out := make([]netip.Addr, 0, len(addrs))
		for _, a := range addrs {
			out = append(out, netip.MustParseAddr(a))
		}
		return out, nil
	}
}

func TestValidateStreamTestURL_NonCanonicalHosts(t *testing.T) {
	t.Parallel()

	hostMap := func(m map[string][]string) lookupHostFunc {
		return func(_ context.Context, host string) ([]netip.Addr, error) {
			raw, ok := m[host]
			if !ok {
				return nil, errors.NewStd("no such host")
			}
			out := make([]netip.Addr, 0, len(raw))
			for _, a := range raw {
				out = append(out, netip.MustParseAddr(a))
			}
			return out, nil
		}
	}
	lookup := hostMap(map[string][]string{
		"cam.example":     {"127.0.0.1"},
		"mixed.example":   {"192.168.1.10", "127.0.0.1"},
		"v6loop.example":  {"::ffff:127.0.0.1"},
		"cam.local":       {"192.168.1.10"},
		"ula.local":       {"fd12:3456:789a::1"},
		"meta.example":    {"169.254.169.254"},
		"alibaba.example": {"100.100.100.200"},
		"cafe1.local":     {"192.168.1.20"},
	})

	tests := []struct {
		name    string
		url     string
		blocked bool
	}{
		{"decimal integer", "http://2130706433/", true},
		{"hex integer", "http://0x7f000001/", true},
		{"octal dotted", "http://0177.0.0.1/", true},
		{"short dotted", "http://127.1/", true},
		{"zero", "http://0/", true},
		{"decimal metadata", "http://2852039166/", true},
		{"rtsp decimal integer", "rtsp://2130706433:554/stream", true},
		{"localhost trailing dot", "http://localhost./", true},
		{"localhost upper trailing dot", "http://LOCALHOST./", true},
		{"ip6-localhost", "http://ip6-localhost/", true},
		{"localhost subdomain", "http://cam.localhost/", true},
		{"metadata hostname trailing dot", "http://metadata.google.internal./", true},
		{"ipv4-mapped loopback", "http://[::ffff:127.0.0.1]/", true},
		{"ipv4-compatible loopback", "http://[::127.0.0.1]/", true},
		{"alibaba metadata", "http://100.100.100.200/", true},
		{"aws imds ipv6", "http://[fd00:ec2::254]/", true},
		{"name resolving to loopback", "http://cam.example/", true},
		{"name resolving to loopback and LAN", "rtsp://mixed.example/", true},
		{"name resolving to mapped loopback", "http://v6loop.example/", true},
		{"name resolving to metadata", "http://meta.example/", true},
		{"name resolving to alibaba metadata", "http://alibaba.example/", true},
		{"lan name allowed", "rtsp://cam.local/stream", false},
		{"ula name allowed", "rtsp://ula.local/stream", false},
		{"rfc1918 192.168 allowed", "rtsp://192.168.1.10/stream", false},
		{"rfc1918 10 allowed", "rtsp://10.0.0.5/stream", false},
		{"ula literal allowed", "rtsp://[fd12:3456:789a::1]/stream", false},
		{"hex-looking hostname allowed", "http://cafe1.local/", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			vErr := validateStreamTestURL(t.Context(), tt.url, lookup)
			if !tt.blocked {
				assert.Nil(t, vErr)
				return
			}
			require.NotNil(t, vErr)
			assert.Equal(t, http.StatusForbidden, vErr.status)
			assert.Equal(t, "errors.streams.test.blockedDestination", vErr.errorKey)
		})
	}

	t.Run("resolution failure is a connection failure, not blocked", func(t *testing.T) {
		t.Parallel()
		vErr := validateStreamTestURL(t.Context(), "http://nxdomain.example/", lookup)
		require.NotNil(t, vErr)
		assert.Equal(t, http.StatusBadGateway, vErr.status)
		assert.Equal(t, "errors.streams.test.connectionFailed", vErr.errorKey)
	})

	t.Run("literal hosts are not resolved", func(t *testing.T) {
		t.Parallel()
		failing := func(context.Context, string) ([]netip.Addr, error) {
			return nil, errors.NewStd("resolver must not be called")
		}
		assert.Nil(t, validateStreamTestURL(t.Context(), "rtsp://192.168.1.10/s", failing))
		assert.NotNil(t, validateStreamTestURL(t.Context(), "http://2130706433/", failing))
	})
}

func TestTestStreamHandler_BlockedHostSkipsProbe(t *testing.T) {
	t.Parallel()

	e := echo.New()
	urls := []string{
		`{"url":"http://2130706433/"}`,
		`{"url":"http://0x7f000001/"}`,
		`{"url":"http://cam.example/"}`,
		`{"url":"http://localhost./"}`,
	}
	for _, body := range urls {
		t.Run(body, func(t *testing.T) {
			t.Parallel()
			req := httptest.NewRequest(http.MethodPost, "/api/v2/streams/test", strings.NewReader(body))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)

			probed := false
			ctrl := &Handler{
				Core:             &apicore.Core{},
				lookupStreamHost: stubLookup("127.0.0.1"),
				probeStreamInfo: func(context.Context, string) (*ffmpeg.StreamInfo, error) {
					probed = true
					return &ffmpeg.StreamInfo{}, nil
				},
			}
			ctrl.Settings.Store(&conf.Settings{})
			require.NoError(t, ctrl.TestStream(ctx))

			assert.False(t, probed, "probe must not run for a blocked host")
			assert.Equal(t, http.StatusForbidden, rec.Code)
			var resp apicore.ErrorResponse
			require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
			assert.Equal(t, "errors.streams.test.blockedDestination", resp.ErrorKey)
		})
	}
}

func TestIsNumericHostForm(t *testing.T) {
	t.Parallel()

	tests := []struct {
		host string
		want bool
	}{
		{"2130706433", true},
		{"0x7f000001", true},
		{"0X7F.0.0.1", true},
		{"0177.0.0.1", true},
		{"127.1", true},
		{"", false},
		{"1..2", false},
		{"0xzz", false},
		{"0x", false},
		{"cafe1.local", false},
		{"cam1", false},
		{"1.2.cam", false},
	}
	for _, tt := range tests {
		t.Run(tt.host, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isNumericHostForm(tt.host))
		})
	}
}

func TestValidateStreamTestURL_EmptyHost(t *testing.T) {
	t.Parallel()
	vErr := validateStreamTestURL(t.Context(), "rtsp:///stream", stubLookup("192.168.1.50"))
	require.NotNil(t, vErr)
	assert.Equal(t, http.StatusBadRequest, vErr.status)
	assert.Equal(t, "errors.streams.test.invalidUrl", vErr.errorKey)
}

func TestAnalyzeChannelsHandler_BlockedHost(t *testing.T) {
	t.Parallel()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/streams/analyze-channels",
		strings.NewReader(`{"url":"http://2130706433/"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)

	ctrl := &Handler{Core: &apicore.Core{}, lookupStreamHost: stubLookup("192.168.1.50")}
	ctrl.Settings.Store(&conf.Settings{})
	require.NoError(t, ctrl.AnalyzeChannels(ctx))

	assert.Equal(t, http.StatusForbidden, rec.Code)
	var resp apicore.ErrorResponse
	require.NoError(t, json.NewDecoder(rec.Body).Decode(&resp))
	assert.Equal(t, "errors.streams.test.blockedDestination", resp.ErrorKey)
}

// TestValidateStreamTestURL_ResolvesHostAsWritten pins that the resolver gets
// the host the probe will dial, trailing dot included, so a search domain
// cannot make the check resolve a different name, and that a resolver failure
// is kept as the cause.
func TestValidateStreamTestURL_ResolvesHostAsWritten(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		wantHost string
	}{
		{name: "absolute name keeps its trailing dot", url: "rtsp://cam.example.:554/live", wantHost: "cam.example."},
		{name: "relative name is passed unchanged", url: "rtsp://cam.example:554/live", wantHost: "cam.example"},
		{name: "host is lowercased", url: "rtsp://Cam.Example.:554/live", wantHost: "cam.example."},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var got string
			lookup := func(_ context.Context, host string) ([]netip.Addr, error) {
				got = host
				return []netip.Addr{netip.MustParseAddr("192.168.1.10")}, nil
			}
			assert.Nil(t, validateStreamTestURL(t.Context(), tt.url, lookup))
			assert.Equal(t, tt.wantHost, got)
		})
	}

	t.Run("resolver failure is kept as the cause", func(t *testing.T) {
		t.Parallel()
		resolveErr := errors.NewStd("no such host")
		lookup := func(context.Context, string) ([]netip.Addr, error) { return nil, resolveErr }
		vErr := validateStreamTestURL(t.Context(), "rtsp://cam.example:554/live", lookup)
		require.NotNil(t, vErr)
		assert.Equal(t, http.StatusBadGateway, vErr.status)
		assert.ErrorIs(t, vErr.err, resolveErr)
	})
}
