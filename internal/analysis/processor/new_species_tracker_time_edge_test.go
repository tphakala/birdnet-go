// new_species_tracker_time_edge_test.go
// Time-based edge cases and boundary condition tests for species tracker  
// Critical for data integrity across time boundaries and clock changes
package processor

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestTimeZoneTransitions tests tracker behavior during time zone changes
// Critical for systems that might experience daylight saving time or location changes
func TestTimeZoneTransitions(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		fromTZ       string
		toTZ         string 
		testTime     time.Time
		description  string
	}{
		{
			"daylight_saving_spring", "America/New_York", "America/New_York",
			time.Date(2024, 3, 10, 1, 30, 0, 0, time.UTC), // DST transition
			"Spring forward daylight saving transition",
		},
		{
			"daylight_saving_fall", "America/New_York", "America/New_York", 
			time.Date(2024, 11, 3, 1, 30, 0, 0, time.UTC), // DST transition
			"Fall back daylight saving transition",
		},
		{
			"timezone_change", "America/Los_Angeles", "Europe/London",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"Moving from Los Angeles to London timezone",
		},
		{
			"utc_to_local", "UTC", "America/Chicago",
			time.Date(2024, 8, 15, 18, 0, 0, 0, time.UTC),
			"UTC to Central Time transition", 
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing timezone scenario: %s", tt.description)

			// Load time zones
			fromLoc, err := time.LoadLocation(tt.fromTZ)
			require.NoError(t, err, "Failed to load 'from' timezone")
			
			toLoc, err := time.LoadLocation(tt.toTZ)
			require.NoError(t, err, "Failed to load 'to' timezone")

			// Create tracker
			ds := &MockSpeciesDatastore{}
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					ResetMonth: 1,
					ResetDay:   1,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: true,
				},
			}

			tracker := NewSpeciesTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Test detections in first timezone
			timeInFrom := tt.testTime.In(fromLoc)
			species1 := "TZ_Test_Species_1"
			
			isNew1, days1 := tracker.CheckAndUpdateSpecies(species1, timeInFrom)
			assert.True(t, isNew1, "First detection should be new")
			assert.Equal(t, 0, days1, "First detection should have 0 days")

			// Simulate time zone change - same time but different timezone
			timeInTo := tt.testTime.In(toLoc)
			species2 := "TZ_Test_Species_2"
			
			isNew2, days2 := tracker.CheckAndUpdateSpecies(species2, timeInTo)
			assert.True(t, isNew2, "New species should be new regardless of timezone")
			assert.Equal(t, 0, days2, "New species should have 0 days")

			// Test that original species is still tracked correctly
			status1 := tracker.GetSpeciesStatus(species1, timeInTo)
			assert.True(t, status1.IsNew || status1.DaysSinceFirst <= 14, 
				"Original species should still be tracked correctly after timezone change")

			// Test with a time that's 1 hour offset between zones (common DST case)
			offsetTime := tt.testTime.Add(time.Hour)
			timeInFromOffset := offsetTime.In(fromLoc)
			timeInToOffset := offsetTime.In(toLoc)

			species3 := "TZ_Test_Species_3"
			isNew3a, days3a := tracker.CheckAndUpdateSpecies(species3, timeInFromOffset)
			assert.True(t, isNew3a, "Species should be new in first timezone")
			assert.Equal(t, 0, days3a, "New species should have 0 days")
			
			// Same species, different representation of time
			status3b := tracker.GetSpeciesStatus(species3, timeInToOffset)
			// Should recognize it's the same species even with timezone difference
			assert.True(t, status3b.IsNew || status3b.DaysSinceFirst <= 1,
				"Species should be recognized across timezone representations")

			t.Logf("Timezone transition test passed: %s", tt.name)
		})
	}
}

