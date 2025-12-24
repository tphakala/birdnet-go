package telemetry

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/getsentry/sentry-go"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
)

// testFlushTimeout is the timeout for flushing events during test cleanup
const testFlushTimeout = 2 * time.Second

// TestConfig holds configuration for telemetry testing
type TestConfig struct {
	MockTransport *MockTransport
	Settings      *conf.Settings
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

	// Mark as initialized and enable test mode
	sentryInitialized = true
	atomic.StoreInt32(&testMode, 1)
	
	// Update telemetry enabled state for test mode
	UpdateTelemetryEnabled()

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
		sentry.Flush(testFlushTimeout)

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

// FakeTimeSource is a time source that returns a fixed time for testing
type FakeTimeSource struct {
	mu          sync.Mutex
	currentTime time.Time
}

// NewFakeTimeSource creates a new FakeTimeSource starting at the given time
func NewFakeTimeSource(t time.Time) *FakeTimeSource {
	return &FakeTimeSource{currentTime: t}
}

// Now returns the current fake time
func (f *FakeTimeSource) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.currentTime
}

// Advance advances the fake time by the given duration
func (f *FakeTimeSource) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.currentTime = f.currentTime.Add(d)
}

// mockSentryReporter is a no-op Sentry reporter for testing (doesn't spawn goroutines)
type mockSentryReporter struct {
	enabled bool
}

func (m *mockSentryReporter) ReportError(_ *errors.EnhancedError) {
	// No-op: don't actually report to Sentry or spawn goroutines
}

func (m *mockSentryReporter) IsEnabled() bool {
	return m.enabled
}

// NewMockSentryReporter creates a mock Sentry reporter for testing
func NewMockSentryReporter(enabled bool) *mockSentryReporter {
	return &mockSentryReporter{enabled: enabled}
}

// mockErrorEvent implements the ErrorEvent interface for testing
type mockErrorEvent struct {
	component string
	category  string
	message   string
	context   map[string]any
	timestamp time.Time
	reported  bool
	mu        sync.RWMutex
}

func (m *mockErrorEvent) GetComponent() string       { return m.component }
func (m *mockErrorEvent) GetCategory() string        { return m.category }
func (m *mockErrorEvent) GetContext() map[string]any { return m.context }
func (m *mockErrorEvent) GetTimestamp() time.Time    { return m.timestamp }
func (m *mockErrorEvent) GetError() error            { return errors.NewStd(m.message) }
func (m *mockErrorEvent) GetMessage() string         { return m.message }
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

// MockErrorEventOption is a functional option for customizing mockErrorEvent
type MockErrorEventOption func(*mockErrorEvent)

// WithCategory sets a custom category
func WithCategory(category string) MockErrorEventOption {
	return func(e *mockErrorEvent) { e.category = category }
}

// WithContext sets custom context
func WithContext(ctx map[string]any) MockErrorEventOption {
	return func(e *mockErrorEvent) { e.context = ctx }
}

// WithTimestamp sets a custom timestamp
func WithTimestamp(ts time.Time) MockErrorEventOption {
	return func(e *mockErrorEvent) { e.timestamp = ts }
}

// WithReported marks the event as already reported
func WithReported() MockErrorEventOption {
	return func(e *mockErrorEvent) { e.reported = true }
}

// NewMockErrorEvent creates a mockErrorEvent with sensible defaults.
// Use functional options to customize.
func NewMockErrorEvent(component, message string, opts ...MockErrorEventOption) *mockErrorEvent {
	event := &mockErrorEvent{
		component: component,
		category:  string(errors.CategorySystem),
		message:   message,
		timestamp: time.Now(),
	}
	for _, opt := range opts {
		opt(event)
	}
	return event
}

