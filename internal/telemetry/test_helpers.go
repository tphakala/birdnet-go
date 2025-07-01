package telemetry

import (
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestConfig holds configuration for telemetry testing
type TestConfig struct {
	MockTransport *MockTransport
	Settings      *conf.Settings
}

// TestingTB is a common interface for *testing.T and *testing.B
type TestingTB interface {
	Helper()
	Fatalf(format string, args ...interface{})
	Logf(format string, args ...interface{})
}

// InitForTesting initializes telemetry with a mock transport for testing
// This ensures tests don't send real data to Sentry
func InitForTesting(t TestingTB) (config *TestConfig, cleanup func()) {
	t.Helper()

	// Create mock transport
	mockTransport := NewMockTransport()

	// Create test settings
	testSettings := &conf.Settings{
		Debug:    true,
		Version:  "test-version",
		SystemID: "test-system-id",
		Sentry: conf.SentrySettings{
			Enabled: true,
		},
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

	// Mark as initialized
	sentryInitialized = true

	// Configure scope with test data
	sentry.ConfigureScope(func(scope *sentry.Scope) {
		scope.SetTag("system_id", testSettings.SystemID)
		scope.SetTag("test_mode", "true")
		scope.SetContext("application", map[string]any{
			"name":      "BirdNET-Go",
			"version":   testSettings.Version,
			"system_id": testSettings.SystemID,
			"test_mode": true,
		})
	})

	// Cleanup function
	cleanup = func() {
		// Flush any pending events
		sentry.Flush(2 * time.Second)
		
		// Reset initialization state
		sentryInitialized = false
		
		// Clear deferred messages
		deferredMutex.Lock()
		deferredMessages = nil
		deferredMutex.Unlock()
	}

	return &TestConfig{
		MockTransport: mockTransport,
		Settings:      testSettings,
	}, cleanup
}

// AssertEventCaptured verifies that an event with the given message was captured
func AssertEventCaptured(t *testing.T, transport *MockTransport, message string, timeout time.Duration) {
	t.Helper()
	
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if transport.FindEventByMessage(message) != nil {
			return // Success
		}
		time.Sleep(10 * time.Millisecond)
	}
	
	// Failed to find event
	events := transport.GetEventMessages()
	t.Errorf("Expected event with message %q not found. Captured events: %v", message, events)
}

// AssertNoEvents verifies that no events were captured
func AssertNoEvents(t *testing.T, transport *MockTransport) {
	t.Helper()
	
	if count := transport.GetEventCount(); count > 0 {
		events := transport.GetEventMessages()
		t.Errorf("Expected no events, but found %d: %v", count, events)
	}
}

// AssertEventCount verifies the exact number of events captured
func AssertEventCount(t *testing.T, transport *MockTransport, expected int, timeout time.Duration) {
	t.Helper()
	
	if !transport.WaitForEventCount(expected, timeout) {
		actual := transport.GetEventCount()
		events := transport.GetEventMessages()
		t.Errorf("Expected %d events, but found %d: %v", expected, actual, events)
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