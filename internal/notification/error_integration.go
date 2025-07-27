package notification

import (
	"github.com/tphakala/birdnet-go/internal/errors"
)

// errorNotificationHook is called when errors are reported
func errorNotificationHook(ee any) {
	// Type assert to get the enhanced error
	enhancedErr, ok := ee.(*errors.EnhancedError)
	if !ok {
		return
	}

	// Check if notification service is initialized
	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	// Determine if this error should create a notification
	category := enhancedErr.GetCategory()
	priority := getNotificationPriority(category)

	// Only create notifications for high and critical priority errors
	if priority < PriorityHigh {
		return
	}

	// Create error notification - EnhancedError implements error interface
	_, _ = service.CreateErrorNotification(enhancedErr)
}

// getNotificationPriority determines the notification priority based on error category
func getNotificationPriority(category string) Priority {
	switch category {
	case string(errors.CategoryModelInit), string(errors.CategoryModelLoad):
		return PriorityCritical // App cannot function without models
	case string(errors.CategoryDatabase):
		return PriorityCritical // Data integrity at risk
	case string(errors.CategorySystem):
		return PriorityHigh // System resources issues
	case string(errors.CategoryConfiguration):
		return PriorityHigh // May prevent proper operation
	case string(errors.CategoryImageProvider):
		return PriorityHigh // Integration failures need user attention
	case string(errors.CategoryMQTTConnection), string(errors.CategoryMQTTAuth):
		return PriorityHigh // Connection/auth failures need immediate attention
	case string(errors.CategoryNetwork), string(errors.CategoryRTSP):
		return PriorityMedium // Often transient
	case string(errors.CategoryFileIO), string(errors.CategoryAudio):
		return PriorityMedium // Usually recoverable
	case string(errors.CategoryValidation):
		return PriorityLow // User input issues
	default:
		return PriorityMedium
	}
}

// SetupErrorIntegration sets up the integration with the error package
func SetupErrorIntegration() {
	// Add our hook to the error package
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		errorNotificationHook(ee)
	})
}
