package repository

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/testutil"
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
	t.Cleanup(func() { require.NoError(t, sqlDB.Close(), "failed to close test database") })

	err = db.AutoMigrate(
		&entities.LabelType{},
		&entities.AIModel{},
		&entities.Label{},
		&entities.Detection{},
		&entities.DetectionReview{},
		&entities.DetectionModelContribution{},
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

	// Species A: detected in two previous years around same DOY.
	// Seed at local noon (not midnight) so the single query-wide offset cannot shift a row onto
	// the adjacent calendar day on a non-UTC host near a DST boundary; the asserted dates stay
	// stable for any host offset.
	labelA := seedLabel(t, db, "Turdus merula")

	// Year 1 (2024): March 8-9 (noon)
	y1Start := time.Date(2024, 3, 8, 12, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, y1Start.Unix(), 0.9)
	seedDetection(t, db, labelA, y1Start.Add(24*time.Hour).Unix(), 0.85)

	// Year 2 (2025): March 7 (noon)
	y2Start := time.Date(2025, 3, 7, 12, 0, 0, 0, time.Local)
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

	results, err := repo.GetExpectedSpeciesToday(ctx, yearRanges, time.Local, nil)
	require.NoError(t, err)
	require.Len(t, results, 2)

	// Species A should be first (seen in 2 years)
	assert.Equal(t, "Turdus merula", results[0].ScientificName)
	assert.Equal(t, 2, results[0].YearsSeen)
	// MAX(date) asserts the date expression groups in the configured zone: the latest Turdus
	// merula detection is 2025-03-07 (00:00 local), so LastSeenDate must be that calendar day.
	assert.Equal(t, "2025-03-07", results[0].LastSeenDate)

	// Species B seen in 1 year
	assert.Equal(t, "Parus major", results[1].ScientificName)
	assert.Equal(t, 1, results[1].YearsSeen)
	assert.Equal(t, "2024-03-08", results[1].LastSeenDate)
}

