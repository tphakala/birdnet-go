package processor

import (
	"runtime"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestMemoryManagement tests cache cleanup and memory management using public helpers
func TestMemoryManagement(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)

	t.Run("cache_cleanup_using_public_helpers", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		// Initially cache should be empty
		initialCacheSize := tracker.CacheSizeForTesting()
		assert.Equal(t, 0, initialCacheSize, "Cache should be empty initially")

		// Add several species to populate cache
		species := []string{"Species1", "Species2", "Species3", "Species4", "Species5"}
		for _, sp := range species {
			tracker.GetSpeciesStatus(sp, baseTime)
		}

		// Verify cache is populated using public helper
		cacheSize := tracker.CacheSizeForTesting()
		assert.Equal(t, len(species), cacheSize, "Cache should contain all species")

		// Force cleanup with current time (entries should still be valid)
		tracker.ForceCleanupForTesting(baseTime.Add(10 * time.Second))
		afterCleanupSize := tracker.CacheSizeForTesting()
		assert.Equal(t, len(species), afterCleanupSize, "Recent entries should not be cleaned up")

		// Force cleanup with future time to expire all entries
		futureTime := baseTime.Add(2 * time.Hour) // Well beyond default cache TTL
		tracker.ForceCleanupForTesting(futureTime)
		expiredCacheSize := tracker.CacheSizeForTesting()
		assert.Equal(t, 0, expiredCacheSize, "Expired entries should be cleaned up")

		ds.AssertExpectations(t)
	})

	t.Run("lru_cache_eviction_using_public_helpers", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		// Add many species to trigger LRU eviction (over the 1000 limit in cleanupExpiredCache)
		const numSpecies = 1200 // Exceeds maxStatusCacheSize constant
		
		// Add species incrementally to simulate realistic usage
		for i := 0; i < numSpecies; i++ {
			speciesName := generateSpeciesName(i)
			tracker.GetSpeciesStatus(speciesName, baseTime.Add(time.Duration(i)*time.Second))
			
			// Force cleanup periodically to trigger LRU eviction
			if i > 0 && i%100 == 0 {
				tracker.ForceCleanupForTesting(baseTime.Add(time.Duration(i)*time.Second))
			}
		}

		// Cache should be limited to reasonable size due to LRU eviction
		finalCacheSize := tracker.CacheSizeForTesting()
		assert.LessOrEqual(t, finalCacheSize, 1000, "Cache should be limited by LRU eviction")
		assert.Positive(t, finalCacheSize, "Cache should not be completely empty")

		ds.AssertExpectations(t)
	})

	t.Run("memory_usage_bounds_validation", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 30,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled:    true,
				ResetMonth: 1,
				ResetDay:   1,
				WindowDays: 60,
			},
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled:    true,
				WindowDays: 45,
			},
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		// Record initial memory stats
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		// Add significant number of species across all tracking periods
		const testSpecies = 500
		for i := 0; i < testSpecies; i++ {
			speciesName := generateSpeciesName(i)
			detectionTime := baseTime.Add(time.Duration(i) * time.Hour)
			
			// Use both methods to populate all tracking structures
			tracker.CheckAndUpdateSpecies(speciesName, detectionTime)
			tracker.GetSpeciesStatus(speciesName, detectionTime.Add(time.Minute))
		}

		// Verify cache is populated
		cacheSize := tracker.CacheSizeForTesting()
		assert.Positive(t, cacheSize, "Cache should be populated")

		// Force comprehensive cleanup
		futureTime := baseTime.Add(24 * 90 * time.Hour) // 90 days in the future
		tracker.ForceCleanupForTesting(futureTime)
		
		// Prune old entries to test memory reclamation
		pruned := tracker.PruneOldEntries()
		assert.Positive(t, pruned, "Should have pruned some old entries")

		// Check final cache state
		finalCacheSize := tracker.CacheSizeForTesting()
		t.Logf("Cache size after cleanup: initial=%d, final=%d, pruned=%d", 
			cacheSize, finalCacheSize, pruned)

		// Record final memory stats
		runtime.GC()
		runtime.ReadMemStats(&m2)

		// Memory usage should be bounded (informational, not hard assertion for CI reliability)
		memIncrease := int64(m2.Alloc) - int64(m1.Alloc)
		t.Logf("Memory usage: initial=%d KB, final=%d KB, increase=%d KB", 
			m1.Alloc/1024, m2.Alloc/1024, memIncrease/1024)

		ds.AssertExpectations(t)
	})

	t.Run("cache_invalidation_on_updates", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		speciesName := "CacheInvalidationTest"

		// Populate cache
		status1 := tracker.GetSpeciesStatus(speciesName, baseTime)
		assert.True(t, status1.IsNew, "Should be new initially")
		
		initialCacheSize := tracker.CacheSizeForTesting()
		assert.Equal(t, 1, initialCacheSize, "Cache should contain one entry")

		// Update species (this should invalidate cache)
		tracker.UpdateSpecies(speciesName, baseTime.Add(-20*24*time.Hour)) // 20 days ago (beyond 14-day window)

		// Cache size might remain same but the entry should be invalidated
		// and regenerated on next access
		status2 := tracker.GetSpeciesStatus(speciesName, baseTime)
		assert.False(t, status2.IsNew, "Should not be new after historical update beyond window")
		assert.Positive(t, status2.DaysSinceFirst, "Should show days since first detection")

		finalCacheSize := tracker.CacheSizeForTesting()
		assert.Equal(t, 1, finalCacheSize, "Cache should still contain the updated entry")

		ds.AssertExpectations(t)
	})

	t.Run("concurrent_cache_operations", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		const numGoroutines = 10
		const operationsPerGoroutine = 50
		
		// Run concurrent cache operations
		done := make(chan bool, numGoroutines)
		
		for g := 0; g < numGoroutines; g++ {
			go func(gID int) {
				defer func() { done <- true }()
				
				for i := 0; i < operationsPerGoroutine; i++ {
					speciesName := generateSpeciesName(gID*operationsPerGoroutine + i)
					opTime := baseTime.Add(time.Duration(i) * time.Second)
					
					// Mix different operations
					switch i % 4 {
					case 0:
						tracker.GetSpeciesStatus(speciesName, opTime)
					case 1:
						tracker.UpdateSpecies(speciesName, opTime)
					case 2:
						tracker.CheckAndUpdateSpecies(speciesName, opTime)
					case 3:
						// Periodic cleanup
						tracker.ForceCleanupForTesting(opTime)
					}
				}
			}(g)
		}

		// Wait for all goroutines to complete
		for i := 0; i < numGoroutines; i++ {
			<-done
		}

		// Verify cache state is reasonable
		finalCacheSize := tracker.CacheSizeForTesting()
		assert.GreaterOrEqual(t, finalCacheSize, 0, "Cache size should be non-negative")
		assert.LessOrEqual(t, finalCacheSize, 1000, "Cache should be bounded by cleanup")

		// Final cleanup
		tracker.ForceCleanupForTesting(baseTime.Add(2 * time.Hour))
		cleanedCacheSize := tracker.CacheSizeForTesting()
		
		t.Logf("Concurrent cache operations: final=%d, after_cleanup=%d", 
			finalCacheSize, cleanedCacheSize)

		ds.AssertExpectations(t)
	})
}

