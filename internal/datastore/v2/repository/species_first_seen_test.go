package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSpeciesFirstSeenInPeriod(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	labelA := seedLabel(t, db, "Parus major")
	labelB := seedLabel(t, db, "Turdus merula")

	// Parus major: earliest in-range detection at 1000 (a later one at 2000 must not move first-seen).
	seedDetection(t, db, labelA, 2000, 0.8)
	seedDetection(t, db, labelA, 1000, 0.9)
	// Turdus merula: earliest in-range at 1500.
	seedDetection(t, db, labelB, 1500, 0.7)

	t.Run("first-seen is MIN(detected_at) per species, ordered ascending", func(t *testing.T) {
		got, err := repo.GetSpeciesFirstSeenInPeriod(ctx, 500, 5000)
		require.NoError(t, err)
		require.Len(t, got, 2)
		// Ordered by first_detected ASC.
		assert.Equal(t, "Parus major", got[0].ScientificName)
		assert.Equal(t, int64(1000), got[0].FirstDetected)
		assert.Equal(t, "Turdus merula", got[1].ScientificName)
		assert.Equal(t, int64(1500), got[1].FirstDetected)
	})

	t.Run("end is exclusive", func(t *testing.T) {
		// With end=1500, Turdus merula's only detection (at 1500) is excluded; Parus major remains.
		got, err := repo.GetSpeciesFirstSeenInPeriod(ctx, 500, 1500)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "Parus major", got[0].ScientificName)
		assert.Equal(t, int64(1000), got[0].FirstDetected)
	})

	t.Run("empty range returns no rows without error", func(t *testing.T) {
		got, err := repo.GetSpeciesFirstSeenInPeriod(ctx, 100000, 200000)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestGetSpeciesFirstSeenInPeriod_ExcludesFalsePositives(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	withFP := seedLabel(t, db, "Parus major")
	allFP := seedLabel(t, db, "Turdus merula")

	// Parus major's earliest detection is a false positive: first-seen must skip it for the next real one.
	fpID := seedDetection(t, db, withFP, 1000, 0.6)
	seedFalsePositiveReview(t, db, fpID)
	seedDetection(t, db, withFP, 1800, 0.9)

	// Turdus merula has only a false-positive detection: it must not appear at all.
	allFPID := seedDetection(t, db, allFP, 1200, 0.5)
	seedFalsePositiveReview(t, db, allFPID)

	got, err := repo.GetSpeciesFirstSeenInPeriod(ctx, 500, 5000)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Parus major", got[0].ScientificName)
	// 1000 was a false positive and is excluded, so the first real detection (1800) is the first-seen.
	assert.Equal(t, int64(1800), got[0].FirstDetected)
}

func TestGetSpeciesFirstSeenInPeriod_MergesModels(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	// The same species detected under two models collapses to one first-seen: the MIN across labels.
	labelM1 := seedLabelForModel(t, db, "Parus major", 1)
	labelM2 := seedLabelForModel(t, db, "Parus major", 2)
	seedDetectionForModel(t, db, labelM1, 1, 1700, 0.9) // model 1, later
	seedDetectionForModel(t, db, labelM2, 2, 1200, 0.8) // model 2, earliest

	got, err := repo.GetSpeciesFirstSeenInPeriod(ctx, 500, 5000)
	require.NoError(t, err)
	require.Len(t, got, 1) // merged to a single species, not one row per model label
	assert.Equal(t, "Parus major", got[0].ScientificName)
	assert.Equal(t, int64(1200), got[0].FirstDetected)
}
