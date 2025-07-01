// Package telemetry - RECOMMENDATION for async telemetry integration
//
// This file demonstrates how to implement a TelemetryWorker that would make
// telemetry reporting truly asynchronous via the event bus, similar to how
// the notification system currently works.

package telemetry

import (
	"context"
	"log/slog"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TelemetryWorker processes error events asynchronously for telemetry reporting
// This is a RECOMMENDATION - not currently implemented
type TelemetryWorker struct {
	settings       *conf.Settings
	logger         *slog.Logger
	rateLimiter    *RateLimiter
	circuitBreaker *CircuitBreaker
}

// NewTelemetryWorker creates a new telemetry worker for event bus integration
func NewTelemetryWorker(settings *conf.Settings) *TelemetryWorker {
	return &TelemetryWorker{
		settings:       settings,
		logger:         slog.Default().With("component", "telemetry-worker"),
		rateLimiter:    NewRateLimiter(1*time.Minute, 1000), // 1000 events per minute
		circuitBreaker: NewCircuitBreaker(10, 5*time.Minute),
	}
}

// ProcessError implements the EventConsumer interface for async telemetry
func (w *TelemetryWorker) ProcessError(ctx context.Context, err error) {
	// Fast path: check if telemetry is enabled
	if !IsTelemetryEnabled() {
		return
	}

	// Check circuit breaker
	if !w.circuitBreaker.CanProceed() {
		w.logger.Debug("telemetry circuit breaker open, skipping error")
		return
	}

	// Rate limiting
	errorKey := "global" // Could use error category/component for per-type limiting
	if !w.rateLimiter.Allow(errorKey) {
		w.logger.Debug("telemetry rate limit exceeded")
		return
	}

	// Extract component from error
	component := "unknown"
	var enhancedErr *errors.EnhancedError
	if errors.As(err, &enhancedErr) {
		component = enhancedErr.GetComponent()
	}

	// Report to telemetry (this can now be synchronous since we're already async)
	startTime := time.Now()
	CaptureError(err, component)
	duration := time.Since(startTime)

	// Track performance
	if duration > 100*time.Millisecond {
		w.logger.Warn("slow telemetry capture",
			"duration", duration,
			"component", component)
		w.circuitBreaker.RecordFailure()
	} else {
		w.circuitBreaker.RecordSuccess()
	}
}

// String returns a string representation of the worker
func (w *TelemetryWorker) String() string {
	return "TelemetryWorker"
}

// RateLimiter provides simple rate limiting (simplified for demo)
type RateLimiter struct {
	window    time.Duration
	maxEvents int
	events    map[string][]time.Time
}

func NewRateLimiter(window time.Duration, maxEvents int) *RateLimiter {
	return &RateLimiter{
		window:    window,
		maxEvents: maxEvents,
		events:    make(map[string][]time.Time),
	}
}

func (r *RateLimiter) Allow(key string) bool {
	now := time.Now()
	cutoff := now.Add(-r.window)

	// Clean old events
	events := r.events[key]
	validEvents := make([]time.Time, 0, len(events))
	for _, t := range events {
		if t.After(cutoff) {
			validEvents = append(validEvents, t)
		}
	}

	// Check limit
	if len(validEvents) >= r.maxEvents {
		return false
	}

	// Record event
	r.events[key] = append(validEvents, now)
	return true
}

// CircuitBreaker provides circuit breaker pattern (simplified for demo)
type CircuitBreaker struct {
	failureThreshold int
	resetTimeout     time.Duration
	failures         int
	lastFailureTime  time.Time
	state            string // "closed", "open", "half-open"
}

func NewCircuitBreaker(threshold int, timeout time.Duration) *CircuitBreaker {
	return &CircuitBreaker{
		failureThreshold: threshold,
		resetTimeout:     timeout,
		state:            "closed",
	}
}

func (cb *CircuitBreaker) CanProceed() bool {
	if cb.state == "closed" {
		return true
	}

	// Check if we should transition from open to half-open
	if cb.state == "open" && time.Since(cb.lastFailureTime) > cb.resetTimeout {
		cb.state = "half-open"
		cb.failures = 0
	}

	return cb.state != "open"
}

func (cb *CircuitBreaker) RecordSuccess() {
	if cb.state == "half-open" {
		cb.state = "closed"
		cb.failures = 0
	}
}

func (cb *CircuitBreaker) RecordFailure() {
	cb.failures++
	cb.lastFailureTime = time.Now()

	if cb.failures >= cb.failureThreshold {
		cb.state = "open"
	}
}

// Example of how to register with event bus (in main initialization):
//
// func InitializeTelemetryWorker(eventBus *events.EventBus, settings *conf.Settings) {
//     if settings.Sentry.Enabled {
//         worker := NewTelemetryWorker(settings)
//         eventBus.RegisterConsumer("telemetry", worker)
//         log.Println("Telemetry worker registered with event bus")
//     }
// }
//
// Then remove the direct telemetry call from errors.Build() to make it fully async.