// new_species_tracker_business_logic_reliability_test.go
// Critical reliability tests for core business logic functions
// Targets high-impact functions that drive species status calculations and seasonal logic
package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestBuildSpeciesStatusLocked_CriticalReliability tests the core business logic engine
// CRITICAL: All species status calculations depend on this function - bugs affect entire system
func TestBuildSpeciesStatusLocked_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		speciesName   string
		currentTime   time.Time
		currentSeason string
		lifetimeData  []datastore.NewSpeciesData
		yearlyData    []datastore.NewSpeciesData
		seasonalData  []datastore.NewSpeciesData
		expectedNew   bool
		expectedDays  int
		description   string
	}{
		{
			"new_species_all_periods",
			"Brand_New_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"summer",
			[]datastore.NewSpeciesData{}, // Not in lifetime
			[]datastore.NewSpeciesData{}, // Not in yearly
			[]datastore.NewSpeciesData{}, // Not in seasonal
			true, 0,
			"Completely new species should be marked as new with 0 days in all periods",
		},
		{
			"existing_lifetime_new_year_season",
			"Existing_Lifetime_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"summer",
			[]datastore.NewSpeciesData{
				{ScientificName: "Existing_Lifetime_Species", FirstSeenDate: "2023-03-10"},
			},
			[]datastore.NewSpeciesData{}, // New this year
			[]datastore.NewSpeciesData{}, // New this season
			false, 463,                   // Days since 2023-03-10 to 2024-06-15 (>14 days, so not new)
			"Species with lifetime history but new to current year/season",
		},
		{
			"existing_all_periods_old",
			"Old_Known_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"summer",
			[]datastore.NewSpeciesData{
				{ScientificName: "Old_Known_Species", FirstSeenDate: "2023-03-10"},
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Old_Known_Species", FirstSeenDate: "2024-03-01"},
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Old_Known_Species", FirstSeenDate: "2024-06-01"},
			},
			false, 463, // Days since first lifetime detection
			"Species known in all periods should not be marked as new",
		},
		{
			"seasonal_transition_edge_case",
			"Seasonal_Transition_Species",
			time.Date(2024, 6, 1, 0, 0, 0, 0, time.UTC), // First day of summer
			"summer",
			[]datastore.NewSpeciesData{
				{ScientificName: "Seasonal_Transition_Species", FirstSeenDate: "2024-03-15"},
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Seasonal_Transition_Species", FirstSeenDate: "2024-03-15"},
			},
			[]datastore.NewSpeciesData{}, // New to summer season
			false, 78,                    // Days since spring detection (>14 days, so not new)
			"Species transitioning between seasons should handle correctly",
		},
		{
			"within_new_species_window",
			"Recent_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"summer",
			[]datastore.NewSpeciesData{
				{ScientificName: "Recent_Species", FirstSeenDate: "2024-06-10"}, // 5 days ago
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Recent_Species", FirstSeenDate: "2024-06-10"},
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Recent_Species", FirstSeenDate: "2024-06-10"},
			},
			true, 5, // Within window, should still be "new"
			"Species within new species window should be marked as new",
		},
		{
			"outside_new_species_window",
			"Older_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"summer",
			[]datastore.NewSpeciesData{
				{ScientificName: "Older_Species", FirstSeenDate: "2024-05-01"}, // 45 days ago
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Older_Species", FirstSeenDate: "2024-05-01"},
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Older_Species", FirstSeenDate: "2024-05-01"},
			},
			false, 45, // Outside window, not "new"
			"Species outside new species window should not be marked as new",
		},
		{
			"year_boundary_calculation",
			"Year_Boundary_Species",
			time.Date(2024, 1, 5, 12, 0, 0, 0, time.UTC), // Early January
			"winter",
			[]datastore.NewSpeciesData{
				{ScientificName: "Year_Boundary_Species", FirstSeenDate: "2023-12-28"}, // Previous year
			},
			[]datastore.NewSpeciesData{}, // New this year
			[]datastore.NewSpeciesData{}, // New this season
			true, 8,                      // Days since late December detection
			"Year boundary crossing should calculate days correctly",
		},
		{
			"leap_year_calculation",
			"Leap_Year_Species",
			time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC), // Day after leap day
			"spring",
			[]datastore.NewSpeciesData{
				{ScientificName: "Leap_Year_Species", FirstSeenDate: "2024-02-29"}, // Leap day
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Leap_Year_Species", FirstSeenDate: "2024-02-29"},
			},
			[]datastore.NewSpeciesData{
				{ScientificName: "Leap_Year_Species", FirstSeenDate: "2024-02-29"},
			},
			true, 1, // 1 day since leap day
			"Leap year (Feb 29) calculations should be accurate",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing business logic scenario: %s", tt.description)

			// Create mock datastore
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(tt.lifetimeData, nil).Maybe()
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(tt.yearlyData, nil).Maybe()
			if len(tt.seasonalData) > 0 {
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(tt.seasonalData, nil).Maybe()
			}

			// Create tracker with comprehensive settings
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14, // 14-day new species window
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

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Call the critical buildSpeciesStatusLocked function
			// We'll test it via GetSpeciesStatus which calls buildSpeciesStatusLocked internally
			status := tracker.GetSpeciesStatus(tt.speciesName, tt.currentTime)

			// Verify business logic correctness
			assert.Equal(t, tt.expectedNew, status.IsNew,
				"IsNew status incorrect for scenario: %s", tt.name)
			assert.Equal(t, tt.expectedDays, status.DaysSinceFirst,
				"DaysSinceFirst incorrect for scenario: %s (expected %d, got %d)",
				tt.name, tt.expectedDays, status.DaysSinceFirst)

			// Verify status fields are consistent
			assert.False(t, status.FirstSeenTime.IsZero(),
				"FirstSeenTime should be populated")
			assert.False(t, status.LastUpdatedTime.IsZero(),
				"LastUpdatedTime should be populated")
			assert.GreaterOrEqual(t, status.DaysSinceFirst, 0,
				"DaysSinceFirst should never be negative")

			t.Logf("✓ Business logic correct: IsNew=%v, Days=%d", status.IsNew, status.DaysSinceFirst)

			// Test that the logic is deterministic
			status2 := tracker.GetSpeciesStatus(tt.speciesName, tt.currentTime)
			assert.Equal(t, status.IsNew, status2.IsNew, "Logic should be deterministic")
			assert.Equal(t, status.DaysSinceFirst, status2.DaysSinceFirst, "Logic should be deterministic")

			tracker.ClearCacheForTesting()
		})
	}
}

