package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// Test helper to create a standard tracker for testing
func createTestTracker(t *testing.T) *SpeciesTracker {
	t.Helper()
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
		},
	}
	return NewTrackerFromSettings(nil, settings)
}

// TestWinterAdjustmentBugFix verifies the core winter adjustment bug is fixed
func TestWinterAdjustmentBugFix(t *testing.T) {
	t.Parallel()
	tracker := createTestTracker(t)

	// Test August 24, 2025 - the original bug report date
	aug24 := time.Date(2025, 8, 24, 17, 42, 39, 0, time.UTC)

	startDate, endDate := tracker.getSeasonDateRange("winter", aug24)

	// After fix: winter should return proper 3-month range regardless of when asked
	assert.Equal(t, "2025-12-21", startDate,
		"Winter should return proper winter start date")
	assert.Equal(t, "2026-03-20", endDate,
		"Winter should return proper winter end date")

	// Verify the current season is correctly detected as summer
	currentSeason := tracker.getCurrentSeason(aug24)
	assert.Equal(t, "summer", currentSeason,
		"August 24, 2025 should be summer, not %s", currentSeason)
}

// TestWinterAdjustmentLogic tests winter adjustment for all months using actual API calls
func TestWinterAdjustmentLogic(t *testing.T) {
	tests := []struct {
		month             int
		expectedStartYear int
		description       string
	}{
		{1, 2024, "January should adjust winter to previous year"},
		{2, 2024, "February should adjust winter to previous year"},
		{3, 2024, "March should adjust winter to previous year"},
		{4, 2024, "April should adjust winter to previous year"},
		{5, 2024, "May should adjust winter to previous year"},
		{6, 2025, "June should use current year winter (empty range)"},
		{7, 2025, "July should use current year winter (empty range)"},
		{8, 2025, "August should use current year winter (empty range)"},
		{9, 2025, "September should use current year winter (empty range)"},
		{10, 2025, "October should use current year winter (empty range)"},
		{11, 2025, "November should use current year winter (empty range)"},
		{12, 2025, "December (day 15) should use current year winter (empty range)"},
	}

	tracker := createTestTracker(t)

	for _, tt := range tests {
		t.Run(time.Month(tt.month).String(), func(t *testing.T) {
			t.Parallel()
			testTime := time.Date(2025, time.Month(tt.month), 15, 12, 0, 0, 0, time.UTC)
			startDate, endDate := tracker.getSeasonDateRange("winter", testTime)

			switch tt.expectedStartYear {
			case 2024:
				// Should get range starting from previous year's winter (proper 3-month range)
				assert.Equal(t, "2024-12-21", startDate,
					"Winter in %s should start from previous year", time.Month(tt.month))
				assert.Equal(t, "2025-03-20", endDate,
					"Winter range should end at spring start (Mar 20)")
			default:
				// For months 6-12: winter season ranges from current year Dec to next year Mar
				assert.Equal(t, "2025-12-21", startDate,
					"Winter in %s should start from current year Dec 21", time.Month(tt.month))
				assert.Equal(t, "2026-03-20", endDate,
					"Winter in %s should end at next year Mar 20", time.Month(tt.month))
			}
		})
	}
}

