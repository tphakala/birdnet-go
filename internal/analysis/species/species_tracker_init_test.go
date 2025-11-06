// new_species_tracker_init_test.go
// Critical tests for initialization and atomic operations
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

// TestInitFromDatabase_CriticalReliability tests complete initialization flow
// CRITICAL: This is the entry point for all tracking - failures here break everything
func TestInitFromDatabase_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupMock     func(*mocks.MockInterface)
		settings      *conf.SpeciesTrackingSettings
		expectedError bool
		description   string
	}{
		{
			"successful_full_initialization",
			func(ds *mocks.MockInterface) {
				// Lifetime data
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{
						{ScientificName: "Lifetime_Species_1", FirstSeenDate: "2024-01-01"},
						{ScientificName: "Lifetime_Species_2", FirstSeenDate: "2024-02-01"},
					}, nil).Maybe()
				// BG-17: InitFromDatabase requires notification history
				ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
					Return([]datastore.NotificationHistory{}, nil).Maybe()
				// Yearly data
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{
						{ScientificName: "Yearly_Species_1", FirstSeenDate: "2024-03-01"},
					}, nil).Once()
				// Seasonal data (4 seasons)
				for i := range 4 {
					ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
						Return([]datastore.NewSpeciesData{
							{ScientificName: fmt.Sprintf("Seasonal_Species_%d", i), FirstSeenDate: "2024-04-01"},
						}, nil).Once()
				}
			},
			&conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    true,
					WindowDays: 7,
					ResetMonth: 1,
					ResetDay:   1,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled:    true,
					WindowDays: 7,
				},
			},
			false,
			"Full initialization with all tracking modes enabled should succeed",
		},
		{
			"nil_datastore",
			nil,
			&conf.SpeciesTrackingSettings{
				Enabled: true,
			},
			true,
			"Nil datastore should return configuration error",
		},
		{
			"lifetime_data_load_failure",
			func(ds *mocks.MockInterface) {
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("database connection lost"))
			},
			&conf.SpeciesTrackingSettings{
				Enabled: true,
			},
			true,
			"Lifetime data load failure should propagate error",
		},
		{
			"yearly_data_load_failure",
			func(ds *mocks.MockInterface) {
				// Lifetime succeeds
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()
				// BG-17: InitFromDatabase now loads notification history
				ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
					Return([]datastore.NotificationHistory{}, nil).Maybe()
				// Yearly fails
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("query timeout"))
			},
			&conf.SpeciesTrackingSettings{
				Enabled: true,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled: true,
				},
			},
			true,
			"Yearly data load failure should propagate error",
		},
		{
			"partial_seasonal_failure_continues",
			func(ds *mocks.MockInterface) {
				// Lifetime succeeds
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()
				// BG-17: InitFromDatabase now loads notification history
				ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
					Return([]datastore.NotificationHistory{}, nil).Maybe()
				// First season fails
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return(nil, fmt.Errorf("seasonal query failed")).Once()
			},
			&conf.SpeciesTrackingSettings{
				Enabled: true,
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: true,
				},
			},
			true,
			"Seasonal data load failure should propagate error",
		},
		{
			"empty_database_initialization",
			func(ds *mocks.MockInterface) {
				ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()
				// BG-17: InitFromDatabase now loads notification history
				ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
					Return([]datastore.NotificationHistory{}, nil).Maybe()
				ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
					Return([]datastore.NewSpeciesData{}, nil).Maybe()
			},
			&conf.SpeciesTrackingSettings{
				Enabled: true,
			},
			false,
			"Empty database should initialize successfully with no data",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			var tracker *SpeciesTracker
			if tt.setupMock != nil {
				ds := mocks.NewMockInterface(t)
				tt.setupMock(ds)
				tracker = NewTrackerFromSettings(ds, tt.settings)
			} else {
				// Test with nil datastore
				tracker = NewTrackerFromSettings(nil, tt.settings)
			}
			require.NotNil(t, tracker)

			// Test InitFromDatabase
			err := tracker.InitFromDatabase()

			if tt.expectedError {
				require.Error(t, err, "Expected initialization error")
				t.Logf("✓ Error correctly returned: %v", err)
			} else {
				require.NoError(t, err, "Initialization should succeed")

				// Verify sync time was set
				assert.False(t, tracker.lastSyncTime.IsZero(), "Sync time should be set")

				// Verify data was loaded based on settings
				if tt.settings.YearlyTracking.Enabled {
					assert.NotNil(t, tracker.speciesThisYear, "Yearly tracking should be initialized")
				}
				if tt.settings.SeasonalTracking.Enabled {
					assert.NotNil(t, tracker.speciesBySeason, "Seasonal tracking should be initialized")
				}

				t.Logf("✓ Initialization successful")
			}
		})
	}
}

