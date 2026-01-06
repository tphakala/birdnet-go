package species

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
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestIsNewSpecies tests the IsNewSpecies method
func TestIsNewSpecies(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	t.Run("unknown species is new", func(t *testing.T) {
		isNew := tracker.IsNewSpecies("Unknown Species")
		assert.True(t, isNew, "Unknown species should be new")
	})

	t.Run("recently added species is new", func(t *testing.T) {
		now := time.Now()
		tracker.UpdateSpecies("Recent Species", now)
		isNew := tracker.IsNewSpecies("Recent Species")
		assert.True(t, isNew, "Recently added species should be new")
	})

	t.Run("old species is not new", func(t *testing.T) {
		oldTime := time.Now().Add(-30 * 24 * time.Hour)
		tracker.UpdateSpecies("Old Species", oldTime)
		isNew := tracker.IsNewSpecies("Old Species")
		assert.False(t, isNew, "Old species should not be new")
	})
}

// TestGetBatchSpeciesStatus tests batch status retrieval
func TestGetBatchSpeciesStatus(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

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

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Add test species
	now := time.Now()
	species := []string{"Species1", "Species2", "Species3"}
	for i, sp := range species {
		detectionTime := now.Add(-time.Duration(i*5) * 24 * time.Hour)
		tracker.UpdateSpecies(sp, detectionTime)
	}

	// Test batch retrieval
	statuses := tracker.GetBatchSpeciesStatus(species, now)

	assert.Len(t, statuses, len(species), "Should return status for all species")
	for _, sp := range species {
		status, exists := statuses[sp]
		assert.True(t, exists, "Status should exist for %s", sp)
		assert.NotEmpty(t, status.CurrentSeason, "Current season should be set")
		assert.GreaterOrEqual(t, status.DaysSinceFirst, 0, "Days since first should be valid")
	}

	// Test empty batch
	emptyStatuses := tracker.GetBatchSpeciesStatus([]string{}, now)
	assert.Empty(t, emptyStatuses, "Empty input should return empty map")
}

// TestSyncIfNeeded tests the sync mechanism
func TestSyncIfNeeded(t *testing.T) {
	t.Parallel()

	t.Run("sync not needed within interval", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		// No expectations set - should not be called

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  60, // 60 minutes
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.lastSyncTime = time.Now() // Just synced

		err := tracker.SyncIfNeeded()
		require.NoError(t, err, "Should not error when sync not needed")

		// Verify no database calls were made
		ds.AssertNotCalled(t, "GetNewSpeciesDetections")
	})

	t.Run("sync needed after interval", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{
				{ScientificName: "Test Species", FirstSeenDate: "2024-01-01"},
			}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  1, // 1 minute for testing
		}

		tracker := NewTrackerFromSettings(ds, settings)
		tracker.lastSyncTime = time.Now().Add(-2 * time.Minute) // 2 minutes ago

		err := tracker.SyncIfNeeded()
		require.NoError(t, err, "Should sync successfully")

		// Verify database was called
		ds.AssertCalled(t, "GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything)
	})

	t.Run("sync handles database error with existing data", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(nil, errors.New("database error"))

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  1,
		}

		tracker := NewTrackerFromSettings(ds, settings)
		// Add existing data
		tracker.speciesFirstSeen["Existing"] = time.Now()
		tracker.lastSyncTime = time.Now().Add(-2 * time.Minute)

		err := tracker.SyncIfNeeded()
		// Should not error if we have existing data
		require.NoError(t, err, "Should continue with existing data on sync error")

		// Verify existing data is preserved
		assert.Contains(t, tracker.speciesFirstSeen, "Existing", "Existing data should be preserved")
	})
}

// TestCleanupExpiredCache tests cache cleanup
func TestCleanupExpiredCache(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()

	// Add expired and valid cache entries
	tracker.mu.Lock()
	tracker.statusCache["expired1"] = cachedSpeciesStatus{
		status:    SpeciesStatus{},
		timestamp: currentTime.Add(-2 * tracker.cacheTTL),
	}
	tracker.statusCache["valid1"] = cachedSpeciesStatus{
		status:    SpeciesStatus{},
		timestamp: currentTime.Add(-tracker.cacheTTL / 2),
	}
	tracker.mu.Unlock()

	// Clean up cache
	// IMPORTANT: Use force=true to bypass recent cleanup check in tests
	tracker.mu.Lock()
	tracker.cleanupExpiredCacheWithForce(currentTime, true)
	tracker.mu.Unlock()

	// Check results
	tracker.mu.RLock()
	_, hasExpired := tracker.statusCache["expired1"]
	_, hasValid := tracker.statusCache["valid1"]
	tracker.mu.RUnlock()

	assert.False(t, hasExpired, "Expired entry should be removed")
	assert.True(t, hasValid, "Valid entry should remain")
}

