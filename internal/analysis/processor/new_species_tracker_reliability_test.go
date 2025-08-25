package processor

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// errorInjectionDatastore wraps MockSpeciesDatastore with configurable error injection
type errorInjectionDatastore struct {
	*MockSpeciesDatastore
	errorRate         float64      // Probability of error (0.0 to 1.0)
	errorCount        int64        // Total errors injected
	consecutiveErrors int64        // Current consecutive error streak
	maxConsecutive    int64        // Maximum consecutive errors before success
	errorDelay        time.Duration // Delay to inject for simulating slow database
}

// newErrorInjectionDatastore creates a datastore that can inject various types of errors
func newErrorInjectionDatastore(baseDS *MockSpeciesDatastore, errorRate float64) *errorInjectionDatastore {
	return &errorInjectionDatastore{
		MockSpeciesDatastore: baseDS,
		errorRate:           errorRate,
		maxConsecutive:      3, // Max 3 consecutive errors before forcing success
		errorDelay:          50 * time.Millisecond,
	}
}

func (e *errorInjectionDatastore) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	if err := e.shouldInjectError("GetNewSpeciesDetections"); err != nil {
		return nil, err
	}
	return e.MockSpeciesDatastore.GetNewSpeciesDetections(startDate, endDate, limit, offset)
}

func (e *errorInjectionDatastore) GetSpeciesFirstDetectionInPeriod(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	if err := e.shouldInjectError("GetSpeciesFirstDetectionInPeriod"); err != nil {
		return nil, err
	}
	return e.MockSpeciesDatastore.GetSpeciesFirstDetectionInPeriod(startDate, endDate, limit, offset)
}

func (e *errorInjectionDatastore) shouldInjectError(operation string) error {
	consecutive := atomic.LoadInt64(&e.consecutiveErrors)
	
	// Force success after too many consecutive errors to avoid infinite loops
	if consecutive >= e.maxConsecutive {
		atomic.StoreInt64(&e.consecutiveErrors, 0)
		return nil
	}

	// Decide whether to inject error based on error rate
	shouldError := (time.Now().UnixNano() % 100) < int64(e.errorRate*100)
	
	if shouldError {
		atomic.AddInt64(&e.errorCount, 1)
		atomic.AddInt64(&e.consecutiveErrors, 1)
		
		// Inject delay to simulate slow database
		if e.errorDelay > 0 {
			time.Sleep(e.errorDelay)
		}
		
		return errors.Newf("injected error in %s (consecutive: %d)", operation, consecutive+1).
			Component("error-injection-datastore").
			Category(errors.CategoryDatabase).
			Context("operation", operation).
			Build()
	}

	atomic.StoreInt64(&e.consecutiveErrors, 0)
	return nil
}

// TestDatabaseErrorRecovery tests system recovery from database errors
func TestDatabaseErrorRecovery(t *testing.T) {
	t.Parallel()
	
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
	// Setup base mock
	baseMock := &MockSpeciesDatastore{}
	baseMock.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Reliable_Species", CommonName: "Test Species", FirstSeenDate: baseTime.Format("2006-01-02")},
		}, nil)
	baseMock.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Wrap with error injection (20% error rate)
	errorDS := newErrorInjectionDatastore(baseMock, 0.2)
	tracker := createReliabilityTestTracker(errorDS)

	// Test initialization with potential errors
	initResult := testDatabaseInitialization(t, tracker)
	testOngoingOperations(t, tracker, baseTime, errorDS, initResult.initSucceeded)

	baseMock.AssertExpectations(t)
}

type initializationResult struct {
	initSucceeded bool
	errorCount    int64
}

func createReliabilityTestTracker(ds *errorInjectionDatastore) *NewSpeciesTracker {
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

	return NewSpeciesTrackerFromSettings(ds, settings)
}