// TestCheckAndUpdateSpecies_CriticalReliability tests the atomic check-and-update operation
// CRITICAL: This ensures thread-safe updates and prevents race conditions
func TestCheckAndUpdateSpecies_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		speciesName   string
		detectionTime time.Time
		setupTracker  func(*SpeciesTracker, time.Time)
		expectedIsNew bool
		expectedDays  int
		description   string
	}{
		{
			"brand_new_species",
			"Never_Seen_Species",
			time.Now(),
			nil, // No setup needed
			true,
			0,
			"Brand new species should be marked as new with 0 days",
		},
		{
			"existing_recent_species",
			"Recent_Species",
			time.Now(),
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.speciesFirstSeen["Recent_Species"] = now.AddDate(0, 0, -5) // 5 days ago
			},
			true, // Still within 14-day window
			5,
			"Recent species within window should be marked as new",
		},
		{
			"existing_old_species",
			"Old_Species",
			time.Now(),
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.speciesFirstSeen["Old_Species"] = now.AddDate(0, 0, -20) // 20 days ago
			},
			false, // Outside 14-day window
			20,
			"Old species outside window should not be marked as new",
		},
		{
			"earlier_detection_updates",
			"Updated_Species",
			time.Now().AddDate(0, 0, -10), // Detection 10 days ago
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.speciesFirstSeen["Updated_Species"] = now.AddDate(0, 0, 5) // Originally 5 days from detection (5 days ago)
			},
			true, // New earliest detection
			0,    // Days from new earliest detection
			"Earlier detection should update first seen time and be marked as new",
		},
		{
			"later_detection_no_update",
			"No_Update_Species",
			time.Now(),
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.speciesFirstSeen["No_Update_Species"] = now.AddDate(0, 0, -10) // 10 days ago
			},
			true, // Still within window
			10,
			"Later detection should not update first seen time",
		},
		{
			"exactly_at_window_boundary",
			"Boundary_Species",
			time.Now(),
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.speciesFirstSeen["Boundary_Species"] = now.AddDate(0, 0, -14) // Exactly 14 days
			},
			true, // Exactly at boundary is still "new"
			14,
			"Species exactly at window boundary should still be new",
		},
		{
			"yearly_tracking_update",
			"Yearly_Species",
			time.Now(),
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.yearlyEnabled = true
				tracker.currentYear = now.Year()
				// Species not seen this year yet
			},
			true,
			0,
			"New species should update yearly tracking",
		},
		{
			"seasonal_tracking_update",
			"Seasonal_Species",
			time.Now(),
			func(tracker *SpeciesTracker, now time.Time) {
				tracker.seasonalEnabled = true
				tracker.currentSeason = "summer"
				// Initialize season map
				tracker.speciesBySeason["summer"] = make(map[string]time.Time)
			},
			true,
			0,
			"New species should update seasonal tracking",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup initial state if needed
			if tt.setupTracker != nil {
				tt.setupTracker(tracker, tt.detectionTime)
			}

			// Test CheckAndUpdateSpecies
			isNew, daysSince := tracker.CheckAndUpdateSpecies(tt.speciesName, tt.detectionTime)

			assert.Equal(t, tt.expectedIsNew, isNew,
				"IsNew mismatch for %s", tt.speciesName)
			assert.Equal(t, tt.expectedDays, daysSince,
				"Days mismatch for %s", tt.speciesName)

			// Verify species was recorded
			_, exists := tracker.speciesFirstSeen[tt.speciesName]
			assert.True(t, exists, "Species should be recorded after check")

			// Verify yearly tracking if enabled
			if tracker.yearlyEnabled && tracker.isWithinCurrentYear(tt.detectionTime) {
				_, yearExists := tracker.speciesThisYear[tt.speciesName]
				assert.True(t, yearExists, "Species should be in yearly tracking")
			}

			// Verify seasonal tracking if enabled
			if tracker.seasonalEnabled {
				season := tracker.getCurrentSeason(tt.detectionTime)
				if tracker.speciesBySeason[season] != nil {
					_, seasonExists := tracker.speciesBySeason[season][tt.speciesName]
					assert.True(t, seasonExists, "Species should be in seasonal tracking")
				}
			}

			t.Logf("✓ Check and update completed: isNew=%v, days=%d", isNew, daysSince)
		})
	}
}

