package httpcontroller

import (
	"fmt"
	"log"
	"strings"

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

	// Create the logger config
	config := logger.Config{
		Level:         "",    // Let the logger package decide based on development mode
		JSON:          false, // Use console format for readability
		Development:   devMode,
		FilePath:      s.Settings.WebServer.Log.Path,
		DisableColor:  s.Settings.WebServer.Log.Path != "" && !devMode, // Colors in dev console
		DisableCaller: true,                                            // Disable caller information
	}

	if devMode {
		fmt.Println("Logger initialized in DEVELOPMENT mode (debug level enabled)")
	}

	// Create rotation config based on server settings
	rotationConfig := logger.RotationConfig{
		MaxSize:    int(s.Settings.WebServer.Log.MaxSize),
		MaxBackups: 5,  // Default to 5 backups
		MaxAge:     30, // Default to 30 days
		Compress:   true,
	}

	var err error

	// Initialize logger with unified method
	if s.Settings.WebServer.Log.Path != "" {
		// If file path is provided, pass rotation config
		// Note: In development mode, this will create a tee logger (console AND file)
		// In production mode, this will create a file-only logger
		s.Logger, err = logger.NewLogger(config, rotationConfig)
	} else {
		// Console-only logger (no rotation config needed)
		s.Logger, err = logger.NewLogger(config)
	}

	if err != nil {
		log.Fatalf("Failed to initialize logger: %v", err)
	}

	// Create a writer adapter for Echo's logger
	// Echo expects an io.Writer, but our logger doesn't implement that interface directly
	echoLogWriter := &echoLogAdapter{logger: s.Logger}
	s.Echo.Logger.SetOutput(echoLogWriter)

	// Configure Echo's logging middleware with our logger
	s.Echo.Use(middleware.RequestLoggerWithConfig(middleware.RequestLoggerConfig{
		LogURI:      true,
		LogStatus:   true,
		LogRemoteIP: true,
		LogMethod:   true,
		LogError:    true,
		HandleError: true,
		LogValuesFunc: func(c echo.Context, v middleware.RequestLoggerValues) error {
			// Use our custom logger
			errorStr := ""
			if v.Error != nil {
				errorStr = v.Error.Error()
			}
			s.Logger.Info(fmt.Sprintf("%s %v %s %d", v.RemoteIP, v.Method, v.URI, v.Status),
				"remote_ip", v.RemoteIP,
				"method", v.Method,
				"uri", v.URI,
				"status", v.Status,
				"error", errorStr,
			)
			return nil
		},
	}))
}
