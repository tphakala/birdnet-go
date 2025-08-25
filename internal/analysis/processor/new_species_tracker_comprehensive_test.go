package processor

import (
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestWinterSeasonAdjustmentBug tests the critical winter season adjustment logic
// This is likely the source of the bug where all species are marked as new
func TestWinterSeasonAdjustmentBug(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	
	// Simulate species that were detected throughout the previous year
	historicalData := []datastore.NewSpeciesData{
		{ScientificName: "Parus major", FirstSeenDate: "2023-01-15"},
		{ScientificName: "Turdus merula", FirstSeenDate: "2023-06-15"},
		{ScientificName: "Corvus corvax", FirstSeenDate: "2023-12-25"},
	}
	
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(historicalData, nil)

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
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Test winter season detection in January (critical bug area)
	t.Run("winter season in January should use previous year", func(t *testing.T) {
		// January 15, 2024 - should be in winter season that started Dec 21, 2023
		januaryTime := time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC)
		
		tracker.mu.Lock()
		season := tracker.getCurrentSeason(januaryTime)
		shouldAdjust := tracker.shouldAdjustWinter(januaryTime, time.December)
		tracker.mu.Unlock()
		
		assert.Equal(t, "winter", season, "January should be in winter season")
		assert.True(t, shouldAdjust, "Should adjust winter dates in January")
		
		// Check species status - they should NOT all be new
		status := tracker.GetSpeciesStatus("Parus major", januaryTime)
		assert.False(t, status.IsNew, "Species from last year should not be new - this is the bug!")
	})

	// Test all months to verify winter adjustment behavior
	t.Run("winter adjustment should only apply January-May", func(t *testing.T) {
		testCases := []struct {
			month        time.Month
			shouldAdjust bool
			description  string
		}{
			{time.January, true, "January should adjust"},
			{time.February, true, "February should adjust"},
			{time.March, true, "March should adjust"},
			{time.April, true, "April should adjust"},
			{time.May, true, "May should adjust"},
			{time.June, false, "June should NOT adjust"},
			{time.July, false, "July should NOT adjust"},
			{time.August, false, "August should NOT adjust"},
			{time.September, false, "September should NOT adjust"},
			{time.October, false, "October should NOT adjust"},
			{time.November, false, "November should NOT adjust"},
			{time.December, false, "December should NOT adjust"},
		}

		for _, tc := range testCases {
			t.Run(tc.description, func(t *testing.T) {
				testTime := time.Date(2024, tc.month, 15, 10, 0, 0, 0, time.UTC)
				tracker.mu.Lock()
				result := tracker.shouldAdjustWinter(testTime, time.December)
				tracker.mu.Unlock()
				assert.Equal(t, tc.shouldAdjust, result, tc.description)
			})
		}
	})

	// Test season transitions around winter
	t.Run("season transitions around winter boundary", func(t *testing.T) {
		testDates := []struct {
			date           time.Time
			expectedSeason string
			description    string
		}{
			{time.Date(2023, 12, 20, 23, 59, 59, 0, time.UTC), "fall", "Dec 20 - last day of fall"},
			{time.Date(2023, 12, 21, 0, 0, 0, 0, time.UTC), "winter", "Dec 21 - first day of winter"},
			{time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), "winter", "Jan 1 - continuing winter"},
			{time.Date(2024, 3, 19, 23, 59, 59, 0, time.UTC), "winter", "Mar 19 - last day of winter"},
			{time.Date(2024, 3, 20, 0, 0, 0, 0, time.UTC), "spring", "Mar 20 - first day of spring"},
		}

		for _, tc := range testDates {
			t.Run(tc.description, func(t *testing.T) {
				tracker.mu.Lock()
				// Clear the season cache to ensure fresh calculation
				tracker.cachedSeason = ""
				season := tracker.getCurrentSeason(tc.date)
				tracker.mu.Unlock()
				assert.Equal(t, tc.expectedSeason, season, "%s: expected %s", tc.description, tc.expectedSeason)
			})
		}
	})
}

