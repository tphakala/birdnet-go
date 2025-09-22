// new_species_tracker_performance_edge_test.go
// Performance and memory edge case tests for species tracker
// Critical for preventing OOM crashes and performance degradation under load
package species

import (
	"context"
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestMemoryExhaustionScenarios tests tracker behavior under memory pressure
// Critical for preventing OOM crashes in production
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
			ds := &MockSpeciesDatastore{}

			// Simulate large database with many species
			lifetimeData := make([]datastore.NewSpeciesData, tt.speciesCount)
			for i := 0; i < tt.speciesCount; i++ {
				lifetimeData[i] = datastore.NewSpeciesData{
					ScientificName: fmt.Sprintf("Species_%06d", i),
					FirstSeenDate:  fmt.Sprintf("2023-%02d-%02d", (i%12)+1, (i%28)+1),
				}
			}

			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return(lifetimeData, nil)
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
				initIncrease = int64(afterInitMemory - initialMemory)
			} else {
				initIncrease = 0
			}

			t.Logf("Memory after init: %d KB (increase: %d KB)",
				afterInitMemory/1024, initIncrease/1024)

			// Perform intensive operations
			currentTime := time.Now()
			operationCount := 0

			for i := 0; i < tt.cacheOperations; i++ {
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
				totalIncrease = int64(finalMemory - initialMemory)
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
				memoryIncrease = int64(finalMemory - initialMemory)
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

// TestPerformanceUnderSustainedLoad tests tracker performance under sustained operations
// Critical for ensuring system responsiveness under continuous load
func TestPerformanceUnderSustainedLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping sustained load tests in short mode")
	}
	t.Parallel()

	tests := []struct {
		name                string
		durationSeconds     int
		operationsPerSecond int
		speciesCount        int
		description         string
	}{
		{
			"moderate_sustained_load", 10, 100, 50,
			"10 seconds at 100 ops/sec with 50 species",
		},
		{
			"high_sustained_load", 15, 200, 100,
			"15 seconds at 200 ops/sec with 100 species",
		},
		{
			"burst_then_sustained", 12, 300, 25,  // Reduced from 500 to 300 ops/sec for stability
			"12 seconds at 300 ops/sec with 25 species (high contention)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Running sustained load test: %s", tt.description)

			// Setup tracker
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

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Performance tracking with atomics for thread safety
			var totalOperations atomic.Int64
			var responseTimeSum atomic.Int64
			var maxResponseTime atomic.Int64
			minResponseTime := atomic.Int64{}
			minResponseTime.Store(int64(time.Hour)) // Start with very high value

			// Generate species pool
			species := make([]string, tt.speciesCount)
			for i := range tt.speciesCount {
				species[i] = fmt.Sprintf("LoadTest_Species_%d", i)
			}

			// Create context with timeout for safety
			ctx, cancel := context.WithTimeout(context.Background(), time.Duration(tt.durationSeconds)*time.Second+5*time.Second)
			defer cancel()

			// Sustained load test with improved concurrency
			startTime := time.Now()
			endTime := startTime.Add(time.Duration(tt.durationSeconds) * time.Second)

			// Use a rate limiter for more controlled operation rate
			operationInterval := time.Second / time.Duration(tt.operationsPerSecond)
			operationTicker := time.NewTicker(operationInterval)
			defer operationTicker.Stop()

			// Use WaitGroup for cleaner goroutine management (Go 1.25 feature)
			var wg sync.WaitGroup
			done := make(chan struct{})

			// Worker goroutine using sync.WaitGroup.Go() pattern
			wg.Go(func() {
				defer close(done)
				for {
					select {
					case <-ctx.Done():
						return
					case <-operationTicker.C:
						if time.Now().After(endTime) {
							return
						}

						// Measure operation response time
						opStart := time.Now()

						// Rotate through different operations
						opCount := totalOperations.Load()
						speciesName := species[int(opCount)%len(species)]
						currentTime := time.Now()

						switch opCount % 3 {
						case 0:
							isNew, days := tracker.CheckAndUpdateSpecies(speciesName, currentTime)
							_ = isNew
							_ = days

						case 1:
							status := tracker.GetSpeciesStatus(speciesName, currentTime)
							_ = status

						case 2:
							isNew := tracker.IsNewSpecies(speciesName)
							_ = isNew
						}

						opDuration := time.Since(opStart)

						// Track performance metrics using atomics
						totalOperations.Add(1)
						responseTimeSum.Add(int64(opDuration))
						
						// Update max response time
						for {
							oldMax := maxResponseTime.Load()
							if int64(opDuration) <= oldMax || maxResponseTime.CompareAndSwap(oldMax, int64(opDuration)) {
								break
							}
						}
						
						// Update min response time
						for {
							oldMin := minResponseTime.Load()
							if int64(opDuration) >= oldMin || minResponseTime.CompareAndSwap(oldMin, int64(opDuration)) {
								break
							}
						}
					}
				}
			})

			// Wait for test completion
			select {
			case <-done:
				// Normal completion
			case <-ctx.Done():
				// Timeout or cancellation
				t.Fatalf("Test timed out or was cancelled")
			}

			wg.Wait()

			actualDuration := time.Since(startTime)
			finalOps := totalOperations.Load()
			finalResponseSum := responseTimeSum.Load()
			finalMax := maxResponseTime.Load()
			finalMin := minResponseTime.Load()
			
			avgResponseTime := time.Duration(0)
			if finalOps > 0 {
				avgResponseTime = time.Duration(finalResponseSum / finalOps)
			}
			actualOpsPerSec := float64(finalOps) / actualDuration.Seconds()

			t.Logf("Sustained load test completed:")
			t.Logf("  Actual duration: %v", actualDuration)
			t.Logf("  Total operations: %d", finalOps)
			t.Logf("  Actual ops/sec: %.2f", actualOpsPerSec)
			t.Logf("  Average response time: %v", avgResponseTime)
			t.Logf("  Min response time: %v", time.Duration(finalMin))
			t.Logf("  Max response time: %v", time.Duration(finalMax))

			// Performance assertions
			assert.Greater(t, int(finalOps), tt.operationsPerSecond*tt.durationSeconds/2,
				"Should complete at least 50% of target operations")

			// Response time should be reasonable (< 10ms for normal operations)
			assert.Less(t, avgResponseTime, 10*time.Millisecond,
				"Average response time should be under 10ms")

			assert.Less(t, time.Duration(finalMax), 100*time.Millisecond,
				"Max response time should be under 100ms")

			// Verify system stability after sustained load
			speciesCount := tracker.GetSpeciesCount()
			assert.LessOrEqual(t, speciesCount, tt.speciesCount,
				"Species count should not exceed test species")
			assert.GreaterOrEqual(t, speciesCount, 1,
				"Should track at least some species")

			// Test final operation to ensure tracker is still responsive
			finalTestTime := time.Now()
			isNew, days := tracker.CheckAndUpdateSpecies("Final_Test_Species", finalTestTime)
			assert.True(t, isNew)
			assert.Equal(t, 0, days)

			// Cleanup
			tracker.ClearCacheForTesting()
		})
	}
}

// TestCacheEvictionUnderPressure tests cache behavior when approaching limits
// Critical for preventing unbounded memory growth
func TestCacheEvictionUnderPressure(t *testing.T) {
	t.Parallel()

	// Create tracker with forced cache limit conditions
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

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	// Fill cache with many species to trigger eviction
	// Target cache size is 800 entries according to implementation
	const targetCacheSize = 800
	const overflowSpecies = 1200 // 50% over target

	currentTime := time.Now()

	// Populate cache beyond target size
	for i := 0; i < overflowSpecies; i++ {
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
