// analytics_test.go: Tests for datastore analytics functions
package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

// setupTestDB creates an in-memory SQLite database for testing
func setupTestDB(t *testing.T) *DataStore {
	t.Helper()

	// Create in-memory SQLite database
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)

	// Create the notes table schema
	err = db.AutoMigrate(&Note{})
	require.NoError(t, err)

	return &DataStore{DB: db}
}

// seedTestData adds test data to the database
func seedTestData(t *testing.T, ds *DataStore) {
	t.Helper()

	// Create test notes with different species
	testNotes := []Note{
		{
			ID:             1,
			Date:           "2024-01-15",
			Time:           "08:30:00",
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Confidence:     0.85,
		},
		{
			ID:             2,
			Date:           "2024-01-15",
			Time:           "09:15:00",
			ScientificName: "Turdus migratorius",
			CommonName:     "American Robin",
			SpeciesCode:    "amerob",
			Confidence:     0.90,
		},
		{
			ID:             3,
			Date:           "2024-01-16",
			Time:           "10:00:00",
			ScientificName: "Cyanocitta cristata",
			CommonName:     "Blue Jay",
			SpeciesCode:    "blujay",
			Confidence:     0.75,
		},
		{
			ID:             4,
			Date:           "2024-01-16",
			Time:           "14:30:00",
			ScientificName: "Cyanocitta cristata",
			CommonName:     "Blue Jay",
			SpeciesCode:    "blujay1", // Different code to test MAX() aggregate
			Confidence:     0.80,
		},
		{
			ID:             5,
			Date:           "2024-01-17",
			Time:           "07:45:00",
			ScientificName: "Cardinalis cardinalis",
			CommonName:     "Northern Cardinal",
			SpeciesCode:    "norcar",
			Confidence:     0.95,
		},
	}

	for _, note := range testNotes {
		err := ds.DB.Create(&note).Error
		require.NoError(t, err)
	}
}

