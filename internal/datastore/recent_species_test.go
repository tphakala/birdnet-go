package datastore

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetRecentSpeciesData(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}))
	ds := &DataStore{DB: db}

	notes := []Note{
		{Date: "2026-05-01", Time: "21:59:59", ScientificName: "Before window", Confidence: 0.99},
		{Date: "2026-05-01", Time: "22:00:00", ScientificName: "Turdus migratorius", CommonName: "American Robin", SpeciesCode: "amerob", Confidence: 0.8},
		{Date: "2026-05-01", Time: "23:00:00", ScientificName: "Turdus migratorius", CommonName: "American Robin", SpeciesCode: "amerob", Confidence: 0.95},
		{Date: "2026-05-02", Time: "00:30:00", ScientificName: "Setophaga petechia", CommonName: "Yellow Warbler", SpeciesCode: "yelwar", Confidence: 0.7},
		{Date: "2026-05-02", Time: "01:30:00", ScientificName: "Turdus migratorius", CommonName: "American Robin", SpeciesCode: "amerob", Confidence: 0.9},
		{Date: "2026-05-02", Time: "01:30:00", ScientificName: "Turdus migratorius", CommonName: "American Robin", SpeciesCode: "amerob", Confidence: 0.75},
		{Date: "2026-05-02", Time: "01:45:00", ScientificName: "Below confidence", Confidence: 0.69},
		{Date: "2026-05-02", Time: "02:00:00", ScientificName: "Cyanocitta cristata", CommonName: "Blue Jay", SpeciesCode: "blujay", Confidence: 0.85},
		{Date: "2026-05-02", Time: "02:00:01", ScientificName: "After window", Confidence: 0.99},
	}
	require.NoError(t, db.Create(&notes).Error)
	require.NoError(t, db.Create(&NoteReview{
		NoteID:   notes[2].ID,
		Verified: "false_positive",
	}).Error)

	start := time.Date(2026, 5, 1, 22, 0, 0, 0, time.Local)
	end := time.Date(2026, 5, 2, 2, 0, 0, 0, time.Local)
	rows, err := ds.GetRecentSpeciesData(t.Context(), start, end, 0.7, 4)
	require.NoError(t, err)
	require.Len(t, rows, 4)

	type speciesBucket struct {
		species string
		bucket  int
	}
	bySpeciesBucket := make(map[speciesBucket]RecentSpeciesData)
	for i := range rows {
		row := rows[i]
		bySpeciesBucket[speciesBucket{species: row.ScientificName, bucket: row.Bucket}] = row
	}

	robinStart := bySpeciesBucket[speciesBucket{species: "Turdus migratorius", bucket: 0}]
	assert.Equal(t, 1, robinStart.Count)
	assert.InDelta(t, 0.8, robinStart.BucketMaxConfidence, 0.001)
	assert.True(t, robinStart.LatestDetectedAt.IsZero())

	robinLatest := bySpeciesBucket[speciesBucket{species: "Turdus migratorius", bucket: 3}]
	assert.Equal(t, 2, robinLatest.Count)
	assert.InDelta(t, 1.65, robinLatest.ConfidenceTotal, 0.001)
	assert.InDelta(t, 0.9, robinLatest.BucketMaxConfidence, 0.001)
	assert.Equal(t, notes[5].ID, robinLatest.LatestDetectionID)
	assert.InDelta(t, 0.75, robinLatest.LatestConfidence, 0.001)
	assert.Equal(t, "2026-05-02 01:30:00", robinLatest.LatestDetectedAt.Format(time.DateTime))

	assert.Equal(t, 2, bySpeciesBucket[speciesBucket{species: "Setophaga petechia", bucket: 2}].Bucket)
	assert.Equal(t, 3, bySpeciesBucket[speciesBucket{species: "Cyanocitta cristata", bucket: 3}].Bucket)
}

func TestGetRecentSpeciesDataHonorsContextCancellation(t *testing.T) {
	t.Parallel()

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}))
	ds := &DataStore{DB: db}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	start := time.Date(2026, 5, 1, 22, 0, 0, 0, time.Local)
	_, err := ds.GetRecentSpeciesData(ctx, start, start.Add(4*time.Hour), 0, 16)
	require.Error(t, err)
}

func TestGetRecentSpeciesDataBucketsAcrossDST(t *testing.T) {
	t.Parallel()

	location, err := time.LoadLocation("America/New_York")
	require.NoError(t, err)
	start := time.Date(2026, time.March, 8, 0, 30, 0, 0, location)
	end := start.Add(4 * time.Hour)

	db := openSQLiteTestDB(t)
	require.NoError(t, db.AutoMigrate(&Note{}, &NoteReview{}))
	ds := &DataStore{DB: db}
	notes := []Note{
		{Date: "2026-03-08", Time: "01:44:59", ScientificName: "Before DST boundary", Confidence: 0.8},
		{Date: "2026-03-08", Time: "01:45:00", ScientificName: "At DST boundary", Confidence: 0.9},
		{Date: "2026-03-08", Time: "03:00:00", ScientificName: "After DST jump", Confidence: 0.95},
	}
	require.NoError(t, db.Create(&notes).Error)

	rows, err := ds.GetRecentSpeciesData(t.Context(), start, end, 0, 16)
	require.NoError(t, err)
	require.Len(t, rows, 3)
	bucketsBySpecies := make(map[string]int, len(rows))
	for i := range rows {
		bucketsBySpecies[rows[i].ScientificName] = rows[i].Bucket
	}
	assert.Equal(t, 4, bucketsBySpecies["Before DST boundary"])
	assert.Equal(t, 5, bucketsBySpecies["At DST boundary"])
	assert.Equal(t, 6, bucketsBySpecies["After DST jump"])
}