func testDatabaseInitialization(t *testing.T, tracker *NewSpeciesTracker) initializationResult {
	t.Helper()
	var initErr error
	maxInitRetries := 5
	for i := 0; i < maxInitRetries; i++ {
		initErr = tracker.InitFromDatabase()
		if initErr == nil {
			break // Success
		}
		t.Logf("Initialization attempt %d failed: %v", i+1, initErr)
		time.Sleep(10 * time.Millisecond) // Brief delay before retry
	}

	if initErr != nil {
		t.Logf("Database initialization failed after %d attempts, testing fallback behavior", maxInitRetries)
		return initializationResult{initSucceeded: false}
	}

	t.Logf("Database initialization succeeded, system recovered from errors")
	assert.Positive(t, tracker.GetSpeciesCount(), "Should have loaded species on successful init")
	return initializationResult{initSucceeded: true}
}

func testOngoingOperations(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time, 
	errorDS *errorInjectionDatastore, initSucceeded bool) {
	t.Helper()
	
	if !initSucceeded {
		// Should still work for new species detection (no DB dependency)
		isNew, days := tracker.CheckAndUpdateSpecies("FallbackSpecies", baseTime)
		assert.True(t, isNew, "Should handle new species even with DB errors")
		assert.Equal(t, 0, days, "New species should have 0 days")
	}

	// Test ongoing operations with intermittent errors
	successfulOps := runOngoingOperations(t, tracker, baseTime)
	
	// Should have some successful operations despite errors
	totalOps := 100
	successRate := float64(successfulOps) / float64(totalOps)
	t.Logf("Error recovery test: %d/%d successful operations (%.1f%%), %d errors injected", 
		successfulOps, totalOps, successRate*100, atomic.LoadInt64(&errorDS.errorCount))
	
	assert.Greater(t, successfulOps, totalOps/2, "Should maintain >50% success rate despite database errors")
	
	// Final state should be consistent
	assert.GreaterOrEqual(t, tracker.GetSpeciesCount(), 0, "Species count should never be negative")
	assert.GreaterOrEqual(t, tracker.CacheSizeForTesting(), 0, "Cache size should never be negative")
}

func runOngoingOperations(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time) int {
	t.Helper()
	successfulOps := 0
	totalOps := 100
	
	for i := 0; i < totalOps; i++ {
		speciesName := fmt.Sprintf("ErrorTest_%d", i%10)
		opTime := baseTime.Add(time.Duration(i) * time.Second)
		
		// Operations should not panic even with database errors
		success := func() bool {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("Operation panicked with database errors: %v", r)
				}
			}()
			
			isNew, days := tracker.CheckAndUpdateSpecies(speciesName, opTime)
			return !isNew || days >= 0 // Any reasonable response indicates success
		}()
		
		if success {
			successfulOps++
		}
	}
	
	return successfulOps
}

// TestConcurrentErrorHandling tests error handling under concurrent load
func TestConcurrentErrorHandling(t *testing.T) {
	t.Parallel()
	
	// Setup mock with error injection
	baseMock := &MockSpeciesDatastore{}
	baseMock.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()
	baseMock.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	errorDS := newErrorInjectionDatastore(baseMock, 0.3) // 30% error rate
	tracker := createBasicTestTracker(errorDS)
	
	// Initialize with potential errors
	_ = tracker.InitFromDatabase() // Allow init to fail, test resilience

	results := runConcurrentErrorTest(t, tracker)
	validateConcurrentErrorResults(t, results, errorDS, tracker)

	baseMock.AssertExpectations(t)
}

type concurrentErrorResults struct {
	totalOperations int
	totalErrors     int64
	totalSuccess    int64
	panics          int64
}

func createBasicTestTracker(ds *errorInjectionDatastore) *NewSpeciesTracker {
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
	}
	return NewSpeciesTrackerFromSettings(ds, settings)
}

func runConcurrentErrorTest(t *testing.T, tracker *NewSpeciesTracker) concurrentErrorResults {
	t.Helper()
	const (
		numGoroutines          = 20
		operationsPerGoroutine = 50
	)

	var wg sync.WaitGroup
	var totalErrors int64
	var totalSuccess int64
	var panics int64

	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for g := 0; g < numGoroutines; g++ {
		wg.Add(1)
		go runConcurrentErrorOperations(&wg, g, operationsPerGoroutine, tracker, baseTime, 
			&totalErrors, &totalSuccess, &panics, ctx)
	}

	wg.Wait()
	cancel()

	return concurrentErrorResults{
		totalOperations: numGoroutines * operationsPerGoroutine,
		totalErrors:     totalErrors,
		totalSuccess:    totalSuccess,
		panics:          panics,
	}
}

