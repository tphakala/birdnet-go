//go:build integration

package v2

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
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

// TestMySQL_LabelBatchGetOrCreate_MapsNamesTheServerTreatsAsEqual proves that
// BatchGetOrCreate returns every requested name as a key. The v2 tables use the
// server's default collation, which is case-insensitive, so a request for "X y"
// matches a stored "X Y" row: the map must hold the requested spelling, and no
// second row may be created.
func TestMySQL_LabelBatchGetOrCreate_MapsNamesTheServerTreatsAsEqual(t *testing.T) {
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, true)
	require.NoError(t, mgr.Initialize())
	t.Cleanup(func() { _ = mgr.Close() })

	ctx := t.Context()
	db := mgr.DB()

	labelType := entities.LabelType{Name: "collation_species"}
	require.NoError(t, db.Create(&labelType).Error)
	model := entities.AIModel{Name: "Collation", Version: "1", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)
	stored := entities.Label{ScientificName: "Homo Sapiens", ModelID: model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(&stored).Error)

	labelRepo := repository.NewLabelRepository(db, nil, true, true)

	got, err := labelRepo.BatchGetOrCreate(ctx, []string{"Homo sapiens", "Homo Sapiens", "Homo sapiéns", "Turdus merula"}, model.ID, labelType.ID, nil)
	require.NoError(t, err)
	require.Contains(t, got, "Turdus merula")
	// MySQL 8.4's default collation also ignores accents, and MariaDB's do too.
	for _, name := range []string{"Homo sapiens", "Homo Sapiens", "Homo sapiéns"} {
		require.Contains(t, got, name, "the requested spelling must be a key")
		assert.Equal(t, stored.ID, got[name].ID, name)
	}

	var count int64
	require.NoError(t, db.Table("v2_labels").Where("model_id = ?", model.ID).Count(&count).Error)
	assert.Equal(t, int64(2), count, "the case variant must not create a second row")

	// Two spellings of a new name in one call: one row is created, both resolve after the re-fetch.
	got, err = labelRepo.BatchGetOrCreate(ctx, []string{"Picus Canus", "Picus canus"}, model.ID, labelType.ID, nil)
	require.NoError(t, err)
	require.Contains(t, got, "Picus Canus")
	require.Contains(t, got, "Picus canus")
	assert.Equal(t, got["Picus Canus"].ID, got["Picus canus"].ID)

	// A name the server already matches to a stored row is resolved without an
	// insert, so a repeat save does not run a doomed INSERT each time.
	var inserts atomic.Int32
	const countCallback = "test:count_inserts"
	require.NoError(t, db.Callback().Create().Before("gorm:create").Register(countCallback, func(*gorm.DB) { inserts.Add(1) }))
	t.Cleanup(func() { _ = db.Callback().Create().Remove(countCallback) })
	got, err = labelRepo.BatchGetOrCreate(ctx, []string{"homo SAPIENS"}, model.ID, labelType.ID, nil)
	require.NoError(t, err)
	assert.Equal(t, stored.ID, got["homo SAPIENS"].ID)
	assert.Zero(t, inserts.Load(), "a variant of a stored name must not be inserted")

	// A failure of the per-name lookup is returned wrapped, from the pass after the
	// first fetch and from the pass after the re-fetch.
	const failCallback = "test:fail_single_name_lookup"
	errLookup := errors.NewStd("injected lookup failure")
	var lookups, failAt atomic.Int32
	require.NoError(t, db.Callback().Query().After("gorm:query").Register(failCallback, func(tx *gorm.DB) {
		if strings.Contains(tx.Statement.SQL.String(), "scientific_name = ") && lookups.Add(1) == failAt.Load() {
			_ = tx.AddError(errLookup)
		}
	}))
	t.Cleanup(func() { _ = db.Callback().Query().Remove(failCallback) })
	for _, tc := range []struct {
		name   string
		names  []string
		failAt int32
	}{
		{"first pass", []string{"homo SAPIENS"}, 1},
		// Both spellings are new: the first pass looks up both (lookups 1 and 2),
		// the pass after the re-fetch looks up the one the insert skipped (3).
		{"after the re-fetch", []string{"Quercus Robur", "Quercus robur"}, 3},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lookups.Store(0)
			failAt.Store(tc.failAt)
			_, err := labelRepo.BatchGetOrCreate(ctx, tc.names, model.ID, labelType.ID, nil)
			require.ErrorIs(t, err, errLookup)
			assert.ErrorContains(t, err, "resolve label")
		})
	}
}
