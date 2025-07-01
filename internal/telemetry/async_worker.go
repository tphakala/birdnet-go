package telemetry

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// AsyncWorker implements EventConsumer to process error events asynchronously
type AsyncWorker struct {
	settings       *conf.Settings
	config         *AsyncWorkerConfig
	rateLimiter    *AsyncRateLimiter
	circuitBreaker *AsyncCircuitBreaker
	
	// Metrics
	eventsProcessed atomic.Uint64
	eventsDropped   atomic.Uint64
	eventsFailed    atomic.Uint64
	
	logger *slog.Logger
}

// AsyncWorkerConfig holds configuration for the telemetry worker
type AsyncWorkerConfig struct {
	// RateLimit settings
	RateLimitWindow   time.Duration
	RateLimitEvents   int
	
	// CircuitBreaker settings
	FailureThreshold  int
	RecoveryTimeout   time.Duration
	HalfOpenMaxEvents int
	
	// Performance settings
	SlowThreshold     time.Duration // Log warning if telemetry takes longer than this
}

// DefaultAsyncWorkerConfig returns default configuration
func DefaultAsyncWorkerConfig() *AsyncWorkerConfig {
	return &AsyncWorkerConfig{
		RateLimitWindow:   1 * time.Minute,
		RateLimitEvents:   1000, // 1000 events per minute
		FailureThreshold:  10,
		RecoveryTimeout:   5 * time.Minute,
		HalfOpenMaxEvents: 3,
		SlowThreshold:     100 * time.Millisecond,
	}
}

// NewAsyncWorker creates a new telemetry worker for event bus integration
func NewAsyncWorker(settings *conf.Settings, config *AsyncWorkerConfig) (*AsyncWorker, error) {
	if settings == nil {
		return nil, fmt.Errorf("settings is required")
	}
	
	if config == nil {
		config = DefaultAsyncWorkerConfig()
	}
	
	workerLogger := logging.ForService("telemetry-async-worker")
	if workerLogger == nil {
		workerLogger = slog.Default().With("service", "telemetry-async-worker")
	}
	
	worker := &AsyncWorker{
		settings:       settings,
		config:         config,
		rateLimiter:    NewAsyncRateLimiter(config.RateLimitWindow, config.RateLimitEvents),
		circuitBreaker: NewAsyncCircuitBreaker(config.FailureThreshold, config.RecoveryTimeout),
		logger:         workerLogger,
	}
	
	return worker, nil
}

// Name returns the consumer name for identification
func (w *AsyncWorker) Name() string {
	return "TelemetryAsyncWorker"
}

// ProcessEvent processes a single error event
func (w *AsyncWorker) ProcessEvent(event events.ErrorEvent) error {
	// Fast path: check if telemetry is enabled
	if !IsTelemetryEnabled() {
		w.eventsDropped.Add(1)
		return nil
	}
	
	// Check circuit breaker
	if !w.circuitBreaker.CanProceed() {
		w.logger.Debug("telemetry circuit breaker open, dropping event")
		w.eventsDropped.Add(1)
		return nil
	}
	
	// Rate limiting - use component as key for per-component limiting
	component := event.GetComponent()
	if !w.rateLimiter.Allow(component) {
		w.logger.Debug("telemetry rate limit exceeded for component",
			"component", component)
		w.eventsDropped.Add(1)
		return nil
	}
	
	// Process the event
	startTime := time.Now()
	err := w.processEventInternal(event)
	duration := time.Since(startTime)
	
	// Track performance and circuit breaker state
	if err != nil {
		w.eventsFailed.Add(1)
		w.circuitBreaker.RecordFailure()
		w.logger.Error("failed to report error to telemetry",
			"error", err,
			"component", component,
			"duration", duration)
		return err
	}
	
	// Check if telemetry was slow
	if duration > w.config.SlowThreshold {
		w.logger.Warn("slow telemetry capture",
			"duration", duration,
			"component", component,
			"threshold", w.config.SlowThreshold)
		// Consider slow operations as failures for circuit breaker
		w.circuitBreaker.RecordFailure()
	} else {
		w.circuitBreaker.RecordSuccess()
	}
	
	w.eventsProcessed.Add(1)
	return nil
}

// processEventInternal handles the actual telemetry reporting
func (w *AsyncWorker) processEventInternal(event events.ErrorEvent) error {
	// Extract the error
	err := event.GetError()
	if err == nil {
		return fmt.Errorf("event has no error")
	}
	
	// Get component from event
	component := event.GetComponent()
	
	// Report to telemetry (this is now safe to be synchronous since we're already async)
	CaptureError(err, component)
	
	// Mark event as reported to prevent duplicate reporting
	event.MarkReported()
	
	return nil
}

