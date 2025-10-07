package notification

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockTelemetryReporter is a test implementation that records events
type mockTelemetryReporter struct {
	enabled        bool
	capturedEvents []mockEvent
	capturedErrors []mockError
}

type mockEvent struct {
	message  string
	level    string
	tags     map[string]string
	contexts map[string]interface{}
}

type mockError struct {
	err       error
	component string
}

func (m *mockTelemetryReporter) CaptureError(err error, component string) {
	m.capturedErrors = append(m.capturedErrors, mockError{
		err:       err,
		component: component,
	})
}

func (m *mockTelemetryReporter) CaptureEvent(message, level string, tags map[string]string, contexts map[string]interface{}) {
	m.capturedEvents = append(m.capturedEvents, mockEvent{
		message:  message,
		level:    level,
		tags:     tags,
		contexts: contexts,
	})
}

func (m *mockTelemetryReporter) IsEnabled() bool {
	return m.enabled
}

func TestNoopTelemetryReporter(t *testing.T) {
	reporter := NewNoopTelemetryReporter()

	// Should not panic
	reporter.CaptureError(assert.AnError, "test")
	reporter.CaptureEvent("test", "info", nil, nil)

	// Should return false
	assert.False(t, reporter.IsEnabled())
}

func TestNotificationTelemetry_IsEnabled(t *testing.T) {
	tests := []struct {
		name           string
		config         *TelemetryConfig
		reporter       TelemetryReporter
		expectedResult bool
	}{
		{
			name:           "nil telemetry",
			config:         nil,
			reporter:       nil,
			expectedResult: false,
		},
		{
			name: "disabled in config",
			config: &TelemetryConfig{
				Enabled:              false,
				ReportCircuitBreaker: true,
			},
			reporter:       &mockTelemetryReporter{enabled: true},
			expectedResult: false,
		},
		{
			name: "disabled in reporter",
			config: &TelemetryConfig{
				Enabled:              true,
				ReportCircuitBreaker: true,
			},
			reporter:       &mockTelemetryReporter{enabled: false},
			expectedResult: false,
		},
		{
			name: "fully enabled",
			config: &TelemetryConfig{
				Enabled:              true,
				ReportCircuitBreaker: true,
			},
			reporter:       &mockTelemetryReporter{enabled: true},
			expectedResult: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var nt *NotificationTelemetry
			if tt.config != nil || tt.reporter != nil {
				nt = NewNotificationTelemetry(tt.config, tt.reporter)
			}

			result := nt.IsEnabled()
			assert.Equal(t, tt.expectedResult, result)
		})
	}
}

func TestCircuitBreakerTelemetry(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry with circuit breaker reporting enabled
	config := DefaultTelemetryConfig()
	config.ReportCircuitBreaker = true
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Create circuit breaker
	cbConfig := CircuitBreakerConfig{
		MaxFailures:         5,
		Timeout:             30 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(cbConfig, nil, "test-provider")
	cb.SetTelemetry(telemetry)

	// Trigger failures to open circuit breaker
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return assert.AnError
		})
	}

	// Wait a bit for async operations
	time.Sleep(50 * time.Millisecond)

	// Verify telemetry event was captured
	require.Len(t, reporter.capturedEvents, 1, "Expected 1 telemetry event for circuit breaker opening")

	event := reporter.capturedEvents[0]
	assert.Contains(t, event.message, "Circuit breaker state transition")
	assert.Equal(t, "warning", event.level, "Opening circuit breaker should be warning level")
	assert.Equal(t, "notification", event.tags["component"])
	assert.Equal(t, "test-provider", event.tags["provider"])
	assert.Equal(t, "closed", event.tags["old_state"])
	assert.Equal(t, "open", event.tags["new_state"])
	assert.Equal(t, "5", event.tags["consecutive_failures"])

	// Verify context data
	cbContext, ok := event.contexts["circuit_breaker"].(map[string]interface{})
	require.True(t, ok, "Circuit breaker context should be present")
	assert.Equal(t, 5, cbContext["failure_threshold"])
	assert.InDelta(t, 30.0, cbContext["timeout_seconds"], 0.001)
	assert.Equal(t, 1, cbContext["half_open_max_requests"])
}

func TestCircuitBreakerTelemetry_Disabled(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry with circuit breaker reporting DISABLED
	config := DefaultTelemetryConfig()
	config.ReportCircuitBreaker = false
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Create circuit breaker
	cbConfig := CircuitBreakerConfig{
		MaxFailures:         5,
		Timeout:             30 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(cbConfig, nil, "test-provider")
	cb.SetTelemetry(telemetry)

	// Trigger failures
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return assert.AnError
		})
	}

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Verify NO telemetry events captured (circuit breaker reporting disabled)
	assert.Empty(t, reporter.capturedEvents, "No telemetry events should be captured when disabled")
}

