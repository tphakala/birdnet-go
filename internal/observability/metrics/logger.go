// Package metrics provides Prometheus metrics for observability.
package metrics

import "github.com/tphakala/birdnet-go/internal/logger"

// Package-level cached logger instance for efficiency.
// All logging in this package should use this variable.
var log = logger.Global().Module("telemetry")
