// Package conf provides configuration management for BirdNET-Go.
package conf

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the config package logger scoped to the config module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("config")
}
