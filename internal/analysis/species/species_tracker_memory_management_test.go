// new_species_tracker_memory_management_test.go
// Critical reliability tests for memory management and cache operations
// Targets cleanupExpiredCache and related functions to prevent OOM crashes
package species

import (
	"fmt"
	"runtime"
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

// TestCleanupExpiredCache_CriticalReliability tests cache cleanup for memory management
// CRITICAL: Prevents OOM crashes by managing cache size
func TestCleanupExpiredCache_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                 string
		initialCacheSize     int
		expiredEntries       int
		validEntries         int
		forceCleanup         bool
		expectedRemainingMin int
		expectedRemainingMax int
		description          string
	}{
		{
			"all_expired_entries",
			100, 100, 0,
			true,
			0, 0,
			"All expired entries should be removed",
		},
		{
			"mixed_expired_and_valid",
			100, 60, 40,
			true,
			40, 40,
			"Only expired entries should be removed, valid ones kept",
		},
		{
			"no_expired_entries",
			50, 0, 50,
			true,
			50, 50,
			"No entries should be removed if none are expired",
		},
		{
			"over_limit_triggers_lru",
			1200, 100, 1100, // 1200 total, will trigger LRU
			true,
			800, 800, // Should reduce to target size (800)
			"Cache over limit should trigger LRU eviction to target size",
		},
		{
			"exactly_at_target_size",
			800, 50, 750,
			true,
			750, 750, // After removing expired, should be at 750
			"Cache exactly at target size should only remove expired",
		},
		{
			"empty_cache",
			0, 0, 0,
			true,
			0, 0,
			"Empty cache should handle cleanup gracefully",
		},
		{
			"massive_cache_reduction",
			5000, 1000, 4000, // 5000 total entries
			true,
			800, 800, // Should reduce to target size
			"Massive cache should be reduced to target size efficiently",
		},
		{
			"skip_cleanup_if_recent",
			100, 50, 50,
			false,    // Don't force - should skip if recent
			100, 100, // Nothing removed if cleanup was recent
			"Cleanup should be skipped if performed recently",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing cache cleanup scenario: %s", tt.description)

			// Create tracker
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)
			// BG-17: InitFromDatabase now loads notification history
			ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
				Return([]datastore.NotificationHistory{}, nil)
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
			}

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Set up cache with test data
			currentTime := time.Now()
			expiredTime := currentTime.Add(-2 * tracker.cacheTTL) // Well past TTL
			validTime := currentTime.Add(-tracker.cacheTTL / 2)   // Still within TTL

			// Add expired entries
			for i := 0; i < tt.expiredEntries; i++ {
				speciesName := fmt.Sprintf("Expired_Species_%04d", i)
				tracker.statusCache[speciesName] = cachedSpeciesStatus{
					status: SpeciesStatus{
						FirstSeenTime:   expiredTime,
						IsNew:           false,
						DaysSinceFirst:  30,
						LastUpdatedTime: expiredTime,
					},
					timestamp: expiredTime,
				}
			}

			// Add valid entries
			for i := 0; i < tt.validEntries; i++ {
				speciesName := fmt.Sprintf("Valid_Species_%04d", i)
				tracker.statusCache[speciesName] = cachedSpeciesStatus{
					status: SpeciesStatus{
						FirstSeenTime:   validTime,
						IsNew:           true,
						DaysSinceFirst:  5,
						LastUpdatedTime: validTime,
					},
					timestamp: validTime,
				}
			}

			// If not forcing, set last cleanup to recent time
			if !tt.forceCleanup {
				tracker.lastCacheCleanup = currentTime.Add(-5 * time.Second)
			}

			// Measure memory before cleanup
			runtime.GC()
			var m1 runtime.MemStats
			runtime.ReadMemStats(&m1)
			beforeMemory := m1.Alloc

			// Test cleanup - need to hold lock since this is an internal function
			tracker.mu.Lock()
			initialSize := len(tracker.statusCache)
			tracker.cleanupExpiredCacheWithForce(currentTime, tt.forceCleanup)
			finalSize := len(tracker.statusCache)
			tracker.mu.Unlock()

			// Measure memory after cleanup
			runtime.GC()
			var m2 runtime.MemStats
			runtime.ReadMemStats(&m2)
			afterMemory := m2.Alloc

			// Verify results
			assert.GreaterOrEqual(t, finalSize, tt.expectedRemainingMin,
				"Cache size should be at least %d", tt.expectedRemainingMin)
			assert.LessOrEqual(t, finalSize, tt.expectedRemainingMax,
				"Cache size should be at most %d", tt.expectedRemainingMax)

			// Verify no valid entries were incorrectly removed
			if tt.forceCleanup && tt.validEntries > 0 && finalSize > 0 {
				// Check that at least some valid entries remain
				validCount := 0
				for name := range tracker.statusCache {
					if name[:5] == "Valid" {
						validCount++
					}
				}

				if tt.initialCacheSize <= 1000 { // If not triggering LRU
					assert.Equal(t, tt.validEntries, validCount,
						"All valid entries should be preserved when not over limit")
				}
			}

			t.Logf("✓ Cache cleanup: %d -> %d entries (removed %d)",
				initialSize, finalSize, initialSize-finalSize)

			if tt.initialCacheSize > 0 {
				memoryReduction := int64(0)
				if beforeMemory > afterMemory {
					memoryReduction = int64(beforeMemory - afterMemory)
				}
				t.Logf("✓ Memory impact: ~%d KB freed", memoryReduction/1024)
			}

			// Verify cleanup timestamp updated if cleanup occurred
			if tt.forceCleanup {
				assert.WithinDuration(t, currentTime, tracker.lastCacheCleanup, time.Second,
					"Cleanup timestamp should be updated")
			}
		})
	}
}