// TestGetSpeciesSummaryData tests the GetSpeciesSummaryData function
func TestGetSpeciesSummaryData(t *testing.T) {
	t.Parallel()

	t.Run("without date filters", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		seedTestData(t, ds)

		// Test without date filters
		summaries, err := ds.GetSpeciesSummaryData("", "")
		require.NoError(t, err)
		assert.Len(t, summaries, 3) // 3 unique species

		// Check American Robin summary
		robin := findSpeciesByScientificName(summaries, "Turdus migratorius")
		require.NotNil(t, robin)
		assert.Equal(t, "American Robin", robin.CommonName)
		assert.Equal(t, "amerob", robin.SpeciesCode)
		assert.Equal(t, 2, robin.Count)
		assert.InDelta(t, 0.875, robin.AvgConfidence, 0.001)
		assert.Equal(t, 0.90, robin.MaxConfidence)

		// Check Blue Jay summary (should have one species_code due to MAX aggregate)
		blueJay := findSpeciesByScientificName(summaries, "Cyanocitta cristata")
		require.NotNil(t, blueJay)
		assert.Equal(t, "Blue Jay", blueJay.CommonName)
		assert.Contains(t, []string{"blujay", "blujay1"}, blueJay.SpeciesCode) // MAX will pick one
		assert.Equal(t, 2, blueJay.Count)
		assert.InDelta(t, 0.775, blueJay.AvgConfidence, 0.001)
		assert.Equal(t, 0.80, blueJay.MaxConfidence)

		// Check Northern Cardinal summary
		cardinal := findSpeciesByScientificName(summaries, "Cardinalis cardinalis")
		require.NotNil(t, cardinal)
		assert.Equal(t, "Northern Cardinal", cardinal.CommonName)
		assert.Equal(t, "norcar", cardinal.SpeciesCode)
		assert.Equal(t, 1, cardinal.Count)
		assert.Equal(t, 0.95, cardinal.AvgConfidence)
		assert.Equal(t, 0.95, cardinal.MaxConfidence)
	})

	t.Run("with start date filter", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		seedTestData(t, ds)

		// Test with start date filter
		summaries, err := ds.GetSpeciesSummaryData("2024-01-16", "")
		require.NoError(t, err)
		assert.Len(t, summaries, 2) // Only Blue Jay and Northern Cardinal

		// American Robin should not be in results
		robin := findSpeciesByScientificName(summaries, "Turdus migratorius")
		assert.Nil(t, robin)
	})

	t.Run("with end date filter", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		seedTestData(t, ds)

		// Test with end date filter
		summaries, err := ds.GetSpeciesSummaryData("", "2024-01-16")
		require.NoError(t, err)
		assert.Len(t, summaries, 2) // American Robin and Blue Jay

		// Northern Cardinal should not be in results
		cardinal := findSpeciesByScientificName(summaries, "Cardinalis cardinalis")
		assert.Nil(t, cardinal)
	})

	t.Run("with date range", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		seedTestData(t, ds)

		// Test with date range
		summaries, err := ds.GetSpeciesSummaryData("2024-01-16", "2024-01-16")
		require.NoError(t, err)
		assert.Len(t, summaries, 1) // Only Blue Jay

		blueJay := findSpeciesByScientificName(summaries, "Cyanocitta cristata")
		require.NotNil(t, blueJay)
		assert.Equal(t, 2, blueJay.Count)
	})

	t.Run("empty database", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Test with empty database
		summaries, err := ds.GetSpeciesSummaryData("", "")
		require.NoError(t, err)
		assert.Empty(t, summaries)
	})

	t.Run("SQL aggregate functions work correctly", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		
		// Add notes with same scientific name but different common names and species codes
		testNotes := []Note{
			{
				Date:           "2024-01-15",
				Time:           "08:00:00",
				ScientificName: "Poecile carolinensis",
				CommonName:     "Carolina Chickadee",
				SpeciesCode:    "carchi",
				Confidence:     0.70,
			},
			{
				Date:           "2024-01-15",
				Time:           "09:00:00",
				ScientificName: "Poecile carolinensis",
				CommonName:     "Carolina Chickadee Alt", // Different common name
				SpeciesCode:    "carchi2", // Different species code
				Confidence:     0.80,
			},
			{
				Date:           "2024-01-15",
				Time:           "10:00:00",
				ScientificName: "Poecile carolinensis",
				CommonName:     "Carolina Chickadee",
				SpeciesCode:    "carchi3", // Another different species code
				Confidence:     0.90,
			},
		}

		for _, note := range testNotes {
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}

		// This query should not fail even with different species_code values
		// because we're using MAX(species_code) aggregate function
		summaries, err := ds.GetSpeciesSummaryData("", "")
		require.NoError(t, err, "Query should not fail with SQL aggregate error")
		assert.Len(t, summaries, 1)

		chickadee := summaries[0]
		assert.Equal(t, "Poecile carolinensis", chickadee.ScientificName)
		assert.Equal(t, 3, chickadee.Count)
		assert.InDelta(t, 0.80, chickadee.AvgConfidence, 0.001)
		assert.Equal(t, 0.90, chickadee.MaxConfidence)
		// MAX() should pick one of the species codes
		assert.Contains(t, []string{"carchi", "carchi2", "carchi3"}, chickadee.SpeciesCode)
	})
}

// TestGetSpeciesSummaryDataTimeFormat tests that the time parsing works correctly
func TestGetSpeciesSummaryDataTimeFormat(t *testing.T) {
	t.Parallel()
	ds := setupTestDB(t)

	// Create a note with specific date and time
	note := Note{
		Date:           "2024-01-15",
		Time:           "14:30:45",
		ScientificName: "Test species",
		CommonName:     "Test Bird",
		SpeciesCode:    "testbird",
		Confidence:     0.80,
	}
	err := ds.DB.Create(&note).Error
	require.NoError(t, err)

	summaries, err := ds.GetSpeciesSummaryData("", "")
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	// Check that FirstSeen and LastSeen are parsed correctly
	summary := summaries[0]
	expectedTime, _ := time.Parse("2006-01-02 15:04:05", "2024-01-15 14:30:45")
	assert.Equal(t, expectedTime, summary.FirstSeen)
	assert.Equal(t, expectedTime, summary.LastSeen)
}

// Helper function to find species by scientific name
func findSpeciesByScientificName(summaries []SpeciesSummaryData, scientificName string) *SpeciesSummaryData {
	for i := range summaries {
		if summaries[i].ScientificName == scientificName {
			return &summaries[i]
		}
	}
	return nil
}