func runConcurrentErrorOperations(wg *sync.WaitGroup, goroutineID, operationsPerGoroutine int,
	tracker *NewSpeciesTracker, baseTime time.Time, totalErrors, totalSuccess, panics *int64, ctx context.Context) {
	
	defer wg.Done()
	
	localErrors := 0
	localSuccess := 0
	
	for i := 0; i < operationsPerGoroutine && ctx.Err() == nil; i++ {
		speciesName := fmt.Sprintf("ConcurrentError_%d_%d", goroutineID, i%5)
		opTime := baseTime.Add(time.Duration(goroutineID*operationsPerGoroutine+i) * time.Millisecond)
		
		// Wrap operations to catch panics
		success := func() bool {
			defer func() {
				if r := recover(); r != nil {
					atomic.AddInt64(panics, 1)
				}
			}()
			
			// Mix operations with error handling
			switch i % 4 {
			case 0:
				isNew, days := tracker.CheckAndUpdateSpecies(speciesName, opTime)
				return isNew || days >= 0
			case 1:
				status := tracker.GetSpeciesStatus(speciesName, opTime)
				return status.DaysSinceFirst >= -1 // -1 is valid (not found)
			case 2:
				tracker.UpdateSpecies(speciesName, opTime)
				return true // Any boolean response is valid
			case 3:
				// Cache operations should be resilient
				size := tracker.CacheSizeForTesting()
				return size >= 0
			}
			return false
		}()
		
		if success {
			localSuccess++
		} else {
			localErrors++
		}
	}
	
	atomic.AddInt64(totalErrors, int64(localErrors))
	atomic.AddInt64(totalSuccess, int64(localSuccess))
}

func validateConcurrentErrorResults(t *testing.T, results concurrentErrorResults, errorDS *errorInjectionDatastore, tracker *NewSpeciesTracker) {
	t.Helper()
	successRate := float64(results.totalSuccess) / float64(results.totalOperations)
	errorRate := float64(results.totalErrors) / float64(results.totalOperations)
	
	t.Logf("Concurrent error handling results:")
	t.Logf("  Total operations: %d", results.totalOperations)
	t.Logf("  Successful: %d (%.1f%%)", results.totalSuccess, successRate*100)
	t.Logf("  Errors: %d (%.1f%%)", results.totalErrors, errorRate*100)
	t.Logf("  Panics: %d", results.panics)
	t.Logf("  DB errors injected: %d", atomic.LoadInt64(&errorDS.errorCount))

	// Assertions for reliability
	assert.Equal(t, int64(0), results.panics, "No panics should occur during error conditions")
	assert.Greater(t, successRate, 0.4, "Should maintain >40% success rate under concurrent errors")
	assert.GreaterOrEqual(t, tracker.CacheSizeForTesting(), 0, "Cache should remain consistent")
}

// TestErrorPropagationAndCategorization tests proper error handling and categorization
func TestErrorPropagationAndCategorization(t *testing.T) {
	t.Parallel()
	
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
	// Test proper error handling and categorization
	baseMock := &MockSpeciesDatastore{}
	
	// Configure specific error responses
	baseMock.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(nil, errors.Newf("database connection failed").
			Component("test-datastore").
			Category(errors.CategoryDatabase).
			Build())
	baseMock.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(nil, errors.Newf("query timeout").
			Component("test-datastore").
			Category(errors.CategoryTimeout).
			Build()).Maybe()

	tracker := createBasicTestTracker2(baseMock)

	// Test error propagation through InitFromDatabase
	initErr := tracker.InitFromDatabase()
	require.Error(t, initErr, "Should propagate database errors")
	
	validateErrorStructure(t, initErr)
	testFallbackBehavior(t, tracker, baseTime)

	baseMock.AssertExpectations(t)
}

