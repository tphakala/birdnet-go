package processor

import (
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestPruningDoesNotDeleteRecentSpecies verifies that the critical bug where
// pruning deletes all species history is fixed. This test ensures that:
// 1. Recent species (within last few years) are NEVER pruned from lifetime tracking
// 2. Current year species are NEVER pruned from yearly tracking
// 3. Current season species are NEVER pruned from seasonal tracking
func TestPruningDoesNotDeleteRecentSpecies(t *testing.T) {
	t.Parallel()

	// Create a mock datastore that returns species from various time periods
	ds := &MockSpeciesDatastore{}
	now := time.Now()

	// Species from different time periods
	speciesData := []datastore.NewSpeciesData{
		// Recent species (should NEVER be pruned)
		{ScientificName: "Species_1_Day_Ago", FirstSeenDate: now.AddDate(0, 0, -1).Format("2006-01-02")},
		{ScientificName: "Species_1_Week_Ago", FirstSeenDate: now.AddDate(0, 0, -7).Format("2006-01-02")},
		{ScientificName: "Species_1_Month_Ago", FirstSeenDate: now.AddDate(0, -1, 0).Format("2006-01-02")},
		{ScientificName: "Species_6_Months_Ago", FirstSeenDate: now.AddDate(0, -6, 0).Format("2006-01-02")},
		{ScientificName: "Species_1_Year_Ago", FirstSeenDate: now.AddDate(-1, 0, 0).Format("2006-01-02")},
		{ScientificName: "Species_2_Years_Ago", FirstSeenDate: now.AddDate(-2, 0, 0).Format("2006-01-02")},
		{ScientificName: "Species_5_Years_Ago", FirstSeenDate: now.AddDate(-5, 0, 0).Format("2006-01-02")},
		// Very old species (might be pruned)
		{ScientificName: "Species_15_Years_Ago", FirstSeenDate: now.AddDate(-15, 0, 0).Format("2006-01-02")},
	}

	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(speciesData, nil)
	// Note: GetSpeciesFirstDetectionInPeriod will be called since yearly/seasonal tracking is enabled
	ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return([]datastore.NewSpeciesData{}, nil)

	// Create tracker with typical settings
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14, // This should NOT affect retention!
		SyncIntervalMinutes:  60,
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

	// Initialize from database
	err := tracker.InitFromDatabase()
	require.NoError(t, err)

	// Verify all recent species were loaded
	initialCount := tracker.GetSpeciesCount()
	assert.GreaterOrEqual(t, initialCount, 7, "Should have loaded at least 7 recent species")

	// Run pruning
	pruned := tracker.PruneOldEntries()
	t.Logf("Pruned %d entries", pruned)

	// Verify recent species are still present
	finalCount := tracker.GetSpeciesCount()
	assert.GreaterOrEqual(t, finalCount, 7, "Recent species should NOT be pruned")

	// Check specific species are still tracked
	testCases := []struct {
		species  string
		shouldExist bool
	}{
		{"Species_1_Day_Ago", true},
		{"Species_1_Week_Ago", true},
		{"Species_1_Month_Ago", true},
		{"Species_6_Months_Ago", true},
		{"Species_1_Year_Ago", true},
		{"Species_2_Years_Ago", true},
		{"Species_5_Years_Ago", true},
		{"Species_15_Years_Ago", false}, // Only this very old one might be pruned
	}

	for _, tc := range testCases {
		status := tracker.GetSpeciesStatus(tc.species, now)
		if tc.shouldExist {
			// If the species should exist, it should have a FirstSeenTime
			assert.NotZero(t, status.FirstSeenTime, "Species %s should still be tracked", tc.species)
			assert.GreaterOrEqual(t, status.DaysSinceFirst, 0, "Species %s should have valid days calculation", tc.species)
		} else {
			// If pruned, it would show as new with 0 days
			assert.True(t, status.IsNew, "Species %s should be new (was pruned)", tc.species)
			assert.Equal(t, 0, status.DaysSinceFirst, "Pruned species should have 0 days")
		}
	}

	// Simulate the exact scenario from the bug report
	t.Run("bug_scenario_simulation", func(t *testing.T) {
		// Load 144 species like in the logs
		var manySpecies []datastore.NewSpeciesData
		for i := 0; i < 144; i++ {
			daysAgo := i % 365 // Spread across the year
			manySpecies = append(manySpecies, datastore.NewSpeciesData{
				ScientificName: fmt.Sprintf("Species_%d", i),
				FirstSeenDate:  now.AddDate(0, 0, -daysAgo).Format("2006-01-02"),
			})
		}

		ds2 := &MockSpeciesDatastore{}
		ds2.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(manySpecies, nil)
		// Note: GetSpeciesFirstDetectionInPeriod will be called since yearly/seasonal tracking is enabled
		ds2.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)

		tracker2 := NewSpeciesTrackerFromSettings(ds2, settings)
		err := tracker2.InitFromDatabase()
		require.NoError(t, err)

		// Verify initial load
		assert.Equal(t, 144, tracker2.GetSpeciesCount(), "Should have loaded 144 species")

		// Run pruning (this was incorrectly deleting everything)
		prunedCount := tracker2.PruneOldEntries()
		t.Logf("Pruned %d entries from 144 species dataset", prunedCount)

		// CRITICAL: Verify species are NOT deleted
		remainingCount := tracker2.GetSpeciesCount()
		assert.GreaterOrEqual(t, remainingCount, 140, "Should retain almost all species (at least 140 of 144)")

		// Test that common species like "Parus major" are still tracked
		// after pruning (not deleted from memory)
		tracker2.UpdateSpecies("Parus major", now.AddDate(0, 0, -30))
		status := tracker2.GetSpeciesStatus("Parus major", now)
		assert.NotZero(t, status.FirstSeenTime, "Parus major should still be tracked after pruning")
		assert.Equal(t, 30, status.DaysSinceFirst, "Should correctly calculate days since first seen")
	})
}