func TestGetDawnChorusRaw(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	since := now.AddDate(0, 0, -30).Unix()

	labelA := seedLabel(t, db, "Turdus merula")

	// Day 1: two detections at 5:00 AM and 6:30 AM, should return 5:00 AM
	day1 := time.Date(now.Year(), now.Month(), now.Day()-5, 0, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, day1.Add(5*time.Hour).Unix(), 0.9)
	seedDetection(t, db, labelA, day1.Add(6*time.Hour+30*time.Minute).Unix(), 0.85)

	// Day 2: detection at 6:00 AM (kept clear of the 4 AM filter edge so a one-hour offset
	// difference cannot drop it out of the dawn window)
	day2 := time.Date(now.Year(), now.Month(), now.Day()-3, 0, 0, 0, 0, time.Local)
	seedDetection(t, db, labelA, day2.Add(6*time.Hour).Unix(), 0.88)

	// Detection outside hour range (11:00 AM, should be excluded)
	seedDetection(t, db, labelA, day1.Add(11*time.Hour).Unix(), 0.92)

	// Detection before since (should be excluded)
	seedDetection(t, db, labelA, now.AddDate(0, 0, -45).Add(5*time.Hour).Unix(), 0.8)

	results, err := repo.GetDawnChorusRaw(ctx, since, now.Unix(), 4, 10, time.Local, nil)
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

func TestGetNewArrivals(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	fourteenDaysAgo := now.AddDate(0, 0, -14).Unix()

	// Species A: first-ever detection 5 days ago (new arrival)
	labelA := seedLabel(t, db, "Phylloscopus collybita")
	seedDetection(t, db, labelA, now.AddDate(0, 0, -5).Unix(), 0.9)
	seedDetection(t, db, labelA, now.AddDate(0, 0, -3).Unix(), 0.85)

	// Species B: first detection 60 days ago (not new)
	labelB := seedLabel(t, db, "Turdus merula")
	seedDetection(t, db, labelB, now.AddDate(0, 0, -60).Unix(), 0.9)
	seedDetection(t, db, labelB, now.AddDate(0, 0, -2).Unix(), 0.88)

	// Species C: only false-positive detections recently (should be excluded)
	labelC := seedLabel(t, db, "Corvus corax")
	detID := seedDetection(t, db, labelC, now.AddDate(0, 0, -3).Unix(), 0.4)
	seedFalsePositiveReview(t, db, detID)

	results, err := repo.GetNewArrivals(ctx, fourteenDaysAgo, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Phylloscopus collybita", results[0].ScientificName)
	assert.Equal(t, int64(2), results[0].DetectionCount)
}

func TestGetGoneQuiet(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	fourteenDaysAgo := now.AddDate(0, 0, -14).Unix()

	// Species A: 10 detections, last one 20 days ago (gone quiet)
	labelA := seedLabel(t, db, "Turdus pilaris")
	for i := 20; i < 30; i++ {
		seedDetection(t, db, labelA, now.AddDate(0, 0, -i).Unix(), 0.85)
	}

	// Species B: 10 detections, latest is 2 days ago (still active, not gone quiet)
	labelB := seedLabel(t, db, "Turdus merula")
	for i := range 10 {
		seedDetection(t, db, labelB, now.AddDate(0, 0, -i).Unix(), 0.9)
	}

	// Species C: only 3 detections, all old (below minTotalDetections=5)
	labelC := seedLabel(t, db, "Corvus corax")
	for i := 20; i < 23; i++ {
		seedDetection(t, db, labelC, now.AddDate(0, 0, -i).Unix(), 0.8)
	}

	results, err := repo.GetGoneQuiet(ctx, fourteenDaysAgo, 5, nil)
	require.NoError(t, err)
	require.Len(t, results, 1)
	assert.Equal(t, "Turdus pilaris", results[0].ScientificName)
	assert.Equal(t, int64(10), results[0].TotalDetections)
}

func TestGetDashboardKPIs(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	// Species A: detections today and yesterday
	labelA := seedLabel(t, db, "Turdus merula")
	seedDetection(t, db, labelA, today.Add(2*time.Hour).Unix(), 0.9)
	seedDetection(t, db, labelA, today.Add(3*time.Hour).Unix(), 0.85)
	seedDetection(t, db, labelA, today.AddDate(0, 0, -1).Add(8*time.Hour).Unix(), 0.88)

	// Species B: detections yesterday and 2 days ago
	labelB := seedLabel(t, db, "Parus major")
	seedDetection(t, db, labelB, today.AddDate(0, 0, -1).Add(6*time.Hour).Unix(), 0.82)
	seedDetection(t, db, labelB, today.AddDate(0, 0, -2).Add(7*time.Hour).Unix(), 0.80)

	// Species C: 5 detections on a single day (should be best day candidate). Seed around midday
	// (hours 10-14) so the single query-wide offset cannot split the day's count across a calendar
	// boundary on a non-UTC host.
	labelC := seedLabel(t, db, "Corvus corax")
	bestDay := today.AddDate(0, 0, -5)
	for i := range 5 {
		seedDetection(t, db, labelC, bestDay.Add(time.Duration(i+10)*time.Hour).Unix(), 0.9)
	}

	kpis, err := repo.GetDashboardKPIs(ctx, today.Unix(), time.Local, nil)
	require.NoError(t, err)
	require.NotNil(t, kpis)

	assert.Equal(t, int64(3), kpis.LifetimeSpecies)
	assert.Equal(t, int64(2), kpis.TodayDetections)
	assert.Equal(t, int64(5), kpis.BestDayCount)
	assert.NotEmpty(t, kpis.RecentDates)
	assert.GreaterOrEqual(t, len(kpis.RecentDates), 4) // today, yesterday, 2d ago, 5d ago
}

func TestGetDashboardKPIs_Empty(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	kpis, err := repo.GetDashboardKPIs(ctx, time.Now().Unix(), nil, nil)
	require.NoError(t, err)
	require.NotNil(t, kpis)
	assert.Equal(t, int64(0), kpis.LifetimeSpecies)
	assert.Equal(t, int64(0), kpis.TodayDetections)
}

// seedLabelForModel creates a test label for a specific model and returns its ID.
func seedLabelForModel(t *testing.T, db *gorm.DB, scientificName string, modelID uint) uint {
	t.Helper()
	label := entities.Label{
		ScientificName: scientificName,
		ModelID:        modelID,
		LabelTypeID:    1,
	}
	require.NoError(t, db.Create(&label).Error)
	return label.ID
}

// seedDetectionForModel creates a test detection for a specific model.
func seedDetectionForModel(t *testing.T, db *gorm.DB, labelID, modelID uint, detectedAt int64, confidence float64) uint {
	t.Helper()
	det := entities.Detection{
		LabelID:    labelID,
		DetectedAt: detectedAt,
		Confidence: confidence,
		ModelID:    modelID,
	}
	require.NoError(t, db.Create(&det).Error)
	return det.ID
}

// setupInsightsTestDBMultiModel creates a test DB with two AI models seeded.
func setupInsightsTestDBMultiModel(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupInsightsTestDB(t)
	require.NoError(t, db.Create(&entities.AIModel{ID: 2, Name: "Perch", Version: "1.0"}).Error)
	return db
}

func TestGetNewArrivals_MultiModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	fourteenDaysAgo := now.AddDate(0, 0, -14).Unix()

	labelA1 := seedLabelForModel(t, db, "Chordeiles minor", 1)
	labelA2 := seedLabelForModel(t, db, "Chordeiles minor", 2)

	seedDetectionForModel(t, db, labelA1, 1, now.AddDate(0, 0, -5).Unix(), 0.9)
	seedDetectionForModel(t, db, labelA2, 2, now.AddDate(0, 0, -3).Unix(), 0.85)

	results, err := repo.GetNewArrivals(ctx, fourteenDaysAgo, nil)
	require.NoError(t, err)
	require.Len(t, results, 1, "same species from two models should produce one result")
	assert.Equal(t, "Chordeiles minor", results[0].ScientificName)
	assert.Equal(t, int64(2), results[0].DetectionCount)
}

func TestGetGoneQuiet_MultiModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	fourteenDaysAgo := now.AddDate(0, 0, -14).Unix()

	labelA1 := seedLabelForModel(t, db, "Setophaga americana", 1)
	labelA2 := seedLabelForModel(t, db, "Setophaga americana", 2)

	for i := 20; i < 25; i++ {
		seedDetectionForModel(t, db, labelA1, 1, now.AddDate(0, 0, -i).Unix(), 0.85)
	}
	for i := 22; i < 27; i++ {
		seedDetectionForModel(t, db, labelA2, 2, now.AddDate(0, 0, -i).Unix(), 0.80)
	}

	results, err := repo.GetGoneQuiet(ctx, fourteenDaysAgo, 5, nil)
	require.NoError(t, err)
	require.Len(t, results, 1, "same species from two models should produce one result")
	assert.Equal(t, "Setophaga americana", results[0].ScientificName)
	assert.Equal(t, int64(10), results[0].TotalDetections)
}

