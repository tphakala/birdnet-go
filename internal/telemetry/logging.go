package telemetry

import (
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the telemetry module logger.
// This is the primary logger interface for all telemetry code.
func GetLogger() logger.Logger {
	return logger.Global().Module("telemetry")
}

// CloseServiceLogger closes the telemetry service logger.
// This is a no-op since the logger writes to stdout.
func CloseServiceLogger() error {
	return nil
}
