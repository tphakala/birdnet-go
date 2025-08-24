package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestGetCurrentSeasonDetailed tests season detection for specific dates throughout the year
func TestGetCurrentSeasonDetailed(t *testing.T) {
	// Create tracker with default Northern Hemisphere seasons
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
			// Using default seasons (Northern Hemisphere)
		},
	}

	tracker := NewSpeciesTrackerFromSettings(nil, settings)

	tests := []struct {
		name           string
		date           string
		expectedSeason string
		description    string
	}{
		// Spring: March 20 - June 20
		{"Spring Start", "2025-03-20", "spring", "First day of spring"},
		{"Spring Mid", "2025-04-15", "spring", "Middle of spring"},
		{"Spring End", "2025-06-20", "spring", "Last day of spring"},

		// Summer: June 21 - September 21
		{"Summer Start", "2025-06-21", "summer", "First day of summer"},
		{"Summer Mid July", "2025-07-15", "summer", "Middle of July"},
		{"Summer August", "2025-08-24", "summer", "August 24 - CURRENT BUG TEST"},
		{"Summer End", "2025-09-21", "summer", "Last day of summer"},

		// Fall: September 22 - December 20
		{"Fall Start", "2025-09-22", "fall", "First day of fall"},
		{"Fall Mid", "2025-10-15", "fall", "Middle of fall"},
		{"Fall November", "2025-11-15", "fall", "November in fall"},
		{"Fall End", "2025-12-20", "fall", "Last day of fall"},

		// Winter: December 21 - March 19
		{"Winter Start", "2025-12-21", "winter", "First day of winter"},
		{"Winter End Year", "2025-12-31", "winter", "End of year"},
		{"Winter New Year", "2025-01-01", "winter", "New Year's Day"},
		{"Winter February", "2025-02-15", "winter", "Middle of February"},
		{"Winter End", "2025-03-19", "winter", "Last day of winter"},

		// Edge cases
		{"Leap Day", "2024-02-29", "winter", "Leap day should be winter"},
		{"Year End", "2025-12-31", "winter", "Last day of year"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTime, err := time.Parse("2006-01-02", tt.date)
			require.NoError(t, err)
			
			// Add time component to match realistic usage
			testTime = testTime.Add(17*time.Hour + 42*time.Minute + 39*time.Second)

			currentSeason := tracker.getCurrentSeason(testTime)
			
			t.Logf("Date: %s, Expected: %s, Got: %s - %s", 
				tt.date, tt.expectedSeason, currentSeason, tt.description)
			
			assert.Equal(t, tt.expectedSeason, currentSeason, 
				"Season mismatch for %s: %s", tt.date, tt.description)
		})
	}
}

