// new_species_tracker_critical_operations_test.go
// Critical reliability tests for core species tracking operations
// Targets UpdateSpecies, checkAndResetPeriods, and batch operations for maximum reliability
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

// TestUpdateSpecies_CriticalReliability tests the core species update logic
// CRITICAL: All tracking operations depend on this function - bugs affect entire system
//
//nolint:gocognit // Table-driven test covering multiple update scenarios
func TestUpdateSpecies_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		speciesName        string
		detectionTime      time.Time
		existingLifetime   map[string]time.Time
		existingYearly     map[string]time.Time
		existingSeasonal   map[string]map[string]time.Time
		yearlyEnabled      bool
		seasonalEnabled    bool
		expectedNewSpecies bool
		expectedLifetime   time.Time
		expectedYearly     *time.Time
		expectedSeasonal   *time.Time
		description        string
	}{
		{
			"completely_new_species",
			"Brand_New_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			map[string]time.Time{},                        // No existing lifetime data
			map[string]time.Time{},                        // No existing yearly data
			map[string]map[string]time.Time{"summer": {}}, // No existing seasonal data
			true, true,
			true, // Should be marked as new
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC), // Lifetime first seen
			&time.Time{}, // Yearly first seen
			&time.Time{}, // Seasonal first seen
			"Completely new species should be tracked in all periods",
		},
		{
			"existing_species_earlier_detection",
			"Existing_Species",
			time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC), // Earlier than existing
			map[string]time.Time{
				"Existing_Species": time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC), // Later date
			},
			map[string]time.Time{
				"Existing_Species": time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC),
			},
			map[string]map[string]time.Time{
				"summer": {"Existing_Species": time.Date(2024, 6, 20, 12, 0, 0, 0, time.UTC)},
			},
			true, true,
			false, // Not new, but should update
			time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC), // Should update to earlier date
			&time.Time{}, // Should update yearly
			&time.Time{}, // Should update seasonal
			"Earlier detection should update all existing records",
		},
		{
			"existing_species_later_detection",
			"Existing_Species",
			time.Date(2024, 6, 25, 12, 0, 0, 0, time.UTC), // Later than existing
			map[string]time.Time{
				"Existing_Species": time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC), // Earlier date
			},
			map[string]time.Time{
				"Existing_Species": time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC),
			},
			map[string]map[string]time.Time{
				"summer": {"Existing_Species": time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC)},
			},
			true, true,
			false, // Not new
			time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC), // Should keep earlier date
			&time.Time{}, // Should keep existing yearly
			&time.Time{}, // Should keep existing seasonal
			"Later detection should not update existing earlier records",
		},
		{
			"new_year_existing_lifetime",
			"Yearly_New_Species",
			time.Date(2024, 3, 15, 12, 0, 0, 0, time.UTC),
			map[string]time.Time{
				"Yearly_New_Species": time.Date(2023, 10, 10, 12, 0, 0, 0, time.UTC), // Previous year
			},
			map[string]time.Time{}, // No data for current year
			map[string]map[string]time.Time{"spring": {}},
			true, true,
			false, // Not new lifetime
			time.Date(2023, 10, 10, 12, 0, 0, 0, time.UTC), // Keep lifetime
			&time.Time{}, // New for this year
			&time.Time{}, // New for this season
			"Species new to current year but not lifetime should be tracked correctly",
		},
		{
			"detection_outside_current_year",
			"Old_Detection_Species",
			time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC), // Previous year detection
			map[string]time.Time{},
			map[string]time.Time{},
			map[string]map[string]time.Time{},
			true, true,
			true, // New lifetime
			time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC),
			nil,          // Should NOT update yearly (different year)
			&time.Time{}, // Should update seasonal
			"Detection from previous year should not update current year tracking",
		},
		{
			"seasonal_transition",
			"Seasonal_Species",
			time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC), // Summer solstice
			map[string]time.Time{},
			map[string]time.Time{},
			map[string]map[string]time.Time{
				"spring": {"Different_Species": time.Date(2024, 3, 21, 12, 0, 0, 0, time.UTC)},
			},
			true, true,
			true,
			time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC),
			&time.Time{},
			&time.Time{},
			"Species detected at season transition should be tracked in correct season",
		},
		{
			"yearly_disabled_seasonal_enabled",
			"Seasonal_Only_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			map[string]time.Time{},
			map[string]time.Time{},
			map[string]map[string]time.Time{},
			false, true, // Yearly disabled, seasonal enabled
			true,
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			nil,          // Yearly tracking disabled
			&time.Time{}, // Seasonal should still work
			"With yearly disabled, seasonal tracking should still function",
		},
		{
			"cache_invalidation_check",
			"Cache_Test_Species",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			map[string]time.Time{
				"Cache_Test_Species": time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC),
			},
			map[string]time.Time{},
			map[string]map[string]time.Time{},
			true, true,
			false,
			time.Date(2024, 6, 10, 12, 0, 0, 0, time.UTC),
			&time.Time{},
			&time.Time{},
			"UpdateSpecies should invalidate cache for the species",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing update scenario: %s", tt.description)

			// Create mock datastore
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()
			// BG-17: InitFromDatabase loads notification history (optional - only if suppression enabled)
			ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
				Return([]datastore.NotificationHistory{}, nil).Maybe()
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()

			// Create tracker with specified settings
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    tt.yearlyEnabled,
					ResetMonth: 1,
					ResetDay:   1,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: tt.seasonalEnabled,
				},
			}

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Set up existing data
			tracker.speciesFirstSeen = tt.existingLifetime
			tracker.speciesThisYear = tt.existingYearly
			if tt.seasonalEnabled {
				tracker.speciesBySeason = tt.existingSeasonal
			}
			tracker.currentYear = 2024 // Set current year for testing

			// Test UpdateSpecies
			isNew := tracker.UpdateSpecies(tt.speciesName, tt.detectionTime)

			// Verify results
			assert.Equal(t, tt.expectedNewSpecies, isNew,
				"IsNew result incorrect for scenario: %s", tt.name)

			// Verify lifetime tracking
			actualLifetime, exists := tracker.speciesFirstSeen[tt.speciesName]
			assert.True(t, exists, "Species should exist in lifetime tracking")
			assert.Equal(t, tt.expectedLifetime, actualLifetime,
				"Lifetime first seen incorrect for scenario: %s", tt.name)

			// Verify yearly tracking if enabled
			if tt.yearlyEnabled && tt.expectedYearly != nil {
				_, yearExists := tracker.speciesThisYear[tt.speciesName]
				if tt.detectionTime.Year() == tracker.currentYear {
					assert.True(t, yearExists, "Species should exist in yearly tracking")
				} else {
					assert.False(t, yearExists, "Species should not be in different year's tracking")
				}
			}

			// Verify seasonal tracking if enabled
			if tt.seasonalEnabled && tt.expectedSeasonal != nil {
				season := tracker.getCurrentSeason(tt.detectionTime)
				_, seasonExists := tracker.speciesBySeason[season][tt.speciesName]
				assert.True(t, seasonExists, "Species should exist in seasonal tracking")
			}

			// Verify cache invalidation
			_, cacheExists := tracker.statusCache[tt.speciesName]
			assert.False(t, cacheExists, "Cache should be invalidated after update")

			t.Logf("✓ Update logic correct: isNew=%v, lifetime=%v", isNew, actualLifetime.Format(time.DateOnly))
		})
	}
}

