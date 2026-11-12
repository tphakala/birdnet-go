package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

const clipDenoisedTestName = "2026/01/denoised.flac"

// seedClipDetection stores a detection that has a clip name and returns its ID.
func seedClipDetection(t *testing.T, repo DetectionRepository, labelID uint, clipName string) uint {
	t.Helper()
	clip := clipName
	det := &entities.Detection{
		LabelID:    labelID,
		DetectedAt: 1000,
		Confidence: 0.9,
		ModelID:    1,
		ClipName:   &clip,
	}
	require.NoError(t, repo.Save(t.Context(), det))
	return det.ID
}

func TestRecordClipDenoised(t *testing.T) {
	db := setupInsightsTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	label := seedLabel(t, db, "Eptesicus nilssonii")

	t.Run("stores true and false while the clip name matches", func(t *testing.T) {
		id := seedClipDetection(t, repo, label, clipDenoisedTestName)

		stored, err := repo.Get(t.Context(), id)
		require.NoError(t, err)
		assert.Nil(t, stored.ClipDenoised, "a new row has no marker")

		require.NoError(t, repo.RecordClipDenoised(t.Context(), id, clipDenoisedTestName, true))
		stored, err = repo.Get(t.Context(), id)
		require.NoError(t, err)
		require.NotNil(t, stored.ClipDenoised)
		assert.True(t, *stored.ClipDenoised)

		require.NoError(t, repo.RecordClipDenoised(t.Context(), id, clipDenoisedTestName, false))
		stored, err = repo.Get(t.Context(), id)
		require.NoError(t, err)
		require.NotNil(t, stored.ClipDenoised)
		assert.False(t, *stored.ClipDenoised)
	})

	t.Run("a different or empty clip name writes nothing", func(t *testing.T) {
		id := seedClipDetection(t, repo, label, "2026/01/other.flac")

		require.NoError(t, repo.RecordClipDenoised(t.Context(), id, clipDenoisedTestName, true))
		require.NoError(t, repo.RecordClipDenoised(t.Context(), id, "", true))

		stored, err := repo.Get(t.Context(), id)
		require.NoError(t, err)
		assert.Nil(t, stored.ClipDenoised)
	})
}
