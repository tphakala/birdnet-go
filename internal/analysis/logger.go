// Package analysis provides structured logging for the analysis package
package analysis

import (
	"io"
	"log"
	"log/slog"
	"path/filepath"
	"sync"
	
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger for analysis operations
var (
	logger         *slog.Logger
	loggerInitOnce sync.Once
	levelVar       = new(slog.LevelVar) // Dynamic level control
	closeLogger    func() error
)

func init() {
	var err error
	// Define log file path relative to working directory
	logFilePath := filepath.Join("logs", "analysis.log")
	initialLevel := slog.LevelInfo // Default to Info level
	levelVar.Set(initialLevel)
	
	// Initialize the service-specific file logger
	logger, closeLogger, err = logging.NewFileLogger(logFilePath, "analysis", levelVar)
	if err != nil {
		// Fallback: Log error to standard log and use console logging
		log.Printf("Failed to initialize analysis file logger at %s: %v. Using console logging.", logFilePath, err)
		// Set logger to a disabled handler to prevent nil panics, but respects level var
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: levelVar})
		logger = slog.New(fbHandler).With("service", "analysis")
		closeLogger = func() error { return nil } // No-op closer
	}
}

// GetLogger returns the package logger for use in subpackages
// This allows other analysis subpackages to use the same logger
// if they don't need their own dedicated logger. Thread-safe initialization
// is guaranteed through sync.Once.
func GetLogger() *slog.Logger {
	loggerInitOnce.Do(func() {
		if logger == nil {
			logger = slog.Default().With("service", "analysis")
		}
	})
	return logger
}

// CloseLogger closes the log file and releases resources
func CloseLogger() error {
	if closeLogger != nil {
		return closeLogger()
	}
	return nil
}