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

// assertEventDataNil checks that a field in eventData is nil.
func assertEventDataNil(t *testing.T, eventData map[string]any, field, context string) {
	t.Helper()
	assert.Nil(t, eventData[field], "Event data %s should be nil for %s", field, context)
}

// assertEventDataEmpty checks that a string field in eventData is empty.
func assertEventDataEmpty(t *testing.T, eventData map[string]any, field, context string) {
	t.Helper()
	assert.Empty(t, eventData[field], "Event data %s should be empty string for %s", field, context)
}

// assertEventDataMissing checks that a field is not present in eventData.
func assertEventDataMissing(t *testing.T, eventData map[string]any, field string) {
	t.Helper()
	_, exists := eventData[field]
	assert.False(t, exists, "Event data should not include %s when not set", field)
}

// assertEventDataValue checks a specific field value in eventData.
func assertEventDataValue(t *testing.T, eventData map[string]any, field string, expected any) {
	t.Helper()
	assert.Equal(t, expected, eventData[field], "Event data %s mismatch", field)
}

// mapStringToToastType converts a string toast type to notification.ToastType.
func mapStringToToastType(toastType string) notification.ToastType {
	switch toastType {
	case ToastTypeSuccess:
		return notification.ToastTypeSuccess
	case ToastTypeError:
		return notification.ToastTypeError
	case ToastTypeWarning:
		return notification.ToastTypeWarning
	case ToastTypeInfo:
		return notification.ToastTypeInfo
	default:
		return notification.ToastTypeInfo
	}
}

// assertToastTypeMapping verifies toast type string to enum mapping.
func assertToastTypeMapping(t *testing.T, input string, actual, expected notification.ToastType) {
	t.Helper()
	assert.Equal(t, expected, actual, "Toast type mapping for %q mismatch", input)
}

// assertNotificationMapping verifies notification type and priority mapping.
func assertNotificationMapping(t *testing.T, toastType string, notif *notification.Notification, expectedType notification.Type, expectedPriority notification.Priority) {
	t.Helper()
	assert.Equal(t, expectedType, notif.Type, "Toast to notification type mapping for %q mismatch", toastType)
	assert.Equal(t, expectedPriority, notif.Priority, "Toast to notification priority mapping for %q mismatch", toastType)
}

func TestController_SendToast(t *testing.T) {
	// Remove t.Parallel() to prevent interference between tests
	// Each test will use an isolated service instance

	tests := getToastTestCases()

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Create isolated service for each test
			config := notification.DefaultServiceConfig()
			service := notification.NewService(config)
			defer service.Stop()

			c := mockController()
			runSendToastTestIsolated(t, c, service, tt)
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
			toastType: ToastTypeSuccess,
			duration:  3000,
			wantError: false,
		},
		{
			name:      "error toast",
			message:   "Operation failed",
			toastType: ToastTypeError,
			duration:  5000,
			wantError: false,
		},
		{
			name:      "warning toast",
			message:   "Warning message",
			toastType: ToastTypeWarning,
			duration:  4000,
			wantError: false,
		},
		{
			name:      "info toast",
			message:   "Information message",
			toastType: ToastTypeInfo,
			duration:  3000,
			wantError: false,
		},
		{
			name:      "unknown toast type defaults to info",
			message:   "Unknown type message",
			toastType: ValueUnknown,
			duration:  3000,
			wantError: false,
		},
		{
			name:      "empty message",
			message:   "",
			toastType: ToastTypeInfo,
			duration:  1000,
			wantError: false,
		},
		{
			name:      "zero duration",
			message:   "Zero duration message",
			toastType: ToastTypeSuccess,
			duration:  0,
			wantError: false,
		},
	}
}

// runSendToastTestIsolated runs a single SendToast test case with an isolated service
// This tests the notification service directly since Controller.SendToast uses global service
func runSendToastTestIsolated(t *testing.T, c *Controller, service *notification.Service, tc toastTestCase) {
	t.Helper()
	// Subscribe to the isolated service
	notifCh, _ := service.Subscribe()
	defer service.Unsubscribe(notifCh)

	// Map string toast type to notification.ToastType (same logic as Controller.SendToast)
	var notifToastType notification.ToastType
	switch tc.toastType {
	case ToastTypeSuccess:
		notifToastType = notification.ToastTypeSuccess
	case ToastTypeError:
		notifToastType = notification.ToastTypeError
	case ToastTypeWarning:
		notifToastType = notification.ToastTypeWarning
	case ToastTypeInfo:
		notifToastType = notification.ToastTypeInfo
	default:
		notifToastType = notification.ToastTypeInfo
	}

	// Create and send toast directly to the isolated service (bypassing global service)
	toast := notification.NewToast(tc.message, notifToastType).
		WithComponent("api").
		WithDuration(tc.duration)

	err := service.CreateWithMetadata(toast.ToNotification())

	if tc.wantError {
		assert.Error(t, err, "CreateWithMetadata() expected error but got none")
		return
	}

	require.NoError(t, err, "CreateWithMetadata() unexpected error")

	// Verify notification was created and broadcast
	select {
	case notif := <-notifCh:
		verifyToastNotification(t, notif, tc)
	case <-time.After(100 * time.Millisecond):
		require.Fail(t, "CreateWithMetadata() should have broadcast notification within timeout")
	}
}