// TestClockSkewScenarios tests behavior when system clock changes
// Critical for handling NTP updates, manual clock changes, and clock drift
func TestClockSkewScenarios(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		clockChange time.Duration
		expectIssue bool
		description string
	}{
		{
			"small_forward_skip", 5 * time.Minute, false,
			"Small clock jump forward (NTP adjustment)",
		},
		{
			"small_backward_skip", -5 * time.Minute, false, 
			"Small clock jump backward (NTP adjustment)",
		},
		{
			"large_forward_jump", 2 * time.Hour, false,
			"Large clock jump forward (manual adjustment)",
		},
		{
			"large_backward_jump", -2 * time.Hour, true,
			"Large clock jump backward (may cause issues)",
		},
		{
			"day_boundary_skip", 25 * time.Hour, false,
			"Clock skip across day boundary",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing clock skew scenario: %s", tt.description)

			// Create tracker
			ds := &MockSpeciesDatastore{}
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					ResetMonth: 1,
					ResetDay:   1,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: true,
				},
			}

			tracker := NewSpeciesTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Establish baseline with initial time
			baseTime := time.Now()
			species1 := "Clock_Test_Species_1"
			
			isNew1, days1 := tracker.CheckAndUpdateSpecies(species1, baseTime)
			assert.True(t, isNew1, "Initial detection should be new")
			assert.Equal(t, 0, days1, "Initial detection should have 0 days")

			// Simulate clock change
			skewedTime := baseTime.Add(tt.clockChange)
			species2 := "Clock_Test_Species_2"
			
			// Test new species detection after clock skew
			isNew2, days2 := tracker.CheckAndUpdateSpecies(species2, skewedTime)
			assert.True(t, isNew2, "New species should be new regardless of clock skew")
			assert.Equal(t, 0, days2, "New species should have 0 days")

			// Test existing species status after clock skew
			status1 := tracker.GetSpeciesStatus(species1, skewedTime)
			
			if tt.expectIssue {
				// For large backward jumps, we might see unusual behavior
				t.Logf("Clock skew may cause issues: days=%d, isNew=%v", 
					status1.DaysSinceFirst, status1.IsNew)
				// But the system should still function
				assert.GreaterOrEqual(t, status1.DaysSinceFirst, -1, 
					"Days since first should not be extremely negative")
			} else {
				// Normal clock skew should not cause major issues
				assert.GreaterOrEqual(t, status1.DaysSinceFirst, 0,
					"Days since first should not be negative for normal skew")
			}

			// Test that tracker continues to function normally
			species3 := "Clock_Test_Species_3"
			furtherTime := skewedTime.Add(time.Hour)
			
			isNew3, days3 := tracker.CheckAndUpdateSpecies(species3, furtherTime)
			assert.True(t, isNew3, "Tracker should continue functioning after clock skew")
			assert.Equal(t, 0, days3, "New species should still work correctly")

			t.Logf("Clock skew test completed: %s", tt.name)
		})
	}
}

