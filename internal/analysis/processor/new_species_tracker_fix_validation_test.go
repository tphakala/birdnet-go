package processor

import (
	"fmt"
	"math/rand"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestSpeciesTrackerValidation covers validation edge cases and precision issues
func TestSpeciesTrackerValidation(t *testing.T) {
	t.Parallel()

	baseTime := time.Date(2023, 1, 2, 15, 4, 5, 0, time.UTC)

	t.Run("input validation and error handling", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		_ = tracker.InitFromDatabase()

		testCases := []struct {
			name        string
			speciesName string
			time        time.Time
			expectNew   bool
			description string
		}{
			{
				name:        "valid_species_name",
				speciesName: "Robin",
				time:        baseTime,
				expectNew:   true,
				description: "Normal species name should work",
			},
			{
				name:        "empty_species_name",
				speciesName: "",
				time:        baseTime.Add(time.Hour),
				expectNew:   false,
				description: "Empty species name should be handled gracefully",
			},
			{
				name:        "very_long_species_name",
				speciesName: "Very_Long_Species_Name_That_Exceeds_Normal_Length_Expectations_And_Tests_System_Limits_For_String_Handling",
				time:        baseTime.Add(2 * time.Hour),
				expectNew:   true,
				description: "Long species names should be handled",
			},
			{
				name:        "species_with_special_characters",
				speciesName: "Robin (Turdus migratorius) - North America",
				time:        baseTime.Add(3 * time.Hour),
				expectNew:   true,
				description: "Species names with special characters should work",
			},
		}

		for _, tc := range testCases {
			isNew, days := tracker.CheckAndUpdateSpecies(tc.speciesName, tc.time)

			if tc.speciesName == "" {
				// Empty species names might be rejected or handled specially
				t.Logf("Empty species handling: isNew=%v, days=%d", isNew, days)
			} else {
				assert.Equal(t, tc.expectNew, isNew, "Case %s: %s", tc.name, tc.description)
				
				// Days should never be negative
				assert.GreaterOrEqual(t, days, 0, "Case %s: days should never be negative", tc.name)
			}
		}

		ds.AssertExpectations(t)
	})

	// Address CodeRabbit comment: Add true concurrency testing
	t.Run("concurrent_operations_with_mixed_timestamps", func(t *testing.T) {
		t.Parallel()
		
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 7,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		_ = tracker.InitFromDatabase()

		// Test concurrent operations with varied timestamps
		const numGoroutines = 50
		const operationsPerGoroutine = 20
		
		var wg sync.WaitGroup
		resultsCh := make(chan struct {
			species       string
			isNew         bool
			days          int
			timestamp     time.Time
			goroutineID   int
			operationID   int
		}, numGoroutines*operationsPerGoroutine)

		// Spawn many goroutines with randomized timestamps
		for i := 0; i < numGoroutines; i++ {
			wg.Add(1)
			go func(goroutineID int) {
				defer wg.Done()
				
				// Create a local random source for this goroutine to avoid contention
				localRand := rand.New(rand.NewSource(time.Now().UnixNano() + int64(goroutineID)))
				
				for opID := 0; opID < operationsPerGoroutine; opID++ {
					// Mix past and future timestamps with randomized offsets
					var timestamp time.Time
					species := fmt.Sprintf("ConcurrentSpecies_%d", goroutineID%10) // Share some species across goroutines
					
					switch opID % 4 {
					case 0:
						// Past timestamp
						offset := time.Duration(localRand.Intn(30*24)) * time.Hour // 0-30 days ago
						timestamp = baseTime.Add(-offset)
					case 1:
						// Future timestamp  
						offset := time.Duration(localRand.Intn(7*24)) * time.Hour // 0-7 days in future
						timestamp = baseTime.Add(offset)
					case 2:
						// Very recent past
						offset := time.Duration(localRand.Intn(60)) * time.Minute // 0-60 minutes ago
						timestamp = baseTime.Add(-offset)
					case 3:
						// Near future
						offset := time.Duration(localRand.Intn(60)) * time.Minute // 0-60 minutes ahead
						timestamp = baseTime.Add(offset)
					}
					
					isNew, days := tracker.CheckAndUpdateSpecies(species, timestamp)
					
					// Collect result via channel for thread-safe validation
					resultsCh <- struct {
						species       string
						isNew         bool
						days          int
						timestamp     time.Time
						goroutineID   int
						operationID   int
					}{species, isNew, days, timestamp, goroutineID, opID}
				}
			}(i)
		}

		// Wait for all goroutines to complete
		wg.Wait()
		close(resultsCh)

		// Validate results from main thread (thread-safe)
		negativeCount := 0
		totalOperations := 0
		
		for result := range resultsCh {
			totalOperations++
			
			// Critical assertion: days should never be negative 
			if result.days < 0 {
				negativeCount++
				t.Errorf("Goroutine %d operation %d: species %s at time %v produced negative days: %d", 
					result.goroutineID, result.operationID, result.species, result.timestamp, result.days)
			}
			
			assert.GreaterOrEqual(t, result.days, 0, 
				"Goroutine %d operation %d should not produce negative days", 
				result.goroutineID, result.operationID)
		}

		// Verify we processed all expected operations
		expectedOperations := numGoroutines * operationsPerGoroutine
		assert.Equal(t, expectedOperations, totalOperations, "Should have processed all operations")
		
		// Verify defensive clamp worked
		assert.Equal(t, 0, negativeCount, "No operations should produce negative days due to defensive clamping")

		t.Logf("Processed %d concurrent operations across %d goroutines with 0 negative day calculations", 
			totalOperations, numGoroutines)

		ds.AssertExpectations(t)
	})
}

