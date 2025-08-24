package processor

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestUpdateSpeciesComprehensive tests UpdateSpecies method thoroughly
func TestUpdateSpeciesComprehensive(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2024)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	t.Run("first detection of species", func(t *testing.T) {
		now := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC)
		isNew := tracker.UpdateSpecies("Parus major", now)
		assert.True(t, isNew, "First detection should return true")

		// Verify it's tracked in all periods
		tracker.mu.RLock()
		_, hasLifetime := tracker.speciesFirstSeen["Parus major"]
		_, hasYearly := tracker.speciesThisYear["Parus major"]
		_, hasSeasonal := tracker.speciesBySeason["summer"]["Parus major"]
		tracker.mu.RUnlock()

		assert.True(t, hasLifetime, "Should be tracked in lifetime")
		assert.True(t, hasYearly, "Should be tracked in yearly")
		assert.True(t, hasSeasonal, "Should be tracked in seasonal")
	})

	t.Run("update with earlier detection", func(t *testing.T) {
		earlier := time.Date(2024, 7, 10, 10, 0, 0, 0, time.UTC)
		isNew := tracker.UpdateSpecies("Parus major", earlier)
		assert.False(t, isNew, "Already known species should return false")

		// Verify earlier time is recorded
		tracker.mu.RLock()
		firstSeen := tracker.speciesFirstSeen["Parus major"]
		tracker.mu.RUnlock()

		assert.Equal(t, earlier.Day(), firstSeen.Day(), "Should update to earlier date")
	})

	t.Run("detection outside current year", func(t *testing.T) {
		oldTime := time.Date(2023, 7, 15, 10, 0, 0, 0, time.UTC)
		isNew := tracker.UpdateSpecies("Old Species", oldTime)
		assert.True(t, isNew, "New species should return true even if old")

		// Should not be in this year's tracking
		tracker.mu.RLock()
		_, hasYearly := tracker.speciesThisYear["Old Species"]
		tracker.mu.RUnlock()

		assert.False(t, hasYearly, "Should not be in current year tracking")
	})

	t.Run("update in different season", func(t *testing.T) {
		springTime := time.Date(2024, 4, 15, 10, 0, 0, 0, time.UTC)
		isNew := tracker.UpdateSpecies("Multi Season", springTime)
		assert.True(t, isNew, "New species should return true")

		fallTime := time.Date(2024, 10, 15, 10, 0, 0, 0, time.UTC)
		isNew = tracker.UpdateSpecies("Multi Season", fallTime)
		assert.False(t, isNew, "Known species should return false")

		// Should be tracked in both seasons
		tracker.mu.RLock()
		_, hasSpring := tracker.speciesBySeason["spring"]["Multi Season"]
		_, hasFall := tracker.speciesBySeason["fall"]["Multi Season"]
		tracker.mu.RUnlock()

		assert.True(t, hasSpring, "Should be tracked in spring")
		assert.True(t, hasFall, "Should be tracked in fall")
	})
}

// TestBuildSpeciesStatusLocked tests the internal status building method
func TestBuildSpeciesStatusLocked(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
			},
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC)
	
	// Add test data
	tracker.mu.Lock()
	tracker.speciesFirstSeen["Test Species"] = now.Add(-10 * 24 * time.Hour)
	tracker.speciesThisYear["Test Species"] = now.Add(-5 * 24 * time.Hour)
	tracker.speciesBySeason["summer"] = make(map[string]time.Time)
	tracker.speciesBySeason["summer"]["Test Species"] = now.Add(-3 * 24 * time.Hour)
	
	// Build status
	status := tracker.buildSpeciesStatusLocked("Test Species", now, "summer")
	tracker.mu.Unlock()

	assert.NotNil(t, status.FirstThisYear, "FirstThisYear should be set")
	assert.NotNil(t, status.FirstThisSeason, "FirstThisSeason should be set")
	assert.Equal(t, "summer", status.CurrentSeason, "Current season should be summer")
	assert.True(t, status.IsNew, "Should be new (within 14 day window)")
	assert.True(t, status.IsNewThisYear, "Should be new this year")
	assert.True(t, status.IsNewThisSeason, "Should be new this season")
	assert.Equal(t, 10, status.DaysSinceFirst, "Days since first should be 10")
	assert.Equal(t, 5, status.DaysThisYear, "Days this year should be 5")
	assert.Equal(t, 3, status.DaysThisSeason, "Days this season should be 3")
}

