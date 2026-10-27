//go:build integration

package v2

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
)

// TestMySQL_BatchInserts_SkipRowsDuplicatedInOneStatement runs every
// multi-row "skip duplicates" insert through the production MySQL DSN with a
// single statement that holds two rows sharing a unique key. MySQL 8.4 rejects
// such a statement (Error 1869) when the skip is written as an assignment of
// the auto-increment column, which loses the whole batch. Comments have no
// unique key, so both rows must be kept.
func TestMySQL_BatchInserts_SkipRowsDuplicatedInOneStatement(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, true)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := t.Context()
	db := mgr.DB()

	labelType := entities.LabelType{Name: "batch_insert_species"}
	require.NoError(t, db.Create(&labelType).Error)
	model := entities.AIModel{Name: "BatchInsert", Version: "1", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)
	labelA := entities.Label{ScientificName: "Turdus merula", ModelID: model.ID, LabelTypeID: labelType.ID}
	labelB := entities.Label{ScientificName: "Parus major", ModelID: model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(&labelA).Error)
	require.NoError(t, db.Create(&labelB).Error)

	const (
		predID    = uint(1)
		reviewID  = uint(2)
		lockID    = uint(3)
		commentID = uint(4)
	)
	for _, id := range []uint{predID, reviewID, lockID, commentID} {
		seedDetection(t, id, nil)
	}

	detRepo := repository.NewDetectionRepository(db, nil, true, true)
	labelRepo := repository.NewLabelRepository(db, nil, true, true)
	weatherRepo := repository.NewWeatherRepository(db, nil, true, true)

	t.Run("predictions keep the first row", func(t *testing.T) {
		const firstConfidence = 0.9
		err := detRepo.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{
			{DetectionID: predID, LabelID: labelA.ID, Confidence: firstConfidence, Rank: 1},
			{DetectionID: predID, LabelID: labelB.ID, Confidence: 0.8, Rank: 2},
			{DetectionID: predID, LabelID: labelA.ID, Confidence: 0.7, Rank: 3},
		})
		require.NoError(t, err)

		var preds []entities.DetectionPrediction
		require.NoError(t, db.Table("v2_detection_predictions").Where("detection_id = ?", predID).Find(&preds).Error)
		assert.Len(t, preds, 2)
		var kept entities.DetectionPrediction
		require.NoError(t, db.Table("v2_detection_predictions").
			Where("detection_id = ? AND label_id = ?", predID, labelA.ID).First(&kept).Error)
		assert.InDelta(t, firstConfidence, kept.Confidence, 1e-9)

		// A pair that is already stored is skipped and the stored row is untouched.
		require.NoError(t, detRepo.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{
			{DetectionID: predID, LabelID: labelA.ID, Confidence: 0.1, Rank: 4},
		}))
		require.NoError(t, db.Table("v2_detection_predictions").
			Where("detection_id = ? AND label_id = ?", predID, labelA.ID).First(&kept).Error)
		assert.InDelta(t, firstConfidence, kept.Confidence, 1e-9)
	})

	t.Run("a foreign key violation still fails the statement", func(t *testing.T) {
		const missingLabelID = uint(9999)
		err := detRepo.SavePredictionsBatch(ctx, []*entities.DetectionPrediction{
			{DetectionID: predID, LabelID: missingLabelID, Confidence: 0.5, Rank: 5},
		})
		require.Error(t, err)
	})

	t.Run("labels differing only in case", func(t *testing.T) {
		_, err := labelRepo.BatchGetOrCreate(ctx, []string{"Picus Canus", "Picus canus"}, model.ID, labelType.ID, nil)
		require.NoError(t, err)

		var count int64
		require.NoError(t, db.Table("v2_labels").
			Where("scientific_name LIKE ? AND model_id = ?", "Picus%", model.ID).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	})

	t.Run("reviews", func(t *testing.T) {
		err := detRepo.SaveReviewsBatch(ctx, []*entities.DetectionReview{
			{DetectionID: reviewID, Verified: entities.VerificationCorrect},
			{DetectionID: reviewID, Verified: entities.VerificationFalsePositive},
		})
		require.NoError(t, err)

		var kept []entities.DetectionReview
		require.NoError(t, db.Table("v2_detection_reviews").Where("detection_id = ?", reviewID).Find(&kept).Error)
		require.Len(t, kept, 1)
		assert.Equal(t, entities.VerificationCorrect, kept[0].Verified)
	})

	t.Run("locks", func(t *testing.T) {
		err := detRepo.SaveLocksBatch(ctx, []*entities.DetectionLock{
			{DetectionID: lockID},
			{DetectionID: lockID},
		})
		require.NoError(t, err)

		var count int64
		require.NoError(t, db.Table("v2_detection_locks").Where("detection_id = ?", lockID).Count(&count).Error)
		assert.Equal(t, int64(1), count)
	})

	t.Run("daily events", func(t *testing.T) {
		const date = "2024-05-01"
		_, err := weatherRepo.SaveAllDailyEvents(ctx, []entities.DailyEvents{
			{Date: date, Sunrise: 1, Sunset: 2},
			{Date: date, Sunrise: 3, Sunset: 4},
		})
		require.NoError(t, err)

		var kept []entities.DailyEvents
		require.NoError(t, db.Table("v2_daily_events").Where("date = ?", date).Find(&kept).Error)
		require.Len(t, kept, 1)
		assert.Equal(t, int64(1), kept[0].Sunrise)
	})

	t.Run("comments without a unique key are all kept", func(t *testing.T) {
		err := detRepo.SaveCommentsBatch(ctx, []*entities.DetectionComment{
			{DetectionID: commentID, Entry: "first"},
			{DetectionID: commentID, Entry: "second"},
		})
		require.NoError(t, err)

		var count int64
		require.NoError(t, db.Table("v2_detection_comments").Where("detection_id = ?", commentID).Count(&count).Error)
		assert.Equal(t, int64(2), count)
	})
}
