package api

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/notification"
)

// TestToastIntegrationFlow tests the complete flow:
// SendToast -> notification creation -> SSE event data creation
func TestToastIntegrationFlow(t *testing.T) {
	// Initialize notification service
	config := notification.DefaultServiceConfig()
	if !notification.IsInitialized() {
		notification.Initialize(config)
	}

	service := notification.GetService()
	c := mockController()

	// Test complete flow for different toast types
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
				"message": "Operation completed successfully",
				"type":    "success",
				"duration": 3000,
				"component": "api",
			},
		},
		{
			name:      "error toast complete flow",
			message:   "Operation failed with error",
			toastType: "error",
			duration:  5000,
			expectedSSEFields: map[string]any{
				"message": "Operation failed with error",
				"type":    "error",
				"duration": 5000,
				"component": "api",
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Step 1: Subscribe to notifications to capture what's created
			notifCh, _ := service.Subscribe()
			defer service.Unsubscribe(notifCh)

			// Step 2: Send toast via API helper
			err := c.SendToast(tc.message, tc.toastType, tc.duration)
			if err != nil {
				t.Fatalf("SendToast() error = %v", err)
			}

			// Step 3: Capture the notification created
			var capturedNotif *notification.Notification
			select {
			case capturedNotif = <-notifCh:
				// Got the notification
			case <-time.After(100 * time.Millisecond):
				t.Fatal("Did not receive notification within timeout")
			}

			// Step 4: Verify notification is properly marked as toast
			isToast, ok := capturedNotif.Metadata["isToast"].(bool)
			if !ok || !isToast {
				t.Error("Notification should be marked as toast")
			}

			// Step 5: Process through SSE event data creation
			sseEventData := c.createToastEventData(capturedNotif)

			// Step 6: Verify SSE event data has correct fields
			for field, expectedValue := range tc.expectedSSEFields {
				actualValue, exists := sseEventData[field]
				if !exists {
					t.Errorf("SSE event data missing field %q", field)
					continue
				}

				if actualValue != expectedValue {
					t.Errorf("SSE event data field %q = %v, want %v", field, actualValue, expectedValue)
				}
			}

			// Step 7: Verify SSE event has required fields
			requiredFields := []string{"id", "message", "type", "timestamp"}
			for _, field := range requiredFields {
				if _, exists := sseEventData[field]; !exists {
					t.Errorf("SSE event data missing required field %q", field)
				}
			}

			// Step 8: Verify timestamp is recent
			if timestamp, ok := sseEventData["timestamp"].(time.Time); ok {
				if time.Since(timestamp) > time.Second {
					t.Error("SSE event timestamp should be recent")
				}
			} else {
				t.Error("SSE event timestamp should be a time.Time")
			}

			// Step 9: Verify toast ID is carried through
			toastID, ok := capturedNotif.Metadata["toastId"].(string)
			if !ok || toastID == "" {
				t.Error("Notification should have toastId in metadata")
			}

			sseID := sseEventData["id"]
			if sseID != toastID {
				t.Errorf("SSE event ID %v should match toast ID %v", sseID, toastID)
			}
		})
	}
}

// TestToastEventDataEdgeCases tests edge cases in SSE event data creation
func TestToastEventDataEdgeCases(t *testing.T) {
	c := mockController()

	t.Run("notification without toast metadata", func(t *testing.T) {
		// Create a regular notification without toast metadata
		notif := notification.NewNotification(
			notification.TypeInfo,
			notification.PriorityLow,
			"Regular notification",
			"This is not a toast",
		)

		// Should not panic, but will have empty/nil values
		eventData := c.createToastEventData(notif)

		// Should have basic structure but with nil/empty values
		if eventData["id"] != nil {
			t.Error("Event data ID should be nil for non-toast notification")
		}

		if eventData["type"] != "" {
			t.Errorf("Event data type should be empty string, got %v", eventData["type"])
		}
	})

	t.Run("notification with partial toast metadata", func(t *testing.T) {
		// Create notification with some but not all toast metadata
		notif := notification.NewNotification(
			notification.TypeInfo,
			notification.PriorityLow,
			"Partial toast",
			"Missing some metadata",
		).WithMetadata("isToast", true).
			WithMetadata("toastType", "info")
		// Missing toastId, duration, action

		eventData := c.createToastEventData(notif)

		// Should handle missing metadata gracefully
		if eventData["id"] != nil {
			t.Error("Event data ID should be nil when toastId missing")
		}

		if eventData["type"] != "info" {
			t.Errorf("Event data type should be 'info', got %v", eventData["type"])
		}

		if _, hasDuration := eventData["duration"]; hasDuration {
			t.Error("Event data should not include duration when not set")
		}

		if _, hasAction := eventData["action"]; hasAction {
			t.Error("Event data should not include action when not set")
		}
	})

	t.Run("notification with nil metadata", func(t *testing.T) {
		// Create notification with nil metadata
		notif := &notification.Notification{
			ID:       "test-id",
			Message:  "Nil metadata test",
			Metadata: nil,
		}

		// Should not panic
		eventData := c.createToastEventData(notif)

		// All extracted values should be nil or empty
		if eventData["id"] != nil {
			t.Error("Event data ID should be nil when metadata is nil")
		}

		if eventData["type"] != "" {
			t.Error("Event data type should be empty when metadata is nil")
		}
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
	if eventData["message"] != originalToast.Message {
		t.Errorf("Message inconsistency: SSE=%v, Toast=%v", eventData["message"], originalToast.Message)
	}

	if eventData["type"] != string(originalToast.Type) {
		t.Errorf("Type inconsistency: SSE=%v, Toast=%v", eventData["type"], string(originalToast.Type))
	}

	if eventData["duration"] != originalToast.Duration {
		t.Errorf("Duration inconsistency: SSE=%v, Toast=%v", eventData["duration"], originalToast.Duration)
	}

	if eventData["component"] != originalToast.Component {
		t.Errorf("Component inconsistency: SSE=%v, Toast=%v", eventData["component"], originalToast.Component)
	}

	if eventData["id"] != originalToast.ID {
		t.Errorf("ID inconsistency: SSE=%v, Toast=%v", eventData["id"], originalToast.ID)
	}

	// Verify action consistency
	eventAction := eventData["action"]
	if eventAction != originalToast.Action {
		t.Errorf("Action inconsistency: SSE=%v, Toast=%v", eventAction, originalToast.Action)
	}
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

	for i := 0; i < b.N; i++ {
		// Step 1: Send toast
		err := c.SendToast("Benchmark message", "info", 3000)
		if err != nil {
			b.Fatalf("SendToast error: %v", err)
		}

		// Step 2: Receive notification
		select {
		case notif := <-notifCh:
			// Step 3: Create SSE event data
			_ = c.createToastEventData(notif)
		case <-time.After(10 * time.Millisecond):
			b.Fatal("Timeout waiting for notification")
		}
	}
}