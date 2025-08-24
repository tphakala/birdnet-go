package telemetry

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
	runtimectx "github.com/tphakala/birdnet-go/internal/runtime"
)

// TestConfig holds configuration for telemetry testing
type TestConfig struct {
	MockTransport *MockTransport
	Settings      *conf.Settings
	Runtime       *runtimectx.Context
}

// TestingTB is a common interface for *testing.T and *testing.B
type TestingTB interface {
	Helper()
	Fatalf(format string, args ...any)
	Logf(format string, args ...any)
}

// InitForTesting initializes telemetry with a mock transport for testing
// This ensures tests don't send real data to Sentry
func InitForTesting(t TestingTB) (config *TestConfig, cleanup func()) {
	t.Helper()

	// Create mock transport
	mockTransport := NewMockTransport()

	// Create test settings and runtime
	testSettings := &conf.Settings{
		Debug: true,
		Sentry: conf.SentrySettings{
			Enabled: true,
		},
	}
	
	testRuntime := &runtimectx.Context{
		Version:  "test-version",
		SystemID: "test-system-id",
	}

	// Initialize Sentry with mock transport
	err := sentry.Init(sentry.ClientOptions{
		Dsn:              "", // Empty DSN prevents real connection
		Transport:        mockTransport,
		Debug:            false,
		AttachStacktrace: true,
		Environment:      "test",
		Release:          "birdnet-go@test",
		SampleRate:       1.0,
		TracesSampleRate: 1.0,
		BeforeSend: func(event *sentry.Event, hint *sentry.EventHint) *sentry.Event {
			// Test mode: preserve all data for verification
			return event
		},
	})
	if err != nil {
		t.Fatalf("Failed to initialize Sentry for testing: %v", err)
	}

	// Mark as initialized and enable test mode
	sentryInitialized = true
	atomic.StoreInt32(&testMode, 1)
	
	// Update telemetry enabled state for test mode
	UpdateTelemetryEnabled()

	// Configure scope with test data
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("system_id", testRuntime.SystemID)
		scope.SetTag("test_mode", "true")
		scope.SetContext("application", map[string]any{
			"name":      "BirdNET-Go",
			"version":   testRuntime.Version,
			"system_id": testRuntime.SystemID,
			"test_mode": true,
		})
	})

	// Initialize event bus for async processing
	eventBusConfig := events.DefaultConfig()
	eventBusConfig.Deduplication.Enabled = false // Disable deduplication for predictable testing
	_, err = events.Initialize(eventBusConfig)
	if err != nil {
		t.Fatalf("Failed to initialize event bus for testing: %v", err)
	}

	// Set up event bus integration for errors package
	if events.IsInitialized() {
		eventBus := events.GetEventBus()
		adapter := events.NewEventPublisherAdapter(eventBus)
		errors.SetEventPublisher(adapter)
	}

	// Initialize error integration
	InitializeErrorIntegration()

	// Initialize telemetry event bus integration
	if err := InitializeEventBusIntegration(); err != nil {
		t.Fatalf("Failed to initialize telemetry event bus integration: %v", err)
	}

	// Cleanup function
	cleanup = func() {
		// Reset telemetry worker first to avoid registration conflicts
		telemetryWorker = nil
		telemetryInitialized.Store(false)

		// Use the testing reset function which properly cleans up global state
		events.ResetForTesting()

		// Flush any pending events
		sentry.Flush(2 * time.Second)

		// Reset initialization state
		sentryInitialized = false
		atomic.StoreInt32(&testMode, 0)

		// Clear deferred messages
		deferredMutex.Lock()
		deferredMessages = nil
		deferredMutex.Unlock()

		// Clear error hooks
		errors.ClearErrorHooks()

		// Note: We don't reset the event publisher to nil as atomic.Value doesn't accept nil
		// The next test will override it if needed
	}

	return &TestConfig{
		MockTransport: mockTransport,
		Settings:      testSettings,
		Runtime:       testRuntime,
	}, cleanup
}

// AssertEventCaptured verifies that an event with the given message was captured
func AssertEventCaptured(t *testing.T, transport *MockTransport, message string, timeout time.Duration) {
	t.Helper()

	// Wait for at least one event first
	if !transport.WaitForEventCount(1, timeout) {
		capturedEvents := transport.GetEventMessages()
		t.Errorf("Expected event with message %q not found within timeout. Captured events: %v", message, capturedEvents)
		return
	}

	// Then check if our specific message exists
	if transport.FindEventByMessage(message) == nil {
		capturedEvents := transport.GetEventMessages()
		t.Errorf("Expected event with message %q not found. Captured events: %v", message, capturedEvents)
	}
}

// AssertNoEvents verifies that no events were captured
func AssertNoEvents(t *testing.T, transport *MockTransport) {
	t.Helper()

	if count := transport.GetEventCount(); count > 0 {
		capturedEvents := transport.GetEventMessages()
		t.Errorf("Expected no events, but found %d: %v", count, capturedEvents)
	}
}

// AssertEventCount verifies the exact number of events captured
func AssertEventCount(t *testing.T, transport *MockTransport, expected int, timeout time.Duration) {
	t.Helper()

	if !transport.WaitForEventCount(expected, timeout) {
		actual := transport.GetEventCount()
		capturedEvents := transport.GetEventMessages()
		t.Errorf("Expected %d events, but found %d: %v", expected, actual, capturedEvents)
	}
}

// AssertEventLevel verifies that an event has the expected level
func AssertEventLevel(t *testing.T, transport *MockTransport, message string, expectedLevel sentry.Level) {
	t.Helper()

	event := transport.FindEventByMessage(message)
	if event == nil {
		t.Errorf("Event with message %q not found", message)
		return
	}

	if event.Level != expectedLevel {
		t.Errorf("Expected event level %s, got %s", expectedLevel, event.Level)
	}
}

// AssertEventTag verifies that an event has a specific tag value
func AssertEventTag(t *testing.T, transport *MockTransport, message, tagKey, expectedValue string) {
	t.Helper()

	event := transport.FindEventByMessage(message)
	if event == nil {
		t.Errorf("Event with message %q not found", message)
		return
	}

	if value, ok := event.Tags[tagKey]; !ok || value != expectedValue {
		t.Errorf("Expected tag %s=%s, got %s=%s", tagKey, expectedValue, tagKey, value)
	}
}

