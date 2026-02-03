// new_species_tracker_daterange_test.go
// Critical tests for date range calculations and period logic
package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestGetYearDateRange_CriticalReliability tests yearly date range calculations
// CRITICAL: Wrong date ranges cause incorrect yearly tracking and data loss
func TestGetYearDateRange_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name              string
		currentTime       time.Time
		resetMonth        int
		resetDay          int
		overrideYear      int // For testing with SetCurrentYearForTesting
		expectedStartDate string
		expectedEndDate   string
		description       string
	}{
		// Standard January 1 reset tests
		{
			"standard_mid_year",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			1, 1, 0,
			"2024-01-01",
			"2024-12-31",
			"Mid-year should return current calendar year",
		},
		{
			"standard_year_start",
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			1, 1, 0,
			"2024-01-01",
			"2024-12-31",
			"Year start should return current calendar year",
		},
		{
			"standard_year_end",
			time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			1, 1, 0,
			"2024-01-01",
			"2024-12-31",
			"Year end should return current calendar year",
		},
		// Custom reset date tests (e.g., July 1 tracking year)
		{
			"tracking_year_before_reset",
			time.Date(2024, 6, 30, 23, 59, 59, 0, time.UTC),
			7, 1, 0, // July 1 reset
			"2023-07-01",
			"2024-06-30",
			"Before July 1 reset should be in previous tracking year",
		},
		{
			"tracking_year_on_reset",
			time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
			7, 1, 0,
			"2024-07-01",
			"2025-06-30",
			"On July 1 reset should start new tracking year",
		},
		{
			"tracking_year_after_reset",
			time.Date(2024, 8, 15, 12, 0, 0, 0, time.UTC),
			7, 1, 0,
			"2024-07-01",
			"2025-06-30",
			"After July 1 reset should be in current tracking year",
		},
		// Academic year tests (September 1)
		{
			"academic_year_august",
			time.Date(2024, 8, 31, 23, 59, 59, 0, time.UTC),
			9, 1, 0,
			"2023-09-01",
			"2024-08-31",
			"August should be end of previous academic year",
		},
		{
			"academic_year_september",
			time.Date(2024, 9, 1, 0, 0, 0, 0, time.UTC),
			9, 1, 0,
			"2024-09-01",
			"2025-08-31",
			"September 1 should start new academic year",
		},
		// Leap year tests
		{
			"leap_year_february",
			time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			1, 1, 0,
			"2024-01-01",
			"2024-12-31",
			"Leap year February 29 should be handled correctly",
		},
		{
			"leap_year_custom_reset",
			time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			2, 29, 0, // February 29 reset (only valid in leap years)
			"2024-02-29",
			"2025-02-28", // Next year is not leap
			"Leap year custom reset should handle non-leap following year",
		},
		// Test with year override (for testing)
		// Note: Override only works if different from current actual year
		{
			"override_year_test",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			1, 1, 2030, // Override to 2030 (far future to ensure it's different from current)
			"2030-01-01",
			"2030-12-31",
			"Override year should be used when set for testing",
		},
		// Edge cases
		{
			"december_31_reset",
			time.Date(2024, 12, 30, 23, 59, 59, 0, time.UTC),
			12, 31, 0,
			"2023-12-31",
			"2024-12-30",
			"December 31 reset should handle year boundary correctly",
		},
		{
			"mid_month_reset",
			time.Date(2024, 6, 14, 23, 59, 59, 0, time.UTC),
			6, 15, 0,
			"2023-06-15",
			"2024-06-14",
			"Mid-month reset before reset date",
		},
		{
			"mid_month_reset_on_date",
			time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			6, 15, 0,
			"2024-06-15",
			"2025-06-14",
			"Mid-month reset on reset date",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker with custom reset date
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					ResetMonth: tt.resetMonth,
					ResetDay:   tt.resetDay,
				},
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Override year if specified (for testing)
			if tt.overrideYear != 0 {
				tracker.SetCurrentYearForTesting(tt.overrideYear)
			}

			// Test getYearDateRange
			startDate, endDate := tracker.getYearDateRange(tt.currentTime)

			assert.Equal(t, tt.expectedStartDate, startDate,
				"Start date mismatch for %s", tt.currentTime.Format(time.DateOnly))
			assert.Equal(t, tt.expectedEndDate, endDate,
				"End date mismatch for %s", tt.currentTime.Format(time.DateOnly))

			t.Logf("✓ Date range correct: %s to %s", startDate, endDate)
		})
	}
}

