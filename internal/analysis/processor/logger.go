// Package processor provides logging for the processor package
package processor

import (
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the processor package logger scoped to the analysis.processor module.
// This is used for package-level functions that don't have access to a Processor instance.
// Processor methods should use p.log instead for the logger passed during construction.
// Fetched dynamically to ensure it uses the current centralized logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("analysis.processor")
}
