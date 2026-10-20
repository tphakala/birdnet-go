package v2only

import (
	"context"
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

const (
	testBatClipRate  = 384000
	testClipName     = "2026/01/bat.flac"
	otherClipRate    = 192000
	testClipRateDate = "2026-01-02"
	testClipRateTime = "03:04:05"
)

// saveRateNote saves a note with the given clip name and rate and returns its ID.
func saveRateNote(t *testing.T, ds *Datastore, clipName string, rate int) uint {
	t.Helper()
	note := &datastore.Note{
		Date:           testClipRateDate,
		Time:           testClipRateTime,
		ScientificName: "Myotis daubentonii",
		CommonName:     "Daubenton's bat",
		Confidence:     0.9,
		ClipName:       clipName,
		ClipSampleRate: rate,
		Model:          perchModelInfo(),
	}
	require.NoError(t, ds.Save(note, nil))
	require.NotZero(t, note.ID)
	return note.ID
}

func TestClipSampleRate_SaveGetSearchRoundTrip(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	withRate := saveRateNote(t, ds, testClipName, testBatClipRate)
	noRate := saveRateNote(t, ds, "2026/01/old.wav", 0)
	noClip := saveRateNote(t, ds, "", testBatClipRate)

	assert.Equal(t, testBatClipRate, loadRate(t, ds, withRate))
	assert.Zero(t, loadRate(t, ds, noRate), "an unknown rate must stay NULL")
	assert.Zero(t, loadRate(t, ds, noClip), "a row without a clip must not store a rate")
	assert.Nil(t, loadDetection(t, ds, noRate).ClipSampleRate)
	assert.Nil(t, loadDetection(t, ds, noClip).ClipSampleRate)

	got, err := ds.Get(strconv.FormatUint(uint64(withRate), 10))
	require.NoError(t, err)
	assert.Equal(t, testBatClipRate, got.ClipSampleRate)

	records, _, err := ds.SearchDetections(&datastore.SearchFilters{Page: 1, PerPage: 50, Ctx: t.Context()})
	require.NoError(t, err)
	byID := map[string]int{}
	for i := range records {
		byID[records[i].ID] = records[i].ClipSampleRate
	}
	assert.Equal(t, testBatClipRate, byID[strconv.FormatUint(uint64(withRate), 10)])
	assert.Zero(t, byID[strconv.FormatUint(uint64(noRate), 10)])
}

func TestClipSampleRate_SurvivesRetention(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	id := saveRateNote(t, ds, testClipName, testBatClipRate)
	affected, err := ds.RetainNoteSpectrogramsByClipNames([]string{testClipName})
	require.NoError(t, err)
	require.Equal(t, int64(1), affected)

	got := loadDetection(t, ds, id)
	assert.Nil(t, got.ClipName)
	require.NotNil(t, got.ClipSampleRate, "retention must keep the capture rate")
	assert.Equal(t, testBatClipRate, *got.ClipSampleRate)
}

func TestRecordClipSampleRate_GuardedByClipName(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	id := saveRateNote(t, ds, testClipName, testBatClipRate)

	// A mismatching clip name changes nothing.
	require.NoError(t, ds.RecordClipSampleRate(t.Context(), id, "other.wav", otherClipRate))
	assert.Equal(t, testBatClipRate, loadRate(t, ds, id))

	// An empty clip name or a non-positive rate is a no-op.
	require.NoError(t, ds.RecordClipSampleRate(t.Context(), id, "", otherClipRate))
	require.NoError(t, ds.RecordClipSampleRate(t.Context(), id, testClipName, 0))
	assert.Equal(t, testBatClipRate, loadRate(t, ds, id))

	// The matching clip name overwrites an existing value (authoritative).
	require.NoError(t, ds.RecordClipSampleRate(t.Context(), id, testClipName, otherClipRate))
	assert.Equal(t, otherClipRate, loadRate(t, ds, id))

	// A cancelled context fails the write and leaves the row unchanged.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	err := ds.RecordClipSampleRate(ctx, id, testClipName, testBatClipRate)
	require.ErrorIs(t, err, context.Canceled)
	assert.Equal(t, otherClipRate, loadRate(t, ds, id))
}

func TestFillClipSampleRate_OnlyFromNull(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	nullRow := saveRateNote(t, ds, testClipName, 0)
	exporterRow := saveRateNote(t, ds, "2026/01/exp.flac", testBatClipRate)

	// 1. A NULL row is filled.
	changed, err := ds.FillClipSampleRate(t.Context(), nullRow, testClipName, testBatClipRate)
	require.NoError(t, err)
	assert.True(t, changed)
	assert.Equal(t, testBatClipRate, loadRate(t, ds, nullRow))

	// 2. A second fill with a different rate changes nothing.
	changed, err = ds.FillClipSampleRate(t.Context(), nullRow, testClipName, otherClipRate)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, testBatClipRate, loadRate(t, ds, nullRow))

	// 3. A row the exporter already wrote is left alone.
	changed, err = ds.FillClipSampleRate(t.Context(), exporterRow, "2026/01/exp.flac", otherClipRate)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Equal(t, testBatClipRate, loadRate(t, ds, exporterRow))

	// 4. A clip_name mismatch on a NULL row changes nothing.
	another := saveRateNote(t, ds, "2026/01/x.flac", 0)
	changed, err = ds.FillClipSampleRate(t.Context(), another, "renamed.flac", testBatClipRate)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Zero(t, loadRate(t, ds, another))

	// 5. A cancelled context fails the write and leaves a NULL row NULL.
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	changed, err = ds.FillClipSampleRate(ctx, another, "2026/01/x.flac", testBatClipRate)
	require.ErrorIs(t, err, context.Canceled)
	assert.False(t, changed)
	assert.Zero(t, loadRate(t, ds, another))
}