// TestGetSeasonDateRange_CriticalReliability tests seasonal date range calculations
// CRITICAL: Wrong season ranges cause incorrect seasonal tracking
func TestGetSeasonDateRange_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		seasonName    string
		currentTime   time.Time
		customSeasons map[string]conf.Season
		expectedStart string
		expectedEnd   string
		description   string
	}{
		// Default seasons (Northern Hemisphere)
		{
			"spring_in_summer",
			"spring",
			time.Date(2024, 8, 15, 12, 0, 0, 0, time.UTC),
			nil, // Use defaults
			"2024-03-20",
			"2024-06-19", // 3 months minus 1 day
			"Spring range requested in summer",
		},
		{
			"summer_in_summer",
			"summer",
			time.Date(2024, 8, 15, 12, 0, 0, 0, time.UTC),
			nil,
			"2024-06-21",
			"2024-09-20",
			"Summer range requested in summer",
		},
		{
			"fall_in_winter",
			"fall",
			time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			nil,
			"2023-09-22", // Previous year's fall
			"2023-12-21",
			"Fall range requested in winter should use previous year",
		},
		{
			"fall_on_last_day_of_fall_dec20",
			"fall",
			time.Date(2024, 12, 20, 12, 0, 0, 0, time.UTC), // Dec 20 is still fall (winter starts Dec 21)
			nil,
			"2024-09-22", // Current year's fall - BUG FIX: was incorrectly returning 2023
			"2024-12-21",
			"Fall range on Dec 20 (still fall) should use current year",
		},
		{
			"fall_on_first_day_of_winter_dec21",
			"fall",
			time.Date(2024, 12, 21, 12, 0, 0, 0, time.UTC), // Dec 21 is winter (fall just ended)
			nil,
			"2024-09-22", // Current year's fall that just ended
			"2024-12-21",
			"Fall range on Dec 21 (winter started) should still use current year's fall",
		},
		{
			"winter_in_january",
			"winter",
			time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			nil,
			"2023-12-21", // Winter started in previous year
			"2024-03-20",
			"Winter in January should adjust to previous year start",
		},
		{
			"winter_in_december",
			"winter",
			time.Date(2024, 12, 25, 12, 0, 0, 0, time.UTC),
			nil,
			"2024-12-21", // Current year's winter
			"2025-03-20",
			"Winter in December should use current year",
		},
		// Custom seasons
		{
			"custom_meteorological_spring",
			"spring",
			time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC),
			map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 1},
				"summer": {StartMonth: 6, StartDay: 1},
				"fall":   {StartMonth: 9, StartDay: 1},
				"winter": {StartMonth: 12, StartDay: 1},
			},
			"2024-03-01",
			"2024-05-31",
			"Custom meteorological spring (March 1)",
		},
		{
			"custom_winter_december_start",
			"winter",
			time.Date(2024, 2, 15, 12, 0, 0, 0, time.UTC),
			map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 1},
				"summer": {StartMonth: 6, StartDay: 1},
				"fall":   {StartMonth: 9, StartDay: 1},
				"winter": {StartMonth: 12, StartDay: 1},
			},
			"2023-12-01", // Adjusted to previous year
			"2024-02-29", // Leap year February
			"Custom winter in February should adjust to previous December",
		},
		// Edge cases
		{
			"unknown_season",
			"unknown",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			nil,
			"",
			"",
			"Unknown season should return empty strings",
		},
		{
			"invalid_season_definition",
			"invalid",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			map[string]conf.Season{
				"invalid": {StartMonth: 0, StartDay: 0}, // Invalid month/day
			},
			"",
			"",
			"Invalid season definition should return empty strings",
		},
		// Leap year boundary
		{
			"season_ending_leap_february",
			"winter",
			time.Date(2024, 3, 1, 0, 0, 0, 0, time.UTC),
			map[string]conf.Season{
				"winter": {StartMonth: 12, StartDay: 1},
				"spring": {StartMonth: 3, StartDay: 1},
			},
			"2023-12-01",
			"2024-02-29", // Leap year
			"Winter ending in leap year February",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker with custom seasons if provided
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: true,
				},
			}

			if tt.customSeasons != nil {
				// Custom seasons need to be set via appropriate configuration
				// For now, we'll use the default seasons
				// TODO: Add support for custom seasons in test configuration
				t.Skip("Custom seasons not yet implemented")
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Test getSeasonDateRange
			startDate, endDate := tracker.getSeasonDateRange(tt.seasonName, tt.currentTime)

			assert.Equal(t, tt.expectedStart, startDate,
				"Start date mismatch for season %s", tt.seasonName)
			assert.Equal(t, tt.expectedEnd, endDate,
				"End date mismatch for season %s", tt.seasonName)

			if tt.expectedStart != "" {
				t.Logf("✓ Season range correct: %s to %s", startDate, endDate)
			} else {
				t.Logf("✓ Correctly returned empty for invalid season")
			}
		})
	}
}

