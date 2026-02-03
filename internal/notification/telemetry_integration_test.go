package notification

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"testing/synctest"
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
	contexts map[string]any
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

func (m *mockTelemetryReporter) CaptureEvent(message, level string, tags map[string]string, contexts map[string]any) {
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
				Enabled: false,
			},
			reporter:       &mockTelemetryReporter{enabled: true},
			expectedResult: false,
		},
		{
			name: "disabled in reporter",
			config: &TelemetryConfig{
				Enabled: true,
			},
			reporter:       &mockTelemetryReporter{enabled: false},
			expectedResult: false,
		},
		{
			name: "fully enabled",
			config: &TelemetryConfig{
				Enabled: true,
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

	// Create telemetry
	config := DefaultTelemetryConfig()
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
	ctx := t.Context()
	for range 5 {
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
	cbContext, ok := event.contexts["circuit_breaker"].(map[string]any)
	require.True(t, ok, "Circuit breaker context should be present")
	assert.Equal(t, 5, cbContext["failure_threshold"])
	assert.InDelta(t, 30.0, cbContext["timeout_seconds"], 0.001)
	assert.Equal(t, 1, cbContext["half_open_max_requests"])
}

func TestCircuitBreakerTelemetry_Disabled(t *testing.T) {
	// Create mock reporter
	reporter := &mockTelemetryReporter{enabled: true}

	// Create telemetry with telemetry DISABLED
	config := DefaultTelemetryConfig()
	config.Enabled = false
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
	ctx := t.Context()
	for range 5 {
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return assert.AnError
		})
	}

	// Wait a bit
	time.Sleep(50 * time.Millisecond)

	// Verify NO telemetry events captured (telemetry disabled)
	assert.Empty(t, reporter.capturedEvents, "No telemetry events should be captured when disabled")
}