// TestPrecisionEdgeCases tests specific edge cases that might cause precision issues
func TestPrecisionEdgeCases(t *testing.T) {
	t.Parallel()

	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.NewSpeciesData{}, nil)

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

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	require.NotNil(t, tracker)
	require.NoError(t, tracker.InitFromDatabase())

	// Test edge cases that might cause precision issues
	baseTime := time.Now()
	species := "Precision_Edge_Case_Species"

	edgeCases := []struct {
		name   string
		offset time.Duration
	}{
		{"1_nanosecond_difference", 1 * time.Nanosecond},
		{"1_microsecond_difference", 1 * time.Microsecond},
		{"1_millisecond_difference", 1 * time.Millisecond},
		{"exactly_24_hours", 24 * time.Hour},
		{"just_under_24_hours", 24*time.Hour - 1*time.Nanosecond},
		{"just_over_24_hours", 24*time.Hour + 1*time.Nanosecond},
	}

	for _, tc := range edgeCases {
		t.Run(tc.name, func(t *testing.T) {
			speciesName := species + "_" + tc.name

			// First detection
			firstTime := baseTime
			isNew1, days1 := tracker.CheckAndUpdateSpecies(speciesName, firstTime)
			assert.True(t, isNew1, "First detection should be new")
			assert.Equal(t, 0, days1, "First detection should have 0 days")

			// Second detection with precise time offset
			secondTime := baseTime.Add(tc.offset)
			isNew2, days2 := tracker.CheckAndUpdateSpecies(speciesName, secondTime)

			// Critical assertion: should never be negative
			assert.GreaterOrEqual(t, days2, 0, 
				"Edge case %s should not produce negative days", tc.name)

			// Verify the second detection behavior makes sense
			if secondTime.Before(firstTime) {
				// If second time is earlier, it should be marked as new (earliest detection)
				assert.True(t, isNew2, "Earlier detection should be marked as new")
				assert.Equal(t, 0, days2, "Earlier detection should have 0 days")
			} else {
				// If second time is later, days calculation should be reasonable
				assert.GreaterOrEqual(t, days2, 0, "Later detection should have non-negative days")
			}

			// Address CodeRabbit comment: Tighten check for very small offsets
			if tc.offset < 24*time.Hour {
				assert.LessOrEqual(t, days2, 1, 
					"Small time offset should produce 0 or 1 days")
				// For sub-day offsets when species was just added, expect isNew2 to be true and 0 days
				if tc.offset < time.Hour && !secondTime.Before(firstTime) {
					assert.True(t, isNew2, "Sub-hour offset should still be considered new")
					assert.Equal(t, 0, days2, "Sub-hour offset should have 0 days")
				}
			}

			t.Logf("Edge case %s: offset=%v, days=%d", tc.name, tc.offset, days2)
		})
	}

	// Assert mock expectations
	ds.AssertExpectations(t)
}

func TestBoundaryConditions(t *testing.T) {
	t.Parallel()
	
	ds := &MockSpeciesDatastore{}
	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 7,
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	_ = tracker.InitFromDatabase()

	baseTime := time.Date(2023, 6, 15, 12, 0, 0, 0, time.UTC)
	speciesName := "BoundaryTestSpecies"

	// Test exact window boundary using fixed baseTime
	testCases := []struct {
		name        string
		timeOffset  time.Duration
		expectNew   bool
		description string
	}{
		{
			name:        "first_detection",
			timeOffset:  0,
			expectNew:   true,
			description: "First detection should be new",
		},
		{
			name:        "within_window",
			timeOffset:  3 * 24 * time.Hour, // 3 days later
			expectNew:   false,
			description: "Detection within window should not be new",
		},
		{
			name:        "at_boundary",
			timeOffset:  7 * 24 * time.Hour, // Exactly 7 days later
			expectNew:   false,
			description: "Detection at window boundary should not be new",
		},
		{
			name:        "outside_window",
			timeOffset:  8 * 24 * time.Hour, // 8 days later (outside 7-day window)
			expectNew:   true,
			description: "Detection outside window should be new",
		},
	}

	for _, tc := range testCases {
		testTime := baseTime.Add(tc.timeOffset)
		isNew, days := tracker.CheckAndUpdateSpecies(speciesName, testTime)

		assert.Equal(t, tc.expectNew, isNew, "Case %s: %s", tc.name, tc.description)
		assert.GreaterOrEqual(t, days, 0, "Case %s: days should never be negative", tc.name)

		// Validate time calculations are consistent with fixed baseTime
		if days > 0 {
			// Days calculation should match expected time difference
			expectedDays := int(tc.timeOffset.Hours() / 24)
			if expectedDays < 0 {
				expectedDays = 0 // Clamp negative to 0
			}
			// Allow some tolerance for different calculation methods
			assert.InDelta(t, expectedDays, days, 1, 
				"Case %s: days calculation should be approximately correct", tc.name)
		}
	}

	ds.AssertExpectations(t)
}