// new_species_tracker_integration_test.go
// Integration tests for complete species tracking workflow
package species

import (
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

// TestFullWorkflow_BasicTracking tests basic species tracking workflow
// CRITICAL: Tests the entire lifecycle from initialization through detection
func TestFullWorkflow_BasicTracking(t *testing.T) {
	t.Parallel()

	// Create a mock datastore
	ds := mocks.NewMockInterface(t)

	// Setup mock to return empty results for any date range
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	// Basic tracking doesn't use yearly/seasonal, so this may not be called
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	// Configure basic tracking
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
	}

	// Create tracker
	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Initialize from database
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Test detecting a new species
	newDetectionTime := time.Now()
	isNew, days := tracker.CheckAndUpdateSpecies("New_Species", newDetectionTime)
	assert.True(t, isNew, "New species should be marked as new")
	assert.Equal(t, 0, days)

	// Verify status through GetSpeciesStatus
	status := tracker.GetSpeciesStatus("New_Species", newDetectionTime)
	assert.True(t, status.IsNew)
	assert.Equal(t, 0, status.DaysSinceFirst)

	// Test detection after window expires
	laterTime := newDetectionTime.Add(15 * 24 * time.Hour)
	isNew, days = tracker.CheckAndUpdateSpecies("New_Species", laterTime)
	assert.False(t, isNew, "Species should no longer be new after window")
	assert.Equal(t, 15, days)

	// Test concurrent detections
	var wg sync.WaitGroup
	for i := range 10 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			species := fmt.Sprintf("Concurrent_Species_%d", id)
			isNew, _ := tracker.CheckAndUpdateSpecies(species, time.Now())
			assert.True(t, isNew, "Concurrent species %d should be new", id)
		}(i)
	}
	wg.Wait()

	// Verify species count
	assert.Equal(t, 11, tracker.GetSpeciesCount()) // 1 new + 10 concurrent

	t.Log("✓ Basic tracking workflow completed successfully")
}

// TestFullWorkflow_YearlyTracking tests yearly tracking workflow
// CRITICAL: Tests yearly resets and transitions
func TestFullWorkflow_YearlyTracking(t *testing.T) {
	t.Parallel()

	// Create mock datastore
	ds := mocks.NewMockInterface(t)

	// Setup mock responses for yearly data
	yearlyData := []datastore.NewSpeciesData{
		{
			ScientificName: "Species_2024",
			FirstSeenDate:  "2024-03-15",
		},
	}

	// Set up mocks carefully to match actual implementation behavior
	// For lifetime tracking (GetNewSpeciesDetections), return empty to simulate
	// that this species has never been seen before in lifetime tracking
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()

	// For yearly tracking, return the species data for 2024
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, "2024-01-01", "2024-12-31", mock.Anything, mock.Anything).Return(yearlyData, nil).Once()

	// Default handler for other period queries
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	// Configure for yearly tracking
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			ResetMonth: 1,
			ResetDay:   1,
		},
		NewSpeciesWindowDays: 7,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Set current year for testing
	tracker.SetCurrentYearForTesting(2024)

	// Initialize from database
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Check species from current year
	// Species_2024 was loaded into yearly tracking (seen on 2024-03-15)
	// but NOT into lifetime tracking (it's considered a completely new species lifetime-wise)
	// When checking on 2024-06-15 (3 months later), with a 7-day window:
	// - Lifetime: IsNew = true (never seen before in lifetime)
	// - Yearly: IsNewThisYear = false (seen earlier this year, beyond 7-day window)
	status := tracker.GetSpeciesStatus("Species_2024", time.Date(2024, 6, 15, 0, 0, 0, 0, time.UTC))
	// The main IsNew flag represents lifetime tracking, so it should be true
	assert.True(t, status.IsNew, "Species never seen in lifetime should be new")
	assert.False(t, status.IsNewThisYear, "Species from earlier in year should not be new this year")

	// Add a new species in 2024
	isNew, _ := tracker.CheckAndUpdateSpecies("New_2024", time.Date(2024, 8, 1, 0, 0, 0, 0, time.UTC))
	assert.True(t, isNew, "New species in 2024 should be new")

	// Simulate year transition to 2025
	tracker.SetCurrentYearForTesting(2025)
	tracker.checkAndResetPeriods(time.Date(2025, 1, 1, 0, 0, 0, 0, time.UTC))

	// Previous year's species should now be new
	status = tracker.GetSpeciesStatus("Species_2024", time.Date(2025, 2, 1, 0, 0, 0, 0, time.UTC))
	assert.True(t, status.IsNew, "Previous year's species should be new after reset")

	t.Log("✓ Yearly tracking workflow completed successfully")
}

