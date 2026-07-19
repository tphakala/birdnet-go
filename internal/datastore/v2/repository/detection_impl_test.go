package repository

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

func setupDetectionTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared"), &gorm.Config{
		Logger:                                   gorm_logger.Default.LogMode(gorm_logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err, "failed to open in-memory database")

	sqlDB, err := db.DB()
	require.NoError(t, err, "failed to get sql.DB")
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close(), "failed to close test database") })

	err = db.AutoMigrate(
		&entities.Detection{},
		&entities.DetectionLock{},
		&entities.DetectionReview{},
	)
	require.NoError(t, err, "failed to migrate detection tables")

	return db
}

func createTestDetection(t *testing.T, db *gorm.DB, detectedAt int64) *entities.Detection {
	t.Helper()
	det := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: detectedAt}
	require.NoError(t, db.Table(tableDetections).Create(det).Error)
	return det
}

func createBulkDetections(t *testing.T, db *gorm.DB, count int) (dets []*entities.Detection, ids []uint) {
	t.Helper()
	dets = make([]*entities.Detection, count)
	for i := range dets {
		dets[i] = &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: int64(1000 + i)}
	}
	require.NoError(t, db.Table(tableDetections).CreateInBatches(dets, 100).Error)
	ids = make([]uint, count)
	for i, d := range dets {
		ids[i] = d.ID
	}
	return dets, ids
}

// setupDetectionTestDBWithLabels migrates the labels table alongside the detection tables so
// text-search joins can be exercised.
func setupDetectionTestDBWithLabels(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupDetectionTestDB(t)
	require.NoError(t, db.AutoMigrate(&entities.Label{}), "failed to migrate labels table")
	return db
}

func createTestLabel(t *testing.T, db *gorm.DB, scientificName string, modelID uint) *entities.Label {
	t.Helper()
	label := &entities.Label{ScientificName: scientificName, ModelID: modelID, LabelTypeID: 1}
	require.NoError(t, db.Table(tableLabels).Create(label).Error)
	return label
}

func createDetectionForLabel(t *testing.T, db *gorm.DB, labelID uint, detectedAt int64) *entities.Detection {
	t.Helper()
	det := &entities.Detection{LabelID: labelID, ModelID: 1, Confidence: 0.9, DetectedAt: detectedAt}
	require.NoError(t, db.Table(tableDetections).Create(det).Error)
	return det
}

