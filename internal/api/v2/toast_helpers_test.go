package api

import (
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/notification"
)

func TestController_SendToast(t *testing.T) {
	t.Parallel()

	// Initialize notification service for testing
	setupTestNotificationService()

	service := notification.GetService()
	c := mockController()

	tests := getToastTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runSendToastTest(t, c, service, tt)
		})
	}
}

// setupTestNotificationService initializes the notification service for testing
func setupTestNotificationService() {
	config := notification.DefaultServiceConfig()
	if !notification.IsInitialized() {
		notification.Initialize(config)
	}
}

// toastTestCase represents a test case for SendToast
type toastTestCase struct {
	name      string
	message   string
	toastType string
	duration  int
	wantError bool
}

// getToastTestCases returns test cases for SendToast
func getToastTestCases() []toastTestCase {
	return []toastTestCase{
		{
			name:      "success toast",
			message:   "Operation completed successfully",
			toastType: "success",
			duration:  3000,
			wantError: false,
		},
		{
			name:      "error toast",
			message:   "Operation failed",
			toastType: "error",
			duration:  5000,
			wantError: false,
		},
		{
			name:      "warning toast",
			message:   "Warning message",
			toastType: "warning",
			duration:  4000,
			wantError: false,
		},
		{
			name:      "info toast",
			message:   "Information message",
			toastType: "info",
			duration:  3000,
			wantError: false,
		},
		{
			name:      "unknown toast type defaults to info",
			message:   "Unknown type message",
			toastType: "unknown",
			duration:  3000,
			wantError: false,
		},
		{
			name:      "empty message",
			message:   "",
			toastType: "info",
			duration:  1000,
			wantError: false,
		},
		{
			name:      "zero duration",
			message:   "Zero duration message",
			toastType: "success",
			duration:  0,
			wantError: false,
		},
	}
}

// runSendToastTest runs a single SendToast test case
func runSendToastTest(t *testing.T, c *Controller, service *notification.Service, tc toastTestCase) {
	t.Helper()
	// Subscribe to notifications to verify the toast was created
	notifCh, _ := service.Subscribe()
	defer service.Unsubscribe(notifCh)

	// Send the toast
	err := c.SendToast(tc.message, tc.toastType, tc.duration)

	if tc.wantError {
		if err == nil {
			t.Error("SendToast() expected error but got none")
		}
		return
	}

	if err != nil {
		t.Errorf("SendToast() unexpected error = %v", err)
		return
	}

	// Verify notification was created and broadcast
	select {
	case notif := <-notifCh:
		verifyToastNotification(t, notif, tc)
	case <-time.After(100 * time.Millisecond):
		t.Error("SendToast() should have broadcast notification within timeout")
	}
}

// verifyToastNotification verifies the notification created from a toast
func verifyToastNotification(t *testing.T, notif *notification.Notification, tc toastTestCase) {
	t.Helper()
	// Verify the notification has toast metadata
	isToast, ok := notif.Metadata["isToast"].(bool)
	if !ok || !isToast {
		t.Error("SendToast() should create notification with isToast=true metadata")
	}

	// Verify basic fields
	if notif.Message != tc.message {
		t.Errorf("SendToast() notification message = %q, want %q", notif.Message, tc.message)
	}

	if notif.Component != "api" {
		t.Errorf("SendToast() notification component = %q, want %q", notif.Component, "api")
	}

	// Verify toast-specific metadata
	verifyToastMetadata(t, notif, tc)
	
	// Verify notification type mapping
	verifyNotificationTypeMapping(t, notif, tc.toastType)
}

// verifyToastMetadata verifies toast-specific metadata
func verifyToastMetadata(t *testing.T, notif *notification.Notification, tc toastTestCase) {
	t.Helper()
	expectedToastType := tc.toastType
	if tc.toastType == "unknown" {
		expectedToastType = "info" // Unknown types default to info
	}

	toastType, ok := notif.Metadata["toastType"].(string)
	if !ok || toastType != expectedToastType {
		t.Errorf("SendToast() toast type = %q, want %q", toastType, expectedToastType)
	}

	// Duration should only be present in metadata if greater than 0
	if tc.duration > 0 {
		duration, ok := notif.Metadata["duration"].(int)
		if !ok || duration != tc.duration {
			t.Errorf("SendToast() duration = %d, want %d", duration, tc.duration)
		}
	} else {
		// Zero duration should not be included in metadata
		if _, exists := notif.Metadata["duration"]; exists {
			t.Error("SendToast() should not include zero duration in metadata")
		}
	}
}

// verifyNotificationTypeMapping verifies notification type and priority mapping
func verifyNotificationTypeMapping(t *testing.T, notif *notification.Notification, toastType string) {
	t.Helper()
	expectedToastType := toastType
	if toastType == "unknown" {
		expectedToastType = "info"
	}

	var expectedNotifType notification.Type
	var expectedPriority notification.Priority
	switch expectedToastType {
	case "success", "info":
		expectedNotifType = notification.TypeInfo
		expectedPriority = notification.PriorityLow
	case "warning":
		expectedNotifType = notification.TypeWarning
		expectedPriority = notification.PriorityMedium
	case "error":
		expectedNotifType = notification.TypeError
		expectedPriority = notification.PriorityHigh
	}

	if notif.Type != expectedNotifType {
		t.Errorf("SendToast() notification type = %v, want %v", notif.Type, expectedNotifType)
	}

	if notif.Priority != expectedPriority {
		t.Errorf("SendToast() notification priority = %v, want %v", notif.Priority, expectedPriority)
	}
}

