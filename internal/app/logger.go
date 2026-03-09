package app

import "github.com/tphakala/birdnet-go/internal/logger"

// getLogger returns the app package logger scoped to the app module.
// Fetched dynamically to ensure it uses the current centralized logger.
func getLogger() logger.Logger {
	return logger.Global().Module("app")
}