func TestGetDawnChorusRaw_MultiModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	since := now.AddDate(0, 0, -30).Unix()

	labelA1 := seedLabelForModel(t, db, "Turdus merula", 1)
	labelA2 := seedLabelForModel(t, db, "Turdus merula", 2)

	day1 := time.Date(now.Year(), now.Month(), now.Day()-5, 0, 0, 0, 0, time.Local)
	seedDetectionForModel(t, db, labelA1, 1, day1.Add(5*time.Hour).Unix(), 0.9)
	seedDetectionForModel(t, db, labelA2, 2, day1.Add(6*time.Hour).Unix(), 0.85)

	results, err := repo.GetDawnChorusRaw(ctx, since, now.Unix(), 4, 10, time.Local, nil)
	require.NoError(t, err)
	require.Len(t, results, 1, "same species on same day from two models should produce one result")
	assert.Equal(t, "Turdus merula", results[0].ScientificName)
	entryTime := time.Unix(results[0].EarliestAt, 0).In(time.Local)
	assert.Equal(t, 5, entryTime.Hour(), "should pick the earliest detection across models")
}

func TestGetPhantomSpecies_MultiModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	since := now.AddDate(0, 0, -30).Unix()

	labelA1 := seedLabelForModel(t, db, "Parus major", 1)
	labelA2 := seedLabelForModel(t, db, "Parus major", 2)

	seedDetectionForModel(t, db, labelA1, 1, now.AddDate(0, 0, -1).Unix(), 0.45)
	seedDetectionForModel(t, db, labelA1, 1, now.AddDate(0, 0, -2).Unix(), 0.40)
	seedDetectionForModel(t, db, labelA2, 2, now.AddDate(0, 0, -3).Unix(), 0.50)

	results, err := repo.GetPhantomSpecies(ctx, since, 3, 0.6, nil)
	require.NoError(t, err)
	require.Len(t, results, 1, "same species from two models should produce one result")
	assert.Equal(t, "Parus major", results[0].ScientificName)
	assert.Equal(t, int64(3), results[0].DetectionCount)
}

