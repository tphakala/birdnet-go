// Package middleware provides HTTP middleware components for the BirdNET-Go server.
package middleware

import (
	"log/slog"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// NewRequestLogger creates a request logging middleware using Echo 4.14.0+ RequestLoggerWithConfig.
// This replaces the deprecated middleware.Logger().
func NewRequestLogger(logger *slog.Logger) echo.MiddlewareFunc {
	return NewRequestLoggerWithSkipper(logger, nil)
}

// NewRequestLoggerWithSkipper creates a request logging middleware with a custom skipper.
func NewRequestLoggerWithSkipper(logger *slog.Logger, skipper middleware.Skipper) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper:     skipper,
		LogStatus:   true,
		LogURI:      true,
		LogMethod:   true,
		LogLatency:  true,
		LogRemoteIP: true,
		LogError:    true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			if logger == nil {
				return nil
			}

			attrs := []slog.Attr{
				slog.String("method", v.Method),
				slog.String("uri", v.URI),
				slog.Int("status", v.Status),
				slog.String("ip", v.RemoteIP),
				slog.Duration("latency", v.Latency),
			}

			if v.Error != nil {
				attrs = append(attrs, slog.String("error", v.Error.Error()))
			}

			logger.LogAttrs(c.Request().Context(), slog.LevelInfo, "request", attrs...)
			return nil
		},
	})
}
