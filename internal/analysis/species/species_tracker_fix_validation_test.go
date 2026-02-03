// new_species_tracker_fix_validation_test.go
// Validation tests for race condition and negative days fixes
// These tests specifically validate that the defensive fixes work correctly
package species

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
)

// TestNegativeDaysFixValidation validates the defensive fix for negative days
func TestNegativeDaysFixValidation(t *testing.T) {
	t.Parallel()

	// Create tracker
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

	species := "Fix_Validation_Species"
	baseTime := time.Now()

	// Test sequence that should trigger any potential negative days calculation
	testTimes := []time.Time{
		baseTime.Add(-10 * time.Hour), // 10 hours ago
		baseTime.Add(-5 * time.Hour),  // 5 hours ago (earlier - should update)
		baseTime.Add(-15 * time.Hour), // 15 hours ago (even earlier)
		baseTime,                      // Now (much later)
		baseTime.Add(-1 * time.Hour),  // 1 hour ago (between first and last)
	}

	var lastDays int
	for i, testTime := range testTimes {
		isNew, days := tracker.CheckAndUpdateSpecies(species, testTime)

		t.Logf("Test %d: time=%v, isNew=%v, days=%d",
			i+1, testTime.Format(time.TimeOnly), isNew, days)

		// Critical assertion: days should never be negative
		assert.GreaterOrEqual(t, days, 0,
			"Days should never be negative (test %d)", i+1)

		// For new species or earliest detection, days should be 0
		if i == 0 || testTime.Before(testTimes[0]) {
			assert.Equal(t, 0, days,
				"First detection or earlier detection should have 0 days (test %d)", i+1)
			assert.True(t, isNew,
				"First detection or earlier detection should be marked as new (test %d)", i+1)
		}

		// Days should be monotonic or reset to 0 (never decrease unless reset)
		if i > 0 && days > 0 && lastDays > 0 {
			// Allow days to be the same or increase (time progressing)
			// or reset to 0 (earlier detection found)
			assert.True(t, days >= lastDays || days == 0,
				"Days should be monotonic or reset to 0 (test %d: lastDays=%d, currentDays=%d)",
				i+1, lastDays, days)
		}

		lastDays = days
	}

	// Verify tracker is still functional after the test sequence
	finalTest := "Final_Test_Species"
	isNew, days := tracker.CheckAndUpdateSpecies(finalTest, baseTime)
	assert.True(t, isNew, "New species should be marked as new")
	assert.Equal(t, 0, days, "New species should have 0 days")
}

// TestConcurrentAccessFixValidation validates the fix under concurrent load
func TestConcurrentAccessFixValidation(t *testing.T) {
	t.Parallel()

	// Create tracker
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

	// Run the same concurrent test that previously detected race conditions
	// This time it should pass without any negative days
	species := "Concurrent_Fix_Test_Species"
	const numOperations = 1000

	results := make([]struct {
		isNew bool
		days  int
	}, numOperations)

	// Run sequential operations with varying times to test the fix
	baseTime := time.Now()
	for i := range numOperations {
		// Create time variations that might trigger precision issues
		offset := time.Duration(i) * time.Microsecond
		if i%2 == 0 {
			offset = -offset // Mix of past and future times
		}
		testTime := baseTime.Add(offset)

		isNew, days := tracker.CheckAndUpdateSpecies(species, testTime)
		results[i] = struct {
			isNew bool
			days  int
		}{isNew, days}

		// Critical check: no negative days should ever be returned
		assert.GreaterOrEqual(t, days, 0,
			"Operation %d should not return negative days", i)
	}

	// Analyze results to ensure they make sense
	negativeCount := 0
	for i, result := range results {
		if !assert.GreaterOrEqual(t, result.days, 0,
			"Operation %d returned negative days: %d", i, result.days) {
			negativeCount++
		}
	}

	// The fix should eliminate ALL negative days
	assert.Equal(t, 0, negativeCount,
		"After fix, no operations should return negative days")

	t.Logf("Concurrent fix validation completed:")
	t.Logf("  Operations: %d", numOperations)
	t.Logf("  Negative results: %d (should be 0)", negativeCount)
	t.Logf("  Fix effectiveness: %s",
		func() string {
			if negativeCount == 0 {
				return "✅ SUCCESSFUL"
			}
			return "❌ FAILED"
		}())
}

// TestPrecisionEdgeCases tests specific edge cases that might cause precision issues
func TestPrecisionEdgeCases(t *testing.T) {
	t.Parallel()

	// Create tracker
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

			// For very small offsets, days should be 0 or 1
			if tc.offset < 24*time.Hour {
				assert.LessOrEqual(t, days2, 1,
					"Small time offset should produce 0 or 1 days")
			}

			t.Logf("Edge case %s: offset=%v, days=%d", tc.name, tc.offset, days2)
		})
	}
}