func TestGetExpectedSpeciesToday_MultiModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	labelA1 := seedLabelForModel(t, db, "Turdus merula", 1)
	labelA2 := seedLabelForModel(t, db, "Turdus merula", 2)

	// Seed at local noon so the single query-wide offset cannot shift a row across a day boundary
	// on a non-UTC host (keeps the distinct-year count stable regardless of host offset).
	y1Start := time.Date(2024, 3, 8, 12, 0, 0, 0, time.Local)
	seedDetectionForModel(t, db, labelA1, 1, y1Start.Unix(), 0.9)

	y2Start := time.Date(2025, 3, 7, 12, 0, 0, 0, time.Local)
	seedDetectionForModel(t, db, labelA2, 2, y2Start.Unix(), 0.88)

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

	results, err := repo.GetExpectedSpeciesToday(ctx, yearRanges, time.Local, nil)
	require.NoError(t, err)
	require.Len(t, results, 1, "same species from two models should produce one result")
	assert.Equal(t, "Turdus merula", results[0].ScientificName)
	assert.Equal(t, 2, results[0].YearsSeen)
}

func TestGetDashboardKPIs_MultiModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local)

	labelA1 := seedLabelForModel(t, db, "Turdus merula", 1)
	labelA2 := seedLabelForModel(t, db, "Turdus merula", 2)

	seedDetectionForModel(t, db, labelA1, 1, today.Add(2*time.Hour).Unix(), 0.9)
	seedDetectionForModel(t, db, labelA2, 2, today.Add(3*time.Hour).Unix(), 0.85)

	labelB := seedLabelForModel(t, db, "Parus major", 1)
	seedDetectionForModel(t, db, labelB, 1, today.Add(4*time.Hour).Unix(), 0.8)

	kpis, err := repo.GetDashboardKPIs(ctx, today.Unix(), time.Local, nil)
	require.NoError(t, err)
	require.NotNil(t, kpis)
	assert.Equal(t, int64(2), kpis.LifetimeSpecies, "same species from two models should count as one")
	assert.Equal(t, int64(3), kpis.TodayDetections)
}

func TestGetNewArrivals_MultiModel_FilterByModel(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	now := time.Now()
	fourteenDaysAgo := now.AddDate(0, 0, -14).Unix()

	labelA1 := seedLabelForModel(t, db, "Chordeiles minor", 1)
	labelA2 := seedLabelForModel(t, db, "Chordeiles minor", 2)

	seedDetectionForModel(t, db, labelA1, 1, now.AddDate(0, 0, -5).Unix(), 0.9)
	seedDetectionForModel(t, db, labelA2, 2, now.AddDate(0, 0, -3).Unix(), 0.85)

	modelID := uint(1)
	results, err := repo.GetNewArrivals(ctx, fourteenDaysAgo, &modelID)
	require.NoError(t, err)
	require.Len(t, results, 1, "filtering by model should return only that model's detections")
	assert.Equal(t, "Chordeiles minor", results[0].ScientificName)
	assert.Equal(t, int64(1), results[0].DetectionCount, "should only count model 1 detections")
}

func TestGetExpectedSpeciesToday_EmptyRanges(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	results, err := repo.GetExpectedSpeciesToday(ctx, nil, time.UTC, nil)
	require.NoError(t, err)
	assert.Nil(t, results)
}

