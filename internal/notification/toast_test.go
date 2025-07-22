package notification

import (
	"testing"
	"time"

	"github.com/google/uuid"
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

			// Verify basic fields
			if toast.Message != tt.message {
				t.Errorf("NewToast() message = %v, want %v", toast.Message, tt.message)
			}

			if toast.Type != tt.wantType {
				t.Errorf("NewToast() type = %v, want %v", toast.Type, tt.wantType)
			}

			// Verify ID is generated
			if toast.ID == "" {
				t.Error("NewToast() should generate a non-empty ID")
			}

			// Verify ID is valid UUID
			if _, err := uuid.Parse(toast.ID); err != nil {
				t.Errorf("NewToast() generated invalid UUID: %v", err)
			}

			// Verify timestamp is recent
			if time.Since(toast.Timestamp) > time.Second {
				t.Error("NewToast() timestamp should be recent")
			}
		})
	}
}

func TestToast_WithDuration(t *testing.T) {
	t.Parallel()

	toast := NewToast("test message", ToastTypeInfo)
	duration := 5000

	result := toast.WithDuration(duration)

	// Should return the same toast (method chaining)
	if result != toast {
		t.Error("WithDuration() should return the same toast instance for chaining")
	}

	if toast.Duration != duration {
		t.Errorf("WithDuration() duration = %v, want %v", toast.Duration, duration)
	}
}

func TestToast_WithComponent(t *testing.T) {
	t.Parallel()

	toast := NewToast("test message", ToastTypeInfo)
	component := "settings"

	result := toast.WithComponent(component)

	// Should return the same toast (method chaining)
	if result != toast {
		t.Error("WithComponent() should return the same toast instance for chaining")
	}

	if toast.Component != component {
		t.Errorf("WithComponent() component = %v, want %v", toast.Component, component)
	}
}

func TestToast_WithAction(t *testing.T) {
	t.Parallel()

	toast := NewToast("test message", ToastTypeInfo)
	label := "View Details"
	url := "/details"
	handler := "handleView"

	result := toast.WithAction(label, url, handler)

	// Should return the same toast (method chaining)
	if result != toast {
		t.Error("WithAction() should return the same toast instance for chaining")
	}

	if toast.Action == nil {
		t.Fatal("WithAction() should create action")
	}

	if toast.Action.Label != label {
		t.Errorf("WithAction() action label = %v, want %v", toast.Action.Label, label)
	}

	if toast.Action.URL != url {
		t.Errorf("WithAction() action URL = %v, want %v", toast.Action.URL, url)
	}

	if toast.Action.Handler != handler {
		t.Errorf("WithAction() action handler = %v, want %v", toast.Action.Handler, handler)
	}
}