// TestDatabaseSyncBug tests if database sync is causing the false new species bug
func TestDatabaseSyncBug(t *testing.T) {
	t.Parallel()
	
	// Create a tracker with long-term historical data
	ds := &MockSpeciesDatastore{}
	
	// Species detected over a year ago
	oldData := []datastore.NewSpeciesData{
		{ScientificName: "Parus major", FirstSeenDate: "2022-06-15"},
		{ScientificName: "Turdus merula", FirstSeenDate: "2022-07-20"},
	}
	
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(oldData, nil).Once()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(oldData, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  1, // Short sync interval for testing
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
			WindowDays: 30,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Species should not be new (detected 2+ years ago)
	currentTime := time.Now()
	status := tracker.GetSpeciesStatus("Parus major", currentTime)
	assert.False(t, status.IsNew, "Old species should not be new before sync")

	// Simulate a database sync that might reset data
	// Mock returns empty data on second call (simulating data loss)
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Once()
	
	// Force a sync after the interval
	time.Sleep(time.Second * 2)
	err = tracker.SyncIfNeeded()
	require.NoError(t, err)

	// Check that sync did NOT cause species to become "new" (bug is fixed)
	status = tracker.GetSpeciesStatus("Parus major", currentTime)
	assert.False(t, status.IsNew, "Old species should still not be new after sync - data preserved!")
}

// TestYearResetLogic tests the year reset behavior comprehensively
func TestYearResetLogic(t *testing.T) {
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
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2023)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Add species in 2023
	dec2023 := time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC)
	tracker.UpdateSpecies("Parus major", dec2023)

	// Verify it's tracked in 2023
	status := tracker.GetSpeciesStatus("Parus major", dec2023)
	assert.True(t, status.IsNewThisYear, "Should be new in 2023")

	// Check status just after midnight on Jan 1, 2024
	jan2024 := time.Date(2024, 1, 1, 0, 0, 1, 0, time.UTC)
	
	// This should trigger year reset
	status = tracker.GetSpeciesStatus("Parus major", jan2024)
	
	// After year reset, the species hasn't been seen in 2024 yet
	assert.True(t, status.IsNewThisYear, "Should be new this year after reset (not seen in 2024)")
	assert.Nil(t, status.FirstThisYear, "FirstThisYear should be nil (not detected in 2024)")
	
	// Now detect it in 2024
	tracker.UpdateSpecies("Parus major", jan2024)
	status = tracker.GetSpeciesStatus("Parus major", jan2024)
	
	assert.True(t, status.IsNewThisYear, "Should still be new (just detected)")
	assert.NotNil(t, status.FirstThisYear, "FirstThisYear should be set after detection")
}

// TestCheckAndUpdateSpecies tests the atomic check and update operation
func TestCheckAndUpdateSpecies(t *testing.T) {
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

	currentTime := time.Now()

	// First detection
	isNew, daysSince := tracker.CheckAndUpdateSpecies("Parus major", currentTime)
	assert.True(t, isNew, "First detection should be new")
	assert.Equal(t, 0, daysSince, "Days since should be 0 for new species")

	// Second detection
	isNew, daysSince = tracker.CheckAndUpdateSpecies("Parus major", currentTime.Add(time.Hour))
	assert.True(t, isNew, "Should still be new within window")
	assert.Equal(t, 0, daysSince, "Days since should still be 0 on same day")

	// Detection after window expires
	laterTime := currentTime.Add(20 * 24 * time.Hour)
	isNew, daysSince = tracker.CheckAndUpdateSpecies("Parus major", laterTime)
	assert.False(t, isNew, "Should not be new after window expires")
	assert.Equal(t, 20, daysSince, "Days since should be 20")
}

