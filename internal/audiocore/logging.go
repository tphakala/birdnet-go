package audiocore

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the audiocore module logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("audiocore")
}
