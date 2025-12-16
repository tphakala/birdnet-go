// Package notification provides shared constants for the notification system.
package notification

// Circuit breaker state string representations.
// Used by both PushCircuitBreaker (circuit_breaker.go) and simpleCircuitBreaker (worker.go).
const (
	circuitStateClosed   = "closed"
	circuitStateHalfOpen = "half-open"
	circuitStateOpen     = "open"
	circuitStateUnknown  = "unknown"
)