// TestNotificationSuppressionSystem tests the notification suppression system
func TestNotificationSuppressionSystem(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24, // 24 hour suppression
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()
	species := "Parus major"

	// First notification - should not be suppressed
	shouldSuppress := tracker.ShouldSuppressNotification(species, currentTime)
	assert.False(t, shouldSuppress, "First notification should not be suppressed")

	// Record notification sent
	tracker.RecordNotificationSent(species, currentTime)

	// Check immediately after - should be suppressed
	shouldSuppress = tracker.ShouldSuppressNotification(species, currentTime.Add(time.Minute))
	assert.True(t, shouldSuppress, "Should suppress within window")

	// Check after suppression window - should not be suppressed
	shouldSuppress = tracker.ShouldSuppressNotification(species, currentTime.Add(25*time.Hour))
	assert.False(t, shouldSuppress, "Should not suppress after window expires")
}

// TestNotificationSuppressionWhenDisabled tests when suppression is disabled
func TestNotificationSuppressionWhenDisabled(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 0, // Disabled
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()
	species := "Parus major"

	// Record notification
	tracker.RecordNotificationSent(species, currentTime)

	// Should never suppress when disabled
	shouldSuppress := tracker.ShouldSuppressNotification(species, currentTime.Add(time.Second))
	assert.False(t, shouldSuppress, "Should never suppress when disabled")
}

// TestNotificationRecordCleanup tests cleanup of old notification records
func TestNotificationRecordCleanup(t *testing.T) {
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

	currentTime := time.Now()

	// Add old and recent notification records
	oldTime := currentTime.Add(-72 * time.Hour) // 3 days ago
	recentTime := currentTime.Add(-12 * time.Hour)

	tracker.RecordNotificationSent("Old Species", oldTime)
	tracker.RecordNotificationSent("Recent Species", recentTime)

	// Cleanup old records
	cleaned := tracker.CleanupOldNotificationRecords(currentTime)
	assert.Equal(t, 1, cleaned, "Should clean 1 old record")

	// Recent should not be suppressed (past window)
	shouldSuppress := tracker.ShouldSuppressNotification("Recent Species", currentTime)
	assert.False(t, shouldSuppress, "Recent species should not be suppressed after window")

	// Old should not be suppressed (cleaned up)
	shouldSuppress = tracker.ShouldSuppressNotification("Old Species", currentTime)
	assert.False(t, shouldSuppress, "Old species should not be suppressed (cleaned)")
}

// TestCacheCleanup tests the cache cleanup mechanism
func TestCacheCleanup(t *testing.T) {
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

	currentTime := time.Now()

	// Add many species to trigger cache growth
	for i := 0; i < 1100; i++ {
		species := fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species, currentTime)
		tracker.GetSpeciesStatus(species, currentTime)
	}

	// Force cache cleanup by getting status with future time
	futureTime := currentTime.Add(10 * time.Minute)
	tracker.GetSpeciesStatus("TriggerCleanup", futureTime)

	// Check that cache was cleaned (should be under max size)
	tracker.mu.Lock()
	cacheSize := len(tracker.statusCache)
	tracker.mu.Unlock()

	assert.LessOrEqual(t, cacheSize, 1000, "Cache should be cleaned to max size")
}

// TestIsSeasonMapInitialized tests the helper method
func TestIsSeasonMapInitialized(t *testing.T) {
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
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)

	// Before any updates, maps may not be initialized
	assert.False(t, tracker.IsSeasonMapInitialized("spring"), "Spring map not initialized")

	// Update a species in spring
	springTime := time.Date(2024, 4, 15, 10, 0, 0, 0, time.UTC)
	tracker.UpdateSpecies("Parus major", springTime)

	// Now spring should be initialized
	assert.True(t, tracker.IsSeasonMapInitialized("spring"), "Spring map should be initialized")
	assert.Equal(t, 1, tracker.GetSeasonMapCount("spring"), "Should have 1 species in spring")
}

