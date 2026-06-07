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

// SendToast sends a toast notification through the notification system. When a
// service is injected (tests), it is used directly; otherwise the call falls
// through to the package-level helper backed by the process-global singleton,
// preserving production behavior exactly (including its not-initialized guard).
func (c *Controller) SendToast(message, toastType string, duration int) error {
	if svc := c.notificationService; svc != nil {
		return svc.SendToastWithDuration(message, mapToastType(toastType), "api", duration)
	}
	return notification.SendToastWithDuration(message, mapToastType(toastType), "api", duration)
}

// SendToastWithKey sends a toast notification with an i18n translation key. It
// uses the injected service when present and otherwise falls through to the
// package-level helper backed by the process-global singleton.
func (c *Controller) SendToastWithKey(message, toastType string, duration int, messageKey string, messageParams map[string]any) error {
	if svc := c.notificationService; svc != nil {
		return svc.SendToastWithDurationAndKey(message, mapToastType(toastType), "api", duration, messageKey, messageParams)
	}
	return notification.SendToastWithDurationAndKey(message, mapToastType(toastType), "api", duration, messageKey, messageParams)
}
