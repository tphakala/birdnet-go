package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

const (
	discardTestCutoff = int64(2_000_000)
	discardTestHour   = int64(3600 * 100)
)

// TestPrivacyFilterDiscardRepository_SQLite runs the discard repository on the
// real SQLite manager, so the production DSN (foreign keys on) and the real
// entity registration are used.
func TestPrivacyFilterDiscardRepository_SQLite(t *testing.T) {
	t.Parallel()
	mgr, err := NewSQLiteManager(Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())

	exercisePrivacyDiscardRepository(t, mgr.DB(), false, false)
}

// exercisePrivacyDiscardRepository checks, in order: a save round-trips every
// column, a NULL source is stored, retention deletes by time and takes the hit
// rows with it, and deleting an audio source removes its discard rows, their hit
// rows and its hourly rollup rows but keeps the NULL-source row.
func exercisePrivacyDiscardRepository(t *testing.T, db *gorm.DB, useV2Prefix, isMySQL bool) {
	t.Helper()
	ctx := t.Context()
	repo := repository.NewPrivacyFilterDiscardRepository(db, nil, useV2Prefix, isMySQL)
	hourly := repository.NewPrivacyFilterHourlyRepository(db, nil, useV2Prefix, isMySQL)
	sources := repository.NewAudioSourceRepository(db, nil, useV2Prefix, isMySQL)

	src, err := sources.GetOrCreate(ctx, "discard-test", "n", nil, entities.SourceType(""))
	require.NoError(t, err)

	discardTable, hitTable, hourlyTable := "privacy_filter_discards", "privacy_filter_discard_hits", "privacy_filter_hourlies"
	if useV2Prefix {
		discardTable, hitTable, hourlyTable = V2TableName(discardTable), V2TableName(hitTable), V2TableName(hourlyTable)
	}
	count := func(table string) int64 {
		var n int64
		require.NoError(t, db.Table(table).Count(&n).Error)
		return n
	}
	newDiscard := func(at int64, sourceID *uint) *entities.PrivacyFilterDiscard {
		return &entities.PrivacyFilterDiscard{
			DiscardedAt: at, SourceID: sourceID, ScientificName: "Parus major", CommonName: "Great Tit",
			Confidence: 0.87, ModelID: "m1", ClipBegin: at - 3000, ClipEnd: at + 6000,
			LabelThresholdMilli: 50, VADEnabled: true, VADThresholdMilli: 350, HitCountsLowerBound: true,
			Hits: []entities.PrivacyFilterDiscardHit{
				{Detector: "m1", TriggerType: "label", HitCount: 2, MinScore: 0.25, MaxScore: 0.75},
				{Detector: "silero_vad", TriggerType: "vad", HitCount: 1, MinScore: 0.5, MaxScore: 0.5},
			},
		}
	}

	require.ErrorIs(t, repo.Save(ctx, nil), repository.ErrPrivacyDiscardNil)

	// Round trip.
	withSource := newDiscard(discardTestCutoff+10, &src.ID)
	require.NoError(t, repo.Save(ctx, withSource))
	require.NotZero(t, withSource.ID)
	var got entities.PrivacyFilterDiscard
	require.NoError(t, db.Table(discardTable).First(&got, withSource.ID).Error)
	require.NotNil(t, got.SourceID)
	assert.Equal(t, src.ID, *got.SourceID)
	assert.Equal(t, withSource.DiscardedAt, got.DiscardedAt)
	assert.Equal(t, "Parus major", got.ScientificName)
	assert.Equal(t, "Great Tit", got.CommonName)
	assert.InDelta(t, 0.87, got.Confidence, 1e-9)
	assert.Equal(t, "m1", got.ModelID)
	assert.Equal(t, withSource.ClipBegin, got.ClipBegin)
	assert.Equal(t, withSource.ClipEnd, got.ClipEnd)
	assert.Equal(t, 50, got.LabelThresholdMilli)
	assert.True(t, got.VADEnabled)
	assert.Equal(t, 350, got.VADThresholdMilli)
	assert.True(t, got.HitCountsLowerBound)
	var hits []entities.PrivacyFilterDiscardHit
	require.NoError(t, db.Table(hitTable).Where("discard_id = ?", withSource.ID).Order("id").Find(&hits).Error)
	require.Len(t, hits, 2)
	assert.Equal(t, "m1", hits[0].Detector)
	assert.Equal(t, "label", hits[0].TriggerType)
	assert.Equal(t, 2, hits[0].HitCount)
	assert.InDelta(t, 0.25, hits[0].MinScore, 1e-9)
	assert.InDelta(t, 0.75, hits[0].MaxScore, 1e-9)
	assert.Equal(t, "vad", hits[1].TriggerType)

	// NULL source.
	orphan := newDiscard(discardTestCutoff+20, nil)
	require.NoError(t, repo.Save(ctx, orphan))
	var orphanRow entities.PrivacyFilterDiscard
	require.NoError(t, db.Table(discardTable).First(&orphanRow, orphan.ID).Error)
	assert.Nil(t, orphanRow.SourceID)

	// Retention: the row before the cutoff goes with its hits, the row at it stays.
	old := newDiscard(discardTestCutoff-1, &src.ID)
	atCutoff := newDiscard(discardTestCutoff, &src.ID)
	require.NoError(t, repo.Save(ctx, old))
	require.NoError(t, repo.Save(ctx, atCutoff))
	require.Equal(t, int64(4), count(discardTable))
	require.Equal(t, int64(8), count(hitTable))
	deleted, err := repo.DeleteBefore(ctx, discardTestCutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)
	assert.Equal(t, int64(3), count(discardTable))
	assert.Equal(t, int64(6), count(hitTable), "the hit rows of the pruned discard are deleted with it")

	// Source deletion cascades to discards, their hits and the hourly rollup.
	require.NoError(t, hourly.AddDelta(ctx, &entities.PrivacyFilterHourly{
		HourStart: discardTestHour, SourceID: src.ID, ModelID: "m1", TriggerType: "label", ThresholdMilli: 50, Hits: 1,
	}))
	require.Equal(t, int64(1), count(hourlyTable))
	require.NoError(t, sources.Delete(ctx, src.ID))
	assert.Equal(t, int64(1), count(discardTable), "only the NULL-source discard is left")
	assert.Equal(t, int64(2), count(hitTable))
	assert.Zero(t, count(hourlyTable), "the hourly rollup rows go with the source")
}
