// Package backup provides backup functionality for BirdNET-Go.
package backup

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the backup package logger scoped to the backup module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("backup")
}

// Field constructors re-exported for use in this package.
// This avoids import shadowing issues with function parameters named "logger".
var (
	logString = logger.String
	logError  = logger.Error
	logInt    = logger.Int
	logBool   = logger.Bool
)