// TestCleanupExpiredCacheLRU tests LRU eviction when cache is over size limit
func TestCleanupExpiredCacheLRU(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()

	// Add more than maxStatusCacheSize (1000) entries
	tracker.mu.Lock()
	for i := range 1100 {
		species := fmt.Sprintf("Species%d", i)
		tracker.statusCache[species] = cachedSpeciesStatus{
			status:    SpeciesStatus{},
			timestamp: currentTime.Add(-time.Duration(i) * time.Second), // Older entries have earlier timestamps
		}
	}
	tracker.mu.Unlock()

	// Clean up cache - test LRU eviction
	// IMPORTANT: Use force=true to bypass recent cleanup check in tests
	tracker.mu.Lock()
	tracker.cleanupExpiredCacheWithForce(currentTime, true)
	tracker.mu.Unlock()

	// Check that cache was reduced to max size
	tracker.mu.RLock()
	cacheSize := len(tracker.statusCache)
	tracker.mu.RUnlock()

	assert.LessOrEqual(t, cacheSize, maxStatusCacheSize, "Cache should be reduced to max size")

	// Verify newer entries remain (lower indices)
	tracker.mu.RLock()
	_, hasNewer := tracker.statusCache["Species10"]
	_, hasOlder := tracker.statusCache["Species1090"]
	tracker.mu.RUnlock()

	assert.True(t, hasNewer, "Newer entries should remain")
	assert.False(t, hasOlder, "Older entries should be removed")
}

// TestCheckAndUpdateSpeciesAtomic tests atomic check and update
func TestCheckAndUpdateSpeciesAtomic(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

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

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()

	t.Run("new species", func(t *testing.T) {
		isNew, daysSince := tracker.CheckAndUpdateSpecies("New Species", now)
		assert.True(t, isNew, "Should be new")
		assert.Equal(t, 0, daysSince, "Should be 0 days")
	})

	t.Run("existing species recent", func(t *testing.T) {
		// Add again after 5 days
		laterTime := now.Add(5 * 24 * time.Hour)
		isNew, daysSince := tracker.CheckAndUpdateSpecies("New Species", laterTime)
		assert.True(t, isNew, "Should still be new within window")
		assert.Equal(t, 5, daysSince, "Should be 5 days")
	})

	t.Run("existing species old", func(t *testing.T) {
		// Add old species
		oldTime := now.Add(-30 * 24 * time.Hour)
		tracker.CheckAndUpdateSpecies("Old Species", oldTime)

		// Check it now
		isNew, daysSince := tracker.CheckAndUpdateSpecies("Old Species", now)
		assert.False(t, isNew, "Should not be new")
		assert.Equal(t, 30, daysSince, "Should be 30 days")
	})

	t.Run("earlier detection updates record", func(t *testing.T) {
		// First detection
		tracker.CheckAndUpdateSpecies("Test Species", now)

		// Earlier detection
		earlierTime := now.Add(-10 * 24 * time.Hour)
		isNew, daysSince := tracker.CheckAndUpdateSpecies("Test Species", earlierTime)
		assert.True(t, isNew, "Should be new (within window)")
		assert.Equal(t, 0, daysSince, "Should be 0 (just detected)")

		// Verify it updated the record
		tracker.mu.RLock()
		firstSeen := tracker.speciesFirstSeen["Test Species"]
		tracker.mu.RUnlock()
		assert.Equal(t, earlierTime.Unix(), firstSeen.Unix(), "Should update to earlier time")
	})
}

