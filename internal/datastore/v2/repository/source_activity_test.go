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

// setupSourceTestDB mirrors setupInsightsTestDB but also migrates the audio_sources table, which the
// source-activity aggregation INNER JOINs against. Tests in this file run sequentially (no t.Parallel),
// matching the shared in-memory DSN used across the repository tests.
func setupSourceTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open("file::memory:?cache=shared&_foreign_keys=ON"), &gorm.Config{
		Logger: gorm_logger.Default.LogMode(gorm_logger.Silent),
	})
	require.NoError(t, err)

	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	t.Cleanup(func() { require.NoError(t, sqlDB.Close(), "failed to close test database") })

	require.NoError(t, db.AutoMigrate(
		&entities.LabelType{},
		&entities.AIModel{},
		&entities.Label{},
		&entities.AudioSource{},
		&entities.Detection{},
		&entities.DetectionReview{},
	))

	require.NoError(t, db.Create(&entities.LabelType{ID: 1, Name: "species"}).Error)
	require.NoError(t, db.Create(&entities.AIModel{ID: 1, Name: "BirdNET", Version: "2.4"}).Error)

	return db
}

// seedAudioSource creates an audio source row and returns its ID.
func seedAudioSource(t *testing.T, db *gorm.DB, uri, node, sourceType string, displayName *string) uint {
	t.Helper()
	src := entities.AudioSource{
		SourceURI:   uri,
		NodeName:    node,
		SourceType:  entities.SourceType(sourceType),
		DisplayName: displayName,
	}
	require.NoError(t, db.Create(&src).Error)
	return src.ID
}

// seedDetectionWithSource creates a detection bound to the given audio source (nil = source-less, as a
// legacy-migrated detection would be).
func seedDetectionWithSource(t *testing.T, db *gorm.DB, labelID uint, detectedAt int64, confidence float64, sourceID *uint) uint {
	t.Helper()
	det := entities.Detection{
		LabelID:    labelID,
		DetectedAt: detectedAt,
		Confidence: confidence,
		ModelID:    1,
		SourceID:   sourceID,
	}
	require.NoError(t, db.Create(&det).Error)
	return det.ID
}

func TestGetSourceActivitySummaries(t *testing.T) {
	db := setupSourceTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	label := seedLabel(t, db, "Parus major")
	nameA := "Backyard"
	srcA := seedAudioSource(t, db, "rtsp://cam-a", "node-a", "rtsp", &nameA)
	srcB := seedAudioSource(t, db, "hw:0,0", "node-b", "alsa", nil)
	// srcC has no detections and must never appear in the summaries.
	seedAudioSource(t, db, "hw:1,0", "node-c", "alsa", nil)

	// srcA: 3 detections (the higher-volume source); srcB: 1 detection.
	seedDetectionWithSource(t, db, label, 1000, 0.8, &srcA)
	seedDetectionWithSource(t, db, label, 2000, 0.9, &srcA)
	seedDetectionWithSource(t, db, label, 3000, 0.7, &srcA)
	seedDetectionWithSource(t, db, label, 1500, 0.7, &srcB)
	// A source-less (legacy-migrated) detection: the INNER JOIN must drop it entirely.
	seedDetectionWithSource(t, db, label, 1600, 0.7, nil)

	t.Run("groups by source, counts, ordered by volume", func(t *testing.T) {
		got, err := repo.GetSourceActivitySummaries(ctx, 500, 5000)
		require.NoError(t, err)
		require.Len(t, got, 2) // srcC (no detections) and the nil-source detection produce no rows

		assert.Equal(t, srcA, got[0].SourceID)
		require.NotNil(t, got[0].DisplayName)
		assert.Equal(t, "Backyard", *got[0].DisplayName)
		assert.Equal(t, "node-a", got[0].NodeName)
		assert.Equal(t, "rtsp", got[0].SourceType)
		assert.Equal(t, 3, got[0].Count)

		assert.Equal(t, srcB, got[1].SourceID)
		assert.Nil(t, got[1].DisplayName)
		assert.Equal(t, "node-b", got[1].NodeName)
		assert.Equal(t, "alsa", got[1].SourceType)
		assert.Equal(t, 1, got[1].Count)
	})

	t.Run("end is exclusive", func(t *testing.T) {
		// With end=3000, srcA's detection at 3000 is excluded: its count drops to 2 (still first).
		got, err := repo.GetSourceActivitySummaries(ctx, 500, 3000)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, srcA, got[0].SourceID)
		assert.Equal(t, 2, got[0].Count)
		assert.Equal(t, srcB, got[1].SourceID)
		assert.Equal(t, 1, got[1].Count)
	})

	t.Run("empty range returns no rows without error", func(t *testing.T) {
		got, err := repo.GetSourceActivitySummaries(ctx, 100000, 200000)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestGetSourceActivitySummaries_ExcludesFalsePositives(t *testing.T) {
	db := setupSourceTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	label := seedLabel(t, db, "Parus major")
	nameA := "Backyard"
	srcA := seedAudioSource(t, db, "rtsp://cam-a", "node-a", "rtsp", &nameA)
	srcB := seedAudioSource(t, db, "hw:0,0", "node-b", "alsa", nil)

	// srcA: 2 genuine detections + 1 false positive -> count must be 2, not 3.
	seedDetectionWithSource(t, db, label, 1000, 0.8, &srcA)
	seedDetectionWithSource(t, db, label, 2000, 0.9, &srcA)
	fp := seedDetectionWithSource(t, db, label, 2500, 0.4, &srcA)
	seedFalsePositiveReview(t, db, fp)

	// srcB has only a false-positive detection: it must not appear at all.
	onlyFP := seedDetectionWithSource(t, db, label, 1200, 0.3, &srcB)
	seedFalsePositiveReview(t, db, onlyFP)

	got, err := repo.GetSourceActivitySummaries(ctx, 500, 5000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, srcA, got[0].SourceID)
	assert.Equal(t, 2, got[0].Count)
}
