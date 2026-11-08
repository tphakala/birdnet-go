package repository

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// missingDetectionID is a detection ID no fixture row ever gets, used for the
// unknown-ID case of SetClipName.
const missingDetectionID = 424242

// TestSetClipName_SetsEmptyClipName verifies SetClipName fills a NULL clip_name
// and reports that the row changed.
func TestSetClipName_SetsEmptyClipName(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	det := createTestDetection(t, db, 1000)

	const clipName = "2025/06/parus_major_85p_20250601T080000Z.mp3"
	changed, err := repo.SetClipName(t.Context(), det.ID, clipName)
	require.NoError(t, err)
	assert.True(t, changed)

	got, err := repo.Get(t.Context(), det.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ClipName)
	assert.Equal(t, clipName, *got.ClipName)
}

// TestSetClipName_EmptyStringClipNameIsAlsoFillable verifies the guard treats a
// stored empty string like NULL, so rows written by older paths are covered too.
func TestSetClipName_EmptyStringClipNameIsAlsoFillable(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	empty := ""
	det := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: 1000, ClipName: &empty}
	require.NoError(t, db.Table(tableDetections).Create(det).Error)

	const clipName = "2025/06/x.mp3"
	changed, err := repo.SetClipName(t.Context(), det.ID, clipName)
	require.NoError(t, err)
	assert.True(t, changed)

	got, err := repo.Get(t.Context(), det.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ClipName)
	assert.Equal(t, clipName, *got.ClipName)
}

// TestSetClipName_NeverOverwritesExistingClipName verifies the SQL guard: a row
// that already has a clip name is left alone, the call is a documented no-op
// (no error, changed=false).
func TestSetClipName_NeverOverwritesExistingClipName(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	existing := "2025/06/kept.mp3"
	det := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: 1000, ClipName: &existing}
	require.NoError(t, db.Table(tableDetections).Create(det).Error)

	changed, err := repo.SetClipName(t.Context(), det.ID, "2025/06/new.mp3")
	require.NoError(t, err, "a non-empty stored clip name is a documented no-op, not an error")
	assert.False(t, changed)

	got, err := repo.Get(t.Context(), det.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ClipName)
	assert.Equal(t, existing, *got.ClipName)
}

// TestSetClipName_UnknownIDReturnsNotFound verifies an unknown ID is a clean
// sentinel error that callers can test with errors.Is.
func TestSetClipName_UnknownIDReturnsNotFound(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)

	changed, err := repo.SetClipName(t.Context(), missingDetectionID, "2025/06/x.mp3")
	require.Error(t, err)
	require.ErrorIs(t, err, ErrDetectionNotFound)
	assert.False(t, changed)
}

// TestSetClipName_EmptyClipNameIsNoOp verifies an empty clip name argument never
// clears or writes anything.
func TestSetClipName_EmptyClipNameIsNoOp(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	det := createTestDetection(t, db, 1000)

	changed, err := repo.SetClipName(t.Context(), det.ID, "")
	require.NoError(t, err)
	assert.False(t, changed)

	got, err := repo.Get(t.Context(), det.ID)
	require.NoError(t, err)
	assert.Nil(t, got.ClipName)
}

// TestSetClipName_AttachesToLockedDetection verifies that a lock does not stop
// a clip from being attached (the clip link is clip metadata) and that the lock
// is kept.
func TestSetClipName_AttachesToLockedDetection(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	det := createTestDetection(t, db, 1000)
	require.NoError(t, repo.Lock(t.Context(), det.ID))

	const clipName = "2025/06/locked.mp3"
	changed, err := repo.SetClipName(t.Context(), det.ID, clipName)
	require.NoError(t, err)
	assert.True(t, changed)

	got, err := repo.Get(t.Context(), det.ID)
	require.NoError(t, err)
	require.NotNil(t, got.ClipName)
	assert.Equal(t, clipName, *got.ClipName)

	locked, err := repo.IsLocked(t.Context(), det.ID)
	require.NoError(t, err)
	assert.True(t, locked, "attaching a clip must keep the detection's lock")
}

// TestSetClipName_LeavesRetainedSpectrogramRowAlone verifies that a row whose
// audio retention deleted (spectrogram_clip_name set, clip_name empty) does not
// get a clip: the two columns are mutually exclusive and re-attaching would undo
// retention.
func TestSetClipName_LeavesRetainedSpectrogramRowAlone(t *testing.T) {
	db := setupDetectionTestDB(t)
	repo := NewDetectionRepository(db, nil, false, false)
	retained := "2025/06/deleted_by_retention.mp3"
	det := &entities.Detection{LabelID: 1, ModelID: 1, Confidence: 0.9, DetectedAt: 1000, SpectrogramClipName: &retained}
	require.NoError(t, db.Table(tableDetections).Create(det).Error)

	changed, err := repo.SetClipName(t.Context(), det.ID, "2025/06/new.mp3")
	require.NoError(t, err, "a retained-spectrogram row is a documented no-op, not an error")
	assert.False(t, changed)

	var stored entities.Detection
	require.NoError(t, db.Table(tableDetections).First(&stored, det.ID).Error)
	assert.Nil(t, stored.ClipName)
	require.NotNil(t, stored.SpectrogramClipName)
	assert.Equal(t, retained, *stored.SpectrogramClipName)
}

// TestSetClipName_NormalizesAttachedRow verifies the columns the attach writes
// besides clip_name: a sample rate left on the clip-less row by an earlier clip
// is cleared, so the new clip is never reported with the rate of a different
// file, and an empty spectrogram_clip_name (not a retention-pruned row) still
// gets the clip and is normalized to NULL.
func TestSetClipName_NormalizesAttachedRow(t *testing.T) {
	staleRate := 48000
	empty := ""
	tests := []struct {
		name string
		det  entities.Detection
	}{
		{name: "stale sample rate is cleared", det: entities.Detection{ClipSampleRate: &staleRate}},
		{name: "empty spectrogram clip name counts as unset", det: entities.Detection{SpectrogramClipName: &empty}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db := setupDetectionTestDB(t)
			repo := NewDetectionRepository(db, nil, false, false)
			det := tt.det
			det.LabelID, det.ModelID, det.Confidence, det.DetectedAt = 1, 1, 0.9, 1000
			require.NoError(t, db.Table(tableDetections).Create(&det).Error)

			changed, err := repo.SetClipName(t.Context(), det.ID, "2025/06/new.mp3")
			require.NoError(t, err)
			assert.True(t, changed)

			var stored entities.Detection
			require.NoError(t, db.Table(tableDetections).First(&stored, det.ID).Error)
			require.NotNil(t, stored.ClipName)
			assert.Equal(t, "2025/06/new.mp3", *stored.ClipName)
			assert.Nil(t, stored.ClipSampleRate, "the rate of an earlier clip must not survive the attach")
			assert.Nil(t, stored.SpectrogramClipName, "spectrogram_clip_name is NULL after the attach")
		})
	}
}