// TestCheckAndUpdateSpecies_Atomicity tests thread safety of the atomic operation
// CRITICAL: Ensures no race conditions during concurrent updates
func TestCheckAndUpdateSpecies_Atomicity(t *testing.T) {
	t.Parallel()

	// Create tracker
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Test concurrent updates to same species
	const goroutines = 100
	const species = "Concurrent_Test_Species"

	var wg sync.WaitGroup
	results := make(chan struct {
		isNew bool
		days  int
	}, goroutines)

	now := time.Now()

	// Launch concurrent updates
	for i := range goroutines {
		wg.Add(1)
		go func(offset int) {
			defer wg.Done()

			// Each goroutine tries to update with a slightly different time
			detectionTime := now.AddDate(0, 0, -offset%20) // Vary from 0-19 days ago
			isNew, days := tracker.CheckAndUpdateSpecies(species, detectionTime)

			results <- struct {
				isNew bool
				days  int
			}{isNew, days}
		}(i)
	}

	// Wait for all updates
	wg.Wait()
	close(results)

	// Collect results
	var newCount int
	minDays := 999
	for result := range results {
		if result.isNew {
			newCount++
		}
		if result.days < minDays {
			minDays = result.days
		}
	}

	// Verify consistency
	assert.Positive(t, newCount, "At least some updates should report as new")
	assert.GreaterOrEqual(t, minDays, 0, "Minimum days should be non-negative")

	// Verify final state
	firstSeen, exists := tracker.speciesFirstSeen[species]
	assert.True(t, exists, "Species should exist after concurrent updates")
	assert.False(t, firstSeen.IsZero(), "First seen time should be set")

	t.Logf("✓ Concurrent updates completed without race: %d new reports, min days: %d", newCount, minDays)
}

// TestIsNewSpecies_CriticalReliability tests the simple new species check
// CRITICAL: Many components rely on this simple check being accurate
func TestIsNewSpecies_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupTracker  func(*SpeciesTracker)
		speciesName   string
		expectedIsNew bool
		description   string
	}{
		{
			"never_seen_species",
			nil,
			"Unknown_Species",
			true,
			"Never seen species should be new",
		},
		{
			"recent_species",
			func(tracker *SpeciesTracker) {
				tracker.speciesFirstSeen["Recent_Species"] = time.Now().AddDate(0, 0, -5)
			},
			"Recent_Species",
			true,
			"Species within window should be new",
		},
		{
			"old_species",
			func(tracker *SpeciesTracker) {
				tracker.speciesFirstSeen["Old_Species"] = time.Now().AddDate(0, 0, -20)
			},
			"Old_Species",
			false,
			"Species outside window should not be new",
		},
		{
			"exactly_at_boundary",
			func(tracker *SpeciesTracker) {
				tracker.speciesFirstSeen["Boundary_Species"] = time.Now().AddDate(0, 0, -14)
			},
			"Boundary_Species",
			true,
			"Species exactly at boundary should be new",
		},
		{
			"just_past_boundary",
			func(tracker *SpeciesTracker) {
				tracker.speciesFirstSeen["Past_Boundary"] = time.Now().AddDate(0, 0, -15)
			},
			"Past_Boundary",
			false,
			"Species just past boundary should not be new",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup initial state
			if tt.setupTracker != nil {
				tt.setupTracker(tracker)
			}

			// Test IsNewSpecies
			isNew := tracker.IsNewSpecies(tt.speciesName)

			assert.Equal(t, tt.expectedIsNew, isNew,
				"IsNew mismatch for %s", tt.speciesName)

			t.Logf("✓ IsNewSpecies correctly returned: %v", isNew)
		})
	}
}

// TestIsNewSpecies_ThreadSafety tests concurrent access to IsNewSpecies
// CRITICAL: Read operations must be thread-safe
func TestIsNewSpecies_ThreadSafety(t *testing.T) {
	t.Parallel()

	// Create tracker with some existing species
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Add some test data
	now := time.Now()
	tracker.speciesFirstSeen["Old_Species"] = now.AddDate(0, 0, -30)
	tracker.speciesFirstSeen["Recent_Species"] = now.AddDate(0, 0, -5)
	tracker.speciesFirstSeen["Boundary_Species"] = now.AddDate(0, 0, -14)

	// Test concurrent reads
	const goroutines = 100
	var wg sync.WaitGroup
	errors := make(chan error, goroutines*3)

	speciesToTest := []string{"Old_Species", "Recent_Species", "Boundary_Species", "Unknown_Species"}
	expectedResults := map[string]bool{
		"Old_Species":      false,
		"Recent_Species":   true,
		"Boundary_Species": true,
		"Unknown_Species":  true,
	}

	for range goroutines {
		wg.Go(func() {

			for _, species := range speciesToTest {
				isNew := tracker.IsNewSpecies(species)
				if isNew != expectedResults[species] {
					errors <- fmt.Errorf("species %s: expected %v, got %v",
						species, expectedResults[species], isNew)
				}
			}
		})
	}

	wg.Wait()
	close(errors)

	// Check for errors
	var errorCount int
	for err := range errors {
		t.Errorf("Concurrent read error: %v", err)
		errorCount++
	}

	assert.Equal(t, 0, errorCount, "Should have no errors during concurrent reads")
	t.Logf("✓ Concurrent reads completed successfully")
}