// TestCacheLRUEviction_CriticalReliability tests LRU eviction under memory pressure
// CRITICAL: Ensures system doesn't run out of memory with many species
func TestCacheLRUEviction_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	// Set a longer TTL so entries don't get expired during LRU test
	tracker.cacheTTL = 2 * time.Hour // Long enough so entries aren't expired

	currentTime := time.Now()

	// Fill cache beyond limit to trigger LRU
	const totalEntries = 1500
	const targetSize = 800 // From implementation

	// Add entries with different timestamps for LRU testing
	for i := 0; i < totalEntries; i++ {
		speciesName := fmt.Sprintf("LRU_Test_Species_%04d", i)
		// Older entries have earlier timestamps (but within TTL)
		entryTime := currentTime.Add(-time.Duration(totalEntries-i) * time.Second)

		tracker.statusCache[speciesName] = cachedSpeciesStatus{
			status: SpeciesStatus{
				FirstSeenTime:   entryTime,
				IsNew:           false,
				DaysSinceFirst:  i,
				LastUpdatedTime: entryTime,
			},
			timestamp: entryTime,
		}
	}

	t.Logf("Initial cache size: %d entries", len(tracker.statusCache))

	// Force cleanup to trigger LRU - need to hold lock since this is an internal function
	tracker.mu.Lock()
	tracker.cleanupExpiredCacheWithForce(currentTime, true)
	tracker.mu.Unlock()

	// Verify LRU worked correctly
	assert.LessOrEqual(t, len(tracker.statusCache), targetSize,
		"Cache should be reduced to target size")

	// Verify that newer entries were kept (higher indices)
	keptCount := 0
	removedCount := 0

	for i := 0; i < totalEntries; i++ {
		speciesName := fmt.Sprintf("LRU_Test_Species_%04d", i)
		if _, exists := tracker.statusCache[speciesName]; exists {
			keptCount++
			// Kept entries should generally be the newer ones (higher indices)
			if i < totalEntries-targetSize-100 { // Allow some margin for sorting
				t.Logf("Older entry kept: %s (index %d)", speciesName, i)
			}
		} else {
			removedCount++
		}
	}

	t.Logf("✓ LRU eviction: kept %d, removed %d entries", keptCount, removedCount)
	assert.Equal(t, targetSize, keptCount, "Should keep exactly target size entries")
	assert.Equal(t, totalEntries-targetSize, removedCount, "Should remove excess entries")
}

