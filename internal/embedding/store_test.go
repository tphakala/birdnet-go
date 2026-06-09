package embedding

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStorePutGetRoundTrip verifies that a stored record can be read back with
// every field intact, including the decoded vector.
func TestStorePutGetRoundTrip(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	rec := makeRecord(t)

	require.NoError(t, s.Put(t.Context(), rec))

	got, err := s.Get(t.Context(), rec.DetectionID)
	require.NoError(t, err)

	assert.Equal(t, rec.DetectionID, got.DetectionID)
	assert.Equal(t, rec.Model, got.Model)
	assert.Equal(t, rec.Source, got.Source)
	assert.True(t, rec.CapturedAt.Equal(got.CapturedAt), "captured_at must round-trip")
	assert.Equal(t, rec.Format, got.Format)
	assert.Equal(t, rec.Dim, got.Dim)
	assert.Equal(t, rec.Version, got.Version)
	assert.Equal(t, rec.Vector, got.Vector, "exactly representable vector must round-trip")
}

// TestStoreGetNotFound verifies that querying an unknown detection id returns a
// typed not-found error rather than a zero record with no error.
func TestStoreGetNotFound(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)

	_, err := s.Get(t.Context(), "missing")
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrNotFound)
}

// TestStorePutUpsert verifies that storing twice under the same detection id
// keeps a single row and the latest value wins. A re-emitted detection must
// not double-store.
func TestStorePutUpsert(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	rec := makeRecord(t)
	require.NoError(t, s.Put(t.Context(), rec))

	updated := makeRecord(t, func(r *Record) {
		r.Vector = []float32{1, 1, 1, 1}
		r.Source = "rtsp://other"
	})
	require.NoError(t, s.Put(t.Context(), updated))

	got, err := s.Get(t.Context(), rec.DetectionID)
	require.NoError(t, err)
	assert.Equal(t, updated.Vector, got.Vector, "latest value must win")
	assert.Equal(t, "rtsp://other", got.Source)

	all, err := s.Query(t.Context(), rec.Model, time.Time{}, time.Now().Add(time.Hour))
	require.NoError(t, err)
	assert.Len(t, all, 1, "upsert must not create a second row")
}

// TestStorePutValidatesDimension verifies that a record whose declared
// dimension disagrees with its vector length is rejected.
func TestStorePutValidatesDimension(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	rec := makeRecord(t, func(r *Record) { r.Dim = 99 })

	err := s.Put(t.Context(), rec)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRecord)
}

// TestStorePutValidatesDetectionID verifies that a record with an empty
// detection id is rejected; an empty key must never be stored.
func TestStorePutValidatesDetectionID(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	rec := makeRecord(t, func(r *Record) { r.DetectionID = "" })

	err := s.Put(t.Context(), rec)
	require.Error(t, err)
	assert.ErrorIs(t, err, ErrInvalidRecord)
}

// TestStoreQueryFiltersByModelAndTime verifies that Query returns only rows for
// the requested model within the requested time window, ordered by capture
// time.
func TestStoreQueryFiltersByModelAndTime(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	// Two birdnet rows an hour apart, plus one perch row in between.
	require.NoError(t, s.Put(t.Context(), makeRecord(t, func(r *Record) {
		r.DetectionID, r.Model, r.CapturedAt = "b1", "birdnet_v3", base
	})))
	require.NoError(t, s.Put(t.Context(), makeRecord(t, func(r *Record) {
		r.DetectionID, r.Model, r.CapturedAt = "p1", "perch_v2", base.Add(30*time.Minute)
	})))
	require.NoError(t, s.Put(t.Context(), makeRecord(t, func(r *Record) {
		r.DetectionID, r.Model, r.CapturedAt = "b2", "birdnet_v3", base.Add(time.Hour)
	})))

	got, err := s.Query(t.Context(), "birdnet_v3", base, base.Add(90*time.Minute))
	require.NoError(t, err)
	require.Len(t, got, 2, "only birdnet rows in window")
	assert.Equal(t, "b1", got[0].DetectionID, "results ordered by capture time")
	assert.Equal(t, "b2", got[1].DetectionID)
}

// TestStoreQuerySkipsCorruptRow verifies that a single undecodable row does not
// abort the whole query; the remaining rows are still returned. The corrupt row
// is inserted directly, bypassing Put's validation, to simulate on-disk
// corruption.
func TestStoreQuerySkipsCorruptRow(t *testing.T) {
	t.Parallel()

	s := newTestStore(t)
	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	require.NoError(t, s.Put(t.Context(), makeRecord(t, func(r *Record) {
		r.DetectionID, r.CapturedAt = "good", base
	})))

	// Blob length (2 bytes) is inconsistent with the declared dim (4).
	require.NoError(t, s.db.Create(&embeddingRow{
		DetectionID: "corrupt",
		Model:       "birdnet_v3",
		Source:      "rtsp://camera",
		CapturedAt:  base.Add(time.Minute),
		Format:      string(FormatFP16),
		Dim:         4,
		Version:     "3.0",
		Vector:      []byte{0x00, 0x00},
	}).Error)

	got, err := s.Query(t.Context(), "birdnet_v3", base, base.Add(time.Hour))
	require.NoError(t, err)
	require.Len(t, got, 1, "corrupt row skipped, good row returned")
	assert.Equal(t, "good", got[0].DetectionID)
}

// TestStorePruneEnforcesRowCap verifies that Prune deletes the oldest rows so
// at most maxRows remain, and reports how many it removed.
func TestStorePruneEnforcesRowCap(t *testing.T) {
	t.Parallel()

	s := newTestStore(t, WithMaxRows(3))
	base := time.Date(2026, 6, 9, 12, 0, 0, 0, time.UTC)

	for i := range 5 {
		require.NoError(t, s.Put(t.Context(), makeRecord(t, func(r *Record) {
			r.DetectionID = "det-" + string(rune('a'+i))
			r.CapturedAt = base.Add(time.Duration(i) * time.Minute)
		})))
	}

	deleted, err := s.Prune(t.Context())
	require.NoError(t, err)
	assert.Equal(t, 2, deleted, "two oldest rows pruned")

	// The two oldest (det-a, det-b) must be gone; the three newest remain.
	for _, id := range []string{"det-a", "det-b"} {
		_, err := s.Get(t.Context(), id)
		require.ErrorIs(t, err, ErrNotFound, "oldest row pruned: %s", id)
	}
	for _, id := range []string{"det-c", "det-d", "det-e"} {
		_, err := s.Get(t.Context(), id)
		assert.NoError(t, err, "newest rows retained: %s", id)
	}
}
