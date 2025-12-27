// Package birdnet provides logging for the BirdNET package.
package birdnet

import (
	"sync"

	"github.com/tphakala/birdnet-go/internal/logger"
)

var (
	serviceLogger logger.Logger
	initOnce      sync.Once
)

// GetLogger returns the birdnet package logger scoped to the birdnet module.
// Uses sync.Once to ensure the logger is only initialized once.
func GetLogger() logger.Logger {
	initOnce.Do(func() {
		serviceLogger = logger.Global().Module("birdnet")
	})
	return serviceLogger
}
