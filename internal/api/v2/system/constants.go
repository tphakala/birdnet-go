package system

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Operating system name constants (compared against runtime.GOOS).
const (
	osLinux   = "linux"
	osWindows = "windows"
	osDarwin  = "darwin"
)

// valueUnknown is the generic fallback value for unavailable system fields.
const valueUnknown = "unknown"

// Time conversion constants.
const (
	secondsPerMinute      = 60   // Seconds in a minute
	secondsPerHour        = 3600 // Seconds in an hour
	millisecondsPerSecond = 1000 // Milliseconds in a second
)

// errResponseHandled signals that an HTTP error response has already been sent to
// the client, so the caller should return it without sending another. It mirrors
// the package-api sentinel of the same purpose; the value never escapes this
// package (callers only check it for non-nil).
var errResponseHandled = errors.NewStd("response already handled")

// validateDateFormatWithResponse validates a date string is in YYYY-MM-DD format.
// It returns errResponseHandled after sending an error response, or nil if valid
// (an empty string is treated as a valid optional parameter).
func (c *Handler) validateDateFormatWithResponse(ctx echo.Context, dateStr, paramName, operation string) error {
	if dateStr == "" {
		return nil // Empty is valid (optional parameter)
	}
	if _, err := time.Parse(time.DateOnly, dateStr); err != nil {
		c.LogErrorIfEnabled("Invalid date format",
			logger.String("parameter", paramName),
			logger.String("value", dateStr),
			logger.String("operation", operation),
			logger.Error(err),
			logger.String("ip", ctx.RealIP()),
			logger.String("path", ctx.Request().URL.Path),
		)
		_ = c.HandleError(ctx, nil, "Invalid date format. Use YYYY-MM-DD", http.StatusBadRequest)
		return errResponseHandled
	}
	return nil
}
