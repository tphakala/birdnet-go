// Package observability provides Prometheus metrics functionality for monitoring the BirdNET-Go application.
package observability

import "github.com/tphakala/birdnet-go/internal/logger"

// Package-level cached logger instance for efficiency.
// All logging in this package should use this variable.
var log = logger.Global().Module("telemetry")
