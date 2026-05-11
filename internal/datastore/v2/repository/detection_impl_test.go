package repository

import (
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
