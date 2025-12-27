package notification

import (
	"io"
	"log/slog"
	"os"
	"sync"
)

// Package-level logger instance and closer for notification service.
// Uses slog for backwards compatibility with existing notification code.
var (
	notificationLogger     *slog.Logger
	notificationLogCloser  func() error
	notificationLevelVar   = new(slog.LevelVar)
	notificationLoggerOnce sync.Once

	// levelVar is an alias for notificationLevelVar for test compatibility
	levelVar = notificationLevelVar
)

// getFileLogger returns the notification logger instance.
// The debug parameter controls the log level (debug vs info).
// Returns a singleton slog.Logger instance.
func getFileLogger(debug bool) *slog.Logger {
	notificationLoggerOnce.Do(func() {
		if debug {
			notificationLevelVar.Set(slog.LevelDebug)
		} else {
			notificationLevelVar.Set(slog.LevelInfo)
		}

		// Create a text handler that writes to stdout
		// This integrates with the console output while maintaining
		// the slog interface expected by notification code
		handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
			Level: notificationLevelVar,
		})

		notificationLogger = slog.New(handler).With("module", "notification")
		notificationLogCloser = func() error { return nil } // No-op since we use stdout
	})

	return notificationLogger
}

// CloseLogger closes the notification logger.
// This is a no-op since the notification logger writes to stdout.
func CloseLogger() error {
	if notificationLogCloser != nil {
		return notificationLogCloser()
	}
	return nil
}

// SetLogLevel dynamically changes the logging level for the notification logger.
func SetLogLevel(level slog.Level) {
	notificationLevelVar.Set(level)
}

// SetDebugLevel sets the logging level based on debug mode.
// This is provided for backwards compatibility.
func SetDebugLevel(debug bool) {
	if notificationLevelVar == nil {
		return
	}
	if debug {
		notificationLevelVar.Set(slog.LevelDebug)
	} else {
		notificationLevelVar.Set(slog.LevelInfo)
	}
}

// discardLogger returns a logger that discards all output.
// Useful for testing.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewJSONHandler(io.Discard, nil))
}
