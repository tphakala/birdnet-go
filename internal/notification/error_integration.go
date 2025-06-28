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
func getNotificationPriority(category errors.ErrorCategory) Priority {
	switch category {
	case errors.CategoryModelInit, errors.CategoryModelLoad:
		return PriorityCritical // App cannot function without models
	case errors.CategoryDatabase:
		return PriorityCritical // Data integrity at risk
	case errors.CategorySystem:
		return PriorityHigh // System resources issues
	case errors.CategoryConfiguration:
		return PriorityHigh // May prevent proper operation
	case errors.CategoryNetwork, errors.CategoryRTSP:
		return PriorityMedium // Often transient
	case errors.CategoryFileIO, errors.CategoryAudio:
		return PriorityMedium // Usually recoverable
	case errors.CategoryValidation:
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
