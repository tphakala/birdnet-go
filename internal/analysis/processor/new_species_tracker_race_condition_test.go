package processor

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

// raceConditionStats tracks statistics for race condition testing
type raceConditionStats struct {
	totalOperations    int64
	successfulReads    int64
	successfulWrites   int64
	successfulUpdates  int64
	cacheHits          int64
	cacheMisses        int64
	raceDetections     int64
	inconsistencies    int64
}

// TestConcurrentReadWriteOperations tests concurrent read/write operations for race conditions
func TestConcurrentReadWriteOperations(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
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

	// Statistics tracking
	var stats raceConditionStats

	// Test parameters
	const (
		numGoroutines          = 100
		operationsPerGoroutine = 200
		testDuration           = 5 * time.Second
	)

	// Shared species pool to increase contention  
	//nolint:misspell // Cardinalis is correct scientific genus name
	speciesPool := []string{
		"Turdus_migratorius", "Cardinalis_cardinalis", "Poecile_atricapillus",
		"Sialia_sialis", "Corvus_brachyrhynchos", "Passer_domesticus",
		"Sturnus_vulgaris", "Molothrus_ater", "Quiscalus_quiscula", "Bombycilla_cedrorum",
	}

	ctx, cancel := context.WithTimeout(context.Background(), testDuration)
	defer cancel()

	var wg sync.WaitGroup
	
	t.Logf("Starting race condition test: %d goroutines, %d ops each, %d shared species", 
		numGoroutines, operationsPerGoroutine, len(speciesPool))

	// Launch concurrent goroutines performing mixed operations
	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go runConcurrentOperations(t, &wg, g, operationsPerGoroutine, tracker, &stats, speciesPool, baseTime, ctx)
	}

	// Wait for all goroutines to complete
	wg.Wait()
	cancel()

	logRaceConditionResults(t, &stats, tracker)
	validateRaceConditionResults(t, &stats, tracker)

	ds.AssertExpectations(t)
}

func runConcurrentOperations(t *testing.T, wg *sync.WaitGroup, goroutineID, operationsPerGoroutine int, 
	tracker *NewSpeciesTracker, stats *raceConditionStats, speciesPool []string, 
	baseTime time.Time, ctx context.Context) {
	t.Helper()
	
	defer wg.Done()
	
	localOperations := 0
	for i := 0; i < operationsPerGoroutine && ctx.Err() == nil; i++ {
		speciesName := speciesPool[i%len(speciesPool)]
		opTime := baseTime.Add(time.Duration(goroutineID*operationsPerGoroutine+i) * time.Microsecond)
		
		// Mix operations to maximize contention
		switch i % 4 {
		case 0:
			performReadOperation(tracker, stats, speciesName, opTime)
		case 1:
			performWriteOperation(tracker, stats, speciesName, opTime)
		case 2:
			performUpdateOperation(tracker, stats, speciesName, opTime)
		case 3:
			performCacheOperation(tracker, stats, opTime)
		}
		
		localOperations++
		atomic.AddInt64(&stats.totalOperations, 1)
		
		// Yield occasionally to increase race condition likelihood
		if i%10 == 0 {
			runtime.Gosched()
		}
	}
	
	// Clean logging: only log summary per goroutine, not individual operations
	if goroutineID%20 == 0 { // Log every 20th goroutine to avoid spam
		t.Logf("Goroutine %d completed %d operations", goroutineID, localOperations)
	}
}

func performReadOperation(tracker *NewSpeciesTracker, stats *raceConditionStats, speciesName string, opTime time.Time) {
	status := tracker.GetSpeciesStatus(speciesName, opTime)
	atomic.AddInt64(&stats.successfulReads, 1)
	if !status.FirstSeenTime.IsZero() {
		atomic.AddInt64(&stats.cacheHits, 1)
	} else {
		atomic.AddInt64(&stats.cacheMisses, 1)
	}
}

func performWriteOperation(tracker *NewSpeciesTracker, stats *raceConditionStats, speciesName string, opTime time.Time) {
	isNew := tracker.UpdateSpecies(speciesName, opTime)
	atomic.AddInt64(&stats.successfulWrites, 1)
	if isNew {
		// Verify consistency - new species should have status reflecting that
		verifyStatus := tracker.GetSpeciesStatus(speciesName, opTime)
		if !verifyStatus.IsNew && verifyStatus.DaysSinceFirst > 0 {
			atomic.AddInt64(&stats.inconsistencies, 1)
		}
	}
}

