package middleware

import (
	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

// Compression configuration constants.
const (
	// DefaultGzipLevel is the default compression level (1-9, higher = more compression).
	DefaultGzipLevel = 5
)

// NewGzip creates a gzip compression middleware with the default compression level.
// It automatically skips compression for SSE endpoints.
func NewGzip() echo.MiddlewareFunc {
	return NewGzipWithLevel(DefaultGzipLevel)
}

// NewGzipWithLevel creates a gzip compression middleware with a custom compression level.
// It automatically skips compression for SSE endpoints.
func NewGzipWithLevel(level int) echo.MiddlewareFunc {
	return middleware.GzipWithConfig(middleware.GzipConfig{
		Level:   level,
		Skipper: SSESkipper,
	})
}

// NewGzipWithSkipper creates a gzip compression middleware with a custom skipper function.
func NewGzipWithSkipper(level int, skipper middleware.Skipper) echo.MiddlewareFunc {
	return middleware.GzipWithConfig(middleware.GzipConfig{
		Level:   level,
		Skipper: skipper,
	})
}

// SSESkipper is a skipper function that skips compression for Server-Sent Events endpoints.
func SSESkipper(c echo.Context) bool {
	return c.Request().Header.Get("Accept") == "text/event-stream"
}
