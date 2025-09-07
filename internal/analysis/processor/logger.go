// Package processor provides documentation for the processor package logging
package processor

import (
	"log"
	"log/slog"
	"os"
	"path/filepath"
	"sync"
	
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Service name constant for the processor package
const serviceName = "analysis.processor"

var (
	processorLogger     *slog.Logger
	processorLoggerOnce sync.Once
	processorLevelVar   = new(slog.LevelVar) // Dynamic level control
	processorCloseFunc  func() error
)

func init() {
	var err error
	// Define log file path for processor operations
	logFilePath := filepath.Join("logs", "analysis-processor.log")
	
	// Set initial level based on global debug flag
	initialLevel := slog.LevelInfo
	if conf.Setting().Debug {
		initialLevel = slog.LevelDebug
	}
	processorLevelVar.Set(initialLevel)
	
	// Initialize the processor-specific file logger
	processorLogger, processorCloseFunc, err = logging.NewFileLogger(logFilePath, serviceName, processorLevelVar)
	if err != nil {
		// Fallback: Log error to standard log and use console logging
		log.Printf("Failed to initialize processor file logger at %s: %v. Using console logging.", logFilePath, err)
		// Set logger to console handler for actual console output
		fbHandler := slog.NewJSONHandler(os.Stderr, &slog.HandlerOptions{Level: processorLevelVar})
		processorLogger = slog.New(fbHandler).With("service", serviceName)
		processorCloseFunc = func() error { return nil } // No-op closer
	}
}

// GetLogger returns the processor package logger
// This provides a uniform API for accessing the logger across packages.
// Note: The species tracker has its own dedicated logger in new_species_tracker.go
func GetLogger() *slog.Logger {
	processorLoggerOnce.Do(func() {
		if processorLogger == nil {
			processorLogger = slog.Default().With("service", serviceName)
		}
	})
	return processorLogger
}

// CloseProcessorLogger closes the processor log file and releases resources
func CloseProcessorLogger() error {
	if processorCloseFunc != nil {
		return processorCloseFunc()
	}
	return nil
}