// ProcessBatch processes multiple events at once
func (w *AsyncWorker) ProcessBatch(events []events.ErrorEvent) error {
	// For telemetry, we don't really benefit from batching
	// Process each event individually
	var lastErr error
	for _, event := range events {
		if err := w.ProcessEvent(event); err != nil {
			lastErr = err
		}
	}
	return lastErr
}

// SupportsBatching returns false as telemetry doesn't benefit from batching
func (w *AsyncWorker) SupportsBatching() bool {
	return false
}

// GetStats returns worker statistics
func (w *AsyncWorker) GetStats() AsyncWorkerStats {
	return AsyncWorkerStats{
		EventsProcessed:    w.eventsProcessed.Load(),
		EventsDropped:      w.eventsDropped.Load(),
		EventsFailed:       w.eventsFailed.Load(),
		CircuitBreakerOpen: w.circuitBreaker.IsOpen(),
	}
}

// AsyncWorkerStats contains runtime statistics for monitoring
type AsyncWorkerStats struct {
	EventsProcessed    uint64
	EventsDropped      uint64
	EventsFailed       uint64
	CircuitBreakerOpen bool
}

// AsyncRateLimiter provides simple rate limiting
type AsyncRateLimiter struct {
	mu        sync.Mutex
	window    time.Duration
	maxEvents int
	events    map[string][]time.Time
}

// NewAsyncRateLimiter creates a new rate limiter
func NewAsyncRateLimiter(window time.Duration, maxEvents int) *AsyncRateLimiter {
	return &AsyncRateLimiter{
		window:    window,
		maxEvents: maxEvents,
		events:    make(map[string][]time.Time),
	}
}

// Allow checks if an event is allowed based on rate limits
func (r *AsyncRateLimiter) Allow(key string) bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	
	now := time.Now()
	cutoff := now.Add(-r.window)
	
	// Get existing events for this key
	eventTimes := r.events[key]
	
	// Remove old events outside the window
	validEvents := make([]time.Time, 0, len(eventTimes))
	for _, t := range eventTimes {
		if t.After(cutoff) {
			validEvents = append(validEvents, t)
		}
	}
	
	// Check if we're at the limit
	if len(validEvents) >= r.maxEvents {
		r.events[key] = validEvents
		return false
	}
	
	// Add the new event
	validEvents = append(validEvents, now)
	r.events[key] = validEvents
	
	// Periodic cleanup to prevent memory growth
	if len(r.events) > 1000 {
		r.cleanup()
	}
	
	return true
}

// cleanup removes old entries to prevent memory growth
func (r *AsyncRateLimiter) cleanup() {
	cutoff := time.Now().Add(-r.window)
	for key, times := range r.events {
		// Remove keys with no recent events
		hasRecent := false
		for _, t := range times {
			if t.After(cutoff) {
				hasRecent = true
				break
			}
		}
		if !hasRecent {
			delete(r.events, key)
		}
	}
}

// AsyncCircuitBreaker implements the circuit breaker pattern
type AsyncCircuitBreaker struct {
	mu               sync.Mutex
	state            string // "closed", "open", "half-open"
	failures         int
	lastFailureTime  time.Time
	successCount     int
	failureThreshold int
	recoveryTimeout  time.Duration
}

// NewAsyncCircuitBreaker creates a new circuit breaker
func NewAsyncCircuitBreaker(failureThreshold int, recoveryTimeout time.Duration) *AsyncCircuitBreaker {
	return &AsyncCircuitBreaker{
		state:            "closed",
		failureThreshold: failureThreshold,
		recoveryTimeout:  recoveryTimeout,
	}
}

// CanProceed checks if requests can proceed through the circuit breaker
func (cb *AsyncCircuitBreaker) CanProceed() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	switch cb.state {
	case "closed":
		return true
	case "open":
		// Check if recovery timeout has passed
		if time.Since(cb.lastFailureTime) > cb.recoveryTimeout {
			cb.state = "half-open"
			cb.successCount = 0
			return true
		}
		return false
	case "half-open":
		// Allow limited requests in half-open state
		return cb.successCount < 3
	default:
		return true
	}
}

// RecordSuccess records a successful operation
func (cb *AsyncCircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	switch cb.state {
	case "half-open":
		cb.successCount++
		if cb.successCount >= 3 {
			// Enough successes, close the circuit
			cb.state = "closed"
			cb.failures = 0
		}
	case "closed":
		// Reset failure count on success
		if cb.failures > 0 {
			cb.failures = 0
		}
	}
}

// RecordFailure records a failed operation
func (cb *AsyncCircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.lastFailureTime = time.Now()
	
	switch cb.state {
	case "closed":
		cb.failures++
		if cb.failures >= cb.failureThreshold {
			cb.state = "open"
		}
	case "half-open":
		// Single failure in half-open state reopens the circuit
		cb.state = "open"
		cb.failures = cb.failureThreshold
	}
}

// IsOpen returns true if the circuit breaker is open
func (cb *AsyncCircuitBreaker) IsOpen() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state == "open"
}