// Package processor provides logging for the processor package
package processor

import (
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the processor package logger scoped to the birdnet module
func GetLogger() logger.Logger {
	return logger.Global().Module("birdnet")
}
