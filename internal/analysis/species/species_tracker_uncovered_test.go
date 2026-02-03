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

// Test constants
const testSeasonSpring = "Spring"

// TestSpeciesTracker_Close tests the Close method
func TestSpeciesTracker_Close(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// Test Close doesn't panic
	assert.NotPanics(t, func() {
		_ = tracker.Close()
	})

	// Test Close is idempotent
	assert.NotPanics(t, func() {
		_ = tracker.Close()
		_ = tracker.Close()
	})
}

// TestSpeciesTracker_SetCurrentYearForTesting tests SetCurrentYearForTesting
func TestSpeciesTracker_SetCurrentYearForTesting(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// Set a specific year for testing
	testYear := 2024
	tracker.SetCurrentYearForTesting(testYear)

	// Verify it affects year calculations
	now := time.Now()
	start, _ := tracker.getYearDateRange(now)
	parsedStart, _ := time.Parse(time.DateOnly, start)
	assert.Equal(t, testYear, parsedStart.Year())

	// Test with different year
	tracker.SetCurrentYearForTesting(2025)
	start, _ = tracker.getYearDateRange(now)
	parsedStart, _ = time.Parse(time.DateOnly, start)
	assert.Equal(t, 2025, parsedStart.Year())

	// Reset to 0 should use current year
	tracker.SetCurrentYearForTesting(0)
	start, _ = tracker.getYearDateRange(now)
	parsedStart, _ = time.Parse(time.DateOnly, start)
	currentYear := time.Now().Year()
	assert.Equal(t, currentYear, parsedStart.Year())
}

// TestSpeciesTracker_shouldResetYear tests shouldResetYear method
func TestSpeciesTracker_shouldResetYear(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		currentYear   int
		testTime      time.Time
		expectedReset bool
	}{
		{
			name:          "same year no reset",
			currentYear:   2024,
			testTime:      time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC), // Same year as currentYear
			expectedReset: false,
		},
		{
			name:          "new year should reset",
			currentYear:   2023,
			testTime:      time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC), // Later year than currentYear
			expectedReset: true,
		},
		{
			name:          "never reset before",
			currentYear:   0,
			testTime:      time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC), // Any year when currentYear is 0
			expectedReset: true,
		},
		{
			name:          "multiple years passed",
			currentYear:   2020,
			testTime:      time.Date(2024, 6, 15, 10, 0, 0, 0, time.UTC), // Much later year
			expectedReset: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ds := mocks.NewMockInterface(t)
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled: true,
				},
			}

			tracker := NewTrackerFromSettings(ds, settings)
			// Set the current tracking year for testing
			tracker.SetCurrentYearForTesting(tt.currentYear)

			shouldReset := tracker.shouldResetYear(tt.testTime)
			assert.Equal(t, tt.expectedReset, shouldReset)
		})
	}
}

// TestSpeciesTracker_loadYearlyDataFromDatabase tests loadYearlyDataFromDatabase
func TestSpeciesTracker_loadYearlyDataFromDatabase(t *testing.T) {
	t.Parallel()

	t.Run("successful load", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)

		// Mock data
		yearlyData := []datastore.NewSpeciesData{
			{
				ScientificName: "Robin",
				CommonName:     "American Robin",
				FirstSeenDate:  time.Now().Add(-10 * 24 * time.Hour).Format(time.DateOnly),
			},
			{
				ScientificName: "Sparrow",
				CommonName:     "House Sparrow",
				FirstSeenDate:  time.Now().Add(-5 * 24 * time.Hour).Format(time.DateOnly),
			},
		}

		ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, 10000, 0).
			Return(yearlyData, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled:    true,
				ResetMonth: 1,
				ResetDay:   1,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		now := time.Now()
		err := tracker.loadYearlyDataFromDatabase(now)
		require.NoError(t, err)

		// Check data was loaded
		tracker.mu.RLock()
		assert.Len(t, tracker.speciesThisYear, 2)
		assert.Contains(t, tracker.speciesThisYear, "Robin")
		assert.Contains(t, tracker.speciesThisYear, "Sparrow")
		tracker.mu.RUnlock()
	})

	t.Run("empty database preserves existing", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, 10000, 0).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)

		// Pre-populate some data
		tracker.speciesThisYear["Existing"] = time.Now().Add(-20 * 24 * time.Hour)

		now := time.Now()
		err := tracker.loadYearlyDataFromDatabase(now)
		require.NoError(t, err)

		// Check existing data preserved
		tracker.mu.RLock()
		assert.Contains(t, tracker.speciesThisYear, "Existing")
		tracker.mu.RUnlock()
	})

	t.Run("database error", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, 10000, 0).
			Return(nil, assert.AnError).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		now := time.Now()
		err := tracker.loadYearlyDataFromDatabase(now)
		assert.Error(t, err)
	})
}

