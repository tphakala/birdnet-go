// analytics_test.go: Tests for datastore analytics functions
package datastore

import (
	"fmt"
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
			ScientificName: "Cardinalis cardinalis", //nolint:misspell // This is the correct scientific name
			CommonName:     "Northern Cardinal",
			SpeciesCode:    "norcar",
			Confidence:     0.95,
		},
	}

	for i := range testNotes {
		err := ds.DB.Create(&testNotes[i]).Error
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
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "", "")
		require.NoError(t, err)
		assert.Len(t, summaries, 3) // 3 unique species

		// Check American Robin summary
		robin := findSpeciesByScientificName(summaries, "Turdus migratorius")
		require.NotNil(t, robin)
		assert.Equal(t, "American Robin", robin.CommonName)
		assert.Equal(t, "amerob", robin.SpeciesCode)
		assert.Equal(t, 2, robin.Count)
		assert.InDelta(t, 0.875, robin.AvgConfidence, 0.001)
		assert.InDelta(t, 0.90, robin.MaxConfidence, 0.01)

		// Check Blue Jay summary (should have one species_code due to MAX aggregate)
		blueJay := findSpeciesByScientificName(summaries, "Cyanocitta cristata")
		require.NotNil(t, blueJay)
		assert.Equal(t, "Blue Jay", blueJay.CommonName)
		assert.Contains(t, []string{"blujay", "blujay1"}, blueJay.SpeciesCode) // MAX will pick one
		assert.Equal(t, 2, blueJay.Count)
		assert.InDelta(t, 0.775, blueJay.AvgConfidence, 0.001)
		assert.InDelta(t, 0.80, blueJay.MaxConfidence, 0.01)

		// Check Northern Cardinal summary
		cardinal := findSpeciesByScientificName(summaries, "Cardinalis cardinalis") //nolint:misspell // This is the correct scientific name
		require.NotNil(t, cardinal)
		assert.Equal(t, "Northern Cardinal", cardinal.CommonName)
		assert.Equal(t, "norcar", cardinal.SpeciesCode)
		assert.Equal(t, 1, cardinal.Count)
		assert.InDelta(t, 0.95, cardinal.AvgConfidence, 0.01)
		assert.InDelta(t, 0.95, cardinal.MaxConfidence, 0.01)
	})

	t.Run("with start date filter", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		seedTestData(t, ds)

		// Test with start date filter
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "2024-01-16", "")
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
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "", "2024-01-16")
		require.NoError(t, err)
		assert.Len(t, summaries, 2) // American Robin and Blue Jay

		// Northern Cardinal should not be in results
		cardinal := findSpeciesByScientificName(summaries, "Cardinalis cardinalis") //nolint:misspell // This is the correct scientific name
		assert.Nil(t, cardinal)
	})

	t.Run("with date range", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)
		seedTestData(t, ds)

		// Test with date range
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "2024-01-16", "2024-01-16")
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
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "", "")
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
				SpeciesCode:    "carchi2",                // Different species code
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
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "", "")
		require.NoError(t, err, "Query should not fail with SQL aggregate error")
		assert.Len(t, summaries, 1)

		chickadee := summaries[0]
		assert.Equal(t, "Poecile carolinensis", chickadee.ScientificName)
		assert.Equal(t, 3, chickadee.Count)
		assert.InDelta(t, 0.80, chickadee.AvgConfidence, 0.001)
		assert.InDelta(t, 0.90, chickadee.MaxConfidence, 0.01)
		// MAX() should pick one of the species codes
		assert.Contains(t, []string{"carchi", "carchi2", "carchi3"}, chickadee.SpeciesCode)
	})

	t.Run("NULL species_code handling", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Test that NULL species_code values (from birdnet-pi imports) don't break the query

		// Insert a note with NULL species_code using raw SQL to bypass GORM's validation
		result := ds.DB.Exec(`
			INSERT INTO notes (date, time, scientific_name, common_name, species_code, confidence)
			VALUES (?, ?, ?, ?, NULL, ?)
		`, "2024-01-20", "10:00:00", "Nullus species", "Null Bird", 0.75)
		require.NoError(t, result.Error)

		// Also insert a normal note with species_code
		err := ds.DB.Create(&Note{
			Date:           "2024-01-20",
			Time:           "11:00:00",
			ScientificName: "Normal species",
			CommonName:     "Normal Bird",
			SpeciesCode:    "norbird",
			Confidence:     0.85,
		}).Error
		require.NoError(t, err)

		// Query should not fail even with NULL species_code
		summaries, err := ds.GetSpeciesSummaryData(t.Context(), "", "")
		require.NoError(t, err, "Query should handle NULL species_code without error")
		require.Len(t, summaries, 2)

		// Find the species with NULL species_code
		nullSpecies := findSpeciesByScientificName(summaries, "Nullus species")
		require.NotNil(t, nullSpecies)
		assert.Equal(t, "Null Bird", nullSpecies.CommonName)
		assert.Empty(t, nullSpecies.SpeciesCode, "NULL species_code should be converted to empty string")
		assert.Equal(t, 1, nullSpecies.Count)

		// Also cover NULL common_name
		result2 := ds.DB.Exec(`
			INSERT INTO notes (date, time, scientific_name, common_name, species_code, confidence)
			VALUES (?, ?, ?, NULL, ?, ?)
		`, "2024-01-21", "10:00:00", "Nullus commonus", "nulcom", 0.66)
		require.NoError(t, result2.Error)

		summaries2, err := ds.GetSpeciesSummaryData(t.Context(), "", "")
		require.NoError(t, err)
		nsCommon := findSpeciesByScientificName(summaries2, "Nullus commonus")
		require.NotNil(t, nsCommon)
		assert.Empty(t, nsCommon.CommonName, "NULL common_name should be converted to empty string")
		assert.Equal(t, "nulcom", nsCommon.SpeciesCode)
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

	summaries, err := ds.GetSpeciesSummaryData(t.Context(), "", "")
	require.NoError(t, err)
	require.Len(t, summaries, 1)

	// Check that FirstSeen and LastSeen are parsed correctly
	summary := summaries[0]
	// Parse as local time to match how the database parsing works
	expectedTime, _ := time.ParseInLocation("2006-01-02 15:04:05", "2024-01-15 14:30:45", time.Local)
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

// TestGetNewSpeciesDetections tests the GetNewSpeciesDetections function
func TestGetNewSpeciesDetections(t *testing.T) {
	t.Parallel()

	t.Run("basic functionality", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Create test data with species detected at different times
		testNotes := []Note{
			// Sylvia atricapilla - first detected in July 2024
			{Date: "2024-07-28", Time: "18:47:11", ScientificName: "Sylvia atricapilla", CommonName: "Eurasian Blackcap", Confidence: 0.95},
			{Date: "2024-07-28", Time: "18:47:37", ScientificName: "Sylvia atricapilla", CommonName: "Eurasian Blackcap", Confidence: 0.92},
			{Date: "2024-07-29", Time: "09:15:00", ScientificName: "Sylvia atricapilla", CommonName: "Eurasian Blackcap", Confidence: 0.88},

			// Alcedo atthis - first detected in March 2024
			{Date: "2024-03-21", Time: "09:23:23", ScientificName: "Alcedo atthis", CommonName: "Common Kingfisher", Confidence: 0.87},
			{Date: "2024-07-21", Time: "10:30:00", ScientificName: "Alcedo atthis", CommonName: "Common Kingfisher", Confidence: 0.91},

			// Loxia leucoptera - first detected in 2023
			{Date: "2023-12-25", Time: "18:32:03", ScientificName: "Loxia leucoptera", CommonName: "White-winged Crossbill", Confidence: 0.79},
			{Date: "2024-07-09", Time: "14:20:00", ScientificName: "Loxia leucoptera", CommonName: "White-winged Crossbill", Confidence: 0.85},

			// Hirundo rustica - first detected in July 2024
			{Date: "2024-07-06", Time: "15:12:31", ScientificName: "Hirundo rustica", CommonName: "Barn Swallow", Confidence: 0.93},
		}

		for _, note := range testNotes {
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}

		// Test 1: Get new species in July 2024
		result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-07-01", "2024-07-31", 10, 0)
		require.NoError(t, err)
		assert.Len(t, result, 2, "Expected 2 new species in July 2024")

		// Verify species details
		speciesMap := make(map[string]NewSpeciesData)
		for _, species := range result {
			speciesMap[species.ScientificName] = species
		}

		// Sylvia atricapilla
		blackcap, exists := speciesMap["Sylvia atricapilla"]
		assert.True(t, exists, "Expected Sylvia atricapilla in results")
		assert.Equal(t, "2024-07-28", blackcap.FirstSeenDate)
		assert.Equal(t, 3, blackcap.CountInPeriod)

		// Hirundo rustica
		swallow, exists := speciesMap["Hirundo rustica"]
		assert.True(t, exists, "Expected Hirundo rustica in results")
		assert.Equal(t, "2024-07-06", swallow.FirstSeenDate)
		assert.Equal(t, 1, swallow.CountInPeriod)
	})

	t.Run("date range validation", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Test invalid date range
		result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-07-31", "2024-07-01", 10, 0)
		require.Error(t, err, "Expected error for invalid date range")
		assert.Contains(t, err.Error(), "start date cannot be after end date")
		assert.Nil(t, result)
	})

	t.Run("pagination", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Create 5 species with different first detection dates
		for i := 1; i <= 5; i++ {
			note := Note{
				Date:           "2024-07-" + fmt.Sprintf("%02d", i),
				Time:           "10:00:00",
				ScientificName: fmt.Sprintf("Species %d", i),
				CommonName:     fmt.Sprintf("Common Species %d", i),
				Confidence:     0.85,
			}
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}

		// Test with limit
		result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-07-01", "2024-07-31", 2, 0)
		require.NoError(t, err)
		assert.Len(t, result, 2, "Expected 2 results with limit=2")

		// Test with offset
		result2, err := ds.GetNewSpeciesDetections(t.Context(), "2024-07-01", "2024-07-31", 2, 2)
		require.NoError(t, err)
		assert.Len(t, result2, 2, "Expected 2 results with limit=2, offset=2")

		// Ensure different results
		assert.NotEqual(t, result[0].ScientificName, result2[0].ScientificName)
	})

	t.Run("null and empty dates", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Insert notes with various date issues
		// Note: AutoMigrate may enforce NOT NULL on date field, so we'll test empty strings
		err := ds.DB.Exec(`
			INSERT INTO notes (scientific_name, common_name, date, time, confidence)
			VALUES 
			('Species empty', 'Empty Date Species', '', '10:00:00', 0.9),
			('Species valid', 'Valid Date Species', '2024-07-01', '10:00:00', 0.9)
		`).Error
		require.NoError(t, err)

		// Should only return species with valid dates
		result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-07-01", "2024-07-31", 10, 0)
		require.NoError(t, err)
		assert.Len(t, result, 1, "Expected only species with valid dates")
		assert.Equal(t, "Species valid", result[0].ScientificName)
	})

	t.Run("species across year boundary", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Species first seen in 2023
		notes := []Note{
			{Date: "2023-12-31", Time: "23:59:00", ScientificName: "Motacilla alba", CommonName: "White Wagtail", Confidence: 0.85},
			{Date: "2024-01-01", Time: "00:01:00", ScientificName: "Motacilla alba", CommonName: "White Wagtail", Confidence: 0.90},
			{Date: "2024-07-15", Time: "10:00:00", ScientificName: "Motacilla alba", CommonName: "White Wagtail", Confidence: 0.92},
		}

		for _, note := range notes {
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}

		// Query for new species in 2024
		result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-01-01", "2024-12-31", 10, 0)
		require.NoError(t, err)

		// Should not find Motacilla alba as new in 2024
		assert.Empty(t, result, "Species first seen in 2023 should not be new in 2024")
	})

	t.Run("seasonal queries", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Spring species
		springSpecies := []Note{
			{Date: "2024-04-15", Time: "08:00:00", ScientificName: "Hirundo rustica", CommonName: "Barn Swallow", Confidence: 0.88},
			{Date: "2024-05-01", Time: "09:00:00", ScientificName: "Phylloscopus trochilus", CommonName: "Willow Warbler", Confidence: 0.85},
		}

		// Summer species
		summerSpecies := []Note{
			{Date: "2024-06-25", Time: "10:00:00", ScientificName: "Apus apus", CommonName: "Common Swift", Confidence: 0.90},
			{Date: "2024-07-10", Time: "11:00:00", ScientificName: "Delichon urbicum", CommonName: "Common House Martin", Confidence: 0.87},
		}

		for _, note := range append(springSpecies, summerSpecies...) {
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}

		// Test spring period
		springResult, err := ds.GetNewSpeciesDetections(t.Context(), "2024-03-20", "2024-06-20", 10, 0)
		require.NoError(t, err)
		assert.Len(t, springResult, 2, "Expected 2 new species in spring")

		// Test summer period
		summerResult, err := ds.GetNewSpeciesDetections(t.Context(), "2024-06-21", "2024-09-21", 10, 0)
		require.NoError(t, err)
		assert.Len(t, summerResult, 2, "Expected 2 new species in summer")

		// Add Barn Swallow detection in summer
		summerSwallow := Note{
			Date:           "2024-07-15",
			Time:           "12:00:00",
			ScientificName: "Hirundo rustica",
			CommonName:     "Barn Swallow",
			Confidence:     0.92,
		}
		err = ds.DB.Create(&summerSwallow).Error
		require.NoError(t, err)

		// Barn Swallow should not be "new" in summer
		summerResult2, err := ds.GetNewSpeciesDetections(t.Context(), "2024-06-21", "2024-09-21", 10, 0)
		require.NoError(t, err)

		for _, species := range summerResult2 {
			assert.NotEqual(t, "Hirundo rustica", species.ScientificName,
				"Hirundo rustica should not be new in summer")
		}
	})

	t.Run("count in period", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Multiple detections of same species
		for i := range 5 {
			note := Note{
				Date:           "2024-07-20",
				Time:           fmt.Sprintf("%02d:00:00", 10+i),
				ScientificName: "Erithacus rubecula",
				CommonName:     "European Robin",
				Confidence:     0.85 + float64(i)*0.01,
			}
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}

		result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-07-20", "2024-07-20", 10, 0)
		require.NoError(t, err)
		assert.Len(t, result, 1, "Expected 1 new species")
		assert.Equal(t, "2024-07-20", result[0].FirstSeenDate)
		assert.Equal(t, 5, result[0].CountInPeriod, "Expected 5 detections on first day")
	})
}