// TestSeasonDateRanges tests date range calculation for various scenarios
func TestSeasonDateRanges(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		currentDate   string
		season        string
		expectedStart string
		expectedEnd   string
		description   string
	}{
		// August 24 test cases - regression test for winter adjustment bug
		{
			"August24_Spring",
			"2025-08-24",
			"spring",
			"2025-03-20",
			"2025-06-19",
			"Spring range for August 24 (consistent 3-month range)",
		},
		{
			"August24_Summer",
			"2025-08-24",
			"summer",
			"2025-06-21",
			"2025-09-20",
			"Summer range for August 24 (consistent 3-month range)",
		},
		{
			"August24_Fall",
			"2025-08-24",
			"fall",
			"2025-09-22",
			"2025-12-21",
			"Fall range for August 24 (consistent 3-month range)",
		},
		{
			"August24_Winter",
			"2025-08-24",
			"winter",
			"2025-12-21",
			"2026-03-20",
			"Winter range for August 24 (consistent 3-month range - regression test)",
		},

		// Winter spanning years
		{
			"January_Winter",
			"2025-01-15",
			"winter",
			"2024-12-21",
			"2025-03-20",
			"Winter range spans from previous year (consistent 3-month range)",
		},
		{
			"January_Spring",
			"2025-01-15",
			"spring",
			"2025-03-20",
			"2025-06-19",
			"Spring range in January (consistent 3-month range)",
		},

		// Winter just starting
		{
			"December22_Winter",
			"2025-12-22",
			"winter",
			"2025-12-21",
			"2026-03-20",
			"Winter just started on Dec 22 (consistent 3-month range)",
		},
		{
			"December20_Winter",
			"2025-12-20",
			"winter",
			"2025-12-21",
			"2026-03-20",
			"Winter not yet started on Dec 20 (consistent 3-month range)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			tracker := createTestTracker(t)
			testTime, err := time.Parse(time.DateOnly, tt.currentDate)
			require.NoError(t, err)
			testTime = testTime.Add(17*time.Hour + 42*time.Minute + 39*time.Second)

			startDate, endDate := tracker.getSeasonDateRange(tt.season, testTime)

			// Check for invalid range (start > end)
			assert.LessOrEqual(t, startDate, endDate,
				"INVALID RANGE for %s: start=%s > end=%s", tt.description, startDate, endDate)

			assert.Equal(t, tt.expectedStart, startDate,
				"Start date mismatch for %s", tt.description)
			assert.Equal(t, tt.expectedEnd, endDate,
				"End date mismatch for %s", tt.description)
		})
	}
}

// TestCurrentSeasonDetection tests season detection for key dates
func TestCurrentSeasonDetection(t *testing.T) {
	t.Parallel()

	tests := []struct {
		date           string
		expectedSeason string
		description    string
		knownBoundary  bool // true for dates with known boundary issues
	}{
		// Test regression scenario
		{"2025-08-24", "summer", "August 24 - regression test for winter adjustment bug", false},

		// Test each season's middle
		{"2025-01-15", "winter", "Middle of winter", false},
		{"2025-04-15", "spring", "Middle of spring", false},
		{"2025-07-15", "summer", "Middle of summer", false},
		{"2025-10-15", "fall", "Middle of fall", false},

		// Test boundary dates - all working correctly now
		{"2025-03-20", "spring", "First day of spring", false},
		{"2025-06-21", "summer", "First day of summer", false}, // Fixed boundary
		{"2025-09-22", "fall", "First day of fall", false},     // Fixed boundary
		{"2025-12-21", "winter", "First day of winter", false}, // Fixed boundary
	}

	for _, tt := range tests {
		t.Run(tt.date+"_"+tt.expectedSeason, func(t *testing.T) {
			t.Parallel()
			tracker := createTestTracker(t)
			testTime, err := time.Parse(time.DateOnly, tt.date)
			require.NoError(t, err)
			testTime = testTime.Add(12 * time.Hour) // Noon

			actualSeason := tracker.getCurrentSeason(testTime)

			// Handle different test scenarios
			switch {
			case tt.date == "2025-08-24":
				// Critical regression test must pass
				assert.Equal(t, tt.expectedSeason, actualSeason,
					"CRITICAL: %s should be %s (regression test)", tt.description, tt.expectedSeason)
			case tt.knownBoundary && actualSeason != tt.expectedSeason:
				// Skip known boundary issues to keep CI clean
				t.Skipf("Known boundary issue: %s expected %s, got %s",
					tt.description, tt.expectedSeason, actualSeason)
			default:
				// All other tests should pass
				assert.Equal(t, tt.expectedSeason, actualSeason,
					"%s should be %s", tt.description, tt.expectedSeason)
			}
		})
	}
}

// TestSeasonConfiguration verifies the season configuration is correct
func TestSeasonConfiguration(t *testing.T) {
	t.Parallel()
	tracker := createTestTracker(t)

	expectedSeasons := map[string]seasonDates{
		"spring": {month: 3, day: 20},  // March 20
		"summer": {month: 6, day: 21},  // June 21
		"fall":   {month: 9, day: 22},  // September 22
		"winter": {month: 12, day: 21}, // December 21
	}

	assert.Len(t, tracker.seasons, 4, "Should have 4 seasons configured")

	for name, expected := range expectedSeasons {
		actual, exists := tracker.seasons[name]
		require.True(t, exists, "Season %s should exist", name)
		assert.Equal(t, expected.month, actual.month,
			"Season %s should start in month %d", name, expected.month)
		assert.Equal(t, expected.day, actual.day,
			"Season %s should start on day %d", name, expected.day)
	}
}

