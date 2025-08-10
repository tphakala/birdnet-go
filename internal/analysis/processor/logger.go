// Package processor provides documentation for the processor package logging
package processor

import (
	"log/slog"
	"sync"
	
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Service name constant for the processor package
const serviceName = "analysis.processor"

// Note: The processor package has dual logging configuration:
// 
// 1. Primary logger (defined in new_species_tracker.go):
//   - Service name: "species-tracking"  
//   - Log file: "logs/species-tracking.log"
//   - Level: Dynamic (configurable via serviceLevelVar)
//   - Purpose: Species-specific operations and tracking
//
// 2. Fallback logger (used when primary logger fails):
//   - Service name: "analysis.processor" (matches package function)
//   - Source: logging.ForService(serviceName) with ultimate fallback to slog.Default()
//   - Purpose: General processor operations when primary logger unavailable
//
// The primary logger is available throughout the processor package as the 
// package-level variable `logger`. GetLogger() returns the primary logger
// if available, otherwise returns the fallback logger.
//
// Usage example:
//   logger.Info("Processing detection", "species", species, "confidence", conf)
//   logger.Debug("Worker started", "worker_id", id, "total", total)
//   logger.Error("Operation failed", "error", err)

var (
	fallbackLogger     *slog.Logger
	fallbackLoggerOnce sync.Once
)

// GetLogger returns the processor package logger
// This provides a uniform API for accessing the logger across packages
func GetLogger() *slog.Logger {
	// Return the logger from new_species_tracker.go if available
	if logger != nil {
		return logger
	}
	
	// Initialize fallback logger only once if main logger is nil
	fallbackLoggerOnce.Do(func() {
		fallbackLogger = logging.ForService(serviceName)
		// Ensure fallbackLogger is never nil to prevent panics
		if fallbackLogger == nil {
			fallbackLogger = slog.Default().With("service", serviceName)
		}
	})
	
	return fallbackLogger
}