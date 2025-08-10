package notification

import (
	"testing"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestGetNotificationPriority tests the priority mappings for new error categories
func TestGetNotificationPriority(t *testing.T) {
	tests := []struct {
		name     string
		category string
		expected Priority
	}{
		// Critical priorities
		{"AudioAnalysis", string(errors.CategoryAudioAnalysis), PriorityCritical},
		{"Worker", string(errors.CategoryWorker), PriorityCritical},
		{"Database", string(errors.CategoryDatabase), PriorityCritical},
		
		// High priorities
		{"JobQueue", string(errors.CategoryJobQueue), PriorityHigh},
		{"Buffer", string(errors.CategoryBuffer), PriorityHigh},
		{"CommandExecution", string(errors.CategoryCommandExecution), PriorityHigh},
		{"System", string(errors.CategorySystem), PriorityHigh},
		{"Configuration", string(errors.CategoryConfiguration), PriorityHigh},
		
		// Medium priorities
		{"Threshold", string(errors.CategoryThreshold), PriorityMedium},
		{"SpeciesTracking", string(errors.CategorySpeciesTracking), PriorityMedium},
		{"Timeout", string(errors.CategoryTimeout), PriorityMedium},
		{"Retry", string(errors.CategoryRetry), PriorityMedium},
		{"Cancellation", string(errors.CategoryCancellation), PriorityMedium},
		{"Broadcast", string(errors.CategoryBroadcast), PriorityMedium},
		{"Integration", string(errors.CategoryIntegration), PriorityMedium},
		{"Network", string(errors.CategoryNetwork), PriorityMedium},
		
		// Low priorities
		{"SoundLevel", string(errors.CategorySoundLevel), PriorityLow},
		{"EventTracking", string(errors.CategoryEventTracking), PriorityLow},
		{"Validation", string(errors.CategoryValidation), PriorityLow},
		
		// Default (unknown category)
		{"Unknown", "unknown-category", PriorityMedium},
	}
	
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			priority := getNotificationPriority(tt.category, "")
			if priority != tt.expected {
				t.Errorf("getNotificationPriority(%s) = %v, want %v", 
					tt.category, priority, tt.expected)
			}
		})
	}
}

// TestExplicitPriorityOverride tests that explicit priority overrides category-based priority
func TestExplicitPriorityOverride(t *testing.T) {
	// Low category but explicit critical priority
	priority := getNotificationPriority(string(errors.CategorySoundLevel), "critical")
	if priority != PriorityCritical {
		t.Errorf("Explicit critical priority should override category priority, got %v", priority)
	}
	
	// Critical category but explicit low priority
	priority = getNotificationPriority(string(errors.CategoryAudioAnalysis), "low")
	if priority != PriorityLow {
		t.Errorf("Explicit low priority should override category priority, got %v", priority)
	}
}

// TestAllCategoriesHavePriority tests that all defined categories have a priority mapping
func TestAllCategoriesHavePriority(t *testing.T) {
	// List of all analysis-related categories
	categories := []errors.ErrorCategory{
		errors.CategoryAudioAnalysis,
		errors.CategoryBuffer,
		errors.CategoryWorker,
		errors.CategoryJobQueue,
		errors.CategoryThreshold,
		errors.CategoryEventTracking,
		errors.CategorySpeciesTracking,
		errors.CategorySoundLevel,
		errors.CategoryCommandExecution,
		errors.CategoryTimeout,
		errors.CategoryCancellation,
		errors.CategoryRetry,
		errors.CategoryBroadcast,
		errors.CategoryIntegration,
	}
	
	for _, category := range categories {
		priority := getNotificationPriority(string(category), "")
		// Should not be empty/zero value
		if priority == "" {
			t.Errorf("Category %s has no priority mapping", category)
		}
		// Should be one of the valid priorities
		validPriorities := []Priority{
			PriorityCritical,
			PriorityHigh,
			PriorityMedium,
			PriorityLow,
		}
		found := false
		for _, valid := range validPriorities {
			if priority == valid {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("Category %s has invalid priority: %v", category, priority)
		}
	}
}