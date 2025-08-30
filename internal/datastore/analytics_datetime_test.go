// analytics_datetime_test.go
// Test to verify the fix for issue #1239 - race condition with last_heard timestamp
package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestGetSpeciesSummaryDataDateTimeHandling verifies that the datetime() function
// correctly handles date/time concatenation to prevent incorrect last_heard timestamps
// This test addresses issue #1239 where incorrect timestamps were shown temporarily
// when new species were detected.
func TestGetSpeciesSummaryDataDateTimeHandling(t *testing.T) {
	t.Parallel()

	// Create test database
	ds := setupTestDB(t)

	// Test data with varying time formats that could cause issues with string concatenation
	testNotes := []Note{
		{
			ScientificName: "Corvus corax",
			CommonName:     "Common Raven",
			SpeciesCode:    "comrav",
			Date:           "2024-01-15",
			Time:           "09:05:30", // Leading zero in minutes
			Confidence:     0.95,
		},
		{
			ScientificName: "Corvus corax",
			CommonName:     "Common Raven", 
			SpeciesCode:    "comrav",
			Date:           "2024-01-15",
			Time:           "15:30:45", // Later time same day
			Confidence:     0.92,
		},
		{
			ScientificName: "Corvus corax",
			CommonName:     "Common Raven",
			SpeciesCode:    "comrav",
			Date:           "2024-01-14",
			Time:           "23:59:59", // Previous day, late time
			Confidence:     0.88,
		},
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Date:           "2024-01-15",
			Time:           "08:15:00",
			Confidence:     0.91,
		},
		{
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Date:           "2024-01-16",
			Time:           "07:00:00", // Next day, early time
			Confidence:     0.89,
		},
	}

	// Insert test data
	for _, note := range testNotes {
		err := ds.DB.Create(&note).Error
		require.NoError(t, err, "Failed to insert test note")
	}

	// Test 1: Verify correct last_seen calculation for all species
	summaries, err := ds.GetSpeciesSummaryData("", "")
	require.NoError(t, err, "GetSpeciesSummaryData should not return error")
	require.Len(t, summaries, 2, "Should have 2 species")

	// Find the raven and robin summaries
	var ravenSummary, robinSummary *SpeciesSummaryData
	for i := range summaries {
		switch summaries[i].ScientificName {
		case "Corvus corax":
			ravenSummary = &summaries[i]
		case "Turdus migratorius":
			robinSummary = &summaries[i]
		}
	}

	require.NotNil(t, ravenSummary, "Raven summary should exist")
	require.NotNil(t, robinSummary, "Robin summary should exist")

	// Verify counts
	assert.Equal(t, 3, ravenSummary.Count, "Raven should have 3 detections")
	assert.Equal(t, 2, robinSummary.Count, "Robin should have 2 detections")

	// Verify last seen times are correct
	// Raven's last detection should be 2024-01-15 15:30:45
	expectedRavenLastSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-15 15:30:45", time.Local)
	assert.Equal(t, expectedRavenLastSeen, ravenSummary.LastSeen, "Raven last_seen should be the latest detection")

	// Robin's last detection should be 2024-01-16 07:00:00
	expectedRobinLastSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-16 07:00:00", time.Local)
	assert.Equal(t, expectedRobinLastSeen, robinSummary.LastSeen, "Robin last_seen should be the latest detection")

	// Test 2: Verify first seen times are correct
	// Raven's first detection should be 2024-01-14 23:59:59
	expectedRavenFirstSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-14 23:59:59", time.Local)
	assert.Equal(t, expectedRavenFirstSeen, ravenSummary.FirstSeen, "Raven first_seen should be the earliest detection")

	// Robin's first detection should be 2024-01-15 08:15:00
	expectedRobinFirstSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-15 08:15:00", time.Local)
	assert.Equal(t, expectedRobinFirstSeen, robinSummary.FirstSeen, "Robin first_seen should be the earliest detection")

	// Test 3: Add a new species and verify existing species timestamps remain correct
	newNote := Note{
		ScientificName: "Poecile carolinensis",
		CommonName:     "Carolina Chickadee",
		SpeciesCode:    "carchi",
		Date:           "2024-01-16",
		Time:           "12:00:00",
		Confidence:     0.87,
	}
	err = ds.DB.Create(&newNote).Error
	require.NoError(t, err, "Failed to insert new species")

	// Re-query and verify existing species timestamps haven't changed
	summariesAfter, err := ds.GetSpeciesSummaryData("", "")
	require.NoError(t, err, "GetSpeciesSummaryData should not return error after new species")
	require.Len(t, summariesAfter, 3, "Should have 3 species now")

	// Find the raven summary again
	var ravenSummaryAfter *SpeciesSummaryData
	for i := range summariesAfter {
		if summariesAfter[i].ScientificName == "Corvus corax" {
			ravenSummaryAfter = &summariesAfter[i]
			break
		}
	}
	require.NotNil(t, ravenSummaryAfter, "Raven summary should still exist")

	// Verify raven's timestamps haven't changed
	assert.Equal(t, expectedRavenLastSeen, ravenSummaryAfter.LastSeen, 
		"Raven last_seen should remain unchanged after new species added")
	assert.Equal(t, expectedRavenFirstSeen, ravenSummaryAfter.FirstSeen,
		"Raven first_seen should remain unchanged after new species added")
}

// TestDateTimeFunctionConsistency verifies that datetime() function handles
// various time formats consistently
func TestDateTimeFunctionConsistency(t *testing.T) {
	t.Parallel()

	ds := setupTestDB(t)

	// Test edge cases with different time formats
	edgeCaseNotes := []Note{
		{
			ScientificName: "Test Species",
			CommonName:     "Test Bird",
			Date:           "2024-01-01",
			Time:           "00:00:00", // Midnight
			Confidence:     0.9,
		},
		{
			ScientificName: "Test Species",
			CommonName:     "Test Bird",
			Date:           "2024-01-01",
			Time:           "00:00:01", // One second after midnight
			Confidence:     0.9,
		},
		{
			ScientificName: "Test Species",
			CommonName:     "Test Bird",
			Date:           "2024-01-01",
			Time:           "23:59:59", // One second before midnight
			Confidence:     0.9,
		},
	}

	for _, note := range edgeCaseNotes {
		err := ds.DB.Create(&note).Error
		require.NoError(t, err, "Failed to insert edge case note")
	}

	summaries, err := ds.GetSpeciesSummaryData("", "")
	require.NoError(t, err, "Should handle edge case times")
	require.Len(t, summaries, 1, "Should have 1 species")

	// The last seen should be 23:59:59 (latest time on the same day)
	expectedLastSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-01 23:59:59", time.Local)
	assert.Equal(t, expectedLastSeen, summaries[0].LastSeen, "Should correctly identify latest time")

	// The first seen should be 00:00:00 (earliest time)
	expectedFirstSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-01 00:00:00", time.Local)
	assert.Equal(t, expectedFirstSeen, summaries[0].FirstSeen, "Should correctly identify earliest time")
}