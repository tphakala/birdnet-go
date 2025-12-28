// Package observability provides Prometheus metrics functionality for monitoring the BirdNET-Go application.
package observability

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the observability package logger scoped to the telemetry module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("telemetry")
}
