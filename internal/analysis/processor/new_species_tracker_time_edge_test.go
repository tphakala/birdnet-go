package processor

import (
	"fmt"
	"testing"
	"time"
	_ "time/tzdata" // Embed tzdata for CI compatibility

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// createTimeZoneTestTracker creates a tracker with mock datastore for timezone tests
func createTimeZoneTestTracker(t *testing.T, windowDays int) (*NewSpeciesTracker, *MockSpeciesDatastore) {
	t.Helper()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: windowDays,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	return tracker, ds
}

// runTimeZoneTestCases runs a set of timezone test cases against a tracker
func runTimeZoneTestCases(t *testing.T, tracker *NewSpeciesTracker, testCases []struct {
	name     string
	testTime time.Time
}, speciesPrefix string) {
	t.Helper()
	
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			speciesName := speciesPrefix + tc.name
			status := tracker.GetSpeciesStatus(speciesName, tc.testTime)
			assert.True(t, status.IsNew, "Species should be new")
			assert.Equal(t, 0, status.DaysSinceFirst, "New species should have 0 days since first")
		})
	}
}

func TestTimeZoneEdgeCases(t *testing.T) {
	t.Parallel()

	// Load test timezones
	fromLoc, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	toLoc, err := time.LoadLocation("Europe/London")
	require.NoError(t, err)

	t.Run("dst_transition_spring_forward", func(t *testing.T) {
		t.Parallel()
		
		tracker, ds := createTimeZoneTestTracker(t, 7)
		
		// Test cases using proper time.Date with location - Spring Forward DST transition
		testCases := []struct {
			name     string
			testTime time.Time
		}{
			{
				name:     "dst_spring_forward_ny_before",
				testTime: time.Date(2024, 3, 10, 1, 30, 0, 0, fromLoc), // Before 2 AM DST transition
			},
			{
				name:     "dst_spring_forward_ny_after", 
				testTime: time.Date(2024, 3, 10, 3, 30, 0, 0, fromLoc), // After 2 AM DST transition (2 AM doesn't exist)
			},
		}

		runTimeZoneTestCases(t, tracker, testCases, "DST_Test_Species_")
		ds.AssertExpectations(t)
	})

	t.Run("dst_transition_fall_back", func(t *testing.T) {
		t.Parallel()
		
		tracker, ds := createTimeZoneTestTracker(t, 7)

		// Test cases using proper time.Date with location - Fall Back DST transition
		testCases := []struct {
			name     string
			testTime time.Time
		}{
			{
				name:     "dst_fall_back_ny_first",
				testTime: time.Date(2024, 11, 3, 1, 30, 0, 0, fromLoc), // First occurrence of 1:30 AM
			},
			{
				name:     "dst_fall_back_ny_second",
				testTime: time.Date(2024, 11, 3, 1, 30, 0, 0, fromLoc), // Second occurrence would be ambiguous
			},
		}

		runTimeZoneTestCases(t, tracker, testCases, "DST_Fall_Test_Species_")
		ds.AssertExpectations(t)
	})

	t.Run("timezone_boundary_crossing", func(t *testing.T) {
		t.Parallel()
		
		tracker, ds := createTimeZoneTestTracker(t, 1) // Short window to test boundary crossing

		// Test cases crossing timezone boundaries
		testCases := []struct {
			name     string
			testTime time.Time
		}{
			{
				name:     "london_midnight",
				testTime: time.Date(2024, 6, 15, 0, 0, 0, 0, toLoc), // London midnight
			},
			{
				name:     "ny_evening_same_utc_day",
				testTime: time.Date(2024, 6, 14, 19, 0, 0, 0, fromLoc), // NY evening, same UTC day
			},
		}

		runTimeZoneTestCases(t, tracker, testCases, "Timezone_Boundary_")
		ds.AssertExpectations(t)
	})
}

func TestLeapYearEdgeCases(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 30,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	years := []int{2020, 2021, 2022, 2023, 2024} // Mix of leap and non-leap years

	for _, year := range years {
		t.Run(fmt.Sprintf("year_%d", year), func(t *testing.T) {
			// Check if February 29 is valid for this year
			feb29 := time.Date(year, 2, 29, 12, 0, 0, 0, time.UTC)
			if feb29.Month() != 2 || feb29.Day() != 29 {
				// This is not a leap year, Feb 29 doesn't exist
				t.Skipf("Skipping Feb 29 test for non-leap year %d", year)
				return
			}

			// Test Feb 29 in leap year
			speciesName := fmt.Sprintf("LeapYear_Species_%d", year)
			status := tracker.GetSpeciesStatus(speciesName, feb29)
			assert.True(t, status.IsNew, "Species should be new on Feb 29 of leap year")
			assert.Equal(t, 0, status.DaysSinceFirst, "New species should have 0 days since first")
		})
	}

	ds.AssertExpectations(t)
}