func TestCircuitBreakerTelemetry_Recovery(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Create circuit breaker with short timeout for testing
	cbConfig := CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(cbConfig, nil, "test-provider")
	cb.SetTelemetry(telemetry)

	// Trigger failures to open
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return assert.AnError
		})
	}

	// Wait for timeout
	time.Sleep(150 * time.Millisecond)

	// Successful call should transition to half-open then closed
	err := cb.Call(ctx, func(ctx context.Context) error {
		return nil // Success
	})
	require.NoError(t, err)

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Should have 2 events: closed→open, half-open→closed
	require.GreaterOrEqual(t, len(reporter.capturedEvents), 2, "Should have at least 2 state transitions")

	// First event: closed → open
	openEvent := reporter.capturedEvents[0]
	assert.Equal(t, "closed", openEvent.tags["old_state"])
	assert.Equal(t, "open", openEvent.tags["new_state"])
	assert.Equal(t, "warning", openEvent.level)

	// Last event: half-open → closed (recovery)
	recoveryEvent := reporter.capturedEvents[len(reporter.capturedEvents)-1]
	assert.Equal(t, "half-open", recoveryEvent.tags["old_state"])
	assert.Equal(t, "closed", recoveryEvent.tags["new_state"])
	assert.Equal(t, "info", recoveryEvent.level, "Recovery should be info level")
}

func TestCircuitBreakerTelemetry_NoProvider(t *testing.T) {
	// Create telemetry without reporter (nil)
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, nil)

	// Create circuit breaker
	cbConfig := CircuitBreakerConfig{
		MaxFailures:         5,
		Timeout:             30 * time.Second,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(cbConfig, nil, "test-provider")
	cb.SetTelemetry(telemetry)

	// Trigger failures - should not panic even with nil reporter
	ctx := context.Background()
	for i := 0; i < 5; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return assert.AnError
		})
	}

	// Should not panic
	assert.Equal(t, StateOpen, cb.State())
}

func TestServiceTelemetryIntegration(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Create service
	serviceConfig := DefaultServiceConfig()
	service := NewService(serviceConfig)

	// Set telemetry
	service.SetTelemetry(telemetry)

	// Verify telemetry is set
	assert.NotNil(t, service.GetTelemetry())
	assert.True(t, service.GetTelemetry().IsEnabled())

	// Clean up
	service.Stop()
}

func TestDefaultTelemetryConfig(t *testing.T) {
	config := DefaultTelemetryConfig()

	// Verify privacy-first defaults
	assert.True(t, config.Enabled)
	assert.True(t, config.ReportCircuitBreaker)
	assert.True(t, config.ReportAPIErrors)
	assert.True(t, config.ReportPanics)
	assert.True(t, config.ReportRateLimit)
	assert.True(t, config.ReportResources)
	assert.False(t, config.IncludeMetadata, "IncludeMetadata should be false by default (privacy-first)")
}

func TestProviderInitializationError(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Report initialization error
	testErr := fmt.Errorf("failed to parse template: unexpected token")
	telemetry.ProviderInitializationError(
		"webhook-primary",
		"webhook",
		"template_parse",
		testErr,
	)

	// Verify event captured
	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	assert.Contains(t, event.message, "Provider initialization failed")
	assert.Equal(t, "error", event.level)
	assert.Equal(t, "notification", event.tags["component"])
	assert.Equal(t, "webhook-primary", event.tags["provider"])
	assert.Equal(t, "webhook", event.tags["provider_type"])
	assert.Equal(t, "template_parse", event.tags["error_type"])

	// Verify contexts
	initCtx, ok := event.contexts["initialization"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, "webhook", initCtx["provider_type"])
	assert.Equal(t, "template_parse", initCtx["error_type"])
}

func TestWorkerPanicRecovered(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Simulate panic recovery
	panicValue := "runtime error: invalid memory address"
	stackTrace := "goroutine 123:\nworker.go:45\ndetection.go:123"
	telemetry.WorkerPanicRecovered(
		"detection_consumer",
		panicValue,
		stackTrace,
		1523,
		12,
	)

	// Verify event captured
	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	assert.Contains(t, event.message, "Worker panic recovered")
	assert.Equal(t, "critical", event.level)
	assert.Equal(t, "notification", event.tags["component"])
	assert.Equal(t, "detection_consumer", event.tags["worker_type"])

	// Verify worker state context
	workerState, ok := event.contexts["worker_state"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, uint64(1523), workerState["events_processed"])
	assert.Equal(t, uint64(12), workerState["events_dropped"])
}

func TestWorkerPanicRecovered_Disabled(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	config.ReportPanics = false
	telemetry := NewNotificationTelemetry(&config, reporter)

	telemetry.WorkerPanicRecovered("test", "panic", "stack", 100, 5)

	// Should not report when disabled
	assert.Empty(t, reporter.capturedEvents)
}

