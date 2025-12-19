package security

import (
	"io"
	"log"
	"log/slog"
	"path/filepath"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logging"
)

// SecurityLogger defines the logging interface used by security helper functions.
// This allows passing contextual loggers (e.g., with request-specific fields) to helper functions.
type SecurityLogger interface {
	Debug(msg string, args ...any)
	Info(msg string, args ...any)
	Warn(msg string, args ...any)
	Error(msg string, args ...any)
}

// Package-level logger for security related events
var (
	securityLogger    *slog.Logger
	securityLogCloser func() error         // Stores the closer function
	securityLevelVar  = new(slog.LevelVar) // Dynamic level control
	loggerMutex       sync.RWMutex         // Protects logger access
)

func init() {
	var err error
	logFilePath := filepath.Join("logs", "security.log") // Use filepath.Join

	initialLevel := slog.LevelInfo
	securityLevelVar.Set(initialLevel)

	// Initialize the service-specific file logger, capturing the closer
	var newLogger *slog.Logger
	newLogger, securityLogCloser, err = logging.NewFileLogger(logFilePath, "security", securityLevelVar)
	if err != nil {
		// Use standard log for this critical setup error
		log.Printf("ERROR: Failed to initialize security file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Fallback to a disabled logger to prevent nil panics
		newLogger = slog.New(slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: securityLevelVar}))
		securityLogCloser = func() error { return nil } // No-op closer for fallback
	}
	// Set the logger with proper locking
	loggerMutex.Lock()
	securityLogger = newLogger
	loggerMutex.Unlock()
}

// LogInfo logs an informational message to the security log.
func LogInfo(msg string, args ...any) {
	logger().Info(msg, args...)
}

// LogWarn logs a warning message to the security log.
func LogWarn(msg string, args ...any) {
	logger().Warn(msg, args...)
}

// LogError logs an error message to the security log.
func LogError(msg string, args ...any) {
	logger().Error(msg, args...)
}

// CloseLogger closes the security-specific file logger, if one was successfully initialized.
// This should be called during graceful shutdown.
func CloseLogger() error {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()
	if securityLogCloser != nil {
		log.Println("Closing security file logger...")
		return securityLogCloser()
	}
	return nil
}

// logger safely returns the current logger for internal use
func logger() *slog.Logger {
	loggerMutex.RLock()
	defer loggerMutex.RUnlock()
	return securityLogger
}

// setTestLogger sets a test-specific logger and returns a function to restore the original
// This function should only be used in tests
func setTestLogger(logger *slog.Logger) func() {
	loggerMutex.Lock()
	defer loggerMutex.Unlock()
	original := securityLogger
	securityLogger = logger
	return func() {
		loggerMutex.Lock()
		defer loggerMutex.Unlock()
		securityLogger = original
	}
}