// TestIsSeasonMapInitialized tests season map initialization check
func TestIsSeasonMapInitializedAndCount(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

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
			},
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	t.Run("uninitialized season", func(t *testing.T) {
		initialized := tracker.IsSeasonMapInitialized("spring")
		assert.False(t, initialized, "Spring should not be initialized")

		count := tracker.GetSeasonMapCount("spring")
		assert.Equal(t, 0, count, "Count should be 0 for uninitialized season")
	})

	t.Run("initialized season", func(t *testing.T) {
		// Add species to spring
		springTime := time.Date(2024, 4, 15, 10, 0, 0, 0, time.UTC)
		tracker.UpdateSpecies("Robin", springTime)
		tracker.UpdateSpecies("Sparrow", springTime)

		initialized := tracker.IsSeasonMapInitialized("spring")
		assert.True(t, initialized, "Spring should be initialized")

		count := tracker.GetSeasonMapCount("spring")
		assert.Equal(t, 2, count, "Count should be 2 for spring")
	})

	t.Run("seasonal tracking disabled", func(t *testing.T) {
		// Create tracker with seasonal tracking disabled
		settings2 := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  60,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled: false,
			},
		}
		tracker2 := NewTrackerFromSettings(ds, settings2)

		initialized := tracker2.IsSeasonMapInitialized("spring")
		assert.False(t, initialized, "Should be false when seasonal tracking disabled")

		count := tracker2.GetSeasonMapCount("spring")
		assert.Equal(t, 0, count, "Count should be 0 when seasonal tracking disabled")
	})
}

// TestExpireCacheForTesting tests the test helper
func TestExpireCacheForTesting(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Add species and get status to populate cache
	now := time.Now()
	tracker.UpdateSpecies("Test Species", now)
	_ = tracker.GetSpeciesStatus("Test Species", now)

	// Expire the cache entry
	tracker.ExpireCacheForTesting("Test Species")

	// Check that it's expired
	tracker.mu.RLock()
	cached, exists := tracker.statusCache["Test Species"]
	tracker.mu.RUnlock()

	assert.True(t, exists, "Cache entry should still exist")
	assert.Greater(t, now.Sub(cached.timestamp), tracker.cacheTTL, "Cache should be expired")

	// Test expiring non-existent entry (should not panic)
	tracker.ExpireCacheForTesting("Non-existent Species")
}

// TestClearCacheForTesting tests cache clearing
func TestClearCacheForTestingMethod(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Add multiple species to cache
	now := time.Now()
	for i := range 5 {
		species := fmt.Sprintf("Species%d", i)
		tracker.UpdateSpecies(species, now)
		_ = tracker.GetSpeciesStatus(species, now)
	}

	// Verify cache has entries
	tracker.mu.RLock()
	cacheSize := len(tracker.statusCache)
	tracker.mu.RUnlock()
	assert.Positive(t, cacheSize, "Cache should have entries")

	// Clear cache
	tracker.ClearCacheForTesting()

	// Verify cache is empty
	tracker.mu.RLock()
	newCacheSize := len(tracker.statusCache)
	tracker.mu.RUnlock()
	assert.Equal(t, 0, newCacheSize, "Cache should be empty after clear")
}

// TestShouldSuppressNotification tests notification suppression
func TestShouldSuppressNotificationMethod(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()
	species := "Test Species"

	t.Run("no previous notification", func(t *testing.T) {
		suppress := tracker.ShouldSuppressNotification(species, now)
		assert.False(t, suppress, "Should not suppress first notification")
	})

	t.Run("recent notification", func(t *testing.T) {
		// Record notification
		tracker.RecordNotificationSent(species, now)

		// Check 1 hour later
		suppress := tracker.ShouldSuppressNotification(species, now.Add(1*time.Hour))
		assert.True(t, suppress, "Should suppress within window")
	})

	t.Run("old notification", func(t *testing.T) {
		// Check 25 hours later
		suppress := tracker.ShouldSuppressNotification(species, now.Add(25*time.Hour))
		assert.False(t, suppress, "Should not suppress after window")
	})

	t.Run("suppression disabled", func(t *testing.T) {
		// Create tracker with suppression disabled
		settings2 := &conf.SpeciesTrackingSettings{
			Enabled:                      true,
			NewSpeciesWindowDays:         14,
			SyncIntervalMinutes:          60,
			NotificationSuppressionHours: 0, // Disabled
		}
		tracker2 := NewTrackerFromSettings(ds, settings2)
		tracker2.RecordNotificationSent("Species", now)

		suppress := tracker2.ShouldSuppressNotification("Species", now.Add(1*time.Minute))
		assert.False(t, suppress, "Should never suppress when disabled")
	})
}