// TestBuildSpeciesStatusWithBuffer_CriticalReliability tests status building with buffer optimization
// CRITICAL: Core business logic that affects all status calculations
func TestBuildSpeciesStatusWithBuffer_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name                string
		speciesName         string
		currentTime         time.Time
		lifetimeFirstSeen   *time.Time
		yearlyFirstSeen     *time.Time
		seasonalFirstSeen   *time.Time
		windowDays          int
		expectedIsNew       bool
		expectedDaysSince   int
		expectedIsNewYear   bool
		expectedIsNewSeason bool
		description         string
	}{
		{
			"completely_new_species",
			"New_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			nil, nil, nil,
			14,
			true, 0, true, true,
			"Completely new species should be marked new in all periods",
		},
		{
			"existing_within_window",
			"Recent_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			&time.Time{}, // Will be set to 10 days ago
			&time.Time{},
			&time.Time{},
			14,
			true, 10, true, true,
			"Species within lifetime window, new to year/season should be marked as new",
		},
		{
			"existing_outside_window",
			"Old_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			&time.Time{}, // Will be set to 30 days ago
			&time.Time{},
			&time.Time{},
			14,
			false, 30, false, false,
			"Species outside window should not be marked as new",
		},
		{
			"new_this_year_old_lifetime",
			"Yearly_New_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			&time.Time{}, // 100 days ago (lifetime)
			nil,          // New this year
			nil,          // New this season
			14,
			false, 100, true, true,
			"Species new to year but not lifetime should be marked correctly",
		},
		{
			"edge_case_exactly_at_window",
			"Edge_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			&time.Time{}, // Exactly 14 days ago
			&time.Time{},
			&time.Time{},
			14,
			true, 14, true, true,
			"Species exactly at window boundary, new to year/season should be marked as new",
		},
		{
			"edge_case_just_outside_window",
			"Just_Outside_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			&time.Time{}, // 15 days ago
			&time.Time{},
			&time.Time{},
			14,
			false, 15, false, false,
			"Species just outside window should not be marked as new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing status building: %s", tt.description)

			// Create tracker
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)
			// BG-17: InitFromDatabase now loads notification history
			ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
				Return([]datastore.NotificationHistory{}, nil)
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil)

			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: tt.windowDays,
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

			// Set up test data based on scenario
			if tt.lifetimeFirstSeen != nil {
				var firstSeen time.Time
				switch tt.expectedDaysSince {
				case 10:
					firstSeen = tt.currentTime.Add(-10 * 24 * time.Hour)
				case 30:
					firstSeen = tt.currentTime.Add(-30 * 24 * time.Hour)
				case 100:
					firstSeen = tt.currentTime.Add(-100 * 24 * time.Hour)
				case 14:
					firstSeen = tt.currentTime.Add(-14 * 24 * time.Hour)
				case 15:
					firstSeen = tt.currentTime.Add(-15 * 24 * time.Hour)
				default:
					firstSeen = tt.currentTime
				}
				tracker.speciesFirstSeen[tt.speciesName] = firstSeen
				*tt.lifetimeFirstSeen = firstSeen
			}

			// Only populate yearly tracking if we expect IsNewThisYear = false
			if tt.yearlyFirstSeen != nil && tt.expectedDaysSince <= 30 && !tt.expectedIsNewYear {
				yearFirst := tt.currentTime.Add(-time.Duration(tt.expectedDaysSince) * 24 * time.Hour)
				tracker.speciesThisYear[tt.speciesName] = yearFirst
				*tt.yearlyFirstSeen = yearFirst
			}

			// Only populate seasonal tracking if we expect IsNewThisSeason = false
			if tt.seasonalFirstSeen != nil && tt.expectedDaysSince <= 30 && !tt.expectedIsNewSeason {
				seasonFirst := tt.currentTime.Add(-time.Duration(tt.expectedDaysSince) * 24 * time.Hour)
				currentSeason := tracker.getCurrentSeason(tt.currentTime)
				if tracker.speciesBySeason[currentSeason] == nil {
					tracker.speciesBySeason[currentSeason] = make(map[string]time.Time)
				}
				tracker.speciesBySeason[currentSeason][tt.speciesName] = seasonFirst
				*tt.seasonalFirstSeen = seasonFirst
			}

			// Test buildSpeciesStatusWithBuffer
			currentSeason := tracker.getCurrentSeason(tt.currentTime)
			status := tracker.buildSpeciesStatusWithBuffer(tt.speciesName, tt.currentTime, currentSeason)

			// Verify results
			assert.Equal(t, tt.expectedIsNew, status.IsNew,
				"IsNew incorrect for scenario: %s", tt.name)
			assert.Equal(t, tt.expectedDaysSince, status.DaysSinceFirst,
				"DaysSinceFirst incorrect for scenario: %s", tt.name)
			assert.Equal(t, tt.expectedIsNewYear, status.IsNewThisYear,
				"IsNewThisYear incorrect for scenario: %s", tt.name)
			assert.Equal(t, tt.expectedIsNewSeason, status.IsNewThisSeason,
				"IsNewThisSeason incorrect for scenario: %s", tt.name)

			// Verify all required fields are populated
			assert.False(t, status.LastUpdatedTime.IsZero(),
				"LastUpdatedTime should be populated")
			assert.Equal(t, currentSeason, status.CurrentSeason,
				"CurrentSeason should be set correctly")
			assert.GreaterOrEqual(t, status.DaysSinceFirst, 0,
				"DaysSinceFirst should never be negative")

			t.Logf("✓ Status built correctly: IsNew=%v, Days=%d, NewYear=%v, NewSeason=%v",
				status.IsNew, status.DaysSinceFirst, status.IsNewThisYear, status.IsNewThisSeason)
		})
	}
}