// TestCheckAndResetPeriods_CriticalReliability tests period reset logic
// CRITICAL: Period management affects all tracking accuracy
//
//nolint:gocognit // Table-driven test for period reset logic
func TestCheckAndResetPeriods_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name               string
		currentTime        time.Time
		initialYear        int
		initialSeason      string
		yearlyEnabled      bool
		seasonalEnabled    bool
		resetMonth         int
		resetDay           int
		expectYearReset    bool
		expectSeasonChange bool
		newSeason          string
		description        string
	}{
		{
			"year_reset_january_1",
			time.Date(2025, 1, 1, 0, 0, 1, 0, time.UTC),
			2024,
			"winter",
			true, true,
			1, 1, // Reset on January 1
			true, false, // Year should reset, season stays winter
			"winter",
			"Year should reset on January 1st at midnight",
		},
		{
			"year_no_reset_december_31",
			time.Date(2024, 12, 31, 23, 59, 59, 0, time.UTC),
			2024,
			"winter",
			true, true,
			1, 1,
			false, false, // Year should not reset yet
			"winter",
			"Year should not reset on December 31st",
		},
		{
			"season_transition_spring_to_summer",
			time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC),
			2024,
			"spring",
			true, true,
			1, 1,
			false, true, // No year reset, but season should change
			"summer",
			"Season should transition from spring to summer on June 21",
		},
		{
			"season_transition_winter_to_spring",
			time.Date(2024, 3, 21, 12, 0, 0, 0, time.UTC),
			2024,
			"winter",
			true, true,
			1, 1,
			false, true,
			"spring",
			"Season should transition from winter to spring on March 21",
		},
		{
			"custom_year_reset_date",
			time.Date(2024, 7, 1, 0, 0, 1, 0, time.UTC),
			2024,
			"summer",
			true, true,
			7, 1, // Reset on July 1
			true, false, // Year should reset based on custom date
			"summer",
			"Year should reset on custom date (July 1)",
		},
		{
			"yearly_disabled_seasonal_enabled",
			time.Date(2024, 6, 21, 12, 0, 0, 0, time.UTC),
			2024,
			"spring",
			false, true, // Yearly disabled
			1, 1,
			false, true, // No year reset (disabled), season should change
			"summer",
			"With yearly disabled, only season should change",
		},
		{
			"both_disabled",
			time.Date(2025, 1, 1, 0, 0, 1, 0, time.UTC),
			2024,
			"winter",
			false, false, // Both disabled
			1, 1,
			false, false, // Nothing should change
			"winter",
			"With both tracking disabled, nothing should reset",
		},
		{
			"first_time_initialization",
			time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC),
			0, // Never initialized (currentYear = 0)
			"",
			true, true,
			1, 1,
			true, true, // Should initialize both
			"spring", // June 15 is still spring (summer starts June 21)
			"First time initialization should set year and season",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing period reset scenario: %s", tt.description)

			// Create mock datastore
			ds := mocks.NewMockInterface(t)
			ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()
			// BG-17: InitFromDatabase loads notification history (optional - only if suppression enabled)
			ds.On("GetActiveNotificationHistory", mock.AnythingOfType("time.Time")).
				Return([]datastore.NotificationHistory{}, nil).Maybe()
			ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything, mock.Anything).
				Return([]datastore.NewSpeciesData{}, nil).Maybe()

			// Create tracker with specified settings
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: 14,
				SyncIntervalMinutes:  60,
				YearlyTracking: conf.YearlyTrackingSettings{
					Enabled:    tt.yearlyEnabled,
					ResetMonth: tt.resetMonth,
					ResetDay:   tt.resetDay,
				},
				SeasonalTracking: conf.SeasonalTrackingSettings{
					Enabled: tt.seasonalEnabled,
				},
			}

			tracker := NewTrackerFromSettings(ds, settings)
			require.NotNil(t, tracker)
			require.NoError(t, tracker.InitFromDatabase())

			// Set initial state
			tracker.currentYear = tt.initialYear
			tracker.currentSeason = tt.initialSeason

			// Add some test data that should be cleared on reset
			if tt.expectYearReset {
				tracker.speciesThisYear["Test_Species"] = time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)
			}

			// Store initial state for comparison
			initialYearSpeciesCount := len(tracker.speciesThisYear)

			// Test checkAndResetPeriods
			tracker.checkAndResetPeriods(tt.currentTime)

			// Verify year reset
			if tt.expectYearReset && tt.yearlyEnabled {
				assert.Equal(t, tt.currentTime.Year(), tracker.currentYear,
					"Year should be updated to current year")
				assert.Empty(t, tracker.speciesThisYear,
					"Year species map should be cleared on reset")
				assert.Empty(t, tracker.statusCache,
					"Status cache should be cleared on year reset")
				t.Logf("✓ Year reset correctly to %d", tracker.currentYear)
			} else if tt.yearlyEnabled {
				assert.Equal(t, tt.initialYear, tracker.currentYear,
					"Year should not change when reset not needed")
				assert.Len(t, tracker.speciesThisYear, initialYearSpeciesCount,
					"Year species data should be preserved")
			}

			// Verify season change
			if tt.expectSeasonChange && tt.seasonalEnabled {
				assert.Equal(t, tt.newSeason, tracker.currentSeason,
					"Season should be updated to %s", tt.newSeason)
				assert.NotNil(t, tracker.speciesBySeason[tt.newSeason],
					"New season map should be initialized")
				t.Logf("✓ Season changed correctly to %s", tracker.currentSeason)
			} else if tt.seasonalEnabled && tt.initialSeason != "" {
				assert.Equal(t, tt.initialSeason, tracker.currentSeason,
					"Season should not change when transition not needed")
			}

			// Verify tracking still works after reset
			testSpecies := "Post_Reset_Test_Species"
			isNew := tracker.UpdateSpecies(testSpecies, tt.currentTime)
			assert.True(t, isNew, "New species should be tracked after reset")
		})
	}
}

