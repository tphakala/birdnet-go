package telemetry

import (
	"fmt"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
)

// TestAsyncTelemetryNonBlocking verifies that telemetry no longer blocks with event bus
func TestAsyncTelemetryNonBlocking(t *testing.T) {
	// Reset global state between tests
	asyncWorker = nil
	
	t.Run("error.Build() should not block with async telemetry", func(t *testing.T) {
		// Initialize test config
		config, cleanup := InitForTesting(t)
		defer cleanup()
		
		// Make telemetry slow to simulate real network delays
		config.MockTransport.SetDelay(100 * time.Millisecond)
		
		// Initialize event bus
		eventBusConfig := events.DefaultConfig()
		eventBus, err := events.Initialize(eventBusConfig)
		if err != nil {
			t.Fatalf("Failed to initialize event bus: %v", err)
		}
		defer eventBus.Shutdown(5 * time.Second)
		
		// Set up the event publisher in errors package
		events.InitializeErrorsIntegration(func(publisher any) {
			errors.SetEventPublisher(publisher.(errors.EventPublisher))
		})
		
		// Initialize error integration
		InitializeErrorIntegration()
		
		// Initialize async telemetry integration
		settings := conf.Setting()
		if err := InitializeEventBusIntegration(settings); err != nil {
			t.Fatalf("Failed to initialize telemetry event bus integration: %v", err)
		}
		
		// Give event bus time to start workers
		time.Sleep(10 * time.Millisecond)
		
		// Measure how long Build() takes
		start := time.Now()
		
		// Create and build an error - this should not block
		err = errors.New(fmt.Errorf("test error")).
			Component("test").
			Category(errors.CategoryNetwork).
			Build()
		
		elapsed := time.Since(start)
		
		// Build() should return immediately with async telemetry
		if elapsed > 5*time.Millisecond {
			t.Errorf("error.Build() took %v - still blocking despite async telemetry!", elapsed)
		}
		
		t.Logf("error.Build() took %v (non-blocking)", elapsed)
		
		// Wait a bit to let async processing happen
		time.Sleep(150 * time.Millisecond)
		
		// Verify the event was eventually captured
		if config.MockTransport.GetEventCount() == 0 {
			t.Error("No events captured - async telemetry may not be working")
		}
		
		_ = err // use the error to avoid compiler warnings
	})

	t.Run("batch error creation should be fast", func(t *testing.T) {
		// Reset global state
		asyncWorker = nil
		
		// Initialize test config
		config, cleanup := InitForTesting(t)
		defer cleanup()
		
		// Make telemetry slow
		config.MockTransport.SetDelay(50 * time.Millisecond)
		
		// Initialize event bus
		eventBusConfig := events.DefaultConfig()
		eventBus, err := events.Initialize(eventBusConfig)
		if err != nil {
			t.Fatalf("Failed to initialize event bus: %v", err)
		}
		defer eventBus.Shutdown(5 * time.Second)
		
		// Set up integrations
		events.InitializeErrorsIntegration(func(publisher any) {
			errors.SetEventPublisher(publisher.(errors.EventPublisher))
		})
		InitializeErrorIntegration()
		
		settings := conf.Setting()
		if err := InitializeEventBusIntegration(settings); err != nil {
			t.Fatalf("Failed to initialize telemetry event bus integration: %v", err)
		}
		
		// Give event bus time to start
		time.Sleep(10 * time.Millisecond)
		
		// Clear any previous events
		config.MockTransport.Clear()
		
		// Create many errors quickly
		start := time.Now()
		numErrors := 100
		
		for i := 0; i < numErrors; i++ {
			_ = errors.New(fmt.Errorf("batch error %d", i)).
				Component("test").
				Category(errors.CategoryGeneric).
				Build()
		}
		
		elapsed := time.Since(start)
		
		// Should be very fast - no blocking
		expectedMaxTime := 50 * time.Millisecond // Allow some overhead
		if elapsed > expectedMaxTime {
			t.Errorf("Creating %d errors took %v - async telemetry may be blocking", numErrors, elapsed)
		}
		
		t.Logf("Created %d errors in %v (%.2f μs per error)", 
			numErrors, elapsed, float64(elapsed.Microseconds())/float64(numErrors))
		
		// Wait for async processing
		time.Sleep(time.Duration(numErrors*50+100) * time.Millisecond)
		
		// Check that events were captured
		capturedCount := config.MockTransport.GetEventCount()
		t.Logf("Captured %d/%d events asynchronously", capturedCount, numErrors)
		
		// Some events might be dropped due to rate limiting, that's OK
		if capturedCount == 0 {
			t.Error("No events captured - async telemetry not working")
		}
	})
}

// TestAsyncWorkerRateLimiting verifies rate limiting works correctly
func TestAsyncWorkerRateLimiting(t *testing.T) {
	t.Run("rate limiter should limit events per component", func(t *testing.T) {
		settings := &conf.Settings{
			Sentry: conf.SentrySettings{
				Enabled: true,
			},
		}
		
		// Create worker with strict rate limit
		config := &AsyncWorkerConfig{
			RateLimitWindow:   1 * time.Second,
			RateLimitEvents:   5, // Only 5 events per second
			FailureThreshold:  10,
			RecoveryTimeout:   5 * time.Minute,
			SlowThreshold:     100 * time.Millisecond,
		}
		
		worker, err := NewAsyncWorker(settings, config)
		if err != nil {
			t.Fatalf("Failed to create worker: %v", err)
		}
		
		// Send many events quickly
		component := "test-component"
		accepted := 0
		
		for i := 0; i < 20; i++ {
			if worker.rateLimiter.Allow(component) {
				accepted++
			}
		}
		
		// Should only accept 5 events
		if accepted != 5 {
			t.Errorf("Rate limiter accepted %d events, expected 5", accepted)
		}
		
		// Wait for window to expire
		time.Sleep(1100 * time.Millisecond)
		
		// Should accept more events now
		accepted2 := 0
		for i := 0; i < 10; i++ {
			if worker.rateLimiter.Allow(component) {
				accepted2++
			}
		}
		
		if accepted2 != 5 {
			t.Errorf("Rate limiter accepted %d events after reset, expected 5", accepted2)
		}
	})
}

// TestAsyncWorkerCircuitBreaker verifies circuit breaker functionality
func TestAsyncWorkerCircuitBreaker(t *testing.T) {
	t.Run("circuit breaker should open after failures", func(t *testing.T) {
		cb := NewAsyncCircuitBreaker(3, 100*time.Millisecond)
		
		// Should start closed
		if !cb.CanProceed() {
			t.Error("Circuit breaker should start closed")
		}
		
		// Record failures
		cb.RecordFailure()
		cb.RecordFailure()
		
		// Should still be closed (threshold is 3)
		if !cb.CanProceed() {
			t.Error("Circuit breaker opened too early")
		}
		
		// One more failure should open it
		cb.RecordFailure()
		
		// Should now be open
		if cb.CanProceed() {
			t.Error("Circuit breaker should be open after 3 failures")
		}
		
		// Wait for recovery timeout
		time.Sleep(150 * time.Millisecond)
		
		// Should now be half-open
		if !cb.CanProceed() {
			t.Error("Circuit breaker should be half-open after recovery timeout")
		}
		
		// Success should eventually close it
		cb.RecordSuccess()
		cb.RecordSuccess()
		cb.RecordSuccess()
		
		// Should be closed again
		if !cb.CanProceed() || cb.IsOpen() {
			t.Error("Circuit breaker should be closed after successes")
		}
	})
}