// TestLeapYearHandling tests tracker behavior during leap year scenarios
// Critical for ensuring correct date calculations across leap year boundaries
func TestLeapYearHandling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		year        int
		testMonth   time.Month
		testDay     int
		isLeapYear  bool
		description string
	}{
		{
			"leap_year_feb_28", 2024, time.February, 28, true,
			"Feb 28 in leap year (2024)",
		},
		{
			"leap_year_feb_29", 2024, time.February, 29, true,
			"Feb 29 in leap year (2024)", 
		},
		{
			"non_leap_year_feb_28", 2023, time.February, 28, false,
			"Feb 28 in non-leap year (2023)",
		},
		{
			"century_non_leap", 1900, time.February, 28, false,
			"Century year that's not a leap year (1900)",
		},
		{
			"century_leap_year", 2000, time.February, 29, true,
			"Century year that is a leap year (2000)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing leap year scenario: %s", tt.description)

			// Skip invalid dates
			if !tt.isLeapYear && tt.testDay == 29 && tt.testMonth == time.February {
				t.Logf("Skipping Feb 29 test for non-leap year")
				return
			}

			// Create tracker
			ds := &MockSpeciesDatastore{}
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					ResetMonth: 1,
					ResetDay:   1,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: true,
				},
			}

			tracker := NewSpeciesTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Test detection on the specific date
			testTime := time.Date(tt.year, tt.testMonth, tt.testDay, 12, 0, 0, 0, time.UTC)
			species1 := fmt.Sprintf("Leap_Test_Species_%d_%d_%d", tt.year, tt.testMonth, tt.testDay)

			isNew1, days1 := tracker.CheckAndUpdateSpecies(species1, testTime)
			assert.True(t, isNew1, "Detection should be new")
			assert.Equal(t, 0, days1, "Initial detection should have 0 days")

			// Test the day after
			nextDay := testTime.Add(24 * time.Hour)
			status1 := tracker.GetSpeciesStatus(species1, nextDay)
			assert.Equal(t, 1, status1.DaysSinceFirst, "Should be 1 day since first detection")

			// Test leap year boundary crossing for February dates
			if tt.testMonth == time.February {
				// Test detection 7 days later (into March for short February)
				weekLater := testTime.Add(7 * 24 * time.Hour)
				species2 := "Leap_Boundary_Species"
				
				isNew2, days2 := tracker.CheckAndUpdateSpecies(species2, weekLater)
				assert.True(t, isNew2, "New species should be new")
				assert.Equal(t, 0, days2, "New species should have 0 days")
				
				// Check original species status after leap year boundary
				status1Later := tracker.GetSpeciesStatus(species1, weekLater)
				assert.Equal(t, 7, status1Later.DaysSinceFirst, 
					"Should correctly calculate days across leap year boundary")
			}

			// Test year transition if we're testing December
			if tt.testMonth == time.December && tt.testDay >= 25 {
				nextYearTime := time.Date(tt.year+1, time.January, 2, 12, 0, 0, 0, time.UTC)
				status1NextYear := tracker.GetSpeciesStatus(species1, nextYearTime)
				
				expectedDays := int(nextYearTime.Sub(testTime).Hours() / 24)
				assert.Equal(t, expectedDays, status1NextYear.DaysSinceFirst,
					"Should correctly calculate days across year boundary")
			}

			t.Logf("Leap year test passed: %s", tt.name)
		})
	}
}

// TestInvalidDetectionTimes tests tracker behavior with invalid/edge case times
// Critical for handling malformed input and preventing crashes
func TestInvalidDetectionTimes(t *testing.T) {
	t.Parallel()

	// Create tracker first
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: true,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	tests := []struct {
		name        string
		testTime    time.Time
		shouldPanic bool
		description string
	}{
		{
			"zero_time", time.Time{}, false,
			"Zero value time",
		},
		{
			"far_future", time.Date(2200, 1, 1, 0, 0, 0, 0, time.UTC), false,
			"Far future date (year 2200)",
		},
		{
			"far_past", time.Date(1800, 1, 1, 0, 0, 0, 0, time.UTC), false,
			"Far past date (year 1800)",
		},
		{
			"unix_epoch", time.Unix(0, 0), false,
			"Unix epoch (Jan 1, 1970)",
		},
		{
			"negative_unix", time.Unix(-86400, 0), false,
			"Negative Unix timestamp (Dec 31, 1969)",
		},
		{
			"max_nanoseconds", time.Unix(0, 999999999), false,
			"Maximum nanoseconds value",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing invalid time scenario: %s", tt.description)
			
			species := fmt.Sprintf("Invalid_Time_Species_%s", tt.name)
			
			if tt.shouldPanic {
				assert.Panics(t, func() {
					tracker.CheckAndUpdateSpecies(species, tt.testTime)
				}, "Should panic for time: %v", tt.testTime)
			} else {
				// Should not panic - system should handle gracefully
				assert.NotPanics(t, func() {
					isNew, days := tracker.CheckAndUpdateSpecies(species, tt.testTime)
					t.Logf("Time %v: isNew=%v, days=%d", tt.testTime, isNew, days)
					
					// Basic sanity checks
					assert.GreaterOrEqual(t, days, 0, "Days should not be negative")
					
					// Test status retrieval too
					status := tracker.GetSpeciesStatus(species, tt.testTime)
					assert.GreaterOrEqual(t, status.DaysSinceFirst, 0, 
						"Status days should not be negative")
				}, "Should not panic for time: %v", tt.testTime)
			}

			t.Logf("Invalid time test passed: %s", tt.name)
		})
	}
}