// TestDateValidation ensures no invalid date ranges are generated
func TestDateValidation(t *testing.T) {
	t.Parallel()
	tracker := createTestTracker(t)

	testDates := []string{
		"2025-01-15", "2025-03-15", "2025-05-15", "2025-07-15",
		"2025-08-24", "2025-09-15", "2025-11-15", "2025-12-15",
	}

	for _, dateStr := range testDates {
		testTime, err := time.Parse(time.DateOnly, dateStr)
		require.NoError(t, err)

		for seasonName := range tracker.seasons {
			startDate, endDate := tracker.getSeasonDateRange(seasonName, testTime)

			// Critical check: no invalid ranges
			assert.LessOrEqual(t, startDate, endDate,
				"INVALID RANGE for %s on %s: start=%s > end=%s",
				seasonName, dateStr, startDate, endDate)

			// Ensure dates are valid format
			_, err1 := time.Parse(time.DateOnly, startDate)
			_, err2 := time.Parse(time.DateOnly, endDate)
			assert.NoError(t, err1, "Start date should be valid: %s", startDate)
			assert.NoError(t, err2, "End date should be valid: %s", endDate)
		}
	}
}

// TestSeasonYearAdjustmentConstant verifies the constant and shouldAdjustYearForSeason behavior
func TestSeasonYearAdjustmentConstant(t *testing.T) {
	t.Parallel()
	tracker := createTestTracker(t)

	// Verify the constant value makes sense
	assert.Equal(t, time.June, yearCrossingCutoffMonth,
		"Year crossing cutoff should be June")

	// Test the logic with the constant and shouldAdjustYearForSeason function
	testCases := []struct {
		month           int
		expectedCompare bool // Expected result of month < cutoff
		expectedAdjust  bool // Expected result of shouldAdjustYearForSeason for December season (current detection)
	}{
		{1, true, true},    // January < June, should adjust winter
		{5, true, true},    // May < June, should adjust winter
		{6, false, false},  // June == June, should not adjust
		{8, false, false},  // August > June, should not adjust
		{12, false, false}, // December > June, should not adjust
	}

	for _, tc := range testCases {
		// Test direct comparison
		actualCompare := time.Month(tc.month) < yearCrossingCutoffMonth
		assert.Equal(t, tc.expectedCompare, actualCompare,
			"Month %d comparison with cutoff should be %v", tc.month, tc.expectedCompare)

		// Test shouldAdjustYearForSeason with December season (current detection)
		testTime := time.Date(2025, time.Month(tc.month), 15, 12, 0, 0, 0, time.UTC)
		actualAdjust := tracker.shouldAdjustYearForSeason(testTime, time.December, false)
		assert.Equal(t, tc.expectedAdjust, actualAdjust,
			"shouldAdjustYearForSeason for month %d with December season should be %v", tc.month, tc.expectedAdjust)

		// Additional test: non-year-crossing seasons (before October) should never adjust
		if tc.month < 10 {
			nonCrossingAdjust := tracker.shouldAdjustYearForSeason(testTime, time.Month(tc.month), false)
			assert.False(t, nonCrossingAdjust,
				"shouldAdjustYearForSeason for month %d with non-year-crossing season should always be false", tc.month)
		}
	}
}

// TestSeasonBoundariesFixed verifies that season boundary detection works correctly
// Previously these were "known boundary issues" but have been resolved
func TestSeasonBoundariesFixed(t *testing.T) {
	t.Parallel()

	tracker := createTestTracker(t)

	seasonBoundaries := []struct {
		date           string
		expectedSeason string
		description    string
	}{
		{"2025-06-21", "summer", "First day of summer"},
		{"2025-09-22", "fall", "First day of fall"},
		{"2025-12-21", "winter", "First day of winter"},
		{"2025-03-20", "spring", "First day of spring"},
	}

	for _, boundary := range seasonBoundaries {
		t.Run(boundary.description, func(t *testing.T) {
			testTime, err := time.Parse(time.DateOnly, boundary.date)
			require.NoError(t, err)
			testTime = testTime.Add(12 * time.Hour)

			actualSeason := tracker.getCurrentSeason(testTime)
			assert.Equal(t, boundary.expectedSeason, actualSeason,
				"Season boundary detection for %s", boundary.description)
		})
	}
}