// TestComputeCurrentSeason_CriticalReliability tests season calculation logic
// CRITICAL: Season calculations affect seasonal tracking accuracy across the entire system
func TestComputeCurrentSeason_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		currentTime    time.Time
		expectedSeason string
		description    string
	}{
		{
			"spring_start_march_21",
			time.Date(2024, 3, 21, 12, 0, 0, 0, time.UTC),
			"spring",
			"Spring equinox should be calculated as Spring",
		},
		{
			"spring_end_june_20",
			time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC),
			"spring",
			"Last day of spring should be Spring",
		},
		{
			"summer_start_june_21",
			time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC),
			"summer",
			"Summer solstice should be calculated as Summer",
		},
		{
			"summer_end_september_20",
			time.Date(2024, 9, 20, 12, 0, 0, 0, time.UTC),
			"summer",
			"Last day of summer should be Summer",
		},
		{
			"autumn_start_september_21",
			time.Date(2024, 9, 21, 12, 0, 0, 0, time.UTC),
			"summer",
			"September 21 should be Summer (Fall starts Sept 22)",
		},
		{
			"autumn_start_september_22",
			time.Date(2024, 9, 22, 12, 0, 0, 0, time.UTC),
			"fall",
			"September 22 should be Fall (actual Autumn equinox)",
		},
		{
			"autumn_end_december_20",
			time.Date(2024, 12, 20, 12, 0, 0, 0, time.UTC),
			"fall",
			"Last day of autumn should be Autumn",
		},
		{
			"winter_start_december_21",
			time.Date(2024, 12, 21, 12, 0, 0, 0, time.UTC),
			"winter",
			"Winter solstice should be calculated as Winter",
		},
		{
			"winter_january",
			time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC),
			"winter",
			"Mid-January should be Winter",
		},
		{
			"winter_february",
			time.Date(2024, 2, 15, 12, 0, 0, 0, time.UTC),
			"winter",
			"Mid-February should be Winter",
		},
		{
			"winter_end_march_19",
			time.Date(2024, 3, 19, 12, 0, 0, 0, time.UTC),
			"winter",
			"March 19 should be Winter (last day before Spring starts)",
		},
		{
			"winter_end_march_20",
			time.Date(2024, 3, 20, 12, 0, 0, 0, time.UTC),
			"spring",
			"March 20 should be Spring (Spring starts March 20)",
		},
		{
			"leap_year_february_29",
			time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			"winter",
			"Leap year February 29 should be Winter",
		},
		{
			"year_boundary_december_31",
			time.Date(2024, 12, 31, 23, 59, 0, 0, time.UTC),
			"winter",
			"New Year's Eve should be Winter",
		},
		{
			"year_boundary_january_1",
			time.Date(2024, 1, 1, 0, 1, 0, 0, time.UTC),
			"winter",
			"New Year's Day should be Winter",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing season calculation: %s", tt.description)

			// Create minimal tracker for season testing
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()
			// BG-17: InitFromDatabase now loads notification history
			ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
				Return([]datastore.NotificationHistory{}, nil).Maybe()
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: true,
				},
			}

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Test computeCurrentSeason function via getCurrentSeason which calls it
			actualSeason := tracker.getCurrentSeason(tt.currentTime)
			assert.Equal(t, tt.expectedSeason, actualSeason,
				"Season calculation incorrect for %v (expected %s, got %s)",
				tt.currentTime.Format(time.DateOnly), tt.expectedSeason, actualSeason)

			t.Logf("✓ Season correctly calculated: %s for %s", actualSeason, tt.currentTime.Format("2006-01-02 15:04"))

			// Test season consistency - same time should always return same season
			actualSeason2 := tracker.getCurrentSeason(tt.currentTime)
			assert.Equal(t, actualSeason, actualSeason2,
				"Season calculation should be deterministic")

			tracker.ClearCacheForTesting()
		})
	}
}