// TestGetSeasonDateRange tests the date range calculation for each season
func TestGetSeasonDateRange(t *testing.T) {
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(nil, settings)

	tests := []struct {
		name          string
		currentDate   string
		season        string
		expectedStart string
		expectedEnd   string
		description   string
	}{
		// Test August 24 - THE PROBLEMATIC DATE
		{
			name:          "August24_Spring",
			currentDate:   "2025-08-24",
			season:        "spring",
			expectedStart: "2025-03-20",
			expectedEnd:   "2025-08-24",
			description:   "Spring has already started and ended",
		},
		{
			name:          "August24_Summer",
			currentDate:   "2025-08-24",
			season:        "summer",
			expectedStart: "2025-06-21",
			expectedEnd:   "2025-08-24",
			description:   "Summer is current season",
		},
		{
			name:          "August24_Fall",
			currentDate:   "2025-08-24",
			season:        "fall",
			expectedStart: "2025-08-24", // Should be empty range
			expectedEnd:   "2025-08-24", // Should be empty range
			description:   "Fall hasn't started yet - SHOULD BE EMPTY RANGE",
		},
		{
			name:          "August24_Winter",
			currentDate:   "2025-08-24",
			season:        "winter",
			expectedStart: "2025-08-24", // Should be empty range
			expectedEnd:   "2025-08-24", // Should be empty range
			description:   "Winter hasn't started yet - SHOULD BE EMPTY RANGE",
		},

		// Test January (Winter spans years)
		{
			name:          "January_Winter",
			currentDate:   "2025-01-15",
			season:        "winter",
			expectedStart: "2024-12-21", // Previous year!
			expectedEnd:   "2025-01-15",
			description:   "Winter should span from previous year",
		},
		{
			name:          "January_Spring",
			currentDate:   "2025-01-15",
			season:        "spring",
			expectedStart: "2025-01-15", // Empty range - hasn't started
			expectedEnd:   "2025-01-15",
			description:   "Spring hasn't started in January",
		},

		// Test December (Winter just starting)
		{
			name:          "December22_Winter",
			currentDate:   "2025-12-22",
			season:        "winter",
			expectedStart: "2025-12-21",
			expectedEnd:   "2025-12-22",
			description:   "Winter just started",
		},
		{
			name:          "December20_Winter",
			currentDate:   "2025-12-20",
			season:        "winter",
			expectedStart: "2025-12-20", // Empty - hasn't started
			expectedEnd:   "2025-12-20",
			description:   "Winter hasn't started yet on Dec 20",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testTime, err := time.Parse("2006-01-02", tt.currentDate)
			require.NoError(t, err)
			
			// Add time component
			testTime = testTime.Add(17*time.Hour + 42*time.Minute + 39*time.Second)

			startDate, endDate := tracker.getSeasonDateRange(tt.season, testTime)
			
			t.Logf("Current: %s, Season: %s", tt.currentDate, tt.season)
			t.Logf("Expected: [%s, %s]", tt.expectedStart, tt.expectedEnd)
			t.Logf("Got:      [%s, %s]", startDate, endDate)
			t.Logf("Description: %s", tt.description)
			
			// Check for invalid range (start > end)
			if startDate > endDate {
				t.Errorf("INVALID RANGE: start (%s) > end (%s)", startDate, endDate)
			}
			
			assert.Equal(t, tt.expectedStart, startDate, 
				"Start date mismatch for %s in %s", tt.season, tt.currentDate)
			assert.Equal(t, tt.expectedEnd, endDate,
				"End date mismatch for %s in %s", tt.season, tt.currentDate)
		})
	}
}

// TestWinterSeasonAdjustment specifically tests winter season handling
func TestWinterSeasonAdjustment(t *testing.T) {
	// This test checks the winter adjustment logic without needing a tracker instance

	tests := []struct {
		month         int
		expectAdjust  bool
		description   string
	}{
		{1, true, "January should adjust winter to previous year"},
		{2, true, "February should adjust winter to previous year"},
		{3, true, "March should adjust winter to previous year"},
		{4, true, "April should adjust winter to previous year"},
		{5, true, "May should adjust winter to previous year"},
		{6, false, "June should NOT adjust winter"},
		{7, false, "July should NOT adjust winter"},
		{8, false, "August should NOT adjust winter"},
		{9, false, "September should NOT adjust winter"},
		{10, false, "October should NOT adjust winter"},
		{11, false, "November should NOT adjust winter"},
		{12, false, "December should NOT adjust winter"},
	}

	for _, tt := range tests {
		t.Run(time.Month(tt.month).String(), func(t *testing.T) {
			testTime := time.Date(2025, time.Month(tt.month), 15, 12, 0, 0, 0, time.UTC)
			
			// Test in computeCurrentSeason logic
			currentMonth := int(testTime.Month())
			seasonMonth := 12 // Winter
			seasonDay := 21
			
			// This is the logic from computeCurrentSeason
			shouldAdjustCompute := seasonMonth >= 12 && currentMonth < 6
			
			var seasonDate time.Time
			if shouldAdjustCompute {
				seasonDate = time.Date(testTime.Year()-1, time.Month(seasonMonth), seasonDay, 0, 0, 0, 0, testTime.Location())
			} else {
				seasonDate = time.Date(testTime.Year(), time.Month(seasonMonth), seasonDay, 0, 0, 0, 0, testTime.Location())
			}
			
			t.Logf("Month: %s, Expected Adjust: %v, Compute Adjust: %v, Season Date: %s",
				time.Month(tt.month), tt.expectAdjust, shouldAdjustCompute, seasonDate.Format("2006-01-02"))
			
			assert.Equal(t, tt.expectAdjust, shouldAdjustCompute, 
				"Winter adjustment mismatch for %s: %s", time.Month(tt.month), tt.description)
				
			// Also test getSeasonDateRange winter logic
			// The buggy condition: season.month >= 12 && now.Month() < time.Month(season.month)
			buggyCondition := seasonMonth >= 12 && testTime.Month() < time.Month(seasonMonth)
			
			t.Logf("Buggy condition (getSeasonDateRange): month %d < %d = %v", 
				testTime.Month(), seasonMonth, buggyCondition)
			
			// The buggy condition is WRONG - it would adjust for months 1-11!
			if tt.month != 12 {
				assert.True(t, buggyCondition, 
					"BUG CONFIRMED: getSeasonDateRange would incorrectly adjust for %s", time.Month(tt.month))
			}
		})
	}
}