func TestToast_ToNotification(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		toastType        ToastType
		wantNotifType    Type
		wantNotifPriority Priority
	}{
		{
			name:             "error toast to high priority error notification",
			toastType:        ToastTypeError,
			wantNotifType:    TypeError,
			wantNotifPriority: PriorityHigh,
		},
		{
			name:             "warning toast to medium priority warning notification",
			toastType:        ToastTypeWarning,
			wantNotifType:    TypeWarning,
			wantNotifPriority: PriorityMedium,
		},
		{
			name:             "success toast to low priority info notification",
			toastType:        ToastTypeSuccess,
			wantNotifType:    TypeInfo,
			wantNotifPriority: PriorityLow,
		},
		{
			name:             "info toast to low priority info notification",
			toastType:        ToastTypeInfo,
			wantNotifType:    TypeInfo,
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

			// Verify notification type and priority mapping
			if notif.Type != tt.wantNotifType {
				t.Errorf("ToNotification() type = %v, want %v", notif.Type, tt.wantNotifType)
			}

			if notif.Priority != tt.wantNotifPriority {
				t.Errorf("ToNotification() priority = %v, want %v", notif.Priority, tt.wantNotifPriority)
			}

			// Verify basic fields
			if notif.Title != "Toast Message" {
				t.Errorf("ToNotification() title = %v, want %v", notif.Title, "Toast Message")
			}

			if notif.Message != toast.Message {
				t.Errorf("ToNotification() message = %v, want %v", notif.Message, toast.Message)
			}

			if notif.Component != toast.Component {
				t.Errorf("ToNotification() component = %v, want %v", notif.Component, toast.Component)
			}

			// Verify metadata
			if notif.Metadata == nil {
				t.Fatal("ToNotification() should create metadata")
			}

			if isToast, ok := notif.Metadata["isToast"].(bool); !ok || !isToast {
				t.Error("ToNotification() should set isToast metadata to true")
			}

			if toastType, ok := notif.Metadata["toastType"].(string); !ok || toastType != string(tt.toastType) {
				t.Errorf("ToNotification() should preserve toast type in metadata: got %v, want %v", toastType, string(tt.toastType))
			}

			if toastID, ok := notif.Metadata["toastId"].(string); !ok || toastID != toast.ID {
				t.Errorf("ToNotification() should preserve toast ID in metadata: got %v, want %v", toastID, toast.ID)
			}

			if duration, ok := notif.Metadata["duration"].(int); !ok || duration != toast.Duration {
				t.Errorf("ToNotification() should preserve duration in metadata: got %v, want %v", duration, toast.Duration)
			}

			if action, ok := notif.Metadata["action"].(*ToastAction); !ok || action != toast.Action {
				t.Error("ToNotification() should preserve action in metadata")
			}

			// Verify expiry is set
			if notif.ExpiresAt == nil {
				t.Error("ToNotification() should set expiry for toasts")
			}

			// Verify expiry is reasonable (around 5 minutes)
			expectedExpiry := time.Now().Add(5 * time.Minute)
			if notif.ExpiresAt.Before(expectedExpiry.Add(-10*time.Second)) || notif.ExpiresAt.After(expectedExpiry.Add(10*time.Second)) {
				t.Errorf("ToNotification() expiry seems incorrect: got %v, expected around %v", notif.ExpiresAt, expectedExpiry)
			}
		})
	}
}

func TestToast_MethodChaining(t *testing.T) {
	t.Parallel()

	// Test that all methods can be chained together
	toast := NewToast("chained message", ToastTypeSuccess).
		WithDuration(2000).
		WithComponent("chain-test").
		WithAction("Chain Action", "/chain", "chainHandler")

	if toast.Message != "chained message" {
		t.Errorf("Method chaining failed: message = %v, want %v", toast.Message, "chained message")
	}

	if toast.Type != ToastTypeSuccess {
		t.Errorf("Method chaining failed: type = %v, want %v", toast.Type, ToastTypeSuccess)
	}

	if toast.Duration != 2000 {
		t.Errorf("Method chaining failed: duration = %v, want %v", toast.Duration, 2000)
	}

	if toast.Component != "chain-test" {
		t.Errorf("Method chaining failed: component = %v, want %v", toast.Component, "chain-test")
	}

	if toast.Action == nil || toast.Action.Label != "Chain Action" {
		t.Error("Method chaining failed: action not properly set")
	}
}

func TestToast_ToNotification_WithoutOptionalFields(t *testing.T) {
	t.Parallel()

	// Test toast with minimal fields
	toast := NewToast("minimal toast", ToastTypeInfo)
	notif := toast.ToNotification()

	// Should still work without optional fields
	if notif.Component != "" {
		t.Errorf("ToNotification() component should be empty when not set: got %v", notif.Component)
	}

	// Duration should not be in metadata if not set
	if _, exists := notif.Metadata["duration"]; exists && toast.Duration == 0 {
		t.Error("ToNotification() should not include zero duration in metadata")
	}

	// Action should not be in metadata if not set
	if _, exists := notif.Metadata["action"]; exists && toast.Action == nil {
		t.Error("ToNotification() should not include nil action in metadata")
	}
}