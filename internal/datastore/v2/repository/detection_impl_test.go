package repository

import (
	"testing"

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
	)
	require.NoError(t, err, "failed to migrate detection tables")

	return db
}

func TestDeleteBatch_SkipsLockedDetections(t *testing.T) {
	db := setupDetectionTestDB(t)
	ctx := t.Context()

	repo := &detectionRepository{db: db}

	det1 := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: 1000}
	det2 := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.8, DetectedAt: 2000}
	det3 := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.7, DetectedAt: 3000}

	require.NoError(t, db.Table(tableDetections).Create(det1).Error)
	require.NoError(t, db.Table(tableDetections).Create(det2).Error)
	require.NoError(t, db.Table(tableDetections).Create(det3).Error)

	// Lock det2
	lock := &entities.DetectionLock{DetectionID: det2.ID}
	require.NoError(t, db.Table(tableDetectionLocks).Create(lock).Error)

	// DeleteBatch all three - should skip det2
	err := repo.DeleteBatch(ctx, []uint{det1.ID, det2.ID, det3.ID})
	require.NoError(t, err)

	var remaining []entities.Detection
	require.NoError(t, db.Table(tableDetections).Find(&remaining).Error)

	assert.Len(t, remaining, 1)
	assert.Equal(t, det2.ID, remaining[0].ID)
}

func TestDeleteBatch_EmptyIDs(t *testing.T) {
	t.Parallel()
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

	det1 := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: 1000}
	det2 := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.8, DetectedAt: 2000}

	require.NoError(t, db.Table(tableDetections).Create(det1).Error)
	require.NoError(t, db.Table(tableDetections).Create(det2).Error)

	err := repo.DeleteBatch(ctx, []uint{det1.ID, det2.ID})
	require.NoError(t, err)

	var remaining []entities.Detection
	require.NoError(t, db.Table(tableDetections).Find(&remaining).Error)
	assert.Empty(t, remaining)
}
