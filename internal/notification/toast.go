package notification

import (
	"fmt"
	"time"

	"github.com/google/uuid"
)

// ToastType represents the visual style/severity of a toast message
type ToastType string

const (
	// ToastTypeInfo for informational messages
	ToastTypeInfo ToastType = "info"
	// ToastTypeSuccess for success messages
	ToastTypeSuccess ToastType = "success"
	// ToastTypeWarning for warning messages
	ToastTypeWarning ToastType = "warning"
	// ToastTypeError for error messages
	ToastTypeError ToastType = "error"
	
	// ToastNotificationTitle is the standard title for toast notifications
	// This constant is used to identify toast notifications for filtering
	ToastNotificationTitle = "Toast Message"
)

// Toast represents a temporary UI notification message
type Toast struct {
	// ID is the unique identifier for the toast
	ID string `json:"id"`
	// Message is the content to display
	Message string `json:"message"`
	// Type determines the visual style
	Type ToastType `json:"type"`
	// Duration in milliseconds (0 = use default)
	Duration int `json:"duration,omitempty"`
	// Timestamp when the toast was created
	Timestamp time.Time `json:"timestamp"`
	// Component that generated the toast (optional)
	Component string `json:"component,omitempty"`
	// Action provides an optional action button
	Action *ToastAction `json:"action,omitempty"`
}

// ToastAction represents an optional action button on a toast
type ToastAction struct {
	// Label is the button text
	Label string `json:"label"`
	// URL is the link to navigate to (if applicable)
	URL string `json:"url,omitempty"`
	// Handler is a frontend event handler name (if applicable)
	Handler string `json:"handler,omitempty"`
}

// NewToast creates a new toast message
func NewToast(message string, toastType ToastType) *Toast {
	return &Toast{
		ID:        uuid.New().String(),
		Message:   message,
		Type:      toastType,
		Timestamp: time.Now(),
	}
}

// WithDuration sets the display duration and returns the toast for chaining
func (t *Toast) WithDuration(milliseconds int) *Toast {
	t.Duration = milliseconds
	return t
}

// WithComponent sets the component field and returns the toast for chaining
func (t *Toast) WithComponent(component string) *Toast {
	t.Component = component
	return t
}

// WithAction adds an action button and returns the toast for chaining
func (t *Toast) WithAction(label, url, handler string) *Toast {
	t.Action = &ToastAction{
		Label:   label,
		URL:     url,
		Handler: handler,
	}
	return t
}

// ToNotification converts a toast to a notification for event bus compatibility
// This allows toasts to be processed through the same notification pipeline
func (t *Toast) ToNotification() *Notification {
	// Map toast types to notification types and priorities
	var notifType Type
	var priority Priority
	
	switch t.Type {
	case ToastTypeError:
		notifType = TypeError
		priority = PriorityHigh
	case ToastTypeWarning:
		notifType = TypeWarning
		priority = PriorityMedium
	case ToastTypeSuccess, ToastTypeInfo:
		notifType = TypeInfo
		priority = PriorityLow
	default:
		notifType = TypeInfo
		priority = PriorityLow
	}
	
	// Create notification with toast metadata
	notif := NewNotification(notifType, priority, ToastNotificationTitle, t.Message)
	notif.WithComponent(t.Component).
		WithMetadata("isToast", true).
		WithMetadata("toastType", string(t.Type)).
		WithMetadata("toastId", t.ID)
	
	if t.Duration > 0 {
		notif.WithMetadata("duration", t.Duration)
	}
	
	if t.Action != nil {
		notif.WithMetadata("action", t.Action)
	}
	
	// Toasts are ephemeral - set short expiry
	notif.WithExpiry(5 * time.Minute)
	
	return notif
}

// SendToast is a convenience function to send a toast through the notification service
func SendToast(message string, toastType ToastType, component string) error {
	if !IsInitialized() {
		return fmt.Errorf("notification service not initialized")
	}
	
	service := GetService()
	if service == nil {
		return fmt.Errorf("notification service is nil")
	}
	
	toast := NewToast(message, toastType).WithComponent(component)
	notification := toast.ToNotification()
	
	// Use CreateWithMetadata to preserve all toast metadata
	return service.CreateWithMetadata(notification)
}

// SendToastWithDuration sends a toast with a specific display duration
func SendToastWithDuration(message string, toastType ToastType, component string, duration int) error {
	if !IsInitialized() {
		return fmt.Errorf("notification service not initialized")
	}
	
	service := GetService()
	if service == nil {
		return fmt.Errorf("notification service is nil")
	}
	
	toast := NewToast(message, toastType).
		WithComponent(component).
		WithDuration(duration)
	notification := toast.ToNotification()
	
	// Use CreateWithMetadata to preserve all toast metadata
	return service.CreateWithMetadata(notification)
}