// TestShouldResetYear tests year reset logic
func TestShouldResetYear(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 3,  // March
			ResetDay:   15,
			WindowDays: 30,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2023)

	testCases := []struct {
		name        string
		currentTime time.Time
		expected    bool
	}{
		{
			name:        "before reset date in same year",
			currentTime: time.Date(2023, 3, 10, 0, 0, 0, 0, time.UTC),
			expected:    false,
		},
		{
			name:        "after reset date in same year",
			currentTime: time.Date(2023, 3, 20, 0, 0, 0, 0, time.UTC),
			expected:    false,
		},
		{
			name:        "new year before reset date",
			currentTime: time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			expected:    true,
		},
		{
			name:        "new year after reset date",
			currentTime: time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
			expected:    true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			tracker.mu.Lock()
			result := tracker.shouldResetYear(tc.currentTime)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expected, result, tc.name)
		})
	}
}

// TestComputeCurrentSeasonEdgeCases tests season computation edge cases
func TestComputeCurrentSeasonEdgeCases(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons: map[string]conf.Season{
				"spring": {StartMonth: 3, StartDay: 20},
				"summer": {StartMonth: 6, StartDay: 21},
				"fall":   {StartMonth: 9, StartDay: 22},
				"winter": {StartMonth: 12, StartDay: 21},
			},
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)

	testCases := []struct {
		date           time.Time
		expectedSeason string
		description    string
	}{
		// Test exact boundaries
		{time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC), "spring", "Spring equinox"},
		{time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC), "summer", "Summer solstice"},
		{time.Date(2024, 9, 22, 0, 0, 0, 0, time.UTC), "fall", "Fall equinox"},
		{time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC), "winter", "Winter solstice"},
		
		// Test day before boundaries
		{time.Date(2024, 3, 19, 23, 59, 59, 0, time.UTC), "winter", "Day before spring"},
		{time.Date(2024, 6, 20, 23, 59, 59, 0, time.UTC), "spring", "Day before summer"},
		{time.Date(2024, 9, 21, 23, 59, 59, 0, time.UTC), "summer", "Day before fall"},
		{time.Date(2024, 12, 20, 23, 59, 59, 0, time.UTC), "fall", "Day before winter"},
		
		// Test mid-season
		{time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC), "winter", "Mid-January"},
		{time.Date(2024, 4, 15, 12, 0, 0, 0, time.UTC), "spring", "Mid-April"},
		{time.Date(2024, 7, 15, 12, 0, 0, 0, time.UTC), "summer", "Mid-July"},
		{time.Date(2024, 10, 15, 12, 0, 0, 0, time.UTC), "fall", "Mid-October"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tracker.mu.Lock()
			season := tracker.computeCurrentSeason(tc.date)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expectedSeason, season, "%s: expected %s", tc.description, tc.expectedSeason)
		})
	}
}

// TestGetSpeciesStatusCacheHit tests cache hit behavior
func TestGetSpeciesStatusCacheHit(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()
	species := "Cached Species"

	// First call - will compute and cache
	tracker.UpdateSpecies(species, now)
	status1 := tracker.GetSpeciesStatus(species, now)

	// Mark the cache entry to verify it's used
	tracker.mu.Lock()
	if cached, exists := tracker.statusCache[species]; exists {
		// Modify the cached status slightly to detect cache hit
		cached.status.DaysSinceFirst = 999
		tracker.statusCache[species] = cached
	}
	tracker.mu.Unlock()

	// Second call - should return cached value
	status2 := tracker.GetSpeciesStatus(species, now.Add(time.Second))

	assert.Equal(t, 999, status2.DaysSinceFirst, "Should return cached value")
	assert.Equal(t, status1.IsNew, status2.IsNew, "Other fields should match")
}

// TestConcurrentBatchOperations tests concurrent batch operations
func TestConcurrentBatchOperations(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Prepare test data
	species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
	now := time.Now()

	// Add species
	for i, sp := range species {
		tracker.UpdateSpecies(sp, now.Add(-time.Duration(i)*24*time.Hour))
	}

	// Run concurrent batch operations
	var wg sync.WaitGroup
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			statuses := tracker.GetBatchSpeciesStatus(species, now)
			assert.Len(t, statuses, len(species), "Should return all species")
		}()
	}

	wg.Wait()
}

