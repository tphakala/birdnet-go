package notification

import (
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

const (
	burstThreshold = 3
	burstWindow    = 5 * time.Minute
)

// globalBurstTracker is the singleton burst tracker for the error hook.
var globalBurstTracker = NewErrorBurstTracker(burstThreshold, burstWindow)

// errorNotificationHook is called when errors are reported
func errorNotificationHook(ee any) {
	enhancedErr, ok := ee.(*errors.EnhancedError)
	if !ok {
		return
	}

	if !IsInitialized() {
		return
	}

	service := GetService()
	if service == nil {
		return
	}

	category := enhancedErr.GetCategory()
	priority := getNotificationPriority(category, enhancedErr.GetPriority())

	if priority == PriorityLow {
		return
	}

	component := enhancedErr.GetComponent()
	errMsg := enhancedErr.Error()

	// Check burst tracker before creating notification.
	action := globalBurstTracker.Record(component, category, errMsg)
	switch action {
	case BurstActionAllow:
		_, _ = service.CreateErrorNotification(enhancedErr)
	case BurstActionSummary:
		summary := globalBurstTracker.GetSummary(component, category)
		if summary != nil {
			createBurstSummaryNotification(service, summary, priority)
		}
	case BurstActionSuppress:
		// Don't create notification — summary already sent.
	}
}

// createBurstSummaryNotification creates a single summary notification for a burst of errors.
func createBurstSummaryNotification(service *Service, summary *BurstSummary, priority Priority) {
	title := fmt.Sprintf("Multiple %s errors", summary.Component)
	message := fmt.Sprintf("%d errors in the last %d minutes: %s",
		summary.Count, summary.WindowMin, summary.SampleError)

	notif := NewNotification(TypeError, priority, title, message).
		WithComponent(summary.Component).
		WithTitleKey(MsgErrorBurstTitle, map[string]any{
			"component": summary.Component,
		}).
		WithMessageKey(MsgErrorBurstMessage, map[string]any{
			"component":      summary.Component,
			"category":       summary.Category,
			"count":          summary.Count,
			"window_minutes": summary.WindowMin,
			"sample_error":   summary.SampleError,
		})

	_ = service.CreateWithMetadata(notif)
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
		return PriorityCritical
	case string(errors.CategoryDatabase):
		return PriorityCritical
	case string(errors.CategoryAudioAnalysis), string(errors.CategoryWorker):
		return PriorityCritical
	case string(errors.CategorySystem):
		return PriorityHigh
	case string(errors.CategoryConfiguration):
		return PriorityHigh
	case string(errors.CategoryImageProvider), string(errors.CategoryImageFetch):
		return PriorityLow
	case string(errors.CategoryMQTTConnection), string(errors.CategoryMQTTAuth):
		return PriorityHigh
	case string(errors.CategoryJobQueue), string(errors.CategoryBuffer):
		return PriorityHigh
	case string(errors.CategoryCommandExecution):
		return PriorityHigh
	case string(errors.CategoryNetwork), string(errors.CategoryRTSP):
		return PriorityMedium
	case string(errors.CategoryFileIO), string(errors.CategoryAudio), string(errors.CategoryAudioSource):
		return PriorityMedium
	case string(errors.CategoryThreshold), string(errors.CategorySpeciesTracking):
		return PriorityMedium
	case string(errors.CategoryTimeout), string(errors.CategoryRetry):
		return PriorityLow
	case string(errors.CategoryCancellation), string(errors.CategoryBroadcast), string(errors.CategoryIntegration):
		return PriorityMedium
	case string(errors.CategoryValidation), string(errors.CategoryNotFound):
		return PriorityLow
	case string(errors.CategorySoundLevel), string(errors.CategoryEventTracking):
		return PriorityLow
	default:
		return PriorityMedium
	}
}

// SetupErrorIntegration sets up the integration with the error package
func SetupErrorIntegration() {
	errors.AddErrorHook(func(ee *errors.EnhancedError) {
		errorNotificationHook(ee)
	})
}
