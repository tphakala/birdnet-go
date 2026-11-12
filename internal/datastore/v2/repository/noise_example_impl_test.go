package repository

import (
	"encoding/binary"
	"math"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

const testSpace = "test/space"

// noiseTestEnv is a real SQLite database with detections, reviews and noise
// examples, foreign keys enforced as in the application.
type noiseTestEnv struct {
	db         *gorm.DB
	examples   NoiseExampleRepository
	detections DetectionRepository
	label      uint
}

func newNoiseTestEnv(t *testing.T) *noiseTestEnv {
	t.Helper()
	db := setupInsightsTestDB(t)
	require.NoError(t, db.AutoMigrate(
		&entities.DetectionPrediction{}, &entities.DetectionComment{}, &entities.DetectionLock{}, &entities.NoiseExample{},
	))
	return &noiseTestEnv{
		db:         db,
		examples:   NewNoiseExampleRepository(db, nil, false),
		detections: NewDetectionRepository(db, nil, false, false),
		label:      seedLabel(t, db, "Eptesicus nilssonii"),
	}
}

func (e *noiseTestEnv) detection(t *testing.T, name string) uint {
	t.Helper()
	return seedClipDetection(t, e.detections, e.label, name)
}

// encodeVector packs values the way the service stores them.
func encodeVector(values ...float32) []byte {
	out := make([]byte, 4*len(values))
	for i, v := range values {
		binary.LittleEndian.PutUint32(out[4*i:], math.Float32bits(v))
	}
	return out
}

func exampleRow(space string, offsetMs int, values ...float32) entities.NoiseExample {
	return entities.NoiseExample{
		WindowOffsetMs: offsetMs,
		EmbeddingSpace: space,
		Dim:            len(values),
		Score:          0.9,
		Vector:         encodeVector(values...),
		CreatedAt:      1000,
	}
}

func attachedCount(t *testing.T, e *noiseTestEnv, id uint) int64 {
	t.Helper()
	bySpace, err := e.examples.CountForDetection(t.Context(), id)
	require.NoError(t, err)
	var total int64
	for _, n := range bySpace {
		total += n
	}
	return total
}

func TestNoiseExampleReplaceForDetection(t *testing.T) {
	e := newNoiseTestEnv(t)
	ctx := t.Context()
	id := e.detection(t, "a.flac")
	other := e.detection(t, "b.flac")

	require.NoError(t, e.examples.InsertDetached(ctx, []entities.NoiseExample{exampleRow(testSpace, 0, 9, 9)}))
	require.NoError(t, e.examples.ReplaceForDetection(ctx, other, []entities.NoiseExample{exampleRow(testSpace, 0, 5, 5)}))

	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{
		exampleRow(testSpace, 0, 1, 0), exampleRow(testSpace, 500, 0, 1),
	}))
	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{
		exampleRow(testSpace, 1000, 1, 1),
	}))

	assert.EqualValues(t, 1, attachedCount(t, e, id), "a second replace leaves one set")
	assert.EqualValues(t, 1, attachedCount(t, e, other), "another detection's rows are untouched")
	all, err := e.examples.ListAll(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 3, "the detached row is untouched")
}

func TestNoiseExampleVectorRoundTrip(t *testing.T) {
	e := newNoiseTestEnv(t)
	id := e.detection(t, "a.flac")
	want := []float32{0.25, -1.5, 3, float32(math.SmallestNonzeroFloat32)}
	require.NoError(t, e.examples.ReplaceForDetection(t.Context(), id, []entities.NoiseExample{exampleRow(testSpace, 0, want...)}))

	rows, err := e.examples.ListAll(t.Context())
	require.NoError(t, err)
	require.Len(t, rows, 1)
	assert.Equal(t, encodeVector(want...), rows[0].Vector)
	assert.Equal(t, len(want), rows[0].Dim)
	assert.Equal(t, testSpace, rows[0].EmbeddingSpace)
	require.NotNil(t, rows[0].DetectionID)
	assert.Equal(t, id, *rows[0].DetectionID)
}

// TestNoiseExamplesSurviveDetectionDelete pins the lifetime rule: the foreign key
// clears the link and keeps the row.
func TestNoiseExamplesSurviveDetectionDelete(t *testing.T) {
	e := newNoiseTestEnv(t)
	ctx := t.Context()
	id := e.detection(t, "a.flac")
	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{
		exampleRow(testSpace, 0, 1, 0), exampleRow(testSpace, 500, 0, 1),
	}))

	require.NoError(t, e.detections.Delete(ctx, id))

	rows, err := e.examples.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2)
	for _, row := range rows {
		assert.Nil(t, row.DetectionID)
		assert.Len(t, row.Vector, 8)
	}
}

// TestNoiseExampleIDReuse covers a detection that gets the ID of a deleted one:
// it has no attached rows, its own rows coexist with the older detached rows of
// equal offsets, and leaving noise on it removes only its own.
func TestNoiseExampleIDReuse(t *testing.T) {
	e := newNoiseTestEnv(t)
	ctx := t.Context()
	id := e.detection(t, "a.flac")
	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{exampleRow(testSpace, 0, 1, 0)}))
	require.NoError(t, e.detections.Delete(ctx, id))

	reused := seedClipDetection(t, e.detections, e.label, "c.flac")
	require.NoError(t, e.detections.Delete(ctx, reused))
	clip := "d.flac"
	require.NoError(t, e.detections.SaveWithID(ctx, &entities.Detection{
		ID: id, LabelID: e.label, ModelID: 1, DetectedAt: 2000, Confidence: 0.8, ClipName: &clip,
	}))

	assert.EqualValues(t, 0, attachedCount(t, e, id), "a reused ID inherits nothing")
	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{exampleRow(testSpace, 0, 0, 1)}))
	assert.EqualValues(t, 1, attachedCount(t, e, id))

	removed, err := e.examples.RemoveForVerdict(ctx, id, entities.VerificationCorrect)
	require.NoError(t, err)
	assert.EqualValues(t, 1, removed)
	rows, err := e.examples.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 1, "the older detached row stays")
	assert.Nil(t, rows[0].DetectionID)
}