// TestNotificationSuppressionEdgeCases tests notification suppression edge cases
func TestNotificationSuppressionEdgeCases(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	t.Run("exactly at suppression window boundary", func(t *testing.T) {
		settings := &conf.SpeciesTrackingSettings{
			Enabled:                      true,
			NewSpeciesWindowDays:         14,
			SyncIntervalMinutes:          60,
			NotificationSuppressionHours: 24,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		now := time.Now()
		species := "Boundary Species"

		// Record notification
		tracker.RecordNotificationSent(species, now)

		// Check exactly at 24 hours
		exactlyAtWindow := now.Add(24 * time.Hour)
		suppress := tracker.ShouldSuppressNotification(species, exactlyAtWindow)
		assert.False(t, suppress, "Should not suppress at exact window boundary")
	})

	t.Run("millisecond before window expires", func(t *testing.T) {
		settings := &conf.SpeciesTrackingSettings{
			Enabled:                      true,
			NewSpeciesWindowDays:         14,
			SyncIntervalMinutes:          60,
			NotificationSuppressionHours: 1,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		now := time.Now()
		species := "Millisecond Species"

		tracker.RecordNotificationSent(species, now)

		// Check just before window expires
		justBefore := now.Add(time.Hour - time.Millisecond)
		suppress := tracker.ShouldSuppressNotification(species, justBefore)
		assert.True(t, suppress, "Should suppress just before window expires")
	})
}

// TestCleanupOldNotificationRecordsLocked tests internal cleanup method
func TestCleanupOldNotificationRecordsLocked(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()

	// Add notifications
	tracker.mu.Lock()
	tracker.notificationLastSent = make(map[string]time.Time)
	tracker.notificationLastSent["VeryOld"] = now.Add(-100 * time.Hour)
	tracker.notificationLastSent["Old"] = now.Add(-50 * time.Hour)
	tracker.notificationLastSent["Recent"] = now.Add(-10 * time.Hour)

	// Clean up
	cleaned := tracker.cleanupOldNotificationRecordsLocked(now)
	tracker.mu.Unlock()

	assert.Equal(t, 2, cleaned, "Should clean 2 old records")

	// Verify what remains
	tracker.mu.RLock()
	_, hasVeryOld := tracker.notificationLastSent["VeryOld"]
	_, hasOld := tracker.notificationLastSent["Old"]
	_, hasRecent := tracker.notificationLastSent["Recent"]
	tracker.mu.RUnlock()

	assert.False(t, hasVeryOld, "Very old should be removed")
	assert.False(t, hasOld, "Old should be removed")
	assert.True(t, hasRecent, "Recent should remain")
}

// TestEmptySeasonMap tests behavior with empty season configuration
func TestEmptySeasonMap(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
			Seasons:    map[string]conf.Season{}, // Empty seasons
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)

	// Should initialize default seasons
	tracker.mu.RLock()
	seasonCount := len(tracker.seasons)
	tracker.mu.RUnlock()

	assert.Equal(t, 4, seasonCount, "Should have 4 default seasons")
}

// TestInitFromDatabaseWithValidData tests initialization with various data scenarios
func TestInitFromDatabaseWithValidData(t *testing.T) {
	t.Parallel()

	historicalData := []datastore.NewSpeciesData{
		{ScientificName: "Species1", FirstSeenDate: "2024-01-15"},
		{ScientificName: "Species2", FirstSeenDate: "2024-06-20"},
		{ScientificName: "Species3", FirstSeenDate: "2024-07-10"},
	}

	yearlyData := []datastore.NewSpeciesData{
		{ScientificName: "Species1", FirstSeenDate: "2024-01-15"},
		{ScientificName: "Species2", FirstSeenDate: "2024-06-20"},
	}

	seasonalData := []datastore.NewSpeciesData{
		{ScientificName: "Species2", FirstSeenDate: "2024-06-20"},
		{ScientificName: "Species3", FirstSeenDate: "2024-07-10"},
	}

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", 
		mock.AnythingOfType("string"), mock.AnythingOfType("string"), 
		mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(yearlyData, nil).Once()
	ds.On("GetSpeciesFirstDetectionInPeriod", 
		mock.AnythingOfType("string"), mock.AnythingOfType("string"), 
		mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(seasonalData, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Verify data was loaded
	assert.Equal(t, 3, tracker.GetSpeciesCount(), "Should have 3 species in lifetime tracking")

	tracker.mu.RLock()
	yearlyCount := len(tracker.speciesThisYear)
	tracker.mu.RUnlock()

	assert.Equal(t, 2, yearlyCount, "Should have 2 species in yearly tracking")
}