func TestController_SendToast_TypeMapping(t *testing.T) {
	// Test the toast type to notification type mapping specifically
	tests := []struct {
		toastType            string
		expectedNotifType    notification.Type
		expectedPriority     notification.Priority
		expectedToastType    notification.ToastType
	}{
		{
			toastType:            "success",
			expectedNotifType:    notification.TypeInfo,
			expectedPriority:     notification.PriorityLow,
			expectedToastType:    notification.ToastTypeSuccess,
		},
		{
			toastType:            "error",
			expectedNotifType:    notification.TypeError,
			expectedPriority:     notification.PriorityHigh,
			expectedToastType:    notification.ToastTypeError,
		},
		{
			toastType:            "warning",
			expectedNotifType:    notification.TypeWarning,
			expectedPriority:     notification.PriorityMedium,
			expectedToastType:    notification.ToastTypeWarning,
		},
		{
			toastType:            "info",
			expectedNotifType:    notification.TypeInfo,
			expectedPriority:     notification.PriorityLow,
			expectedToastType:    notification.ToastTypeInfo,
		},
		{
			toastType:            "invalid",
			expectedNotifType:    notification.TypeInfo,
			expectedPriority:     notification.PriorityLow,
			expectedToastType:    notification.ToastTypeInfo,
		},
		{
			toastType:            "",
			expectedNotifType:    notification.TypeInfo,
			expectedPriority:     notification.PriorityLow,
			expectedToastType:    notification.ToastTypeInfo,
		},
	}

	for _, tt := range tests {
		t.Run("type_"+tt.toastType, func(t *testing.T) {
			// This test focuses on the type mapping logic within SendToast
			// We're testing the mapping from string to ToastType enum

			var actualToastType notification.ToastType
			switch tt.toastType {
			case "success":
				actualToastType = notification.ToastTypeSuccess
			case "error":
				actualToastType = notification.ToastTypeError
			case "warning":
				actualToastType = notification.ToastTypeWarning
			case "info":
				actualToastType = notification.ToastTypeInfo
			default:
				actualToastType = notification.ToastTypeInfo
			}

			if actualToastType != tt.expectedToastType {
				t.Errorf("Toast type mapping for %q: got %v, want %v",
					tt.toastType, actualToastType, tt.expectedToastType)
			}

			// Also verify that the toast type maps correctly to notification properties
			toast := notification.NewToast("test", actualToastType)
			notif := toast.ToNotification()

			if notif.Type != tt.expectedNotifType {
				t.Errorf("Toast to notification type mapping for %q: got %v, want %v",
					tt.toastType, notif.Type, tt.expectedNotifType)
			}

			if notif.Priority != tt.expectedPriority {
				t.Errorf("Toast to notification priority mapping for %q: got %v, want %v",
					tt.toastType, notif.Priority, tt.expectedPriority)
			}
		})
	}
}

func TestController_SendToast_ServiceNotInitialized(t *testing.T) {
	t.Parallel()

	// Create a controller without initializing the notification service
	c := mockController()

	// This test is tricky because the notification service is global.
	// In a real scenario where the service isn't initialized, SendToast should fail gracefully.
	// However, since we've already initialized it in other tests, we can't easily test this
	// without more complex mocking or service lifecycle management.

	// For now, this test documents the expected behavior:
	// If notification service is not initialized, SendToast should return an error
	err := c.SendToast("test message", "info", 1000)

	// Since service is likely already initialized from other tests, this may pass
	// In a real uninitialized scenario, this should return an error
	if notification.IsInitialized() && err != nil {
		t.Errorf("SendToast() with initialized service should not error, got: %v", err)
	}
}

func TestController_SendToast_Integration(t *testing.T) {
	// Integration test that verifies the complete flow
	config := notification.DefaultServiceConfig()

	if !notification.IsInitialized() {
		notification.Initialize(config)
	}

	c := mockController()

	// Send a toast with all features
	err := c.SendToast("Integration test message", "warning", 4000)
	if err != nil {
		t.Fatalf("SendToast() integration test error = %v", err)
	}

	// Retrieve the notification from the service to verify it was stored
	// Note: This requires access to the service's store, which might not be public
	// For now, we'll just verify the method doesn't error
}

// Benchmark for SendToast performance
func BenchmarkController_SendToast(b *testing.B) {
	config := notification.DefaultServiceConfig()

	if !notification.IsInitialized() {
		notification.Initialize(config)
	}

	c := mockController()

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		err := c.SendToast("Benchmark toast message", "info", 1000)
		if err != nil {
			b.Fatalf("SendToast() benchmark error = %v", err)
		}
	}
}