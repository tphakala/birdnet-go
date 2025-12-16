package notification

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Telemetry debouncing configuration
const (
	// MinTelemetryReportInterval prevents telemetry spam during rapid state changes.
	// State transitions closer than this interval will be logged but not reported.
	MinTelemetryReportInterval = 30 * time.Second
)

// CircuitState represents the state of a circuit breaker.
type CircuitState int

const (
	// StateClosed means the circuit is closed and requests are flowing normally.
	StateClosed CircuitState = iota
	// StateHalfOpen means the circuit is testing if the service has recovered.
	StateHalfOpen
	// StateOpen means the circuit is open and requests are being rejected.
	StateOpen
)

// String returns the string representation of CircuitState.
// Uses constants from constants.go for consistent state names.
func (s CircuitState) String() string {
	switch s {
	case StateClosed:
		return circuitStateClosed
	case StateHalfOpen:
		return circuitStateHalfOpen
	case StateOpen:
		return circuitStateOpen
	default:
		return circuitStateUnknown
	}
}

var (
	// ErrCircuitBreakerOpen is returned when the circuit breaker is open.
	ErrCircuitBreakerOpen = errors.Newf("circuit breaker is open").
				Component("notification").
				Category(errors.CategoryLimit).
				Build()
	// ErrTooManyRequests is returned when the circuit breaker is half-open and has already allowed a test request.
	ErrTooManyRequests = errors.Newf("circuit breaker is half-open, too many requests").
				Component("notification").
				Category(errors.CategoryLimit).
				Build()
)

// CircuitBreakerConfig holds configuration for a circuit breaker.
type CircuitBreakerConfig struct {
	// MaxFailures is the number of consecutive failures before opening the circuit.
	MaxFailures int
	// Timeout is how long to wait before transitioning from Open to Half-Open.
	Timeout time.Duration
	// HalfOpenMaxRequests is the maximum number of requests allowed in half-open state.
	HalfOpenMaxRequests int
}

// DefaultCircuitBreakerConfig returns default circuit breaker configuration.
func DefaultCircuitBreakerConfig() CircuitBreakerConfig {
	return CircuitBreakerConfig{
		// MaxFailures: 5 provides quick failure detection without being overly sensitive
		// to transient network issues. Most services recover within 5 attempts.
		MaxFailures: 5,
		// Timeout: 30s balances recovery testing with API protection:
		// - Long enough for temporary network issues to resolve
		// - Short enough to detect actual recovery promptly
		// - Matches typical API timeout values (most APIs timeout at 20-60s)
		Timeout: 30 * time.Second,
		// HalfOpenMaxRequests: 1 ensures conservative recovery testing
		// Only one request is allowed to test if the service has recovered
		HalfOpenMaxRequests: 1,
	}
}

// Validate checks if the circuit breaker configuration is valid.
func (c CircuitBreakerConfig) Validate() error {
	if c.MaxFailures < 1 {
		return fmt.Errorf("max_failures must be at least 1, got %d", c.MaxFailures)
	}
	if c.Timeout < time.Second {
		return fmt.Errorf("timeout must be at least 1 second, got %v", c.Timeout)
	}
	if c.HalfOpenMaxRequests < 1 {
		return fmt.Errorf("half_open_max_requests must be at least 1, got %d", c.HalfOpenMaxRequests)
	}
	return nil
}

// CircuitBreaker implements the circuit breaker pattern for push notification providers.
// It tracks failures and opens the circuit after a threshold is reached, preventing
// requests to failing providers and allowing them time to recover.
type PushCircuitBreaker struct {
	config              CircuitBreakerConfig
	state               CircuitState
	failures            int
	lastFailureTime     time.Time
	lastStateChange     time.Time
	lastTelemetryReport time.Time // Prevents spam during rapid state changes
	halfOpenRequests    int
	mu                  sync.RWMutex
	metrics             *metrics.NotificationMetrics
	providerName        string
	telemetry           *NotificationTelemetry
}

