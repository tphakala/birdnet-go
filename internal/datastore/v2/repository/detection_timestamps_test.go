package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGetDetectionTimestamps(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	ctx := t.Context()

	labelA := seedLabel(t, db, "Parus major")
	labelB := seedLabel(t, db, "Turdus merula")

	seedDetection(t, db, labelA, 1000, 0.9) // in range, label A
	seedDetection(t, db, labelB, 1500, 0.8) // in range, label B
	seedDetection(t, db, labelA, 2000, 0.7) // in range, label A
	seedDetection(t, db, labelA, 9000, 0.9) // out of range (>= end)
	fpID := seedDetection(t, db, labelA, 3000, 0.6)
	seedFalsePositiveReview(t, db, fpID) // in range but false positive -> excluded

	// The method makes no ordering guarantee (the caller buckets and sorts), so assert on
	// set membership rather than sequence.
	t.Run("all species in range, false positives excluded", func(t *testing.T) {
		got, err := repo.GetDetectionTimestamps(ctx, 500, 5000, nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, []int64{1000, 1500, 2000}, got)
	})

	t.Run("species filter", func(t *testing.T) {
		got, err := repo.GetDetectionTimestamps(ctx, 500, 5000, &labelA)
		require.NoError(t, err)
		assert.ElementsMatch(t, []int64{1000, 2000}, got)
	})

	t.Run("end is exclusive", func(t *testing.T) {
		got, err := repo.GetDetectionTimestamps(ctx, 500, 2000, nil)
		require.NoError(t, err)
		assert.ElementsMatch(t, []int64{1000, 1500}, got)
	})

	t.Run("empty range returns no rows without error", func(t *testing.T) {
		got, err := repo.GetDetectionTimestamps(ctx, 100000, 200000, nil)
		require.NoError(t, err)
		assert.Empty(t, got)
	})
}
