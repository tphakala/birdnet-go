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
	t.Cleanup(func() { require.NoError(t, sqlDB.Close(), "failed to close test database") })

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

// localOffsetAt returns time.Local's UTC offset (seconds) at ref. The tests below seed timestamps
// in time.Local and assert local-zone bucketing, so they pass the matching offset (the same value
// production derives via GetTimezoneOffsetAt) to the offset-aware insights queries. Dedicated
// bucketing tests pass explicit offsets and fixed UTC instants instead, so they are independent of
// the host's zone.
func localOffsetAt(ref time.Time) int {
	return GetTimezoneOffsetAt(time.Local, ref)
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

	results, err := repo.GetExpectedSpeciesToday(ctx, yearRanges, localOffsetAt(y2Start), nil)
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

	results, err := repo.GetDawnChorusRaw(ctx, since, 4, 10, localOffsetAt(now), nil)
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

	// Species C: 5 detections on a single day (should be best day candidate)
	labelC := seedLabel(t, db, "Corvus corax")
	bestDay := today.AddDate(0, 0, -5)
	for i := range 5 {
		seedDetection(t, db, labelC, bestDay.Add(time.Duration(i)*time.Hour).Unix(), 0.9)
	}

	kpis, err := repo.GetDashboardKPIs(ctx, today.Unix(), localOffsetAt(today), nil)
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

	kpis, err := repo.GetDashboardKPIs(ctx, time.Now().Unix(), localOffsetAt(time.Now()), nil)
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

	results, err := repo.GetDawnChorusRaw(ctx, since, 4, 10, localOffsetAt(now), nil)
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

	y1Start := time.Date(2024, 3, 8, 0, 0, 0, 0, time.Local)
	seedDetectionForModel(t, db, labelA1, 1, y1Start.Unix(), 0.9)

	y2Start := time.Date(2025, 3, 7, 0, 0, 0, 0, time.Local)
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

	results, err := repo.GetExpectedSpeciesToday(ctx, yearRanges, localOffsetAt(y2Start), nil)
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

	kpis, err := repo.GetDashboardKPIs(ctx, today.Unix(), localOffsetAt(today), nil)
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

	results, err := repo.GetExpectedSpeciesToday(ctx, nil, 0, nil)
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

	const offsetEast = 5 * 3600 // UTC+5

	t.Run("utc buckets the two detections as distinct years", func(t *testing.T) {
		got, err := repo.GetExpectedSpeciesToday(ctx, ranges, 0, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 2, got[0].YearsSeen, "2023-12-31 and 2024-03-08 are distinct years at UTC")
		assert.Equal(t, "2024-03-08", got[0].LastSeenDate)
	})

	t.Run("east offset rolls both detections into 2024", func(t *testing.T) {
		got, err := repo.GetExpectedSpeciesToday(ctx, ranges, offsetEast, nil)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, 1, got[0].YearsSeen, "at UTC+5 the year-boundary detection rolls into 2024")
		assert.Equal(t, "2024-03-09", got[0].LastSeenDate, "23:30Z + 5h crosses to the next calendar day")
	})
}
