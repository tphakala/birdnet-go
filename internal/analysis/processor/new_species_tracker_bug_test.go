package processor

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBug1_WinterAdjustmentInGetSeasonDateRange demonstrates the bug in getSeasonDateRange
// where winter is incorrectly adjusted to previous year for ALL months except December
func TestBug1_WinterAdjustmentInGetSeasonDateRange(t *testing.T) {
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

	// Test August 24 - winter should NOT have started yet
	aug24 := time.Date(2025, 8, 24, 17, 42, 39, 0, time.UTC)
	
	startDate, endDate := tracker.getSeasonDateRange("winter", aug24)
	
	t.Logf("Winter range for August 24: [%s, %s]", startDate, endDate)
	
	// BUG: Returns [2024-12-21, 2025-08-24] because of incorrect winter adjustment
	// SHOULD return [2025-08-24, 2025-08-24] (empty range)
	
	if startDate != endDate {
		t.Errorf("BUG CONFIRMED: Winter in August returns non-empty range [%s, %s]", startDate, endDate)
		t.Logf("  This happens because getSeasonDateRange incorrectly adjusts winter to previous year")
		t.Logf("  The buggy condition: season.month >= 12 && now.Month() < time.Month(season.month)")
		t.Logf("  For winter (month=12) in August (month=8): 12 >= 12 && 8 < 12 = true && true = true")
		t.Logf("  This causes winter to be adjusted to 2024-12-21, which is in the past!")
	}
	
	// The correct logic should be: only adjust winter in early months (Jan-May)
	// Like in computeCurrentSeason: seasonStart.month >= 12 && currentMonth < 6
}

// TestBug2_SeasonBoundaryIssues demonstrates issues with season boundaries
func TestBug2_SeasonBoundaryIssues(t *testing.T) {
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
		date           string
		expectedSeason string
		actualSeason   string // What we actually get
		isBug          bool
	}{
		// These are the boundary issues found in tests
		{"2025-06-21", "summer", "spring", true},  // First day of summer shows as spring
		{"2025-09-22", "fall", "summer", true},    // First day of fall shows as summer
		{"2025-12-21", "winter", "fall", true},    // First day of winter shows as fall
	}

	for _, tt := range tests {
		testTime, err := time.Parse("2006-01-02", tt.date)
		require.NoError(t, err)
		testTime = testTime.Add(12 * time.Hour) // Noon

		actualSeason := tracker.getCurrentSeason(testTime)
		
		if tt.isBug {
			t.Logf("BUG: %s should be %s but returns %s", tt.date, tt.expectedSeason, actualSeason)
			if actualSeason == tt.actualSeason {
				t.Logf("  CONFIRMED: Season boundary off by one day")
			}
		}
	}
}

// TestBug3_InitializationDateRanges shows the invalid date ranges during initialization
func TestBug3_InitializationDateRanges(t *testing.T) {
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
	
	// Simulate what happens during loadSeasonalDataFromDatabase
	now := time.Date(2025, 8, 24, 17, 42, 39, 0, time.UTC)
	
	t.Log("Date ranges that would be used during initialization on August 24:")
	t.Log("(These are passed to GetSpeciesFirstDetectionInPeriod)")
	
	for seasonName := range tracker.seasons {
		startDate, endDate := tracker.getSeasonDateRange(seasonName, now)
		
		switch {
		case startDate > endDate:
			t.Errorf("INVALID RANGE for %s: start=%s > end=%s", seasonName, startDate, endDate)
			t.Logf("  This would cause database query issues!")
		case startDate == endDate:
			t.Logf("  %s: [%s, %s] (empty - season not started)", seasonName, startDate, endDate)
		default:
			t.Logf("  %s: [%s, %s]", seasonName, startDate, endDate)
			
			// Check if this makes sense
			if seasonName == "fall" && startDate > now.Format("2006-01-02") {
				t.Errorf("    ERROR: Fall start date %s is in the future!", startDate)
			}
			if seasonName == "winter" && startDate < "2025-01-01" && now.Month() >= 6 {
				t.Errorf("    ERROR: Winter adjusted to previous year (%s) in %s!", 
					startDate, now.Month())
			}
		}
	}
}

// TestProposedFix shows what the correct logic should be
func TestProposedFix(t *testing.T) {
	t.Log("PROPOSED FIX for getSeasonDateRange winter adjustment:")
	t.Log("")
	t.Log("Current BUGGY code (line 626-628):")
	t.Log("  if season.month >= 12 && now.Month() < time.Month(season.month) {")
	t.Log("")
	t.Log("Should be changed to match computeCurrentSeason logic:")
	t.Log("  if season.month >= 12 && int(now.Month()) < 6 {")
	t.Log("")
	t.Log("This ensures winter is only adjusted to previous year in Jan-May, not Aug-Nov!")
	
	// Demonstrate the fix
	winterMonth := 12
	
	for month := 1; month <= 12; month++ {
		currentBuggy := winterMonth >= 12 && month < winterMonth
		proposedFix := winterMonth >= 12 && month < 6
		
		shouldAdjust := month >= 1 && month <= 5 // Winter should only adjust Jan-May
		
		status := ""
		if currentBuggy != shouldAdjust {
			status = "BUG!"
		}
		if proposedFix != shouldAdjust {
			status = "FIX WRONG!"
		}
		if proposedFix == shouldAdjust && currentBuggy != shouldAdjust {
			status = "FIXED!"
		}
		
		t.Logf("Month %2d: Current=%5v, Proposed=%5v, Should=%5v %s", 
			month, currentBuggy, proposedFix, shouldAdjust, status)
	}
}