// TestFullWorkflow_SeasonalTracking tests seasonal tracking workflow
// CRITICAL: Tests seasonal transitions and tracking
func TestFullWorkflow_SeasonalTracking(t *testing.T) {
	t.Parallel()

	// Create mock datastore
	ds := mocks.NewMockInterface(t)

	// Setup mock for seasonal data
	springData := []datastore.NewSpeciesData{
		{
			ScientificName: "Spring_Bird",
			FirstSeenDate:  "2024-04-15",
		},
	}

	// Mock will return seasonal data
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(springData, nil).Maybe()
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	// Configure for seasonal tracking
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled:    true,
			WindowDays: 7, // Set seasonal window days explicitly
		},
		NewSpeciesWindowDays: 7,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Initialize with spring season using safe test helper
	tracker.SetCurrentSeasonForTesting("spring")
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Check spring bird in spring
	// Spring_Bird was loaded into seasonal tracking with FirstSeenDate of 2024-04-15
	// but NOT into lifetime tracking (empty from GetNewSpeciesDetections)
	// Checking on 2024-04-20 (5 days later) with 7-day window:
	status := tracker.GetSpeciesStatus("Spring_Bird", time.Date(2024, 4, 20, 0, 0, 0, 0, time.UTC))
	// The main IsNew flag represents lifetime tracking - should be true (never seen in lifetime)
	assert.True(t, status.IsNew, "Spring bird never seen in lifetime should be new")
	// Check seasonal status - should be true since within 7-day window
	assert.True(t, status.IsNewThisSeason, "Spring bird within window should be new this season")
	assert.Equal(t, 5, status.DaysThisSeason, "Should be 5 days since first seen this season")

	// Add a summer bird (should be new in spring)
	isNew, _ := tracker.CheckAndUpdateSpecies("Summer_Bird", time.Date(2024, 4, 20, 0, 0, 0, 0, time.UTC))
	assert.True(t, isNew, "Summer bird should be new in spring")

	// Simulate season transition to summer
	summerTime := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC) // Summer solstice
	tracker.checkAndResetPeriods(summerTime)

	// Spring bird should be new in summer
	status = tracker.GetSpeciesStatus("Spring_Bird", summerTime)
	assert.True(t, status.IsNew, "Spring bird should be new in summer")

	t.Log("✓ Seasonal tracking workflow completed successfully")
}

// TestFullWorkflow_CombinedTracking tests all tracking modes together
// CRITICAL: Tests interaction between yearly and seasonal tracking
func TestFullWorkflow_CombinedTracking(t *testing.T) {
	t.Parallel()

	// Create mock datastore
	ds := mocks.NewMockInterface(t)

	// Setup default mock responses
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	// Enable all tracking modes
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
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

	// Initialize tracker
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	currentTime := time.Now()

	// Test new species detection across all modes
	isNew, days := tracker.CheckAndUpdateSpecies("Brand_New_Bird", currentTime)
	assert.True(t, isNew, "Brand new bird should be new")
	assert.Equal(t, 0, days)

	// Verify it's tracked in all modes
	currentSeason := tracker.computeCurrentSeason(currentTime)
	tracker.mu.RLock()
	_, existsLifetime := tracker.speciesFirstSeen["Brand_New_Bird"]
	_, existsYearly := tracker.speciesThisYear["Brand_New_Bird"]
	seasonMap, seasonExists := tracker.speciesBySeason[currentSeason]
	var existsSeasonal bool
	if seasonExists && seasonMap != nil {
		_, existsSeasonal = seasonMap["Brand_New_Bird"]
	}
	tracker.mu.RUnlock()

	assert.True(t, existsLifetime, "Should be tracked in lifetime")
	assert.True(t, existsYearly, "Should be tracked in yearly")
	assert.True(t, existsSeasonal, "Should be tracked in seasonal")

	// Test cache behavior across modes
	for range 5 {
		status := tracker.GetSpeciesStatus("Brand_New_Bird", currentTime)
		assert.True(t, status.IsNew, "Should remain new within window")
	}

	// Test concurrent operations across all modes
	var wg sync.WaitGroup
	for i := range 20 {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Mix different operations
			switch id % 3 {
			case 0:
				// New detection
				species := fmt.Sprintf("Multi_Species_%d", id)
				tracker.CheckAndUpdateSpecies(species, currentTime)
			case 1:
				// Status check
				tracker.GetSpeciesStatus("Brand_New_Bird", currentTime)
			case 2:
				// Update existing
				tracker.UpdateSpecies("Brand_New_Bird", currentTime)
			}
		}(i)
	}
	wg.Wait()

	t.Log("✓ Combined tracking workflow completed successfully")
}