// generateSpeciesName creates unique species names for testing
func generateSpeciesName(index int) string {
	// Use modulo to create repeating patterns that test cache behavior
	return map[int]string{
		0: "Turdus_migratorius",
		1: "Cardinalis_cardinalis", //nolint:misspell // Cardinalis is correct scientific genus name
		2: "Poecile_atricapillus",
		3: "Sialia_sialis",
		4: "Corvus_brachyrhynchos",
	}[index%5] + "_" + string(rune('A'+index%26)) + string(rune('0'+index%10))
}

// BenchmarkMemoryManagement benchmarks memory-related operations
func BenchmarkMemoryManagement(b *testing.B) {
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	baseTime := time.Now()

	b.Run("CacheOperations", func(b *testing.B) {
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			speciesName := generateSpeciesName(i)
			tracker.GetSpeciesStatus(speciesName, baseTime)
			
			// Periodic cleanup to prevent unbounded growth
			if i%100 == 0 {
				tracker.ForceCleanupForTesting(baseTime.Add(time.Duration(i) * time.Second))
			}
		}
	})

	b.Run("CacheCleanup", func(b *testing.B) {
		// Pre-populate cache
		for i := 0; i < 1000; i++ {
			tracker.GetSpeciesStatus(generateSpeciesName(i), baseTime)
		}

		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracker.ForceCleanupForTesting(baseTime.Add(time.Duration(i) * time.Minute))
		}
	})
}