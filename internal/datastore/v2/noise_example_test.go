package v2

import (
	"bytes"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

func TestSQLite_NoiseExampleRepositories(t *testing.T) {
	mgr, err := NewSQLiteManager(Config{DataDir: t.TempDir()})
	require.NoError(t, err)
	t.Cleanup(func() { _ = mgr.Close() })
	require.NoError(t, mgr.Initialize())

	exerciseNoiseExampleRepositories(t, mgr.DB(), false, false)
}

// exerciseNoiseExampleRepositories runs the statements of the noise example
// repository that could differ by database server on a real schema: the review
// reason expression, a BLOB round trip, the foreign key that keeps examples when
// their detection is deleted, and two detached rows with the same offset under
// the unique index. SQLite and the MySQL and MariaDB suites share it.
func exerciseNoiseExampleRepositories(t *testing.T, db *gorm.DB, useV2Prefix, isMySQL bool) {
	t.Helper()
	ctx := t.Context()
	detections := repository.NewDetectionRepository(db, nil, useV2Prefix, isMySQL)
	examples := repository.NewNoiseExampleRepository(db, nil, useV2Prefix)

	labelType := entities.LabelType{Name: "noise_example_species"}
	require.NoError(t, db.Create(&labelType).Error)
	model := entities.AIModel{Name: "NoiseExample", Version: "1", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)
	label := entities.Label{ScientificName: "Eptesicus nilssonii", ModelID: model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(&label).Error)

	newDetection := func() uint {
		clip := "noise.flac"
		det := &entities.Detection{LabelID: label.ID, ModelID: model.ID, DetectedAt: 1000, Confidence: 0.9, ClipName: &clip}
		require.NoError(t, detections.Save(ctx, det))
		return det.ID
	}
	row := func(offsetMs int, vector []byte) entities.NoiseExample {
		return entities.NoiseExample{
			WindowOffsetMs: offsetMs, EmbeddingSpace: "test/space", Dim: len(vector) / 4,
			Score: 0.75, Vector: vector, CreatedAt: 1000,
		}
	}
	reasonOf := func(id uint) *string {
		review, err := detections.GetReview(ctx, id)
		require.NoError(t, err)
		return review.Reason
	}
	noAnimal := entities.ReviewReasonNoAnimal
	marked := func() uint {
		id := newDetection()
		require.NoError(t, detections.SaveReview(ctx, &entities.DetectionReview{
			DetectionID: id, Verified: entities.VerificationFalsePositive, Reason: &noAnimal,
		}))
		return id
	}

	t.Run("review reason follows the status", func(t *testing.T) {
		id := marked()
		require.NoError(t, detections.SaveReview(ctx, &entities.DetectionReview{DetectionID: id, Verified: entities.VerificationFalsePositive}))
		require.NotNil(t, reasonOf(id), "a save that keeps false_positive keeps the reason")
		require.NoError(t, detections.SaveReview(ctx, &entities.DetectionReview{DetectionID: id, Verified: entities.VerificationCorrect}))
		assert.Nil(t, reasonOf(id), "a save to correct clears it")

		id = marked()
		require.NoError(t, detections.UpdateReview(ctx, id, entities.VerificationFalsePositive))
		require.NotNil(t, reasonOf(id))
		require.NoError(t, detections.UpdateReview(ctx, id, entities.VerificationCorrect))
		assert.Nil(t, reasonOf(id))

		err := detections.SaveReview(ctx, &entities.DetectionReview{
			DetectionID: newDetection(), Verified: entities.VerificationCorrect, Reason: &noAnimal,
		})
		require.ErrorIs(t, err, repository.ErrReviewReasonNeedsFalsePositive)

		for _, verdict := range []entities.VerificationStatus{entities.VerificationCorrect, entities.VerificationFalsePositive} {
			id = marked()
			require.NoError(t, examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{row(0, make([]byte, 8))}))
			removed, err := examples.RemoveForVerdict(ctx, id, verdict)
			require.NoError(t, err)
			assert.EqualValues(t, 1, removed)
			assert.Nil(t, reasonOf(id))
			review, err := detections.GetReview(ctx, id)
			require.NoError(t, err)
			assert.Equal(t, verdict, review.Verified)
		}
	})

	t.Run("vector bytes round trip", func(t *testing.T) {
		vector := make([]byte, 4*1024)
		for i := range vector {
			vector[i] = byte(i % 256)
		}
		id := newDetection()
		require.NoError(t, examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{row(0, vector)}))
		all, err := examples.ListAll(ctx)
		require.NoError(t, err)
		var found bool
		for i := range all {
			if all[i].DetectionID != nil && *all[i].DetectionID == id {
				found = true
				assert.True(t, bytes.Equal(vector, all[i].Vector), "the BLOB must come back byte for byte")
				assert.Equal(t, 1024, all[i].Dim)
			}
		}
		assert.True(t, found)
	})

	t.Run("deleting the detection keeps its examples with a NULL link", func(t *testing.T) {
		id := newDetection()
		require.NoError(t, examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{row(0, make([]byte, 8)), row(500, make([]byte, 8))}))
		before, err := examples.CountBySpace(ctx)
		require.NoError(t, err)

		require.NoError(t, detections.Delete(ctx, id))

		after, err := examples.CountBySpace(ctx)
		require.NoError(t, err)
		assert.Equal(t, before, after, "no example is removed by the delete")
		attached, err := examples.CountForDetection(ctx, id)
		require.NoError(t, err)
		assert.Empty(t, attached, "the link is cleared")
	})

	t.Run("detached rows with equal offsets coexist", func(t *testing.T) {
		require.NoError(t, examples.InsertDetached(ctx, []entities.NoiseExample{row(0, make([]byte, 8))}))
		require.NoError(t, examples.InsertDetached(ctx, []entities.NoiseExample{row(0, make([]byte, 8))}))
		id := newDetection()
		require.NoError(t, examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{row(0, make([]byte, 8))}))
		require.NoError(t, examples.ReplaceForDetection(ctx, id, []entities.NoiseExample{row(0, make([]byte, 8))}), "relearning a detection is idempotent")
	})
}
