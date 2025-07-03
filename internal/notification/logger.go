package notification

import (
	"log/slog"
	"sync"

	"github.com/tphakala/birdnet-go/internal/logging"
)

var (
	// fileLogger is the dedicated file logger for notifications
	fileLogger *slog.Logger
	// levelVar allows dynamic log level adjustment
	levelVar   *slog.LevelVar
	// loggerCloser stores the cleanup function for the log file
	loggerCloser func() error
	loggerOnce sync.Once
)

// initFileLogger initializes the dedicated file logger for notifications
func initFileLogger(debug bool) {
	loggerOnce.Do(func() {
		// Create level var for dynamic log level adjustment
		levelVar = new(slog.LevelVar)
		if debug {
			levelVar.Set(slog.LevelDebug)
		} else {
			levelVar.Set(slog.LevelInfo)
		}

		// Create file logger with service-specific attributes
		logger, closer, err := logging.NewFileLogger("logs/notifications.log", "notifications", levelVar)
		if err != nil || logger == nil {
			// Fallback to default logger if file logger creation fails
			fileLogger = slog.Default().With("service", "notifications")
			return
		}
		
		fileLogger = logger
		loggerCloser = closer
	})
}

// getFileLogger returns the file logger, initializing it if necessary
func getFileLogger(debug bool) *slog.Logger {
	if fileLogger == nil {
		initFileLogger(debug)
	}
	return fileLogger
}

// SetDebugLevel updates the log level for the file logger
func SetDebugLevel(debug bool) {
	if levelVar != nil {
		if debug {
			levelVar.Set(slog.LevelDebug)
		} else {
			levelVar.Set(slog.LevelInfo)
		}
	}
}

// CloseLogger closes the log file and cleans up resources
func CloseLogger() error {
	if loggerCloser != nil {
		return loggerCloser()
	}
	return nil
}