// TestGetExpectedSpeciesToday_TimezoneBucketing verifies the date (MAX) and year
// (COUNT DISTINCT) expressions bucket by the supplied timezone offset, not the database/OS-local
// zone. Two near-midnight detections at fixed UTC instants straddle a calendar-day and a
// calendar-year boundary, so the offset changes both the date and the distinct-year count. Using
// explicit UTC instants and explicit offsets keeps the test independent of the host's zone.
func TestGetExpectedSpeciesToday_TimezoneBucketing(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)
	ctx := t.Context()

	label := seedLabel(t, db, "Turdus merula")

	// 2023-12-31T23:30:00Z: year 2023 at UTC, but 2024-01-01 (year 2024) at UTC+5.
	yearBoundary := time.Date(2023, 12, 31, 23, 30, 0, 0, time.UTC).Unix()
	seedDetection(t, db, label, yearBoundary, 0.9)
	// 2024-03-08T23:30:00Z: date 2024-03-08 at UTC, but 2024-03-09 at UTC+5.
	nearMidnight := time.Date(2024, 3, 8, 23, 30, 0, 0, time.UTC).Unix()
	seedDetection(t, db, label, nearMidnight, 0.9)

	// A single wide range covering both detections; the range filter is on the raw detected_at
	// epoch, so it is unaffected by the bucketing offset.
	ranges := []TimeRange{{
		Start: time.Date(2023, 12, 1, 0, 0, 0, 0, time.UTC).Unix(),
		End:   time.Date(2024, 4, 1, 0, 0, 0, 0, time.UTC).Unix(),
	}}

	eastZone := time.FixedZone("UTC+5", 5*3600)

	t.Run("nil zone (UTC) buckets the two detections as distinct years", func(t *testing.T) {
		got, err := repo.GetExpectedSpeciesToday(ctx, ranges, nil, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 2, got[0].YearsSeen, "2023-12-31 and 2024-03-08 are distinct years at UTC")
		assert.Equal(t, "2024-03-08", got[0].LastSeenDate)
	})

	t.Run("east offset rolls both detections into 2024", func(t *testing.T) {
		got, err := repo.GetExpectedSpeciesToday(ctx, ranges, eastZone, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 1, got[0].YearsSeen, "at UTC+5 the year-boundary detection rolls into 2024")
		assert.Equal(t, "2024-03-09", got[0].LastSeenDate, "23:30Z + 5h crosses to the next calendar day")
	})
}

