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
	priority := getNotificationPriority(category, enhancedErr.GetPriority())

	// Filter out low priority notifications
	if priority == PriorityLow {
		return
	}

	// Create error notification - EnhancedError implements error interface
	_, _ = service.CreateErrorNotification(enhancedErr)
}

// getNotificationPriority determines the notification priority based on error category and explicit priority
func getNotificationPriority(category, explicitPriority string) Priority {
	// If explicit priority is set, use it first
	if explicitPriority != "" {
		switch explicitPriority {
		case string(PriorityCritical):
			return PriorityCritical
		case string(PriorityHigh):
			return PriorityHigh
		case string(PriorityMedium):
			return PriorityMedium
		case string(PriorityLow):
			return PriorityLow
		}
	}

	// Fall back to category-based priority
	switch category {
	case string(errors.CategoryModelInit), string(errors.CategoryModelLoad):
		return PriorityCritical // App cannot function without models
	case string(errors.CategoryDatabase):
		return PriorityCritical // Data integrity at risk
	case string(errors.CategoryAudioAnalysis), string(errors.CategoryWorker):
		return PriorityCritical // Core functionality failures
	case string(errors.CategorySystem):
		return PriorityHigh // System resources issues
	case string(errors.CategoryConfiguration):
		return PriorityHigh // May prevent proper operation
	case string(errors.CategoryImageProvider):
		return PriorityHigh // Integration failures need user attention
	case string(errors.CategoryMQTTConnection), string(errors.CategoryMQTTAuth):
		return PriorityHigh // Connection/auth failures need immediate attention
	case string(errors.CategoryJobQueue), string(errors.CategoryBuffer):
		return PriorityHigh // May impact processing pipeline
	case string(errors.CategoryCommandExecution):
		return PriorityHigh // User-configured actions failing
	case string(errors.CategoryNetwork), string(errors.CategoryRTSP):
		return PriorityMedium // Often transient
	case string(errors.CategoryFileIO), string(errors.CategoryAudio), string(errors.CategoryAudioSource):
		return PriorityMedium // Usually recoverable
	case string(errors.CategoryThreshold), string(errors.CategorySpeciesTracking):
		return PriorityMedium // Important but not critical
	case string(errors.CategoryTimeout), string(errors.CategoryRetry):
		return PriorityLow // Transient issues - don't bother users with these
	case string(errors.CategoryCancellation), string(errors.CategoryBroadcast), string(errors.CategoryIntegration):
		return PriorityMedium // General operational issues
	case string(errors.CategoryValidation):
		return PriorityLow // User input issues
	case string(errors.CategorySoundLevel), string(errors.CategoryEventTracking):
		return PriorityLow // Monitoring/tracking issues
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
