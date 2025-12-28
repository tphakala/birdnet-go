// Package middleware provides HTTP middleware components for the BirdNET-Go server.
package middleware

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// NewRequestLogger creates a request logging middleware using Echo 4.14.0+ RequestLoggerWithConfig.
// This replaces the deprecated middleware.Logger().
func NewRequestLogger() echo.MiddlewareFunc {
	return NewRequestLoggerWithSkipper(nil)
}

// NewRequestLoggerWithSkipper creates a request logging middleware with a custom skipper.
func NewRequestLoggerWithSkipper(skipper middleware.Skipper) echo.MiddlewareFunc {
	return middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		Skipper:     skipper,
		LogStatus:   true,
		LogURI:      true,
		LogMethod:   true,
		LogLatency:  true,
		LogRemoteIP: true,
		LogError:    true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			log := GetLogger()

			if v.Error != nil {
				log.Info("request",
					logger.String("method", v.Method),
					logger.String("uri", v.URI),
					logger.Int("status", v.Status),
					logger.String("ip", v.RemoteIP),
					logger.Int64("latency_ms", v.Latency.Milliseconds()),
					logger.Error(v.Error))
			} else {
				log.Info("request",
					logger.String("method", v.Method),
					logger.String("uri", v.URI),
					logger.Int("status", v.Status),
					logger.String("ip", v.RemoteIP),
					logger.Int64("latency_ms", v.Latency.Milliseconds()))
			}
			return nil
		},
	})
}
