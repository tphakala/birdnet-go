package processor

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
)

func TestSpeciesTrackerIntegration(t *testing.T) {
	t.Parallel()

	t.Run("basic_initialization_and_species_count", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{
				{ScientificName: "Turdus migratorius", CommonName: "Robin", FirstSeenDate: "2024-01-15"},
				{ScientificName: "Cardinalis cardinalis", CommonName: "Cardinal", FirstSeenDate: "2024-02-10"}, //nolint:misspell // Cardinalis is correct scientific genus name
			}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SyncIntervalMinutes:  60,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		// Verify species count - should have loaded 2 species from database
		speciesCount := tracker.GetSpeciesCount()
		assert.Equal(t, 2, speciesCount, "Should have loaded 2 species from database")

		// Address CodeRabbit comment: Add mock expectations assertion
		ds.AssertExpectations(t)
	})

	t.Run("yearly_tracking_integration", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		
		// Setup year-specific mocks with expected calls
		yearlyData := []datastore.NewSpeciesData{
			{ScientificName: "Turdus migratorius", CommonName: "Robin", FirstSeenDate: "2024-03-01"},
		}
		
		// Lifetime data call (always made)
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		
		// Yearly data call (made when yearly tracking enabled)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(yearlyData, nil).Once()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			YearlyTracking: conf.YearlyTrackingSettings{
				Enabled:    true,
				ResetMonth: 1,
				ResetDay:   1,
				WindowDays: 30,
			},
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		// Address CodeRabbit comment: Assert yearly expectations after InitFromDatabase()
		ds.AssertExpectations(t)
	})

	t.Run("seasonal_tracking_integration", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		
		// Setup seasonal-specific mocks
		seasonalData := []datastore.NewSpeciesData{
			{ScientificName: "Turdus migratorius", CommonName: "Robin", FirstSeenDate: "2024-06-15"},
		}

		// Lifetime data call
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		
		// Seasonal data calls (made for each season when seasonal tracking enabled)
		// We expect calls for all seasons: spring, summer, fall, winter
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(seasonalData, nil).Times(4) // 4 seasons

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
			SeasonalTracking: conf.SeasonalTrackingSettings{
				Enabled:    true,
				WindowDays: 21,
			},
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		require.NotNil(t, tracker)
		
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		// Address CodeRabbit comment: Assert seasonal expectations after InitFromDatabase()
		ds.AssertExpectations(t)
	})

	t.Run("combined_tracking_workflow", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe() // Called for yearly and seasonal if enabled

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

		// Initialize tracker
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		currentTime := time.Now()

		// Test new species detection across all modes
		isNew, days := tracker.CheckAndUpdateSpecies("Brand_New_Bird", currentTime)
		assert.True(t, isNew, "Brand new bird should be new")
		assert.Equal(t, 0, days)

		// Address CodeRabbit comment: Use public helpers instead of accessing internal maps
		// Verify it's tracked in all modes using public methods
		currentSeason := tracker.computeCurrentSeason(currentTime)
		
		// Use public methods instead of accessing internal fields
		speciesCount := tracker.GetSpeciesCount()
		assert.Positive(t, speciesCount, "Should have at least one species tracked")
		
		seasonMapCount := tracker.GetSeasonMapCount(currentSeason)
		assert.Positive(t, seasonMapCount, "Should be tracked in current season")

		// Test cache behavior across modes
		for i := 0; i < 5; i++ {
			status := tracker.GetSpeciesStatus("Brand_New_Bird", currentTime)
			assert.True(t, status.IsNew, "Should remain new within window")
		}

		// Test concurrent operations across all modes
		var wg sync.WaitGroup
		for i := 0; i < 20; i++ {
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

		// Address CodeRabbit comment: Assert mocks
		ds.AssertExpectations(t)

		t.Log("✓ Combined tracking workflow completed successfully")
	})

	t.Run("error_handling_integration", func(t *testing.T) {
		t.Parallel()
		
		// Test with error-returning datastore
		errorDS := &MockSpeciesDatastore{}
		errorDS.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(nil, assert.AnError)
		errorDS.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(nil, assert.AnError).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
		}

		tracker := NewSpeciesTrackerFromSettings(errorDS, settings)
		require.NotNil(t, tracker)

		// Should handle database error gracefully
		err := tracker.InitFromDatabase()
		require.Error(t, err, "Should return error when database fails")

		// Tracker should still be functional for new operations
		currentTime := time.Now()
		isNew, days := tracker.CheckAndUpdateSpecies("Test_Species", currentTime)
		assert.True(t, isNew, "Should still work for new species despite DB error")
		assert.Equal(t, 0, days, "New species should have 0 days")

		// Address CodeRabbit comment: Assert expectations and cleanup
		errorDS.AssertExpectations(t)
	})

	t.Run("caching_and_performance_integration", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{
				{ScientificName: "Cached_Species", CommonName: "Cached Bird", FirstSeenDate: "2024-01-01"},
			}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil).Maybe()

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		currentTime := time.Now()

		// Test caching behavior
		start := time.Now()
		for i := 0; i < 100; i++ {
			_ = tracker.GetSpeciesStatus("Cached_Species", currentTime)
		}
		cacheDuration := time.Since(start)

		// Should be very fast due to caching
		assert.Less(t, cacheDuration, 10*time.Millisecond, "Cached operations should be fast")

		// Test cache invalidation
		tracker.ExpireCacheForTesting("Cached_Species")
		
		// Next call should rebuild cache
		status := tracker.GetSpeciesStatus("Cached_Species", currentTime)
		assert.False(t, status.IsNew, "Cached species loaded from DB should not be new")

		// Test pruning behavior
		pruned := tracker.PruneOldEntries()
		t.Logf("Pruned %d old entries", pruned)

		// Address CodeRabbit comment: Assert expectations before test end
		ds.AssertExpectations(t)
	})

	t.Run("notification_suppression_integration", func(t *testing.T) {
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
		err := tracker.InitFromDatabase()
		require.NoError(t, err)

		currentTime := time.Now()
		species := "Notification_Test_Species"

		// First notification should not be suppressed
		shouldSuppress1 := tracker.ShouldSuppressNotification(species, currentTime)
		assert.False(t, shouldSuppress1, "First notification should not be suppressed")

		// Record notification sent
		tracker.RecordNotificationSent(species, currentTime)

		// Immediate subsequent notification should be suppressed
		shouldSuppress2 := tracker.ShouldSuppressNotification(species, currentTime.Add(time.Minute))
		assert.True(t, shouldSuppress2, "Recent notification should be suppressed")

		// Notification after long time should not be suppressed
		futureTime := currentTime.Add(8 * 24 * time.Hour) // 8 days later
		shouldSuppress3 := tracker.ShouldSuppressNotification(species, futureTime)
		assert.False(t, shouldSuppress3, "Old notification should not suppress new one")

		// Test cleanup of old notification records
		cleaned := tracker.CleanupOldNotificationRecords(futureTime)
		t.Logf("Cleaned up %d old notification records", cleaned)

		// Address CodeRabbit comment: Assert mock expectations before test finishes
		ds.AssertExpectations(t)

		t.Log("✓ Notification suppression integration test completed")
	})
}