func TestCircuitBreakerTelemetry_Recovery(t *testing.T) {
	// Use synctest for deterministic time-based testing (Go 1.25+)
	synctest.Test(t, func(t *testing.T) {
		// Create mock reporter
		reporter := &mockTelemetryReporter{enabled: true}

		// Create telemetry
		config := DefaultTelemetryConfig()
		telemetry := NewNotificationTelemetry(&config, reporter)

		// Create circuit breaker with timeout for testing
		cbConfig := CircuitBreakerConfig{
			MaxFailures:         3,
			Timeout:             2 * time.Second, // Use reasonable timeout with synctest
			HalfOpenMaxRequests: 1,
		}
		cb := NewPushCircuitBreaker(cbConfig, nil, "test-provider")
		cb.SetTelemetry(telemetry)

		// Trigger failures to open
		ctx := t.Context()
		for range 3 {
			_ = cb.Call(ctx, func(ctx context.Context) error {
				return assert.AnError
			})
		}

		// Wait for timeout - in synctest, this advances the virtual clock instantly
		time.Sleep(3 * time.Second)

		// Successful call should transition to half-open then closed
		err := cb.Call(ctx, func(ctx context.Context) error {
			return nil // Success
		})
		require.NoError(t, err)

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
	})
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
	ctx := t.Context()
	for range 5 {
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

	// Verify defaults
	assert.True(t, config.Enabled)
	assert.InEpsilon(t, 50.0, config.RateLimitReportThreshold, 0.001)
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
	initCtx, ok := event.contexts["initialization"].(map[string]any)
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
	workerState, ok := event.contexts["worker_state"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, uint64(1523), workerState["events_processed"])
	assert.Equal(t, uint64(12), workerState["events_dropped"])
}

func TestWorkerPanicRecovered_Disabled(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	config.Enabled = false
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
	rlCtx, ok := event.contexts["rate_limiter"].(map[string]any)
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
	config.Enabled = false
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

	ctx := t.Context()
	for range 3 {
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
	// Use synctest for deterministic time-based testing (Go 1.25+)
	synctest.Test(t, func(t *testing.T) {
		reporter := &mockTelemetryReporter{enabled: true}
		config := DefaultTelemetryConfig()
		telemetry := NewNotificationTelemetry(&config, reporter)

		cbConfig := CircuitBreakerConfig{
			MaxFailures:         2,
			Timeout:             2 * time.Second, // Use reasonable timeout with synctest
			HalfOpenMaxRequests: 1,
		}
		cb := NewPushCircuitBreaker(cbConfig, nil, "flapping-provider")
		cb.SetTelemetry(telemetry)

		ctx := t.Context()

		// Simulate flapping: closed → open → half-open → open → half-open...
		// 1. Fail twice to open circuit
		for range 2 {
			_ = cb.Call(ctx, func(ctx context.Context) error {
				return fmt.Errorf("failure")
			})
		}

		initialEventCount := len(reporter.capturedEvents)
		assert.GreaterOrEqual(t, initialEventCount, 1, "should capture initial open")

		// 2. Wait for timeout to enter half-open
		time.Sleep(3 * time.Second)

		// 3. Try a request in half-open (will fail and reopen)
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return fmt.Errorf("still failing")
		})

		// 4. Try another transition quickly (should be debounced since within MinTelemetryReportInterval)
		time.Sleep(10 * time.Second) // Less than MinTelemetryReportInterval (30s)
		time.Sleep(3 * time.Second)  // Wait for another timeout
		_ = cb.Call(ctx, func(ctx context.Context) error {
			return fmt.Errorf("still failing")
		})

		// Verify: Should have reported closed→open, but subsequent transitions may be debounced
		// (since they happen within MinTelemetryReportInterval of 30 seconds)
		assert.LessOrEqual(t, len(reporter.capturedEvents), initialEventCount+2,
			"rapid transitions should be debounced")
	})
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

// TestWebhookRequestError_NetworkTimeout tests webhook timeout reporting
func TestWebhookRequestError_NetworkTimeout(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test timeout error
	telemetry.WebhookRequestError(
		"webhook-primary",
		context.DeadlineExceeded,
		0, // No status code for network errors
		"https://hooks.slack.com/services/T00/B00/xyz",
		"POST",
		"bearer",
		true,  // isTimeout
		false, // isCancelled
	)

	// Verify event captured
	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	assert.Contains(t, event.message, "timed out")
	assert.Equal(t, "warning", event.level, "Timeouts should be warning level (often user network issues)")
	assert.Equal(t, "notification", event.tags["component"])
	assert.Equal(t, "webhook-primary", event.tags["provider"])
	assert.Equal(t, "webhook", event.tags["provider_type"])
	assert.Equal(t, "0", event.tags["status_code"])
	assert.Equal(t, "POST", event.tags["method"])
	assert.Equal(t, "bearer", event.tags["auth_type"])
	assert.Equal(t, "true", event.tags["is_timeout"])

	// Verify URL is anonymized (should be hash, not actual URL)
	endpointHash := event.tags["endpoint_hash"]
	assert.Contains(t, endpointHash, "url-")
	assert.NotContains(t, endpointHash, "hooks.slack.com")
	assert.NotContains(t, endpointHash, "T00")

	// Verify contexts
	reqContext, ok := event.contexts["request"].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, "POST", reqContext["method"])
	assert.Equal(t, "bearer", reqContext["auth_type"])
	assert.Equal(t, true, reqContext["is_timeout"])
	assert.Equal(t, false, reqContext["is_cancelled"])
}

// TestWebhookRequestError_HTTPServerError tests 5xx error reporting
func TestWebhookRequestError_HTTPServerError(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test 500 Internal Server Error
	testErr := fmt.Errorf("webhook returned status 500: Internal Server Error")
	telemetry.WebhookRequestError(
		"webhook-discord",
		testErr,
		500,
		"https://discord.com/api/webhooks/123/token",
		"POST",
		"none",
		false, // Not a timeout
		false, // Not cancelled
	)

	// Verify event captured
	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	assert.Contains(t, event.message, "returned status 500")
	assert.Equal(t, "error", event.level, "5xx errors should be error level")
	assert.Equal(t, "webhook-discord", event.tags["provider"])
	assert.Equal(t, "500", event.tags["status_code"])
	assert.Equal(t, "false", event.tags["is_timeout"])
}

