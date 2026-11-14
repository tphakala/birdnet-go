//go:build integration

package v2

import (
	"fmt"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"gorm.io/gorm"
)

const (
	speciesChangeTealName    = "Anas crecca"
	speciesChangeMallardName = "Anas platyrhynchos"
	speciesChangeConfidence  = 0.8
	speciesChangeAlternate   = 0.3
	speciesChangeDetectedAt  = int64(1000)
	speciesChangeInjectName  = "test:inject_before_detection_update"
)

// speciesChangeFixture is a MySQL database with one detection of teal that has
// a prediction row for teal (the legacy shape that broke a marker stored in
// the predictions table) and a review of false_positive.
type speciesChangeFixture struct {
	mgr     *MySQLManager
	repo    repository.DetectionRepository
	teal    *entities.Label
	mallard *entities.Label
	det     *entities.Detection
}

// newSpeciesChangeFixture initializes the schema under the given table layout and
// seeds the detection.
func newSpeciesChangeFixture(t *testing.T, useV2Prefix bool) *speciesChangeFixture {
	t.Helper()
	dropAllTables(t, coexTestDB, coexTestDBName)

	mgr := newTestMySQLManager(t, useV2Prefix)
	t.Cleanup(func() {
		_ = mgr.Delete()
		_ = mgr.Close()
	})
	require.NoError(t, mgr.Initialize())
	db := mgr.DB()

	labelType := entities.LabelType{Name: "species_change_test"}
	require.NoError(t, db.Create(&labelType).Error)
	model := entities.AIModel{Name: "SpeciesChange", Version: "1", ModelType: entities.ModelTypeBird}
	require.NoError(t, db.Create(&model).Error)
	teal := &entities.Label{ScientificName: speciesChangeTealName, ModelID: model.ID, LabelTypeID: labelType.ID}
	mallard := &entities.Label{ScientificName: speciesChangeMallardName, ModelID: model.ID, LabelTypeID: labelType.ID}
	require.NoError(t, db.Create(teal).Error)
	require.NoError(t, db.Create(mallard).Error)

	det := &entities.Detection{
		LabelID: teal.ID, ModelID: model.ID, Confidence: speciesChangeConfidence, DetectedAt: speciesChangeDetectedAt,
	}
	require.NoError(t, db.Create(det).Error)
	require.NoError(t, db.Create(&entities.DetectionPrediction{
		DetectionID: det.ID, LabelID: teal.ID, Confidence: speciesChangeAlternate, Rank: 1,
	}).Error)

	repo := repository.NewDetectionRepository(db, nil, useV2Prefix, true)
	require.NoError(t, repo.SaveReview(t.Context(), &entities.DetectionReview{
		DetectionID: det.ID, Verified: entities.VerificationFalsePositive,
	}))
	return &speciesChangeFixture{mgr: mgr, repo: repo, teal: teal, mallard: mallard, det: det}
}

// injectBeforeDetectionUpdate registers a callback that runs inject once, just
// before the first UPDATE of the detections table. The inject function gets the
// manager's own handle, which takes another connection from the pool, so what it
// writes commits independently of the species change's transaction.
func (f *speciesChangeFixture) injectBeforeDetectionUpdate(t *testing.T, inject func(db *gorm.DB)) {
	t.Helper()
	db := f.mgr.DB()
	var fired atomic.Bool
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(speciesChangeInjectName, func(tx *gorm.DB) {
		if !strings.HasSuffix(tx.Statement.Table, "detections") || !fired.CompareAndSwap(false, true) {
			return
		}
		inject(db)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(speciesChangeInjectName)) })
}

