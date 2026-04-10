package middleware

import (
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Compression configuration constants.
const (
	// DefaultGzipLevel is the default compression level (1-9, higher = more compression).
	DefaultGzipLevel = 5

	// headerRange is the canonical HTTP Range request header name. Echo does
	// not export a constant for it, so we define one locally to avoid a
	// magic string.
	headerRange = "Range"
)

// binaryMediaRoutes is the set of Echo route templates whose responses are
// binary media (audio, images) and must never be gzip-compressed.
//
// Compressing media responses is always wrong:
//
//  1. HTTP/1.1 applies Range requests after Content-Encoding, so ranged
//     reads through http.ServeContent return slices of compressed bytes
//     that browsers cannot reassemble. This alone breaks audio seeking.
//  2. Chromium has rejected gzipped <audio>/<video> responses with
//     ERR_CONTENT_DECODING_FAILED since crbug 47381 (2010). Firefox strips
//     Accept-Encoding from media requests entirely (bz 614760). Only Safari
//     is lenient, and intermediate proxies can still re-pack the doubly
//     encoded response and make intermittent failures permanent.
//
// Lookup uses echo.Context.Path(), which is set by the router to the matched
// route template (e.g. "/api/v2/audio/:id") before middleware runs. Matching
// on the template rather than the raw URL prevents accidental matches on
// JSON endpoints that share a prefix.
var binaryMediaRoutes = map[string]struct{}{
	"/api/v2/audio/:id":                         {},
	"/api/v2/audio/:id/clip":                    {},
	"/api/v2/audio/:id/process":                 {},
	"/api/v2/media/audio/:filename":             {},
	"/api/v2/media/audio":                       {},
	"/api/v2/spectrogram/:id":                   {},
	"/api/v2/spectrogram/:id/process":           {},
	"/api/v2/media/spectrogram/:filename":       {},
	"/api/v2/media/species-image":               {},
	"/api/v2/media/image/:scientific_name":      {},
	"/api/v2/media/bird-image/:scientific_name": {},
}

// hlsContentRoutePrefix matches token-based HLS playlist and segment routes.
// The registered route template is /api/v2/streams/hls/t/:streamToken/* where
// the trailing * is an Echo wildcard. We cannot lookup wildcard templates in
// binaryMediaRoutes verbatim, so we match them with a prefix check instead.
//
// HLS segments are binary MPEG-TS and players range-request them, so they
// suffer the same Range+gzip incompatibility as audio files.
const hlsContentRoutePrefix = "/api/v2/streams/hls/t/"

// NewGzip creates a gzip compression middleware with the default compression level.
// It automatically skips compression for SSE, binary media, and ranged requests.
func NewGzip() echo.MiddlewareFunc {
	return NewGzipWithLevel(DefaultGzipLevel)
}

// NewGzipWithLevel creates a gzip compression middleware with a custom compression level.
// It automatically skips compression for SSE, binary media, and ranged requests.
func NewGzipWithLevel(level int) echo.MiddlewareFunc {
	return middleware.GzipWithConfig(middleware.GzipConfig{
		Level:   level,
		Skipper: DefaultGzipSkipper,
	})
}

// NewGzipWithSkipper creates a gzip compression middleware with a custom skipper function.
func NewGzipWithSkipper(level int, skipper middleware.Skipper) echo.MiddlewareFunc {
	return middleware.GzipWithConfig(middleware.GzipConfig{
		Level:   level,
		Skipper: skipper,
	})
}

// DefaultGzipSkipper is the default skipper used by NewGzip and NewGzipWithLevel.
// It skips compression for responses that must not be gzipped:
//   - Server-Sent Events endpoints (see SSESkipper).
//   - Binary media routes serving audio, images, or HLS content (see MediaPathSkipper).
//   - Any request carrying a Range header, since gzip is incompatible with
//     byte-range responses served via http.ServeContent.
func DefaultGzipSkipper(c echo.Context) bool {
	if SSESkipper(c) {
		return true
	}
	if MediaPathSkipper(c) {
		return true
	}
	return c.Request().Header.Get(headerRange) != ""
}

// SSESkipper is a skipper function that skips compression for Server-Sent Events endpoints.
func SSESkipper(c echo.Context) bool {
	return c.Request().Header.Get("Accept") == "text/event-stream"
}

// MediaPathSkipper returns true when the matched Echo route serves binary
// media (audio, images, HLS segments) that must not be gzip-compressed.
//
// It uses echo.Context.Path(), which is the registered route template set
// by the router before middleware runs, rather than the raw request URI.
// Matching on the template prevents a JSON endpoint whose URL happens to
// contain a media substring from being skipped accidentally.
func MediaPathSkipper(c echo.Context) bool {
	p := c.Path()
	if _, ok := binaryMediaRoutes[p]; ok {
		return true
	}
	return strings.HasPrefix(p, hlsContentRoutePrefix)
}
