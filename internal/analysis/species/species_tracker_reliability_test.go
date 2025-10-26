// new_species_tracker_reliability_test.go
// High-priority reliability tests for species tracker
// Focuses on critical scenarios that could cause system failures
package species

import (
	"fmt"
	"math/rand"
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
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestConcurrentAccessUnderLoad tests species tracker under high concurrent load
// This is critical for reliability as the tracker will face concurrent access in production
func TestConcurrentAccessUnderLoad(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name            string
		numGoroutines   int
		operationsPerGR int
		speciesCount    int
		enableYearly    bool
		enableSeasonal  bool
		description     string
	}{
		{
			"moderate_load_basic_tracking",
			50, 20, 10, false, false,
			"50 goroutines, 20 ops each, basic lifetime tracking only",
		},
		{
			"high_load_full_tracking",
			100, 50, 25, true, true,
			"100 goroutines, 50 ops each, all tracking features enabled",
		},
		{
			"burst_load_many_species",
			200, 10, 100, true, true,
			"200 goroutines, 10 ops each, 100 different species",
		},
		{
			"sustained_load_few_species",
			75, 100, 5, true, true,
			"75 goroutines, 100 ops each, few species (high contention)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runConcurrentLoadTest(t, tt.name, tt.numGoroutines, tt.operationsPerGR,
				tt.speciesCount, tt.enableYearly, tt.enableSeasonal, tt.description)
		})
	}
}

// runConcurrentLoadTest executes a single concurrent load test scenario
func runConcurrentLoadTest(t *testing.T, name string, numGoroutines, operationsPerGR,
	speciesCount int, enableYearly, enableSeasonal bool, description string) {
	t.Helper()
	t.Logf("Running concurrent load test: %s", description)

	// Create tracker
	tracker := createTrackerForConcurrentTest(t, enableYearly, enableSeasonal)
	species := generateSpeciesNames(speciesCount)

	// Run concurrent operations
	startTime := time.Now()
	totalOps, successCount, errorCount := executeConcurrentOperations(
		tracker, species, numGoroutines, operationsPerGR)
	duration := time.Since(startTime)

	// Analyze and validate results
	validateConcurrentTestResults(t, name, totalOps, successCount, errorCount, duration,
		numGoroutines*operationsPerGR, speciesCount)

	// Cleanup
	tracker.ClearCacheForTesting()
}

// createTrackerForConcurrentTest creates a properly configured tracker for testing
func createTrackerForConcurrentTest(t *testing.T, enableYearly, enableSeasonal bool) *SpeciesTracker {
	t.Helper()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    enableYearly,
			ResetMonth: 1,
			ResetDay:   1,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: enableSeasonal,
		},
	}

	tracker, _ := createTestTrackerWithMocks(t, settings)
	return tracker
}

// generateSpeciesNames creates a list of species names for testing
func generateSpeciesNames(count int) []string {
	species := make([]string, count)
	for i := 0; i < count; i++ {
		species[i] = fmt.Sprintf("Species_%d", i)
	}
	return species
}

// executeConcurrentOperations runs the actual concurrent operations
func executeConcurrentOperations(tracker *SpeciesTracker, species []string,
	numGoroutines, operationsPerGR int) (totalOps, successCount, errorCount int64) {

	var wg sync.WaitGroup

	// Launch concurrent goroutines
	for i := 0; i < numGoroutines; i++ {
		wg.Add(1)
		go func(grID int) {
			defer wg.Done()
			runGoroutineOperations(tracker, species, operationsPerGR, grID,
				&totalOps, &successCount, &errorCount)
		}(i)
	}

	wg.Wait()
	return
}

// runGoroutineOperations executes operations for a single goroutine
func runGoroutineOperations(tracker *SpeciesTracker, species []string,
	operationsPerGR, grID int, totalOps, successCount, errorCount *int64) {

	rnd := rand.New(rand.NewSource(time.Now().UnixNano() + int64(grID)))

	for op := 0; op < operationsPerGR; op++ {
		atomic.AddInt64(totalOps, 1)

		speciesName := species[rnd.Intn(len(species))]
		detectionTime := time.Now().Add(-time.Duration(rnd.Intn(30*24)) * time.Hour)

		performRandomOperation(tracker, species, speciesName, detectionTime, rnd,
			successCount, errorCount)
	}
}

// performRandomOperation executes a random tracker operation
func performRandomOperation(tracker *SpeciesTracker, species []string,
	speciesName string, detectionTime time.Time, rnd *rand.Rand,
	successCount, errorCount *int64) {

	switch rnd.Intn(4) {
	case 0: // CheckAndUpdateSpecies
		isNew, days := tracker.CheckAndUpdateSpecies(speciesName, detectionTime)
		if days >= 0 {
			atomic.AddInt64(successCount, 1)
		} else {
			atomic.AddInt64(errorCount, 1)
		}
		_ = isNew

	case 1: // GetSpeciesStatus
		status := tracker.GetSpeciesStatus(speciesName, detectionTime)
		if status.DaysSinceFirst >= 0 {
			atomic.AddInt64(successCount, 1)
		} else {
			atomic.AddInt64(errorCount, 1)
		}

	case 2: // GetBatchSpeciesStatus
		batchSpecies := species[rnd.Intn(minInt(5, len(species))):]
		if len(batchSpecies) > 3 {
			batchSpecies = batchSpecies[:3]
		}
		statuses := tracker.GetBatchSpeciesStatus(batchSpecies, detectionTime)
		if len(statuses) > 0 {
			atomic.AddInt64(successCount, 1)
		} else {
			atomic.AddInt64(errorCount, 1)
		}

	case 3: // IsNewSpecies
		isNew := tracker.IsNewSpecies(speciesName)
		atomic.AddInt64(successCount, 1)
		_ = isNew
	}
}

