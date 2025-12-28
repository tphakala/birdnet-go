package telemetry

import (
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/events"
)

//nolint:gocognit // test requires multiple scenarios for comprehensive coverage
func TestTelemetryWorker_ProcessEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		enabled      bool
		event        events.ErrorEvent
		expectReport bool
	}{
		{
			name:         "enabled_worker_processes_event",
			enabled:      true,
			event:        NewMockErrorEvent("test", "Test error"),
			expectReport: true,
		},
		{
			name:         "disabled_worker_skips_event",
			enabled:      false,
			event:        NewMockErrorEvent("test", "Test error"),
			expectReport: false,
		},
		{
			name:         "already_reported_event_skipped",
			enabled:      true,
			event:        NewMockErrorEvent("test", "Test error", WithReported()),
			expectReport: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Create worker
			worker, err := NewTelemetryWorker(tt.enabled, nil)
			require.NoError(t, err, "Failed to create worker")

			// Process event
			err = worker.ProcessEvent(tt.event)
			require.NoError(t, err, "ProcessEvent should not fail")

			// Check if event was reported
			stats := worker.GetStats()
			if tt.expectReport {
				assert.NotZero(t, stats.EventsProcessed, "Expected event to be processed")
			} else {
				assert.Zero(t, stats.EventsProcessed, "Expected event to be skipped")
			}
		})
	}
}

func TestTelemetryWorker_RateLimiting(t *testing.T) {
	t.Parallel()

	// Create a fake time source starting at a fixed time
	fakeTime := NewFakeTimeSource(time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC))

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
	require.NoError(t, err, "Failed to create worker")

	// Replace the Sentry reporter with a mock to prevent goroutine spawning
	worker.sentryReporter = NewMockSentryReporter(true)

	// Inject the fake time source into the rate limiter
	worker.rateLimiter.timeSource = fakeTime

	// Process multiple events rapidly - all at the SAME fixed time
	for range 5 {
		event := NewMockErrorEvent("test", "Test error", WithTimestamp(fakeTime.Now()))
		_ = worker.ProcessEvent(event)
	}

	stats := worker.GetStats()

	// With rate limiting, should have processed exactly 2 events
	// The rest should be dropped (3 events dropped)
	totalHandled := stats.EventsProcessed + stats.EventsDropped
	assert.Equal(t, uint64(5), totalHandled, "Expected 5 total events handled (processed + dropped)")
	assert.Equal(t, uint64(2), stats.EventsProcessed, "Expected exactly 2 events processed")
	assert.Equal(t, uint64(3), stats.EventsDropped, "Expected exactly 3 events dropped")

	// Advance time past the rate limit window
	fakeTime.Advance(150 * time.Millisecond)

	// Should be able to process more events now
	event := NewMockErrorEvent("test", "Test error after window", WithTimestamp(fakeTime.Now()))
	err = worker.ProcessEvent(event)
	require.NoError(t, err, "ProcessEvent should succeed after rate limit window")

	newStats := worker.GetStats()
	assert.Equal(t, uint64(3), newStats.EventsProcessed, "Expected 3 events processed after rate limit window reset (2 + 1)")
}

//nolint:gocognit // test requires multiple scenarios for comprehensive coverage
func TestTelemetryWorker_CircuitBreaker(t *testing.T) {
	t.Parallel()

	// Use synctest for deterministic time-based testing
	synctest.Test(t, func(t *testing.T) {
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
		assert.True(t, cb.Allow(), "Circuit breaker should allow when closed")

		// Record failures
		for range 3 {
			cb.RecordFailure()
		}

		// Should be open now
		assert.Equal(t, "open", cb.State(), "Expected circuit breaker to be open")
		assert.False(t, cb.Allow(), "Circuit breaker should not allow when open")

		// Wait for circuit to allow requests after recovery timeout
		// synctest advances time instantly - no need for polling
		time.Sleep(150 * time.Millisecond)
		synctest.Wait()

		assert.True(t, cb.Allow(), "Circuit breaker should allow after recovery timeout")
		assert.Equal(t, "half-open", cb.State(), "Expected circuit breaker to be half-open")

		// Record successes to close circuit
		for range 2 {
			cb.RecordSuccess()
		}

		assert.Equal(t, "closed", cb.State(), "Expected circuit breaker to be closed after successes")
	})
}

func TestTelemetryWorker_Sampling(t *testing.T) {
	t.Parallel()

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
	require.NoError(t, err, "Failed to create worker")

	// Process many events with different components
	// Use enough components to reduce probability of all being sampled
	components := []string{
		"component1", "component2", "component3", "component4",
		"component5", "component6", "component7", "component8",
	}
	processedCount := 0

	for _, comp := range components {
		event := NewMockErrorEvent(comp, "Test error")

		_ = worker.ProcessEvent(event)

		// Check if it was sampled
		if worker.shouldSample(event) {
			processedCount++
		}
	}

	// With 50% sampling and 8 components, we expect roughly 4 to be sampled
	// Due to deterministic hashing, it won't be exactly 50%
	assert.NotZero(t, processedCount, "Expected some events to be sampled")
	assert.NotEqual(t, len(components), processedCount, "Expected not all events to be sampled with 50% rate")
}

func TestTelemetryWorker_BatchProcessing(t *testing.T) {
	t.Parallel()

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
	require.NoError(t, err, "Failed to create worker")

	// Verify batching is supported
	assert.True(t, worker.SupportsBatching(), "Expected worker to support batching")

	// Create batch of events
	errorEvents := make([]events.ErrorEvent, 0, 5)
	for range 5 {
		errorEvents = append(errorEvents, NewMockErrorEvent("test", "Batch test error"))
	}

	// Process batch
	err = worker.ProcessBatch(errorEvents)
	require.NoError(t, err, "ProcessBatch should succeed")

	stats := worker.GetStats()
	assert.Equal(t, uint64(5), stats.EventsProcessed, "Expected 5 events processed in batch")
}

func TestTelemetryWorker_ReportToSentry_WithContext(t *testing.T) {
	t.Parallel()

	// Create worker
	worker, err := NewTelemetryWorker(true, nil)
	require.NoError(t, err, "Failed to create worker")

	// Replace with mock reporter to avoid actual Sentry calls
	worker.sentryReporter = NewMockSentryReporter(true)

	// Create event with context - this should not panic even if ee.Context is nil
	event := NewMockErrorEvent("test", "Test error with context",
		WithContext(map[string]any{
			"key1": "value1",
			"key2": 42,
		}))

	// This should not panic - the bug is that maps.Copy panics on nil destination
	err = worker.reportToSentry(event)
	assert.NoError(t, err, "reportToSentry should succeed with context")
}

func TestTelemetryWorker_ReportToSentry_NilContextSafe(t *testing.T) {
	t.Parallel()

	// Create worker
	worker, err := NewTelemetryWorker(true, nil)
	require.NoError(t, err, "Failed to create worker")

	// Replace with mock reporter
	worker.sentryReporter = NewMockSentryReporter(true)

	// Create event without context (nil)
	event := NewMockErrorEvent("test", "Test error without context")

	// This should not panic
	err = worker.reportToSentry(event)
	assert.NoError(t, err, "reportToSentry should succeed with nil context")
}
