package repository

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
)

const (
	speciesTestBirdnet    = uint(1)
	speciesTestPerch      = uint(2)
	speciesTestThirdModel = uint(3)
	speciesTestTeal       = "Anas crecca"
	speciesTestMallard    = "Anas platyrhynchos"
	speciesTestGadwall    = "Mareca strepera"
	speciesTestDoveOld    = "Streptopelia senegalensis" // legacy alias of speciesTestDoveNew
	speciesTestDoveNew    = "Spilopelia senegalensis"
	speciesTestConfidence = 0.87
	speciesTestAlternate  = 0.31
	speciesTestAt         = int64(1000)
	speciesTestClip       = "clips/teal.flac"
	speciesTestInjectName = "test:inject_before_detection_update"
)

// speciesTestDB opens a database with the tables a species change touches and a
// repository on it.
func speciesTestDB(t *testing.T) (*gorm.DB, DetectionRepository) {
	t.Helper()
	db := setupDetectionTestDBWithLabels(t)
	require.NoError(t, db.AutoMigrate(&entities.DetectionPrediction{}), "failed to migrate predictions table")
	return db, NewDetectionRepository(db, nil, false, false)
}

// speciesTestDetection stores a detection of the label by its model.
func speciesTestDetection(t *testing.T, db *gorm.DB, label *entities.Label) *entities.Detection {
	t.Helper()
	det := &entities.Detection{
		LabelID: label.ID, ModelID: label.ModelID, Confidence: speciesTestConfidence, DetectedAt: speciesTestAt,
		ClipName: new(speciesTestClip), Unlikely: true,
	}
	require.NoError(t, db.Table(tableDetections).Create(det).Error)
	return det
}

// speciesTestLabelCount returns the number of label rows.
func speciesTestLabelCount(t *testing.T, db *gorm.DB) int64 {
	t.Helper()
	var n int64
	require.NoError(t, db.Table(tableLabels).Count(&n).Error)
	return n
}

// TestDetectionRepository_ChangeSpecies_KeepsOriginalAcrossChanges follows one
// detection through A to B, B to C, C to A, A to B and the last request again.
// After every step only label_id and original_label_id differ from the stored row,
// the original stays the first pick, and the detection is reviewed as correct.
func TestDetectionRepository_ChangeSpecies_KeepsOriginalAcrossChanges(t *testing.T) {
	db, repo := speciesTestDB(t)
	ctx := t.Context()

	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	mallard := createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
	gadwall := createTestLabel(t, db, speciesTestGadwall, speciesTestBirdnet)
	det := speciesTestDetection(t, db, teal)
	require.NoError(t, repo.SaveReview(ctx, &entities.DetectionReview{
		DetectionID: det.ID, Verified: entities.VerificationFalsePositive,
	}))

	steps := []struct {
		name    string
		species string
		want    *entities.Label
	}{
		{"A to B", speciesTestMallard, mallard},
		{"B to C", speciesTestGadwall, gadwall},
		{"C to A", speciesTestTeal, teal},
		{"A to B again", speciesTestMallard, mallard},
		{"the same request twice", speciesTestMallard, mallard},
	}
	for _, step := range steps {
		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, step.species), step.name)

		want := *det
		want.LabelID = step.want.ID
		want.OriginalLabelID = new(teal.ID)
		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, want, *got, "%s: only label_id and original_label_id change", step.name)

		review, err := repo.GetReview(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, entities.VerificationCorrect, review.Verified, step.name)
	}
}

