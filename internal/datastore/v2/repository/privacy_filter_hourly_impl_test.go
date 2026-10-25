package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

func setupPrivacyFilterHourlyTestDB(t *testing.T) *gorm.DB {
	t.Helper()
	db := setupAudioSourceTestDB(t)
	require.NoError(t, db.AutoMigrate(&entities.PrivacyFilterHourly{}))
	return db
}

func newPrivacyHourlyRow(sourceID uint, hourStart int64, modelID string, thresholdMilli int, hits int64, maxScore float64) *entities.PrivacyFilterHourly {
	return &entities.PrivacyFilterHourly{
		HourStart:      hourStart,
		SourceID:       sourceID,
		ModelID:        modelID,
		TriggerType:    "label",
		ThresholdMilli: thresholdMilli,
		Hits:           hits,
		MaxScore:       maxScore,
		MarginLt50:     hits,
	}
}

func TestPrivacyFilterHourlyRepository_AddDeltaAccumulatesPerKey(t *testing.T) {
	t.Parallel()
	db := setupPrivacyFilterHourlyTestDB(t)
	repo := NewPrivacyFilterHourlyRepository(db, nil, false, false)
	ctx := t.Context()

	src := &entities.AudioSource{SourceURI: "test", NodeName: "n", SourceType: entities.SourceTypeFile}
	require.NoError(t, db.Create(src).Error)

	const hour = int64(3600 * 100)
	// Scores arrive lower first, then higher, then lower again.
	for _, d := range []*entities.PrivacyFilterHourly{
		newPrivacyHourlyRow(src.ID, hour, "BirdNET_V2.4", 50, 2, 0.7),
		newPrivacyHourlyRow(src.ID, hour, "BirdNET_V2.4", 50, 3, 0.9),
		newPrivacyHourlyRow(src.ID, hour, "BirdNET_V2.4", 50, 1, 0.8),
	} {
		d.Discards, d.Held, d.Confirmed, d.Ignored, d.UnscannedSaves = 1, 1, 1, 1, 1
		require.NoError(t, repo.AddDelta(ctx, d))
	}
	// Differs only in threshold, and only in model: each is its own row.
	require.NoError(t, repo.AddDelta(ctx, newPrivacyHourlyRow(src.ID, hour, "BirdNET_V2.4", 60, 1, 0.5)))
	require.NoError(t, repo.AddDelta(ctx, newPrivacyHourlyRow(src.ID, hour, "Perch_V2", 50, 1, 0.5)))

	var rows []entities.PrivacyFilterHourly
	require.NoError(t, db.Order("id").Find(&rows).Error)
	require.Len(t, rows, 3)

	first := rows[0]
	assert.Equal(t, int64(6), first.Hits)
	assert.Equal(t, int64(6), first.MarginLt50)
	assert.Equal(t, int64(3), first.Discards)
	assert.Equal(t, int64(3), first.Held)
	assert.Equal(t, int64(3), first.Confirmed)
	assert.Equal(t, int64(3), first.Ignored)
	assert.Equal(t, int64(3), first.UnscannedSaves)
	assert.InDelta(t, 0.9, first.MaxScore, 1e-9)
	assert.Equal(t, int64(1), rows[1].Hits)
	assert.Equal(t, int64(1), rows[2].Hits)
}

func TestPrivacyFilterHourlyRepository_RejectsZeroSource(t *testing.T) {
	t.Parallel()
	db := setupPrivacyFilterHourlyTestDB(t)
	repo := NewPrivacyFilterHourlyRepository(db, nil, false, false)

	err := repo.AddDelta(t.Context(), newPrivacyHourlyRow(0, 3600, "m", 50, 1, 0.5))
	require.ErrorIs(t, err, ErrPrivacyRollupNoSource)

	var count int64
	require.NoError(t, db.Model(&entities.PrivacyFilterHourly{}).Count(&count).Error)
	assert.Zero(t, count)
}

func TestPrivacyFilterHourlyRepository_DeleteBefore(t *testing.T) {
	t.Parallel()
	db := setupPrivacyFilterHourlyTestDB(t)
	repo := NewPrivacyFilterHourlyRepository(db, nil, false, false)
	ctx := t.Context()

	src := &entities.AudioSource{SourceURI: "test", NodeName: "n", SourceType: entities.SourceTypeFile}
	require.NoError(t, db.Create(src).Error)

	const cutoff = int64(3600 * 100)
	for _, h := range []int64{cutoff - 3600, cutoff, cutoff + 3600} {
		require.NoError(t, repo.AddDelta(ctx, newPrivacyHourlyRow(src.ID, h, "m", 50, 1, 0.5)))
	}

	deleted, err := repo.DeleteBefore(ctx, cutoff)
	require.NoError(t, err)
	assert.Equal(t, int64(1), deleted)

	var hours []int64
	require.NoError(t, db.Model(&entities.PrivacyFilterHourly{}).Order("hour_start").Pluck("hour_start", &hours).Error)
	assert.Equal(t, []int64{cutoff, cutoff + 3600}, hours)
}
