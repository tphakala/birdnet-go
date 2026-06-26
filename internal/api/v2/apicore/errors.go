package apicore

import (
	"context"
	"crypto/rand"
	"net/http"

	"github.com/labstack/echo/v4"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// ErrorResponse is the API error response structure.
type ErrorResponse struct {
	Error         string         `json:"error"`
	Message       string         `json:"message"`
	Code          int            `json:"code"`
	CorrelationID string         `json:"correlation_id"`         // Unique identifier for tracking this error
	ErrorKey      string         `json:"error_key,omitempty"`    // i18n translation key for frontend
	ErrorParams   map[string]any `json:"error_params,omitempty"` // Interpolation parameters for error_key
}

// ErrDatastoreUnavailable is returned by DS-dependent handlers when the controller was
// constructed without a datastore. NewWithOptions permits a nil datastore ("datastore
// disabled" mode) and initRoutes skips registering the detection and media route groups
// in that mode; RequireDatastore is defense in depth for any such handler reached anyway.
var ErrDatastoreUnavailable = errors.NewStd("datastore is not available")

// NewErrorResponse creates a new API error response using the controller's
// injected settings to decide whether to expose raw error details.
func (c *Core) NewErrorResponse(err error, message string, code int) *ErrorResponse {
	// Generate a random correlation ID (8 characters should be sufficient)
	correlationID := GenerateCorrelationID()

	// Only expose raw err.Error() in debug mode: it can contain internal
	// paths, SQL errors, stack traces, etc. In production, use the
	// sanitized message parameter instead.
	var errorStr string
	// Read the controller's own debug flag via the lock-free atomic Settings pointer: this
	// is reached from HandleError while UpdateSettings holds the settings write lock, so
	// it must not acquire the lock (a non-reentrant RLock would deadlock), and the
	// flag must remain per-controller (handlers assert debug-gated error verbosity
	// per controller, not via the shared global snapshot). ControllerSettings()
	// reads the per-controller snapshot published under that same write lock, so
	// the read is race-free. See ControllerSettings.
	settings := c.ControllerSettings()
	if err != nil && settings != nil && settings.WebServer.Debug {
		errorStr = err.Error()
	} else {
		errorStr = message
	}

	return &ErrorResponse{
		Error:         errorStr,
		Message:       message,
		Code:          code,
		CorrelationID: correlationID,
	}
}

// GenerateCorrelationID creates a unique identifier for error tracking using cryptographic randomness
// for better security and uniqueness guarantees across all platforms.
func GenerateCorrelationID() string {
	const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	const length = 8

	b := make([]byte, length)
	if _, err := rand.Read(b); err != nil {
		// Fall back to a default ID if crypto/rand fails
		return "ERR-RAND"
	}

	// Map the random bytes to charset characters
	for i := range b {
		b[i] = charset[int(b[i])%len(charset)]
	}
	return string(b)
}

// handleErrorInternal is the shared implementation for HandleError and HandleErrorWithKey.
func (c *Core) handleErrorInternal(ctx echo.Context, err error, message string, code int, errorKey string, errorParams map[string]any) error {
	errorResp := c.NewErrorResponse(err, message, code)
	errorResp.ErrorKey = errorKey
	errorResp.ErrorParams = errorParams

	// Determine IP to log using the request context
	ip := ctx.RealIP()

	// Get tunnel info from context
	isTunneled, _ := ctx.Get(CtxKeyIsTunneled).(bool)
	tunnelProvider, _ := ctx.Get(CtxKeyTunnelProvider).(string)

	// Build error string for logging
	var errorStr string
	if err != nil {
		errorStr = err.Error()
	} else {
		errorStr = message
	}

	// Build log fields
	fields := []logger.Field{
		logger.String("correlation_id", errorResp.CorrelationID),
		logger.String("message", message),
		logger.String("error", errorStr),
		logger.Int("code", code),
		logger.String("path", ctx.Request().URL.Path),
		logger.String("method", ctx.Request().Method),
		logger.String("ip", ip),
		logger.Bool("tunneled", isTunneled),
		logger.String("tunnel_provider", tunnelProvider),
	}
	if errorKey != "" {
		fields = append(fields, logger.String("error_key", errorKey))
	}

	c.LogErrorIfEnabled("API Error", fields...)

	// Report server-side errors (5xx) to Sentry telemetry.
	// 4xx errors are client mistakes (bad input, not found), not bugs, and are excluded.
	if code >= http.StatusInternalServerError {
		c.reportErrorToTelemetry(ctx, err, message, code)
	}

	return ctx.JSON(code, errorResp)
}

// reportErrorToTelemetry reports server-side errors (5xx) to Sentry telemetry.
// Errors already reported by lower layers (e.g., datastore) are skipped to avoid
// duplicate Sentry events. Privacy scrubbing and opt-in checks are handled by the
// internal/errors and internal/telemetry packages.
func (c *Core) reportErrorToTelemetry(ctx echo.Context, err error, message string, code int) {
	// Skip if the underlying error was already reported by a lower layer.
	if err != nil {
		var ee *errors.EnhancedError
		if errors.As(err, &ee) && ee.IsReported() {
			return
		}
	}

	// Client disconnects and request timeouts are not server bugs.
	if err != nil && (errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded)) {
		return
	}

	path := ctx.Request().URL.Path
	method := ctx.Request().Method

	var builder *errors.ErrorBuilder
	if err != nil {
		builder = errors.New(err)
	} else {
		builder = errors.Newf("%s", message)
	}

	_ = builder.
		Component("api").
		Category(errors.CategoryHTTP).
		Context("http_status", code).
		Context("endpoint", path).
		Context("method", method).
		Build()
}

// HandleError constructs and returns an appropriate error response.
func (c *Core) HandleError(ctx echo.Context, err error, message string, code int) error {
	return c.handleErrorInternal(ctx, err, message, code, "", nil)
}

// HandleErrorWithKey constructs and returns an error response with an i18n translation key.
// The errorKey and errorParams allow the frontend to display translated error messages.
func (c *Core) HandleErrorWithKey(ctx echo.Context, err error, message string, code int, errorKey string, errorParams map[string]any) error {
	return c.handleErrorInternal(ctx, err, message, code, errorKey, errorParams)
}

// RequireDatastore writes a 503 Service Unavailable response and returns the non-nil
// errDatastoreUnavailable when the controller has no datastore, so handlers can guard with:
//
//	if err := c.RequireDatastore(ctx); err != nil {
//	    return err
//	}
//
// It returns the sentinel (not HandleError's nil) so the guard actually short-circuits the
// caller; the 503 body is already written, so echo's error handler skips the committed
// response. This honors the constructor's advertised "datastore disabled" mode instead of
// letting a nil c.DS dereference panic.
func (c *Core) RequireDatastore(ctx echo.Context) error {
	if c.DS == nil {
		_ = c.HandleError(ctx, ErrDatastoreUnavailable, "Datastore is not available", http.StatusServiceUnavailable)
		return ErrDatastoreUnavailable
	}
	return nil
}
