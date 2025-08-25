package processor

import (
	"os"
	"runtime"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// Performance threshold configuration with environment variable overrides
type performanceThresholds struct {
	maxLatencyMs          int // Maximum acceptable latency in milliseconds
	minOpsPerSecond       int // Minimum operations per second
	maxMemoryIncreaseMB   int // Maximum memory increase in MB
	concurrentGoroutines  int // Number of concurrent goroutines for load testing
	operationsPerTest     int // Number of operations per performance test
	maxCacheCleanupMs     int // Maximum time for cache cleanup in milliseconds
}

// getPerformanceThresholds returns configurable performance thresholds
func getPerformanceThresholds() performanceThresholds {
	return performanceThresholds{
		maxLatencyMs:          getEnvInt("BIRDNET_TEST_MAX_LATENCY_MS", 10),      // Default: 10ms max latency
		minOpsPerSecond:       getEnvInt("BIRDNET_TEST_MIN_OPS_PER_SEC", 1000),   // Default: 1000 ops/sec minimum
		maxMemoryIncreaseMB:   getEnvInt("BIRDNET_TEST_MAX_MEMORY_MB", 10),       // Default: 10MB max increase
		concurrentGoroutines:  getEnvInt("BIRDNET_TEST_GOROUTINES", 50),          // Default: 50 goroutines
		operationsPerTest:     getEnvInt("BIRDNET_TEST_OPERATIONS", 10000),       // Default: 10k operations
		maxCacheCleanupMs:     getEnvInt("BIRDNET_TEST_MAX_CLEANUP_MS", 100),     // Default: 100ms max cleanup
	}
}

// getEnvInt gets an integer from environment variable with fallback to default
func getEnvInt(key string, defaultVal int) int {
	if val := os.Getenv(key); val != "" {
		if parsed, err := strconv.Atoi(val); err == nil {
			return parsed
		}
	}
	return defaultVal
}

// TestPerformanceEdgeCases tests performance under various edge conditions with configurable thresholds
func TestPerformanceEdgeCases(t *testing.T) {
	t.Parallel()

	thresholds := getPerformanceThresholds()
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)

	t.Logf("Performance test configuration: MaxLatency=%dms, MinOps=%d/sec, MaxMemory=%dMB, Goroutines=%d, Operations=%d",
		thresholds.maxLatencyMs, thresholds.minOpsPerSecond, thresholds.maxMemoryIncreaseMB,
		thresholds.concurrentGoroutines, thresholds.operationsPerTest)

	t.Run("single_operation_latency", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
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
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		// Test single operation latency
		speciesName := "LatencyTestSpecies"
		
		// Warm up
		tracker.GetSpeciesStatus(speciesName, baseTime)
		
		// Measure latency for subsequent calls
		const measurements = 100
		var totalDuration time.Duration
		
		for i := 0; i < measurements; i++ {
			start := time.Now()
			tracker.GetSpeciesStatus(speciesName, baseTime.Add(time.Duration(i)*time.Second))
			duration := time.Since(start)
			totalDuration += duration
		}
		
		avgLatencyMs := float64(totalDuration.Nanoseconds()) / float64(measurements) / 1e6
		t.Logf("Average single operation latency: %.2f ms", avgLatencyMs)
		
		// Use configurable threshold
		if avgLatencyMs > float64(thresholds.maxLatencyMs) {
			t.Logf("WARNING: Latency %.2fms exceeds threshold %dms (configurable via BIRDNET_TEST_MAX_LATENCY_MS)", 
				avgLatencyMs, thresholds.maxLatencyMs)
		} else {
			t.Logf("✓ Latency within acceptable threshold of %dms", thresholds.maxLatencyMs)
		}

		ds.AssertExpectations(t)
	})

	t.Run("high_throughput_performance", func(t *testing.T) {
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

		// High throughput test
		operations := thresholds.operationsPerTest
		species := make([]string, 100) // Pre-generate species names
		for i := range species {
			species[i] = "ThroughputSpecies_" + strconv.Itoa(i)
		}

		start := time.Now()
		for i := 0; i < operations; i++ {
			speciesName := species[i%len(species)]
			tracker.CheckAndUpdateSpecies(speciesName, baseTime.Add(time.Duration(i)*time.Microsecond))
		}
		duration := time.Since(start)
		
		opsPerSecond := float64(operations) / duration.Seconds()
		t.Logf("Throughput: %.0f operations/second (target: %d ops/sec)", opsPerSecond, thresholds.minOpsPerSecond)
		
		// Use configurable threshold
		if opsPerSecond < float64(thresholds.minOpsPerSecond) {
			t.Logf("WARNING: Throughput %.0f ops/sec below threshold %d ops/sec (configurable via BIRDNET_TEST_MIN_OPS_PER_SEC)", 
				opsPerSecond, thresholds.minOpsPerSecond)
		} else {
			t.Logf("✓ Throughput meets threshold of %d ops/sec", thresholds.minOpsPerSecond)
		}

		ds.AssertExpectations(t)
	})

	t.Run("concurrent_load_performance", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
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
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		// Concurrent load test
		goroutines := thresholds.concurrentGoroutines
		operationsPerGoroutine := thresholds.operationsPerTest / goroutines
		
		var wg sync.WaitGroup
		results := make(chan time.Duration, goroutines)
		
		start := time.Now()
		for g := 0; g < goroutines; g++ {
			wg.Add(1)
			go func(gID int) {
				defer wg.Done()
				
				goroutineStart := time.Now()
				for i := 0; i < operationsPerGoroutine; i++ {
					speciesName := "ConcurrentSpecies_" + strconv.Itoa(gID) + "_" + strconv.Itoa(i%10)
					opTime := baseTime.Add(time.Duration(gID*operationsPerGoroutine+i) * time.Microsecond)
					
					// Mix operations
					switch i % 3 {
					case 0:
						tracker.CheckAndUpdateSpecies(speciesName, opTime)
					case 1:
						tracker.GetSpeciesStatus(speciesName, opTime)
					case 2:
						tracker.UpdateSpecies(speciesName, opTime)
					}
				}
				results <- time.Since(goroutineStart)
			}(g)
		}
		
		wg.Wait()
		close(results)
		totalDuration := time.Since(start)
		
		// Analyze per-goroutine performance
		var slowestGoroutine time.Duration
		for result := range results {
			if result > slowestGoroutine {
				slowestGoroutine = result
			}
		}
		
		totalOps := goroutines * operationsPerGoroutine
		overallOpsPerSec := float64(totalOps) / totalDuration.Seconds()
		
		t.Logf("Concurrent load test: %d goroutines, %d total ops, %.0f ops/sec, slowest goroutine: %v", 
			goroutines, totalOps, overallOpsPerSec, slowestGoroutine)
		
		// Use configurable threshold for concurrent performance
		minConcurrentOps := float64(thresholds.minOpsPerSecond) * 0.8 // Allow 20% degradation under concurrent load
		if overallOpsPerSec < minConcurrentOps {
			t.Logf("WARNING: Concurrent throughput %.0f ops/sec below threshold %.0f ops/sec", 
				overallOpsPerSec, minConcurrentOps)
		} else {
			t.Logf("✓ Concurrent throughput meets threshold of %.0f ops/sec", minConcurrentOps)
		}

		ds.AssertExpectations(t)
	})

	t.Run("cache_cleanup_performance", func(t *testing.T) {
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

		// Pre-populate cache with many entries
		const cacheEntries = 2000 // Exceed the 1000 limit to trigger cleanup
		for i := 0; i < cacheEntries; i++ {
			speciesName := "CacheCleanupSpecies_" + strconv.Itoa(i)
			tracker.GetSpeciesStatus(speciesName, baseTime.Add(time.Duration(i)*time.Second))
		}

		initialCacheSize := tracker.CacheSizeForTesting()
		t.Logf("Pre-populated cache with %d entries, current size: %d", cacheEntries, initialCacheSize)

		// Measure cleanup performance
		cleanupTime := baseTime.Add(2 * time.Hour) // Force expiration
		start := time.Now()
		tracker.ForceCleanupForTesting(cleanupTime)
		cleanupDuration := time.Since(start)
		
		finalCacheSize := tracker.CacheSizeForTesting()
		cleanupMs := float64(cleanupDuration.Nanoseconds()) / 1e6
		
		t.Logf("Cache cleanup: %v (%.2fms), entries before: %d, after: %d", 
			cleanupDuration, cleanupMs, initialCacheSize, finalCacheSize)
		
		// Use configurable threshold
		if cleanupMs > float64(thresholds.maxCacheCleanupMs) {
			t.Logf("WARNING: Cache cleanup %.2fms exceeds threshold %dms (configurable via BIRDNET_TEST_MAX_CLEANUP_MS)", 
				cleanupMs, thresholds.maxCacheCleanupMs)
		} else {
			t.Logf("✓ Cache cleanup within threshold of %dms", thresholds.maxCacheCleanupMs)
		}
		
		// Verify cleanup was effective
		assert.Less(t, finalCacheSize, initialCacheSize, "Cache cleanup should reduce cache size")
		assert.LessOrEqual(t, finalCacheSize, 1000, "Cache should respect size limits")

		ds.AssertExpectations(t)
	})

	t.Run("memory_efficiency_under_load", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
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
		require.NotNil(t, tracker)
		require.NoError(t, tracker.InitFromDatabase())

		// Measure memory usage before load
		var m1, m2 runtime.MemStats
		runtime.GC()
		runtime.ReadMemStats(&m1)

		// Apply sustained load
		operations := thresholds.operationsPerTest
		speciesVariety := 200 // Limit variety to test cache efficiency
		
		for i := 0; i < operations; i++ {
			speciesName := "MemoryTestSpecies_" + strconv.Itoa(i%speciesVariety)
			opTime := baseTime.Add(time.Duration(i) * time.Microsecond)
			
			// Mix operations to stress all code paths
			switch i % 4 {
			case 0:
				tracker.CheckAndUpdateSpecies(speciesName, opTime)
			case 1:
				tracker.GetSpeciesStatus(speciesName, opTime)
			case 2:
				tracker.UpdateSpecies(speciesName, opTime)
			case 3:
				// Periodic cleanup to maintain reasonable memory usage
				if i%1000 == 0 {
					tracker.ForceCleanupForTesting(opTime)
				}
			}
		}

		// Measure memory after load
		runtime.GC()
		runtime.ReadMemStats(&m2)
		
		memIncreaseMB := float64(int64(m2.Alloc)-int64(m1.Alloc)) / (1024 * 1024)
		
		t.Logf("Memory efficiency test: %d operations, memory increase: %.2f MB", 
			operations, memIncreaseMB)
		
		// Use configurable threshold
		if memIncreaseMB > float64(thresholds.maxMemoryIncreaseMB) {
			t.Logf("WARNING: Memory increase %.2fMB exceeds threshold %dMB (configurable via BIRDNET_TEST_MAX_MEMORY_MB)", 
				memIncreaseMB, thresholds.maxMemoryIncreaseMB)
		} else {
			t.Logf("✓ Memory increase within threshold of %dMB", thresholds.maxMemoryIncreaseMB)
		}

		// Verify cache is reasonably sized
		finalCacheSize := tracker.CacheSizeForTesting()
		t.Logf("Final cache size: %d entries", finalCacheSize)
		assert.LessOrEqual(t, finalCacheSize, 1000, "Cache should be bounded")

		ds.AssertExpectations(t)
	})
}

