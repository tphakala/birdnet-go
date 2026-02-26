package api

import (
	"github.com/tphakala/birdnet-go/internal/notification"
)

// mapToastType converts a string toast type to notification.ToastType
func mapToastType(toastType string) notification.ToastType {
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

// SendToast sends a toast notification through the notification system
func (c *Controller) SendToast(message, toastType string, duration int) error {
	return notification.SendToastWithDuration(message, mapToastType(toastType), "api", duration)
}

// SendToastWithKey sends a toast notification with an i18n translation key
func (c *Controller) SendToastWithKey(message, toastType string, duration int, messageKey string, messageParams map[string]any) error {
	return notification.SendToastWithDurationAndKey(message, mapToastType(toastType), "api", duration, messageKey, messageParams)
}
