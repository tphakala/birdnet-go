package repository

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
	gorm_logger "gorm.io/gorm/logger"
)

func createTestModel(t *testing.T, db *gorm.DB, table, name, version string, modelType entities.ModelType) *entities.AIModel {
	t.Helper()
	m := &entities.AIModel{Name: name, Version: version, Variant: "default", ModelType: modelType}
	require.NoError(t, db.Table(table).Create(m).Error)
	return m
}

func TestDetectionRepository_GetModelContributionsByDetectionIDs(t *testing.T) {
	db := setupDetectionTestDB(t)
	require.NoError(t, db.AutoMigrate(&entities.AIModel{}, &entities.DetectionModelContribution{}))
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	m1 := createTestModel(t, db, tableAIModels, "BirdNET", "2.4", entities.ModelTypeBird)
	m2 := createTestModel(t, db, tableAIModels, "Perch", "2", entities.ModelTypeMulti)
	m3 := createTestModel(t, db, tableAIModels, "Other", "1", entities.ModelTypeBird)

	detA := createTestDetection(t, db, 1000)
	detB := createTestDetection(t, db, 2000)
	detC := createTestDetection(t, db, 3000)

	first, last := int64(1777777777123), int64(1777777788456)
	minConf, sum := 0.6, 1.7
	rows := []*entities.DetectionModelContribution{
		{DetectionID: detA.ID, ModelID: m1.ID, HitCount: 2, MaxConfidence: 0.8},
		{DetectionID: detA.ID, ModelID: m2.ID, HitCount: 2, MaxConfidence: 0.9,
			FirstHitAt: &first, LastHitAt: &last, MinConfidence: &minConf, ConfidenceSum: &sum},
		{DetectionID: detA.ID, ModelID: m3.ID, HitCount: 5, MaxConfidence: 0.7},
		{DetectionID: detB.ID, ModelID: m1.ID, HitCount: 1, MaxConfidence: 0.5},
	}
	for _, row := range rows {
		require.NoError(t, db.Table(tableDetectionModelContributions).Create(row).Error)
	}

	got, err := repo.GetModelContributionsByDetectionIDs(ctx, []uint{detA.ID, detB.ID, detC.ID})
	require.NoError(t, err)

	require.Len(t, got[detA.ID], 3)
	assert.Equal(t, []uint{m3.ID, m2.ID, m1.ID}, []uint{got[detA.ID][0].ModelID, got[detA.ID][1].ModelID, got[detA.ID][2].ModelID},
		"ordered by hit count, then max confidence")

	perch := got[detA.ID][1]
	require.NotNil(t, perch.Model)
	assert.Equal(t, "Perch", perch.Model.Name)
	assert.Equal(t, "2", perch.Model.Version)
	assert.Equal(t, "default", perch.Model.Variant)
	assert.Equal(t, entities.ModelTypeMulti, perch.Model.ModelType)
	assert.Equal(t, m2.ID, perch.Model.ID)
	require.NotNil(t, perch.FirstHitAt)
	require.NotNil(t, perch.LastHitAt)
	require.NotNil(t, perch.MinConfidence)
	require.NotNil(t, perch.ConfidenceSum)
	assert.Equal(t, first, *perch.FirstHitAt)
	assert.Equal(t, last, *perch.LastHitAt)
	assert.InDelta(t, minConf, *perch.MinConfidence, 1e-9)
	assert.InDelta(t, sum, *perch.ConfidenceSum, 1e-9)

	birdnet := got[detA.ID][2]
	assert.Nil(t, birdnet.FirstHitAt)
	assert.Nil(t, birdnet.LastHitAt)
	assert.Nil(t, birdnet.MinConfidence)
	assert.Nil(t, birdnet.ConfidenceSum)

	require.Len(t, got[detB.ID], 1)
	assert.Equal(t, "BirdNET", got[detB.ID][0].Model.Name)
	assert.NotContains(t, got, detC.ID)

	t.Run("chunks past batchQuerySize", func(t *testing.T) {
		_, ids := createBulkDetections(t, db, batchQuerySize+1)
		bulk := make([]*entities.DetectionModelContribution, len(ids))
		for i, id := range ids {
			bulk[i] = &entities.DetectionModelContribution{DetectionID: id, ModelID: m1.ID, HitCount: 1, MaxConfidence: 0.5}
		}
		require.NoError(t, db.Table(tableDetectionModelContributions).CreateInBatches(bulk, 100).Error)

		query := append(slices.Clone(ids), ids[0])
		res, err := repo.GetModelContributionsByDetectionIDs(ctx, query)
		require.NoError(t, err)
		assert.Len(t, res, batchQuerySize+1)
		assert.Len(t, res[ids[0]], 1, "a repeated id must not duplicate rows")
		assert.Len(t, res[ids[len(ids)-1]], 1, "the remainder chunk must be read")
	})
}

func TestDetectionRepository_GetModelContributionsByDetectionIDs_V2Prefix(t *testing.T) {
	db, err := gorm.Open(sqlite.Open(filepath.Join(t.TempDir(), "test.db")), &gorm.Config{
		Logger:                                   gorm_logger.Default.LogMode(gorm_logger.Silent),
		DisableForeignKeyConstraintWhenMigrating: true,
	})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close()) })

	require.NoError(t, db.Exec("CREATE TABLE v2_ai_models (id INTEGER PRIMARY KEY AUTOINCREMENT, name VARCHAR(50) NOT NULL, version VARCHAR(20) NOT NULL, variant VARCHAR(100) NOT NULL DEFAULT 'default', model_type VARCHAR(20) NOT NULL, classifier_path VARCHAR(500), created_at DATETIME)").Error)
	require.NoError(t, db.Exec("CREATE TABLE v2_detection_model_contributions (id INTEGER PRIMARY KEY AUTOINCREMENT, detection_id INTEGER NOT NULL, model_id INTEGER NOT NULL, hit_count INTEGER NOT NULL, max_confidence REAL NOT NULL, first_hit_at INTEGER, last_hit_at INTEGER, min_confidence REAL, confidence_sum REAL)").Error)

	model := createTestModel(t, db, tableV2AIModels, "BirdNET", "2.4", entities.ModelTypeBird)
	require.NoError(t, db.Table(tableV2DetectionModelContributions).Create(
		&entities.DetectionModelContribution{DetectionID: 7, ModelID: model.ID, HitCount: 3, MaxConfidence: 0.9}).Error)

	repo := NewDetectionRepository(db, nil, true, false)
	got, err := repo.GetModelContributionsByDetectionIDs(t.Context(), []uint{7})
	require.NoError(t, err)
	require.Len(t, got[7], 1)
	assert.Equal(t, 3, got[7][0].HitCount)
	assert.Equal(t, "BirdNET", got[7][0].Model.Name)
}
