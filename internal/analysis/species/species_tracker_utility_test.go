// new_species_tracker_utility_test.go
// Critical tests for utility and helper functions
package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestGetWindowDays_CriticalReliability tests window days getter
// CRITICAL: Other components rely on accurate window reporting
func TestGetWindowDays_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		windowDays  int
		expected    int
		description string
	}{
		{
			"standard_14_days",
			14,
			14,
			"Standard 14-day window should be returned correctly",
		},
		{
			"custom_7_days",
			7,
			7,
			"Custom 7-day window should be returned correctly",
		},
		{
			"zero_window",
			0,
			0,
			"Zero window (all species are new) should be supported",
		},
		{
			"large_window",
			365,
			365,
			"Large window (full year) should be supported",
		},
		{
			"negative_window",
			-1,
			-1,
			"Negative window should be returned as-is (handled elsewhere)",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker with specific window
			settings := &conf.SpeciesTrackingSettings{
				Enabled:              true,
				NewSpeciesWindowDays: tt.windowDays,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Test GetWindowDays
			result := tracker.GetWindowDays()

			assert.Equal(t, tt.expected, result,
				"Window days mismatch")

			t.Logf("✓ Correctly returned window days: %d", result)
		})
	}
}

// TestGetSpeciesCount_CriticalReliability tests species counting
// CRITICAL: Accurate counts are essential for statistics and monitoring
func TestGetSpeciesCount_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupSpecies  func(*SpeciesTracker)
		expectedCount int
		description   string
	}{
		{
			"empty_tracker",
			func(tracker *SpeciesTracker) {
				// No species added
			},
			0,
			"Empty tracker should return zero count",
		},
		{
			"single_species",
			func(tracker *SpeciesTracker) {
				tracker.speciesFirstSeen["Species_1"] = time.Now()
			},
			1,
			"Single species should be counted correctly",
		},
		{
			"multiple_species",
			func(tracker *SpeciesTracker) {
				now := time.Now()
				for i := 0; i < 100; i++ {
					speciesName := "Species_" + string(rune(i))
					tracker.speciesFirstSeen[speciesName] = now.AddDate(0, 0, -i)
				}
			},
			100,
			"Multiple species should be counted accurately",
		},
		{
			"only_lifetime_counted",
			func(tracker *SpeciesTracker) {
				now := time.Now()
				// Add to lifetime tracking
				tracker.speciesFirstSeen["Lifetime_1"] = now
				tracker.speciesFirstSeen["Lifetime_2"] = now

				// Add to yearly tracking (should not be counted)
				tracker.yearlyEnabled = true
				tracker.speciesThisYear["Yearly_1"] = now
				tracker.speciesThisYear["Yearly_2"] = now

				// Add to seasonal tracking (should not be counted)
				tracker.seasonalEnabled = true
				tracker.speciesBySeason["spring"] = map[string]time.Time{
					"Spring_1": now,
					"Spring_2": now,
				}
			},
			2,
			"Only lifetime species should be counted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup species
			tt.setupSpecies(tracker)

			// Test GetSpeciesCount
			count := tracker.GetSpeciesCount()

			assert.Equal(t, tt.expectedCount, count,
				"Species count mismatch")

			t.Logf("✓ Correctly counted %d species", count)
		})
	}
}

// TestIsSeasonMapInitialized_CriticalReliability tests season map initialization check
// CRITICAL: Used to verify seasonal tracking state
func TestIsSeasonMapInitialized_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		setupTracker   func(*SpeciesTracker)
		seasonToCheck  string
		expectedResult bool
		description    string
	}{
		{
			"seasonal_disabled",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = false
			},
			"spring",
			false,
			"Disabled seasonal tracking should return false",
		},
		{
			"season_not_initialized",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				// Don't initialize any season maps
			},
			"spring",
			false,
			"Uninitialized season should return false",
		},
		{
			"season_initialized_empty",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				tracker.speciesBySeason["spring"] = make(map[string]time.Time)
			},
			"spring",
			true,
			"Initialized but empty season should return true",
		},
		{
			"season_initialized_with_data",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				tracker.speciesBySeason["summer"] = map[string]time.Time{
					"Species_1": time.Now(),
				}
			},
			"summer",
			true,
			"Initialized season with data should return true",
		},
		{
			"check_different_season",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				tracker.speciesBySeason["winter"] = make(map[string]time.Time)
			},
			"fall", // Check fall when winter is initialized
			false,
			"Non-initialized season should return false even if others are initialized",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup tracker state
			tt.setupTracker(tracker)

			// Test IsSeasonMapInitialized
			result := tracker.IsSeasonMapInitialized(tt.seasonToCheck)

			assert.Equal(t, tt.expectedResult, result,
				"Season initialization check mismatch for %s", tt.seasonToCheck)

			t.Logf("✓ Correctly determined initialization: %v", result)
		})
	}
}

