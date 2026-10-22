package v2only

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_LatestAcrossLabels pins the
// cases the per-label lookup has to get right beyond the name matching covered in
// TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore: the latest detection across
// every label of the species (one per model), falling back past a false positive to
// the previous detection, and bucketing the date with the zone offset in effect at
// the before date even when the detection itself is on the other side of a DST change.
func TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_LatestAcrossLabels(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()
	assertLastDetectionDateAcrossLabels(t, ds)
}

// assertLastDetectionDateAcrossLabels seeds and checks the cases described on
// TestV2OnlyDatastore_GetSpeciesLastDetectionDateBefore_LatestAcrossLabels against
// an empty datastore, so the SQLite and MySQL tests share them.
func assertLastDetectionDateAcrossLabels(t *testing.T, ds *Datastore) {
	t.Helper()
	helsinki, err := time.LoadLocation("Europe/Helsinki")
	require.NoError(t, err)
	ds.timezone = helsinki
	ctx := t.Context()

	markFalsePositive := func(t *testing.T, det *entities.Detection) {
		t.Helper()
		require.NoError(t, ds.manager.DB().Create(&entities.DetectionReview{
			DetectionID: det.ID,
			Verified:    entities.VerificationFalsePositive,
		}).Error)
	}

	t.Run("latest detection across labels of every model", func(t *testing.T) {
		const species = "Turdus merula"
		other := &entities.AIModel{Name: "Perch", Version: "2", Variant: "default", ModelType: entities.ModelTypeMulti}
		require.NoError(t, ds.manager.DB().Create(other).Error)
		perchLabel, err := ds.label.GetOrCreate(ctx, species, other.ID, ds.speciesLabelTypeID, ds.avesClassID)
		require.NoError(t, err)

		seedDetection(t, ds, species, time.Date(2024, 6, 10, 12, 0, 0, 0, helsinki))
		require.NoError(t, ds.detection.Save(ctx, &entities.Detection{
			ModelID:    other.ID,
			LabelID:    perchLabel.ID,
			DetectedAt: time.Date(2024, 6, 12, 12, 0, 0, 0, helsinki).Unix(),
			Confidence: 0.9,
		}))
		seedDetection(t, ds, species, time.Date(2024, 6, 11, 12, 0, 0, 0, helsinki))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, species, "2024-06-15")
		require.NoError(t, err)
		assert.Equal(t, "2024-06-12", got)
	})

	t.Run("false positive latest falls back to the previous detection", func(t *testing.T) {
		const species = "Parus major"
		seedDetection(t, ds, species, time.Date(2024, 6, 9, 12, 0, 0, 0, helsinki))
		markFalsePositive(t, seedDetection(t, ds, species, time.Date(2024, 6, 13, 12, 0, 0, 0, helsinki)))
		markFalsePositive(t, seedDetection(t, ds, species, time.Date(2024, 6, 14, 12, 0, 0, 0, helsinki)))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, species, "2024-06-15")
		require.NoError(t, err)
		assert.Equal(t, "2024-06-09", got)
	})

	t.Run("only false positives before the cutoff returns empty", func(t *testing.T) {
		const species = "Cyanistes caeruleus"
		markFalsePositive(t, seedDetection(t, ds, species, time.Date(2024, 6, 13, 12, 0, 0, 0, helsinki)))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, species, "2024-06-15")
		require.NoError(t, err)
		assert.Empty(t, got)
	})

	t.Run("date uses the offset at the before date", func(t *testing.T) {
		const species = "Erithacus rubecula"
		// 00:30 EEST (UTC+3) on 2024-10-01 is 21:30 UTC on 2024-09-30. The before date is
		// in EET (UTC+2), and the date is bucketed with that single anchored offset, so
		// the detection reports as 2024-09-30 (23:30 at UTC+2).
		seedDetection(t, ds, species, time.Date(2024, 10, 1, 0, 30, 0, 0, helsinki))

		got, err := ds.GetSpeciesLastDetectionDateBefore(ctx, species, "2024-11-15")
		require.NoError(t, err)
		assert.Equal(t, "2024-09-30", got)

		// Anchored inside summer time the same detection reports its local date.
		got, err = ds.GetSpeciesLastDetectionDateBefore(ctx, species, "2024-10-15")
		require.NoError(t, err)
		assert.Equal(t, "2024-10-01", got)
	})

	t.Run("invalid before date is rejected", func(t *testing.T) {
		_, err := ds.GetSpeciesLastDetectionDateBefore(ctx, "Parus major", "15.06.2024")
		require.Error(t, err)
	})
}