func TestRateLimitExceeded(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Report sustained high drop rate
	telemetry.RateLimitExceeded(150, 60, 100, 60.0)

	// Verify event captured
	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	assert.Contains(t, event.message, "rate limit exceeded")
	assert.Equal(t, "warning", event.level)
	assert.Equal(t, "notification", event.tags["component"])
	assert.Equal(t, "rate_limiter", event.tags["subsystem"])

	// Verify rate limiter context
	rlCtx, ok := event.contexts["rate_limiter"].(map[string]interface{})
	require.True(t, ok)
	assert.Equal(t, 60, rlCtx["window_seconds"])
	assert.Equal(t, 100, rlCtx["max_events"])
	assert.Equal(t, 150, rlCtx["dropped_count"])
	assert.InDelta(t, 60.0, rlCtx["drop_rate_percent"], 0.1)
}

func TestRateLimitExceeded_LowDropRate(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Low drop rate should not be reported
	telemetry.RateLimitExceeded(30, 60, 100, 30.0)

	// Should not report low drop rates
	assert.Empty(t, reporter.capturedEvents)
}

func TestRateLimitExceeded_Disabled(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	config.ReportRateLimit = false
	telemetry := NewNotificationTelemetry(&config, reporter)

	telemetry.RateLimitExceeded(150, 60, 100, 60.0)

	// Should not report when disabled
	assert.Empty(t, reporter.capturedEvents)
}

// TestTelemetryIntegration_EndToEnd verifies full wiring from service to circuit breaker
func TestTelemetryIntegration_EndToEnd(t *testing.T) {
	// Setup: Create service with telemetry
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	serviceConfig := &ServiceConfig{
		MaxNotifications:   100,
		CleanupInterval:    5 * time.Minute,
		RateLimitWindow:    1 * time.Minute,
		RateLimitMaxEvents: 100,
	}

	service := NewService(serviceConfig)
	service.SetTelemetry(telemetry)

	// Verify telemetry is set
	require.NotNil(t, service.GetTelemetry())
	assert.True(t, service.GetTelemetry().IsEnabled())

	// Create circuit breaker and inject telemetry
	cbConfig := CircuitBreakerConfig{
		MaxFailures:         3,
		Timeout:             100 * time.Millisecond,
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(cbConfig, nil, "test-provider")
	cb.SetTelemetry(telemetry)

	// Trigger state transition: closed → open
	failingFunc := func(ctx context.Context) error {
		return fmt.Errorf("simulated failure")
	}

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		_ = cb.Call(ctx, failingFunc)
	}

	// Verify telemetry captured the transition
	require.Len(t, reporter.capturedEvents, 1, "should capture closed → open transition")

	event := reporter.capturedEvents[0]
	assert.Contains(t, event.message, "closed → open")
	assert.Equal(t, SeverityWarning, event.level)
	assert.Equal(t, "test-provider", event.tags["provider"])
	assert.Equal(t, "closed", event.tags["old_state"])
	assert.Equal(t, "open", event.tags["new_state"])
}

// TestTelemetryIntegration_Debouncing verifies rapid transitions are debounced
func TestTelemetryIntegration_Debouncing(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	cbConfig := CircuitBreakerConfig{
		MaxFailures:         2,
		Timeout:             10 * time.Millisecond, // Very short for rapid transitions
		HalfOpenMaxRequests: 1,
	}
	cb := NewPushCircuitBreaker(cbConfig, nil, "flapping-provider")
	cb.SetTelemetry(telemetry)

	ctx := context.Background()

	// Simulate flapping: closed → open → half-open → open → half-open...
	// 1. Fail twice to open circuit
	for i := 0; i < 2; i++ {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return fmt.Errorf("failure")
		})
	}

	initialEventCount := len(reporter.capturedEvents)
	assert.GreaterOrEqual(t, initialEventCount, 1, "should capture initial open")

	// 2. Wait for timeout to enter half-open
	time.Sleep(15 * time.Millisecond)

	// 3. Try a request in half-open (will fail and reopen)
	_ = cb.Call(ctx, func(ctx context.Context) error {
		return fmt.Errorf("still failing")
	})

	// 4. Immediately try to transition again (should be debounced)
	time.Sleep(5 * time.Millisecond)
	_ = cb.Call(ctx, func(ctx context.Context) error {
		return fmt.Errorf("still failing")
	})

	// Verify: Should have reported closed→open, but subsequent transitions debounced
	// (since they happen within MinTelemetryReportInterval)
	assert.LessOrEqual(t, len(reporter.capturedEvents), initialEventCount+1,
		"rapid transitions should be debounced")
}

// TestTelemetryIntegration_ConfigurableThreshold verifies rate limit threshold
func TestTelemetryIntegration_ConfigurableThreshold(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	config.RateLimitReportThreshold = 75.0 // Custom threshold
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Drop rate below threshold - should not report
	telemetry.RateLimitExceeded(70, 60, 100, 70.0)
	assert.Empty(t, reporter.capturedEvents)

	// Drop rate above threshold - should report
	telemetry.RateLimitExceeded(80, 60, 100, 80.0)
	require.Len(t, reporter.capturedEvents, 1)
	assert.Contains(t, reporter.capturedEvents[0].message, "80.0%")
}
