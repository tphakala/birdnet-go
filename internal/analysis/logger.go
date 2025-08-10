// Package analysis provides structured logging for the analysis package
package analysis

import (
	"log/slog"
	"sync"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// Package-level logger for analysis operations
var (
	logger         *slog.Logger
	loggerInitOnce sync.Once
)

func init() {
	// Create service-specific logger for analysis package
	logger = logging.ForService("analysis")
	
	// Defensive initialization for early startup scenarios
	// This ensures we always have a working logger even if
	// the logging system isn't fully initialized yet
	if logger == nil {
		logger = slog.Default().With("service", "analysis")
	}
}

// GetLogger returns the package logger for use in subpackages
// This allows other analysis subpackages to use the same logger
// if they don't need their own dedicated logger. Thread-safe initialization
// is guaranteed through sync.Once.
func GetLogger() *slog.Logger {
	loggerInitOnce.Do(func() {
		if logger == nil {
			logger = logging.ForService("analysis")
			if logger == nil {
				logger = slog.Default().With("service", "analysis")
			}
		}
	})
	return logger
}