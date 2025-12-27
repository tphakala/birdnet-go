// Package targets provides backup target implementations.
package targets

import "github.com/tphakala/birdnet-go/internal/logger"

// Package-level cached logger instance for efficiency.
// Used for logging in helper functions that don't have access to injected loggers.
var log = logger.Global().Module("backup")
