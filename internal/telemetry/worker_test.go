package telemetry

import (
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	"github.com/tphakala/birdnet-go/internal/logging"
)

// mockErrorEvent implements the ErrorEvent interface for testing
type mockErrorEvent struct {
	component string
	category  string
	message   string
	context   map[string]interface{}
	timestamp time.Time
	reported  bool
	mu        sync.RWMutex
}

func (m *mockErrorEvent) GetComponent() string               { return m.component }
func (m *mockErrorEvent) GetCategory() string                { return m.category }
func (m *mockErrorEvent) GetContext() map[string]interface{} { return m.context }
func (m *mockErrorEvent) GetTimestamp() time.Time            { return m.timestamp }
func (m *mockErrorEvent) GetError() error                    { return errors.NewStd(m.message) }
func (m *mockErrorEvent) GetMessage() string                 { return m.message }
func (m *mockErrorEvent) IsReported() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.reported
}
func (m *mockErrorEvent) MarkReported() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reported = true
}

func TestTelemetryWorker_ProcessEvent(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	tests := []struct {
		name         string
		enabled      bool
		event        events.ErrorEvent
		expectReport bool
	}{
		{
			name:    "enabled_worker_processes_event",
			enabled: true,
			event: &mockErrorEvent{
				component: "test",
				category:  string(errors.CategorySystem),
				message:   "Test error",
				timestamp: time.Now(),
			},
			expectReport: true,
		},
		{
			name:    "disabled_worker_skips_event",
			enabled: false,
			event: &mockErrorEvent{
				component: "test",
				category:  string(errors.CategorySystem),
				message:   "Test error",
				timestamp: time.Now(),
			},
			expectReport: false,
		},
		{
			name:    "already_reported_event_skipped",
			enabled: true,
			event: &mockErrorEvent{
				component: "test",
				category:  string(errors.CategorySystem),
				message:   "Test error",
				timestamp: time.Now(),
				reported:  true,
			},
			expectReport: false,
		},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			
			// Create worker
			worker, err := NewTelemetryWorker(tt.enabled, nil)
			if err != nil {
				t.Fatalf("Failed to create worker: %v", err)
			}
			
			// Process event
			err = worker.ProcessEvent(tt.event)
			if err != nil {
				t.Errorf("ProcessEvent failed: %v", err)
			}
			
			// Check if event was reported
			if tt.expectReport {
				stats := worker.GetStats()
				if stats.EventsProcessed == 0 {
					t.Error("Expected event to be processed, but it wasn't")
				}
			} else {
				stats := worker.GetStats()
				if stats.EventsProcessed > 0 {
					t.Error("Expected event to be skipped, but it was processed")
				}
			}
		})
	}
}

func TestTelemetryWorker_RateLimiting(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	// Create worker with low rate limit
	config := &WorkerConfig{
		FailureThreshold:   10,
		RecoveryTimeout:    60 * time.Second,
		HalfOpenMaxEvents:  5,
		RateLimitWindow:    100 * time.Millisecond,
		RateLimitMaxEvents: 2, // Very low to test rate limiting
		SamplingRate:       1.0,
	}
	
	worker, err := NewTelemetryWorker(true, config)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	// Process multiple events rapidly
	for i := 0; i < 5; i++ {
		event := &mockErrorEvent{
			component: "test",
			category:  string(errors.CategorySystem),
			message:   "Test error",
			timestamp: time.Now(),
		}
		_ = worker.ProcessEvent(event)
	}
	
	stats := worker.GetStats()
	
	// Should have processed 2 events and dropped the rest
	if stats.EventsProcessed != 2 {
		t.Errorf("Expected 2 events processed, got %d", stats.EventsProcessed)
	}
	
	if stats.EventsDropped != 3 {
		t.Errorf("Expected 3 events dropped, got %d", stats.EventsDropped)
	}
	
	// Wait for rate limit window to pass using a helper function
	waitForRateLimitReset := func(t *testing.T, rl *RateLimiter, timeout time.Duration) bool {
		t.Helper()
		deadline := time.Now().Add(timeout)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		
		for time.Now().Before(deadline) {
			<-ticker.C
			if rl.Allow() {
				return true
			}
		}
		return false
	}
	
	// Wait for rate limiter to reset
	if !waitForRateLimitReset(t, worker.rateLimiter, 200*time.Millisecond) {
		t.Fatal("Rate limiter did not reset within timeout")
	}
	
	// Should be able to process more events now
	event := &mockErrorEvent{
		component: "test",
		category:  string(errors.CategorySystem),
		message:   "Test error after wait",
		timestamp: time.Now(),
	}
	err = worker.ProcessEvent(event)
	if err != nil {
		t.Errorf("ProcessEvent failed after rate limit window: %v", err)
	}
	
	newStats := worker.GetStats()
	if newStats.EventsProcessed != 3 {
		t.Errorf("Expected 3 events processed after wait, got %d", newStats.EventsProcessed)
	}
}

