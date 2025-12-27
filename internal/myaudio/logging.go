package myaudio

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the myaudio logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("audio")
}