// TestRecordNotificationSent tests recording notifications
func TestRecordNotificationSentMethod(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()

	t.Run("record notification", func(t *testing.T) {
		tracker.RecordNotificationSent("Species1", now)

		// Verify it was recorded
		tracker.mu.RLock()
		sentTime, exists := tracker.notificationLastSent["Species1"]
		tracker.mu.RUnlock()

		assert.True(t, exists, "Notification should be recorded")
		assert.Equal(t, now.Unix(), sentTime.Unix(), "Time should match")
	})

	t.Run("record with suppression disabled", func(t *testing.T) {
		// Create tracker with suppression disabled
		settings2 := &conf.SpeciesTrackingSettings{
			Enabled:                      true,
			NewSpeciesWindowDays:         14,
			SyncIntervalMinutes:          60,
			NotificationSuppressionHours: 0,
		}
		tracker2 := NewTrackerFromSettings(ds, settings2)

		// Should not panic or error
		tracker2.RecordNotificationSent("Species", now)

		// Map should not be initialized when suppression is disabled
		tracker2.mu.RLock()
		mapSize := len(tracker2.notificationLastSent)
		tracker2.mu.RUnlock()

		assert.Equal(t, 0, mapSize, "Map should not be populated when suppression disabled")
	})
}

// TestCleanupOldNotificationRecords tests notification cleanup
func TestCleanupOldNotificationRecordsMethod(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()

	// Add old and recent notifications
	tracker.RecordNotificationSent("OldSpecies", now.Add(-72*time.Hour))
	tracker.RecordNotificationSent("RecentSpecies", now.Add(-12*time.Hour))

	// Cleanup
	cleaned := tracker.CleanupOldNotificationRecords(now)
	assert.Equal(t, 1, cleaned, "Should clean 1 old record")

	// Verify old was removed
	tracker.mu.RLock()
	_, hasOld := tracker.notificationLastSent["OldSpecies"]
	_, hasRecent := tracker.notificationLastSent["RecentSpecies"]
	tracker.mu.RUnlock()

	assert.False(t, hasOld, "Old record should be removed")
	assert.True(t, hasRecent, "Recent record should remain")

	t.Run("cleanup with suppression disabled", func(t *testing.T) {
		settings2 := &conf.SpeciesTrackingSettings{
			Enabled:                      true,
			NewSpeciesWindowDays:         14,
			SyncIntervalMinutes:          60,
			NotificationSuppressionHours: 0,
		}
		tracker2 := NewTrackerFromSettings(ds, settings2)

		cleaned := tracker2.CleanupOldNotificationRecords(now)
		assert.Equal(t, 0, cleaned, "Should not clean when suppression disabled")
	})
}

// TestClose tests the Close method
func TestCloseMethod(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// Close should not error
	err := tracker.Close()
	require.NoError(t, err, "Close should not error")

	// Calling close multiple times should be safe
	err = tracker.Close()
	assert.NoError(t, err, "Multiple close calls should not error")
}

// TestCheckAndResetPeriods tests period reset logic
func TestCheckAndResetPeriods(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

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

	tracker := NewTrackerFromSettings(ds, settings)
	tracker.SetCurrentYearForTesting(2023)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Add data for 2023
	tracker.mu.Lock()
	tracker.speciesThisYear["TestSpecies"] = time.Date(2023, 6, 15, 0, 0, 0, 0, time.UTC)
	tracker.mu.Unlock()

	// Check in 2024 - should trigger reset
	time2024 := time.Date(2024, 1, 15, 0, 0, 0, 0, time.UTC)
	tracker.mu.Lock()
	tracker.checkAndResetPeriods(time2024)
	tracker.mu.Unlock()

	// Verify year was reset
	tracker.mu.RLock()
	yearMapSize := len(tracker.speciesThisYear)
	currentYear := tracker.currentYear
	tracker.mu.RUnlock()

	assert.Equal(t, 0, yearMapSize, "Year map should be reset")
	assert.Equal(t, 2024, currentYear, "Current year should be updated")
}