// TestGetHourlyDistribution tests the GetHourlyDistribution function
func TestGetHourlyDistribution(t *testing.T) {
	t.Parallel()

	t.Run("basic hourly distribution", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Insert detections at different hours
		hours := []struct {
			hour  int
			count int
		}{
			{6, 2},
			{7, 5},
			{8, 8},
			{9, 10},
			{10, 7},
			{18, 4},
			{19, 3},
			{20, 1},
		}

		for _, h := range hours {
			for i := range h.count {
				note := Note{
					Date:           "2024-07-15",
					Time:           fmt.Sprintf("%02d:%02d:00", h.hour, i),
					ScientificName: "Test species",
					CommonName:     "Test Common",
					Confidence:     0.85,
				}
				err := ds.DB.Create(&note).Error
				require.NoError(t, err)
			}
		}

		// Test hourly distribution
		distribution, err := ds.GetHourlyDistribution(t.Context(), "2024-07-15", "2024-07-15", "")
		require.NoError(t, err)

		// Create map for verification
		hourMap := make(map[int]int)
		for _, h := range distribution {
			hourMap[h.Hour] = h.Count
		}

		assert.Equal(t, 5, hourMap[7], "Expected 5 detections at hour 7")
		assert.Equal(t, 10, hourMap[9], "Expected 10 detections at hour 9")
		assert.Equal(t, 4, hourMap[18], "Expected 4 detections at hour 18")
	})

	t.Run("species filter", func(t *testing.T) {
		t.Parallel()
		ds := setupTestDB(t)

		// Insert detections for multiple species
		species := []string{"Species A", "Species B"}
		for _, sp := range species {
			for hour := 8; hour < 12; hour++ {
				note := Note{
					Date:           "2024-07-15",
					Time:           fmt.Sprintf("%02d:00:00", hour),
					ScientificName: sp,
					CommonName:     sp + " Common",
					Confidence:     0.85,
				}
				err := ds.DB.Create(&note).Error
				require.NoError(t, err)
			}
		}

		// Test with species filter
		distribution, err := ds.GetHourlyDistribution(t.Context(), "2024-07-15", "2024-07-15", "Species A")
		require.NoError(t, err)

		totalCount := 0
		for _, h := range distribution {
			totalCount += h.Count
		}
		assert.Equal(t, 4, totalCount, "Expected 4 detections for Species A")
	})
}