// TestGetDawnChorusRaw_WallClockAcrossDST pins that the hour window and the date grouping
// follow each detection's own wall clock when the period crosses a DST change, rather than
// the offset in effect at the start of the period.
func TestGetDawnChorusRaw_WallClockAcrossDST(t *testing.T) {
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	kolkata, err := time.LoadLocation("Asia/Kolkata")
	require.NoError(t, err)

	type seeded struct {
		species string
		at      time.Time
	}
	type want struct {
		species string
		date    string
		at      time.Time
	}

	tests := []struct {
		name               string
		loc                *time.Location
		since, until       time.Time
		startHour, endHour int
		detections         []seeded
		want               []want
	}{
		{
			name:      "Helsinki fall-back: window follows EET after the change",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 30, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				{"A", time.Date(2026, time.October, 22, 5, 0, 0, 0, helsinki)},
				{"A", time.Date(2026, time.October, 27, 3, 38, 0, 0, helsinki)},
				{"A", time.Date(2026, time.October, 27, 4, 10, 0, 0, helsinki)},
				{"B", time.Date(2026, time.October, 28, 9, 30, 0, 0, helsinki)},
			},
			want: []want{
				{"A", "2026-10-22", time.Date(2026, time.October, 22, 5, 0, 0, 0, helsinki)},
				{"A", "2026-10-27", time.Date(2026, time.October, 27, 4, 10, 0, 0, helsinki)},
				{"B", "2026-10-28", time.Date(2026, time.October, 28, 9, 30, 0, 0, helsinki)},
			},
		},
		{
			name:      "Helsinki spring-forward: window follows EEST after the change",
			loc:       helsinki,
			since:     time.Date(2026, time.March, 25, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.April, 2, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				{"C", time.Date(2026, time.March, 31, 4, 20, 0, 0, helsinki)},
				{"C", time.Date(2026, time.March, 31, 10, 15, 0, 0, helsinki)},
			},
			want: []want{
				{"C", "2026-03-31", time.Date(2026, time.March, 31, 4, 20, 0, 0, helsinki)},
			},
		},
		{
			name:      "Kolkata has no DST: single segment, half-hour offset at the window edge",
			loc:       kolkata,
			since:     time.Date(2026, time.March, 25, 0, 0, 0, 0, kolkata),
			until:     time.Date(2026, time.April, 2, 0, 0, 0, 0, kolkata),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				{"D", time.Date(2026, time.March, 28, 3, 50, 0, 0, kolkata)},
				{"D", time.Date(2026, time.March, 28, 4, 5, 0, 0, kolkata)},
				{"D", time.Date(2026, time.March, 28, 9, 55, 0, 0, kolkata)},
			},
			want: []want{
				{"D", "2026-03-28", time.Date(2026, time.March, 28, 4, 5, 0, 0, kolkata)},
			},
		},
		{
			name:      "local date straddling the fall-back merges to one row",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 24, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 26, 0, 0, 0, 0, helsinki),
			startHour: 0,
			endHour:   24,
			detections: []seeded{
				{"E", time.Date(2026, time.October, 25, 2, 30, 0, 0, helsinki)},
				{"E", time.Date(2026, time.October, 25, 5, 0, 0, 0, helsinki)},
			},
			want: []want{
				{"E", "2026-10-25", time.Date(2026, time.October, 25, 2, 30, 0, 0, helsinki)},
			},
		},
		{
			name:      "detections after until are still included",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 22, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				{"F", time.Date(2026, time.October, 27, 6, 0, 0, 0, helsinki)},
			},
			want: []want{
				{"F", "2026-10-27", time.Date(2026, time.October, 27, 6, 0, 0, 0, helsinki)},
			},
		},
		{
			name:      "detections after until use the offset in effect at the start of the last segment",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 22, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				// 03:30 EET reads as 04:30 under the EEST offset of the last segment, so it is kept.
				{"J", time.Date(2026, time.October, 27, 3, 30, 0, 0, helsinki)},
			},
			want: []want{
				{"J", "2026-10-27", time.Date(2026, time.October, 27, 3, 30, 0, 0, helsinki)},
			},
		},
		{
			name:      "pre-change detection outside the window is not re-read under the next offset",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 30, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   5,
			detections: []seeded{
				// 02:00Z on 24 Oct is 05:00 EEST (out) but would read as 04:00 under EET (in).
				{"G", time.Date(2026, time.October, 24, 2, 0, 0, 0, time.UTC)},
			},
			want: []want{},
		},
		{
			name:      "detection exactly at the change belongs to the later segment only",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 30, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   5,
			detections: []seeded{
				// 01:00Z on 25 Oct is 03:00 EET after the change; read under EEST it would be 04:00.
				{"H", time.Date(2026, time.October, 25, 1, 0, 0, 0, time.UTC)},
			},
			want: []want{},
		},
		{
			name:      "detection exactly at the change is kept by the later segment",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 30, 0, 0, 0, 0, helsinki),
			startHour: 3,
			endHour:   4,
			detections: []seeded{
				{"H", time.Date(2026, time.October, 25, 1, 0, 0, 0, time.UTC)},
			},
			want: []want{
				{"H", "2026-10-25", time.Date(2026, time.October, 25, 1, 0, 0, 0, time.UTC)},
			},
		},
		{
			name:      "until before since still returns detections from since on",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 10, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				{"I", time.Date(2026, time.October, 22, 5, 0, 0, 0, helsinki)},
			},
			want: []want{
				{"I", "2026-10-22", time.Date(2026, time.October, 22, 5, 0, 0, 0, helsinki)},
			},
		},
		{
			name:      "until equal to since still returns detections from since on",
			loc:       helsinki,
			since:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			until:     time.Date(2026, time.October, 20, 0, 0, 0, 0, helsinki),
			startHour: 4,
			endHour:   10,
			detections: []seeded{
				{"I", time.Date(2026, time.October, 22, 5, 0, 0, 0, helsinki)},
			},
			want: []want{
				{"I", "2026-10-22", time.Date(2026, time.October, 22, 5, 0, 0, 0, helsinki)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupInsightsTestDB(t)
			repo := NewInsightsRepository(db, false, false)

			labels := make(map[string]uint)
			for _, d := range tt.detections {
				if _, ok := labels[d.species]; !ok {
					labels[d.species] = seedLabel(t, db, d.species)
				}
				seedDetection(t, db, labels[d.species], d.at.Unix(), 0.9)
			}

			got, err := repo.GetDawnChorusRaw(t.Context(), tt.since.Unix(), tt.until.Unix(), tt.startHour, tt.endHour, tt.loc, nil)
			require.NoError(t, err)

			wantEntries := make([]DawnChorusRawEntry, 0, len(tt.want))
			for _, w := range tt.want {
				wantEntries = append(wantEntries, DawnChorusRawEntry{ScientificName: w.species, Date: w.date, EarliestAt: w.at.Unix()})
			}
			assert.ElementsMatch(t, wantEntries, got)
		})
	}
}

// loadInsightsZone returns an IANA zone from the toolchain zone database for the DST tests below.
func loadInsightsZone(t *testing.T, name string) *time.Location {
	t.Helper()
	loc := testutil.ToolchainZones(t)[name]
	require.NotNil(t, loc, "zone %s", name)
	return loc
}

