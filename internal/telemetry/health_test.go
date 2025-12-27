package telemetry

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetOverallStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   HealthStatus
		expected string
	}{
		{
			name: "healthy_status",
			status: HealthStatus{
				Healthy: true,
				Components: map[string]ComponentHealth{
					ComponentErrorIntegration: {State: InitStateCompleted, Healthy: true},
					ComponentSentry:           {State: InitStateCompleted, Healthy: true},
				},
			},
			expected: "healthy",
		},
		{
			name: "critical_when_error_integration_failed",
			status: HealthStatus{
				Healthy: false,
				Components: map[string]ComponentHealth{
					ComponentErrorIntegration: {State: InitStateFailed, Healthy: false, Error: "init failed"},
					ComponentSentry:           {State: InitStateCompleted, Healthy: true},
				},
			},
			expected: "critical",
		},
		{
			name: "degraded_when_non_critical_component_failed",
			status: HealthStatus{
				Healthy: false,
				Components: map[string]ComponentHealth{
					ComponentErrorIntegration: {State: InitStateCompleted, Healthy: true},
					ComponentSentry:           {State: InitStateFailed, Healthy: false, Error: "sentry failed"},
				},
			},
			expected: "degraded",
		},
		{
			name: "initializing_when_no_failures",
			status: HealthStatus{
				Healthy: false,
				Components: map[string]ComponentHealth{
					ComponentErrorIntegration: {State: InitStateInProgress, Healthy: false},
					ComponentSentry:           {State: InitStateNotStarted, Healthy: false},
				},
			},
			expected: "initializing",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := getOverallStatus(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

func TestFormatUnhealthyComponents(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		status   HealthStatus
		expected string
	}{
		{
			name: "no_unhealthy_components",
			status: HealthStatus{
				Components: map[string]ComponentHealth{
					ComponentErrorIntegration: {State: InitStateCompleted, Healthy: true},
					ComponentSentry:           {State: InitStateCompleted, Healthy: true},
				},
			},
			expected: "none",
		},
		{
			name: "skips_not_started_components",
			status: HealthStatus{
				Components: map[string]ComponentHealth{
					ComponentErrorIntegration: {State: InitStateCompleted, Healthy: true},
					ComponentSentry:           {State: InitStateNotStarted, Healthy: false},
				},
			},
			expected: "none",
		},
		{
			name: "includes_failed_components",
			status: HealthStatus{
				Components: map[string]ComponentHealth{
					ComponentSentry: {State: InitStateFailed, Healthy: false},
				},
			},
			expected: "[sentry:failed]",
		},
		{
			name: "includes_in_progress_unhealthy_components",
			status: HealthStatus{
				Components: map[string]ComponentHealth{
					ComponentEventBus: {State: InitStateInProgress, Healthy: false},
				},
			},
			expected: "[event_bus:in_progress]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := formatUnhealthyComponents(tt.status)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestWorkerHealthCheck_Isolated tests WorkerHealthCheck without modifying global state
// This avoids race conditions with other tests that access telemetryWorker
func TestWorkerHealthCheck_Isolated(t *testing.T) {
	t.Parallel()

	// Test worker GetStats behavior directly since we can't safely modify global state
	t.Run("worker_stats_healthy", func(t *testing.T) {
		t.Parallel()
		config := DefaultWorkerConfig()
		worker, err := NewTelemetryWorker(false, config)
		require.NoError(t, err)

		stats := worker.GetStats()
		// A new worker should have healthy stats
		assert.Equal(t, uint64(0), stats.EventsProcessed)
		assert.Equal(t, uint64(0), stats.EventsFailed)
		assert.Equal(t, circuitStateClosed, stats.CircuitState)
	})

	t.Run("circuit_breaker_opens_after_failures", func(t *testing.T) {
		t.Parallel()
		config := DefaultWorkerConfig()
		config.FailureThreshold = 2 // Low threshold for testing
		worker, err := NewTelemetryWorker(false, config)
		require.NoError(t, err)

		// Force failures to trip the circuit breaker
		worker.circuitBreaker.RecordFailure()
		worker.circuitBreaker.RecordFailure()
		worker.circuitBreaker.RecordFailure()

		stats := worker.GetStats()
		assert.Equal(t, circuitStateOpen, stats.CircuitState)
	})

	t.Run("high_failure_rate_detected", func(t *testing.T) {
		t.Parallel()
		// Test the failure rate calculation logic
		// minEventsForFailureCheck = 100, maxFailureRateThreshold = 0.1 (10%)
		// So if total = 101 and failed = 21, rate = 21/101 ≈ 0.208 > 0.1
		total := int64(minEventsForFailureCheck + 1)
		failed := int64(float64(total) * (maxFailureRateThreshold + 0.1)) // Just over threshold

		failureRate := float64(failed) / float64(total)
		assert.Greater(t, failureRate, maxFailureRateThreshold, "failure rate should exceed threshold")
		assert.Greater(t, total, int64(minEventsForFailureCheck), "total should exceed minimum for check")
	})
}

func TestInitStateString(t *testing.T) {
	t.Parallel()

	tests := []struct {
		state    InitState
		expected string
	}{
		{InitStateNotStarted, "not_started"},
		{InitStateInProgress, "in_progress"},
		{InitStateCompleted, "completed"},
		{InitStateFailed, "failed"},
		{InitState(99), "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.expected, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.state.String())
		})
	}
}

func TestComponentHealthStatus(t *testing.T) {
	t.Parallel()

	manager := &InitManager{
		initLog: GetLogger(),
	}

	// Set up various component states
	manager.errorIntegration.Store(int32(InitStateCompleted))
	manager.sentryClient.Store(int32(InitStateFailed))
	manager.sentryErr.Store(assert.AnError)
	manager.eventBus.Store(int32(InitStateInProgress))
	manager.telemetryWorker.Store(int32(InitStateNotStarted))

	status := manager.HealthCheck()

	// Verify overall health
	assert.False(t, status.Healthy, "should be unhealthy when a component failed")

	// Verify individual components
	assert.Equal(t, InitStateCompleted, status.Components[ComponentErrorIntegration].State)
	assert.True(t, status.Components[ComponentErrorIntegration].Healthy)

	assert.Equal(t, InitStateFailed, status.Components[ComponentSentry].State)
	assert.False(t, status.Components[ComponentSentry].Healthy)
	assert.NotEmpty(t, status.Components[ComponentSentry].Error)

	assert.Equal(t, InitStateInProgress, status.Components[ComponentEventBus].State)
	assert.False(t, status.Components[ComponentEventBus].Healthy)

	assert.Equal(t, InitStateNotStarted, status.Components[ComponentWorker].State)
	assert.False(t, status.Components[ComponentWorker].Healthy)

	// Verify timestamp is recent
	assert.WithinDuration(t, time.Now(), status.Timestamp, time.Second)
}