// TestIsSameSeasonPeriod tests season period comparison
func TestIsSameSeasonPeriodComprehensive(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)

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
			"Same day different hours",
		},
		{
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 7, 16, 0, 0, 0, 0, time.UTC),
			true,
			"Adjacent days",
		},
		{
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 7, 21, 0, 0, 0, 0, time.UTC),
			true,
			"Within 7 day buffer",
		},
		{
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 7, 23, 0, 0, 0, 0, time.UTC),
			false,
			"Beyond 7 day buffer",
		},
		{
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2025, 7, 15, 0, 0, 0, 0, time.UTC),
			false,
			"Different years",
		},
		{
			time.Date(2024, 7, 15, 0, 0, 0, 0, time.UTC),
			time.Date(2024, 7, 8, 0, 0, 0, 0, time.UTC),
			true,
			"Within 7 days backwards",
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

// TestPruneOldEntriesComprehensive tests comprehensive pruning
func TestPruneOldEntriesComprehensive(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// BG-17: Notification persistence - async operations
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24,
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

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	now := time.Now()
	// Calculate year start (Jan 1 of current year) for proper yearly entry placement
	yearStart := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, now.Location())

	// Add old and recent entries for all tracking types
	tracker.mu.Lock()
	// Lifetime - only entries older than 10 years are pruned
	tracker.speciesFirstSeen["VeryOldLifetime"] = now.AddDate(-11, 0, 0) // 11 years ago - WILL be pruned
	tracker.speciesFirstSeen["OldLifetime"] = now.AddDate(0, 0, -30)     // 30 days ago - will NOT be pruned
	tracker.speciesFirstSeen["RecentLifetime"] = now.AddDate(0, 0, -10)  // 10 days ago - will NOT be pruned

	// Yearly - only entries from before the current tracking year are pruned
	// Use dates relative to yearStart to ensure entries are correctly within/outside the year
	tracker.speciesThisYear["LastYearEntry"] = yearStart.AddDate(0, 0, -1) // Day before year start - WILL be pruned
	tracker.speciesThisYear["OldYearly"] = yearStart.AddDate(0, 0, 1)      // Day after year start - will NOT be pruned
	tracker.speciesThisYear["RecentYearly"] = now.AddDate(0, 0, -1)        // Yesterday - will NOT be pruned (guaranteed in current year)

	// Seasonal - entire seasons older than 1 year are pruned
	tracker.speciesBySeason["old_spring"] = make(map[string]time.Time)
	tracker.speciesBySeason["old_spring"]["VeryOldSeasonal"] = now.AddDate(-2, 0, 0) // 2 years ago - WILL be pruned (entire season)
	tracker.speciesBySeason["current_spring"] = make(map[string]time.Time)
	tracker.speciesBySeason["current_spring"]["OldSeasonal"] = now.AddDate(0, 0, -50)    // 50 days ago - will NOT be pruned
	tracker.speciesBySeason["current_spring"]["RecentSeasonal"] = now.AddDate(0, 0, -15) // 15 days ago - will NOT be pruned

	// Notifications - use hours for sub-day precision
	tracker.notificationLastSent = make(map[string]time.Time)
	tracker.notificationLastSent["OldNotification"] = now.Add(-72 * time.Hour)    // 3 days ago
	tracker.notificationLastSent["RecentNotification"] = now.Add(-12 * time.Hour) // 12 hours ago
	tracker.mu.Unlock()

	// Prune
	pruned := tracker.PruneOldEntries()
	assert.Positive(t, pruned, "Should prune some entries")

	// Check what remains
	tracker.mu.RLock()
	_, hasVeryOldLifetime := tracker.speciesFirstSeen["VeryOldLifetime"]
	_, hasOldLifetime := tracker.speciesFirstSeen["OldLifetime"]
	_, hasRecentLifetime := tracker.speciesFirstSeen["RecentLifetime"]
	_, hasLastYearEntry := tracker.speciesThisYear["LastYearEntry"]
	_, hasOldYearly := tracker.speciesThisYear["OldYearly"]
	_, hasRecentYearly := tracker.speciesThisYear["RecentYearly"]
	_, hasOldSpring := tracker.speciesBySeason["old_spring"]
	_, hasCurrentSpring := tracker.speciesBySeason["current_spring"]
	_, hasOldSeasonal := tracker.speciesBySeason["current_spring"]["OldSeasonal"]
	_, hasRecentSeasonal := tracker.speciesBySeason["current_spring"]["RecentSeasonal"]
	_, hasOldNotification := tracker.notificationLastSent["OldNotification"]
	_, hasRecentNotification := tracker.notificationLastSent["RecentNotification"]
	tracker.mu.RUnlock()

	// Very old entries should be pruned
	assert.False(t, hasVeryOldLifetime, "11-year-old lifetime entry should be pruned")
	assert.False(t, hasLastYearEntry, "Last year's yearly entry should be pruned")
	assert.False(t, hasOldSpring, "Old spring season (2 years ago) should be pruned entirely")
	assert.False(t, hasOldNotification, "Old notification should be pruned")

	// These should remain (not old enough to prune)
	assert.True(t, hasOldLifetime, "30-day-old lifetime entry should remain")
	assert.True(t, hasRecentLifetime, "Recent lifetime should remain")
	assert.True(t, hasOldYearly, "Yearly entry after year start should remain")
	assert.True(t, hasRecentYearly, "Recent yearly should remain")
	assert.True(t, hasCurrentSpring, "Current spring season should remain")
	assert.True(t, hasOldSeasonal, "50-day-old seasonal entry should remain")
	assert.True(t, hasRecentSeasonal, "Recent seasonal should remain")
	assert.True(t, hasRecentNotification, "Recent notification should remain")
}