// NewPushCircuitBreaker creates a new PushCircuitBreaker with the given configuration.
// If the configuration is invalid, it logs a warning but still uses the provided config
// (to allow testing with short timeouts). Production configs should pass validation.
func NewPushCircuitBreaker(config CircuitBreakerConfig, notificationMetrics *metrics.NotificationMetrics, providerName string) *PushCircuitBreaker {
	// Validate configuration and warn if invalid (but don't override for test flexibility)
	if err := config.Validate(); err != nil {
		slog.Warn("Circuit breaker config validation failed",
			"provider", providerName,
			"error", err,
			"action", "proceeding with provided config")
	}

	cb := &PushCircuitBreaker{
		config:          config,
		state:           StateClosed,
		lastStateChange: time.Now(),
		metrics:         notificationMetrics,
		providerName:    providerName,
	}

	if cb.metrics != nil {
		cb.metrics.UpdateCircuitBreakerState(providerName, int(StateClosed))
		cb.metrics.UpdateHealthStatus(providerName, true)
	}

	return cb
}

// Call executes the given function if the circuit breaker allows it.
// It tracks success/failure and manages state transitions.
func (cb *PushCircuitBreaker) Call(ctx context.Context, fn func(context.Context) error) error {
	// Check if we can proceed
	if err := cb.beforeCall(); err != nil {
		// Capture state and failures under lock for thread-safe error message
		state, failures := cb.State(), cb.Failures()
		// Add context about circuit breaker state to help debugging
		return fmt.Errorf("circuit breaker rejected request (%v, %d consecutive failures): %w",
			state, failures, err)
	}

	// Execute the function
	err := fn(ctx)

	// Record the result
	cb.afterCall(err)

	return err
}

// beforeCall checks if the circuit breaker allows the call.
func (cb *PushCircuitBreaker) beforeCall() error {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	switch cb.state {
	case StateClosed:
		return nil

	case StateOpen:
		// Check if enough time has passed to try half-open
		if time.Since(cb.lastStateChange) >= cb.config.Timeout {
			cb.setState(StateHalfOpen)
			cb.halfOpenRequests = 1 // Count this transition call as the first request
			return nil
		}
		return ErrCircuitBreakerOpen

	case StateHalfOpen:
		// Allow limited requests in half-open state
		if cb.halfOpenRequests >= cb.config.HalfOpenMaxRequests {
			return ErrTooManyRequests
		}
		cb.halfOpenRequests++
		return nil

	default:
		return ErrCircuitBreakerOpen
	}
}

// afterCall records the result of a call and updates state.
func (cb *PushCircuitBreaker) afterCall(err error) {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	if err == nil {
		// Success - handle based on current state
		cb.onSuccess()
		return
	}

	// Don't count client-side cancellation as provider failure
	// Circuit breaker should only open for actual provider issues
	if errors.Is(err, context.Canceled) {
		return
	}

	// Failure - handle based on current state
	cb.onFailure()
}

// onSuccess handles a successful call.
func (cb *PushCircuitBreaker) onSuccess() {
	cb.failures = 0
	cb.lastFailureTime = time.Time{}

	if cb.metrics != nil {
		cb.metrics.UpdateHealthStatus(cb.providerName, true)
	}

	if cb.state == StateHalfOpen {
		// Successful call in half-open state - close the circuit
		cb.setState(StateClosed)
	}
}

// onFailure handles a failed call.
func (cb *PushCircuitBreaker) onFailure() {
	cb.failures++
	cb.lastFailureTime = time.Now()

	if cb.metrics != nil {
		cb.metrics.IncrementConsecutiveFailures(cb.providerName)
	}

	switch cb.state {
	case StateClosed:
		// Check if we've hit the failure threshold
		if cb.failures >= cb.config.MaxFailures {
			cb.setState(StateOpen)
			if cb.metrics != nil {
				cb.metrics.UpdateHealthStatus(cb.providerName, false)
			}
		}

	case StateHalfOpen:
		// Failure in half-open state - reopen the circuit
		cb.setState(StateOpen)
		if cb.metrics != nil {
			cb.metrics.UpdateHealthStatus(cb.providerName, false)
		}

	case StateOpen:
		// Already open, no action needed
	}
}