// BenchmarkPerformanceEdges provides detailed benchmarks for performance edge cases
func BenchmarkPerformanceEdges(b *testing.B) {
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
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
	_ = tracker.InitFromDatabase()
	baseTime := time.Now()

	b.Run("SingleOperationLatency", func(b *testing.B) {
		speciesName := "BenchmarkSpecies"
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracker.GetSpeciesStatus(speciesName, baseTime.Add(time.Duration(i)*time.Nanosecond))
		}
	})

	b.Run("CacheHitPerformance", func(b *testing.B) {
		// Pre-populate cache
		speciesName := "CachedSpecies"
		tracker.GetSpeciesStatus(speciesName, baseTime)
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracker.GetSpeciesStatus(speciesName, baseTime)
		}
	})

	b.Run("CacheCleanupPerformance", func(b *testing.B) {
		// Pre-populate with many entries
		for i := 0; i < 2000; i++ {
			tracker.GetSpeciesStatus("CleanupSpecies_"+strconv.Itoa(i), baseTime)
		}
		
		b.ResetTimer()
		for i := 0; i < b.N; i++ {
			tracker.ForceCleanupForTesting(baseTime.Add(time.Duration(i) * time.Hour))
		}
	})

	b.Run("ConcurrentOperations", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				speciesName := "ConcurrentSpecies_" + strconv.Itoa(i%10)
				opTime := baseTime.Add(time.Duration(i) * time.Nanosecond)
				
				switch i % 3 {
				case 0:
					tracker.CheckAndUpdateSpecies(speciesName, opTime)
				case 1:
					tracker.GetSpeciesStatus(speciesName, opTime)
				case 2:
					tracker.UpdateSpecies(speciesName, opTime)
				}
				i++
			}
		})
	})
}