// TestGetExpectedSpeciesToday_PerRowOffsets pins that the date and year buckets use the UTC
// offset in effect at each row, not one offset for the whole span: every case has rows on both
// sides of a DST change 30 minutes from local midnight, so any single offset misplaces one of
// them.
func TestGetExpectedSpeciesToday_PerRowOffsets(t *testing.T) {
	newYork := loadInsightsZone(t, "America/New_York")
	sydney := loadInsightsZone(t, "Australia/Sydney")

	type row struct {
		species string
		at      time.Time
	}
	tests := []struct {
		name      string
		loc       *time.Location
		spans     []TimeRange
		rows      []row
		wantLast  map[string]string
		wantYears map[string]int
	}{
		{
			name: "New York spring forward, rows either side of local midnight",
			loc:  newYork,
			spans: []TimeRange{{
				Start: time.Date(2025, 2, 1, 0, 0, 0, 0, newYork).Unix(),
				End:   time.Date(2025, 4, 30, 23, 59, 59, 0, newYork).Unix(),
			}},
			rows: []row{
				{"Turdus merula", time.Date(2025, 3, 5, 23, 30, 0, 0, newYork)},
				{"Parus major", time.Date(2025, 3, 10, 0, 30, 0, 0, newYork)},
			},
			wantLast:  map[string]string{"Turdus merula": "2025-03-05", "Parus major": "2025-03-10"},
			wantYears: map[string]int{"Turdus merula": 1, "Parus major": 1},
		},
		{
			name: "Sydney new year and autumn change, rows either side of local midnight",
			loc:  sydney,
			spans: []TimeRange{{
				Start: time.Date(2024, 10, 1, 0, 0, 0, 0, sydney).Unix(),
				End:   time.Date(2025, 5, 1, 0, 0, 0, 0, sydney).Unix(),
			}},
			rows: []row{
				{"Turdus merula", time.Date(2024, 12, 31, 23, 30, 0, 0, sydney)},
				{"Turdus merula", time.Date(2025, 1, 1, 0, 30, 0, 0, sydney)},
				{"Parus major", time.Date(2025, 4, 6, 23, 30, 0, 0, sydney)},
			},
			wantLast:  map[string]string{"Turdus merula": "2025-01-01", "Parus major": "2025-04-06"},
			wantYears: map[string]int{"Turdus merula": 2, "Parus major": 1},
		},
		{
			name: "New York two disjoint ranges, the change lies between them",
			loc:  newYork,
			spans: []TimeRange{
				{
					Start: time.Date(2025, 2, 1, 0, 0, 0, 0, newYork).Unix(),
					End:   time.Date(2025, 2, 28, 0, 0, 0, 0, newYork).Unix(),
				},
				{
					Start: time.Date(2025, 3, 8, 0, 0, 0, 0, newYork).Unix(),
					End:   time.Date(2025, 3, 15, 0, 0, 0, 0, newYork).Unix(),
				},
			},
			rows: []row{
				{"Turdus merula", time.Date(2025, 2, 10, 23, 30, 0, 0, newYork)},
				{"Parus major", time.Date(2025, 3, 10, 0, 30, 0, 0, newYork)},
			},
			wantLast:  map[string]string{"Turdus merula": "2025-02-10", "Parus major": "2025-03-10"},
			wantYears: map[string]int{"Turdus merula": 1, "Parus major": 1},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupInsightsTestDB(t)
			repo := NewInsightsRepository(db, false, false)
			labels := map[string]uint{}
			for _, r := range tt.rows {
				if _, ok := labels[r.species]; !ok {
					labels[r.species] = seedLabel(t, db, r.species)
				}
				seedDetection(t, db, labels[r.species], r.at.Unix(), 0.9)
			}

			got, err := repo.GetExpectedSpeciesToday(t.Context(), tt.spans, tt.loc, nil)
			require.NoError(t, err)
			require.Len(t, got, len(tt.wantLast))
			for _, sp := range got {
				assert.Equal(t, tt.wantLast[sp.ScientificName], sp.LastSeenDate, "last seen date of %s", sp.ScientificName)
				assert.Equal(t, tt.wantYears[sp.ScientificName], sp.YearsSeen, "years seen of %s", sp.ScientificName)
			}
		})
	}
}