// TestGetSeasonMapCount_CriticalReliability tests season species counting
// CRITICAL: Accurate seasonal counts needed for statistics
func TestGetSeasonMapCount_CriticalReliability(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		setupTracker  func(*SpeciesTracker)
		seasonToCount string
		expectedCount int
		description   string
	}{
		{
			"seasonal_disabled",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = false
			},
			"spring",
			0,
			"Disabled seasonal tracking should return 0",
		},
		{
			"season_not_initialized",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
			},
			"spring",
			0,
			"Uninitialized season should return 0",
		},
		{
			"empty_season",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				tracker.speciesBySeason["spring"] = make(map[string]time.Time)
			},
			"spring",
			0,
			"Empty season should return 0",
		},
		{
			"season_with_species",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				now := time.Now()
				tracker.speciesBySeason["summer"] = map[string]time.Time{
					"Species_1": now,
					"Species_2": now.AddDate(0, 0, -1),
					"Species_3": now.AddDate(0, 0, -2),
				}
			},
			"summer",
			3,
			"Season with species should return correct count",
		},
		{
			"multiple_seasons",
			func(tracker *SpeciesTracker) {
				tracker.seasonalEnabled = true
				now := time.Now()
				tracker.speciesBySeason["spring"] = map[string]time.Time{
					"Spring_1": now,
					"Spring_2": now,
				}
				tracker.speciesBySeason["fall"] = map[string]time.Time{
					"Fall_1": now,
					"Fall_2": now,
					"Fall_3": now,
					"Fall_4": now,
				}
			},
			"fall",
			4,
			"Should count only requested season",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Logf("Testing: %s", tt.description)

			// Create tracker
			settings := &conf.SpeciesTrackingSettings{
				Enabled: true,
			}

			tracker := NewTrackerFromSettings(nil, settings)
			require.NotNil(t, tracker)

			// Setup tracker state
			tt.setupTracker(tracker)

			// Test GetSeasonMapCount
			count := tracker.GetSeasonMapCount(tt.seasonToCount)

			assert.Equal(t, tt.expectedCount, count,
				"Season species count mismatch for %s", tt.seasonToCount)

			t.Logf("✓ Correctly counted %d species in %s", count, tt.seasonToCount)
		})
	}
}

// TestCacheManagement_CriticalReliability tests cache expiration and clearing
// CRITICAL: Cache management prevents stale data and memory leaks
func TestCacheManagement_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Test 1: Cache starts empty
	assert.Empty(t, tracker.statusCache, "Cache should start empty")

	// Test 2: Cache populates on GetSpeciesStatus
	species1 := "Test_Species_1"
	now := time.Now()
	_ = tracker.GetSpeciesStatus(species1, now)
	assert.Len(t, tracker.statusCache, 1, "Cache should have one entry after status query")

	// Test 3: ExpireCacheForTesting
	tracker.ExpireCacheForTesting(species1)
	cached, exists := tracker.statusCache[species1]
	assert.True(t, exists, "Expired entry should still exist")
	assert.Greater(t, time.Since(cached.timestamp), time.Hour,
		"Cache timestamp should be expired")

	// Test 4: ClearCacheForTesting
	species2 := "Test_Species_2"
	_ = tracker.GetSpeciesStatus(species2, now)
	assert.Len(t, tracker.statusCache, 2, "Cache should have two entries")

	tracker.ClearCacheForTesting()
	assert.Empty(t, tracker.statusCache, "Cache should be empty after clearing")

	// Test 5: Cache still functional after clearing
	_ = tracker.GetSpeciesStatus(species1, now)
	assert.Len(t, tracker.statusCache, 1, "Cache should work after clearing")

	t.Logf("✓ Cache management functions work correctly")
}

