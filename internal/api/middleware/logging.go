// Package middleware provides HTTP middleware components for the BirdNET-Go server.
package middleware

import (
	"regexp"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

// hlsTokenPathPrefix is the path segment that precedes an HLS stream token, e.g.
// /api/v2/streams/hls/t/<token>/playlist.m3u8. It is used both as a cheap presence
// check and to build the token-collapsing regex.
const hlsTokenPathPrefix = "/streams/hls/t/"

var hlsTokenPathRegex = regexp.MustCompile(`(` + regexp.QuoteMeta(hlsTokenPathPrefix) + `)[^/?]+`)

// scrubURIForLog redacts secrets from a raw request URI before it is written to the
// access log. Media and HLS endpoints carry access tokens in the query string
// (?token=...) and HLS also embeds a stream token in the path
// (/streams/hls/t/<token>/...), so both must be removed. The URI is split on the
// first '?' (the Go HTTP server has already validated the request line, so no full
// URL parse is needed): the HLS path token is collapsed to its route placeholder and
// the query string is scrubbed via privacy.ScrubQueryString, which percent-decodes
// before scrubbing. Scrubbing is applied only to the query, never the whole URI,
// because anonymizing the path structure would destroy the log's debugging value.
func scrubURIForLog(uri string) string {
	path, query, hasQuery := strings.Cut(uri, "?")
	// Cheap guard: only run the regex when the HLS token prefix is present, so the
	// vast majority of requests skip the regex machinery on this per-request path.
	if strings.Contains(path, hlsTokenPathPrefix) {
		path = hlsTokenPathRegex.ReplaceAllString(path, "${1}:streamToken")
	}
	if !hasQuery {
		return path
	}
	return path + "?" + privacy.ScrubQueryString(query)
}

// NewRequestLogger creates a request logging middleware using Echo 4.14.0+ RequestLoggerWithConfig.
// This replaces the deprecated middleware.Logger().
func NewRequestLogger() echo.MiddlewareFunc {
	return NewRequestLoggerWithSkipper(nil)
}

// NewRequestLoggerWithSkipper creates a request logging middleware with a custom skipper.
func NewRequestLoggerWithSkipper(skipper middleware.Skipper) echo.MiddlewareFunc {
	log := GetLogger()
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper:     skipper,
		LogStatus:   true,
		LogURI:      true,
		LogMethod:   true,
		LogLatency:  true,
		LogRemoteIP: true,
		LogError:    true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			uri := scrubURIForLog(v.URI)

			fields := []logger.Field{
				logger.String("method", v.Method),
				logger.String("uri", uri),
				logger.Int("status", v.Status),
				logger.String("ip", v.RemoteIP),
				logger.Int64("latency_ms", v.Latency.Milliseconds()),
			}

			if v.Error != nil {
				fields = append(fields, logger.Error(v.Error))
			}
			log.Info("request", fields...)
			return nil
		},
	})
}
