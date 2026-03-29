// Package classifier provides logging for the classifier package.
package classifier

import (
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the classifier package logger scoped to the birdnet module.
// The logger is fetched from the global logger each time to ensure it uses
// the current centralized logger (which may be set after package init).
func GetLogger() logger.Logger {
	return logger.Global().Module("birdnet")
}