// TestDetectionRepository_ChangeSpecies_IndependentOfPredictionRows pins that the
// outcome does not depend on the detection's prediction rows and that they are
// never written: the legacy case holds the old label as a prediction row, which
// would collide with a marker stored in that table.
func TestDetectionRepository_ChangeSpecies_IndependentOfPredictionRows(t *testing.T) {
	tests := []struct {
		name string
		// rows returns the prediction rows to store for a detection of teal that is
		// changed to mallard.
		rows func(detectionID uint, teal, mallard *entities.Label) []*entities.DetectionPrediction
	}{
		{"no prediction rows", func(uint, *entities.Label, *entities.Label) []*entities.DetectionPrediction { return nil }},
		{"a row for the old label", func(id uint, teal, _ *entities.Label) []*entities.DetectionPrediction {
			return []*entities.DetectionPrediction{{DetectionID: id, LabelID: teal.ID, Confidence: speciesTestAlternate, Rank: 1}}
		}},
		{"a row for the new label", func(id uint, _, mallard *entities.Label) []*entities.DetectionPrediction {
			return []*entities.DetectionPrediction{{DetectionID: id, LabelID: mallard.ID, Confidence: speciesTestAlternate, Rank: 1}}
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, repo := speciesTestDB(t)
			ctx := t.Context()
			teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
			mallard := createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
			det := speciesTestDetection(t, db, teal)
			sibling := speciesTestDetection(t, db, teal)
			if rows := tt.rows(det.ID, teal, mallard); len(rows) > 0 {
				require.NoError(t, repo.SavePredictionsBatch(ctx, rows))
			}
			before, err := repo.GetPredictions(ctx, det.ID)
			require.NoError(t, err)

			require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard))

			got, err := repo.Get(ctx, det.ID)
			require.NoError(t, err)
			assert.Equal(t, mallard.ID, got.LabelID)
			require.NotNil(t, got.OriginalLabelID)
			assert.Equal(t, teal.ID, *got.OriginalLabelID)
			after, err := repo.GetPredictions(ctx, det.ID)
			require.NoError(t, err)
			assert.Equal(t, before, after, "prediction rows are neither read for the result nor written")
			gotSibling, err := repo.Get(ctx, sibling.ID)
			require.NoError(t, err)
			assert.Equal(t, *sibling, *gotSibling, "another detection of the same label is left as it is")
			review, err := repo.GetReview(ctx, det.ID)
			require.NoError(t, err, "a first change creates the review of an unreviewed detection")
			assert.Equal(t, entities.VerificationCorrect, review.Verified)
		})
	}
}

// TestDetectionRepository_ChangeSpecies_Label pins which label a species name
// resolves to for the detection's model.
func TestDetectionRepository_ChangeSpecies_Label(t *testing.T) {
	const (
		animalLabelType = uint(7)
		mammalClass     = uint(3)
	)

	t.Run("a species only another model has gets a label for the detection's model", func(t *testing.T) {
		db, repo := speciesTestDB(t)
		ctx := t.Context()
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		perchMallard := &entities.Label{
			ScientificName: speciesTestMallard, ModelID: speciesTestPerch,
			LabelTypeID: animalLabelType, TaxonomicClassID: new(mammalClass),
		}
		require.NoError(t, db.Table(tableLabels).Create(perchMallard).Error)
		// A newer label of the same species from a third model, with other values:
		// copying from anything but the oldest label would show in the created label.
		newerMallard := &entities.Label{
			ScientificName: speciesTestMallard, ModelID: speciesTestThirdModel, LabelTypeID: animalLabelType + 1,
		}
		require.NoError(t, db.Table(tableLabels).Create(newerMallard).Error)
		det := speciesTestDetection(t, db, teal)

		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard))

		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, speciesTestBirdnet, got.ModelID)
		var created entities.Label
		require.NoError(t, db.Table(tableLabels).First(&created, got.LabelID).Error)
		assert.NotEqual(t, perchMallard.ID, created.ID, "the detection must not take another model's label")
		assert.NotEqual(t, newerMallard.ID, created.ID, "the detection must not take another model's label")
		assert.Equal(t, speciesTestMallard, created.ScientificName)
		assert.Equal(t, speciesTestBirdnet, created.ModelID)
		assert.Equal(t, animalLabelType, created.LabelTypeID, "the label type is copied from the existing label")
		require.NotNil(t, created.TaxonomicClassID)
		assert.Equal(t, mammalClass, *created.TaxonomicClassID, "the taxonomic class is copied from the existing label")
	})

	t.Run("a taxonomic alias resolves to the model's own label", func(t *testing.T) {
		db, repo := speciesTestDB(t)
		ctx := t.Context()
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		dove := createTestLabel(t, db, speciesTestDoveOld, speciesTestBirdnet)
		createTestLabel(t, db, speciesTestDoveNew, speciesTestPerch)
		det := speciesTestDetection(t, db, teal)
		labelsBefore := speciesTestLabelCount(t, db)

		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestDoveNew))

		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, dove.ID, got.LabelID)
		assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db), "no label is created for an alias of an existing one")
	})

	t.Run("an exact name wins over an older alias of the same species", func(t *testing.T) {
		db, repo := speciesTestDB(t)
		ctx := t.Context()
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		createTestLabel(t, db, speciesTestDoveOld, speciesTestBirdnet)
		exact := createTestLabel(t, db, speciesTestDoveNew, speciesTestBirdnet)
		det := speciesTestDetection(t, db, teal)
		labelsBefore := speciesTestLabelCount(t, db)

		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestDoveNew))

		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, exact.ID, got.LabelID, "the label of the requested spelling is taken, not an older alias")
		assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db), "no label is created")
	})

	t.Run("the oldest of a model's aliases of one species is taken", func(t *testing.T) {
		db, repo := speciesTestDB(t)
		ctx := t.Context()
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		// Two spellings of one species in one model. The older label sorts later by
		// name, so a pick by name order would take the newer one.
		oldest := createTestLabel(t, db, speciesTestDoveOld, speciesTestBirdnet)
		createTestLabel(t, db, speciesTestDoveNew, speciesTestBirdnet)
		det := speciesTestDetection(t, db, teal)
		labelsBefore := speciesTestLabelCount(t, db)

		// Neither stored spelling matches this one exactly.
		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, strings.ToLower(speciesTestDoveOld)))

		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, oldest.ID, got.LabelID)
		assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db), "no label is created")
	})
}

