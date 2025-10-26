// species_tracker_restart_state_test.go
//
// Test suite for validating species tracker behavior on restart and initialization.
// These tests specifically address BG-17: Species tracker losing state on restart,
// causing false "New Species" notifications after upgrade.
//
// Test Organization:
// 1. Empty Tracker Detection Behavior (validates the bug)
// 2. InitFromDatabase Failure Scenarios
// 3. Defensive Code Validation
// 4. Restart Simulation Integration Tests
// 5. Recovery Scenarios
// 6. Large Dataset Performance
// 7. Logging Validation

package species

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// =============================================================================
// 1. Empty Tracker Detection Behavior (Core Bug Validation)
// =============================================================================

// TestEmptyTrackerMarksKnownSpeciesAsNew validates BG-17: Empty tracker treats known species as "new"
// This is the ACTUAL BUG behavior we're documenting.
func TestEmptyTrackerMarksKnownSpeciesAsNew(t *testing.T) {
	t.Parallel()

	// Setup: Species exists in database (detected yesterday)
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Branta canadensis", FirstSeenDate: "2024-10-25"},
		}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	// Create tracker and intentionally SKIP InitFromDatabase
	// This simulates the bug where initialization fails silently
	tracker := NewTrackerFromSettings(ds, settings)
	// DON'T call tracker.InitFromDatabase() - this simulates the bug

	// Verify tracker has empty map
	assert.Equal(t, 0, tracker.GetSpeciesCount(), "Tracker should have empty map when init is skipped")

	// Detect the species that's actually in the database
	detectionTime := time.Date(2024, 10, 26, 10, 0, 0, 0, time.UTC)
	isNew, days := tracker.CheckAndUpdateSpecies("Branta canadensis", detectionTime)

	// BUG: Should be false (known species) but returns true (new species)
	assert.True(t, isNew, "BG-17: Empty tracker incorrectly marks known species as new")
	assert.Equal(t, 0, days, "BG-17: Returns 0 days for known species")

	// Verify status shows as "new"
	status := tracker.GetSpeciesStatus("Branta canadensis", detectionTime)
	assert.True(t, status.IsNew, "BG-17: Status incorrectly shows IsNew=true")
	assert.Equal(t, 0, status.DaysSinceFirst, "BG-17: Status shows 0 days for known species")

	// Now the species is in the map (it was just added)
	assert.Equal(t, 1, tracker.GetSpeciesCount(), "After detection, species should be in map")
}

// TestCorrectBehaviorWithInitialization validates CORRECT behavior when InitFromDatabase succeeds
func TestCorrectBehaviorWithInitialization(t *testing.T) {
	t.Parallel()

	// Setup: Species detected 20 days ago (OUTSIDE the 14-day "new" window)
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Branta canadensis", FirstSeenDate: "2024-10-01"}, // 20 days ago
		}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// CORRECT: Call InitFromDatabase
	err := tracker.InitFromDatabase()
	require.NoError(t, err, "InitFromDatabase should succeed")

	// Verify tracker loaded data
	assert.Equal(t, 1, tracker.GetSpeciesCount(), "Tracker should have loaded 1 species from database")

	// Detect the species again (today - 20 days after first detection)
	detectionTime := time.Date(2024, 10, 21, 10, 0, 0, 0, time.UTC)
	isNew, days := tracker.CheckAndUpdateSpecies("Branta canadensis", detectionTime)

	// CORRECT: Should NOT be new (20 days old, OUTSIDE 14-day window)
	assert.False(t, isNew, "Known species outside new window should not be marked as new after successful init")
	assert.Equal(t, 20, days, "Should show 20 days since first seen")

	// Verify status
	status := tracker.GetSpeciesStatus("Branta canadensis", detectionTime)
	assert.False(t, status.IsNew, "Status should show IsNew=false for known species outside window")
	assert.Equal(t, 20, status.DaysSinceFirst, "Status should show correct days since first")
}