// TestSearch_QueryAndCommonLabelIDs verifies buildSearchJoins ORs the unbounded scientific_name
// LIKE (Query) with common-name-resolved label IDs (CommonLabelIDs). See issue #3378.
func TestSearch_QueryAndCommonLabelIDs(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	corone := createTestLabel(t, db, "Corvus corone", 1)
	cornix := createTestLabel(t, db, "Corvus cornix", 1)
	robin := createTestLabel(t, db, "Erithacus rubecula", 1)

	dCorone := createDetectionForLabel(t, db, corone.ID, 1000)
	_ = createDetectionForLabel(t, db, cornix.ID, 1001)
	dRobin := createDetectionForLabel(t, db, robin.ID, 1002)

	t.Run("scientific substring matches via Query LIKE", func(t *testing.T) {
		_, total, err := repo.Search(ctx, &SearchFilters{Query: "Corvus", Limit: 100})
		require.NoError(t, err)
		assert.Equal(t, int64(2), total)
	})

	t.Run("common-only label IDs match when Query has no scientific hit", func(t *testing.T) {
		results, total, err := repo.Search(ctx, &SearchFilters{
			Query: "Corneille", CommonLabelIDs: []uint{corone.ID}, Limit: 100,
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		require.Len(t, results, 1)
		assert.Equal(t, dCorone.ID, results[0].ID)
	})

	t.Run("scientific LIKE OR common label IDs returns the union", func(t *testing.T) {
		_, total, err := repo.Search(ctx, &SearchFilters{
			Query: "Corvus", CommonLabelIDs: []uint{robin.ID}, Limit: 100,
		})
		require.NoError(t, err)
		assert.Equal(t, int64(3), total)
	})

	t.Run("common label IDs without Query need no join", func(t *testing.T) {
		results, total, err := repo.Search(ctx, &SearchFilters{
			CommonLabelIDs: []uint{robin.ID}, Limit: 100,
		})
		require.NoError(t, err)
		require.Equal(t, int64(1), total)
		require.Len(t, results, 1)
		assert.Equal(t, dRobin.ID, results[0].ID)
	})
}

// TestGetSpeciesFirstDetectionInPeriod characterizes the per-species first-detection
// query: period filtering, aggregation across multiple labels of the same scientific
// name, MIN(detected_at) selection, and ascending order. It must hold for any
// implementation (window function or GROUP BY+MIN).
func TestGetSpeciesFirstDetectionInPeriod(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	// Two species with a single label, plus one species with two labels (two models)
	// to verify aggregation across labels by scientific_name.
	corone := createTestLabel(t, db, "Corvus corone", 1)
	robin := createTestLabel(t, db, "Erithacus rubecula", 1)
	merulaM1 := createTestLabel(t, db, "Turdus merula", 1)
	merulaM2 := createTestLabel(t, db, "Turdus merula", 2)

	const start, end int64 = 2000, 4000

	// Corvus corone: two detections in period -> first is 2000.
	createDetectionForLabel(t, db, corone.ID, 2000)
	createDetectionForLabel(t, db, corone.ID, 2500)
	// Erithacus rubecula: one BEFORE the period (excluded), one inside -> first is 3000.
	createDetectionForLabel(t, db, robin.ID, 1000)
	createDetectionForLabel(t, db, robin.ID, 3000)
	// Turdus merula: detections under two different labels; first across both is 2100.
	createDetectionForLabel(t, db, merulaM1.ID, 2200)
	createDetectionForLabel(t, db, merulaM2.ID, 2100)
	// A detection AFTER the period end is excluded (end is exclusive).
	createDetectionForLabel(t, db, corone.ID, 5000)

	results, err := repo.GetSpeciesFirstDetectionInPeriod(ctx, start, end, 100, 0)
	require.NoError(t, err)

	// Build a name->firstDetected map for order-independent value checks.
	got := make(map[string]int64, len(results))
	for _, r := range results {
		got[r.ScientificName] = r.FirstDetected
	}
	assert.Equal(t, int64(2000), got["Corvus corone"], "corone first detection in period")
	assert.Equal(t, int64(3000), got["Erithacus rubecula"], "robin first in-period detection (pre-period excluded)")
	assert.Equal(t, int64(2100), got["Turdus merula"], "merula first across both labels")
	assert.Len(t, results, 3, "exactly the three species detected within the period")

	// Results are ordered by first_detected ascending.
	firsts := make([]int64, len(results))
	for i, r := range results {
		firsts[i] = r.FirstDetected
	}
	assert.Equal(t, []int64{2000, 2100, 3000}, firsts, "ascending by first_detected")
}

// TestSearch_ScientificLikeNotTruncated guards against the prior 100-row cap: the scientific
// LIKE runs in SQL and must return all matches, not a capped subset. See issue #3378.
func TestSearch_ScientificLikeNotTruncated(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	const n = 150
	for i := range n {
		l := createTestLabel(t, db, fmt.Sprintf("Owlspecies%03d aves", i), 1)
		createDetectionForLabel(t, db, l.ID, int64(1000+i))
	}

	_, total, err := repo.Search(ctx, &SearchFilters{Query: "Owlspecies", Limit: 1000})
	require.NoError(t, err)
	assert.Equal(t, int64(n), total, "scientific LIKE must not be capped at 100 labels")
}

func TestDeleteBatch_SkipsLockedDetections(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	det1 := createTestDetection(t, db, 1000)
	det2 := createTestDetection(t, db, 2000)
	det3 := createTestDetection(t, db, 3000)

	lock := &entities.DetectionLock{DetectionID: det2.ID}
	require.NoError(t, db.Table(tableDetectionLocks).Create(lock).Error)

	err := repo.DeleteBatch(ctx, []uint{det1.ID, det2.ID, det3.ID})
	require.NoError(t, err)

	var remaining []entities.Detection
	require.NoError(t, db.Table(tableDetections).Find(&remaining).Error)

	assert.Len(t, remaining, 1)
	assert.Equal(t, det2.ID, remaining[0].ID)
}

func TestDeleteBatch_EmptyIDs(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	err := repo.DeleteBatch(ctx, []uint{})
	require.NoError(t, err)
}

func TestDeleteBatch_AllUnlocked(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	det1 := createTestDetection(t, db, 1000)
	det2 := createTestDetection(t, db, 2000)

	err := repo.DeleteBatch(ctx, []uint{det1.ID, det2.ID})
	require.NoError(t, err)

	var remaining []entities.Detection
	require.NoError(t, db.Table(tableDetections).Find(&remaining).Error)
	assert.Empty(t, remaining)
}

func TestDeleteBatch_ChunksLargeBatch(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	_, ids := createBulkDetections(t, db, batchQuerySize+1)

	err := repo.DeleteBatch(ctx, ids)
	require.NoError(t, err)

	var count int64
	require.NoError(t, db.Table(tableDetections).Count(&count).Error)
	assert.Equal(t, int64(0), count)
}

func TestDeleteBatch_ChunksRemainderCorrectly(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	// The remainder chunk (1 item) must still execute and respect the lock.
	dets, ids := createBulkDetections(t, db, batchQuerySize+1)

	lock := &entities.DetectionLock{DetectionID: dets[len(dets)-1].ID}
	require.NoError(t, db.Table(tableDetectionLocks).Create(lock).Error)

	err := repo.DeleteBatch(ctx, ids)
	require.NoError(t, err)

	var remaining []entities.Detection
	require.NoError(t, db.Table(tableDetections).Find(&remaining).Error)
	require.Len(t, remaining, 1)
	assert.Equal(t, dets[len(dets)-1].ID, remaining[0].ID)
}

// ============================================================================
// Batch Hourly Occurrence Tests
// ============================================================================

// TestGetBatchHourlyOccurrences verifies the batch hourly query returns per-label-ID
// counts, omits labels with no detections, honors the confidence filter, handles empty
// input, and propagates context cancellation (rather than silently returning zeros).
func TestGetBatchHourlyOccurrences(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := &detectionRepository{db: db}

	// Fixed epoch so the test does not depend on the wall clock. Per-label daily totals
	// (sum across hours) are asserted to stay independent of the OS-local hour bucketing.
	const base = int64(1_700_000_000)
	createDetectionForLabel(t, db, 10, base)
	createDetectionForLabel(t, db, 10, base+3600)
	createDetectionForLabel(t, db, 20, base+7200)
	// Low-confidence detection on label 30 must be filtered out by minConfidence below.
	require.NoError(t, db.Table(tableDetections).Create(
		&entities.Detection{LabelID: 30, ModelID: 1, Confidence: 0.2, DetectedAt: base}).Error)

	t.Run("per-label counts with confidence filter", func(t *testing.T) {
		got, err := repo.GetBatchHourlyOccurrences(t.Context(), []uint{10, 20, 30, 99}, base-1, base+10_000, 0, 0.5)
		require.NoError(t, err)

		require.Contains(t, got, uint(10))
		require.Contains(t, got, uint(20))
		assert.NotContains(t, got, uint(30), "below-confidence detection must be filtered out")
		assert.NotContains(t, got, uint(99), "label with no detections must be absent from the map")
		assert.Equal(t, 2, totalBatchHourly(got, 10), "label 10 has two qualifying detections")
		assert.Equal(t, 1, totalBatchHourly(got, 20), "label 20 has one qualifying detection")
	})

	t.Run("empty input returns empty map", func(t *testing.T) {
		got, err := repo.GetBatchHourlyOccurrences(t.Context(), nil, base-1, base+10_000, 0, 0.0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("cancelled context surfaces an error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := repo.GetBatchHourlyOccurrences(ctx, []uint{10}, base-1, base+10_000, 0, 0.0)
		require.ErrorIs(t, err, context.Canceled, "a cancelled context must abort the query with context.Canceled, not return zeros")
	})
}

// totalBatchHourly returns the total detections for a label across its 24-hour occurrence
// array. It takes the map and key (rather than the [24]int by value) to avoid copying the
// 192-byte array, and returns 0 for a label that is absent from the map.
func totalBatchHourly(byLabel map[uint][24]int, labelID uint) int {
	total := 0
	hours := byLabel[labelID]
	for _, c := range hours {
		total += c
	}
	return total
}

// TestGetBatchHourlyOccurrences_TimezoneOffset verifies that a detection buckets into the
// hour given by the supplied timezone offset, not the database/OS-local zone. A single
// detection at UTC midnight must land in hour 0 at UTC, hour 2 at UTC+2, and hour 23
// (previous day) at UTC-1. Guards against bucketing in the engine's local timezone.
func TestGetBatchHourlyOccurrences_TimezoneOffset(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := &detectionRepository{db: db}

	// 2024-06-15T00:00:00Z.
	const utcMidnight = int64(1718409600)
	createDetectionForLabel(t, db, 10, utcMidnight)

	cases := []struct {
		name       string
		offsetSecs int
		wantHour   int
	}{
		{"utc", 0, 0},
		{"plus two hours", 2 * 3600, 2},
		{"minus one hour wraps to previous day", -3600, 23},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.GetBatchHourlyOccurrences(
				t.Context(), []uint{10}, utcMidnight-86_400, utcMidnight+86_400, tc.offsetSecs, 0.0)
			require.NoError(t, err)
			require.Equal(t, 1, totalBatchHourly(got, 10), "exactly one detection regardless of offset")
			counts := got[10]
			assert.Equal(t, 1, counts[tc.wantHour],
				"detection must bucket into the offset-adjusted hour %d", tc.wantHour)
		})
	}
}

// TestGetDailyAnalytics_TimezoneOffset verifies that a near-midnight detection buckets into the
// calendar date given by the supplied timezone offset, not the database/OS-local zone. A detection
// at 2024-06-14T23:30:00Z falls on 2024-06-14 at UTC, crosses to 2024-06-15 at UTC+2, and stays on
// 2024-06-14 at UTC-1. Guards against date bucketing in the engine's local timezone (the deferred
// half of the hourly fix in TestGetBatchHourlyOccurrences_TimezoneOffset).
func TestGetDailyAnalytics_TimezoneOffset(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := &detectionRepository{db: db}

	// 2024-06-14T23:30:00Z, 30 minutes before UTC midnight.
	const nearMidnight = int64(1718407800)
	createDetectionForLabel(t, db, 10, nearMidnight)

	cases := []struct {
		name       string
		offsetSecs int
		wantDate   string
	}{
		{"utc", 0, "2024-06-14"},
		{"plus two hours crosses midnight", 2 * 3600, "2024-06-15"},
		{"minus one hour stays previous day", -3600, "2024-06-14"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := repo.GetDailyAnalytics(
				t.Context(), nearMidnight-86_400, nearMidnight+86_400, tc.offsetSecs, nil, nil)
			require.NoError(t, err)
			require.Len(t, got, 1, "exactly one date bucket regardless of offset")
			assert.Equal(t, tc.wantDate, got[0].Date,
				"detection must bucket into the offset-adjusted date %s", tc.wantDate)
			assert.Equal(t, int64(1), got[0].TotalDetections)
		})
	}
}

// ============================================================================
// Lock Tests
// ============================================================================

func TestLock_ExistingUnlocked(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	err := repo.Lock(ctx, det.ID)
	require.NoError(t, err)

	locked, err := repo.IsLocked(ctx, det.ID)
	require.NoError(t, err)
	assert.True(t, locked)
}

func TestLock_AlreadyLockedIsIdempotent(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	require.NoError(t, repo.Lock(ctx, det.ID))

	// Second lock should succeed silently
	err := repo.Lock(ctx, det.ID)
	require.NoError(t, err)

	// Should still be exactly one lock row
	var count int64
	require.NoError(t, db.Table(tableDetectionLocks).Where("detection_id = ?", det.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestLock_NonexistentDetection(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	err := repo.Lock(ctx, 99999)
	require.ErrorIs(t, err, ErrDetectionNotFound)
}

func TestLock_ConcurrentCallsNoDuplicates(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	const goroutines = 10
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			errs <- repo.Lock(ctx, det.ID)
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	// Exactly one lock row regardless of concurrency
	var count int64
	require.NoError(t, db.Table(tableDetectionLocks).Where("detection_id = ?", det.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

// ============================================================================
// SaveReview Tests
// ============================================================================

func TestSaveReview_CreateNew(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	review := &entities.DetectionReview{
		DetectionID: det.ID,
		Verified:    entities.VerificationCorrect,
	}

	err := repo.SaveReview(ctx, review)
	require.NoError(t, err)

	got, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.VerificationCorrect, got.Verified)
	assert.Equal(t, det.ID, got.DetectionID)
}

func TestSaveReview_UpsertExisting(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	// Create initial review
	review := &entities.DetectionReview{
		DetectionID: det.ID,
		Verified:    entities.VerificationCorrect,
	}
	require.NoError(t, repo.SaveReview(ctx, review))

	// Upsert with different status
	review2 := &entities.DetectionReview{
		DetectionID: det.ID,
		Verified:    entities.VerificationFalsePositive,
	}
	err := repo.SaveReview(ctx, review2)
	require.NoError(t, err)

	got, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.VerificationFalsePositive, got.Verified)

	// Should still be exactly one review row
	var count int64
	require.NoError(t, db.Table(tableDetectionReviews).Where("detection_id = ?", det.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

func TestSaveReview_UpdatedAtChangesOnUpsert(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	review := &entities.DetectionReview{
		DetectionID: det.ID,
		Verified:    entities.VerificationCorrect,
	}
	require.NoError(t, repo.SaveReview(ctx, review))

	first, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	firstUpdated := first.UpdatedAt

	// Small delay to ensure timestamp differs
	time.Sleep(10 * time.Millisecond)

	review2 := &entities.DetectionReview{
		DetectionID: det.ID,
		Verified:    entities.VerificationFalsePositive,
	}
	require.NoError(t, repo.SaveReview(ctx, review2))

	second, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	assert.True(t, second.UpdatedAt.After(firstUpdated),
		"UpdatedAt should advance on upsert: first=%v, second=%v", firstUpdated, second.UpdatedAt)
}

func TestSearch_IncludedHoursOnSQLite(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	t08 := time.Date(2026, 5, 16, 8, 30, 0, 0, time.UTC).Unix()
	t14 := time.Date(2026, 5, 16, 14, 0, 0, 0, time.UTC).Unix()
	t22 := time.Date(2026, 5, 16, 22, 15, 0, 0, time.UTC).Unix()
	createTestDetection(t, db, t08)
	createTestDetection(t, db, t14)
	createTestDetection(t, db, t22)

	tests := []struct {
		name           string
		includedHours  []int
		timezoneOffset int
		wantTotal      int64
		wantDetectedAt []int64
	}{
		{
			name:           "single hour UTC",
			includedHours:  []int{14},
			timezoneOffset: 0,
			wantTotal:      1,
			wantDetectedAt: []int64{t14},
		},
		{
			name:           "multiple hours UTC",
			includedHours:  []int{8, 22},
			timezoneOffset: 0,
			wantTotal:      2,
			wantDetectedAt: []int64{t08, t22},
		},
		{
			name:           "negative timezone offset UTC-5",
			includedHours:  []int{9},
			timezoneOffset: -5 * 3600,
			wantTotal:      1,
			wantDetectedAt: []int64{t14},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			results, total, err := repo.Search(ctx, &SearchFilters{
				IncludedHours:  tc.includedHours,
				TimezoneOffset: tc.timezoneOffset,
			})
			require.NoError(t, err)
			assert.Equal(t, tc.wantTotal, total)
			require.Len(t, results, len(tc.wantDetectedAt))

			got := make([]int64, 0, len(results))
			for _, d := range results {
				got = append(got, d.DetectedAt)
			}
			assert.ElementsMatch(t, tc.wantDetectedAt, got)
		})
	}
}

func TestSaveReview_ConcurrentUpsertNoDuplicates(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}
	det := createTestDetection(t, db, 1000)

	const goroutines = 10
	errs := make(chan error, goroutines)
	var wg sync.WaitGroup

	for i := range goroutines {
		wg.Go(func() {
			status := entities.VerificationCorrect
			if i%2 == 0 {
				status = entities.VerificationFalsePositive
			}
			errs <- repo.SaveReview(ctx, &entities.DetectionReview{
				DetectionID: det.ID,
				Verified:    status,
			})
		})
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err)
	}

	// Exactly one review row regardless of concurrency
	var count int64
	require.NoError(t, db.Table(tableDetectionReviews).Where("detection_id = ?", det.ID).Count(&count).Error)
	assert.Equal(t, int64(1), count)
}

// ============================================================================
// GetByHour / CountByHour boundary tests
// ============================================================================

// TestGetByHour_HalfOpenBoundary verifies that GetByHour and CountByHour use
// a half-open interval [hourStart, hourStart+3600) so a detection whose
// detected_at equals the next hour's start is not double-counted.
//
// Three detections are seeded:
//   - t+10    (inside the hour, clearly included)
//   - t+3599  (last second of the hour, included)
//   - t+3600  (next hour's first second, must be EXCLUDED from the current hour)
//
// The current hour must return exactly 2. The next hour must return exactly 1.
func TestGetByHour_HalfOpenBoundary(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	const hourStart = int64(1_700_000_000)

	createTestDetection(t, db, hourStart+10)
	createTestDetection(t, db, hourStart+3599)
	createTestDetection(t, db, hourStart+3600) // boundary: next hour's first second

	t.Run("GetByHour current hour excludes boundary second", func(t *testing.T) {
		dets, total, err := repo.GetByHour(ctx, hourStart, 100, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(2), total,
			"GetByHour must use half-open [hourStart, hourStart+3600): expected 2, got %d", total)
		assert.Len(t, dets, 2)
	})

	t.Run("CountByHour current hour excludes boundary second", func(t *testing.T) {
		n, err := repo.CountByHour(ctx, hourStart)
		require.NoError(t, err)
		assert.Equal(t, int64(2), n,
			"CountByHour must use half-open [hourStart, hourStart+3600): expected 2, got %d", n)
	})

	t.Run("GetByHour next hour includes boundary second exactly once", func(t *testing.T) {
		dets, total, err := repo.GetByHour(ctx, hourStart+3600, 100, 0)
		require.NoError(t, err)
		assert.Equal(t, int64(1), total,
			"boundary second must belong to the next hour exactly once: expected 1, got %d", total)
		assert.Len(t, dets, 1)
	})

	t.Run("CountByHour next hour includes boundary second exactly once", func(t *testing.T) {
		n, err := repo.CountByHour(ctx, hourStart+3600)
		require.NoError(t, err)
		assert.Equal(t, int64(1), n,
			"boundary second must belong to the next hour exactly once: expected 1, got %d", n)
	})
}

// TestGetByHour_PaginationHonored verifies that LIMIT is applied by GetByHour even
// though the query is used for a Count first. If the GORM query object's state leaked
// across the Count/Find boundary, LIMIT would be ignored and all rows returned.
func TestGetByHour_PaginationHonored(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	const hourStart = int64(1_700_000_000)
	for i := int64(0); i < 5; i++ {
		createTestDetection(t, db, hourStart+i*60)
	}

	dets, total, err := repo.GetByHour(ctx, hourStart, 2, 0)
	require.NoError(t, err)
	assert.Equal(t, int64(5), total, "total must count all rows in the hour")
	assert.Len(t, dets, 2, "LIMIT must be honored; query reuse after Count would return all 5")
}

func TestGetTopSpecies_DeterministicOrder(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	labelA := createTestLabel(t, db, "Species A", 1)
	labelB := createTestLabel(t, db, "Species B", 1)
	labelC := createTestLabel(t, db, "Species C", 1)

	// Create equal detection count
	for i := range 5 {
		createDetectionForLabel(t, db, labelA.ID, int64(1000+i))
		createDetectionForLabel(t, db, labelB.ID, int64(1000+i))
		createDetectionForLabel(t, db, labelC.ID, int64(1000+i))
	}

	results, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, nil, 3)
	require.NoError(t, err)
	require.Len(t, results, 3)

	assert.Equal(t, labelA.ID, results[0].LabelID)
	assert.Equal(t, labelB.ID, results[1].LabelID)
	assert.Equal(t, labelC.ID, results[2].LabelID)
}

// TestGetTopSpecies_SpeciesFilter verifies the optional scientific-name filter narrows the ranking
// to the selected species (applied before ORDER BY count / LIMIT) while an empty filter ranks all.
func TestGetTopSpecies_SpeciesFilter(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	labelA := createTestLabel(t, db, "Species A", 1)
	labelB := createTestLabel(t, db, "Species B", 1)
	labelC := createTestLabel(t, db, "Species C", 1)

	for i := range 5 {
		createDetectionForLabel(t, db, labelA.ID, int64(1000+i))
		createDetectionForLabel(t, db, labelB.ID, int64(1000+i))
		createDetectionForLabel(t, db, labelC.ID, int64(1000+i))
	}

	t.Run("restricts to the selected species", func(t *testing.T) {
		results, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, []string{"Species B"}, 3)
		require.NoError(t, err)
		require.Len(t, results, 1)
		assert.Equal(t, labelB.ID, results[0].LabelID)
		assert.Equal(t, "Species B", results[0].ScientificName)
	})

	t.Run("empty filter ranks every species", func(t *testing.T) {
		results, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, []string{}, 3)
		require.NoError(t, err)
		require.Len(t, results, 3)
	})
}

// TestGetTopSpecies_ExcludesFalsePositives verifies the ranking counts only non-false-positive
// detections, matching GetSpeciesSummary (the species selector's ranking) and the hourly/confidence
// data these species are then charted from. A species whose detections are all false positives must
// drop out of the ranking entirely -- the mechanism behind the who-sings-when "N selected, fewer
// drawn" symptom when the selector (false-positive-excluded) and this ranking disagreed.
func TestGetTopSpecies_ExcludesFalsePositives(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	labelA := createTestLabel(t, db, "Species A", 1)
	labelB := createTestLabel(t, db, "Species B", 1)
	labelC := createTestLabel(t, db, "Species C", 1)

	markFalsePositive := func(detectionID uint) {
		t.Helper()
		require.NoError(t, repo.SaveReview(ctx, &entities.DetectionReview{
			DetectionID: detectionID,
			Verified:    entities.VerificationFalsePositive,
		}))
	}

	// A: 5 clean detections -> counts 5.
	// B: 5 detections, 3 flagged false positive -> counts 2.
	// C: 5 detections, all flagged false positive -> counts 0, so it must not appear at all.
	for i := range 5 {
		createDetectionForLabel(t, db, labelA.ID, int64(1000+i))

		detB := createDetectionForLabel(t, db, labelB.ID, int64(1000+i))
		if i < 3 {
			markFalsePositive(detB.ID)
		}

		detC := createDetectionForLabel(t, db, labelC.ID, int64(1000+i))
		markFalsePositive(detC.ID)
	}

	results, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, nil, 10)
	require.NoError(t, err)

	// C is gone (all false positives); A outranks B on non-false-positive volume.
	require.Len(t, results, 2)
	assert.Equal(t, labelA.ID, results[0].LabelID)
	assert.Equal(t, int64(5), results[0].Count)
	assert.Equal(t, labelB.ID, results[1].LabelID)
	assert.Equal(t, int64(2), results[1].Count)

	// Even named explicitly, an all-false-positive species yields no row -- so a species the selector
	// picked can be absent here, which is exactly what the client-side diagnostic surfaces.
	filtered, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, []string{"Species C"}, 10)
	require.NoError(t, err)
	assert.Empty(t, filtered)
}