// TestClearCacheForTesting tests the test helper method
func TestClearCacheForTesting(t *testing.T) {
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

	currentTime := time.Now()

	// Add species and get status (populates cache)
	tracker.UpdateSpecies("Parus major", currentTime)
	tracker.GetSpeciesStatus("Parus major", currentTime)

	// Clear cache
	tracker.ClearCacheForTesting()

	// Verify cache is empty
	tracker.mu.Lock()
	cacheSize := len(tracker.statusCache)
	tracker.mu.Unlock()

	assert.Equal(t, 0, cacheSize, "Cache should be empty after clear")
}

// TestInitFromDatabaseError tests error handling in initialization
func TestInitFromDatabaseError(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	
	// Mock returns error
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(nil, errors.New("database error"))

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.Error(t, err, "Should return error from database")
	assert.Contains(t, err.Error(), "database error")
}

// TestInitFromDatabaseNilDatastore tests nil datastore handling
func TestInitFromDatabaseNilDatastore(t *testing.T) {
	t.Parallel()
	
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	// Create tracker with nil datastore
	tracker := NewSpeciesTrackerFromSettings(nil, settings)
	err := tracker.InitFromDatabase()
	require.Error(t, err, "Should error with nil datastore")
	assert.Contains(t, err.Error(), "datastore is nil")
}

// TestGetYearDateRange tests year date range calculation
func TestGetYearDateRange(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 3, // March reset
			ResetDay:   15,
			WindowDays: 30,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)

	testCases := []struct {
		now         time.Time
		expectStart string
		expectEnd   string
		description string
	}{
		{
			time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC),
			"2024-03-15",
			"2024-04-01",
			"After reset date in same year",
		},
		{
			time.Date(2024, 2, 1, 0, 0, 0, 0, time.UTC),
			"2023-03-15",
			"2024-02-01",
			"Before reset date uses previous year",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tracker.mu.Lock()
			start, end := tracker.getYearDateRange(tc.now)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expectStart, start, "Start date mismatch")
			assert.Equal(t, tc.expectEnd, end, "End date mismatch")
		})
	}
}

// TestGetSeasonDateRange tests season date range calculation
func TestGetSeasonDateRange(t *testing.T) {
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
		season      string
		now         time.Time
		expectStart string
		expectEnd   string
		description string
	}{
		{
			"summer",
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			"2024-06-21",
			"2024-07-15",
			"Summer season in July",
		},
		{
			"winter",
			time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC),
			"2023-12-21",
			"2024-01-15",
			"Winter season in January (crosses year)",
		},
		{
			"spring",
			time.Date(2024, 3, 15, 0, 0, 0, 0, time.UTC),
			"2024-03-15",
			"2024-03-15",
			"Before spring starts (empty range)",
		},
		{
			"unknown",
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			"2024-07-15",
			"2024-07-15",
			"Unknown season returns empty range",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tracker.mu.Lock()
			start, end := tracker.getSeasonDateRange(tc.season, tc.now)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expectStart, start, "Start date mismatch")
			assert.Equal(t, tc.expectEnd, end, "End date mismatch")
		})
	}
}

// TestIsWithinCurrentYear tests year boundary checks
func TestIsWithinCurrentYear(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
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
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2024)

	testCases := []struct {
		detection time.Time
		expected  bool
		description string
	}{
		{time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC), true, "Current year"},
		{time.Date(2023, 12, 31, 23, 59, 59, 0, time.UTC), false, "Previous year"},
		{time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC), true, "Start of current year"},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tracker.mu.Lock()
			result := tracker.isWithinCurrentYear(tc.detection)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expected, result, tc.description)
		})
	}
}

// TestLoadYearlyDataError tests error handling in yearly data loading
func TestLoadYearlyDataError(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(nil, errors.New("yearly data error"))

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
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.Error(t, err, "Should return error from yearly data loading")
	assert.Contains(t, err.Error(), "yearly data error")
}