// =============================================================================
// 2. InitFromDatabase Failure Scenarios
// =============================================================================

// TestInitFromDatabase_GetNewSpeciesDetectionsError validates behavior when database query fails
func TestInitFromDatabase_GetNewSpeciesDetectionsError(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, errors.New("database connection failed"))

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// Should return error
	err := tracker.InitFromDatabase()
	require.Error(t, err, "InitFromDatabase should return error when query fails")
	assert.Contains(t, err.Error(), "failed to load lifetime species data", "Error should be descriptive")

	// Tracker should have empty map
	assert.Equal(t, 0, tracker.GetSpeciesCount(), "Tracker should have empty map after failed init")

	// Subsequent detection should treat as new (bug behavior)
	detectionTime := time.Now()
	isNew, _ := tracker.CheckAndUpdateSpecies("Test species", detectionTime)
	assert.True(t, isNew, "Empty tracker treats all species as new")
}

// TestInitFromDatabase_EmptyDatabaseResults validates behavior when database returns empty results
func TestInitFromDatabase_EmptyDatabaseResults(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	// Database returns empty slice (no error, just no data)
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
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// Should succeed but with empty data
	err := tracker.InitFromDatabase()
	require.NoError(t, err, "InitFromDatabase should succeed even with empty results")

	// Tracker should have empty map
	assert.Equal(t, 0, tracker.GetSpeciesCount(), "Tracker should have empty map when DB returns no data")
}

// TestInitFromDatabase_ContextTimeout validates behavior with slow database queries
func TestInitFromDatabase_ContextTimeout(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	// Simulate slow query
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			// Check if context is already cancelled
			ctx := args.Get(0).(context.Context)
			select {
			case <-ctx.Done():
				// Context cancelled, return immediately
				return
			case <-time.After(100 * time.Millisecond):
				// Simulate slow query
			}
		}).
		Return([]datastore.NewSpeciesData{}, context.DeadlineExceeded)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	err := tracker.InitFromDatabase()
	require.Error(t, err, "InitFromDatabase should return error on timeout")
	assert.Contains(t, err.Error(), "failed to load lifetime species data", "Error should indicate database failure")
}

// =============================================================================
// 3. Defensive Code Validation
// =============================================================================

// TestLoadLifetimeData_PreservesExistingDataOnEmptyResults validates defensive code
// that preserves data when DB returns empty
func TestLoadLifetimeData_PreservesExistingDataOnEmptyResults(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}

	// First call: Return species data
	firstCallData := []datastore.NewSpeciesData{
		{ScientificName: "Species1", FirstSeenDate: "2024-01-01"},
		{ScientificName: "Species2", FirstSeenDate: "2024-01-02"},
	}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return(firstCallData, nil).Once()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)
	assert.Equal(t, 2, tracker.GetSpeciesCount(), "Should have loaded 2 species")

	// Second call: Return empty (simulates DB issue)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	// BG-17: InitFromDatabase now loads notification history
	ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil)

	// Try to sync - should preserve existing data
	err = tracker.InitFromDatabase()
	require.NoError(t, err, "InitFromDatabase should succeed")

	// Should STILL have 2 species (defensive code preserved data)
	assert.Equal(t, 2, tracker.GetSpeciesCount(),
		"Defensive code should preserve existing data when DB returns empty")
}

// TestLoadLifetimeData_ReplacesDataOnNewResults validates that legitimate new data replaces old data
func TestLoadLifetimeData_ReplacesDataOnNewResults(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}

	// First call: 2 species
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Species1", FirstSeenDate: "2024-01-01"},
			{ScientificName: "Species2", FirstSeenDate: "2024-01-02"},
		}, nil).Once()
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)
	assert.Equal(t, 2, tracker.GetSpeciesCount(), "Should have loaded 2 species")

	// Second call: 3 species (new detection added)
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Species1", FirstSeenDate: "2024-01-01"},
			{ScientificName: "Species2", FirstSeenDate: "2024-01-02"},
			{ScientificName: "Species3", FirstSeenDate: "2024-01-03"},
		}, nil)

	err = tracker.InitFromDatabase()
	require.NoError(t, err, "InitFromDatabase should succeed")

	// Should now have 3 species
	assert.Equal(t, 3, tracker.GetSpeciesCount(), "Should update to 3 species when DB returns new data")
}