// TestDateRangeFunctions_CriticalReliability tests date range calculation reliability
// CRITICAL: Date ranges drive period queries - incorrect ranges cause tracking failures
func TestDateRangeFunctions_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		testTime    time.Time
		testSeason  string
		description string
	}{
		{
			"mid_year_ranges",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			"summer",
			"Mid-year date ranges should be calculated correctly",
		},
		{
			"year_start_ranges",
			time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			"winter",
			"Year start should calculate ranges correctly",
		},
		{
			"year_end_ranges",
			time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			"winter",
			"Year end should calculate ranges correctly",
		},
		{
			"leap_year_ranges",
			time.Date(2024, 2, 29, 12, 0, 0, 0, time.UTC),
			"winter",
			"Leap year should handle date ranges correctly",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing date range calculation: %s", tt.description)

			// Create tracker for date range testing
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()
			// BG-17: InitFromDatabase now loads notification history
			ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
				Return([]datastore.NotificationHistory{}, nil).Maybe()
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()

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

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Test year date range calculation
			yearStart, yearEnd := tracker.getYearDateRange(tt.testTime)
			assert.NotEmpty(t, yearStart, "Year start date should not be empty")
			assert.NotEmpty(t, yearEnd, "Year end date should not be empty")

			// Verify year range format and logic
			assert.Regexp(t, `^\d{4}-01-01$`, yearStart, "Year start should be January 1st")
			assert.Regexp(t, `^\d{4}-12-31$`, yearEnd, "Year end should be December 31st")

			t.Logf("✓ Year range: %s to %s", yearStart, yearEnd)

			// Test season date range calculation
			seasonStart, seasonEnd := tracker.getSeasonDateRange(tt.testSeason, tt.testTime)
			assert.NotEmpty(t, seasonStart, "Season start date should not be empty")
			assert.NotEmpty(t, seasonEnd, "Season end date should not be empty")

			// Verify season range format
			assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, seasonStart, "Season start should be valid date format")
			assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, seasonEnd, "Season end should be valid date format")

			t.Logf("✓ %s season range: %s to %s", tt.testSeason, seasonStart, seasonEnd)

			// Test date range consistency - same inputs should give same outputs
			yearStart2, yearEnd2 := tracker.getYearDateRange(tt.testTime)
			assert.Equal(t, yearStart, yearStart2, "Year range calculation should be deterministic")
			assert.Equal(t, yearEnd, yearEnd2, "Year range calculation should be deterministic")

			seasonStart2, seasonEnd2 := tracker.getSeasonDateRange(tt.testSeason, tt.testTime)
			assert.Equal(t, seasonStart, seasonStart2, "Season range calculation should be deterministic")
			assert.Equal(t, seasonEnd, seasonEnd2, "Season range calculation should be deterministic")

			tracker.ClearCacheForTesting()
		})
	}
}
