// Package middleware provides HTTP middleware components for the BirdNET-Go server.
package middleware

import (
	"net/url"
	"regexp"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/privacy"
)

var hlsTokenPathRegex = regexp.MustCompile(`(/streams/hls/t/)[^/?]+`)

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
			uri := v.URI

			parsed, err := url.ParseRequestURI(uri)
			if err == nil {
				parsed.Path = hlsTokenPathRegex.ReplaceAllString(parsed.Path, "${1}:streamToken")
				// Only scrub the query string; ScrubMessage on the full URI destroys the path
				if parsed.RawQuery != "" {
					parsed.RawQuery = privacy.ScrubMessage(parsed.RawQuery)
				}
				uri = parsed.String()
			} else {
				// Fallback if parsing fails
				uri = hlsTokenPathRegex.ReplaceAllString(uri, "${1}:streamToken")
			}

			fields := []logger.Field{
				logger.String("method", v.Method),
				logger.String("uri", uri),
				logger.Int("status", v.Status),
				logger.String("ip", v.RemoteIP),
				logger.Int64("latency_ms", v.Latency.Milliseconds()),
			}

			if v.Error != nil {
				fields = append(fields, logger.Error(v.Error))
				log.Error("request", fields...)
			} else {
				log.Info("request", fields...)
			}
			return nil
		},
	})
}