// TestDetectionRepository_ChangeSpecies_Refused covers the requests that must
// leave the detection, its original marker, its review and the labels untouched.
func TestDetectionRepository_ChangeSpecies_Refused(t *testing.T) {
	const missingID = uint(4242)

	db, repo := speciesTestDB(t)
	ctx := t.Context()
	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
	// Only another model has a label of this species, so changing to it would
	// need a new label.
	createTestLabel(t, db, speciesTestGadwall, speciesTestPerch)
	labelsBefore := speciesTestLabelCount(t, db)

	// untouched asserts that nothing was written.
	untouched := func(t *testing.T, det *entities.Detection) {
		t.Helper()
		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, *det, *got)
		assert.Nil(t, got.OriginalLabelID)
		_, err = repo.GetReview(ctx, det.ID)
		require.ErrorIs(t, err, ErrReviewNotFound)
		assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db), "no label is created")
	}

	t.Run("the same species is a no-op", func(t *testing.T) {
		det := speciesTestDetection(t, db, teal)
		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, " "+speciesTestTeal+" "))
		untouched(t, det)
	})

	t.Run("a locked detection", func(t *testing.T) {
		det := speciesTestDetection(t, db, teal)
		require.NoError(t, db.Table(tableDetectionLocks).Create(&entities.DetectionLock{DetectionID: det.ID}).Error)
		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard), ErrDetectionLocked)
		// The lock is reported for the detection's own species too, and for a
		// species that would need a new label, which is then not created.
		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestTeal), ErrDetectionLocked)
		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestGadwall), ErrDetectionLocked)
		untouched(t, det)
	})

	for name, species := range map[string]string{
		"a species no model has a label for": "Corvus corax",
		"an empty species":                   "  ",
	} {
		t.Run(name, func(t *testing.T) {
			det := speciesTestDetection(t, db, teal)
			require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, species), ErrLabelNotFound)
			untouched(t, det)
		})
	}

	t.Run("a missing detection", func(t *testing.T) {
		require.ErrorIs(t, repo.ChangeSpecies(ctx, missingID, speciesTestMallard), ErrDetectionNotFound)
	})
}

// injectBeforeDetectionUpdate registers a callback that runs inject once, just
// before the first UPDATE of the detections table, on the session of the
// statement itself. The test database has one connection, which the species
// change's transaction holds, so the callback must not use another handle.
func injectBeforeDetectionUpdate(t *testing.T, db *gorm.DB, inject func(tx *gorm.DB)) {
	t.Helper()
	var fired atomic.Bool
	require.NoError(t, db.Callback().Update().Before("gorm:update").Register(speciesTestInjectName, func(tx *gorm.DB) {
		if !strings.HasSuffix(tx.Statement.Table, tableDetections) || !fired.CompareAndSwap(false, true) {
			return
		}
		inject(tx.Session(&gorm.Session{NewDB: true}))
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Update().Remove(speciesTestInjectName)) })
}