// setState transitions the circuit breaker to a new state.
func (cb *PushCircuitBreaker) setState(newState CircuitState) {
	if cb.state == newState {
		return
	}

	oldState := cb.state
	now := time.Now()
	timeInPreviousState := now.Sub(cb.lastStateChange)

	cb.state = newState
	cb.lastStateChange = now

	if cb.metrics != nil {
		cb.metrics.UpdateCircuitBreakerState(cb.providerName, int(newState))
	}

	// Log state transitions for operational visibility
	slog.Info("Circuit breaker state transition",
		"provider", cb.providerName,
		"old_state", oldState.String(),
		"new_state", newState.String(),
		"consecutive_failures", cb.failures,
		"last_failure", cb.lastFailureTime.Format(time.RFC3339))

	// Report telemetry for state transitions (with debouncing to prevent spam)
	if cb.telemetry != nil {
		timeSinceLastReport := now.Sub(cb.lastTelemetryReport)

		// Always report critical transitions (closed → open, or any transition to closed)
		// For other transitions, debounce to prevent spam during flapping
		shouldReport := false
		switch {
		case newState == StateOpen && oldState == StateClosed:
			shouldReport = true // Critical: provider just failed
		case newState == StateClosed:
			shouldReport = true // Always report recovery
		case timeSinceLastReport >= MinTelemetryReportInterval:
			shouldReport = true // Enough time passed since last report
		}

		if shouldReport {
			cb.telemetry.CircuitBreakerStateTransition(
				cb.providerName,
				oldState,
				newState,
				cb.failures,
				timeInPreviousState,
				cb.config,
			)
			cb.lastTelemetryReport = now
		} else {
			slog.Debug("Telemetry report debounced (too soon since last report)",
				"provider", cb.providerName,
				"transition", fmt.Sprintf("%s → %s", oldState.String(), newState.String()),
				"time_since_last_report", timeSinceLastReport,
				"min_interval", MinTelemetryReportInterval)
		}
	}
}

// State returns the current state of the circuit breaker.
func (cb *PushCircuitBreaker) State() CircuitState {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state
}

// Failures returns the current number of consecutive failures.
func (cb *PushCircuitBreaker) Failures() int {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.failures
}

// Reset manually resets the circuit breaker to closed state.
func (cb *PushCircuitBreaker) Reset() {
	cb.mu.Lock()
	defer cb.mu.Unlock()

	cb.failures = 0
	cb.lastFailureTime = time.Time{}
	cb.halfOpenRequests = 0
	cb.setState(StateClosed)

	if cb.metrics != nil {
		cb.metrics.UpdateHealthStatus(cb.providerName, true)
	}
}

// IsHealthy returns true if the circuit breaker is in a healthy state (closed).
func (cb *PushCircuitBreaker) IsHealthy() bool {
	cb.mu.RLock()
	defer cb.mu.RUnlock()
	return cb.state == StateClosed
}

// SetTelemetry sets the telemetry integration for the circuit breaker.
// This allows telemetry to be injected after circuit breaker creation.
func (cb *PushCircuitBreaker) SetTelemetry(telemetry *NotificationTelemetry) {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	cb.telemetry = telemetry
}

// GetStats returns current statistics about the circuit breaker.
func (cb *PushCircuitBreaker) GetStats() CircuitBreakerStats {
	cb.mu.RLock()
	defer cb.mu.RUnlock()

	return CircuitBreakerStats{
		State:              cb.state,
		Failures:           cb.failures,
		LastFailureTime:    cb.lastFailureTime,
		LastStateChange:    cb.lastStateChange,
		HalfOpenRequests:   cb.halfOpenRequests,
	}
}

// CircuitBreakerStats contains statistics about a circuit breaker's state.
type CircuitBreakerStats struct {
	State              CircuitState
	Failures           int
	LastFailureTime    time.Time
	LastStateChange    time.Time
	HalfOpenRequests   int
}
