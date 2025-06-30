package handlers

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// UUID regex pattern for strict UUID validation
var uuidRegex = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[1-5][0-9a-fA-F]{3}-[89abAB][0-9a-fA-F]{3}-[0-9a-fA-F]{12}$`)

// TelemetryMiddleware provides HTTP request telemetry and enhanced error handling
type TelemetryMiddleware struct {
	httpMetrics *metrics.HTTPMetrics
}

// NewTelemetryMiddleware creates a new telemetry middleware instance
func NewTelemetryMiddleware(httpMetrics *metrics.HTTPMetrics) *TelemetryMiddleware {
	return &TelemetryMiddleware{
		httpMetrics: httpMetrics,
	}
}

// Middleware returns the Echo middleware function
func (tm *TelemetryMiddleware) Middleware() echo.MiddlewareFunc {
	return func(next echo.HandlerFunc) echo.HandlerFunc {
		return func(c echo.Context) error {
			start := time.Now()

			// Normalize path for metrics (remove dynamic segments)
			path := tm.normalizePath(c.Path())
			method := c.Request().Method

			// Execute the handler
			err := next(c)

			// Calculate duration
			duration := time.Since(start).Seconds()

			// Get status code
			statusCode := c.Response().Status
			if statusCode == 0 {
				statusCode = 200 // Default to 200 if not set
			}

			// Record HTTP metrics
			if tm.httpMetrics != nil {
				tm.httpMetrics.RecordHTTPRequest(method, path, statusCode, duration)
				tm.httpMetrics.RecordHTTPResponseSize(method, path, c.Response().Size)

				// Record error metrics if there was an error
				if err != nil {
					errorType := tm.categorizeError(err)
					tm.httpMetrics.RecordHTTPRequestError(method, path, errorType)
				}
			}

			return err
		}
	}
}

// normalizePath removes dynamic segments from paths for consistent metrics
func (tm *TelemetryMiddleware) normalizePath(path string) string {
	// Replace UUIDs and IDs with placeholders
	segments := strings.Split(path, "/")
	for i, segment := range segments {
		// Check if segment looks like an ID (numeric or UUID-like)
		if tm.isID(segment) {
			segments[i] = ":id"
		}
	}

	return strings.Join(segments, "/")
}

// isID checks if a path segment looks like an identifier
func (tm *TelemetryMiddleware) isID(segment string) bool {
	// Check for numeric ID
	if _, err := strconv.Atoi(segment); err == nil {
		return true
	}

	// Check for UUID pattern using strict regex validation
	if uuidRegex.MatchString(segment) {
		return true
	}

	// Check for other common ID patterns
	if len(segment) > 10 && (strings.Contains(segment, "-") || len(segment) > 20) {
		return true
	}

	return false
}

// categorizeError categorizes errors for metrics
func (tm *TelemetryMiddleware) categorizeError(err error) string {
	// Check if it's our enhanced error with category
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) {
		category := enhancedErr.GetCategory()
		switch category {
		case string(errors.CategoryValidation):
			return "validation"
		case string(errors.CategoryDatabase):
			return "database"
		case string(errors.CategoryNetwork):
			return "network"
		case string(errors.CategoryFileIO):
			return "file_io"
		case string(errors.CategoryConfiguration):
			return "configuration"
		case string(errors.CategorySystem):
			return "system"
		default:
			return "unknown"
		}
	}

	// Check for Echo HTTP errors
	var echoErr *echo.HTTPError
	if errors.As(err, &echoErr) {
		switch echoErr.Code {
		case 400:
			return "validation"
		case 401, 403:
			return "auth"
		case 404:
			return "not_found"
		case 500:
			return "system"
		default:
			return "http_error"
		}
	}

	// Check for handler errors
	var handlerErr *HandlerError
	if errors.As(err, &handlerErr) {
		switch handlerErr.Code {
		case 400:
			return "validation"
		case 401, 403:
			return "auth"
		case 404:
			return "not_found"
		case 500:
			return "system"
		default:
			return "handler_error"
		}
	}

	return "unknown"
}

// WithHandlerTelemetry wraps a handler function with telemetry tracking
func (tm *TelemetryMiddleware) WithHandlerTelemetry(handlerName string, fn echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		start := time.Now()

		// Execute the handler
		err := fn(c)

		// Record handler-specific metrics
		if tm.httpMetrics != nil {
			duration := time.Since(start).Seconds()

			if err != nil {
				tm.httpMetrics.RecordHandlerOperation(handlerName, "handle_request", "error")
				errorType := tm.categorizeError(err)
				tm.httpMetrics.RecordHandlerOperationError(handlerName, "handle_request", errorType)
			} else {
				tm.httpMetrics.RecordHandlerOperation(handlerName, "handle_request", "success")
			}

			tm.httpMetrics.RecordHandlerOperationDuration(handlerName, "handle_request", duration)
		}

		return err
	}
}

// RecordDatabaseOperation records a database operation from a handler
func (tm *TelemetryMiddleware) RecordDatabaseOperation(handlerName, operation string, duration time.Duration, err error) {
	if tm.httpMetrics == nil {
		return
	}

	durationSeconds := duration.Seconds()
	tm.httpMetrics.RecordHandlerDatabaseOperationDuration(handlerName, operation, durationSeconds)

	if err != nil {
		tm.httpMetrics.RecordHandlerDatabaseOperation(handlerName, operation, "error")
		errorType := tm.categorizeError(err)
		tm.httpMetrics.RecordHandlerDatabaseOperationError(handlerName, operation, errorType)
	} else {
		tm.httpMetrics.RecordHandlerDatabaseOperation(handlerName, operation, "success")
	}
}

// RecordTemplateRender records template rendering metrics
func (tm *TelemetryMiddleware) RecordTemplateRender(templateName string, duration time.Duration, err error) {
	if tm.httpMetrics == nil {
		return
	}

	durationSeconds := duration.Seconds()
	tm.httpMetrics.RecordTemplateRender(templateName, durationSeconds)

	if err != nil {
		errorType := tm.categorizeError(err)
		tm.httpMetrics.RecordTemplateRenderError(templateName, errorType)
	}
}

// RecordAuthOperation records authentication operation metrics
func (tm *TelemetryMiddleware) RecordAuthOperation(authType, operation string, err error) {
	if tm.httpMetrics == nil {
		return
	}

	if err != nil {
		tm.httpMetrics.RecordAuthOperation(authType, operation, "error")
		errorType := tm.categorizeError(err)
		tm.httpMetrics.RecordAuthError(authType, errorType)
	} else {
		tm.httpMetrics.RecordAuthOperation(authType, operation, "success")
	}
}

// RecordHandlerOperation records a handler operation
func (tm *TelemetryMiddleware) RecordHandlerOperation(handlerName, operation, status string) {
	if tm.httpMetrics == nil {
		return
	}
	tm.httpMetrics.RecordHandlerOperation(handlerName, operation, status)
}

// RecordHandlerOperationDuration records the duration of a handler operation
func (tm *TelemetryMiddleware) RecordHandlerOperationDuration(handlerName, operation string, duration float64) {
	if tm.httpMetrics == nil {
		return
	}
	tm.httpMetrics.RecordHandlerOperationDuration(handlerName, operation, duration)
}

// RecordHandlerOperationError records a handler operation error
func (tm *TelemetryMiddleware) RecordHandlerOperationError(handlerName, operation, errorType string) {
	if tm.httpMetrics == nil {
		return
	}
	tm.httpMetrics.RecordHandlerOperationError(handlerName, operation, errorType)
}