// TestDetectionRepository_ChangeSpecies_LockOrDeleteDuringChange reaches the
// branch where the guarded UPDATE matches no row: a lock lands, or the detection
// is deleted, after the reads and before the UPDATE. Without the injection that
// branch cannot be reached.
func TestDetectionRepository_ChangeSpecies_LockOrDeleteDuringChange(t *testing.T) {
	setup := func(t *testing.T) (*gorm.DB, DetectionRepository, *entities.Detection) {
		t.Helper()
		db, repo := speciesTestDB(t)
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
		det := speciesTestDetection(t, db, teal)
		require.NoError(t, repo.SaveReview(t.Context(), &entities.DetectionReview{
			DetectionID: det.ID, Verified: entities.VerificationFalsePositive,
		}))
		return db, repo, det
	}

	t.Run("a lock lands before the update", func(t *testing.T) {
		db, repo, det := setup(t)
		ctx := t.Context()
		injectBeforeDetectionUpdate(t, db, func(tx *gorm.DB) {
			require.NoError(t, tx.Table(tableDetectionLocks).Create(&entities.DetectionLock{DetectionID: det.ID}).Error)
		})

		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard), ErrDetectionLocked)

		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, *det, *got, "the detection is unchanged")
		review, err := repo.GetReview(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, entities.VerificationFalsePositive, review.Verified, "the review is unchanged")
	})

	t.Run("the detection is deleted before the update", func(t *testing.T) {
		db, repo, det := setup(t)
		injectBeforeDetectionUpdate(t, db, func(tx *gorm.DB) {
			require.NoError(t, tx.Table(tableDetections).Delete(&entities.Detection{}, det.ID).Error)
		})

		require.ErrorIs(t, repo.ChangeSpecies(t.Context(), det.ID, speciesTestMallard), ErrDetectionNotFound)
	})
}

// TestDetectionRepository_ChangeSpecies_RecordsLabelHeldAtUpdateTime pins that the
// original is the label the row holds when the UPDATE runs, not the label the
// earlier read saw: the label is changed by another writer after the reads and
// before the UPDATE.
func TestDetectionRepository_ChangeSpecies_RecordsLabelHeldAtUpdateTime(t *testing.T) {
	db, repo := speciesTestDB(t)
	ctx := t.Context()
	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	mallard := createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
	gadwall := createTestLabel(t, db, speciesTestGadwall, speciesTestBirdnet)
	det := speciesTestDetection(t, db, teal)
	injectBeforeDetectionUpdate(t, db, func(tx *gorm.DB) {
		require.NoError(t, tx.Table(tableDetections).Where("id = ?", det.ID).
			UpdateColumn("label_id", gadwall.ID).Error)
	})

	require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard))

	got, err := repo.Get(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, mallard.ID, got.LabelID)
	require.NotNil(t, got.OriginalLabelID)
	assert.Equal(t, gadwall.ID, *got.OriginalLabelID, "the original is the label the row held at update time")
}

// TestDetectionRepository_ChangeSpecies_ReviewFailureRollsBackLabel pins that the
// label update and the review upsert are one transaction: the review insert runs
// on the transaction's handle, and when it fails the detection keeps its label,
// its original stays NULL and its review is as it was.
func TestDetectionRepository_ChangeSpecies_ReviewFailureRollsBackLabel(t *testing.T) {
	const callbackName = "test:fail_review_insert"
	errReviewInsert := errors.NewStd("review insert failed")

	db, repo := speciesTestDB(t)
	ctx := t.Context()
	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
	det := speciesTestDetection(t, db, teal)
	require.NoError(t, repo.SaveReview(ctx, &entities.DetectionReview{
		DetectionID: det.ID, Verified: entities.VerificationFalsePositive,
	}))

	// Registered after the seeding above, which also goes through the review upsert.
	// It runs before the statement's own transaction handling, so a statement on the
	// pool is failed here instead of waiting for the connection the transaction holds.
	var onTransaction atomic.Bool
	require.NoError(t, db.Callback().Create().Before("gorm:begin_transaction").Register(callbackName, func(tx *gorm.DB) {
		if !strings.HasSuffix(tx.Statement.Table, tableDetectionReviews) {
			return
		}
		_, inTransaction := tx.Statement.ConnPool.(gorm.TxCommitter)
		onTransaction.Store(inTransaction)
		_ = tx.AddError(errReviewInsert)
	}))
	t.Cleanup(func() { require.NoError(t, db.Callback().Create().Remove(callbackName)) })

	require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard), errReviewInsert)

	assert.True(t, onTransaction.Load(), "the review insert runs on the transaction of the label update")
	got, err := repo.Get(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, *det, *got, "the label update is rolled back with the failed review")
	review, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.VerificationFalsePositive, review.Verified)
}