// TestEquatorialSeasonTracking verifies that equatorial regions with wet/dry seasons work correctly
func TestEquatorialSeasonTracking(t *testing.T) {
	t.Parallel()

	// Create tracker with equatorial seasons
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
			Seasons: map[string]conf.Season{
				"wet1": {StartMonth: 3, StartDay: 1},  // March-May wet season
				"dry1": {StartMonth: 6, StartDay: 1},  // June-August dry season
				"wet2": {StartMonth: 9, StartDay: 1},  // September-November wet season
				"dry2": {StartMonth: 12, StartDay: 1}, // December-February dry season
			},
		},
	}
	tracker := NewTrackerFromSettings(nil, settings)

	testCases := []struct {
		date           time.Time
		expectedSeason string
		description    string
	}{
		{
			date:           time.Date(2024, 3, 15, 10, 0, 0, 0, time.UTC),
			expectedSeason: "wet1",
			description:    "March should be in first wet season",
		},
		{
			date:           time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC),
			expectedSeason: "dry1",
			description:    "June should be in first dry season",
		},
		{
			date:           time.Date(2024, 9, 15, 10, 0, 0, 0, time.UTC),
			expectedSeason: "wet2",
			description:    "September should be in second wet season",
		},
		{
			date:           time.Date(2024, 12, 15, 10, 0, 0, 0, time.UTC),
			expectedSeason: "dry2",
			description:    "December should be in second dry season",
		},
		{
			date:           time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
			expectedSeason: "dry2",
			description:    "January should be in second dry season (continuing from December)",
		},
		{
			date:           time.Date(2024, 2, 28, 10, 0, 0, 0, time.UTC),
			expectedSeason: "dry2",
			description:    "February should be in second dry season",
		},
		{
			date:           time.Date(2024, 4, 1, 10, 0, 0, 0, time.UTC),
			expectedSeason: "wet1",
			description:    "April should be in first wet season",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tracker.mu.Lock()
			season := tracker.computeCurrentSeason(tc.date)
			tracker.mu.Unlock()

			assert.Equal(t, tc.expectedSeason, season, tc.description)
		})
	}
}

// TestHemisphereDetection verifies latitude-based hemisphere detection
func TestHemisphereDetection(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		latitude           float64
		expectedHemisphere string
		description        string
	}{
		{
			latitude:           45.0,
			expectedHemisphere: "northern",
			description:        "45° latitude should be northern hemisphere",
		},
		{
			latitude:           -45.0,
			expectedHemisphere: "southern",
			description:        "-45° latitude should be southern hemisphere",
		},
		{
			latitude:           0.0,
			expectedHemisphere: "equatorial",
			description:        "0° latitude should be equatorial",
		},
		{
			latitude:           5.0,
			expectedHemisphere: "equatorial",
			description:        "5° latitude should be equatorial",
		},
		{
			latitude:           -5.0,
			expectedHemisphere: "equatorial",
			description:        "-5° latitude should be equatorial",
		},
		{
			latitude:           10.0,
			expectedHemisphere: "equatorial",
			description:        "10° latitude should be equatorial (at threshold)",
		},
		{
			latitude:           -10.0,
			expectedHemisphere: "equatorial",
			description:        "-10° latitude should be equatorial (at threshold)",
		},
		{
			latitude:           10.1,
			expectedHemisphere: "northern",
			description:        "10.1° latitude should be northern hemisphere",
		},
		{
			latitude:           -10.1,
			expectedHemisphere: "southern",
			description:        "-10.1° latitude should be southern hemisphere",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			hemisphere := conf.DetectHemisphere(tc.latitude)
			assert.Equal(t, tc.expectedHemisphere, hemisphere, tc.description)
		})
	}
}

// TestGetDefaultSeasons verifies that default seasons are returned correctly based on hemisphere
func TestGetDefaultSeasons(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		latitude        float64
		expectedSeasons []string
		description     string
	}{
		{
			latitude:        45.0, // Northern
			expectedSeasons: []string{"spring", "summer", "fall", "winter"},
			description:     "Northern hemisphere should get traditional seasons",
		},
		{
			latitude:        -45.0, // Southern
			expectedSeasons: []string{"spring", "summer", "fall", "winter"},
			description:     "Southern hemisphere should get traditional seasons",
		},
		{
			latitude:        0.0, // Equatorial
			expectedSeasons: []string{"wet1", "dry1", "wet2", "dry2"},
			description:     "Equatorial region should get wet/dry seasons",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			seasons := conf.GetDefaultSeasons(tc.latitude)

			// Check that all expected seasons are present
			for _, expectedSeason := range tc.expectedSeasons {
				_, exists := seasons[expectedSeason]
				assert.True(t, exists, "Season %s should exist for %s", expectedSeason, tc.description)
			}

			// Check that we have exactly 4 seasons
			assert.Len(t, seasons, 4, "Should have exactly 4 seasons")
		})
	}
}

