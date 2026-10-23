package v2only

import (
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// seedDetectionWithClip inserts a detection whose clip_name is clipName.
func seedDetectionWithClip(t *testing.T, ds *Datastore, clipName string) uint {
	t.Helper()
	det := seedDetection(t, ds, "Turdus merula", time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	require.NoError(t, ds.manager.DB().Model(&entities.Detection{}).
		Where("id = ?", det.ID).Update("clip_name", clipName).Error)
	return det.ID
}

// loadDetection reloads a detection row by ID.
func loadDetection(t *testing.T, ds *Datastore, id uint) entities.Detection {
	t.Helper()
	var det entities.Detection
	require.NoError(t, ds.manager.DB().First(&det, id).Error)
	return det
}

func TestRetainNoteSpectrogramsByClipNames_MovesNameAndNullsClip(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	matched := seedDetectionWithClip(t, ds, "2026/01/a.wav")
	untouched := seedDetectionWithClip(t, ds, "2026/01/b.wav")

	affected, err := ds.RetainNoteSpectrogramsByClipNames([]string{"2026/01/a.wav"})
	require.NoError(t, err)
	assert.Equal(t, int64(1), affected)

	got := loadDetection(t, ds, matched)
	assert.Nil(t, got.ClipName, "clip_name must be cleared")
	require.NotNil(t, got.SpectrogramClipName, "spectrogram_clip_name must hold the old clip name")
	assert.Equal(t, "2026/01/a.wav", *got.SpectrogramClipName)

	other := loadDetection(t, ds, untouched)
	require.NotNil(t, other.ClipName)
	assert.Equal(t, "2026/01/b.wav", *other.ClipName)
	assert.Nil(t, other.SpectrogramClipName)

	// A rerun matches nothing and must not overwrite the stored name with NULL.
	affected, err = ds.RetainNoteSpectrogramsByClipNames([]string{"2026/01/a.wav"})
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
	got = loadDetection(t, ds, matched)
	require.NotNil(t, got.SpectrogramClipName)
	assert.Equal(t, "2026/01/a.wav", *got.SpectrogramClipName)
}

func TestRetainNoteSpectrogramsByClipNames_BatchesLargeInput(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	const count = 1100 // more than two batches of 500
	names := make([]string, 0, count)
	for i := range count {
		names = append(names, fmt.Sprintf("2026/01/clip%04d.wav", i))
	}
	first := seedDetectionWithClip(t, ds, names[0])
	last := seedDetectionWithClip(t, ds, names[count-1])

	affected, err := ds.RetainNoteSpectrogramsByClipNames(names)
	require.NoError(t, err)
	assert.Equal(t, int64(2), affected)
	for _, id := range []uint{first, last} {
		got := loadDetection(t, ds, id)
		assert.Nil(t, got.ClipName)
		assert.NotNil(t, got.SpectrogramClipName)
	}
}

func TestRetainNoteSpectrogramsByClipNames_EmptyInput(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	affected, err := ds.RetainNoteSpectrogramsByClipNames(nil)
	require.NoError(t, err)
	assert.Equal(t, int64(0), affected)
}

func TestDetectionToNote_MapsSpectrogramClipName(t *testing.T) {
	t.Parallel()

	ds := &Datastore{timezone: time.UTC}
	name := "2026/01/a.wav"

	note := ds.detectionToNote(&entities.Detection{ID: 1, SpectrogramClipName: &name})
	assert.Equal(t, name, note.SpectrogramClipName)
	assert.Empty(t, note.ClipName)

	note = ds.detectionToNote(&entities.Detection{ID: 2})
	assert.Empty(t, note.SpectrogramClipName)
}

func TestDetectionToRecord_SpectrogramOnly(t *testing.T) {
	t.Parallel()

	ds := &Datastore{timezone: time.UTC}
	name := "2026/01/a.wav"
	empty := ""

	tests := []struct {
		name     string
		clip     *string
		spec     *string
		wantOnly bool
		wantAud  bool
	}{
		{"audio present", &name, nil, false, true},
		{"spectrogram only", nil, &name, true, false},
		{"audio present wins over stale spectrogram name", &name, &name, false, true},
		{"empty spectrogram name", nil, &empty, false, false},
		{"neither", nil, nil, false, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			rec := ds.detectionToRecord(&entities.Detection{ID: 1, ClipName: tc.clip, SpectrogramClipName: tc.spec})
			assert.Equal(t, tc.wantOnly, rec.SpectrogramOnly)
			assert.Equal(t, tc.wantAud, rec.HasAudio)
		})
	}
}

func TestGetNoteKeptSpectrogram(t *testing.T) {
	t.Parallel()

	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	moved := seedDetectionWithClip(t, ds, "2026/01/a.wav")
	_, err := ds.RetainNoteSpectrogramsByClipNames([]string{"2026/01/a.wav"})
	require.NoError(t, err)
	withAudio := seedDetectionWithClip(t, ds, "2026/01/b.wav")

	t.Run("returns the kept clip name and the model type", func(t *testing.T) {
		clip, modelType, err := ds.GetNoteKeptSpectrogram(strconv.FormatUint(uint64(moved), 10))
		require.NoError(t, err)
		assert.Equal(t, "2026/01/a.wav", clip)
		assert.Equal(t, string(entities.ModelTypeBird), modelType)
	})

	t.Run("a detection with audio has no kept spectrogram", func(t *testing.T) {
		clip, _, err := ds.GetNoteKeptSpectrogram(strconv.FormatUint(uint64(withAudio), 10))
		require.NoError(t, err)
		assert.Empty(t, clip)
	})

	t.Run("an unknown detection is not found", func(t *testing.T) {
		_, _, err := ds.GetNoteKeptSpectrogram("999999")
		require.ErrorIs(t, err, repository.ErrDetectionNotFound)
	})
}