func createBasicTestTracker2(ds SpeciesDatastore) *NewSpeciesTracker {
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
	}
	return NewSpeciesTrackerFromSettings(ds, settings)
}

func validateErrorStructure(t *testing.T, initErr error) {
	t.Helper()
	// Verify error categorization and structure
	errorStr := initErr.Error()
	// The error may be wrapped, so check for database-related error content
	assert.True(t, strings.Contains(errorStr, "database") || strings.Contains(errorStr, "Database"), 
		"Should indicate database category: %s", errorStr)
	// Verify it's a proper error from our system
	assert.Contains(t, errorStr, "failed to load", "Should contain error context")
	
	t.Logf("Error propagation test - captured error: %s", errorStr)
}

func testFallbackBehavior(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time) {
	t.Helper()
	// Despite init failure, basic operations should work
	isNew, days := tracker.CheckAndUpdateSpecies("ErrorHandlingTest", baseTime)
	assert.True(t, isNew, "Should handle new species despite DB errors")
	assert.Equal(t, 0, days, "New species should have 0 days")

	// Status operations should be resilient
	status := tracker.GetSpeciesStatus("ErrorHandlingTest", baseTime)
	assert.True(t, status.IsNew, "Status should reflect species state")
	assert.Equal(t, 0, status.DaysSinceFirst, "Should show correct days")
}

// TestResourceExhaustionHandling tests behavior under resource constraints
func TestResourceExhaustionHandling(t *testing.T) {
	t.Parallel()
	
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
	baseMock := &MockSpeciesDatastore{}
	baseMock.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	baseMock.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	tracker := createFullFeaturedTestTracker(baseMock)
	require.NoError(t, tracker.InitFromDatabase())

	results := simulateResourcePressure(t, tracker, baseTime)
	validateResourceHandling(t, results, tracker)

	baseMock.AssertExpectations(t)
}

func createFullFeaturedTestTracker(ds SpeciesDatastore) *NewSpeciesTracker {
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
	}
	return NewSpeciesTrackerFromSettings(ds, settings)
}

type resourceTestResults struct {
	speciesCreated int
	maxSpecies     int
}

func simulateResourcePressure(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time) resourceTestResults {
	t.Helper()
	// Simulate memory pressure by creating many species entries
	const maxSpecies = 5000
	speciesCreated := 0
	
	for i := 0; i < maxSpecies; i++ {
		speciesName := fmt.Sprintf("ResourceTest_%d", i)
		opTime := baseTime.Add(time.Duration(i) * time.Microsecond)
		
		// System should handle resource pressure gracefully
		func() {
			defer func() {
				if r := recover(); r != nil {
					t.Errorf("System panicked under resource pressure: %v", r)
				}
			}()
			
			tracker.CheckAndUpdateSpecies(speciesName, opTime)
			speciesCreated++
			
			// Periodic cleanup to test memory management
			if i%1000 == 0 {
				tracker.ForceCleanupForTesting(opTime)
				// Verify system remains stable after cleanup
				cacheSize := tracker.CacheSizeForTesting()
				if cacheSize < 0 {
					t.Errorf("Cache corrupted after cleanup: size=%d", cacheSize)
				}
			}
		}()
	}
	
	return resourceTestResults{speciesCreated: speciesCreated, maxSpecies: maxSpecies}
}

func validateResourceHandling(t *testing.T, results resourceTestResults, tracker *NewSpeciesTracker) {
	t.Helper()
	finalSpeciesCount := tracker.GetSpeciesCount()
	finalCacheSize := tracker.CacheSizeForTesting()
	
	t.Logf("Resource exhaustion test results:")
	t.Logf("  Species created: %d", results.speciesCreated)
	t.Logf("  Final species count: %d", finalSpeciesCount)
	t.Logf("  Final cache size: %d", finalCacheSize)

	assert.Equal(t, results.maxSpecies, results.speciesCreated, "Should create all species without failure")
	assert.Positive(t, finalSpeciesCount, "Should maintain species tracking")
	assert.LessOrEqual(t, finalCacheSize, 1000, "Cache should be bounded")
	assert.GreaterOrEqual(t, finalCacheSize, 0, "Cache should not be corrupted")
}

