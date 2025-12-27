package notification

import "github.com/tphakala/birdnet-go/internal/logger"

// GetLogger returns the notification logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("notification")
}

// CloseLogger closes the notification logger.
// This is a no-op since the centralized logger manages resources.
func CloseLogger() error {
	return nil
}