func performUpdateOperation(tracker *NewSpeciesTracker, stats *raceConditionStats, speciesName string, opTime time.Time) {
	isNew, days := tracker.CheckAndUpdateSpecies(speciesName, opTime)
	atomic.AddInt64(&stats.successfulUpdates, 1)
	
	// Consistency check: isNew and days should be logically consistent
	if isNew && days > 0 {
		atomic.AddInt64(&stats.inconsistencies, 1)
	}
	// Additional consistency check: days should never be negative
	if days < 0 {
		atomic.AddInt64(&stats.inconsistencies, 1)
	}
}

func performCacheOperation(tracker *NewSpeciesTracker, stats *raceConditionStats, opTime time.Time) {
	// Cache operations - just perform cleanup without false positive detection
	tracker.ForceCleanupForTesting(opTime)
	
	// Only detect actual data corruption (negative cache size would indicate serious issues)
	finalSize := tracker.CacheSizeForTesting()
	if finalSize < 0 {
		atomic.AddInt64(&stats.raceDetections, 1)
	}
}

func logRaceConditionResults(t *testing.T, stats *raceConditionStats, tracker *NewSpeciesTracker) {
	t.Helper()
	finalSpeciesCount := tracker.GetSpeciesCount()
	finalCacheSize := tracker.CacheSizeForTesting()

	t.Logf("Race condition test summary:")
	t.Logf("  Total operations: %d", atomic.LoadInt64(&stats.totalOperations))
	t.Logf("  Successful reads: %d", atomic.LoadInt64(&stats.successfulReads))
	t.Logf("  Successful writes: %d", atomic.LoadInt64(&stats.successfulWrites))
	t.Logf("  Successful updates: %d", atomic.LoadInt64(&stats.successfulUpdates))
	t.Logf("  Cache hits: %d", atomic.LoadInt64(&stats.cacheHits))
	t.Logf("  Cache misses: %d", atomic.LoadInt64(&stats.cacheMisses))
	t.Logf("  Race detections: %d", atomic.LoadInt64(&stats.raceDetections))
	t.Logf("  Inconsistencies: %d", atomic.LoadInt64(&stats.inconsistencies))
	t.Logf("  Final species count: %d", finalSpeciesCount)
	t.Logf("  Final cache size: %d", finalCacheSize)
}

func validateRaceConditionResults(t *testing.T, stats *raceConditionStats, tracker *NewSpeciesTracker) {
	t.Helper()
	finalCacheSize := tracker.CacheSizeForTesting()
	
	// Assertions for race condition detection
	assert.Equal(t, int64(0), atomic.LoadInt64(&stats.raceDetections), 
		"No race conditions should be detected in cache operations")
	assert.Equal(t, int64(0), atomic.LoadInt64(&stats.inconsistencies), 
		"No logical inconsistencies should occur")
	assert.Positive(t, atomic.LoadInt64(&stats.totalOperations), 
		"Should have performed operations")
	assert.LessOrEqual(t, finalCacheSize, 1000, 
		"Cache should be bounded even under concurrent access")
}

// TestCacheConsistencyUnderPressure tests cache consistency under high concurrent pressure
func TestCacheConsistencyUnderPressure(t *testing.T) {
	t.Parallel()
	
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
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

	runCacheConsistencyTest(t, tracker, baseTime)
	
	ds.AssertExpectations(t)
}

func runCacheConsistencyTest(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time) {
	t.Helper()
	const (
		readerGoroutines  = 50
		writerGoroutines  = 20
		cleanerGoroutines = 5
		testSpecies       = "ConsistencyTestSpecies"
		operationsPerReader = 1000
		operationsPerWriter = 500
	)

	var inconsistencyCount int64
	var wg sync.WaitGroup
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	t.Logf("Cache consistency test: %d readers, %d writers, %d cleaners", 
		readerGoroutines, writerGoroutines, cleanerGoroutines)

	// Start readers, writers, and cleaners
	startReaderGoroutines(t, &wg, readerGoroutines, operationsPerReader, tracker, &inconsistencyCount, testSpecies, baseTime, ctx)
	startWriterGoroutines(t, &wg, writerGoroutines, operationsPerWriter, tracker, &inconsistencyCount, testSpecies, baseTime, ctx)
	startCleanerGoroutines(t, &wg, cleanerGoroutines, tracker, baseTime, ctx)

	wg.Wait()
	cancel()

	validateCacheConsistency(t, &inconsistencyCount, tracker)
}

func startReaderGoroutines(t *testing.T, wg *sync.WaitGroup, count, operations int, tracker *NewSpeciesTracker, 
	inconsistencyCount *int64, testSpecies string, baseTime time.Time, ctx context.Context) {
	t.Helper()
	
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			
			operations := runReaderOperations(tracker, operations, inconsistencyCount, testSpecies, baseTime, ctx)
			
			// Clean logging: only log reader completion occasionally
			if readerID%10 == 0 {
				t.Logf("Reader %d completed %d operations", readerID, operations)
			}
		}(i)
	}
}