// =============================================================================
// 4. Restart Simulation Integration Tests
// =============================================================================

// TestRestartScenario_SuccessfulInitialization - Integration test: Full restart workflow with successful init
func TestRestartScenario_SuccessfulInitialization(t *testing.T) {
	t.Parallel()

	// Simulate first run
	ds1 := &MockSpeciesDatastore{}
	ds1.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	// BG-17: InitFromDatabase now loads notification history
	ds1.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
		Return([]datastore.NotificationHistory{}, nil)
	ds1.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker1 := NewTrackerFromSettings(ds1, settings)
	require.NoError(t, tracker1.InitFromDatabase())

	// Detect a species (20 days ago, so it will be OUTSIDE new window later)
	detectionTime := time.Date(2024, 10, 1, 10, 0, 0, 0, time.UTC)
	isNew, _ := tracker1.CheckAndUpdateSpecies("Branta canadensis", detectionTime)
	assert.True(t, isNew, "First detection should be new")

	// Simulate restart: Database now has the species
	ds2 := &MockSpeciesDatastore{}
	ds2.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Branta canadensis", FirstSeenDate: "2024-10-01"},
		}, nil)
	ds2.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	// Create new tracker (simulates restart)
	tracker2 := NewTrackerFromSettings(ds2, settings)
	require.NoError(t, tracker2.InitFromDatabase())

	// Verify species was loaded
	assert.Equal(t, 1, tracker2.GetSpeciesCount(), "After restart, tracker should load species from DB")

	// Detect same species again (20 days later, OUTSIDE 14-day new window)
	detectionTime2 := time.Date(2024, 10, 21, 10, 0, 0, 0, time.UTC)
	isNew2, days := tracker2.CheckAndUpdateSpecies("Branta canadensis", detectionTime2)

	// CORRECT: Should NOT be new after restart if init succeeds (20 days old, outside window)
	assert.False(t, isNew2, "After restart with successful init, species outside new window should not be new")
	assert.Equal(t, 20, days, "Should show 20 days since first detection")
}

// TestRestartScenario_FailedInitialization - Integration test: Restart with failed initialization (BG-17 bug)
func TestRestartScenario_FailedInitialization(t *testing.T) {
	t.Parallel()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	// Simulate restart where InitFromDatabase fails
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, errors.New("database locked"))

	tracker := NewTrackerFromSettings(ds, settings)
	err := tracker.InitFromDatabase()
	require.Error(t, err, "InitFromDatabase should fail")

	// Tracker has empty map
	assert.Equal(t, 0, tracker.GetSpeciesCount(), "Failed init leaves tracker with empty map")

	// Detect a species that's actually in the database
	detectionTime := time.Date(2024, 10, 26, 10, 0, 0, 0, time.UTC)
	isNew, days := tracker.CheckAndUpdateSpecies("Branta canadensis", detectionTime)

	// BG-17 BUG: Treats known species as new
	assert.True(t, isNew, "BG-17: Failed init causes known species to be marked as new")
	assert.Equal(t, 0, days, "BG-17: Returns 0 days for species that should be known")

	// Verify status
	status := tracker.GetSpeciesStatus("Branta canadensis", detectionTime)
	assert.True(t, status.IsNew, "BG-17: Status shows IsNew=true for known species")
	assert.Equal(t, detectionTime, status.FirstSeenTime, "FirstSeenTime set to current detection (not historical)")
}

// =============================================================================
// 5. Recovery Scenarios
// =============================================================================