// TestDatabasePerformance tests performance with larger datasets
func TestDatabasePerformance(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping performance test in short mode")
	}

	// Performance thresholds - made configurable to reduce CI flakiness
	const (
		singleQueryThresholdMs = 500  // Increased from 100ms for CI stability
		paginationThresholdMs  = 1000 // Increased from 200ms for CI stability
	)

	t.Parallel()
	ds := setupTestDB(t)

	// Create composite index for performance (only if it doesn't already exist)
	err := ds.DB.Exec("CREATE INDEX IF NOT EXISTS idx_notes_date_scientific ON notes(date, scientific_name)").Error
	require.NoError(t, err)

	// Insert a larger dataset
	numSpecies := 100
	detectionsPerSpecies := 50
	startDate := time.Date(2024, 1, 1, 0, 0, 0, 0, time.UTC)

	for i := range numSpecies {
		scientificName := fmt.Sprintf("Species %03d", i)
		commonName := fmt.Sprintf("Common Species %03d", i)

		// First detection at different dates
		firstDetectionDate := startDate.AddDate(0, 0, i)

		for j := range detectionsPerSpecies {
			detectionDate := firstDetectionDate.AddDate(0, 0, j)
			note := Note{
				Date:           detectionDate.Format(time.DateOnly),
				Time:           "12:00:00",
				ScientificName: scientificName,
				CommonName:     commonName,
				Confidence:     0.85,
			}
			err := ds.DB.Create(&note).Error
			require.NoError(t, err)
		}
	}

	// Measure query performance
	start := time.Now()
	result, err := ds.GetNewSpeciesDetections(t.Context(), "2024-01-01", "2024-01-31", 50, 0)
	duration := time.Since(start)

	require.NoError(t, err)
	assert.Len(t, result, 31, "Expected 31 new species in January")
	assert.Less(t, duration.Milliseconds(), int64(singleQueryThresholdMs), "Query should complete within %dms", singleQueryThresholdMs)

	// Test pagination performance
	start = time.Now()
	for offset := 0; offset < 30; offset += 10 {
		_, err := ds.GetNewSpeciesDetections(t.Context(), "2024-01-01", "2024-01-31", 10, offset)
		require.NoError(t, err)
	}
	duration = time.Since(start)
	assert.Less(t, duration.Milliseconds(), int64(paginationThresholdMs), "Paginated queries should complete within %dms", paginationThresholdMs)
}