// TestSpeciesTracker_loadSeasonalDataFromDatabase tests loadSeasonalDataFromDatabase
func TestSpeciesTracker_loadSeasonalDataFromDatabase(t *testing.T) {
	t.Parallel()

	t.Run("successful load", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)

		// Mock data
		seasonalData := []datastore.NewSpeciesData{
			{
				ScientificName: "Cardinal",
				CommonName:     "Northern Cardinal",
				FirstSeenDate:  time.Now().Add(-3 * 24 * time.Hour).Format(time.DateOnly),
			},
			{
				ScientificName: "BlueJay",
				CommonName:     "Blue Jay",
				FirstSeenDate:  time.Now().Add(-1 * 24 * time.Hour).Format(time.DateOnly),
			},
		}

		ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, 10000, 0).
			Return(seasonalData, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)

		// Initialize season maps
		tracker.seasons = map[string]seasonDates{
			testSeasonSpring: {month: 3, day: 1},
			"Summer":         {month: 6, day: 1},
			"Autumn":         {month: 9, day: 1},
			"Winter":         {month: 12, day: 1},
		}
		tracker.currentSeason = testSeasonSpring

		now := time.Now()
		err := tracker.loadSeasonalDataFromDatabase(now)
		require.NoError(t, err)

		// Check data was loaded
		tracker.mu.RLock()
		if tracker.speciesBySeason != nil && tracker.speciesBySeason[testSeasonSpring] != nil {
			assert.Len(t, tracker.speciesBySeason[testSeasonSpring], 2)
			assert.Contains(t, tracker.speciesBySeason[testSeasonSpring], "Cardinal")
			assert.Contains(t, tracker.speciesBySeason[testSeasonSpring], "BlueJay")
		}
		tracker.mu.RUnlock()
	})

	t.Run("no season maps", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)

		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.seasons = nil // No season maps

		now := time.Now()
		err := tracker.loadSeasonalDataFromDatabase(now)
		// Should not error but not load anything
		require.NoError(t, err)
		if tracker.speciesBySeason != nil {
			assert.Empty(t, tracker.speciesBySeason)
		}
	})

	t.Run("empty database preserves existing", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, 10000, 0).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled: true,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)

		// Initialize season
		tracker.seasons = map[string]seasonDates{
			testSeasonSpring: {month: 3, day: 1},
		}
		tracker.currentSeason = testSeasonSpring

		// Pre-populate some data
		if tracker.speciesBySeason == nil {
			tracker.speciesBySeason = make(map[string]map[string]time.Time)
		}
		if tracker.speciesBySeason[testSeasonSpring] == nil {
			tracker.speciesBySeason[testSeasonSpring] = make(map[string]time.Time)
		}
		tracker.speciesBySeason[testSeasonSpring]["Existing"] = time.Now().Add(-10 * 24 * time.Hour)

		now := time.Now()
		err := tracker.loadSeasonalDataFromDatabase(now)
		require.NoError(t, err)

		// Check existing data preserved
		tracker.mu.RLock()
		if tracker.speciesBySeason != nil && tracker.speciesBySeason[testSeasonSpring] != nil {
			assert.Contains(t, tracker.speciesBySeason[testSeasonSpring], "Existing")
		}
		tracker.mu.RUnlock()
	})
}

// TestSpeciesTracker_getYearDateRange tests getYearDateRange
func TestSpeciesTracker_getYearDateRange(t *testing.T) {
	t.Parallel()

	t.Run("standard year range", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			YearlyTracking: conf.YearlyTrackingSettings{
				ResetMonth: 1,
				ResetDay:   1,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.SetCurrentYearForTesting(2024)

		now := time.Now()
		start, end := tracker.getYearDateRange(now)
		assert.Equal(t, "2024-01-01", start)
		assert.Equal(t, "2024-12-31", end)
	})

	t.Run("custom reset date", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			YearlyTracking: conf.YearlyTrackingSettings{
				ResetMonth: 7,
				ResetDay:   15,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.SetCurrentYearForTesting(2024)

		// After reset date in year
		// Note: Can't directly set lastYearReset (private field)
		now := time.Now()
		start, end := tracker.getYearDateRange(now)

		// Should be from July 15, 2024 to July 14, 2025
		assert.Equal(t, "2024-07-15", start)
		assert.Equal(t, "2025-07-14", end)
	})

	t.Run("before reset date", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			YearlyTracking: conf.YearlyTrackingSettings{
				ResetMonth: 10,
				ResetDay:   1,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		// Set to February 2024
		testTime := time.Date(2024, 2, 15, 0, 0, 0, 0, time.UTC)

		// Should be from October 1, 2023 to September 30, 2024
		start, end := tracker.getYearDateRange(testTime)
		assert.Equal(t, "2023-10-01", start)
		assert.Equal(t, "2024-09-30", end)
	})
}