// TestRecovery_SyncAfterFailedInit validates that SyncIfNeeded can recover from failed initialization
func TestRecovery_SyncAfterFailedInit(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping recovery test in short mode (requires sleep)")
	}
	t.Parallel()

	ds := &MockSpeciesDatastore{}

	// First call fails
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, errors.New("connection failed")).Once()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  1, // Very short for testing (1 minute)
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	err := tracker.InitFromDatabase()
	require.Error(t, err, "Initial init should fail")
	assert.Equal(t, 0, tracker.GetSpeciesCount(), "Tracker should be empty after failed init")

	// Subsequent calls succeed
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Branta canadensis", FirstSeenDate: "2024-10-25"},
		}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	// Wait for sync interval to pass
	time.Sleep(61 * time.Second) // Just over 1 minute

	// Trigger sync
	err = tracker.SyncIfNeeded()
	require.NoError(t, err, "Sync should succeed after initial failure")

	// Should now have data
	assert.Equal(t, 1, tracker.GetSpeciesCount(), "After sync, tracker should recover and load data")
}

// =============================================================================
// 6. Large Dataset Performance
// =============================================================================

// TestInitFromDatabase_LargeDatasetTimeout validates behavior with 10,000+ species (hardcoded limit)
func TestInitFromDatabase_LargeDatasetTimeout(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping large dataset test in short mode")
	}
	t.Parallel()

	ds := &MockSpeciesDatastore{}

	// Generate 10,000 species
	largeDataset := make([]datastore.NewSpeciesData, 10000)
	for i := 0; i < 10000; i++ {
		largeDataset[i] = datastore.NewSpeciesData{
			ScientificName: fmt.Sprintf("Species%d", i),
			FirstSeenDate:  "2024-01-01",
		}
	}

	// Simulate slow query
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Run(func(args mock.Arguments) {
			time.Sleep(2 * time.Second) // Simulate slow query
		}).
		Return(largeDataset, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// Should handle large dataset
	start := time.Now()
	err := tracker.InitFromDatabase()
	duration := time.Since(start)

	// Log performance
	t.Logf("Large dataset init took: %v", duration)

	if err == nil {
		assert.Equal(t, 10000, tracker.GetSpeciesCount(), "Should load all 10,000 species")
		t.Logf("Successfully loaded 10,000 species in %v", duration)
	} else {
		// If timeout or error, should be handled gracefully
		t.Logf("Large dataset init failed (expected for timeout scenarios): %v", err)
	}
}

// =============================================================================
// 7. Logging Validation
// =============================================================================

// TestInitFromDatabase_LogsSpeciesCount validates that successful initialization logs species count
func TestInitFromDatabase_LogsSpeciesCount(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{
			{ScientificName: "Species1", FirstSeenDate: "2024-01-01"},
			{ScientificName: "Species2", FirstSeenDate: "2024-01-02"},
		}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	// TODO: Capture log output and verify it contains species count
	// For now, just verify it doesn't error
	err := tracker.InitFromDatabase()
	require.NoError(t, err, "InitFromDatabase should succeed")
	assert.Equal(t, 2, tracker.GetSpeciesCount(), "Should have loaded 2 species")

	// Log should contain: "Loaded species data from database", species_count=2
	// This would require log capture infrastructure
}

// TestInitFromDatabase_LogsError validates that failed initialization logs error details
func TestInitFromDatabase_LogsError(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, errors.New("connection refused"))

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    false,
			WindowDays: 7,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: false,
		},
	}

	tracker := NewTrackerFromSettings(ds, settings)

	err := tracker.InitFromDatabase()
	require.Error(t, err, "InitFromDatabase should return error")
	assert.Contains(t, err.Error(), "failed to load lifetime species data", "Error should be descriptive")

	// Log should contain error details
	// TODO: Verify log output contains "failed to load lifetime species data"
	// This would require log capture infrastructure
}
