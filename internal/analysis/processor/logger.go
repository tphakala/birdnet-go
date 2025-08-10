// Package logger provides documentation for the processor package logging
package processor

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
//
// For external access, use the processor functions directly rather
// than accessing the logger variable.