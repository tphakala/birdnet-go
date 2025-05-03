package security

import (
	"io"
	"log"
	"log/slog"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger for security related events
var (
	securityLogger    *slog.Logger
	securityLogCloser func() error // Stores the closer function
)

func init() {
	var err error
	logFilePath := filepath.Join("logs", "security.log") // Use filepath.Join

	// Initialize the service-specific file logger, capturing the closer
	securityLogger, securityLogCloser, err = logging.NewFileLogger(logFilePath, "security", slog.LevelInfo)
	if err != nil {
		// Use standard log for this critical setup error
		log.Printf("ERROR: Failed to initialize security file logger at %s: %v. Service logging disabled.", logFilePath, err)
		// Fallback to a disabled logger to prevent nil panics
		securityLogger = slog.New(slog.NewJSONHandler(io.Discard, nil))
		securityLogCloser = func() error { return nil } // No-op closer for fallback
	} else {
		// Use standard log for initial confirmation message
		log.Printf("Security file logger initialized successfully to %s", logFilePath)
	}
}

// LogInfo logs an informational message to the security log.
func LogInfo(msg string, args ...any) {
	securityLogger.Info(msg, args...)
}

// LogWarn logs a warning message to the security log.
func LogWarn(msg string, args ...any) {
	securityLogger.Warn(msg, args...)
}

// LogError logs an error message to the security log.
func LogError(msg string, args ...any) {
	securityLogger.Error(msg, args...)
}

// CloseLogger closes the security-specific file logger, if one was successfully initialized.
// This should be called during graceful shutdown.
func CloseLogger() error {
	if securityLogCloser != nil {
		log.Println("Closing security file logger...")
		return securityLogCloser()
	}
	return nil
}
