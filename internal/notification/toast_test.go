//nolint:gocognit // Table-driven tests have expected complexity
package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewToast(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		message   string
		toastType ToastType
		wantType  ToastType
	}{
		{
			name:      "info toast",
			message:   "Information message",
			toastType: ToastTypeInfo,
			wantType:  ToastTypeInfo,
		},
		{
			name:      "success toast",
			message:   "Success message",
			toastType: ToastTypeSuccess,
			wantType:  ToastTypeSuccess,
		},
		{
			name:      "warning toast",
			message:   "Warning message",
			toastType: ToastTypeWarning,
			wantType:  ToastTypeWarning,
		},
		{
			name:      "error toast",
			message:   "Error message",
			toastType: ToastTypeError,
			wantType:  ToastTypeError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			toast := NewToast(tt.message, tt.toastType)

			assert.Equal(t, tt.message, toast.Message)
			assert.Equal(t, tt.wantType, toast.Type)
			assert.NotEmpty(t, toast.ID, "should generate a non-empty ID")

			// Verify ID is valid UUID
			_, err := uuid.Parse(toast.ID)
			require.NoError(t, err, "should generate valid UUID")

			// Verify timestamp is recent
			assert.WithinDuration(t, time.Now(), toast.Timestamp, time.Second,
				"timestamp should be recent")
		})
	}
}

func TestToast_WithDuration(t *testing.T) {
	t.Parallel()

	toast := NewToast("test message", ToastTypeInfo)
	duration := 5000

	result := toast.WithDuration(duration)

	assert.Same(t, toast, result, "should return the same toast instance for chaining")
	assert.Equal(t, duration, toast.Duration)
}

func TestToast_WithComponent(t *testing.T) {
	t.Parallel()

	toast := NewToast("test message", ToastTypeInfo)
	component := "settings"

	result := toast.WithComponent(component)

	assert.Same(t, toast, result, "should return the same toast instance for chaining")
	assert.Equal(t, component, toast.Component)
}

func TestToast_WithAction(t *testing.T) {
	t.Parallel()

	toast := NewToast("test message", ToastTypeInfo)
	label := "View Details"
	url := "/details"
	handler := "handleView"

	result := toast.WithAction(label, url, handler)

	assert.Same(t, toast, result, "should return the same toast instance for chaining")
	require.NotNil(t, toast.Action, "should create action")
	assert.Equal(t, label, toast.Action.Label)
	assert.Equal(t, url, toast.Action.URL)
	assert.Equal(t, handler, toast.Action.Handler)
}

func TestToast_ToNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		toastType         ToastType
		wantNotifType     Type
		wantNotifPriority Priority
	}{
		{
			name:              "error toast to high priority error notification",
			toastType:         ToastTypeError,
			wantNotifType:     TypeError,
			wantNotifPriority: PriorityHigh,
		},
		{
			name:              "warning toast to medium priority warning notification",
			toastType:         ToastTypeWarning,
			wantNotifType:     TypeWarning,
			wantNotifPriority: PriorityMedium,
		},
		{
			name:              "success toast to low priority info notification",
			toastType:         ToastTypeSuccess,
			wantNotifType:     TypeInfo,
			wantNotifPriority: PriorityLow,
		},
		{
			name:              "info toast to low priority info notification",
			toastType:         ToastTypeInfo,
			wantNotifType:     TypeInfo,
			wantNotifPriority: PriorityLow,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			toast := NewToast("test message", tt.toastType).
				WithComponent("test-component").
				WithDuration(3000).
				WithAction("Action", "/url", "handler")

			notif := toast.ToNotification()

			assert.Equal(t, tt.wantNotifType, notif.Type)
			assert.Equal(t, tt.wantNotifPriority, notif.Priority)
			assert.Equal(t, "Toast Message", notif.Title)
			assert.Equal(t, toast.Message, notif.Message)
			assert.Equal(t, toast.Component, notif.Component)

			require.NotNil(t, notif.Metadata, "should create metadata")

			isToast, ok := notif.Metadata["isToast"].(bool)
			require.True(t, ok && isToast, "should set isToast metadata to true")

			toastType, ok := notif.Metadata["toastType"].(string)
			require.True(t, ok)
			assert.Equal(t, string(tt.toastType), toastType)

			toastID, ok := notif.Metadata["toastId"].(string)
			require.True(t, ok)
			assert.Equal(t, toast.ID, toastID)

			duration, ok := notif.Metadata["duration"].(int)
			require.True(t, ok)
			assert.Equal(t, toast.Duration, duration)

			action, ok := notif.Metadata["action"].(*ToastAction)
			require.True(t, ok)
			assert.Same(t, toast.Action, action)

			require.NotNil(t, notif.ExpiresAt, "should set expiry for toasts")

			expectedExpiry := time.Now().Add(5 * time.Minute)
			assert.WithinDuration(t, expectedExpiry, *notif.ExpiresAt, 10*time.Second)
		})
	}
}

func TestToast_MethodChaining(t *testing.T) {
	t.Parallel()

	toast := NewToast("chained message", ToastTypeSuccess).
		WithDuration(2000).
		WithComponent("chain-test").
		WithAction("Chain Action", "/chain", "chainHandler")

	assert.Equal(t, "chained message", toast.Message)
	assert.Equal(t, ToastTypeSuccess, toast.Type)
	assert.Equal(t, 2000, toast.Duration)
	assert.Equal(t, "chain-test", toast.Component)
	require.NotNil(t, toast.Action)
	assert.Equal(t, "Chain Action", toast.Action.Label)
}

func TestToast_ToNotification_WithoutOptionalFields(t *testing.T) {
	t.Parallel()

	toast := NewToast("minimal toast", ToastTypeInfo)
	notif := toast.ToNotification()

	assert.Empty(t, notif.Component, "component should be empty when not set")

	// Duration should not be in metadata if not set
	_, durationExists := notif.Metadata["duration"]
	assert.False(t, durationExists && toast.Duration == 0, "should not include zero duration in metadata")

	// Action should not be in metadata if not set
	_, actionExists := notif.Metadata["action"]
	assert.False(t, actionExists && toast.Action == nil, "should not include nil action in metadata")
}
