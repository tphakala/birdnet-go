//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/events"
)

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

// newMockErrorEvent creates a mock error event for testing.
func newMockErrorEvent(component, category, message string) *mockErrorEvent {
	return &mockErrorEvent{
		component: component,
		category:  category,
		message:   message,
		timestamp: time.Now(),
	}
}

func TestNotificationWorker_ProcessEvent(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		event          events.ErrorEvent
		expectNotif    bool
		expectPriority Priority
	}{
		{
			name:           "critical_error_creates_notification",
			event:          newMockErrorEvent("database", string(errors.CategoryDatabase), "Database connection failed"),
			expectNotif:    true,
			expectPriority: PriorityCritical,
		},
		{
			name:           "high_priority_error_creates_notification",
			event:          newMockErrorEvent("system", string(errors.CategorySystem), "High memory usage detected"),
			expectNotif:    true,
			expectPriority: PriorityHigh,
		},
		{
			name:           "medium_priority_error_creates_notification",
			event:          newMockErrorEvent("network", string(errors.CategoryNetwork), "Temporary network issue"),
			expectNotif:    true,
			expectPriority: PriorityMedium,
		},
		{
			name:        "low_priority_error_skipped",
			event:       newMockErrorEvent("validation", string(errors.CategoryValidation), "Invalid input parameter"),
			expectNotif: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			service := newTestService(t)
			defer service.Stop()

			worker, err := NewNotificationWorker(service, nil)
			require.NoError(t, err, "Failed to create worker")

			err = worker.ProcessEvent(tt.event)
			require.NoError(t, err, "ProcessEvent should not fail")

			notifications, err := service.List(&FilterOptions{
				Types: []Type{TypeError},
			})
			require.NoError(t, err, "Failed to list notifications")

			if tt.expectNotif {
				require.Len(t, notifications, 1, "Expected 1 notification")
				notif := notifications[0]
				assert.Equal(t, tt.expectPriority, notif.Priority, "Priority mismatch")
				assert.Equal(t, tt.event.GetComponent(), notif.Component, "Component mismatch")
			} else {
				assert.Empty(t, notifications, "Expected no notifications")
			}
		})
	}
}

func TestNotificationWorker_CircuitBreaker(t *testing.T) {
	t.Parallel()

	// Create service with very low rate limit to trigger failures
	service := newTestServiceForRateLimiting(t, 2)
	defer service.Stop()

	config := &WorkerConfig{
		BatchingEnabled:   false,
		FailureThreshold:  3,
		RecoveryTimeout:   100 * time.Millisecond,
		HalfOpenMaxEvents: 2,
	}

	worker, err := NewNotificationWorker(service, config)
	require.NoError(t, err, "Failed to create worker")

	event := newMockErrorEvent("database", string(errors.CategoryDatabase), "Critical database error")

	// Process events until circuit opens
	successCount := 0
	for range 10 {
		if err := worker.ProcessEvent(event); err == nil {
			successCount++
		}
	}

	stats := worker.GetStats()
	t.Logf("Circuit breaker stats: state=%s, processed=%d, dropped=%d, failed=%d, success=%d",
		stats.CircuitState, stats.EventsProcessed, stats.EventsDropped, stats.EventsFailed, successCount)

	// Verify that rate limiting is working
	assert.True(t, stats.EventsProcessed > 0 || stats.EventsDropped > 0,
		"Expected some events to be processed or dropped")

	// If circuit is open, test recovery behavior
	if stats.CircuitState == "open" {
		waitForCondition(t, 200*time.Millisecond, func() bool {
			return worker.circuitBreaker.Allow()
		}, "circuit breaker should allow request after recovery timeout")
	}
}

