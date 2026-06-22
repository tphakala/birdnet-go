package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetSpeciesPhenologyInPeriod(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	labelA := seedLabel(t, db, "Parus major")
	labelB := seedLabel(t, db, "Turdus merula")

	// Parus major: 3 detections spanning 1000..3000 (the higher-volume species).
	seedDetection(t, db, labelA, 1000, 0.8)
	seedDetection(t, db, labelA, 2000, 0.9)
	seedDetection(t, db, labelA, 3000, 0.7)
	// Turdus merula: a single detection at 1500.
	seedDetection(t, db, labelB, 1500, 0.7)

	t.Run("first/last/count per species, ranked by volume", func(t *testing.T) {
		got, err := repo.GetSpeciesPhenologyInPeriod(ctx, 500, 5000, 10)
		require.NoError(t, err)
		require.Len(t, got, 2)
		// Ordered by count DESC: Parus major (3) before Turdus merula (1).
		assert.Equal(t, "Parus major", got[0].ScientificName)
		assert.Equal(t, int64(1000), got[0].FirstDetected)
		assert.Equal(t, int64(3000), got[0].LastDetected)
		assert.Equal(t, 3, got[0].Count)
		assert.Equal(t, "Turdus merula", got[1].ScientificName)
		assert.Equal(t, int64(1500), got[1].FirstDetected)
		assert.Equal(t, int64(1500), got[1].LastDetected)
		assert.Equal(t, 1, got[1].Count)
	})

	t.Run("limit caps the number of species to the top-N by volume", func(t *testing.T) {
		got, err := repo.GetSpeciesPhenologyInPeriod(ctx, 500, 5000, 1)
		require.NoError(t, err)
		require.Len(t, got, 1)
		assert.Equal(t, "Parus major", got[0].ScientificName)
	})

	t.Run("end is exclusive", func(t *testing.T) {
		// With end=3000, Parus major's detection at 3000 is excluded: last becomes 2000, count 2.
		got, err := repo.GetSpeciesPhenologyInPeriod(ctx, 500, 3000, 10)
		require.NoError(t, err)
		require.Len(t, got, 2)
		assert.Equal(t, "Parus major", got[0].ScientificName)
		assert.Equal(t, int64(1000), got[0].FirstDetected)
		assert.Equal(t, int64(2000), got[0].LastDetected)
		assert.Equal(t, 2, got[0].Count)
	})

	t.Run("empty range returns no rows without error", func(t *testing.T) {
		got, err := repo.GetSpeciesPhenologyInPeriod(ctx, 100000, 200000, 10)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}

func TestGetSpeciesPhenologyInPeriod_ExcludesFalsePositives(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	withFP := seedLabel(t, db, "Parus major")
	allFP := seedLabel(t, db, "Turdus merula")

	// Parus major: the earliest (1000) and latest (3000) detections are false positives, so the real
	// residency span must shrink to the genuine detections 1800..2500 (count 2), not 1000..3000.
	fpEarly := seedDetection(t, db, withFP, 1000, 0.6)
	seedFalsePositiveReview(t, db, fpEarly)
	seedDetection(t, db, withFP, 1800, 0.9)
	seedDetection(t, db, withFP, 2500, 0.85)
	fpLate := seedDetection(t, db, withFP, 3000, 0.5)
	seedFalsePositiveReview(t, db, fpLate)

	// Turdus merula has only a false-positive detection: it must not appear at all.
	allFPID := seedDetection(t, db, allFP, 1200, 0.5)
	seedFalsePositiveReview(t, db, allFPID)

	got, err := repo.GetSpeciesPhenologyInPeriod(ctx, 500, 5000, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Parus major", got[0].ScientificName)
	assert.Equal(t, int64(1800), got[0].FirstDetected)
	assert.Equal(t, int64(2500), got[0].LastDetected)
	assert.Equal(t, 2, got[0].Count)
}

func TestGetSpeciesPhenologyInPeriod_MergesModels(t *testing.T) {
	db := setupInsightsTestDBMultiModel(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	// The same species detected under two models collapses to one residency span: MIN/MAX/COUNT
	// across all the species' label IDs, not one row per model label.
	labelM1 := seedLabelForModel(t, db, "Parus major", 1)
	labelM2 := seedLabelForModel(t, db, "Parus major", 2)
	seedDetectionForModel(t, db, labelM1, 1, 1700, 0.9)
	seedDetectionForModel(t, db, labelM2, 2, 1200, 0.8) // earliest, under model 2
	seedDetectionForModel(t, db, labelM1, 1, 2600, 0.7) // latest, under model 1

	got, err := repo.GetSpeciesPhenologyInPeriod(ctx, 500, 5000, 10)
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, "Parus major", got[0].ScientificName)
	assert.Equal(t, int64(1200), got[0].FirstDetected) // MIN across models
	assert.Equal(t, int64(2600), got[0].LastDetected)  // MAX across models
	assert.Equal(t, 3, got[0].Count)                   // COUNT across models
}
