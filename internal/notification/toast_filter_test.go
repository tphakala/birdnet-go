package notification

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/goleak"
)

// TestToastNotificationsExcludedFromList verifies that toast notifications
// are never returned in notification lists, even when they exist in the store
func TestToastNotificationsExcludedFromList(t *testing.T) {
	// Create service with test config
	config := &ServiceConfig{
		Debug:              false,
		MaxNotifications:   100,
		CleanupInterval:    time.Hour,
		RateLimitWindow:    time.Minute,
		RateLimitMaxEvents: 60,
	}
	service := NewService(config)

	// Stop service before goleak check (defer runs in LIFO order)
	defer goleak.VerifyNone(t,
		goleak.IgnoreCurrent(),
	)
	defer service.Stop()

	// Create a regular notification
	regularNotif, err := service.Create(TypeInfo, PriorityMedium, "Regular Alert", "This is a regular notification")
	require.NoError(t, err)
	require.NotNil(t, regularNotif)

	// Create a toast notification directly with the service (not via global SendToast)
	toast := NewToast("Test toast message", ToastTypeInfo).WithComponent("test")
	toastNotif := toast.ToNotification()
	err = service.CreateWithMetadata(toastNotif)
	require.NoError(t, err)

	// Create another regular notification
	regularNotif2, err := service.Create(TypeWarning, PriorityHigh, "Warning Alert", "This is another regular notification")
	require.NoError(t, err)
	require.NotNil(t, regularNotif2)

	// List all notifications without filter
	notifications, err := service.List(nil)
	require.NoError(t, err)

	// Should only return the 2 regular notifications, not the toast
	assert.Len(t, notifications, 2, "Toast notifications should be excluded from lists")

	// Verify none of the returned notifications are toasts
	for _, n := range notifications {
		isToast, exists := n.Metadata[MetadataKeyIsToast]
		assert.False(t, exists && isToast == true, "No toast notifications should be returned")
		assert.NotEqual(t, ToastNotificationTitle, n.Title, "Toast notifications should not appear in list")
	}

	// List with various filters - toast should never appear
	testCases := []struct {
		name   string
		filter *FilterOptions
	}{
		{
			name:   "Filter by info type",
			filter: &FilterOptions{Types: []Type{TypeInfo}},
		},
		{
			name:   "Filter by low priority",
			filter: &FilterOptions{Priorities: []Priority{PriorityLow}},
		},
		{
			name:   "Filter by component",
			filter: &FilterOptions{Component: "test"},
		},
		{
			name:   "Filter with limit",
			filter: &FilterOptions{Limit: 10},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			notifications, err := service.List(tc.filter)
			require.NoError(t, err)

			// Verify no toast notifications in results
			for _, n := range notifications {
				assert.NotEqual(t, ToastNotificationTitle, n.Title, "Toast notifications should not appear in filtered list")
			}
		})
	}
}

// TestToastNotificationsStillBroadcast verifies that toast notifications
// are still broadcast to subscribers even though they're excluded from lists
func TestToastNotificationsStillBroadcast(t *testing.T) {
	// Create service with test config
	config := &ServiceConfig{
		Debug:              false,
		MaxNotifications:   100,
		CleanupInterval:    time.Hour,
		RateLimitWindow:    time.Minute,
		RateLimitMaxEvents: 60,
	}
	service := NewService(config)

	// Stop service before goleak check (defer runs in LIFO order)
	defer goleak.VerifyNone(t,
		goleak.IgnoreCurrent(),
	)
	defer service.Stop()

	// Subscribe to notifications
	notifCh, _ := service.Subscribe()
	defer service.Unsubscribe(notifCh)

	// Send a toast notification
	toast := NewToast("Test broadcast", ToastTypeSuccess).WithComponent("test")
	notification := toast.ToNotification()
	err := service.CreateWithMetadata(notification)
	require.NoError(t, err)

	// Should receive the toast via broadcast (increased timeout for CI reliability)
	select {
	case received := <-notifCh:
		assert.NotNil(t, received)
		isToast, exists := received.Metadata[MetadataKeyIsToast]
		assert.True(t, exists, "Broadcast notification should have isToast metadata")
		assert.True(t, isToast.(bool), "Broadcast notification should be marked as toast")
	case <-time.After(500 * time.Millisecond):
		require.Fail(t, "Toast notification was not broadcast")
	}

	// But it should not appear in lists
	notifications, err := service.List(nil)
	require.NoError(t, err)
	assert.Empty(t, notifications, "Toast should not appear in notification list")
}
