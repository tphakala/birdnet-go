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
		got, err := repo.GetBatchHourlyOccurrences(t.Context(), []uint{10, 20, 30, 99}, base-1, base+10_000, 0.5)
		require.NoError(t, err)

		require.Contains(t, got, uint(10))
		require.Contains(t, got, uint(20))
		assert.NotContains(t, got, uint(30), "below-confidence detection must be filtered out")
		assert.NotContains(t, got, uint(99), "label with no detections must be absent from the map")
		assert.Equal(t, 2, totalBatchHourly(got, 10), "label 10 has two qualifying detections")
		assert.Equal(t, 1, totalBatchHourly(got, 20), "label 20 has one qualifying detection")
	})

	t.Run("empty input returns empty map", func(t *testing.T) {
		got, err := repo.GetBatchHourlyOccurrences(t.Context(), nil, base-1, base+10_000, 0.0)
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("cancelled context surfaces an error", func(t *testing.T) {
		ctx, cancel := context.WithCancel(t.Context())
		cancel()
		_, err := repo.GetBatchHourlyOccurrences(ctx, []uint{10}, base-1, base+10_000, 0.0)
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
