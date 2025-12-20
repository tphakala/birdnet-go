package api

import (
	"github.com/tphakala/birdnet-go/internal/notification"
)

// SendToast sends a toast notification through the notification system
func (c *Controller) SendToast(message, toastType string, duration int) error {
	// Map string toast type to notification.ToastType
	var notifToastType notification.ToastType
	switch toastType {
	case ToastTypeSuccess:
		notifToastType = notification.ToastTypeSuccess
	case LogLevelError:
		notifToastType = notification.ToastTypeError
	case LogLevelWarning:
		notifToastType = notification.ToastTypeWarning
	case LogLevelInfo:
		notifToastType = notification.ToastTypeInfo
	default:
		notifToastType = notification.ToastTypeInfo
	}

	// Use the notification service to send the toast
	return notification.SendToastWithDuration(message, notifToastType, "api", duration)
}
