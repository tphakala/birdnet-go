// Package backup provides backup functionality for BirdNET-Go.
package backup

import "github.com/tphakala/birdnet-go/internal/logger"

// Package-level cached logger instance for efficiency.
// Used for logging in helper functions that don't have access to injected loggers.
var log = logger.Global().Module("backup")

// Field constructors re-exported for use in this package.
// This avoids import shadowing issues with function parameters named "logger".
var (
	logString = logger.String
	logError  = logger.Error
	logInt    = logger.Int
	logBool   = logger.Bool
)
