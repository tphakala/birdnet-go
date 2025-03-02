package httpcontroller

import (
	"fmt"
	"log"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// echoLogAdapter adapts our Logger to implement io.Writer for Echo
type echoLogAdapter struct {
	logger *logger.Logger
}

// Write implements io.Writer for echoLogAdapter
func (a *echoLogAdapter) Write(p []byte) (n int, err error) {
	msg := strings.TrimSpace(string(p))
	if msg != "" {
		a.logger.Info(msg)
	}
	return len(p), nil
}

// Debug logs debug messages if debug mode is enabled
func (s *Server) Debug(format string, v ...interface{}) {
	if s.isDevMode() {
		// In debug mode, always use the logger if available
		if s.Logger != nil {
			message := format
			if len(v) > 0 {
				message = fmt.Sprintf(format, v...)
			}
			s.Logger.Debug(message)
		} else {
			// Fall back to standard log if logger isn't initialized yet
			if len(v) == 0 {
				log.Print(format)
			} else {
				log.Printf(format, v...)
			}
		}
	}
}

// initLogger initializes the custom logger.
func (s *Server) initLogger() {
	if !s.Settings.WebServer.Log.Enabled {
		fmt.Println("Logging disabled")
		return
	}

	// Check if we're in development mode
	devMode := s.isDevMode()

	if devMode {
		fmt.Println("Logger initialized in DEVELOPMENT mode (debug level enabled)")
	}

	// Only initialize logger if not already set
	// This allows NewWithLogger to set a proper parent logger
	if s.Logger == nil {
		// Use the global logger with a component name instead of creating a new one
		// This ensures consistent logging behavior across the application
		s.Logger = logger.GetGlobal().Named("http")

		if s.Logger == nil {
			log.Fatal("Failed to get global logger")
		}
	}

	// Create a writer adapter for Echo's logger
	// Echo expects an io.Writer, but our logger doesn't implement that interface directly
	echoLogWriter := &echoLogAdapter{logger: s.Logger}
	s.Echo.Logger.SetOutput(echoLogWriter)

	// Configure Echo's logging middleware with our structured logger
	s.setupRequestLogger()
}

// setupRequestLogger configures the HTTP request logging middleware
func (s *Server) setupRequestLogger() {
	// Create a component-specific logger for HTTP requests
	httpLogger := s.Logger.Named("http.request")

	// Configure request logger middleware
	s.Echo.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:          true,
		LogStatus:       true,
		LogLatency:      true,
		LogRemoteIP:     true,
		LogMethod:       true,
		LogError:        true,
		LogResponseSize: true,
		LogUserAgent:    true,
		LogReferer:      true,
		HandleError:     true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			// Get a request-specific ID to track related log entries
			requestID := c.Request().Header.Get("X-Request-ID")
			if requestID == "" {
				requestID = c.Response().Header().Get("X-Request-ID")
			}

			// Extract path parameters if any
			pathParams := make(map[string]string)
			for _, name := range c.ParamNames() {
				pathParams[name] = c.Param(name)
			}

			// Determine which log level to use based on status code
			var logMethod func(string, ...interface{})
			statusCode := v.Status

			switch {
			case statusCode >= 500:
				logMethod = httpLogger.Error
			case statusCode >= 400:
				logMethod = httpLogger.Warn
			case s.isDevMode() && statusCode >= 300:
				// Log redirects as debug in dev mode only
				logMethod = httpLogger.Debug
			default:
				logMethod = httpLogger.Info
			}

			// Calculate request latency in milliseconds
			latencyMs := float64(v.Latency) / float64(time.Millisecond)

			// Format a concise message showing the essential request info
			message := fmt.Sprintf("%s %s %d", v.Method, v.URI, statusCode)

			// Fields to include in all log messages
			fields := []interface{}{
				"remote_ip", v.RemoteIP,
				"method", v.Method,
				"uri", v.URI,
				"status", statusCode,
				"latency_ms", latencyMs,
			}

			// Add optional fields only if they have values
			if requestID != "" {
				fields = append(fields, "request_id", requestID)
			}

			if v.ResponseSize > 0 {
				fields = append(fields, "resp_size", v.ResponseSize)
			}

			if v.Error != nil {
				fields = append(fields, "error", v.Error.Error())
			}

			if len(pathParams) > 0 {
				fields = append(fields, "path_params", pathParams)
			}

			// Only log UserAgent and Referer for non-success responses or in debug mode
			if (statusCode >= 400 || s.isDevMode()) && v.UserAgent != "" {
				fields = append(fields, "user_agent", v.UserAgent)
			}

			if (statusCode >= 400 || s.isDevMode()) && v.Referer != "" {
				fields = append(fields, "referer", v.Referer)
			}

			// Log with the appropriate level
			logMethod(message, fields...)

			return nil
		},
	}))
}