// TestConcurrentOperationsStress tests heavy concurrent usage
func TestConcurrentOperationsStress(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping stress test in short mode")
	}

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: RecordNotificationSent saves to database (called in case 7)
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).
		Return(nil).Maybe()
	// BG-17: CleanupOldNotificationRecords deletes from database (called in PruneOldEntries)
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).
		Return(int64(0), nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         14,
		SyncIntervalMinutes:          60,
		NotificationSuppressionHours: 24,
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

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	var wg sync.WaitGroup
	numGoroutines := 20
	numOperations := 100
	species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
	now := time.Now()

	// Stress test with many concurrent operations
	for i := range numGoroutines {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := range numOperations {
				sp := species[j%len(species)]
				op := (id + j) % 10

				switch op {
				case 0, 1:
					_ = tracker.UpdateSpecies(sp, now.Add(time.Duration(j)*time.Hour))
				case 2, 3:
					_ = tracker.GetSpeciesStatus(sp, now)
				case 4:
					_ = tracker.IsNewSpecies(sp)
				case 5:
					_, _ = tracker.CheckAndUpdateSpecies(sp, now)
				case 6:
					_ = tracker.GetBatchSpeciesStatus(species[:2], now)
				case 7:
					tracker.RecordNotificationSent(sp, now)
				case 8:
					_ = tracker.ShouldSuppressNotification(sp, now)
				case 9:
					_ = tracker.PruneOldEntries()
				}
			}
		}(i)
	}

	wg.Wait()

	// Verify tracker is still in consistent state
	assert.Positive(t, tracker.GetSpeciesCount(), "Should have tracked some species")
}

// TestLoadDataErrorPaths tests error handling in data loading
func TestLoadDataErrorPaths(t *testing.T) {
	t.Parallel()

	t.Run("invalid date format in lifetime data", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{
				{ScientificName: "Test Species", FirstSeenDate: "invalid-date"},
			}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  60,
		}

		tracker := NewTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err, "Should not error on invalid date")

		// Species with invalid date should be skipped
		assert.Equal(t, 0, tracker.GetSpeciesCount(), "Species with invalid date should be skipped")
	})

	t.Run("empty first seen date", func(t *testing.T) {
		ds := mocks.NewMockInterface(t)
		ds.On("GetNewSpeciesDetections", mock.Anything, mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{
				{ScientificName: "Test Species", FirstSeenDate: ""},
			}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  60,
		}

		tracker := NewTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err, "Should not error on empty date")

		// Species with empty date should be skipped
		assert.Equal(t, 0, tracker.GetSpeciesCount(), "Species with empty date should be skipped")
	})
}

// TestSetCurrentYearForTesting tests the testing helper
func TestSetCurrentYearForTestingMethod(t *testing.T) {
	// Note: This test doesn't use t.Parallel() because it modifies internal state

	ds := mocks.NewMockInterface(t)
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

	tracker := NewTrackerFromSettings(ds, settings)

	// Set year for testing
	tracker.SetCurrentYearForTesting(2025)

	// Verify it was set
	tracker.mu.RLock()
	currentYear := tracker.currentYear
	tracker.mu.RUnlock()

	assert.Equal(t, 2025, currentYear, "Year should be set to 2025")
}