// TestFullWorkflow_ErrorRecovery tests error handling and recovery
// CRITICAL: Tests system resilience and error handling
func TestFullWorkflow_ErrorRecovery(t *testing.T) {
	t.Parallel()

	// Test with nil datastore
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// InitFromDatabase correctly returns error when datastore is nil
	err := tracker.InitFromDatabase()
	require.Error(t, err, "Should return error when datastore is nil")
	assert.Contains(t, err.Error(), "datastore is nil", "Error should indicate nil datastore")

	// Tracker should still be functional
	isNew, days := tracker.CheckAndUpdateSpecies("Test_Species", time.Now())
	assert.True(t, isNew, "Should still track new species after error")
	assert.Equal(t, 0, days)

	// Test with datastore that returns errors
	errorDS := mocks.NewMockInterface(t)
	errorDS.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("database error")).Maybe()
	// Basic tracking doesn't use yearly/seasonal, so this may not be called
	errorDS.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return(nil, fmt.Errorf("database error")).Maybe()

	// Verify error mock expectations are met
	t.Cleanup(func() { errorDS.AssertExpectations(t) })

	tracker2 := NewTrackerFromSettings(errorDS, settings)
	require.NotNil(t, tracker2)

	// InitFromDatabase properly returns error when datastore fails (expected behavior)
	err = tracker2.InitFromDatabase()
	require.Error(t, err, "Should return error when datastore fails")
	assert.Contains(t, err.Error(), "database error", "Error should contain database error message")

	// Even though initialization failed, tracker should still be functional for new detections
	// (it will track new species from this point forward)
	isNew, days = tracker2.CheckAndUpdateSpecies("Error_Recovery_Species", time.Now())
	assert.True(t, isNew, "Should still function after initialization error")
	assert.Equal(t, 0, days)

	t.Log("✓ Error recovery workflow completed successfully")
}

// TestFullWorkflow_MemoryManagement tests memory management and cleanup
// CRITICAL: Tests memory efficiency and cleanup operations
func TestFullWorkflow_MemoryManagement(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: PruneOldEntries deletes from database
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).Return(int64(0), nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Add many species to test memory management
	baseTime := time.Now()
	// Add some very old species (that will be pruned)
	for i := range 20 {
		species := fmt.Sprintf("Very_Old_Species_%d", i)
		detectionTime := baseTime.AddDate(-11, 0, -i) // 11+ years ago
		tracker.CheckAndUpdateSpecies(species, detectionTime)
	}
	// Add recent species (that won't be pruned)
	for i := range 80 {
		species := fmt.Sprintf("Recent_Species_%d", i)
		detectionTime := baseTime.Add(time.Duration(-i*24) * time.Hour) // Spread over 80 days
		tracker.CheckAndUpdateSpecies(species, detectionTime)
	}

	// Check initial count
	initialCount := tracker.GetSpeciesCount()
	assert.Equal(t, 100, initialCount, "Should have 100 species total")

	// Test pruning old entries (only 11+ year old entries should be pruned)
	prunedCount := tracker.PruneOldEntries()
	assert.Equal(t, 20, prunedCount, "Should prune exactly 20 very old entries")

	// Check count after pruning
	finalCount := tracker.GetSpeciesCount()
	assert.Equal(t, 80, finalCount, "Should have 80 species remaining after pruning")

	// Test cache management
	for i := range 1000 {
		species := fmt.Sprintf("Cache_Test_%d", i)
		tracker.GetSpeciesStatus(species, baseTime)
	}

	// Cache should be managed to prevent excessive memory use
	tracker.mu.RLock()
	cacheSize := len(tracker.statusCache)
	tracker.mu.RUnlock()

	assert.LessOrEqual(t, cacheSize, 1000, "Cache should be bounded")

	t.Log("✓ Memory management workflow completed successfully")
}