func TestNoiseExampleDetachOrphans(t *testing.T) {
	e := newNoiseTestEnv(t)
	ctx := t.Context()
	id := e.detection(t, "a.flac")
	keep := e.detection(t, "b.flac")
	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{exampleRow(testSpace, 0, 1, 0)}))
	require.NoError(t, e.examples.ReplaceForDetection(ctx, keep, []entities.NoiseExample{exampleRow(testSpace, 0, 0, 1)}))

	// A delete made while foreign keys are not enforced leaves the link dangling.
	require.NoError(t, e.db.Exec("PRAGMA foreign_keys=OFF").Error)
	require.NoError(t, e.db.Exec("DELETE FROM detections WHERE id = ?", id).Error)
	require.NoError(t, e.db.Exec("PRAGMA foreign_keys=ON").Error)

	n, err := e.examples.DetachOrphans(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 1, n)
	n, err = e.examples.DetachOrphans(ctx)
	require.NoError(t, err)
	assert.EqualValues(t, 0, n, "the second run changes nothing")

	rows, err := e.examples.ListAll(ctx)
	require.NoError(t, err)
	require.Len(t, rows, 2, "no row is removed")
	assert.EqualValues(t, 1, attachedCount(t, e, keep))
}

func TestNoiseExampleCounts(t *testing.T) {
	e := newNoiseTestEnv(t)
	ctx := t.Context()
	id := e.detection(t, "a.flac")
	require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{
		exampleRow(testSpace, 0, 1, 0), exampleRow(testSpace, 500, 0, 1), exampleRow("other/space", 1000, 1),
	}))
	require.NoError(t, e.examples.InsertDetached(ctx, []entities.NoiseExample{exampleRow(testSpace, 0, 1, 1)}))

	bySpace, err := e.examples.CountForDetection(ctx, id)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testSpace: 2, "other/space": 1}, bySpace)

	all, err := e.examples.CountBySpace(ctx)
	require.NoError(t, err)
	assert.Equal(t, map[string]int64{testSpace: 3, "other/space": 1}, all)
}

// TestRemoveForVerdict covers the one transaction behind leaving noise.
func TestRemoveForVerdict(t *testing.T) {
	for _, verdict := range []entities.VerificationStatus{entities.VerificationCorrect, entities.VerificationFalsePositive} {
		t.Run(string(verdict), func(t *testing.T) {
			e := newNoiseTestEnv(t)
			ctx := t.Context()
			id := markedReview(t, e.detections, e.label)
			other := markedReview(t, e.detections, e.label)
			require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{exampleRow(testSpace, 0, 1, 0)}))
			require.NoError(t, e.examples.ReplaceForDetection(ctx, other, []entities.NoiseExample{exampleRow(testSpace, 0, 0, 1)}))

			removed, err := e.examples.RemoveForVerdict(ctx, id, verdict)
			require.NoError(t, err)
			assert.EqualValues(t, 1, removed)

			assert.EqualValues(t, 0, attachedCount(t, e, id))
			assert.EqualValues(t, 1, attachedCount(t, e, other))
			review, err := e.detections.GetReview(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, verdict, review.Verified)
			assert.Nil(t, review.Reason)
			require.NotNil(t, storedReason(t, e.detections, other))
		})
	}

	t.Run("a detection that was never marked and has no review gets one", func(t *testing.T) {
		e := newNoiseTestEnv(t)
		id := e.detection(t, "a.flac")
		removed, err := e.examples.RemoveForVerdict(t.Context(), id, entities.VerificationCorrect)
		require.NoError(t, err)
		assert.Zero(t, removed)
		review, err := e.detections.GetReview(t.Context(), id)
		require.NoError(t, err)
		assert.Equal(t, entities.VerificationCorrect, review.Verified)
	})

	t.Run("a failing review write rolls the delete back", func(t *testing.T) {
		e := newNoiseTestEnv(t)
		ctx := t.Context()
		id := markedReview(t, e.detections, e.label)
		require.NoError(t, e.examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{exampleRow(testSpace, 0, 1, 0)}))
		require.NoError(t, e.db.Exec(
			"CREATE TRIGGER fail_review BEFORE INSERT ON detection_reviews BEGIN SELECT RAISE(ABORT, 'review write failed'); END",
		).Error)

		_, err := e.examples.RemoveForVerdict(ctx, id, entities.VerificationCorrect)
		require.Error(t, err)

		assert.EqualValues(t, 1, attachedCount(t, e, id), "the examples are still there")
		review, getErr := e.detections.GetReview(ctx, id)
		require.NoError(t, getErr)
		assert.Equal(t, entities.VerificationFalsePositive, review.Verified)
		require.NotNil(t, review.Reason)
	})
}
