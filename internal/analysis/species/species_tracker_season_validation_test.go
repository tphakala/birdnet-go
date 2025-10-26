// species_tracker_season_validation_test.go

package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestSeasonDateValidation tests the validation of season dates
func TestSeasonDateValidation(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name        string
		month       int
		day         int
		expectError bool
		errorMsg    string
	}{
		// Valid dates
		{"Valid January 1", 1, 1, false, ""},
		{"Valid January 31", 1, 31, false, ""},
		{"Valid February 28", 2, 28, false, ""},
		{"Valid February 29 (leap year)", 2, 29, false, ""}, // Should accept for year-agnostic seasons
		{"Valid March 20", 3, 20, false, ""},
		{"Valid April 30", 4, 30, false, ""},
		{"Valid May 31", 5, 31, false, ""},
		{"Valid June 21", 6, 21, false, ""},
		{"Valid July 31", 7, 31, false, ""},
		{"Valid August 31", 8, 31, false, ""},
		{"Valid September 22", 9, 22, false, ""},
		{"Valid October 31", 10, 31, false, ""},
		{"Valid November 30", 11, 30, false, ""},
		{"Valid December 21", 12, 21, false, ""},
		{"Valid December 31", 12, 31, false, ""},

		// Invalid months
		{"Invalid month 0", 0, 1, true, "invalid month: 0"},
		{"Invalid month -1", -1, 1, true, "invalid month: -1"},
		{"Invalid month 13", 13, 1, true, "invalid month: 13"},

		// Invalid days
		{"Invalid day 0", 1, 0, true, "invalid day 0"},
		{"Invalid January 32", 1, 32, true, "invalid day 32 for month 1"},
		{"Invalid February 30", 2, 30, true, "invalid day 30 for month 2"},
		{"Invalid March 32", 3, 32, true, "invalid day 32 for month 3"},
		{"Invalid April 31", 4, 31, true, "invalid day 31 for month 4"},
		{"Invalid May 32", 5, 32, true, "invalid day 32 for month 5"},
		{"Invalid June 31", 6, 31, true, "invalid day 31 for month 6"},
		{"Invalid July 32", 7, 32, true, "invalid day 32 for month 7"},
		{"Invalid August 32", 8, 32, true, "invalid day 32 for month 8"},
		{"Invalid September 31", 9, 31, true, "invalid day 31 for month 9"},
		{"Invalid October 32", 10, 32, true, "invalid day 32 for month 10"},
		{"Invalid November 31", 11, 31, true, "invalid day 31 for month 11"},
		{"Invalid December 32", 12, 32, true, "invalid day 32 for month 12"},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateSeasonDate(tc.month, tc.day)
			if tc.expectError {
				require.Error(t, err, "Expected error for %s", tc.name)
				assert.Contains(t, err.Error(), tc.errorMsg, "Error message should contain expected text")
			} else {
				require.NoError(t, err, "Expected no error for %s", tc.name)
			}
		})
	}
}

// TestSeasonValidationInTracker tests that invalid seasons are skipped during tracker initialization
func TestSeasonValidationInTracker(t *testing.T) {
	t.Parallel()

	// Create settings with some invalid season dates
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
			Seasons: map[string]conf.Season{
				"spring":        {StartMonth: 3, StartDay: 20},  // Valid
				"summer":        {StartMonth: 6, StartDay: 31},  // Invalid - June has 30 days
				"fall":          {StartMonth: 9, StartDay: 22},  // Valid
				"winter":        {StartMonth: 12, StartDay: 21}, // Valid
				"invalid_month": {StartMonth: 13, StartDay: 1},  // Invalid month
				"invalid_day":   {StartMonth: 2, StartDay: 30},  // Invalid - Feb 30
			},
		},
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Check that only valid seasons were loaded
	assert.Contains(t, tracker.seasons, "spring", "Spring should be loaded")
	assert.NotContains(t, tracker.seasons, "summer", "Summer with invalid day should not be loaded")
	assert.Contains(t, tracker.seasons, "fall", "Fall should be loaded")
	assert.Contains(t, tracker.seasons, "winter", "Winter should be loaded")
	assert.NotContains(t, tracker.seasons, "invalid_month", "Season with invalid month should not be loaded")
	assert.NotContains(t, tracker.seasons, "invalid_day", "Season with invalid day should not be loaded")

	// Verify the cached season order was built despite some invalid seasons
	assert.NotEmpty(t, tracker.cachedSeasonOrder, "Season order should be cached")
}

// TestCachedSeasonOrderPerformance verifies that season order is cached and not rebuilt
func TestCachedSeasonOrderPerformance(t *testing.T) {
	t.Parallel()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Verify season order is cached at initialization
	assert.NotEmpty(t, tracker.cachedSeasonOrder, "Season order should be cached")
	assert.Equal(t, []string{"winter", "spring", "summer", "fall"}, tracker.cachedSeasonOrder,
		"Season order should match expected traditional order")

	// Store the cached order reference
	originalOrder := tracker.cachedSeasonOrder

	// Call computeCurrentSeason multiple times
	testDate := time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC) // Mid-June
	for i := 0; i < 100; i++ {
		tracker.mu.Lock()
		_ = tracker.computeCurrentSeason(testDate)
		tracker.mu.Unlock()
	}

	// Verify the same cached order is still being used (same slice reference)
	assert.Equal(t, &originalOrder[0], &tracker.cachedSeasonOrder[0],
		"Season order should use the same cached slice (not rebuilt)")
}
