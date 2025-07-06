package audiocore

// Buffer and pool constants
const (
	// CircularBufferExtraCapacity is the multiplier for extra buffer capacity (10% extra)
	CircularBufferExtraCapacity = 1.1
	
	// DefaultWorkerCount is the default number of worker goroutines for analyzer wrapper
	DefaultWorkerCount = 5
	
	// DefaultTimingBufferSize is the default size for timing history in analyzer wrapper
	DefaultTimingBufferSize = 1000
	
	// DefaultAnalyzerTimeout is the default timeout for analyzer operations
	DefaultAnalyzerTimeout = 30 // seconds
	
	// DefaultCircuitBreakerMaxFailures is the default max failures before circuit opens
	DefaultCircuitBreakerMaxFailures = 5
	
	// DefaultCircuitBreakerResetTimeout is the default reset timeout for circuit breaker
	DefaultCircuitBreakerResetTimeout = 30 // seconds
	
	// DefaultCircuitBreakerHalfOpenRequests is the default requests allowed in half-open state
	DefaultCircuitBreakerHalfOpenRequests = 2
	
	// DefaultCircuitBreakerRecoverySteps is the default number of recovery steps
	DefaultCircuitBreakerRecoverySteps = 3
)

// Circuit breaker failure type constants
const (
	// CircuitBreakerFailureTypeTimeout represents a timeout failure
	CircuitBreakerFailureTypeTimeout = "timeout"
	
	// CircuitBreakerFailureTypeError represents a general error failure
	CircuitBreakerFailureTypeError = "error"
	
	// CircuitBreakerFailureTypePanic represents a panic failure
	CircuitBreakerFailureTypePanic = "panic"
)

// CircuitBreakerState represents the state of a circuit breaker
type CircuitBreakerState int

const (
	// CircuitBreakerStateClosed allows all requests through
	CircuitBreakerStateClosed CircuitBreakerState = iota
	
	// CircuitBreakerStateOpen rejects all requests
	CircuitBreakerStateOpen
	
	// CircuitBreakerStateHalfOpen allows limited requests for testing recovery
	CircuitBreakerStateHalfOpen
)

// String returns the string representation of the circuit breaker state
func (s CircuitBreakerState) String() string {
	switch s {
	case CircuitBreakerStateClosed:
		return "closed"
	case CircuitBreakerStateOpen:
		return "open"
	case CircuitBreakerStateHalfOpen:
		return "half-open"
	default:
		return "unknown"
	}
}