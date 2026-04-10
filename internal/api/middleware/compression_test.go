package middleware

import (
	"bytes"
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dummyTextPayload is large enough to exceed any gzip MinLength buffering
// threshold so the Echo gzip middleware actually emits Content-Encoding: gzip
// when it decides to compress a response. The content is plain text rather
// than JSON-shaped to keep testifylint from suggesting assert.JSONEq on the
// decoded-body equality check below.
var dummyTextPayload = strings.Repeat("value-ok-repeated-payload ", 200)

// setPathForContext manually sets echo.Context.Path for skipper unit tests
// that do not go through the router. Echo exposes SetPath() on its Context
// interface, which is exactly what the router itself calls after Find().
func setPathForContext(t *testing.T, c echo.Context, routeTemplate string) {
	t.Helper()
	c.SetPath(routeTemplate)
}

func TestSSESkipper(t *testing.T) {
	t.Parallel()

	t.Run("skips text/event-stream", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/anything")
		c.Request().Header.Set("Accept", "text/event-stream")
		assert.True(t, SSESkipper(c))
	})

	t.Run("does not skip JSON accept", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/anything")
		c.Request().Header.Set("Accept", "application/json")
		assert.False(t, SSESkipper(c))
	})

	t.Run("does not skip missing accept", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/anything")
		assert.False(t, SSESkipper(c))
	})
}

func TestMediaPathSkipper_SkipsBinaryRoutes(t *testing.T) {
	t.Parallel()

	for route := range binaryMediaRoutes {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestContext(t, http.MethodGet, "/irrelevant")
			setPathForContext(t, c, route)
			assert.True(t, MediaPathSkipper(c),
				"expected route %q to be skipped", route)
		})
	}
}

func TestMediaPathSkipper_SkipsHLSContentWildcard(t *testing.T) {
	t.Parallel()

	// The actual wildcard template Echo stores when /api/v2/streams/hls/t/:streamToken/*
	// is registered. Prefix match on c.Path() must return true.
	wildcardTemplate := "/api/v2/streams/hls/t/:streamToken/*"
	c, _ := newTestContext(t, http.MethodGet, "/irrelevant")
	setPathForContext(t, c, wildcardTemplate)
	assert.True(t, MediaPathSkipper(c),
		"expected HLS wildcard template to be skipped")

	// Also the bare prefix with a token and segment, as Echo's c.Path()
	// always returns the template, this guards against a future regression
	// if someone accidentally switches the implementation to use the raw URI.
	literalPath := "/api/v2/streams/hls/t/abc123/segment5.ts"
	c2, _ := newTestContext(t, http.MethodGet, "/irrelevant")
	setPathForContext(t, c2, literalPath)
	assert.True(t, MediaPathSkipper(c2),
		"expected literal HLS path to be skipped via prefix")
}

func TestMediaPathSkipper_DoesNotSkipJSONRoutes(t *testing.T) {
	t.Parallel()

	// These are real API routes on adjacent templates that return JSON and
	// must continue to be compressed. If any of these start getting skipped,
	// clients will see bloated payloads.
	jsonRoutes := []string{
		"/api/v2/spectrogram/:id/status",
		"/api/v2/spectrogram/:id/generate",
		"/api/v2/media/species-image/info",
		"/api/v2/detections",
		"/api/v2/health",
		"/api/v2/streams/audio-level",
		"",
	}
	for _, route := range jsonRoutes {
		t.Run(route, func(t *testing.T) {
			t.Parallel()
			c, _ := newTestContext(t, http.MethodGet, "/irrelevant")
			setPathForContext(t, c, route)
			assert.False(t, MediaPathSkipper(c),
				"expected route %q NOT to be skipped", route)
		})
	}
}

