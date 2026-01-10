// Package spectrogram provides logging utilities for spectrogram generation.
package spectrogram

import (
	"github.com/tphakala/birdnet-go/internal/logger"
)

// GetLogger returns the spectrogram package logger scoped to the spectrogram module.
// Fetched dynamically to ensure it uses the current centralized logger.
func GetLogger() logger.Logger {
	return logger.Global().Module("spectrogram")
}

// GetPreRendererLogger returns the logger for the pre-renderer subsystem.
func GetPreRendererLogger() logger.Logger {
	return logger.Global().Module("spectrogram.prerenderer")
}
