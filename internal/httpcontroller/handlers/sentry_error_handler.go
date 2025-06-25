package handlers

import (
	"fmt"
	"log"
	"net/http"

	"github.com/getsentry/sentry-go"
	sentryecho "github.com/getsentry/sentry-go/echo"
	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/telemetry"
)

// getStatusCodeFromError determines the appropriate HTTP status code based on error type
func getStatusCodeFromError(err error) int {
	// Check for enhanced errors with categories
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) {
		switch enhancedErr.GetCategory() {
		case errors.CategoryValidation:
			return http.StatusBadRequest
		case errors.CategoryMQTTAuth:
			return http.StatusUnauthorized
		case errors.CategoryNetwork, errors.CategoryDatabase, errors.CategoryFileIO, errors.CategoryRTSP, errors.CategoryMQTTConnection:
			return http.StatusServiceUnavailable
		case errors.CategoryConfiguration, errors.CategoryModelInit, errors.CategoryModelLoad:
			return http.StatusInternalServerError
		case errors.CategoryHTTP:
			// For HTTP errors, default to bad gateway
			return http.StatusBadGateway
		default:
			return http.StatusInternalServerError
		}
	}

	// Check for Echo HTTP errors
	var echoErr *echo.HTTPError
	if errors.As(err, &echoErr) {
		return echoErr.Code
	}

	// Check for handler errors
	var handlerErr *HandlerError
	if errors.As(err, &handlerErr) {
		return handlerErr.Code
	}

	// Default to internal server error
	return http.StatusInternalServerError
}

// HandleErrorWithSentry is a helper function that handles errors in a privacy-compliant way
// It logs the error locally and sends it to Sentry if enabled
func HandleErrorWithSentry(c echo.Context, err error, component, userMessage string, statusCode int) error {
	// Log the error locally for debugging
	log.Printf("Error in %s: %v", component, err)

	// Send to Sentry if enabled (privacy-compliant)
	telemetry.CaptureError(err, component)

	// Return appropriate error response to user
	return c.JSON(statusCode, map[string]string{
		"error": userMessage,
	})
}

// HandleErrorWithSentryAuto is like HandleErrorWithSentry but automatically determines the status code
func HandleErrorWithSentryAuto(c echo.Context, err error, component, userMessage string) error {
	statusCode := getStatusCodeFromError(err)
	return HandleErrorWithSentry(c, err, component, userMessage, statusCode)
}

// WithSentryContext wraps a handler to capture errors with additional context
func WithSentryContext(component string, handler echo.HandlerFunc) echo.HandlerFunc {
	return func(c echo.Context) error {
		// Get the Sentry hub from context (if Sentry is enabled)
		if hub := sentryecho.GetHubFromContext(c); hub != nil {
			hub.Scope().SetTag("component", component)
		}

		// Execute the handler
		err := handler(c)
		if err != nil {
			// Capture the error with context
			telemetry.CaptureError(err, component)
		}

		return err
	}
}

// Example usage in a handler:
// This is a demonstration of how to use Sentry error handling in your handlers
/*
func (h *Handlers) ExampleHandler(c echo.Context) error {
	// Wrap with Sentry context
	return WithSentryContext("example", func(c echo.Context) error {
		// Your handler logic here
		data, err := h.DS.GetAllDetections(nil, nil) // Example database call
		if err != nil {
			// Use the privacy-compliant error handler
			return HandleErrorWithSentry(c, err, "example", "Failed to retrieve detections", 500)
		}

		return c.JSON(200, data)
	})(c)
}
*/

// CaptureMessage sends a message to Sentry with privacy-compliant context
func CaptureMessage(message string, level sentry.Level, component string) {
	telemetry.CaptureMessage(message, level, component)
}

// RecoverWithSentry recovers from panics and sends them to Sentry
func RecoverWithSentry(component string) {
	if r := recover(); r != nil {
		err := fmt.Errorf("panic recovered: %v", r)
		telemetry.CaptureError(err, component)
		// Re-panic to let Echo's recover middleware handle it
		panic(r)
	}
}