// TestWebhookRequestError_HTTPClientError tests 4xx error reporting
func TestWebhookRequestError_HTTPClientError(t *testing.T) {
	config := DefaultTelemetryConfig()

	tests := []struct {
		name       string
		statusCode int
		errorMsg   string
	}{
		{
			name:       "400 Bad Request",
			statusCode: 400,
			errorMsg:   "webhook returned status 400: Bad Request",
		},
		{
			name:       "401 Unauthorized",
			statusCode: 401,
			errorMsg:   "webhook returned status 401: Unauthorized",
		},
		{
			name:       "404 Not Found",
			statusCode: 404,
			errorMsg:   "webhook returned status 404: Not Found",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reporter := &mockTelemetryReporter{enabled: true}
			telemetry := NewNotificationTelemetry(&config, reporter)

			testErr := fmt.Errorf("%s", tt.errorMsg)
			telemetry.WebhookRequestError(
				"webhook-test",
				testErr,
				tt.statusCode,
				"https://example.com/webhook",
				"POST",
				"basic",
				false,
				false,
			)

			require.Len(t, reporter.capturedEvents, 1)
			event := reporter.capturedEvents[0]

			assert.Equal(t, "warning", event.level, "4xx errors should be warning level")
			assert.Equal(t, fmt.Sprintf("%d", tt.statusCode), event.tags["status_code"])
			assert.Contains(t, event.message, fmt.Sprintf("status %d", tt.statusCode))
		})
	}
}

// TestWebhookRequestError_Cancelled tests that cancelled requests are not reported
func TestWebhookRequestError_Cancelled(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test cancelled error
	telemetry.WebhookRequestError(
		"webhook-test",
		context.Canceled,
		0,
		"https://example.com/webhook",
		"POST",
		"bearer",
		false, // Not a timeout
		true,  // Is cancelled
	)

	// Verify NO event captured (cancellations filtered out)
	assert.Empty(t, reporter.capturedEvents, "Cancelled requests should not be reported")
}

// TestWebhookRequestError_Disabled tests disabled webhook error reporting
func TestWebhookRequestError_Disabled(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	config.Enabled = false // Disable telemetry
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Try to report error
	telemetry.WebhookRequestError(
		"webhook-test",
		fmt.Errorf("error"),
		500,
		"https://example.com/webhook",
		"POST",
		"bearer",
		false,
		false,
	)

	// Should not report when disabled
	assert.Empty(t, reporter.capturedEvents)
}

// TestWebhookRequestError_PrivacyScrubbing tests privacy scrubbing of error messages
func TestWebhookRequestError_PrivacyScrubbing(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test with error message containing sensitive paths
	sensitiveErr := fmt.Errorf("failed to read /home/user/.secrets/token.txt: permission denied")
	telemetry.WebhookRequestError(
		"webhook-test",
		sensitiveErr,
		500,
		"https://api.example.com/webhook?token=secret123",
		"POST",
		"bearer",
		false,
		false,
	)

	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	// Verify message is scrubbed (exact scrubbing depends on privacy.ScrubMessage implementation)
	// At minimum, the URL should be anonymized
	assert.NotContains(t, event.tags["endpoint_hash"], "secret123")
	assert.NotContains(t, event.tags["endpoint_hash"], "api.example.com")
	assert.Contains(t, event.tags["endpoint_hash"], "url-")
}

// TestWebhookRequestError_NilError tests handling of nil error (should not panic)
func TestWebhookRequestError_NilError(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test with nil error - should not panic
	telemetry.WebhookRequestError(
		"webhook-test",
		nil, // nil error
		500,
		"https://example.com/webhook",
		"POST",
		"bearer",
		false,
		false,
	)

	require.Len(t, reporter.capturedEvents, 1)
	event := reporter.capturedEvents[0]

	// Should use generic message when err is nil
	assert.Equal(t, "Webhook request failed", event.message)
	assert.Equal(t, "error", event.level)
	assert.Equal(t, "500", event.tags["status_code"])
}

