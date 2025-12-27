// Package conf provides configuration management for BirdNET-Go.
package conf

import "github.com/tphakala/birdnet-go/internal/logger"

// Package-level cached logger instance for efficiency.
// All logging in this package should use this variable.
var log = logger.Global().Module("config")
