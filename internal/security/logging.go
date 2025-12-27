package security

import (
	"github.com/tphakala/birdnet-go/internal/logger"
)

// SecurityLogger is a type alias for logger.Logger to maintain backwards compatibility.
// This allows passing contextual loggers (e.g., with request-specific fields) to helper functions.
type SecurityLogger = logger.Logger

// GetLogger returns a logger scoped to the security module.
// Use this to obtain a logger instance for security-related operations.
func GetLogger() logger.Logger {
	return logger.Global().Module("security")
}

// CloseLogger is a no-op for backwards compatibility.
// The centralized logger handles cleanup automatically.
func CloseLogger() error {
	return nil
}