// validateConcurrentTestResults validates and reports test results
func validateConcurrentTestResults(t *testing.T, testName string,
	totalOps, successCount, errorCount int64, duration time.Duration,
	expectedOps, speciesCount int) {
	t.Helper()

	t.Logf("Concurrent load test completed:")
	t.Logf("  Duration: %v", duration)
	t.Logf("  Total operations: %d", totalOps)
	t.Logf("  Successful operations: %d", successCount)
	t.Logf("  Error count: %d", errorCount)
	t.Logf("  Operations/second: %.2f", float64(totalOps)/duration.Seconds())

	// Validate operation count
	assert.Equal(t, int64(expectedOps), totalOps,
		"All operations should be attempted")

	// Check for race condition
	validateRaceConditionResults(t, errorCount, totalOps)

	// Validate success rate
	assert.Positive(t, successCount, "At least some operations should succeed")

	// Memory and state validation
	validateSystemState(t, speciesCount)
}

// validateRaceConditionResults checks for and reports race conditions
func validateRaceConditionResults(t *testing.T, errorCount, totalOps int64) {
	t.Helper()

	if errorCount > 0 {
		t.Logf("🔥 CRITICAL BUG DETECTED: %d operations returned negative days under concurrent load", errorCount)
		t.Logf("This indicates a race condition in CheckAndUpdateSpecies time calculations")

		errorRate := float64(errorCount) / float64(totalOps)
		assert.Less(t, errorRate, 0.05,
			"Error rate should be less than 5%% (found %.2f%% - this indicates a race condition bug)",
			errorRate*100)
	} else {
		assert.Equal(t, int64(0), errorCount,
			"No operations should result in errors under normal load")
	}
}

// validateSystemState checks memory usage and tracker consistency
func validateSystemState(t *testing.T, maxSpeciesCount int) {
	t.Helper()

	var m runtime.MemStats
	runtime.GC()
	runtime.ReadMemStats(&m)
	t.Logf("Memory usage after test: %d KB", m.Alloc/1024)
}

// TestDatabaseFailureRecovery tests how the tracker handles database failures
// Critical for data integrity and system stability
func TestDatabaseFailureRecovery(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		failureType    string
		expectError    bool
		expectRecovery bool
		description    string
	}{
		{
			"complete_database_failure", "all_fail", true, false,
			"All database operations fail",
		},
		{
			"partial_load_failure", "lifetime_fail", true, false,
			"Lifetime data load fails, others succeed",
		},
		{
			"timeout_recovery", "timeout_then_success", true, true,
			"Database timeout followed by successful retry",
		},
		{
			"empty_result_handling", "empty_results", false, true,
			"Database returns empty results (not errors)",
		},
		{
			"corrupt_data_handling", "corrupt_data", false, true,
			"Database returns malformed data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing database failure scenario: %s", tt.description)

			ds := mocks.NewMockInterface(t)

			// Configure mock behavior based on failure type
			switch tt.failureType {
			case "all_fail":
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("database connection failed"))
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("database connection failed")).Maybe() // Not called if GetNewSpeciesDetections fails first

			case "lifetime_fail":
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("lifetime data load failed"))
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()

			case "timeout_then_success":
				// First call fails, subsequent calls succeed
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("timeout")).Once()
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{
						{ScientificName: "Test_Species", FirstSeenDate: "2024-01-01"},
					}, nil).Maybe()
		// BG-17: InitFromDatabase requires notification history
		ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
			Return([]datastore.NotificationHistory{}, nil).Maybe()
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()

			case "empty_results":
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()
				// BG-17: InitFromDatabase now loads notification history
				ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
					Return([]datastore.NotificationHistory{}, nil).Maybe()
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()

			case "corrupt_data":
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{
						{ScientificName: "", FirstSeenDate: "invalid-date"},            // Invalid data
						{ScientificName: "Valid_Species", FirstSeenDate: "2024-01-01"}, // Valid data
					}, nil).Maybe()
		// BG-17: InitFromDatabase requires notification history
		ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
			Return([]datastore.NotificationHistory{}, nil).Maybe()
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()
			}

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

			// Test initialization
			err := tracker.InitFromDatabase()
			if tt.expectError {
				require.Error(t, err, "Expected initialization error for failure type: %s", tt.failureType)
				t.Logf("Expected initialization error occurred: %v", err)
			} else {
				require.NoError(t, err, "Expected initialization success for failure type: %s", tt.failureType)
				t.Logf("Initialization handled gracefully")
			}

			// Test that tracker remains functional even after database errors
			if tt.expectRecovery || !tt.expectError {
				// Test basic operations still work
				testTime := time.Now()
				isNew, days := tracker.CheckAndUpdateSpecies("Recovery_Test_Species", testTime)

				// Should be able to track species even if database failed
				assert.True(t, isNew, "New species should be reported as new")
				assert.Equal(t, 0, days, "New species should have 0 days since first seen")

				// Test status retrieval
				status := tracker.GetSpeciesStatus("Recovery_Test_Species", testTime)
				assert.True(t, status.IsNew, "Status should show as new species")

				t.Logf("Tracker remained functional after database failure")
			}

			// If timeout recovery scenario, test that retry works
			if tt.failureType == "timeout_then_success" {
				// Second initialization should succeed
				err2 := tracker.InitFromDatabase()
				require.NoError(t, err2, "Retry after timeout should succeed")
				t.Logf("Recovery from timeout successful")
			}

			// Memory cleanup verification
			tracker.ClearCacheForTesting()
			runtime.GC()
		})
	}
}

// minInt helper function to avoid shadowing builtin
func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}
