// Package processor provides logging for the processor package
package processor

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	serviceLogger logger.Logger
	initOnce      sync.Once
)

// GetLogger returns the processor package logger scoped to the analysis.processor module.
// This is used for package-level functions that don't have access to a Processor instance.
// Processor methods should use p.log instead for the logger passed during construction.
func GetLogger() logger.Logger {
	initOnce.Do(func() {
		serviceLogger = logger.Global().Module("analysis.processor")
	})
	return serviceLogger
}