// TestLoadSeasonalDataError tests error handling in seasonal data loading
func TestLoadSeasonalDataError(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Once()
	
	// First call succeeds for yearly, second fails for seasonal
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Once() // yearly succeeds
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(nil, errors.New("seasonal data error")) // seasonal fails

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
	require.Error(t, err, "Should return error from seasonal data loading")
	assert.Contains(t, err.Error(), "seasonal data error")
}

// TestTrackerClose tests resource cleanup
func TestTrackerClose(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	
	// Close should not error
	err := tracker.Close()
	assert.NoError(t, err, "Close should not error")
}

// TestConcurrentNotificationOperations tests thread safety of notification system
func TestConcurrentNotificationOperations(t *testing.T) {
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

	var wg sync.WaitGroup
	species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
	currentTime := time.Now()

	// Run concurrent notification operations
	for i := 0; i < 10; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < 100; j++ {
				speciesName := species[j%len(species)]
				switch id % 3 {
				case 0:
					tracker.RecordNotificationSent(speciesName, currentTime)
				case 1:
					_ = tracker.ShouldSuppressNotification(speciesName, currentTime)
				default:
					_ = tracker.CleanupOldNotificationRecords(currentTime)
				}
			}
		}(i)
	}

	wg.Wait()
	// Test passes if no race conditions or panics occurred
}

// TestSeasonCachingLogic tests the season caching mechanism
func TestSeasonCachingLogic(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 21,
		},
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)

	// First call should compute season
	time1 := time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC)
	tracker.mu.Lock()
	season1 := tracker.getCurrentSeason(time1)
	cachedSeason1 := tracker.cachedSeason
	tracker.mu.Unlock()
	assert.Equal(t, "summer", season1)
	assert.Equal(t, "summer", cachedSeason1, "Should cache the season")

	// Second call with same time should use cache
	tracker.mu.Lock()
	season2 := tracker.getCurrentSeason(time1)
	tracker.mu.Unlock()
	assert.Equal(t, season1, season2, "Should return cached value")

	// Call with very different time should recompute
	time2 := time.Date(2024, 12, 25, 10, 0, 0, 0, time.UTC)
	tracker.mu.Lock()
	season3 := tracker.getCurrentSeason(time2)
	tracker.mu.Unlock()
	assert.Equal(t, "winter", season3, "Should compute new season for different date")
}

// TestIsSameSeasonPeriod tests the season period comparison
func TestIsSameSeasonPeriod(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)

	testCases := []struct {
		time1       time.Time
		time2       time.Time
		expected    bool
		description string
	}{
		{
			time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC),
			time.Date(2024, 7, 15, 14, 0, 0, 0, time.UTC),
			true,
			"Same day should be same season period",
		},
		{
			time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC),
			time.Date(2024, 7, 20, 10, 0, 0, 0, time.UTC),
			true,
			"Within buffer days should be same season period",
		},
		{
			time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC),
			time.Date(2025, 7, 15, 10, 0, 0, 0, time.UTC),
			false,
			"Different years should not be same season period",
		},
		{
			time.Date(2024, 7, 15, 10, 0, 0, 0, time.UTC),
			time.Date(2024, 12, 15, 10, 0, 0, 0, time.UTC),
			false,
			"Far apart dates should not be same season period",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.description, func(t *testing.T) {
			tracker.mu.Lock()
			result := tracker.isSameSeasonPeriod(tc.time1, tc.time2)
			tracker.mu.Unlock()
			assert.Equal(t, tc.expected, result, tc.description)
		})
	}
}

// TestNotificationSuppressionWithNegativeValue tests negative suppression hours
func TestNotificationSuppressionWithNegativeValue(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: -1, // Negative value should use default
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	
	// Check that default was applied
	tracker.mu.Lock()
	suppressionWindow := tracker.notificationSuppressionWindow
	tracker.mu.Unlock()
	
	assert.Equal(t, 168*time.Hour, suppressionWindow, "Negative value should use default 168 hours")
}