func loadRate(t *testing.T, ds *Datastore, id uint) int {
	t.Helper()
	det := loadDetection(t, ds, id)
	return clipSampleRateValue(det.ClipSampleRate)
}

// TestFillClipSampleRate_IgnoresZeroRateAndEmptyClipName verifies the fill guards:
// a non-positive rate is never stored, and an empty clip name does not match a row
// whose stored clip_name is the empty string.
func TestFillClipSampleRate_IgnoresZeroRateAndEmptyClipName(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	withClip := saveRateNote(t, ds, testClipName, 0)
	changed, err := ds.FillClipSampleRate(t.Context(), withClip, testClipName, 0)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, loadDetection(t, ds, withClip).ClipSampleRate, "a zero rate must leave the row NULL")

	emptyName := saveRateNote(t, ds, testClipName, 0)
	require.NoError(t, ds.manager.DB().Model(&entities.Detection{}).Where("id = ?", emptyName).Update("clip_name", "").Error)
	changed, err = ds.FillClipSampleRate(t.Context(), emptyName, "", testBatClipRate)
	require.NoError(t, err)
	assert.False(t, changed)
	assert.Nil(t, loadDetection(t, ds, emptyName).ClipSampleRate, "an empty clip name must not match a row")
}

// TestRecordClipSampleRate_EmptyClipNameIsNoOp verifies an empty clip name does not
// match a row whose stored clip_name is the empty string.
func TestRecordClipSampleRate_EmptyClipNameIsNoOp(t *testing.T) {
	ds, cleanup := setupTestDatastore(t)
	defer cleanup()

	id := saveRateNote(t, ds, testClipName, 0)
	require.NoError(t, ds.manager.DB().Model(&entities.Detection{}).Where("id = ?", id).Update("clip_name", "").Error)

	require.NoError(t, ds.RecordClipSampleRate(t.Context(), id, "", testBatClipRate))
	assert.Nil(t, loadDetection(t, ds, id).ClipSampleRate)
}
