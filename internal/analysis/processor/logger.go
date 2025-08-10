// Package processor provides documentation for the processor package logging
package processor

import (
	"log/slog"
)

// Note: The processor package uses a logger defined in new_species_tracker.go
// 
// This logger is initialized as:
//   - Service name: "species-tracking"  
//   - Log file: "logs/species-tracking.log"
//   - Level: Dynamic (configurable via serviceLevelVar)
//   - Fallback: slog.Default() if file logger fails
//
// The logger is available throughout the processor package as the 
// package-level variable `logger`.
//
// Usage example:
//   logger.Info("Processing detection", "species", species, "confidence", conf)
//   logger.Debug("Worker started", "worker_id", id, "total", total)
//   logger.Error("Operation failed", "error", err)

// GetLogger returns the processor package logger
// This provides a uniform API for accessing the logger across packages
func GetLogger() *slog.Logger {
	// Return the logger from new_species_tracker.go
	// Note: this assumes the logger is already initialized
	return logger
}