// TestIsWithinCurrentYear_CriticalReliability tests year boundary checking
// CRITICAL: Determines if species should be included in yearly tracking
func TestIsWithinCurrentYear_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		detectionTime  time.Time
		currentYear    int
		resetMonth     int
		resetDay       int
		expectedWithin bool
		description    string
	}{
		// Standard calendar year (Jan 1 reset)
		{
			"current_year_mid",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			2024, 1, 1,
			true,
			"Mid-year detection should be within current year",
		},
		{
			"current_year_start",
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			2024, 1, 1,
			true,
			"Year start should be within current year",
		},
		{
			"current_year_end",
			time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			2024, 1, 1,
			true,
			"Year end should be within current year",
		},
		{
			"previous_year",
			time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC),
			2024, 1, 1,
			false,
			"Previous year should not be within current year",
		},
		{
			"next_year",
			time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			2024, 1, 1,
			false,
			"Next year should not be within current year",
		},
		// Custom tracking year (July 1 reset)
		{
			"tracking_year_before_reset",
			time.Date(2024, 6, 30, 23, 59, 59, 0, time.UTC),
			2024, 7, 1,
			false, // In tracking year 2023 (July 2023 - June 2024), not 2024
			"Before July 1 should be in previous tracking year",
		},
		{
			"tracking_year_on_reset",
			time.Date(2024, 7, 1, 0, 0, 0, 0, time.UTC),
			2024, 7, 1,
			true, // Start of FY 2024-2025
			"July 1 should start new tracking year",
		},
		{
			"tracking_year_after_reset",
			time.Date(2024, 8, 15, 12, 0, 0, 0, time.UTC),
			2024, 7, 1,
			true,
			"After July 1 should be in current tracking year",
		},
		{
			"tracking_year_next_calendar_year",
			time.Date(2025, 3, 15, 12, 0, 0, 0, time.UTC),
			2024, 7, 1,
			true, // Still in FY 2024-2025
			"March of next calendar year should still be in tracking year",
		},
		{
			"tracking_year_past_next_reset",
			time.Date(2025, 7, 1, 0, 0, 0, 0, time.UTC),
			2024, 7, 1,
			false, // Now in FY 2025-2026
			"Next tracking year should not be within current",
		},
		// Edge cases
		{
			"leap_year_february_29",
			time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			2024, 1, 1,
			true,
			"Leap year February 29 should be within year",
		},
		{
			"year_zero_unset",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			0, 1, 1, // Year not set
			true, // Should use detection time's year
			"Unset year should use detection time's year",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					ResetMonth: tt.resetMonth,
					ResetDay:   tt.resetDay,
				},
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Always set the current year for testing to ensure consistent behavior
			tracker.SetCurrentYearForTesting(tt.currentYear)

			// Test isWithinCurrentYear
			withinYear := tracker.isWithinCurrentYear(tt.detectionTime)

			assert.Equal(t, tt.expectedWithin, withinYear,
				"Within year mismatch for %s", tt.detectionTime.Format(time.DateOnly))

			t.Logf("✓ Correctly determined within year: %v", withinYear)
		})
	}
}

