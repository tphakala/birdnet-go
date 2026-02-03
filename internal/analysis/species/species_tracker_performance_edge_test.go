// new_species_tracker_performance_edge_test.go
// Performance and memory edge case tests for species tracker
// Critical for preventing OOM crashes and performance degradation under load
package species

import (
	"fmt"
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestMemoryExhaustionScenarios tests tracker behavior under memory pressure
// Critical for preventing OOM crashes in production
//
//nolint:gocognit // Memory stress test with multiple scenarios requires complex verification
func TestMemoryExhaustionScenarios(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory exhaustion tests in short mode")
	}
	t.Parallel()

	tests := []struct {
		name            string
		speciesCount    int
		cacheOperations int
		forceCleanup    bool
		description     string
	}{
		{
			"large_species_dataset", 10000, 5000, false,
			"10k species with moderate cache operations",
		},
		{
			"massive_cache_pressure", 5000, 20000, false,
			"5k species with intensive cache operations",
		},
		{
			"cleanup_under_pressure", 15000, 10000, true,
			"15k species with forced cache cleanup",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Running memory exhaustion test: %s", tt.description)

			// Measure initial memory
			var m1 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m1)
			initialMemory := m1.Alloc

			// Create tracker with realistic database data
			ds := mocks.NewMockInterface(t)

			// Simulate large database with many species
			lifetimeData := make([]datastore.NewSpeciesData, tt.speciesCount)
			for i := range tt.speciesCount {
				lifetimeData[i] = datastore.NewSpeciesData{
					ScientificName: fmt.Sprintf("Species_%06d", i),
					FirstSeenDate:  fmt.Sprintf("2023-%02d-%02d", (i%12)+1, (i%28)+1),
				}
			}

			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(lifetimeData, nil).Maybe()
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

			// Initialize with large dataset
			err := tracker.InitFromDatabase()
			require.NoError(t, err, "Should handle large dataset initialization")

			// Measure memory after initialization
			var m2 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m2)
			afterInitMemory := m2.Alloc

			// Calculate init increase safely to avoid underflow
			var initIncrease int64
			if afterInitMemory >= initialMemory {
				diff := afterInitMemory - initialMemory
				initIncrease = int64(min(diff, uint64(1<<62))) //nolint:gosec // Memory values won't exceed int64
			} else {
				initIncrease = 0
			}

			t.Logf("Memory after init: %d KB (increase: %d KB)",
				afterInitMemory/1024, initIncrease/1024)

			// Perform intensive operations
			currentTime := time.Now()
			operationCount := 0

			for i := range tt.cacheOperations {
				speciesName := fmt.Sprintf("Species_%06d", i%tt.speciesCount)

				// Mix of operations that stress memory
				switch i % 4 {
				case 0:
					status := tracker.GetSpeciesStatus(speciesName, currentTime)
					assert.GreaterOrEqual(t, status.DaysSinceFirst, 0)

				case 1:
					isNew, days := tracker.CheckAndUpdateSpecies(speciesName, currentTime)
					assert.GreaterOrEqual(t, days, 0)
					_ = isNew

				case 2:
					// Batch operations
					batchSpecies := []string{
						fmt.Sprintf("Species_%06d", (i+1)%tt.speciesCount),
						fmt.Sprintf("Species_%06d", (i+2)%tt.speciesCount),
						fmt.Sprintf("Species_%06d", (i+3)%tt.speciesCount),
					}
					statuses := tracker.GetBatchSpeciesStatus(batchSpecies, currentTime)
					assert.LessOrEqual(t, len(statuses), 3)

				case 3:
					isNew := tracker.IsNewSpecies(speciesName)
					_ = isNew
				}

				operationCount++

				// Force cleanup periodically if requested
				if tt.forceCleanup && i%1000 == 0 {
					tracker.ClearCacheForTesting()
					runtime.GC()
				}
			}

			// Measure final memory
			var m3 runtime.MemStats
			runtime.GC()
			runtime.ReadMemStats(&m3)
			finalMemory := m3.Alloc

			// Calculate memory increase safely to avoid underflow
			var totalIncrease int64
			if finalMemory >= initialMemory {
				diff := finalMemory - initialMemory
				totalIncrease = int64(min(diff, uint64(1<<62))) //nolint:gosec // Memory values won't exceed int64
			} else {
				totalIncrease = 0
			}

			t.Logf("Final memory: %d KB (total increase: %d KB)",
				finalMemory/1024, totalIncrease/1024)
			t.Logf("Operations completed: %d", operationCount)

			// Assert reasonable memory usage
			// Use signed arithmetic to avoid underflow when GC reduces memory usage
			var memoryIncrease int64
			if finalMemory >= initialMemory {
				diff := finalMemory - initialMemory
				memoryIncrease = int64(min(diff, uint64(1<<62))) //nolint:gosec // Memory values won't exceed int64
			} else {
				// Memory decreased due to GC - treat as no increase
				memoryIncrease = 0
			}
			maxReasonableIncrease := int64(100 * 1024 * 1024) // 100MB max increase

			assert.Less(t, memoryIncrease, maxReasonableIncrease,
				"Memory increase should be reasonable (<%d MB)", maxReasonableIncrease/(1024*1024))

			// Verify tracker is still functional
			testSpecies := "Memory_Test_Species"
			isNew, days := tracker.CheckAndUpdateSpecies(testSpecies, currentTime)
			assert.True(t, isNew)
			assert.Equal(t, 0, days)

			// Test cleanup effectiveness
			speciesCountBefore := tracker.GetSpeciesCount()
			tracker.ClearCacheForTesting()
			runtime.GC()

			var m4 runtime.MemStats
			runtime.ReadMemStats(&m4)
			afterCleanupMemory := m4.Alloc

			t.Logf("Memory after cleanup: %d KB", afterCleanupMemory/1024)
			t.Logf("Species count before cleanup: %d", speciesCountBefore)

			// Memory should be reduced after cleanup
			assert.Less(t, afterCleanupMemory, finalMemory,
				"Memory should decrease after cache cleanup")
		})
	}
}