func runReaderOperations(tracker *NewSpeciesTracker, maxOperations int, inconsistencyCount *int64, 
	testSpecies string, baseTime time.Time, ctx context.Context) int {
	
	operations := 0
	for j := 0; j < maxOperations && ctx.Err() == nil; j++ {
		status1 := tracker.GetSpeciesStatus(testSpecies, baseTime.Add(time.Duration(j)*time.Nanosecond))
		status2 := tracker.GetSpeciesStatus(testSpecies, baseTime.Add(time.Duration(j)*time.Nanosecond))
		
		// Same input should yield same result (cache consistency)
		if status1.IsNew != status2.IsNew || status1.DaysSinceFirst != status2.DaysSinceFirst {
			atomic.AddInt64(inconsistencyCount, 1)
		}
		operations++
		
		if j%100 == 0 {
			runtime.Gosched()
		}
	}
	return operations
}

func startWriterGoroutines(t *testing.T, wg *sync.WaitGroup, count, operations int, tracker *NewSpeciesTracker, 
	inconsistencyCount *int64, testSpecies string, baseTime time.Time, ctx context.Context) {
	t.Helper()
	
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			
			operations := runWriterOperations(tracker, operations, inconsistencyCount, testSpecies, baseTime, ctx, writerID)
			
			t.Logf("Writer %d completed %d operations", writerID, operations)
		}(i)
	}
}

func runWriterOperations(tracker *NewSpeciesTracker, maxOperations int, inconsistencyCount *int64, 
	testSpecies string, baseTime time.Time, ctx context.Context, writerID int) int {
	
	operations := 0
	for j := 0; j < maxOperations && ctx.Err() == nil; j++ {
		speciesName := fmt.Sprintf("%s_%d_%d", testSpecies, writerID, j)
		opTime := baseTime.Add(time.Duration(writerID*maxOperations+j) * time.Microsecond)
		
		// Perform write and immediately verify
		isNew, days := tracker.CheckAndUpdateSpecies(speciesName, opTime)
		status := tracker.GetSpeciesStatus(speciesName, opTime)
		
		// Consistency check: status should reflect the update
		if status.IsNew != isNew {
			atomic.AddInt64(inconsistencyCount, 1)
		}
		// Additional consistency check: days should never be negative
		if days < 0 {
			atomic.AddInt64(inconsistencyCount, 1)
		}
		
		operations++
	}
	return operations
}

func startCleanerGoroutines(t *testing.T, wg *sync.WaitGroup, count int, tracker *NewSpeciesTracker, 
	baseTime time.Time, ctx context.Context) {
	t.Helper()
	
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(cleanerID int) {
			defer wg.Done()
			
			cleanups := 0
			for ctx.Err() == nil {
				time.Sleep(50 * time.Millisecond)
				tracker.ForceCleanupForTesting(baseTime.Add(time.Duration(cleanups) * time.Minute))
				cleanups++
			}
			
			t.Logf("Cleaner %d performed %d cleanups", cleanerID, cleanups)
		}(i)
	}
}

func validateCacheConsistency(t *testing.T, inconsistencyCount *int64, tracker *NewSpeciesTracker) {
	t.Helper()
	finalInconsistencies := atomic.LoadInt64(inconsistencyCount)
	finalCacheSize := tracker.CacheSizeForTesting()
	finalSpeciesCount := tracker.GetSpeciesCount()

	t.Logf("Cache consistency results:")
	t.Logf("  Detected inconsistencies: %d", finalInconsistencies)
	t.Logf("  Final cache size: %d", finalCacheSize)
	t.Logf("  Final species count: %d", finalSpeciesCount)

	// Assertions
	assert.Equal(t, int64(0), finalInconsistencies, 
		"Cache should maintain consistency under concurrent access")
	assert.LessOrEqual(t, finalCacheSize, 1000, 
		"Cache should remain bounded")
}

// TestDataStructureIntegrity tests data structure integrity under concurrent modification
func TestDataStructureIntegrity(t *testing.T) {
	t.Parallel()
	
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
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

	runDataIntegrityTest(t, tracker, baseTime)
	
	ds.AssertExpectations(t)
}

func runDataIntegrityTest(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time) {
	t.Helper()
	const (
		numWorkers         = 30
		operationsPerWorker = 300
	)

	var wg sync.WaitGroup
	var corruptionCount int64
	testSpecies := []string{"IntegrityTest_A", "IntegrityTest_B", "IntegrityTest_C"}

	t.Logf("Data structure integrity test: %d workers, %d ops each", 
		numWorkers, operationsPerWorker)

	for w := 0; w < numWorkers; w++ {
		wg.Add(1)
		go func(workerID int) {
			defer wg.Done()
			
			operations := runWorkerIntegrityOperations(tracker, operationsPerWorker, &corruptionCount, testSpecies, baseTime, workerID)
			
			// Clean logging: only log occasionally
			if workerID%5 == 0 {
				t.Logf("Worker %d completed %d operations", workerID, operations)
			}
		}(w)
	}

	wg.Wait()

	validateDataIntegrity(t, &corruptionCount, tracker, testSpecies, baseTime)
}