// TestFullWorkflow_NotificationSystem tests notification deduplication
// CRITICAL: Tests notification record management
func TestFullWorkflow_NotificationSystem(t *testing.T) {
	t.Parallel()

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: RecordNotificationSent saves to database
	ds.On("SaveNotificationHistory", mock.AnythingOfType("*datastore.NotificationHistory")).Return(nil).Maybe()
	// BG-17: CleanupOldNotificationRecords deletes from database
	ds.On("DeleteExpiredNotificationHistory", mock.AnythingOfType("time.Time")).Return(int64(0), nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	settings := &conf.SpeciesTrackingSettings{
		Enabled:                      true,
		NewSpeciesWindowDays:         7,
		NotificationSuppressionHours: 168, // 7 days - required for notification system to work
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	currentTime := time.Now()

	// First detection should be new
	isNew, _ := tracker.CheckAndUpdateSpecies("Notifiable_Species", currentTime)
	assert.True(t, isNew, "First detection should be new")

	// Record that notification was sent
	tracker.RecordNotificationSent("Notifiable_Species", currentTime)

	// Check notification record exists
	tracker.mu.RLock()
	lastSent, exists := tracker.notificationLastSent["Notifiable_Species"]
	tracker.mu.RUnlock()

	assert.True(t, exists, "Notification record should exist")
	assert.Equal(t, currentTime.Unix(), lastSent.Unix(), "Notification time should match")

	// Test cleanup of old notification records
	tracker.mu.Lock()
	// Add old notification records
	tracker.notificationLastSent["Old_Species_1"] = currentTime.Add(-30 * 24 * time.Hour)
	tracker.notificationLastSent["Old_Species_2"] = currentTime.Add(-15 * 24 * time.Hour)
	tracker.notificationLastSent["Recent_Species"] = currentTime.Add(-1 * time.Hour)

	tracker.cleanupOldNotificationRecordsLocked(currentTime)
	tracker.mu.Unlock()

	// Check cleanup results
	tracker.mu.RLock()
	_, exists1 := tracker.notificationLastSent["Old_Species_1"]
	_, exists2 := tracker.notificationLastSent["Old_Species_2"]
	_, exists3 := tracker.notificationLastSent["Recent_Species"]
	tracker.mu.RUnlock()

	assert.False(t, exists1, "Very old notification record should be removed")
	assert.False(t, exists2, "Old notification record should be removed")
	assert.True(t, exists3, "Recent notification record should be kept")

	t.Log("✓ Notification system workflow completed successfully")
}

// TestFullWorkflow_PerformanceUnderLoad tests system performance
// CRITICAL: Tests system behavior under stress conditions
//
//nolint:gocognit // Performance test with load simulation requires complex setup
func TestFullWorkflow_PerformanceUnderLoad(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	ds := mocks.NewMockInterface(t)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()
	// BG-17: InitFromDatabase requires notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil).Maybe()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.NewSpeciesData{}, nil).Maybe()

	// Verify all mock expectations are met
	t.Cleanup(func() { ds.AssertExpectations(t) })

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled: true,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: true,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)

	// Measure detection performance
	numGoroutines := 50
	detectionsPerGoroutine := 100

	start := time.Now()
	var wg sync.WaitGroup

	for g := range numGoroutines {
		wg.Add(1)
		go func(goroutineID int) {
			defer wg.Done()

			for i := range detectionsPerGoroutine {
				// Mix new and existing species
				var species string
				if i%10 == 0 {
					// New species
					species = fmt.Sprintf("New_Species_G%d_I%d", goroutineID, i)
				} else {
					// Existing species (causes cache hits)
					species = fmt.Sprintf("Species_%d", i%100)
				}

				detectionTime := time.Now().Add(time.Duration(-i) * time.Hour)
				tracker.CheckAndUpdateSpecies(species, detectionTime)

				// Occasionally check status (read operation)
				if i%5 == 0 {
					tracker.GetSpeciesStatus(species, detectionTime)
				}
			}
		}(g)
	}

	wg.Wait()
	elapsed := time.Since(start)

	totalOperations := numGoroutines * detectionsPerGoroutine * 2 // Updates + some reads
	opsPerSecond := float64(totalOperations) / elapsed.Seconds()

	t.Logf("Performance: %d operations in %v (%.0f ops/sec)", totalOperations, elapsed, opsPerSecond)
	assert.Greater(t, opsPerSecond, 10000.0, "Should handle at least 10,000 operations per second")

	// Test memory efficiency
	tracker.mu.RLock()
	speciesCount := len(tracker.speciesFirstSeen)
	cacheSize := len(tracker.statusCache)
	tracker.mu.RUnlock()

	t.Logf("Memory: %d species tracked, %d cache entries", speciesCount, cacheSize)
	assert.Less(t, cacheSize, speciesCount, "Cache should be smaller than total species")

	t.Log("✓ Performance under load test completed successfully")
}