func TestDefaultGzipSkipper(t *testing.T) {
	t.Parallel()

	t.Run("skips SSE", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/api/v2/streams/audio-level")
		c.Request().Header.Set("Accept", "text/event-stream")
		setPathForContext(t, c, "/api/v2/streams/audio-level")
		assert.True(t, DefaultGzipSkipper(c))
	})

	t.Run("skips binary media route", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/api/v2/audio/42")
		setPathForContext(t, c, "/api/v2/audio/:id")
		assert.True(t, DefaultGzipSkipper(c))
	})

	t.Run("skips range request on compressible route", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/api/v2/detections")
		setPathForContext(t, c, "/api/v2/detections")
		c.Request().Header.Set(headerRange, "bytes=0-1023")
		assert.True(t, DefaultGzipSkipper(c))
	})

	t.Run("does not skip plain JSON route", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/api/v2/detections")
		setPathForContext(t, c, "/api/v2/detections")
		assert.False(t, DefaultGzipSkipper(c))
	})
}

// TestNewGzip_EndToEnd_AudioRouteNotCompressed is a regression test for
// tphakala/birdnet-go#2709. It drives a real Echo router with the gzip
// middleware stack used in production and verifies that a matched audio
// route is not wrapped in a gzip writer even when the client advertises
// Accept-Encoding: gzip.
func TestNewGzip_EndToEnd_AudioRouteNotCompressed(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.Use(NewGzip())
	// Large payload to ensure the middleware would otherwise compress.
	e.GET("/api/v2/audio/:id", func(c echo.Context) error {
		return c.String(http.StatusOK, strings.Repeat("RAW_WAV_BYTES", 2048))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/12345", http.NoBody)
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding),
		"audio route must not be gzip-compressed even when client asks for it")
	// Sanity check: the raw body is not a gzip stream.
	assert.False(t, bytes.HasPrefix(rec.Body.Bytes(), []byte{0x1f, 0x8b}),
		"response body must not start with gzip magic bytes")
}

// TestNewGzip_EndToEnd_HLSContentNotCompressed verifies the HLS wildcard
// route is also excluded.
func TestNewGzip_EndToEnd_HLSContentNotCompressed(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.Use(NewGzip())
	e.GET("/api/v2/streams/hls/t/:streamToken/*", func(c echo.Context) error {
		return c.String(http.StatusOK, strings.Repeat("TS_SEGMENT_BYTES", 2048))
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/hls/t/tok/segment0.ts", http.NoBody)
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding),
		"HLS segment route must not be gzip-compressed")
}

// TestNewGzip_EndToEnd_JSONRouteStillCompressed is the negative regression:
// the fix must not accidentally disable gzip for JSON endpoints.
func TestNewGzip_EndToEnd_JSONRouteStillCompressed(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.Use(NewGzip())
	e.GET("/api/v2/detections", func(c echo.Context) error {
		return c.String(http.StatusOK, dummyTextPayload)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections", http.NoBody)
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gzip", rec.Header().Get(echo.HeaderContentEncoding),
		"JSON route should still be gzip-compressed")

	// Verify the body decompresses to the original payload.
	gzr, err := gzip.NewReader(rec.Body)
	require.NoError(t, err)
	defer func() {
		assert.NoError(t, gzr.Close())
	}()
	decoded, err := io.ReadAll(gzr)
	require.NoError(t, err)
	assert.Equal(t, dummyTextPayload, string(decoded))
}

// TestNewGzip_EndToEnd_RangeRequestNotCompressed verifies that any request
// with a Range header is served without gzip, so http.ServeContent-style
// range handling works unmodified.
func TestNewGzip_EndToEnd_RangeRequestNotCompressed(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.Use(NewGzip())
	e.GET("/api/v2/detections", func(c echo.Context) error {
		return c.String(http.StatusOK, dummyTextPayload)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections", http.NoBody)
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	req.Header.Set(headerRange, "bytes=0-1023")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding),
		"ranged request must not be gzip-compressed")
}

// TestNewGzip_EndToEnd_SSEStillNotCompressed is a preservation test for the
// original SSESkipper behavior.
func TestNewGzip_EndToEnd_SSEStillNotCompressed(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.Use(NewGzip())
	e.GET("/api/v2/streams/audio-level", func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentType, "text/event-stream")
		return c.String(http.StatusOK, "data: {}\n\n")
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/audio-level", http.NoBody)
	req.Header.Set("Accept", "text/event-stream")
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding),
		"SSE endpoint must not be gzip-compressed")
}
