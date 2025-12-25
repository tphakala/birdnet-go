// Go 1.25 improvements:
// - Uses b.Loop() for benchmark iterations
// LLM GUIDANCE: Always use b.Loop() instead of manual for i := 0; i < b.N; i++

package api

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// awaitNotification waits for a notification with timeout.
func awaitNotification(t *testing.T, ch <-chan *notification.Notification, timeout time.Duration) *notification.Notification {
	t.Helper()
	select {
	case notif := <-ch:
		return notif
	case <-time.After(timeout):
		require.Fail(t, "Did not receive notification within timeout")
		return nil
	}
}

// checkToastMarking checks that notification is marked as toast and returns toast ID.
func checkToastMarking(t *testing.T, notif *notification.Notification) string {
	t.Helper()
	isToast, ok := notif.Metadata["isToast"].(bool)
	require.True(t, ok && isToast, "Notification should be marked as toast")
	toastID, ok := notif.Metadata["toastId"].(string)
	require.True(t, ok && toastID != "", "Notification should have toastId in metadata")
	return toastID
}

// checkSSEFields checks that SSE event data contains expected fields.
func checkSSEFields(t *testing.T, eventData, expected map[string]any) {
	t.Helper()
	for field, expectedValue := range expected {
		actualValue, exists := eventData[field]
		assert.True(t, exists, "SSE event data missing field %q", field)
		if exists {
			assert.Equal(t, expectedValue, actualValue, "SSE event data field %q mismatch", field)
		}
	}
}

// checkSSERequired checks that SSE event data has all required fields and recent timestamp.
func checkSSERequired(t *testing.T, eventData map[string]any) {
	t.Helper()
	for _, field := range []string{"id", "message", "type", "timestamp"} {
		_, exists := eventData[field]
		assert.True(t, exists, "SSE event data missing required field %q", field)
	}
	timestamp, ok := eventData["timestamp"].(time.Time)
	require.True(t, ok, "SSE event timestamp should be a time.Time")
	assert.WithinDuration(t, time.Now(), timestamp, time.Second, "SSE event timestamp should be recent")
}

// TestToastIntegrationFlow tests the complete flow:
// SendToast -> notification creation -> SSE event data creation
func TestToastIntegrationFlow(t *testing.T) {
	config := notification.DefaultServiceConfig()
	if !notification.IsInitialized() {
		notification.Initialize(config)
	}

	service := notification.GetService()
	c := mockController()

	testCases := []struct {
		name              string
		message           string
		toastType         string
		duration          int
		expectedSSEFields map[string]any
	}{
		{
			name:      "success toast complete flow",
			message:   "Operation completed successfully",
			toastType: "success",
			duration:  3000,
			expectedSSEFields: map[string]any{
				"message": "Operation completed successfully", "type": "success",
				"duration": 3000, "component": "api",
			},
		},
		{
			name:      "error toast complete flow",
			message:   "Operation failed with error",
			toastType: "error",
			duration:  5000,
			expectedSSEFields: map[string]any{
				"message": "Operation failed with error", "type": "error",
				"duration": 5000, "component": "api",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			notifCh, _ := service.Subscribe()
			defer service.Unsubscribe(notifCh)

			err := c.SendToast(tc.message, tc.toastType, tc.duration)
			require.NoError(t, err, "SendToast() error")

			capturedNotif := awaitNotification(t, notifCh, 100*time.Millisecond)
			toastID := checkToastMarking(t, capturedNotif)
			sseEventData := c.createToastEventData(capturedNotif)

			checkSSEFields(t, sseEventData, tc.expectedSSEFields)
			checkSSERequired(t, sseEventData)

			assert.Equal(t, toastID, sseEventData["id"], "SSE event ID should match toast ID")
		})
	}
}

// TestToastEventDataEdgeCases tests edge cases in SSE event data creation
func TestToastEventDataEdgeCases(t *testing.T) {
	c := mockController()

	t.Run("notification without toast metadata", func(t *testing.T) {
		notif := notification.NewNotification(
			notification.TypeInfo,
			notification.PriorityLow,
			"Regular notification",
			"This is not a toast",
		)
		eventData := c.createToastEventData(notif)

		assertEventDataNil(t, eventData, "id", "non-toast notification")
		assertEventDataEmpty(t, eventData, "type", "non-toast notification")
	})

	t.Run("notification with partial toast metadata", func(t *testing.T) {
		notif := notification.NewNotification(
			notification.TypeInfo,
			notification.PriorityLow,
			"Partial toast",
			"Missing some metadata",
		).WithMetadata("isToast", true).
			WithMetadata("toastType", "info")

		eventData := c.createToastEventData(notif)

		assertEventDataNil(t, eventData, "id", "missing toastId")
		assertEventDataValue(t, eventData, "type", "info")
		assertEventDataMissing(t, eventData, "duration")
		assertEventDataMissing(t, eventData, "action")
	})

	t.Run("notification with nil metadata", func(t *testing.T) {
		notif := &notification.Notification{
			ID:       "test-id",
			Message:  "Nil metadata test",
			Metadata: nil,
		}
		eventData := c.createToastEventData(notif)

		assertEventDataNil(t, eventData, "id", "nil metadata")
		assertEventDataEmpty(t, eventData, "type", "nil metadata")
	})
}

// TestToastToSSEEventConsistency verifies that toast data is consistently
// represented from creation through to SSE event data
func TestToastToSSEEventConsistency(t *testing.T) {
	// Create a toast with all fields populated
	originalToast := notification.NewToast("Consistency test message", notification.ToastTypeWarning).
		WithComponent("test-component").
		WithDuration(4000).
		WithAction("Test Action", "/test", "testHandler")

	// Convert to notification
	notif := originalToast.ToNotification()

	// Create SSE event data
	c := mockController()
	eventData := c.createToastEventData(notif)

	// Verify consistency
	assert.Equal(t, originalToast.Message, eventData["message"], "Message inconsistency")
	assert.Equal(t, string(originalToast.Type), eventData["type"], "Type inconsistency")
	assert.Equal(t, originalToast.Duration, eventData["duration"], "Duration inconsistency")
	assert.Equal(t, originalToast.Component, eventData["component"], "Component inconsistency")
	assert.Equal(t, originalToast.ID, eventData["id"], "ID inconsistency")

	// Verify action consistency
	assert.Equal(t, originalToast.Action, eventData["action"], "Action inconsistency")
}

// BenchmarkCompleteToastFlow benchmarks the complete toast flow
func BenchmarkCompleteToastFlow(b *testing.B) {
	// Initialize notification service
	config := notification.DefaultServiceConfig()
	if !notification.IsInitialized() {
		notification.Initialize(config)
	}

	service := notification.GetService()
	c := mockController()

	// Subscribe to notifications (needed for SendToast to work)
	notifCh, _ := service.Subscribe()
	defer service.Unsubscribe(notifCh)

	b.ResetTimer()

	// Use b.Loop() for benchmark iteration (Go 1.25)
	for b.Loop() {
		// Step 1: Send toast
		err := c.SendToast("Benchmark message", "info", 3000)
		require.NoError(b, err, "SendToast error")

		// Step 2: Receive notification
		select {
		case notif := <-notifCh:
			// Step 3: Create SSE event data
			_ = c.createToastEventData(notif)
		case <-time.After(10 * time.Millisecond):
			require.Fail(b, "Timeout waiting for notification")
		}
	}
}