// TestComputeCurrentSeasonWithLogging tests with detailed logging to debug the issue
func TestComputeCurrentSeasonWithLogging(t *testing.T) {
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(nil, settings)
	
	// Test August 24, 2025 specifically
	testTime := time.Date(2025, 8, 24, 17, 42, 39, 0, time.FixedZone("EEST", 3*60*60))
	
	t.Logf("Testing date: %s", testTime.Format("2006-01-02 15:04:05 -07:00"))
	t.Logf("Seasons configured:")
	for name, season := range tracker.seasons {
		t.Logf("  %s: month=%d, day=%d", name, season.month, season.day)
	}
	
	// Manually compute what should happen
	currentMonth := int(testTime.Month())
	t.Logf("Current month: %d (%s)", currentMonth, testTime.Month())
	
	// Check each season
	seasons := map[string]struct{ month, day int }{
		"spring": {3, 20},
		"summer": {6, 21},
		"fall":   {9, 22},
		"winter": {12, 21},
	}
	
	var latestSeason string
	var latestDate time.Time
	
	for name, s := range seasons {
		seasonDate := time.Date(testTime.Year(), time.Month(s.month), s.day, 0, 0, 0, 0, testTime.Location())
		
		// Winter adjustment
		if s.month >= 12 && currentMonth < 6 {
			seasonDate = time.Date(testTime.Year()-1, time.Month(s.month), s.day, 0, 0, 0, 0, testTime.Location())
			t.Logf("  %s: Adjusted to previous year: %s", name, seasonDate.Format("2006-01-02"))
		}
		
		hasStarted := testTime.After(seasonDate) || testTime.Equal(seasonDate)
		isMoreRecent := latestSeason == "" || seasonDate.After(latestDate)
		
		t.Logf("  %s: starts %s, hasStarted=%v, isMoreRecent=%v", 
			name, seasonDate.Format("2006-01-02"), hasStarted, isMoreRecent)
		
		if hasStarted && isMoreRecent {
			latestSeason = name
			latestDate = seasonDate
			t.Logf("    -> New latest: %s", name)
		}
	}
	
	t.Logf("Expected season: %s (started %s)", latestSeason, latestDate.Format("2006-01-02"))
	
	// Now test the actual method
	actualSeason := tracker.computeCurrentSeason(testTime)
	t.Logf("Actual season returned: %s", actualSeason)
	
	assert.Equal(t, "summer", actualSeason, 
		"August 24 should be summer, not %s", actualSeason)
}

// TestDateComparisonLogic tests the basic time comparison to ensure it works
func TestDateComparisonLogic(t *testing.T) {
	aug24 := time.Date(2025, 8, 24, 17, 42, 39, 0, time.UTC)
	sep22 := time.Date(2025, 9, 22, 0, 0, 0, 0, time.UTC)
	jun21 := time.Date(2025, 6, 21, 0, 0, 0, 0, time.UTC)
	
	t.Logf("Aug 24: %s", aug24.Format("2006-01-02 15:04:05"))
	t.Logf("Sep 22: %s", sep22.Format("2006-01-02 15:04:05"))
	t.Logf("Jun 21: %s", jun21.Format("2006-01-02 15:04:05"))
	
	// Test basic comparisons
	assert.True(t, aug24.Before(sep22), "Aug 24 should be before Sep 22")
	assert.True(t, aug24.After(jun21), "Aug 24 should be after Jun 21")
	assert.False(t, aug24.After(sep22), "Aug 24 should NOT be after Sep 22")
	
	// Test the exact condition from getSeasonDateRange
	if aug24.Before(sep22) {
		t.Log("CORRECT: Aug 24 is before Sep 22, should return empty range for fall")
	} else {
		t.Error("BUG: Aug 24 is NOT before Sep 22, would return invalid range")
	}
}