// TestGetBatchSpeciesStatus_CriticalReliability tests batch operations performance and correctness
// CRITICAL: Batch operations are used for UI updates and must be efficient
//
//nolint:gocognit // Table-driven test for batch status operations
func TestGetBatchSpeciesStatus_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker with test data
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

	// Setup test data
	currentTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)

	// Add various species with different statuses
	tracker.UpdateSpecies("Recent_Species", currentTime.Add(-5*24*time.Hour))    // 5 days ago (new)
	tracker.UpdateSpecies("Older_Species", currentTime.Add(-20*24*time.Hour))    // 20 days ago (not new)
	tracker.UpdateSpecies("Very_Old_Species", currentTime.Add(-60*24*time.Hour)) // 60 days ago
	tracker.UpdateSpecies("Today_Species", currentTime)                          // Today

	tests := []struct {
		name          string
		speciesList   []string
		expectedCount int
		description   string
	}{
		{
			"empty_batch",
			[]string{},
			0,
			"Empty batch should return empty map",
		},
		{
			"single_species",
			[]string{"Recent_Species"},
			1,
			"Single species batch should work correctly",
		},
		{
			"multiple_existing_species",
			[]string{"Recent_Species", "Older_Species", "Very_Old_Species"},
			3,
			"Multiple existing species should all be returned",
		},
		{
			"mixed_existing_and_new",
			[]string{"Recent_Species", "Never_Seen_Species", "Older_Species"},
			3,
			"Mix of existing and never-seen species should all be returned",
		},
		{
			"duplicate_species",
			[]string{"Recent_Species", "Recent_Species", "Older_Species"},
			2, // Maps naturally deduplicate, so 2 unique species expected
			"Duplicate species in batch should be handled correctly",
		},
		{
			"large_batch",
			generateLargeSpeciesList(100),
			100,
			"Large batch should be handled efficiently",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing batch scenario: %s", tt.description)

			// Test GetBatchSpeciesStatus
			startTime := time.Now()
			results := tracker.GetBatchSpeciesStatus(tt.speciesList, currentTime)
			duration := time.Since(startTime)

			// Verify results
			assert.Len(t, results, tt.expectedCount,
				"Result count mismatch for scenario: %s", tt.name)

			// Verify each requested species is in results
			for _, species := range tt.speciesList {
				status, exists := results[species]
				assert.True(t, exists, "Species %s should be in results", species)
				assert.GreaterOrEqual(t, status.DaysSinceFirst, 0,
					"Days should never be negative")

				// Verify status consistency
				switch species {
				case "Recent_Species":
					assert.True(t, status.IsNew, "Recent species should be marked as new")
					assert.Equal(t, 5, status.DaysSinceFirst, "Recent species should have 5 days")
				case "Older_Species":
					assert.False(t, status.IsNew, "Older species should not be marked as new")
					assert.Equal(t, 20, status.DaysSinceFirst, "Older species should have 20 days")
				}
			}

			// Performance check for large batches
			if len(tt.speciesList) >= 100 {
				assert.Less(t, duration, 10*time.Millisecond,
					"Large batch should complete within 10ms")
				t.Logf("✓ Large batch processed in %v", duration)
			}

			t.Logf("✓ Batch processed correctly: %d species in %v", len(results), duration)
		})
	}

	// Test concurrent batch operations
	t.Run("concurrent_batch_operations", func(t *testing.T) {
		t.Logf("Testing concurrent batch operations for thread safety")

		var wg sync.WaitGroup
		concurrentOps := 50
		species := []string{"Species_A", "Species_B", "Species_C"}

		// Run many concurrent batch operations
		for i := range concurrentOps {
			wg.Add(1)
			go func(id int) {
				defer wg.Done()

				results := tracker.GetBatchSpeciesStatus(species, currentTime)
				assert.Len(t, results, len(species),
					"Concurrent batch %d should return correct count", id)

				for _, s := range species {
					_, exists := results[s]
					assert.True(t, exists,
						"Concurrent batch %d should have species %s", id, s)
				}
			}(i)
		}

		wg.Wait()
		t.Logf("✓ %d concurrent batch operations completed successfully", concurrentOps)
	})
}

// generateLargeSpeciesList creates a list of species names for testing
func generateLargeSpeciesList(count int) []string {
	species := make([]string, count)
	for i := range count {
		species[i] = fmt.Sprintf("Batch_Test_Species_%04d", i)
	}
	return species
}