// TestGetTopSpecies_NoLimitWhenNonPositive verifies limit <= 0 disables the LIMIT clause so every
// matching label row is returned. Callers with an explicit species selection pass 0 to avoid
// truncating a species that owns several model labels to fewer rows than selected species.
func TestGetTopSpecies_NoLimitWhenNonPositive(t *testing.T) {
	db := setupDetectionTestDBWithLabels(t)
	ctx := t.Context()
	repo := &detectionRepository{db: db}

	labelA := createTestLabel(t, db, "Species A", 1)
	labelB := createTestLabel(t, db, "Species B", 1)
	labelC := createTestLabel(t, db, "Species C", 1)

	// Distinct volumes so ordering is unambiguous: A(3) > B(2) > C(1).
	for i := range 3 {
		createDetectionForLabel(t, db, labelA.ID, int64(1000+i))
	}
	for i := range 2 {
		createDetectionForLabel(t, db, labelB.ID, int64(1000+i))
	}
	createDetectionForLabel(t, db, labelC.ID, 1000)

	// A positive limit still truncates (existing behavior).
	limited, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, nil, 2)
	require.NoError(t, err)
	require.Len(t, limited, 2)

	// limit == 0 returns every row, volume-ordered.
	all, err := repo.GetTopSpecies(ctx, 900, 1100, 0.0, nil, nil, 0)
	require.NoError(t, err)
	require.Len(t, all, 3)
	assert.Equal(t, labelA.ID, all[0].LabelID)
	assert.Equal(t, labelC.ID, all[2].LabelID)
}