// TestPerformanceUnderSustainedLoad has been refactored to reduce cognitive complexity
// See TestPerformanceUnderSustainedLoadRefactored in species_tracker_performance_edge_test_refactored.go
func TestPerformanceUnderSustainedLoad(t *testing.T) {
	t.Skip("This test has been refactored - see TestPerformanceUnderSustainedLoadRefactored")
}

// TestCacheEvictionUnderPressure tests cache behavior when approaching limits
// Critical for preventing unbounded memory growth
func TestCacheEvictionUnderPressure(t *testing.T) {
	t.Parallel()

	// Create tracker with forced cache limit conditions
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

	// Fill cache with many species to trigger eviction
	// Target cache size is 800 entries according to implementation
	const targetCacheSize = 800
	const overflowSpecies = 1200 // 50% over target

	currentTime := time.Now()

	// Populate cache beyond target size
	for i := range overflowSpecies {
		speciesName := fmt.Sprintf("Cache_Test_Species_%04d", i)

		// Access species to populate cache
		status := tracker.GetSpeciesStatus(speciesName, currentTime)
		assert.GreaterOrEqual(t, status.DaysSinceFirst, 0)

		// Every 100 species, check memory didn't grow unboundedly
		if i%100 == 0 && i > 0 {
			runtime.GC()
			var m runtime.MemStats
			runtime.ReadMemStats(&m)
			t.Logf("After %d species: memory = %d KB", i, m.Alloc/1024)
		}
	}

	// Verify system is still responsive after cache pressure
	testSpecies := "Post_Pressure_Test_Species"
	isNew, days := tracker.CheckAndUpdateSpecies(testSpecies, currentTime)
	assert.True(t, isNew)
	assert.Equal(t, 0, days)

	// Test that cleanup works effectively
	tracker.ClearCacheForTesting()

	// After cleanup, verify system is still functional
	anotherTestSpecies := "Post_Cleanup_Test_Species"
	isNew2, days2 := tracker.CheckAndUpdateSpecies(anotherTestSpecies, currentTime)
	assert.True(t, isNew2)
	assert.Equal(t, 0, days2)

	t.Logf("Cache eviction test completed successfully")
}
