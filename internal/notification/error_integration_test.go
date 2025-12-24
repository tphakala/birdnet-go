package notification

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestGetNotificationPriority tests the priority mappings for new error categories
func TestGetNotificationPriority(t *testing.T) {
	t.Parallel()

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
		{"Timeout", string(errors.CategoryTimeout), PriorityLow},
		{"Retry", string(errors.CategoryRetry), PriorityLow},
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
			t.Parallel()
			priority := getNotificationPriority(tt.category, "")
			assert.Equal(t, tt.expected, priority,
				"getNotificationPriority(%s) mismatch", tt.category)
		})
	}
}

// TestExplicitPriorityOverride tests that explicit priority overrides category-based priority
func TestExplicitPriorityOverride(t *testing.T) {
	t.Parallel()

	// Low category but explicit critical priority
	priority := getNotificationPriority(string(errors.CategorySoundLevel), "critical")
	assert.Equal(t, PriorityCritical, priority,
		"Explicit critical priority should override category priority")

	// Critical category but explicit low priority
	priority = getNotificationPriority(string(errors.CategoryAudioAnalysis), "low")
	assert.Equal(t, PriorityLow, priority,
		"Explicit low priority should override category priority")
}

// TestAllCategoriesHavePriority tests that all defined categories have a priority mapping
func TestAllCategoriesHavePriority(t *testing.T) {
	t.Parallel()

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

	validPriorities := []Priority{
		PriorityCritical,
		PriorityHigh,
		PriorityMedium,
		PriorityLow,
	}

	for _, category := range categories {
		priority := getNotificationPriority(string(category), "")

		assert.NotEmpty(t, priority, "Category %s has no priority mapping", category)
		assert.True(t, slices.Contains(validPriorities, priority),
			"Category %s has invalid priority: %v", category, priority)
	}
}