func TestTelemetryWorker_CircuitBreaker(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	// This test verifies circuit breaker behavior
	// Since we can't easily simulate Sentry failures in unit tests,
	// we'll test the circuit breaker logic directly
	
	config := &WorkerConfig{
		FailureThreshold:  3,
		RecoveryTimeout:   100 * time.Millisecond,
		HalfOpenMaxEvents: 2,
	}
	
	cb := &CircuitBreaker{
		state:  "closed",
		config: config,
	}
	
	// Initially should allow
	if !cb.Allow() {
		t.Error("Circuit breaker should allow when closed")
	}
	
	// Record failures
	for i := 0; i < 3; i++ {
		cb.RecordFailure()
	}
	
	// Should be open now
	if cb.State() != "open" {
		t.Errorf("Expected circuit breaker to be open, got %s", cb.State())
	}
	
	if cb.Allow() {
		t.Error("Circuit breaker should not allow when open")
	}
	
	// Create a test helper to wait for circuit recovery
	waitForCircuitRecovery := func(t *testing.T, cb *CircuitBreaker, timeout time.Duration) bool {
		t.Helper()
		deadline := time.Now().Add(timeout)
		ticker := time.NewTicker(10 * time.Millisecond)
		defer ticker.Stop()
		
		for time.Now().Before(deadline) {
			<-ticker.C
			if cb.Allow() {
				return true
			}
		}
		return false
	}
	
	// Wait for circuit to allow requests after recovery timeout
	if !waitForCircuitRecovery(t, cb, 200*time.Millisecond) {
		t.Error("Circuit breaker should allow after recovery timeout")
	}
	
	if cb.State() != "half-open" {
		t.Errorf("Expected circuit breaker to be half-open, got %s", cb.State())
	}
	
	// Record successes to close circuit
	for i := 0; i < 2; i++ {
		cb.RecordSuccess()
	}
	
	if cb.State() != "closed" {
		t.Errorf("Expected circuit breaker to be closed after successes, got %s", cb.State())
	}
}

func TestTelemetryWorker_Sampling(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	// Create worker with 50% sampling
	config := &WorkerConfig{
		FailureThreshold:   10,
		RecoveryTimeout:    60 * time.Second,
		HalfOpenMaxEvents:  5,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 1000,
		SamplingRate:       0.5, // 50% sampling
	}
	
	worker, err := NewTelemetryWorker(true, config)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	// Process many events with different components
	// Some should be sampled, some should not
	components := []string{"component1", "component2", "component3", "component4"}
	processedCount := 0
	
	for _, comp := range components {
		event := &mockErrorEvent{
			component: comp,
			category:  string(errors.CategorySystem),
			message:   "Test error",
			timestamp: time.Now(),
		}
		
		_ = worker.ProcessEvent(event)
		
		// Check if it was sampled
		if worker.shouldSample(event) {
			processedCount++
		}
	}
	
	// With 50% sampling, we should have processed roughly half
	// But due to deterministic hashing, it might not be exactly 50%
	if processedCount == 0 || processedCount == len(components) {
		t.Errorf("Expected some but not all events to be sampled with 50%% rate, got %d/%d",
			processedCount, len(components))
	}
}

func TestTelemetryWorker_BatchProcessing(t *testing.T) {
	t.Parallel()
	
	// Initialize logging
	logging.Init()
	
	config := &WorkerConfig{
		FailureThreshold:   10,
		RecoveryTimeout:    60 * time.Second,
		HalfOpenMaxEvents:  5,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
		SamplingRate:       1.0,
		BatchingEnabled:    true,
		BatchSize:          10,
		BatchTimeout:       100 * time.Millisecond,
	}
	
	worker, err := NewTelemetryWorker(true, config)
	if err != nil {
		t.Fatalf("Failed to create worker: %v", err)
	}
	
	// Verify batching is supported
	if !worker.SupportsBatching() {
		t.Error("Expected worker to support batching")
	}
	
	// Create batch of events
	errorEvents := make([]events.ErrorEvent, 0, 5)
	for i := 0; i < 5; i++ {
		errorEvents = append(errorEvents, &mockErrorEvent{
			component: "test",
			category:  string(errors.CategorySystem),
			message:   "Batch test error",
			timestamp: time.Now(),
		})
	}
	
	// Process batch
	err = worker.ProcessBatch(errorEvents)
	if err != nil {
		t.Errorf("ProcessBatch failed: %v", err)
	}
	
	stats := worker.GetStats()
	if stats.EventsProcessed != 5 {
		t.Errorf("Expected 5 events processed in batch, got %d", stats.EventsProcessed)
	}
}