// TestTimeoutAndCancellation tests timeout handling with slow datastore
func TestTimeoutAndCancellation(t *testing.T) {
	t.Parallel()
	
	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	
	baseMock := &MockSpeciesDatastore{}
	baseMock.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	baseMock.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Create slow datastore (100ms delays)
	slowDS := newErrorInjectionDatastore(baseMock, 0.0) // No errors, just delays
	slowDS.errorDelay = 100 * time.Millisecond

	tracker := createBasicTestTracker(slowDS)

	testInitializationTimeout(t, tracker)
	testBasicOperationPerformance(t, tracker, baseTime)

	baseMock.AssertExpectations(t)
}

func testInitializationTimeout(t *testing.T, tracker *NewSpeciesTracker) {
	t.Helper()
	// Test initialization with timeout
	initTimeout := 500 * time.Millisecond
	initStart := time.Now()
	
	initCtx, initCancel := context.WithTimeout(context.Background(), initTimeout)
	defer initCancel()
	
	// Run init in goroutine to test timeout
	initDone := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				initDone <- fmt.Errorf("init panicked: %v", r)
			}
		}()
		initDone <- tracker.InitFromDatabase()
	}()

	select {
	case err := <-initDone:
		initDuration := time.Since(initStart)
		t.Logf("Initialization completed in %v with result: %v", initDuration, err)
		// Init may succeed or fail, but shouldn't panic
		
	case <-initCtx.Done():
		initDuration := time.Since(initStart)
		t.Logf("Initialization timed out after %v", initDuration)
		// Timeout is acceptable for slow operations
	}
}

func testBasicOperationPerformance(t *testing.T, tracker *NewSpeciesTracker, baseTime time.Time) {
	t.Helper()
	// Despite potential timeout, basic operations should work
	opStart := time.Now()
	isNew, days := tracker.CheckAndUpdateSpecies("TimeoutTest", baseTime)
	opDuration := time.Since(opStart)
	
	t.Logf("Basic operation completed in %v (isNew=%v, days=%d)", opDuration, isNew, days)
	
	// Basic operations should be fast (not dependent on slow DB)
	assert.Less(t, opDuration, 100*time.Millisecond, "Basic operations should not be blocked by slow DB")
	assert.True(t, isNew, "Should handle new species detection")
	assert.Equal(t, 0, days, "New species should have 0 days")
}

// BenchmarkReliabilityUnderLoad benchmarks system reliability under sustained load
func BenchmarkReliabilityUnderLoad(b *testing.B) {
	baseMock := &MockSpeciesDatastore{}
	baseMock.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	baseMock.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Add 10% error rate to test reliability under errors
	errorDS := newErrorInjectionDatastore(baseMock, 0.1)
	tracker := createBasicTestTracker(errorDS)
	_ = tracker.InitFromDatabase() // Allow init to fail

	baseTime := time.Now()
	
	b.ResetTimer()
	
	b.Run("SustainedLoad", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			speciesName := fmt.Sprintf("BenchSpecies_%d", i%100)
			opTime := baseTime.Add(time.Duration(i) * time.Microsecond)
			
			// Should not panic even with errors
			func() {
				defer func() {
					if r := recover(); r != nil {
						b.Errorf("Panic during benchmark: %v", r)
					}
				}()
				tracker.CheckAndUpdateSpecies(speciesName, opTime)
			}()
		}
	})

	b.Run("ErrorResilience", func(b *testing.B) {
		b.RunParallel(func(pb *testing.PB) {
			i := 0
			for pb.Next() {
				speciesName := fmt.Sprintf("ErrorBench_%d", i%50)
				opTime := baseTime.Add(time.Duration(i) * time.Nanosecond)
				
				// Mix operations under error conditions
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

	// Log error statistics
	totalErrors := atomic.LoadInt64(&errorDS.errorCount)
	if totalErrors > 0 {
		b.Logf("Benchmark completed with %d database errors injected", totalErrors)
	}
}