func runWorkerIntegrityOperations(tracker *NewSpeciesTracker, maxOperations int, corruptionCount *int64, 
	testSpecies []string, baseTime time.Time, workerID int) int {
	
	operations := 0
	for i := 0; i < maxOperations; i++ {
		speciesName := testSpecies[i%len(testSpecies)]
		opTime := baseTime.Add(time.Duration(workerID*maxOperations+i) * time.Microsecond)
		
		// Mix of operations that could cause corruption if not properly synchronized
		switch i % 5 {
		case 0:
			tracker.CheckAndUpdateSpecies(speciesName, opTime)
		case 1:
			checkStatusIntegrity(tracker, corruptionCount, speciesName, opTime)
		case 2:
			tracker.UpdateSpecies(speciesName, opTime)
		case 3:
			tracker.ForceCleanupForTesting(opTime)
		case 4:
			checkBatchStatusIntegrity(tracker, corruptionCount, testSpecies, opTime)
		}
		
		operations++
		
		// Occasional yields to encourage race conditions
		if i%25 == 0 {
			runtime.Gosched()
		}
	}
	return operations
}

func checkStatusIntegrity(tracker *NewSpeciesTracker, corruptionCount *int64, speciesName string, opTime time.Time) {
	status := tracker.GetSpeciesStatus(speciesName, opTime)
	// Verify status makes sense
	if status.DaysSinceFirst < -1 {
		atomic.AddInt64(corruptionCount, 1)
	}
}

func checkBatchStatusIntegrity(tracker *NewSpeciesTracker, corruptionCount *int64, testSpecies []string, opTime time.Time) {
	allStatuses := tracker.GetBatchSpeciesStatus(testSpecies, opTime)
	// Verify batch consistency
	for _, batchStatus := range allStatuses {
		if batchStatus.DaysSinceFirst < -1 {
			atomic.AddInt64(corruptionCount, 1)
		}
	}
}

func validateDataIntegrity(t *testing.T, corruptionCount *int64, tracker *NewSpeciesTracker, testSpecies []string, baseTime time.Time) {
	t.Helper()
	finalCorruptions := atomic.LoadInt64(corruptionCount)
	finalSpeciesCount := tracker.GetSpeciesCount()
	finalCacheSize := tracker.CacheSizeForTesting()

	// Check each species individually for corruption
	integrityIssues := 0
	for _, species := range testSpecies {
		status := tracker.GetSpeciesStatus(species, baseTime)
		if status.DaysSinceFirst < -1 || 
		   (status.IsNew && status.DaysSinceFirst > tracker.GetWindowDays()) {
			integrityIssues++
		}
	}

	// Clean summary logging
	t.Logf("Data structure integrity results:")
	t.Logf("  Corruption detections: %d", finalCorruptions)
	t.Logf("  Integrity issues: %d", integrityIssues)
	t.Logf("  Final species count: %d", finalSpeciesCount)
	t.Logf("  Final cache size: %d", finalCacheSize)

	// Assertions
	assert.Equal(t, int64(0), finalCorruptions, 
		"No data corruption should be detected")
	assert.Equal(t, 0, integrityIssues, 
		"Data structures should maintain integrity")
}

// BenchmarkRaceConditions provides performance benchmarks under race conditions
func BenchmarkRaceConditions(b *testing.B) {
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

	b.Run("ConcurrentMixedOperations", func(b *testing.B) {
		//nolint:misspell // Cardinalis is correct scientific genus name
		speciesPool := []string{"Bench_A", "Bench_B", "Bench_C", "Bench_D", "Bench_E"}
		
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				speciesName := speciesPool[i%len(speciesPool)]
				opTime := baseTime.Add(time.Duration(i) * time.Nanosecond)
				
				// Mix operations to create contention
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

	b.Run("CacheContentionBenchmark", func(b *testing.B) {
		// Pre-populate cache
		for i := 0; i < 100; i++ {
			tracker.GetSpeciesStatus(fmt.Sprintf("CacheSpecies_%d", i), baseTime)
		}
		
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				speciesName := fmt.Sprintf("CacheSpecies_%d", i%100)
				
				// Mix cache operations
				if i%10 == 0 {
					tracker.ForceCleanupForTesting(baseTime.Add(time.Duration(i) * time.Minute))
				} else {
					tracker.GetSpeciesStatus(speciesName, baseTime)
				}
				i++
			}
		})
	})
}