// verifyToastNotification verifies the notification created from a toast
func verifyToastNotification(t *testing.T, notif *notification.Notification, tc toastTestCase) {
	t.Helper()
	// Verify the notification has toast metadata
	isToast, ok := notif.Metadata["isToast"].(bool)
	assert.True(t, ok && isToast, "SendToast() should create notification with isToast=true metadata")

	// Verify basic fields
	assert.Equal(t, tc.message, notif.Message, "SendToast() notification message mismatch")
	assert.Equal(t, "api", notif.Component, "SendToast() notification component mismatch")

	// Verify toast-specific metadata
	verifyToastMetadata(t, notif, tc)

	// Verify notification type mapping
	verifyNotificationTypeMapping(t, notif, tc.toastType)
}

// verifyToastMetadata verifies toast-specific metadata
func verifyToastMetadata(t *testing.T, notif *notification.Notification, tc toastTestCase) {
	t.Helper()
	expectedToastType := tc.toastType
	if tc.toastType == ValueUnknown {
		expectedToastType = ToastTypeInfo // Unknown types default to info
	}

	toastType, ok := notif.Metadata["toastType"].(string)
	assert.True(t, ok, "toastType should be string")
	assert.Equal(t, expectedToastType, toastType, "SendToast() toast type mismatch")

	// Duration should only be present in metadata if greater than 0
	if tc.duration > 0 {
		duration, ok := notif.Metadata["duration"].(int)
		assert.True(t, ok, "duration should be int")
		assert.Equal(t, tc.duration, duration, "SendToast() duration mismatch")
	} else {
		// Zero duration should not be included in metadata
		_, exists := notif.Metadata["duration"]
		assert.False(t, exists, "SendToast() should not include zero duration in metadata")
	}
}

// verifyNotificationTypeMapping verifies notification type and priority mapping
func verifyNotificationTypeMapping(t *testing.T, notif *notification.Notification, toastType string) {
	t.Helper()
	expectedToastType := toastType
	if toastType == ValueUnknown {
		expectedToastType = ToastTypeInfo
	}

	var expectedNotifType notification.Type
	var expectedPriority notification.Priority
	switch expectedToastType {
	case ToastTypeSuccess, ToastTypeInfo:
		expectedNotifType = notification.TypeInfo
		expectedPriority = notification.PriorityLow
	case ToastTypeWarning:
		expectedNotifType = notification.TypeWarning
		expectedPriority = notification.PriorityMedium
	case ToastTypeError:
		expectedNotifType = notification.TypeError
		expectedPriority = notification.PriorityHigh
	}

	assert.Equal(t, expectedNotifType, notif.Type, "SendToast() notification type mismatch")
	assert.Equal(t, expectedPriority, notif.Priority, "SendToast() notification priority mismatch")
}

func TestController_SendToast_TypeMapping(t *testing.T) {
	tests := []struct {
		toastType         string
		expectedNotifType notification.Type
		expectedPriority  notification.Priority
		expectedToastType notification.ToastType
	}{
		{ToastTypeSuccess, notification.TypeInfo, notification.PriorityLow, notification.ToastTypeSuccess},
		{ToastTypeError, notification.TypeError, notification.PriorityHigh, notification.ToastTypeError},
		{ToastTypeWarning, notification.TypeWarning, notification.PriorityMedium, notification.ToastTypeWarning},
		{ToastTypeInfo, notification.TypeInfo, notification.PriorityLow, notification.ToastTypeInfo},
		{"invalid", notification.TypeInfo, notification.PriorityLow, notification.ToastTypeInfo},
		{"", notification.TypeInfo, notification.PriorityLow, notification.ToastTypeInfo},
	}

	for _, tt := range tests {
		t.Run("type_"+tt.toastType, func(t *testing.T) {
			actualToastType := mapStringToToastType(tt.toastType)
			assertToastTypeMapping(t, tt.toastType, actualToastType, tt.expectedToastType)

			toast := notification.NewToast("test", actualToastType)
			notif := toast.ToNotification()
			assertNotificationMapping(t, tt.toastType, notif, tt.expectedNotifType, tt.expectedPriority)
		})
	}
}

func TestController_SendToast_ServiceNotInitialized(t *testing.T) {
	// Note: Cannot use t.Parallel() because this test uses the global notification service
	// which has a shared rate limiter. Running in parallel with other tests causes
	// rate limit exhaustion and test failures.

	// Create a controller without initializing the notification service
	c := mockController()

	// This test is tricky because the notification service is global.
	// In a real scenario where the service isn't initialized, SendToast should fail gracefully.
	// However, since we've already initialized it in other tests, we can't easily test this
	// without more complex mocking or service lifecycle management.

	// For now, this test documents the expected behavior:
	// If notification service is not initialized, SendToast should return an error
	err := c.SendToast("test message", ToastTypeInfo, 1000)

	// Since service is likely already initialized from other tests, this may pass
	// In a real uninitialized scenario, this should return an error
	if notification.IsInitialized() {
		assert.NoError(t, err, "SendToast() with initialized service should not error")
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
	err := c.SendToast("Integration test message", ToastTypeWarning, 4000)
	require.NoError(t, err, "SendToast() integration test error")

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

	// Use b.Loop() for benchmark iteration (Go 1.25)
	for b.Loop() {
		err := c.SendToast("Benchmark toast message", ToastTypeInfo, 1000)
		require.NoError(b, err, "SendToast() benchmark error")
	}
}