// TestMySQL_ChangeSpecies runs the species change through the production MySQL
// DSN under both table layouts. It pins that the original_label_id column exists
// after Initialize, that the COALESCE assignment records the old label and keeps the
// first original (MySQL evaluates SET left to right, so an UPDATE that assigns
// label_id first would store the new label as the original), that the review
// upsert works inside the transaction, and that a locked or a missing detection is
// refused.
func TestMySQL_ChangeSpecies(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			f := newSpeciesChangeFixture(t, useV2Prefix)
			ctx := t.Context()

			// A to B to A keeps A as the original, with a prediction row for A present.
			for _, step := range []struct {
				species string
				label   *entities.Label
			}{
				{speciesChangeMallardName, f.mallard},
				{speciesChangeTealName, f.teal},
				{speciesChangeMallardName, f.mallard},
			} {
				species := step.species
				require.NoError(t, f.repo.ChangeSpecies(ctx, f.det.ID, species))
				got, err := f.repo.Get(ctx, f.det.ID)
				require.NoError(t, err)
				assert.Equal(t, step.label.ID, got.LabelID, "the label is the requested species %s", species)
				require.NotNil(t, got.OriginalLabelID)
				assert.Equal(t, f.teal.ID, *got.OriginalLabelID, "the original stays the model's pick after a change to %s", species)
			}
			got, err := f.repo.Get(ctx, f.det.ID)
			require.NoError(t, err)
			assert.Equal(t, f.mallard.ID, got.LabelID)
			review, err := f.repo.GetReview(ctx, f.det.ID)
			require.NoError(t, err)
			assert.Equal(t, entities.VerificationCorrect, review.Verified)

			// A detection without a review gets one: the insert branch of the upsert.
			unreviewed := &entities.Detection{
				LabelID: f.teal.ID, ModelID: f.det.ModelID, Confidence: speciesChangeConfidence, DetectedAt: speciesChangeDetectedAt,
			}
			require.NoError(t, f.mgr.DB().Create(unreviewed).Error)
			require.NoError(t, f.repo.ChangeSpecies(ctx, unreviewed.ID, speciesChangeMallardName))
			review, err = f.repo.GetReview(ctx, unreviewed.ID)
			require.NoError(t, err)
			assert.Equal(t, entities.VerificationCorrect, review.Verified)

			// A locked detection is refused and left as it is.
			locked := &entities.Detection{
				LabelID: f.teal.ID, ModelID: f.det.ModelID, Confidence: speciesChangeConfidence, DetectedAt: speciesChangeDetectedAt,
			}
			require.NoError(t, f.mgr.DB().Create(locked).Error)
			require.NoError(t, f.repo.Lock(ctx, locked.ID))
			require.ErrorIs(t, f.repo.ChangeSpecies(ctx, locked.ID, speciesChangeMallardName), repository.ErrDetectionLocked)
			got, err = f.repo.Get(ctx, locked.ID)
			require.NoError(t, err)
			assert.Equal(t, f.teal.ID, got.LabelID)
			assert.Nil(t, got.OriginalLabelID)

			const missingID = uint(999999)
			require.ErrorIs(t, f.repo.ChangeSpecies(ctx, missingID, speciesChangeMallardName), repository.ErrDetectionNotFound)
		})
	}
}

// TestMySQL_ChangeSpecies_LockOrDeleteFromAnotherConnection commits a lock, or a
// delete, from a second connection just before the guarded UPDATE of a species
// change. The change must report the lock or the missing detection and write
// nothing. This measures that the UPDATE, the first statement of its transaction,
// sees what the reads before the transaction could not.
func TestMySQL_ChangeSpecies_LockOrDeleteFromAnotherConnection(t *testing.T) {
	for _, useV2Prefix := range []bool{true, false} {
		t.Run(fmt.Sprintf("useV2Prefix=%t", useV2Prefix), func(t *testing.T) {
			t.Run("a lock", func(t *testing.T) {
				f := newSpeciesChangeFixture(t, useV2Prefix)
				ctx := t.Context()
				f.injectBeforeDetectionUpdate(t, func(db *gorm.DB) {
					require.NoError(t, db.Create(&entities.DetectionLock{DetectionID: f.det.ID}).Error)
				})

				require.ErrorIs(t, f.repo.ChangeSpecies(ctx, f.det.ID, speciesChangeMallardName), repository.ErrDetectionLocked)

				got, err := f.repo.Get(ctx, f.det.ID)
				require.NoError(t, err)
				assert.Equal(t, f.teal.ID, got.LabelID)
				assert.Nil(t, got.OriginalLabelID)
				review, err := f.repo.GetReview(ctx, f.det.ID)
				require.NoError(t, err)
				assert.Equal(t, entities.VerificationFalsePositive, review.Verified)
			})

			t.Run("a delete", func(t *testing.T) {
				f := newSpeciesChangeFixture(t, useV2Prefix)
				f.injectBeforeDetectionUpdate(t, func(db *gorm.DB) {
					require.NoError(t, db.Delete(&entities.Detection{}, f.det.ID).Error)
				})

				require.ErrorIs(t, f.repo.ChangeSpecies(t.Context(), f.det.ID, speciesChangeMallardName), repository.ErrDetectionNotFound)
			})
		})
	}
}