// BenchmarkFullWorkflow_PerformanceUnderLoad tests performance under load
// Address CodeRabbit comment: Move brittle performance assertions to benchmark
func BenchmarkFullWorkflow_PerformanceUnderLoad(b *testing.B) {
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
	_ = tracker.InitFromDatabase()
	
	currentTime := time.Now()
	species := []string{"Species_A", "Species_B", "Species_C", "Species_D", "Species_E"}

	b.ResetTimer()
	
	// Use b.RunParallel for realistic concurrency testing
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			speciesName := species[i%len(species)]
			
			// Mix different operations
			switch i % 3 {
			case 0:
				tracker.CheckAndUpdateSpecies(speciesName, currentTime)
			case 1:
				tracker.GetSpeciesStatus(speciesName, currentTime)
			case 2:
				tracker.UpdateSpecies(speciesName, currentTime)
			}
			
			i++
		}
	})
}

func TestPerformanceInformational(t *testing.T) {
	t.Parallel()
	
	// Address CodeRabbit comment: Replace hard assertion with informational logging
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
	_ = tracker.InitFromDatabase()

	// Performance test with informational logging instead of hard assertions
	const operations = 1000
	currentTime := time.Now()
	
	start := time.Now()
	for i := 0; i < operations; i++ {
		species := fmt.Sprintf("Perf_Species_%d", i%10)
		tracker.CheckAndUpdateSpecies(species, currentTime)
	}
	duration := time.Since(start)
	
	opsPerSecond := float64(operations) / duration.Seconds()
	
	// Log performance instead of asserting hard thresholds
	t.Logf("Performance: %.0f ops/sec (no hard pass/fail threshold in CI)", opsPerSecond)
	
	ds.AssertExpectations(t)
}