// TestSpeciesTracker_getSeasonDateRange tests getSeasonDateRange
func TestSpeciesTracker_getSeasonDateRange(t *testing.T) {
	t.Parallel()

	t.Run("spring season", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.seasons = map[string]seasonDates{
			testSeasonSpring: {month: 3, day: 1},
		}
		tracker.currentSeason = testSeasonSpring
		tracker.SetCurrentYearForTesting(2024)

		now := time.Now()
		start, end := tracker.getSeasonDateRange(testSeasonSpring, now)
		assert.Equal(t, "2024-03-01", start)
		assert.Equal(t, "2024-05-31", end)
	})

	t.Run("winter season crossing year", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.seasons = map[string]seasonDates{
			"Winter": {month: 12, day: 1},
		}
		tracker.currentSeason = "Winter"
		tracker.SetCurrentYearForTesting(2024)

		// In December - winter extends to next year
		now := time.Date(2024, 12, 15, 0, 0, 0, 0, time.UTC)
		start, end := tracker.getSeasonDateRange("Winter", now)
		assert.Equal(t, "2024-12-01", start)
		assert.Equal(t, "2025-02-28", end)
	})

	t.Run("no current season", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: true,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.currentSeason = ""

		now := time.Now()
		start, end := tracker.getSeasonDateRange("", now)
		assert.Empty(t, start)
		assert.Empty(t, end)
	})

	t.Run("invalid season format", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.seasons = map[string]seasonDates{
			"BadSeason": {month: 0, day: 0}, // Invalid
		}
		tracker.currentSeason = "BadSeason"

		now := time.Now()
		start, end := tracker.getSeasonDateRange("BadSeason", now)
		assert.Empty(t, start)
		assert.Empty(t, end)
	})
}

// TestSpeciesTracker_isWithinCurrentYear tests isWithinCurrentYear
func TestSpeciesTracker_isWithinCurrentYear(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	settings := &conf.SpeciesTrackingSettings{
		YearlyTracking: conf.YearlyTrackingSettings{
			ResetMonth: 1,
			ResetDay:   1,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2024)

	tests := []struct {
		name     string
		testTime time.Time
		expected bool
	}{
		{
			name:     "current year",
			testTime: time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC),
			expected: true,
		},
		{
			name:     "start of year",
			testTime: time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: true,
		},
		{
			name:     "end of year",
			testTime: time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			expected: true,
		},
		{
			name:     "previous year",
			testTime: time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC),
			expected: false,
		},
		{
			name:     "next year",
			testTime: time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC),
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tracker.isWithinCurrentYear(tt.testTime)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSpeciesTracker_yearlyResetBoundaries tests yearly reset with custom boundaries
func TestSpeciesTracker_yearlyResetBoundaries(t *testing.T) {
	t.Parallel()

	t.Run("mid-year reset", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		settings := &conf.SpeciesTrackingSettings{
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled:    true,
				ResetMonth: 7,
				ResetDay:   1,
			},
		}

		tracker := NewTrackerFromSettings(ds, settings)

		// Test detection before reset date (June 30, 2024)
		beforeReset := time.Date(2024, 6, 30, 0, 0, 0, 0, time.UTC)
		tracker.SetCurrentYearForTesting(2024)
		isWithin := tracker.isWithinCurrentYear(beforeReset)

		// June 30, 2024 is in tracking year 2023 (July 1, 2023 - June 30, 2024)
		// Since currentYear=2024, this detection is NOT in the current tracking year
		assert.False(t, isWithin)

		// Test detection after reset date (July 2, 2024)
		afterReset := time.Date(2024, 7, 2, 0, 0, 0, 0, time.UTC)
		isWithin = tracker.isWithinCurrentYear(afterReset)
		// July 2, 2024 is in tracking year 2024 (July 1, 2024 - June 30, 2025)
		// Since currentYear=2024, this detection IS in the current tracking year
		assert.True(t, isWithin)
	})
}