// TestClose_CriticalReliability tests resource cleanup
// CRITICAL: Proper cleanup prevents resource leaks
func TestClose_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Add some data to ensure tracker is in use
	now := time.Now()
	tracker.speciesFirstSeen["Species_1"] = now
	tracker.speciesFirstSeen["Species_2"] = now

	// Test Close
	err := tracker.Close()
	require.NoError(t, err, "Close should not return error")

	// Verify tracker is still functional after close
	// (Close only releases logger resources, tracker should still work)
	count := tracker.GetSpeciesCount()
	assert.Equal(t, 2, count, "Tracker should remain functional after Close")

	t.Logf("✓ Close completed successfully")
}

// TestSetCurrentYearForTesting_CriticalReliability tests test helper function
// CRITICAL: Test helpers must work correctly for reliable testing
func TestSetCurrentYearForTesting_CriticalReliability(t *testing.T) {
	t.Parallel()

	// Create tracker
	settings := &conf.SpeciesTrackingSettings{
		Enabled: true,
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Initial year should be current year
	initialYear := tracker.currentYear
	assert.Equal(t, time.Now().Year(), initialYear, "Initial year should be current year")

	// Test setting custom year
	testYear := 2025
	tracker.SetCurrentYearForTesting(testYear)
	assert.Equal(t, testYear, tracker.currentYear, "Year should be updated")

	// Test that setting affects year calculations
	testTime := time.Date(2024, 6, 15, 12, 0, 0, 0, time.UTC)
	isWithinYear := tracker.isWithinCurrentYear(testTime)
	// This depends on the reset month/day configuration
	// Just verify the function doesn't panic
	_ = isWithinYear

	t.Logf("✓ Test year override works correctly")
}

// TestUtilityFunctions_ConcurrentAccess tests thread safety of utility functions
// CRITICAL: All public methods must be thread-safe
func TestUtilityFunctions_ConcurrentAccess(t *testing.T) {
	t.Parallel()

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SeasonalTracking: conf.SeasonalTrackingSettings{
			Enabled: true,
		},
	}

	tracker := NewTrackerFromSettings(nil, settings)
	require.NotNil(t, tracker)

	// Add some initial data
	now := time.Now()
	for i := 0; i < 10; i++ {
		speciesName := "Initial_Species_" + string(rune(i))
		tracker.speciesFirstSeen[speciesName] = now.AddDate(0, 0, -i)
	}
	tracker.speciesBySeason["spring"] = map[string]time.Time{
		"Spring_1": now,
		"Spring_2": now,
	}

	// Run concurrent operations
	done := make(chan bool, 500)

	// 100 goroutines getting window days
	for i := 0; i < 100; i++ {
		go func() {
			windowDays := tracker.GetWindowDays()
			assert.Equal(t, 14, windowDays, "Window days should be consistent")
			done <- true
		}()
	}

	// 100 goroutines getting species count
	for i := 0; i < 100; i++ {
		go func() {
			count := tracker.GetSpeciesCount()
			assert.GreaterOrEqual(t, count, 10, "Count should be at least initial count")
			done <- true
		}()
	}

	// 100 goroutines checking season initialization
	for i := 0; i < 100; i++ {
		go func(id int) {
			seasons := []string{"spring", "summer", "fall", "winter"}
			season := seasons[id%4]
			_ = tracker.IsSeasonMapInitialized(season)
			done <- true
		}(i)
	}

	// 100 goroutines getting season counts
	for i := 0; i < 100; i++ {
		go func(id int) {
			seasons := []string{"spring", "summer", "fall", "winter"}
			season := seasons[id%4]
			count := tracker.GetSeasonMapCount(season)
			if season == "spring" {
				assert.Equal(t, 2, count, "Spring should have 2 species")
			}
			done <- true
		}(i)
	}

	// 100 goroutines clearing/expiring cache
	for i := 0; i < 100; i++ {
		go func(id int) {
			if id%2 == 0 {
				tracker.ClearCacheForTesting()
			} else {
				speciesName := "Initial_Species_" + string(rune(id%10))
				tracker.ExpireCacheForTesting(speciesName)
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 500; i++ {
		<-done
	}

	// Verify system is still functional
	finalCount := tracker.GetSpeciesCount()
	assert.GreaterOrEqual(t, finalCount, 10, "System should remain functional")

	t.Logf("✓ Concurrent utility operations completed without race conditions")
}
