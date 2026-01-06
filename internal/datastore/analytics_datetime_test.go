// analytics_datetime_test.go
// Test to verify the fix for issue #1239 - race condition with last_heard timestamp
package datastore

import (
	"context"
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
	summaries, err := ds.GetSpeciesSummaryData(context.Background(), "", "")
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
	summariesAfter, err := ds.GetSpeciesSummaryData(context.Background(), "", "")
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

	summaries, err := ds.GetSpeciesSummaryData(context.Background(), "", "")
	require.NoError(t, err, "Should handle edge case times")
	require.Len(t, summaries, 1, "Should have 1 species")

	// The last seen should be 23:59:59 (latest time on the same day)
	expectedLastSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-01 23:59:59", time.Local)
	assert.Equal(t, expectedLastSeen, summaries[0].LastSeen, "Should correctly identify latest time")

	// The first seen should be 00:00:00 (earliest time)
	expectedFirstSeen, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-01 00:00:00", time.Local)
	assert.Equal(t, expectedFirstSeen, summaries[0].FirstSeen, "Should correctly identify earliest time")
}

// TestGetDateTimeFormat tests the database-specific datetime formatting function
func TestGetDateTimeFormat(t *testing.T) {
	t.Parallel()

	t.Run("SQLite format", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t) // setupTestDB creates SQLite database

		format := ds.GetDateTimeFormat()
		expected := "datetime(date || ' ' || time)"
		assert.Equal(t, expected, format, "Should return SQLite datetime format")

		// Test the new GetDateTimeExpr with custom column names
		customFormat := ds.GetDateTimeExpr("created_date", "created_time")
		expectedCustom := "datetime(created_date || ' ' || created_time)"
		assert.Equal(t, expectedCustom, customFormat, "Should return SQLite datetime format with custom columns")
	})

	t.Run("nil dialector", func(t *testing.T) {
		t.Parallel()
		ds := &DataStore{DB: nil} // No database initialized

		format := ds.GetDateTimeFormat()
		assert.Empty(t, format, "Should return empty string for nil dialector")

		// Also test with an empty DataStore struct
		ds2 := &DataStore{}
		format2 := ds2.GetDateTimeFormat()
		assert.Empty(t, format2, "Should return empty string for uninitialized DataStore")
	})
}

// TestGetDateTimeFormatIntegration tests the integration of GetDateTimeFormat with actual queries
func TestGetDateTimeFormatIntegration(t *testing.T) {
	t.Parallel()
	ds := setupTestDB(t)

	// Insert test data
	testNote := Note{
		ScientificName: "Integration Test",
		CommonName:     "Test Species",
		SpeciesCode:    "test",
		Date:           "2024-01-15",
		Time:           "14:30:00",
		Confidence:     0.85,
	}
	err := ds.DB.Create(&testNote).Error
	require.NoError(t, err)

	// Test that GetSpeciesSummaryData works with the datetime format
	summaries, err := ds.GetSpeciesSummaryData(context.Background(), "", "")
	require.NoError(t, err, "Query should succeed with datetime format")
	require.Len(t, summaries, 1, "Should return one species")

	// Verify the datetime was parsed correctly
	expectedTime, err := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-15 14:30:00", time.Local)
	require.NoError(t, err, "Time parsing should not fail")
	assert.Equal(t, expectedTime, summaries[0].FirstSeen, "FirstSeen should be parsed correctly")
	assert.Equal(t, expectedTime, summaries[0].LastSeen, "LastSeen should be parsed correctly")
}

// TestGetSpeciesSummaryDataErrorHandling tests error handling for unsupported database types
func TestGetSpeciesSummaryDataErrorHandling(t *testing.T) {
	t.Parallel()

	// This test would ideally use a mock to simulate an unsupported database
	// For now, we test with a valid database and ensure no errors occur
	ds := setupTestDB(t)

	// Insert test data
	testNote := Note{
		ScientificName: "Error Test",
		CommonName:     "Error Species",
		SpeciesCode:    "error",
		Date:           "2024-01-15",
		Time:           "14:30:00",
		Confidence:     0.85,
	}
	err := ds.DB.Create(&testNote).Error
	require.NoError(t, err)

	// Test that the function handles supported databases correctly
	summaries, err := ds.GetSpeciesSummaryData(context.Background(), "", "")
	require.NoError(t, err, "Should not error with supported database")
	require.Len(t, summaries, 1, "Should return results")

	// Verify that GetDateTimeFormat returns a valid format
	format := ds.GetDateTimeFormat()
	assert.NotEmpty(t, format, "Should return non-empty format for supported database")
	// Note: This assertion is SQLite-specific since setupTestDB creates an SQLite database
	assert.Contains(t, format, "datetime", "SQLite format should contain 'datetime'")
}

// TestGetDateFormat tests the database-specific date formatting function
func TestGetDateFormat(t *testing.T) {
	t.Parallel()

	t.Run("SQLite format", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t) // setupTestDB creates SQLite database

		format := ds.GetDateFormat("time")
		expected := "date(time)"
		assert.Equal(t, expected, format, "Should return SQLite date format")
	})

	t.Run("different column names", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Test with different column names
		timeFormat := ds.GetDateFormat("time")
		assert.Equal(t, "date(time)", timeFormat, "Should format time column correctly")

		createdFormat := ds.GetDateFormat("created_at")
		assert.Equal(t, "date(created_at)", createdFormat, "Should format created_at column correctly")
	})

	t.Run("nil dialector", func(t *testing.T) {
		t.Parallel()
		ds := &DataStore{DB: nil} // No database initialized

		format := ds.GetDateFormat("time")
		assert.Empty(t, format, "Should return empty string for nil dialector")
	})
}

// TestGetHourlyWeatherWithDateFormat tests the integration of GetDateFormat with GetHourlyWeather
func TestGetHourlyWeatherWithDateFormat(t *testing.T) {
	t.Parallel()
	ds := setupTestDB(t)

	// Note: This test would require creating a HourlyWeather table and model
	// For now, we just test that the GetDateFormat method works correctly
	dateFormat := ds.GetDateFormat("time")
	assert.NotEmpty(t, dateFormat, "Should return valid date format")
	assert.Contains(t, dateFormat, "time", "Should include the column name")
}