// TestSouthernHemisphereSeasonAdjustment verifies that southern hemisphere gets inverted seasons
func TestSouthernHemisphereSeasonAdjustment(t *testing.T) {
	t.Parallel()

	// Get default seasons for northern and southern hemispheres
	northernSeasons := conf.GetDefaultSeasons(45.0)  // Northern hemisphere
	southernSeasons := conf.GetDefaultSeasons(-45.0) // Southern hemisphere

	// Test that southern hemisphere seasons are shifted by 6 months from northern
	testCases := []struct {
		seasonName       string
		northernMonth    int
		expectedSouthern int
		description      string
	}{
		{
			seasonName:       "spring",
			northernMonth:    3, // March in Northern
			expectedSouthern: 9, // September in Southern
			description:      "Spring should be in September for southern hemisphere",
		},
		{
			seasonName:       "summer",
			northernMonth:    6,  // June in Northern
			expectedSouthern: 12, // December in Southern
			description:      "Summer should be in December for southern hemisphere",
		},
		{
			seasonName:       "fall",
			northernMonth:    9, // September in Northern
			expectedSouthern: 3, // March in Southern
			description:      "Fall should be in March for southern hemisphere",
		},
		{
			seasonName:       "winter",
			northernMonth:    12, // December in Northern
			expectedSouthern: 6,  // June in Southern
			description:      "Winter should be in June for southern hemisphere",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			northSeason := northernSeasons[tc.seasonName]
			southSeason := southernSeasons[tc.seasonName]

			assert.Equal(t, tc.northernMonth, northSeason.StartMonth,
				"Northern hemisphere %s should start in month %d", tc.seasonName, tc.northernMonth)
			assert.Equal(t, tc.expectedSouthern, southSeason.StartMonth,
				"Southern hemisphere %s should start in month %d", tc.seasonName, tc.expectedSouthern)
		})
	}
}

// TestHemisphereSeasonTracking verifies season tracking works correctly for each hemisphere
func TestHemisphereSeasonTracking(t *testing.T) {
	t.Parallel()

	// Test northern hemisphere season tracking
	t.Run("Northern Hemisphere", func(t *testing.T) {
		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
			SyncIntervalMinutes:  60,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled:    true,
				WindowDays: 7,
				Seasons:    conf.GetDefaultSeasons(45.0), // Northern hemisphere
			},
		}
		tracker := NewTrackerFromSettings(nil, settings)

		testCases := []struct {
			date           time.Time
			expectedSeason string
		}{
			{time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), "winter"},  // January
			{time.Date(2024, 4, 15, 10, 0, 0, 0, time.UTC), "spring"},  // April
			{time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC), "summer"},  // July
			{time.Date(2024, 10, 15, 10, 0, 0, 0, time.UTC), "fall"},   // October
			{time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC), "winter"}, // December
		}

		for _, tc := range testCases {
			tracker.mu.Lock()
			season := tracker.computeCurrentSeason(tc.date)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expectedSeason, season,
				"Northern hemisphere %s should be %s", tc.date.Month(), tc.expectedSeason)
		}
	})

	// Test southern hemisphere season tracking
	t.Run("Southern Hemisphere", func(t *testing.T) {
		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
			SyncIntervalMinutes:  60,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled:    true,
				WindowDays: 7,
				Seasons:    conf.GetDefaultSeasons(-45.0), // Southern hemisphere
			},
		}
		tracker := NewTrackerFromSettings(nil, settings)

		testCases := []struct {
			date           time.Time
			expectedSeason string
		}{
			{time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC), "summer"},  // January (summer in Southern)
			{time.Date(2024, 4, 15, 10, 0, 0, 0, time.UTC), "fall"},    // April (fall in Southern)
			{time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC), "winter"},  // July (winter in Southern)
			{time.Date(2024, 10, 15, 10, 0, 0, 0, time.UTC), "spring"}, // October (spring in Southern)
			{time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC), "summer"}, // December (summer in Southern)
		}

		for _, tc := range testCases {
			tracker.mu.Lock()
			season := tracker.computeCurrentSeason(tc.date)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expectedSeason, season,
				"Southern hemisphere %s should be %s", tc.date.Month(), tc.expectedSeason)
		}
	})
}