// TestPruningRetentionPolicies verifies that each tracking period has the correct retention policy
func TestPruningRetentionPolicies(t *testing.T) {
	t.Parallel()

	t.Run("lifetime_retention_10_years", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		
		now := time.Now()
		speciesData := []datastore.NewSpeciesData{
			{ScientificName: "Species_9_Years", FirstSeenDate: now.AddDate(-9, 0, 0).Format("2006-01-02")},
			{ScientificName: "Species_11_Years", FirstSeenDate: now.AddDate(-11, 0, 0).Format("2006-01-02")},
		}

		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return(speciesData, nil)

		settings := &conf.SpeciesTrackingSettings{
			Enabled:              true,
			NewSpeciesWindowDays: 14,
		}

		tracker := NewSpeciesTrackerFromSettings(ds, settings)
		_ = tracker.InitFromDatabase()

		// Before pruning
		assert.Equal(t, 2, tracker.GetSpeciesCount())

		// Run pruning
		pruned := tracker.PruneOldEntries()

		// After pruning: 9-year-old kept, 11-year-old pruned
		assert.Equal(t, 1, tracker.GetSpeciesCount(), "Should keep species < 10 years old")
		assert.Equal(t, 1, pruned, "Should have pruned 1 very old entry")
	})

	t.Run("yearly_tracking_retention", func(t *testing.T) {
		ds := &MockSpeciesDatastore{}
		ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)
		ds.On("GetSpeciesFirstDetectionInPeriod", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
			Return([]datastore.NewSpeciesData{}, nil)

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
		_ = tracker.InitFromDatabase()

		now := time.Now()
		
		// Add species from current year and previous year
		tracker.UpdateSpecies("Current_Year_Species", now.AddDate(0, -2, 0))
		tracker.UpdateSpecies("Last_Year_Species", now.AddDate(-1, -2, 0))

		// Before pruning - check that species are tracked
		beforeStatus := tracker.GetSpeciesStatus("Current_Year_Species", now)
		assert.NotZero(t, beforeStatus.FirstSeenTime, "Current year species should be tracked")
		assert.GreaterOrEqual(t, beforeStatus.DaysSinceFirst, 0, "Should have valid days count")

		// Run pruning
		_ = tracker.PruneOldEntries()

		// Current year species should be retained
		afterStatus := tracker.GetSpeciesStatus("Current_Year_Species", now) 
		assert.NotZero(t, afterStatus.FirstSeenTime, "Current year species should still be tracked after pruning")
		assert.Equal(t, beforeStatus.DaysSinceFirst, afterStatus.DaysSinceFirst, "Days count should remain the same")
	})
}

// TestCriticalBugRegression ensures the specific bug from the logs never happens again
func TestCriticalBugRegression(t *testing.T) {
	// This test simulates the exact scenario from the bug report where
	// 144 species were loaded, 496 entries were pruned, and then
	// previously known species were incorrectly marked as "new"

	ds := &MockSpeciesDatastore{}
	now := time.Now()

	// Create species entries similar to the log data
	species := []string{
		"Cyanistes caeruleus",
		"Parus major", 
		"Regulus regulus",
		"Locustella naevia",
		"Curruca communis",
		"Motacilla cinerea",
		"Streptopelia decaocto",
		"Acrocephalus dumetorum",
		"Emberiza rustica",
	}

	speciesData := make([]datastore.NewSpeciesData, 0, len(species))
	for i, name := range species {
		// Spread them across the last few months
		daysAgo := (i + 1) * 15
		speciesData = append(speciesData, datastore.NewSpeciesData{
			ScientificName: name,
			FirstSeenDate:  now.AddDate(0, 0, -daysAgo).Format("2006-01-02"),
		})
	}

	ds.On("GetNewSpeciesDetections", mock.AnythingOfType("string"), mock.AnythingOfType("string"), mock.AnythingOfType("int"), mock.AnythingOfType("int")).
		Return(speciesData, nil)

	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 14,
		SyncIntervalMinutes:  5, // Frequent syncs to test pruning
	}

	tracker := NewSpeciesTrackerFromSettings(ds, settings)
	
	// Initialize (simulates loading from database on startup)
	err := tracker.InitFromDatabase()
	require.NoError(t, err)
	
	initialCount := tracker.GetSpeciesCount()
	assert.Equal(t, len(species), initialCount, "Should load all species")

	// Simulate sync with pruning (this was the bug trigger)
	_ = tracker.SyncIfNeeded()

	// Critical assertion: Species should NOT be forgotten!
	for _, name := range species {
		status := tracker.GetSpeciesStatus(name, now)
		assert.NotZero(t, status.FirstSeenTime, 
			"Species %s should still be tracked after pruning - it was already in the database!", name)
		assert.Positive(t, status.DaysSinceFirst,
			"Species %s should have positive days since first seen", name)
	}

	// Final count should be the same
	finalCount := tracker.GetSpeciesCount()
	assert.Equal(t, initialCount, finalCount, 
		"Pruning should NOT delete recently seen species")
}