// TestShouldResetYear_CriticalReliability tests year reset logic
// CRITICAL: Determines when to clear yearly tracking data
func TestShouldResetYear_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		currentTime   time.Time
		trackerYear   int
		expectedReset bool
		description   string
	}{
		{
			"uninitialized_tracker",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			0, // Never initialized
			true,
			"Uninitialized tracker should reset",
		},
		{
			"same_year",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			2024,
			false,
			"Same year should not reset",
		},
		{
			"next_year",
			time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			2024,
			true,
			"Next year should trigger reset",
		},
		{
			"previous_year_somehow",
			time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC),
			2024,
			false,
			"Previous year (shouldn't happen) should not reset",
		},
		{
			"far_future",
			time.Date(2030, 1, 1, 0, 0, 0, 0, time.UTC),
			2024,
			true,
			"Far future should trigger reset",
		},
		{
			"year_boundary_before_midnight",
			time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			2024,
			false,
			"Before midnight on Dec 31 should not reset",
		},
		{
			"year_boundary_after_midnight",
			time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			2024,
			true,
			"After midnight on Jan 1 should reset",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled: true,
				},
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Set current year
			tracker.currentYear = tt.trackerYear

			// Test shouldResetYear
			shouldReset := tracker.shouldResetYear(tt.currentTime)

			assert.Equal(t, tt.expectedReset, shouldReset,
				"Reset mismatch for year %d at %s",
				tt.trackerYear, tt.currentTime.Format(time.DateOnly))

			t.Logf("✓ Correctly determined reset: %v", shouldReset)
		})
	}
}

// TestCheckAndResetPeriods_DateRange tests period transition logic
// CRITICAL: Ensures proper state transitions for yearly and seasonal tracking
//
//nolint:gocognit // Table-driven test for date range period resets
func TestCheckAndResetPeriods_DateRange(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		currentTime      time.Time
		initialYear      int
		initialSeason    string
		setupTracker     func(*SpeciesTracker)
		expectedYear     int
		expectedSeason   string
		expectYearReset  bool
		expectSeasonInit bool
		description      string
	}{
		{
			"year_transition",
			time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			2024,
			"winter",
			func(tracker *SpeciesTracker) {
				// Add some data that should be cleared
				tracker.speciesThisYear["Old_Species"] = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
			},
			2025,
			"winter",
			true,
			false,
			"Year transition should reset yearly data",
		},
		{
			"season_transition",
			time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC), // Summer solstice
			2024,
			"spring",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
			},
			2024,
			"summer",
			false,
			true,
			"Season transition should initialize new season",
		},
		{
			"no_transition",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			2024,
			"spring",
			nil,
			2024,
			"spring",
			false,
			false,
			"No transition should keep current periods",
		},
		{
			"both_transitions",
			time.Date(2025, 3, 20, 0, 0, 0, 0, time.UTC), // New year and spring equinox
			2024,
			"winter",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				tracker.speciesThisYear["Old_Species"] = time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC)
			},
			2025,
			"spring",
			true,
			true,
			"Both year and season should transition",
		},
		{
			"uninitialized_year",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			0, // Uninitialized
			"",
			nil,
			2024,
			"spring", // Will be computed
			true,
			false,
			"Uninitialized year should be set",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					ResetMonth: 1,
					ResetDay:   1,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: false, // Will be overridden in setup if needed
				},
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Set initial state
			tracker.currentYear = tt.initialYear
			tracker.currentSeason = tt.initialSeason

			if tt.setupTracker != nil {
				tt.setupTracker(tracker)
			}

			// Remember initial state for comparison
			initialYearlyCount := len(tracker.speciesThisYear)

			// Test checkAndResetPeriods
			tracker.checkAndResetPeriods(tt.currentTime)

			// Verify year
			assert.Equal(t, tt.expectedYear, tracker.currentYear,
				"Year mismatch after period check")

			// Verify season if seasonal tracking enabled
			if tracker.seasonalEnabled {
				assert.Equal(t, tt.expectedSeason, tracker.currentSeason,
					"Season mismatch after period check")

				if tt.expectSeasonInit {
					_, exists := tracker.speciesBySeason[tt.expectedSeason]
					assert.True(t, exists, "New season map should be initialized")
				}
			}

			// Verify year reset
			if tt.expectYearReset && initialYearlyCount > 0 {
				assert.Empty(t, tracker.speciesThisYear,
					"Yearly data should be cleared on reset")
				assert.Empty(t, tracker.statusCache,
					"Status cache should be cleared on year reset")
			}

			t.Logf("✓ Period transitions handled correctly")
		})
	}
}
