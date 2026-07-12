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

	t.Run("skips precompressed route", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/api/v2/species/dictionary/fi")
		setPathForContext(t, c, "/api/v2/species/dictionary/:locale")
		assert.True(t, DefaultGzipSkipper(c),
			"the species dictionary route serves precompressed bytes and must not be re-gzipped")
	})
}

func TestDefaultGzipSkipper_SkipsStreamingJSONRoutes(t *testing.T) {
	t.Parallel()

	// Hardcoded route templates (NOT derived from streamingJSONRoutes) so that
	// dropping or renaming an entry in the production map fails this test instead
	// of silently reducing coverage. These must equal the templates the
	// integrations handler registers (RegisterRoutes under the /api/v2 group).
	streamingRoutes := []string{
		"/api/v2/integrations/mqtt/test",
		"/api/v2/integrations/birdweather/test",
		"/api/v2/integrations/weather/test",
		"/api/v2/integrations/ebird/test",
	}
	for _, route := range streamingRoutes {
		t.Run("skips "+route, func(t *testing.T) {
			t.Parallel()
			// Fetch-based POSTs whose Accept is not text/event-stream, so SSESkipper
			// does not cover them; the route-template skip must, otherwise gzip
			// buffering would withhold the flushed progress results.
			c, _ := newTestContext(t, http.MethodPost, "/irrelevant")
			setPathForContext(t, c, route)
			assert.True(t, DefaultGzipSkipper(c),
				"streaming route %q must skip gzip so flushed results reach the client immediately", route)
		})
	}

	// Negative: a non-streaming sibling endpoint must still be compressed.
	t.Run("does not skip non-streaming integration route", func(t *testing.T) {
		t.Parallel()
		c, _ := newTestContext(t, http.MethodGet, "/irrelevant")
		setPathForContext(t, c, "/api/v2/integrations/mqtt/status")
		assert.False(t, DefaultGzipSkipper(c),
			"the MQTT status route returns a normal JSON body and must still be gzip-compressed")
	})

	// Guard against the production map drifting from the hardcoded set: every known
	// route must be present, and the map must contain nothing unexpected.
	known := make(map[string]struct{}, len(streamingRoutes))
	for _, r := range streamingRoutes {
		known[r] = struct{}{}
		if _, ok := streamingJSONRoutes[r]; !ok {
			t.Errorf("streamingJSONRoutes is missing expected streaming route %q", r)
		}
	}
	for r := range streamingJSONRoutes {
		if _, ok := known[r]; !ok {
			t.Errorf("streamingJSONRoutes has unexpected route %q not asserted by this test", r)
		}
	}
}

// TestNewGzip_EndToEnd_StreamingJSONRouteNotCompressed drives the real gzip
// middleware stack against a registered streaming route template and verifies the
// response is not gzip-wrapped even when the client advertises Accept-Encoding:
// gzip. This complements the skipper unit test by exercising c.Path() through the
// actual Echo router, so a mismatch between the skip-set template and the
// registered route would surface here.
func TestNewGzip_EndToEnd_StreamingJSONRouteNotCompressed(t *testing.T) {
	t.Parallel()

	e := echo.New()
	e.Use(NewGzip())
	// Register the real streaming route template so c.Path() matches the skip set.
	e.POST("/api/v2/integrations/mqtt/test", func(c echo.Context) error {
		return c.String(http.StatusOK, strings.Repeat("PROGRESS_JSON_LINE", 2048))
	})

	req := httptest.NewRequest(http.MethodPost, "/api/v2/integrations/mqtt/test", http.NoBody)
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentEncoding),
		"streaming test route must not be gzip-compressed even when the client asks for it")
	assert.False(t, bytes.HasPrefix(rec.Body.Bytes(), []byte{0x1f, 0x8b}),
		"streaming test route body must not be a gzip stream")
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

// TestNewGzip_EndToEnd_PrecompressedRouteNotDoubleCompressed guards the species
// dictionary endpoint: it serves bytes the handler already gzip-compressed and sets
// Content-Encoding: gzip itself. Echo's gzip middleware does not check for an existing
// Content-Encoding, so without the precompressedRoutes skip it would compress the body
// a second time, and a browser decompressing once would get gzip bytes instead of JSON.
// This drives the production gzip stack and asserts the body decompresses in ONE pass.
func TestNewGzip_EndToEnd_PrecompressedRouteNotDoubleCompressed(t *testing.T) {
	t.Parallel()

	payload := []byte(`{"Barbastella barbastellus":"mopsilepakko"}`)
	var gzBuf bytes.Buffer
	zw := gzip.NewWriter(&gzBuf)
	_, err := zw.Write(payload)
	require.NoError(t, err)
	require.NoError(t, zw.Close())
	precompressed := gzBuf.Bytes()

	e := echo.New()
	e.Use(NewGzip())
	e.GET("/api/v2/species/dictionary/:locale", func(c echo.Context) error {
		c.Response().Header().Set(echo.HeaderContentEncoding, "gzip")
		return c.Blob(http.StatusOK, "application/json", precompressed)
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/species/dictionary/fi", http.NoBody)
	req.Header.Set(echo.HeaderAcceptEncoding, "gzip")
	rec := httptest.NewRecorder()
	e.ServeHTTP(rec, req)

	require.Equal(t, http.StatusOK, rec.Code)
	assert.Equal(t, "gzip", rec.Header().Get(echo.HeaderContentEncoding))

	// A single gzip pass: decompressing the body once must yield the JSON payload.
	// If the middleware had re-compressed it, one pass would yield gzip bytes instead.
	zr, err := gzip.NewReader(bytes.NewReader(rec.Body.Bytes()))
	require.NoError(t, err)
	t.Cleanup(func() { _ = zr.Close() })
	got, err := io.ReadAll(zr)
	require.NoError(t, err)
	assert.Equal(t, payload, got, "dictionary body must decompress in a single gzip pass")
}
