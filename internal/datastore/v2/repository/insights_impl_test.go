package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

// setupInsightsTestDB creates an in-memory SQLite database for insights tests.
// Migrates Detection, Label, DetectionReview, AIModel, and LabelType entities.
func setupInsightsTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_foreign_keys=ON"), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { _ = sqlDB.Close() })

	err = db.AutoMigrate(
		&entities.LabelType{},
		&entities.AIModel{},
		&entities.Label{},
		&entities.Detection{},
		&entities.DetectionReview{},
	)
	require.NoError(t, err)

	// Seed required reference data
	require.NoError(t, db.Create(&entities.LabelType{ID: 1, Name: "species"}).Error)
	require.NoError(t, db.Create(&entities.AIModel{ID: 1, Name: "BirdNET", Version: "2.4"}).Error)

	return db
}

// seedLabel creates a test label and returns its ID.
func seedLabel(t *testing.T, db *gorm.DB, scientificName string) uint {
	t.Helper()
	label := entities.Label{
		ScientificName: scientificName,
		ModelID:        1,
		LabelTypeID:    1,
	}
	require.NoError(t, db.Create(&label).Error)
	return label.ID
}

// seedDetection creates a test detection at the given Unix timestamp with given confidence.
func seedDetection(t *testing.T, db *gorm.DB, labelID uint, detectedAt int64, confidence float64) uint {
	t.Helper()
	det := entities.Detection{
		LabelID:    labelID,
		DetectedAt: detectedAt,
		Confidence: confidence,
		ModelID:    1,
	}
	require.NoError(t, db.Create(&det).Error)
	return det.ID
}

// seedFalsePositiveReview marks a detection as false positive.
func seedFalsePositiveReview(t *testing.T, db *gorm.DB, detectionID uint) {
	t.Helper()
	review := entities.DetectionReview{
		DetectionID: detectionID,
		Verified:    "false_positive",
	}
	require.NoError(t, db.Create(&review).Error)
}

func TestGetPhantomSpecies(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	since := now.AddDate(0, 0, -30).Unix()

	// Species A: phantom (low confidence, 3+ detections)
	labelA := seedLabel(t, db, "Parus major")
	for i := range 4 {
		seedDetection(t, db, labelA, now.AddDate(0, 0, -i).Unix(), 0.45)
	}

	// Species B: high confidence (not phantom)
	labelB := seedLabel(t, db, "Turdus merula")
	for i := range 5 {
		seedDetection(t, db, labelB, now.AddDate(0, 0, -i).Unix(), 0.85)
	}

	// Species C: low confidence but too few detections (only 2)
	labelC := seedLabel(t, db, "Corvus corax")
	for i := range 2 {
		seedDetection(t, db, labelC, now.AddDate(0, 0, -i).Unix(), 0.35)
	}

	// Species D: would be phantom but all detections are false positives
	labelD := seedLabel(t, db, "Erithacus rubecula")
	for i := range 3 {
		detID := seedDetection(t, db, labelD, now.AddDate(0, 0, -i).Unix(), 0.40)
		seedFalsePositiveReview(t, db, detID)
	}

	results, err := repo.GetPhantomSpecies(ctx, since, 3, 0.6, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Parus major", results[0].ScientificName)
	assert.Equal(t, int64(4), results[0].DetectionCount)
	assert.InDelta(t, 0.45, results[0].AvgConfidence, 0.01)
}

func TestGetExpectedSpeciesToday(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	// Species A: detected in two previous years around same DOY
	labelA := seedLabel(t, db, "Turdus merula")

	// Year 1 (2024): March 8-10 timestamps
	y1Start := time.Date(2024, 3, 8, 0, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, y1Start.Unix(), 0.9)
	seedDetection(t, db, labelA, y1Start.Add(24*time.Hour).Unix(), 0.85)

	// Year 2 (2025): March 7-9 timestamps
	y2Start := time.Date(2025, 3, 7, 0, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, y2Start.Unix(), 0.88)

	// Species B: detected in only one previous year
	labelB := seedLabel(t, db, "Parus major")
	seedDetection(t, db, labelB, y1Start.Unix(), 0.82)

	// Species C: detected but OUTSIDE the timestamp ranges (should not appear)
	labelC := seedLabel(t, db, "Corvus corax")
	seedDetection(t, db, labelC, time.Date(2024, 6, 15, 0, 0, 0, 0, time.Local).Unix(), 0.9)

	// Build year ranges for March 6-12 in 2024 and 2025
	yearRanges := []TimeRange{
		{
			Start: time.Date(2024, 3, 6, 0, 0, 0, 0, time.Local).Unix(),
			End:   time.Date(2024, 3, 12, 23, 59, 59, 0, time.Local).Unix(),
		},
		{
			Start: time.Date(2025, 3, 6, 0, 0, 0, 0, time.Local).Unix(),
			End:   time.Date(2025, 3, 12, 23, 59, 59, 0, time.Local).Unix(),
		},
	}

	results, err := repo.GetExpectedSpeciesToday(ctx, yearRanges, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Species A should be first (seen in 2 years)
	assert.Equal(t, "Turdus merula", results[0].ScientificName)
	assert.Equal(t, 2, results[0].YearsSeen)

	// Species B seen in 1 year
	assert.Equal(t, "Parus major", results[1].ScientificName)
	assert.Equal(t, 1, results[1].YearsSeen)
}

func TestGetDawnChorusRaw(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	since := now.AddDate(0, 0, -30).Unix()

	labelA := seedLabel(t, db, "Turdus merula")

	// Day 1: two detections at 5:00 AM and 6:30 AM — should return 5:00 AM
	day1 := time.Date(now.Year(), now.Month(), now.Day()-5, 0, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, day1.Add(5*time.Hour).Unix(), 0.9)
	seedDetection(t, db, labelA, day1.Add(6*time.Hour+30*time.Minute).Unix(), 0.85)

	// Day 2: detection at 4:15 AM
	day2 := time.Date(now.Year(), now.Month(), now.Day()-3, 0, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, day2.Add(4*time.Hour+15*time.Minute).Unix(), 0.88)

	// Detection outside hour range (11:00 AM — should be excluded)
	seedDetection(t, db, labelA, day1.Add(11*time.Hour).Unix(), 0.92)

	// Detection before since (should be excluded)
	seedDetection(t, db, labelA, now.AddDate(0, 0, -45).Add(5*time.Hour).Unix(), 0.8)

	results, err := repo.GetDawnChorusRaw(ctx, since, 4, 10, nil)
	require.NoError(t, err)
	require.Len(t, results, 2) // 2 days

	// Verify we got the earliest detection per day
	for _, entry := range results {
		assert.Equal(t, "Turdus merula", entry.ScientificName)
		// Verify the time is within dawn hours
		entryTime := time.Unix(entry.EarliestAt, 0).In(time.Local)
		assert.GreaterOrEqual(t, entryTime.Hour(), 4)
		assert.Less(t, entryTime.Hour(), 10)
	}
}

func TestGetExpectedSpeciesToday_EmptyRanges(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	results, err := repo.GetExpectedSpeciesToday(ctx, nil, nil)
	require.NoError(t, err)
	assert.Nil(t, results)
}