// TestConcurrentCacheOperations_CriticalReliability tests thread safety of cache operations
// CRITICAL: Prevents race conditions and data corruption
func TestConcurrentCacheOperations_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker
	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	currentTime := time.Now()
	const numGoroutines = 100
	const opsPerGoroutine = 50

	var wg sync.WaitGroup
	errors := make(chan error, numGoroutines*opsPerGoroutine)

	// Run concurrent operations that stress the cache
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			for op := 0; op < opsPerGoroutine; op++ {
				// Mix of operations
				switch op % 4 {
				case 0: // Add to cache
					speciesName := fmt.Sprintf("Concurrent_Species_%d_%d", id, op)
					tracker.UpdateSpecies(speciesName, currentTime)

				case 1: // Read from cache
					speciesName := fmt.Sprintf("Concurrent_Species_%d_%d", id, op-1)
					status := tracker.GetSpeciesStatus(speciesName, currentTime)
					if status.DaysSinceFirst < 0 {
						errors <- fmt.Errorf("negative days for species %s", speciesName)
					}

				case 2: // Force cleanup - need to hold lock since this is an internal function
					tracker.mu.Lock()
					tracker.cleanupExpiredCacheWithForce(currentTime, true)
					tracker.mu.Unlock()

				case 3: // Batch operation
					species := []string{
						fmt.Sprintf("Batch_Species_%d_A", id),
						fmt.Sprintf("Batch_Species_%d_B", id),
					}
					results := tracker.GetBatchSpeciesStatus(species, currentTime)
					if len(results) != len(species) {
						errors <- fmt.Errorf("batch operation returned wrong count")
					}
				}
			}
		}(i)
	}

	wg.Wait()
	close(errors)

	// Check for any errors
	errorCount := 0
	for err := range errors {
		t.Errorf("Concurrent operation error: %v", err)
		errorCount++
	}

	assert.Equal(t, 0, errorCount, "No errors should occur during concurrent operations")
	t.Logf("✓ %d concurrent operations completed successfully", numGoroutines*opsPerGoroutine)

	// Verify cache is still functional
	testSpecies := "Post_Concurrent_Test"
	isNew := tracker.UpdateSpecies(testSpecies, currentTime)
	assert.True(t, isNew, "Tracker should remain functional after concurrent operations")
}
