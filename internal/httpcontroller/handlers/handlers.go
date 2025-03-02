package handlers

import (
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"runtime/debug"
	"strings"
	"sync"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
	"github.com/tphakala/birdnet-go/internal/security"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

var settingsMutex sync.RWMutex

// Handlers contains all the handler functions and their dependencies
type Handlers struct {
	baseHandler
	DS                datastore.Interface
	Settings          *conf.Settings
	DashboardSettings *conf.Dashboard
	BirdImageCache    *imageprovider.BirdImageCache
	SSE               *SSEHandler                 // Server Side Events handler
	SunCalc           *suncalc.SunCalc            // SunCalc instance for calculating sun event times
	AudioLevelChan    chan myaudio.AudioLevelData // Channel for audio level updates
	OAuth2Server      *security.OAuth2Server
	controlChan       chan string
	notificationChan  chan Notification
	CloudflareAccess  *security.CloudflareAccess
	debug             bool
	Server            interface{ IsAccessAllowed(c echo.Context) bool }
	Logger            *logger.Logger // Our custom logger
}

// HandlerError is a custom error type that includes an HTTP status code and a user-friendly message.
type HandlerError struct {
	Err     error
	Message string
	Code    int
}

// Error implements the error interface for HandlerError.
func (e *HandlerError) Error() string {
	return fmt.Sprintf("%s: %v", e.Message, e.Err)
}

// baseHandler provides common functionality for all handlers.
type baseHandler struct {
	errorHandler func(error) *HandlerError
	logger       *log.Logger // Keep for backward compatibility
}

// newHandlerError creates a new HandlerError with the given parameters.
func (bh *baseHandler) NewHandlerError(err error, message string, code int) *HandlerError {
	handlerErr := &HandlerError{
		Err:     err,
		Message: fmt.Sprintf("%s: %v", message, err),
		Code:    code,
	}
	bh.logError(handlerErr)
	return handlerErr
}

// logError logs an error message.
func (bh *baseHandler) logError(err *HandlerError) {
	bh.logger.Printf("Error: %s (Code: %d, Underlying error: %v)", err.Message, err.Code, err.Err)
}

// logInfo logs an info message.
func (bh *baseHandler) logInfo(message string) {
	bh.logger.Printf("Info: %s", message)
}

// LogDebug logs a debug message using the custom logger if available, otherwise uses standard logger
func (h *Handlers) LogDebug(msg string, fields ...interface{}) {
	if h.Logger != nil {
		h.Logger.Debug(msg, fields...)
	} else {
		h.baseHandler.logInfo("DEBUG: " + msg)
	}
}

// LogInfo logs an info message using the custom logger if available, otherwise uses standard logger
func (h *Handlers) LogInfo(msg string, fields ...interface{}) {
	if h.Logger != nil {
		h.Logger.Info(msg, fields...)
	} else {
		h.baseHandler.logInfo(msg)
	}
}

// LogWarn logs a warning message using the custom logger if available, otherwise uses standard logger
func (h *Handlers) LogWarn(msg string, fields ...interface{}) {
	if h.Logger != nil {
		h.Logger.Warn(msg, fields...)
	} else {
		h.baseHandler.logInfo("WARN: " + msg)
	}
}

// LogError logs an error message using the custom logger if available, otherwise uses standard logger
func (h *Handlers) LogError(msg string, err error, fields ...interface{}) {
	if h.Logger != nil {
		if err != nil {
			h.Logger.Error(msg, append(fields, "error", err)...)
		} else {
			h.Logger.Error(msg, fields...)
		}
	} else {
		if err != nil {
			h.baseHandler.logError(&HandlerError{
				Err:     err,
				Message: msg,
				Code:    http.StatusInternalServerError,
			})
		} else {
			h.baseHandler.logInfo("ERROR: " + msg)
		}
	}
}

// New creates a new Handlers instance with the given dependencies.
func New(ds datastore.Interface, settings *conf.Settings, dashboardSettings *conf.Dashboard,
	birdImageCache *imageprovider.BirdImageCache, stdLogger *log.Logger, sunCalc *suncalc.SunCalc,
	audioLevelChan chan myaudio.AudioLevelData, oauth2Server *security.OAuth2Server,
	controlChan chan string, notificationChan chan Notification,
	server interface{ IsAccessAllowed(c echo.Context) bool }) *Handlers {

	if stdLogger == nil {
		stdLogger = log.New(os.Stderr, "ERROR: ", log.Ldate|log.Ltime|log.Lshortfile)
	}

	handlers := &Handlers{
		baseHandler: baseHandler{
			errorHandler: defaultErrorHandler,
			logger:       stdLogger,
		},
		DS:                ds,
		Settings:          settings,
		DashboardSettings: dashboardSettings,
		BirdImageCache:    birdImageCache,
		SSE:               nil, // Initialize later with logger
		SunCalc:           sunCalc,
		AudioLevelChan:    audioLevelChan,
		OAuth2Server:      oauth2Server,
		controlChan:       controlChan,
		notificationChan:  notificationChan,
		CloudflareAccess:  security.NewCloudflareAccess(),
		debug:             settings.Debug,
		Server:            server,
		// Logger will be set separately
	}

	return handlers
}

// SetLogger sets the custom logger for the handlers
func (h *Handlers) SetLogger(logger *logger.Logger) {
	h.Logger = logger

	// Now that we have the logger, initialize the SSE handler with it
	if h.SSE == nil {
		h.SSE = NewSSEHandler(logger)
	}
}

// defaultErrorHandler is the default implementation of error handling.
func defaultErrorHandler(err error) *HandlerError {
	var he *HandlerError
	if errors.As(err, &he) {
		return he
	}
	return &HandlerError{
		Err:     err,
		Message: "An unexpected error occurred",
		Code:    http.StatusInternalServerError,
	}
}

// HandleError is a utility method to handle errors in Echo handlers.
func (h *Handlers) HandleError(err error, c echo.Context) error {
	var he *HandlerError
	var echoHTTPError *echo.HTTPError

	switch {
	case errors.As(err, &echoHTTPError):
		he = &HandlerError{
			Err:     echoHTTPError,
			Message: fmt.Sprintf("%v", echoHTTPError.Message),
			Code:    echoHTTPError.Code,
		}
	case errors.As(err, &he):
		// It's already a HandlerError, use it as is
	default:
		// For any other error, treat it as an internal server error
		he = &HandlerError{
			Err:     err,
			Message: "An unexpected error occurred",
			Code:    http.StatusInternalServerError,
		}
	}

	// Get request-specific details for logging
	requestID := c.Request().Header.Get("X-Request-ID")
	if requestID == "" {
		requestID = c.Response().Header().Get("X-Request-ID")
	}
	clientIP := c.RealIP()
	path := c.Request().URL.Path
	method := c.Request().Method

	// Log the error using our custom logger if available
	if h.Logger != nil {
		// Create an error-specific logger with component hierarchy
		errorLogger := h.Logger.Named("http.error")

		// Get a shortened stack trace (limit to 5 frames for readability)
		stackTrace := limitStackTrace(string(debug.Stack()), 5)

		// Include all relevant fields for debugging
		fields := []interface{}{
			"code", he.Code,
			"message", he.Message,
			"error", he.Err,
			"path", path,
			"method", method,
			"client_ip", clientIP,
		}

		// Only include request ID if it exists
		if requestID != "" {
			fields = append(fields, "request_id", requestID)
		}

		// Only include stack trace for server errors
		if he.Code >= 500 {
			fields = append(fields, "stacktrace", stackTrace)
		}

		// Log at appropriate level based on status code
		switch {
		case he.Code >= 500:
			errorLogger.Error("Server error occurred", fields...)
		case he.Code >= 400:
			errorLogger.Warn("Client error occurred", fields...)
		default:
			errorLogger.Info("Request error", fields...)
		}
	} else {
		// Fallback to the base handler logger
		h.baseHandler.logError(he)
	}

	// Check if headers have already been sent
	if c.Response().Committed {
		return nil
	}

	// Get security state for the current context
	security := h.GetSecurity(c)

	// Prepare error data with consistent structure across all error templates
	errorData := struct {
		Code       int
		Title      string
		Message    string
		StackTrace string
		Settings   *conf.Settings
		Security   *Security // Use the Security type from handlers package
		User       interface{}
		Debug      bool
	}{
		Code:       he.Code,
		Title:      fmt.Sprintf("%d Error", he.Code),
		Message:    he.Message,
		StackTrace: string(debug.Stack()),
		Settings:   h.Settings,
		Security:   security, // Pass the security state
		User:       c.Get("user"),
		Debug:      h.Settings.Debug,
	}

	// Choose the appropriate template based on the error code
	template := h.getErrorTemplate(he.Code)

	// Set the response status code
	c.Response().Status = he.Code

	// Render the template
	return c.Render(he.Code, template, errorData)
}

// limitStackTrace reduces the stack trace to a given number of frames for readability
func limitStackTrace(stackTrace string, maxFrames int) string {
	lines := strings.Split(stackTrace, "\n")
	if len(lines) <= maxFrames*2 {
		return stackTrace
	}

	// Take the first line (which has the error) plus maxFrames*2 lines (each frame has 2 lines in Go stack traces)
	truncatedLines := make([]string, 0, maxFrames*2+2)                   // Pre-allocate capacity
	truncatedLines = append(truncatedLines, lines[0])                    // Add first line (error message)
	truncatedLines = append(truncatedLines, lines[1:(maxFrames*2)+1]...) // Add stack frames
	truncatedLines = append(truncatedLines, fmt.Sprintf("... %d more frames omitted ...", (len(lines)-(maxFrames*2+1))/2))

	return strings.Join(truncatedLines, "\n")
}

// getErrorTemplate returns the appropriate error template name based on the error code
func (h *Handlers) getErrorTemplate(code int) string {
	switch code {
	case http.StatusNotFound:
		return "error-404"
	case http.StatusInternalServerError:
		return "error-500"
	default:
		return "error-default" // Use the correct template name
	}
}

// WithErrorHandling wraps an Echo handler function with error handling.
func (h *Handlers) WithErrorHandling(fn func(echo.Context) error) echo.HandlerFunc {
	return func(c echo.Context) error {
		err := fn(c)
		if err != nil {
			return h.HandleError(err, c)
		}
		return nil
	}
}

// GetLabels returns the list of all available species labels
func (h *Handlers) GetLabels() []string {
	return h.Settings.BirdNET.Labels
}

// Security represents the authentication and access control state
type Security struct {
	Enabled       bool
	AccessAllowed bool
	IsCloudflare  bool
}

// GetSecurity returns the current security state for the context
func (h *Handlers) GetSecurity(c echo.Context) *Security {
	return &Security{
		Enabled:       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
		AccessAllowed: h.Server.IsAccessAllowed(c),
		IsCloudflare:  h.CloudflareAccess.IsEnabled(c),
	}
}