// TestIsConnectionError tests the connection error detection function
func TestIsConnectionError(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error",
			err:      nil,
			expected: false,
		},
		{
			name:     "connection refused",
			err:      errors.New("dial tcp 192.168.1.100:443: connect: connection refused"),
			expected: true,
		},
		{
			name:     "connection reset",
			err:      errors.New("read tcp: connection reset by peer"),
			expected: true,
		},
		{
			name:     "no route to host",
			err:      errors.New("dial tcp 10.0.0.1:8080: connect: no route to host"),
			expected: true,
		},
		{
			name:     "network unreachable",
			err:      errors.New("dial tcp: network is unreachable"),
			expected: true,
		},
		{
			name:     "DNS no such host",
			err:      errors.New("dial tcp: lookup homeassistant.local: no such host"),
			expected: true,
		},
		{
			name:     "DNS lookup failure",
			err:      errors.New("lookup myserver.local on 192.168.1.1:53: server misbehaving"),
			expected: true,
		},
		{
			name:     "i/o timeout",
			err:      errors.New("dial tcp 192.168.1.100:443: i/o timeout"),
			expected: true,
		},
		{
			name:     "broken pipe",
			err:      errors.New("write tcp: broken pipe"),
			expected: true,
		},
		{
			name:     "HTTP 500 error - not connection error",
			err:      errors.New("server returned 500: internal server error"),
			expected: false,
		},
		{
			name:     "authentication error - not connection error",
			err:      errors.New("401 unauthorized"),
			expected: false,
		},
		{
			name:     "generic error - not connection error",
			err:      errors.New("something went wrong"),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isConnectionError(tt.err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestWebhookRequestError_ConnectionErrorsNotReported tests that connection errors are filtered from telemetry
func TestWebhookRequestError_ConnectionErrorsNotReported(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test connection refused - should NOT be reported
	telemetry.WebhookRequestError(
		"homeassistant",
		errors.New("dial tcp 192.168.1.100:443: connect: connection refused"),
		0, // no status code for connection errors
		"https://homeassistant.local/api/webhook/xyz",
		"POST",
		"none",
		false,
		false,
	)

	// No events should be captured - connection errors are user config issues
	assert.Empty(t, reporter.capturedEvents, "Connection refused errors should not be reported to telemetry")

	// Test DNS error - should NOT be reported
	telemetry.WebhookRequestError(
		"homeassistant",
		errors.New("dial tcp: lookup homeassistant.local: no such host"),
		0,
		"https://homeassistant.local/api/webhook/xyz",
		"POST",
		"none",
		false,
		false,
	)

	assert.Empty(t, reporter.capturedEvents, "DNS errors should not be reported to telemetry")

	// Test network unreachable - should NOT be reported
	telemetry.WebhookRequestError(
		"homeassistant",
		errors.New("dial tcp 10.0.0.1:443: network is unreachable"),
		0,
		"https://10.0.0.1/api/webhook/xyz",
		"POST",
		"none",
		false,
		false,
	)

	assert.Empty(t, reporter.capturedEvents, "Network unreachable errors should not be reported to telemetry")
}

// TestWebhookRequestError_ServerErrorsStillReported tests that non-connection errors are still reported
func TestWebhookRequestError_ServerErrorsStillReported(t *testing.T) {
	reporter := &mockTelemetryReporter{enabled: true}
	config := DefaultTelemetryConfig()
	telemetry := NewNotificationTelemetry(&config, reporter)

	// Test HTTP 500 error - SHOULD be reported (server-side issue, potential code problem)
	telemetry.WebhookRequestError(
		"webhook-test",
		errors.New("server returned 500: internal server error"),
		500,
		"https://example.com/webhook",
		"POST",
		"bearer",
		false,
		false,
	)

	// This should be captured
	require.Len(t, reporter.capturedEvents, 1, "HTTP 500 errors should be reported to telemetry")
	assert.Equal(t, "error", reporter.capturedEvents[0].level)
}
