package telemetry

import (
	"sync"
	"sync/atomic"
	"time"

	"log/slog"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// getLoggerSafe returns a logger for the service, falling back to default if logging not initialized
func getLoggerSafe(service string) *slog.Logger {
	logger := logging.ForService(service)
	if logger == nil {
		logger = slog.Default().With("service", service)
	}
	return logger
}

// TelemetryWorker implements EventConsumer to process error events and send them to Sentry
type TelemetryWorker struct {
	enabled        bool
	circuitBreaker *CircuitBreaker
	config         *WorkerConfig
	configMu       sync.RWMutex // Protects config access
	
	// Metrics
	eventsProcessed atomic.Uint64
	eventsDropped   atomic.Uint64
	eventsFailed    atomic.Uint64
	
	// Rate limiting
	rateLimiter *RateLimiter

	// Sentry reporter (using interface for testability)
	sentryReporter errors.TelemetryReporter

	logger *slog.Logger
}

// WorkerConfig holds configuration for the telemetry worker
type WorkerConfig struct {
	// CircuitBreaker settings
	FailureThreshold  int
	RecoveryTimeout   time.Duration
	HalfOpenMaxEvents int
	
	// Rate limiting
	RateLimitWindow    time.Duration
	RateLimitMaxEvents int
	
	// Sampling
	SamplingRate float64 // 0.0-1.0, where 1.0 = 100% sampling
	
	// Batching
	BatchingEnabled bool
	BatchSize       int
	BatchTimeout    time.Duration
}

// DefaultWorkerConfig returns default configuration
func DefaultWorkerConfig() *WorkerConfig {
	return &WorkerConfig{
		FailureThreshold:   10,
		RecoveryTimeout:    60 * time.Second,
		HalfOpenMaxEvents:  5,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
		SamplingRate:       1.0, // 100% by default
		BatchingEnabled:    true,
		BatchSize:          10,
		BatchTimeout:       100 * time.Millisecond,
	}
}

// CircuitBreaker implements the circuit breaker pattern for Sentry failures
type CircuitBreaker struct {
	mu              sync.Mutex
	state           string // "closed", "open", "half-open"
	failures        int
	lastFailureTime time.Time
	successCount    int
	config          *WorkerConfig
}

// TimeSource is an interface for getting the current time (allows testing with fake time)
type TimeSource interface {
	Now() time.Time
}

// RealTimeSource uses the actual system time
type RealTimeSource struct{}

func (RealTimeSource) Now() time.Time { return time.Now() }

// RateLimiter implements rate limiting for telemetry
type RateLimiter struct {
	mu         sync.Mutex
	window     time.Duration
	maxEvents  int
	eventTimes []time.Time
	timeSource TimeSource // Injectable time source for testing
}

// NewTelemetryWorker creates a new telemetry worker
func NewTelemetryWorker(enabled bool, config *WorkerConfig) (*TelemetryWorker, error) {
	if config == nil {
		config = DefaultWorkerConfig()
	}
	
	worker := &TelemetryWorker{
		enabled: enabled,
		config:  config,
		circuitBreaker: &CircuitBreaker{
			state:  "closed",
			config: config,
		},
		rateLimiter: &RateLimiter{
			window:     config.RateLimitWindow,
			maxEvents:  config.RateLimitMaxEvents,
			timeSource: RealTimeSource{}, // Use real time by default
		},
		sentryReporter: errors.NewSentryReporter(enabled),
		logger:         getLoggerSafe("telemetry-worker"),
	}
	
	return worker, nil
}

// Name returns the consumer name
func (w *TelemetryWorker) Name() string {
	return "telemetry-worker"
}

// ProcessEvent processes a single error event
func (w *TelemetryWorker) ProcessEvent(event events.ErrorEvent) error {
	// Skip if telemetry is disabled
	if !w.enabled {
		return nil
	}
	
	// Check circuit breaker
	if !w.circuitBreaker.Allow() {
		w.eventsDropped.Add(1)
		w.logger.Debug("circuit breaker open, dropping event",
			"component", event.GetComponent(),
			"category", event.GetCategory(),
		)
		return nil
	}
	
	// Check rate limit
	if !w.rateLimiter.Allow() {
		w.eventsDropped.Add(1)
		w.logger.Debug("rate limit exceeded, dropping event",
			"component", event.GetComponent(),
			"category", event.GetCategory(),
		)
		return nil
	}
	
	// Apply sampling
	if !w.shouldSample(event) {
		w.eventsDropped.Add(1)
		return nil
	}
	
	// Check if already reported
	if event.IsReported() {
		return nil
	}
	
	// Report to Sentry
	err := w.reportToSentry(event)
	if err != nil {
		w.eventsFailed.Add(1)
		w.circuitBreaker.RecordFailure()
		w.logger.Error("failed to report to Sentry",
			"error", err,
			"component", event.GetComponent(),
			"category", event.GetCategory(),
		)
		return err
	}
	
	// Success
	w.eventsProcessed.Add(1)
	w.circuitBreaker.RecordSuccess()
	event.MarkReported()
	
	return nil
}

// ProcessBatch processes multiple events at once
func (w *TelemetryWorker) ProcessBatch(errorEvents []events.ErrorEvent) error {
	if !w.enabled || len(errorEvents) == 0 {
		return nil
	}
	
	// Process each event individually for now
	// Future enhancement: batch reporting to Sentry
	var firstError error
	successCount := 0
	
	for _, event := range errorEvents {
		err := w.ProcessEvent(event)
		if err != nil && firstError == nil {
			firstError = err
		} else if err == nil {
			successCount++
		}
	}
	
	w.logger.Debug("processed telemetry batch",
		"total", len(errorEvents),
		"success", successCount,
		"failed", len(errorEvents)-successCount,
	)
	
	return firstError
}

// SupportsBatching returns true if this consumer supports batch processing
func (w *TelemetryWorker) SupportsBatching() bool {
	w.configMu.RLock()
	defer w.configMu.RUnlock()
	return w.config.BatchingEnabled
}

// reportToSentry sends the error event to Sentry
func (w *TelemetryWorker) reportToSentry(event events.ErrorEvent) error {
	// Convert ErrorEvent to EnhancedError for compatibility
	ee, ok := event.(*errors.EnhancedError)
	if !ok {
		// If not an EnhancedError, create one using the builder pattern
		ee = errors.New(event.GetError()).
			Component(event.GetComponent()).
			Category(errors.ErrorCategory(event.GetCategory())).
			Build()
		
		// Add context if available
		if ctx := event.GetContext(); ctx != nil {
			for k, v := range ctx {
				ee.Context[k] = v
			}
		}
	}
	
	// Use cached SentryReporter
	w.sentryReporter.ReportError(ee)
	
	return nil
}

// shouldSample determines if an event should be sampled
func (w *TelemetryWorker) shouldSample(event events.ErrorEvent) bool {
	w.configMu.RLock()
	samplingRate := w.config.SamplingRate
	w.configMu.RUnlock()
	
	if samplingRate >= 1.0 {
		return true
	}
	
	// Simple sampling based on hash of component + category
	// This ensures consistent sampling for similar errors
	hash := hashString(event.GetComponent() + event.GetCategory())
	sample := float64(hash%100) / 100.0
	
	return sample < samplingRate
}

// hashString returns a simple hash of a string
func hashString(s string) uint32 {
	var h uint32
	for _, c := range s {
		h = h*31 + uint32(c)
	}
	return h
}

// GetStats returns worker statistics
func (w *TelemetryWorker) GetStats() WorkerStats {
	return WorkerStats{
		EventsProcessed: w.eventsProcessed.Load(),
		EventsDropped:   w.eventsDropped.Load(),
		EventsFailed:    w.eventsFailed.Load(),
		CircuitState:    w.circuitBreaker.State(),
	}
}

// WorkerStats contains runtime statistics
type WorkerStats struct {
	EventsProcessed uint64
	EventsDropped   uint64
	EventsFailed    uint64
	CircuitState    string
}

// CircuitBreaker methods

// Allow checks if the circuit allows the operation
func (cb *CircuitBreaker) Allow() bool {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	switch cb.state {
	case "open":
		// Check if we should transition to half-open
		if time.Since(cb.lastFailureTime) > cb.config.RecoveryTimeout {
			cb.state = "half-open"
			cb.successCount = 0
			return true
		}
		return false
		
	case "half-open":
		// Allow limited events in half-open state
		return cb.successCount < cb.config.HalfOpenMaxEvents
		
	default: // closed
		return true
	}
}

// RecordSuccess records a successful operation
func (cb *CircuitBreaker) RecordSuccess() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures = 0
	
	if cb.state == "half-open" {
		cb.successCount++
		if cb.successCount >= cb.config.HalfOpenMaxEvents {
			cb.state = "closed"
		}
	}
}

// RecordFailure records a failed operation
func (cb *CircuitBreaker) RecordFailure() {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	
	cb.failures++
	cb.lastFailureTime = time.Now()
	
	if cb.failures >= cb.config.FailureThreshold {
		cb.state = "open"
	}
	
	if cb.state == "half-open" {
		cb.state = "open"
	}
}

// State returns the current circuit breaker state
func (cb *CircuitBreaker) State() string {
	cb.mu.Lock()
	defer cb.mu.Unlock()
	return cb.state
}

// RateLimiter methods

// Allow checks if an event is allowed under the rate limit
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := rl.timeSource.Now()
	cutoff := now.Add(-rl.window)
	
	// Remove old events outside the window
	validEvents := make([]time.Time, 0, len(rl.eventTimes))
	for _, t := range rl.eventTimes {
		if t.After(cutoff) {
			validEvents = append(validEvents, t)
		}
	}
	rl.eventTimes = validEvents
	
	// Check if we can accept a new event
	if len(rl.eventTimes) >= rl.maxEvents {
		return false
	}
	
	// Add the new event
	rl.eventTimes = append(rl.eventTimes, now)
	return true
}