func TestNotificationWorker_TemplateGeneration(t *testing.T) {
	t.Parallel()

	service := NewService(nil)
	defer service.Stop()

	worker, err := NewNotificationWorker(service, nil)
	require.NoError(t, err, "Failed to create worker")

	tests := []struct {
		name            string
		event           events.ErrorEvent
		priority        Priority
		expectedTitle   string
		expectedMessage string
	}{
		{
			name:            "critical_title_and_message",
			event:           newMockErrorEvent("database", string(errors.CategoryDatabase), "Connection failed"),
			priority:        PriorityCritical,
			expectedTitle:   "Critical database Error in database",
			expectedMessage: "Critical database error in database: Connection failed",
		},
		{
			name:            "high_title_and_message",
			event:           newMockErrorEvent("network", string(errors.CategoryNetwork), "Timeout occurred"),
			priority:        PriorityHigh,
			expectedTitle:   "network Error in network",
			expectedMessage: "network error in network: Timeout occurred",
		},
		{
			name:            "medium_title_and_message",
			event:           newMockErrorEvent("audio", string(errors.CategoryAudio), "Buffer underrun"),
			priority:        PriorityMedium,
			expectedTitle:   "audio Issue",
			expectedMessage: "audio reported: Buffer underrun",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			title := worker.generateTitle(tt.event, tt.priority)
			assert.Equal(t, tt.expectedTitle, title, "Title mismatch")

			message := worker.generateMessage(tt.event, tt.priority)
			assert.Equal(t, tt.expectedMessage, message, "Message mismatch")
		})
	}
}

func TestNotificationWorker_MessageTruncation(t *testing.T) {
	t.Parallel()

	service := NewService(nil)
	defer service.Stop()

	worker, err := NewNotificationWorker(service, nil)
	require.NoError(t, err, "Failed to create worker")

	// Create event with very long message
	longMessage := strings.Repeat("a", 1000)
	event := newMockErrorEvent("test", string(errors.CategoryGeneric), longMessage)

	message := worker.generateMessage(event, PriorityHigh)

	assert.LessOrEqual(t, len(message), 500, "Message should be truncated to 500 chars")
	assert.True(t, strings.HasSuffix(message, "..."), "Truncated message should end with '...'")
}

func TestNotificationWorker_BatchProcessing(t *testing.T) {
	t.Parallel()

	service := newTestService(t)
	defer service.Stop()

	config := &WorkerConfig{
		BatchingEnabled: true,
		BatchSize:       10,
		BatchTimeout:    100 * time.Millisecond,
	}

	worker, err := NewNotificationWorker(service, config)
	require.NoError(t, err, "Failed to create worker")

	// Create multiple events for batch processing
	errorEvents := []events.ErrorEvent{
		// Same component and category - should be grouped
		newMockErrorEvent("database", string(errors.CategoryDatabase), "Connection failed"),
		newMockErrorEvent("database", string(errors.CategoryDatabase), "Connection timeout"),
		newMockErrorEvent("database", string(errors.CategoryDatabase), "Connection failed"), // Duplicate message
		// Different component - should be separate group
		newMockErrorEvent("system", string(errors.CategorySystem), "High memory usage"),
		// Low priority - should be skipped
		newMockErrorEvent("validation", string(errors.CategoryValidation), "Invalid input"),
	}

	err = worker.ProcessBatch(errorEvents)
	require.NoError(t, err, "ProcessBatch should not fail")

	notifications, err := service.List(&FilterOptions{
		Types: []Type{TypeError},
	})
	require.NoError(t, err, "Failed to list notifications")

	// Should have 2 notifications (database group + system)
	assert.Len(t, notifications, 2, "Expected 2 notifications (grouped)")

	// Verify aggregation in titles
	for _, notif := range notifications {
		if notif.Component == "database" {
			assert.Contains(t, notif.Title, "occurrences",
				"Database notification should show occurrences in title")
			assert.Contains(t, notif.Message, "Multiple",
				"Database notification should mention multiple errors")
		}
	}

	// Check stats
	stats := worker.GetStats()
	assert.Equal(t, uint64(4), stats.EventsProcessed,
		"Should process 4 events (5 - 1 low priority)")
}