// TestGetDashboardKPIs_PerRowOffsets pins that the best day and the recent dates use the offset
// in effect at each row. The two seeded days lie on either side of the New York spring-forward
// change, with rows 30 minutes before local midnight (EST) and after it (EDT).
func TestGetDashboardKPIs_PerRowOffsets(t *testing.T) {
	newYork := loadInsightsZone(t, "America/New_York")
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)

	label := seedLabel(t, db, "Turdus merula")
	for _, at := range []time.Time{
		time.Date(2025, 3, 5, 23, 30, 0, 0, newYork),
		time.Date(2025, 3, 5, 23, 45, 0, 0, newYork),
		time.Date(2025, 3, 10, 0, 30, 0, 0, newYork),
	} {
		seedDetection(t, db, label, at.Unix(), 0.9)
	}

	todaySince := time.Date(2025, 4, 1, 0, 0, 0, 0, newYork).Unix()
	kpis, err := repo.GetDashboardKPIs(t.Context(), todaySince, newYork, nil)
	require.NoError(t, err)

	assert.Equal(t, "2025-03-05", kpis.BestDayDate)
	assert.Equal(t, int64(2), kpis.BestDayCount)
	assert.Equal(t, []string{"2025-03-10", "2025-03-05"}, kpis.RecentDates)
}

// TestGetExpectedSpeciesToday_RangeIsHalfOpen pins that a range includes a detection at its
// Start and excludes one at its End, the first instant of the next local day.
func TestGetExpectedSpeciesToday_RangeIsHalfOpen(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewInsightsRepository(db, false, false)

	start := time.Date(2025, 3, 6, 0, 0, 0, 0, time.UTC).Unix()
	end := time.Date(2025, 3, 7, 0, 0, 0, 0, time.UTC).Unix()
	atStart := seedLabel(t, db, "Turdus merula")
	atEnd := seedLabel(t, db, "Parus major")
	seedDetection(t, db, atStart, start, 0.9)
	seedDetection(t, db, atEnd, end, 0.9)

	got, err := repo.GetExpectedSpeciesToday(t.Context(), []TimeRange{{Start: start, End: end}}, time.UTC, nil)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
}

// TestGetDashboardKPIs_LookbackAndZone pins the start of the best-day window (the local date one
// year before today, 29 February becoming 1 March) and that a nil zone buckets in UTC.
func TestGetDashboardKPIs_LookbackAndZone(t *testing.T) {
	newYork := loadInsightsZone(t, "America/New_York")
	tests := []struct {
		name      string
		today     time.Time
		inside    time.Time
		outside   time.Time
		wantDate  string
		queryZone *time.Location
	}{
		{
			name:      "window starts at local midnight one year back",
			today:     time.Date(2025, 3, 20, 0, 0, 0, 0, newYork),
			inside:    time.Date(2024, 3, 20, 0, 30, 0, 0, newYork),
			outside:   time.Date(2024, 3, 19, 23, 30, 0, 0, newYork),
			wantDate:  "2024-03-20",
			queryZone: newYork,
		},
		{
			name:      "29 February looks back to 1 March",
			today:     time.Date(2028, 2, 29, 0, 0, 0, 0, newYork),
			inside:    time.Date(2027, 3, 1, 0, 30, 0, 0, newYork),
			outside:   time.Date(2027, 2, 28, 23, 30, 0, 0, newYork),
			wantDate:  "2027-03-01",
			queryZone: newYork,
		},
		{
			name:      "nil zone is UTC",
			today:     time.Date(2025, 4, 1, 0, 0, 0, 0, time.UTC),
			inside:    time.Date(2025, 3, 5, 23, 30, 0, 0, time.UTC),
			outside:   time.Date(2024, 3, 31, 23, 30, 0, 0, time.UTC),
			wantDate:  "2025-03-05",
			queryZone: nil,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupInsightsTestDB(t)
			repo := NewInsightsRepository(db, false, false)
			label := seedLabel(t, db, "Turdus merula")
			// Two rows outside the window on one day outweigh the single row inside it, so the
			// best day is the inside one only when the window excludes them.
			seedDetection(t, db, label, tt.outside.Unix(), 0.9)
			seedDetection(t, db, label, tt.outside.Add(time.Minute).Unix(), 0.9)
			seedDetection(t, db, label, tt.inside.Unix(), 0.9)

			kpis, err := repo.GetDashboardKPIs(t.Context(), tt.today.Unix(), tt.queryZone, nil)
			require.NoError(t, err)
			assert.Equal(t, tt.wantDate, kpis.BestDayDate)
			assert.Equal(t, int64(1), kpis.BestDayCount)
		})
	}
}
