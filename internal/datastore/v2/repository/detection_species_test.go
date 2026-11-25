package repository

import (
	"strings"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
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

// The model identities an offer lists; they match the ai_models rows speciesTestDB
// seeds under the IDs above.
var (
	speciesTestModelBirdnet = detection.ModelInfo{Name: "BirdNET", Version: "2.4", Variant: "default"}
	speciesTestModelPerch   = detection.ModelInfo{Name: "Perch", Version: "2", Variant: "default"}
	speciesTestModelBat     = detection.ModelInfo{Name: "BSG-BAT", Version: "1", Variant: "default"}
)

// speciesTestKnownBy is an offered model that spells the species as sci.
func speciesTestKnownBy(model detection.ModelInfo, sci string) OfferedModel {
	return OfferedModel{Model: model, ScientificName: sci}
}

// speciesTestOffer is an offer listing the models.
func speciesTestOffer(models ...OfferedModel) *SpeciesOffer {
	return &SpeciesOffer{Models: models}
}

// speciesTestDB opens a database with the tables a species change touches and a
// repository on it.
func speciesTestDB(t *testing.T) (*gorm.DB, DetectionRepository) {
	t.Helper()
	db := setupDetectionTestDBWithLabels(t)
	require.NoError(t, db.AutoMigrate(&entities.DetectionPrediction{}, &entities.LabelType{}, &entities.TaxonomicClass{}, &entities.AIModel{}),
		"failed to migrate predictions, lookup and model tables")
	require.NoError(t, db.Create(entities.DefaultLabelTypes()).Error)
	require.NoError(t, db.Create(entities.DefaultTaxonomicClasses()).Error)
	// The detection models the creation tests use, with the IDs of the constants.
	require.NoError(t, db.Create([]entities.AIModel{
		{ID: speciesTestBirdnet, Name: "BirdNET", Version: "2.4", Variant: "default", ModelType: entities.ModelTypeBird},
		{ID: speciesTestPerch, Name: "Perch", Version: "2", Variant: "default", ModelType: entities.ModelTypeMulti},
		{ID: speciesTestThirdModel, Name: "BSG-BAT", Version: "1", Variant: "default", ModelType: entities.ModelTypeBat},
	}).Error)
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
		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, step.species, nil), step.name)

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

// TestDetectionRepository_ChangeSpecies_ReadsLoadOriginalLabel pins that the single
// and the batch relation loaders attach the label of the model's original pick, only
// to a changed detection, and that a deleted label row is not an error.
func TestDetectionRepository_ChangeSpecies_ReadsLoadOriginalLabel(t *testing.T) {
	db, repo := speciesTestDB(t)
	require.NoError(t, db.AutoMigrate(&entities.DetectionComment{}), "failed to migrate comments table")
	ctx := t.Context()
	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	mallard := createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
	changed := speciesTestDetection(t, db, teal)
	// No other row holds the original label, so the read finds it only through the
	// changed row's original id.
	untouched := speciesTestDetection(t, db, mallard)
	require.NoError(t, repo.ChangeSpecies(ctx, changed.ID, speciesTestMallard, nil))

	got, err := repo.GetWithRelations(ctx, changed.ID)
	require.NoError(t, err)
	require.NotNil(t, got.OriginalLabel)
	assert.Equal(t, teal.ID, got.OriginalLabel.ID)
	got, err = repo.GetWithRelations(ctx, untouched.ID)
	require.NoError(t, err)
	assert.Nil(t, got.OriginalLabel)

	recent, err := repo.GetRecent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, recent, 2)
	for _, d := range recent {
		if d.ID == changed.ID {
			require.NotNil(t, d.OriginalLabel)
			assert.Equal(t, teal.ID, d.OriginalLabel.ID)
		} else {
			assert.Nil(t, d.OriginalLabel)
		}
	}

	// A label row that is gone leaves the original unset instead of failing the read.
	require.NoError(t, db.Exec("PRAGMA foreign_keys = OFF").Error)
	require.NoError(t, db.Delete(&entities.Label{}, teal.ID).Error)
	got, err = repo.GetWithRelations(ctx, changed.ID)
	require.NoError(t, err)
	assert.Nil(t, got.OriginalLabel)
	recent, err = repo.GetRecent(ctx, 10)
	require.NoError(t, err)
	require.Len(t, recent, 2)
	for _, d := range recent {
		assert.Nil(t, d.OriginalLabel)
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

			require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard, nil))

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
	t.Run("a taxonomic alias resolves to the model's own label", func(t *testing.T) {
		db, repo := speciesTestDB(t)
		ctx := t.Context()
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		dove := createTestLabel(t, db, speciesTestDoveOld, speciesTestBirdnet)
		createTestLabel(t, db, speciesTestDoveNew, speciesTestPerch)
		det := speciesTestDetection(t, db, teal)
		labelsBefore := speciesTestLabelCount(t, db)

		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestDoveNew, nil))

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

		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestDoveNew, nil))

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
		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, strings.ToLower(speciesTestDoveOld), nil))

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
	// Only another model has a label of this species; without an offer it is not
	// a species the caller offers, so changing to it is refused.
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
		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, " "+speciesTestTeal+" ", nil))
		untouched(t, det)
	})

	t.Run("a locked detection", func(t *testing.T) {
		det := speciesTestDetection(t, db, teal)
		require.NoError(t, db.Table(tableDetectionLocks).Create(&entities.DetectionLock{DetectionID: det.ID}).Error)
		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard, nil), ErrDetectionLocked)
		// The lock is reported for the detection's own species too, and for an
		// offered species that would need a new label, which is then not created.
		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestTeal, nil), ErrDetectionLocked)
		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestGadwall, speciesTestOffer(speciesTestKnownBy(speciesTestModelPerch, speciesTestGadwall))), ErrDetectionLocked)
		untouched(t, det)
	})

	for name, species := range map[string]string{
		"a species not offered": "Corvus corax",
		"a species only another model has a label for, not offered": speciesTestGadwall,
		"an empty species": "  ",
	} {
		t.Run(name, func(t *testing.T) {
			det := speciesTestDetection(t, db, teal)
			require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, species, nil), ErrLabelNotFound)
			untouched(t, det)
		})
	}

	t.Run("a missing detection", func(t *testing.T) {
		require.ErrorIs(t, repo.ChangeSpecies(ctx, missingID, speciesTestMallard, nil), ErrDetectionNotFound)
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

		require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard, nil), ErrDetectionLocked)

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

		require.ErrorIs(t, repo.ChangeSpecies(t.Context(), det.ID, speciesTestMallard, nil), ErrDetectionNotFound)
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

	require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard, nil))

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

	require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard, nil), errReviewInsert)

	assert.True(t, onTransaction.Load(), "the review insert runs on the transaction of the label update")
	got, err := repo.Get(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, *det, *got, "the label update is rolled back with the failed review")
	review, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.VerificationFalsePositive, review.Verified)
}

// TestDetectionRepository_ChangeSpecies_SameSpeciesIsNoOp pins that a request for
// the species the detection's current label already stands for writes nothing,
// whichever spelling is requested and whichever of the model's labels of the
// species the detection holds. The model holds two spellings: the old one has the
// lower label ID.
func TestDetectionRepository_ChangeSpecies_SameSpeciesIsNoOp(t *testing.T) {
	tests := []struct {
		name string
		// onNewer is whether the detection holds the newer spelling.
		onNewer bool
		request string
	}{
		{"newer spelling, lower-case old spelling", true, strings.ToLower(speciesTestDoveOld)},
		{"newer spelling, old spelling exactly", true, speciesTestDoveOld},
		{"newer spelling, lower-case new spelling", true, strings.ToLower(speciesTestDoveNew)},
		{"older spelling, new spelling exactly", false, speciesTestDoveNew},
		{"older spelling, lower-case new spelling", false, strings.ToLower(speciesTestDoveNew)},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, repo := speciesTestDB(t)
			ctx := t.Context()
			older := createTestLabel(t, db, speciesTestDoveOld, speciesTestBirdnet)
			newer := createTestLabel(t, db, speciesTestDoveNew, speciesTestBirdnet)
			held := older
			if tt.onNewer {
				held = newer
			}
			det := speciesTestDetection(t, db, held)
			labelsBefore := speciesTestLabelCount(t, db)

			require.NoError(t, repo.ChangeSpecies(ctx, det.ID, tt.request, nil))

			got, err := repo.Get(ctx, det.ID)
			require.NoError(t, err)
			assert.Equal(t, *det, *got, "the detection is left as it is, with its original NULL")
			_, err = repo.GetReview(ctx, det.ID)
			require.ErrorIs(t, err, ErrReviewNotFound, "no review is created")
			assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db), "no label is created")
		})
	}

	t.Run("another species still changes the detection and records the original", func(t *testing.T) {
		db, repo := speciesTestDB(t)
		ctx := t.Context()
		createTestLabel(t, db, speciesTestDoveOld, speciesTestBirdnet)
		newer := createTestLabel(t, db, speciesTestDoveNew, speciesTestBirdnet)
		teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
		det := speciesTestDetection(t, db, newer)

		require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestTeal, nil))

		got, err := repo.Get(ctx, det.ID)
		require.NoError(t, err)
		assert.Equal(t, teal.ID, got.LabelID)
		require.NotNil(t, got.OriginalLabelID)
		assert.Equal(t, newer.ID, *got.OriginalLabelID)
	})
}

// TestDetectionRepository_ChangeSpecies_EmptyKey pins that a name with no species
// part is refused and writes nothing, also when a label of the model has an empty
// key too.
func TestDetectionRepository_ChangeSpecies_EmptyKey(t *testing.T) {
	for _, name := range []string{"_Blackbird", "_", " _Blackbird_Code "} {
		t.Run(name, func(t *testing.T) {
			db, repo := speciesTestDB(t)
			ctx := t.Context()
			teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
			// A label whose own key is empty must not match the empty request.
			createTestLabel(t, db, "_Odd", speciesTestBirdnet)
			det := speciesTestDetection(t, db, teal)
			labelsBefore := speciesTestLabelCount(t, db)

			require.ErrorIs(t, repo.ChangeSpecies(ctx, det.ID, name, speciesTestOffer(speciesTestKnownBy(speciesTestModelBirdnet, name))), ErrLabelNotFound)

			got, err := repo.Get(ctx, det.ID)
			require.NoError(t, err)
			assert.Equal(t, *det, *got)
			_, err = repo.GetReview(ctx, det.ID)
			require.ErrorIs(t, err, ErrReviewNotFound)
			assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db))
		})
	}
}

// TestDetectionRepository_ChangeSpecies_CurrentLabelGone pins that a detection
// whose current label row no longer exists can still be changed: the comparison
// with the current species is skipped, and the original is the id the row held.
func TestDetectionRepository_ChangeSpecies_CurrentLabelGone(t *testing.T) {
	db, repo := speciesTestDB(t)
	ctx := t.Context()
	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	mallard := createTestLabel(t, db, speciesTestMallard, speciesTestBirdnet)
	det := speciesTestDetection(t, db, teal)
	require.NoError(t, db.Table(tableLabels).Delete(&entities.Label{}, teal.ID).Error)
	labelsBefore := speciesTestLabelCount(t, db)

	require.NoError(t, repo.ChangeSpecies(ctx, det.ID, speciesTestMallard, nil))

	want := *det
	want.LabelID = mallard.ID
	want.OriginalLabelID = new(teal.ID)
	got, err := repo.Get(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, want, *got)
	review, err := repo.GetReview(ctx, det.ID)
	require.NoError(t, err)
	assert.Equal(t, entities.VerificationCorrect, review.Verified)
	assert.Equal(t, labelsBefore, speciesTestLabelCount(t, db), "no label is created")
}

// speciesTestLabelAttributes returns the label type name and the taxonomic class
// name ("" for none) of a label row.
func speciesTestLabelAttributes(t *testing.T, db *gorm.DB, label *entities.Label) (typeName, className string) {
	t.Helper()
	var labelType entities.LabelType
	require.NoError(t, db.First(&labelType, label.LabelTypeID).Error)
	if label.TaxonomicClassID != nil {
		var class entities.TaxonomicClass
		require.NoError(t, db.First(&class, *label.TaxonomicClassID).Error)
		className = class.Name
	}
	return labelType.Name, className
}

// TestDetectionRepository_ChangeSpecies_CreatesOfferedLabel pins the label made
// for a species the detection's model has no label of: its name, owner, label type
// and taxonomic class, for each way the offer describes the species.
func TestDetectionRepository_ChangeSpecies_CreatesOfferedLabel(t *testing.T) {
	const missingModel = uint(99)
	tests := []struct {
		name      string
		model     uint // the detection's model
		request   string
		offer     []OfferedModel
		other     *entities.Label // a pre-existing label of another model for the species
		wantName  string
		wantType  string
		wantClass string
	}{
		{
			name: "own model knows a bird", model: speciesTestBirdnet, request: speciesTestMallard,
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, speciesTestMallard), speciesTestKnownBy(speciesTestModelPerch, speciesTestMallard)},
			wantName: speciesTestMallard, wantType: entities.LabelTypeSpecies, wantClass: entities.TaxonomicClassAves,
		},
		{
			name: "only a multi model knows it", model: speciesTestBirdnet, request: "Canis familiaris",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelPerch, "Canis familiaris")},
			wantName: "Canis familiaris", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "a bat and a multi model know a bat", model: speciesTestBirdnet, request: "Pipistrellus pipistrellus",
			offer: []OfferedModel{
				speciesTestKnownBy(speciesTestModelBat, "Pipistrellus pipistrellus"), speciesTestKnownBy(speciesTestModelPerch, "Pipistrellus pipistrellus"),
			},
			wantName: "Pipistrellus pipistrellus", wantType: entities.LabelTypeSpecies, wantClass: entities.TaxonomicClassChiroptera,
		},
		{
			name: "a bird model and a bat model disagree on the class", model: speciesTestPerch, request: "Rousettus aegyptiacus",
			offer: []OfferedModel{
				speciesTestKnownBy(speciesTestModelBirdnet, "Rousettus aegyptiacus"), speciesTestKnownBy(speciesTestModelBat, "Rousettus aegyptiacus"),
			},
			wantName: "Rousettus aegyptiacus", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "own multi model knows a species a bird model also knows", model: speciesTestPerch, request: "Canis latrans",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, "Canis latrans"), speciesTestKnownBy(speciesTestModelPerch, "Canis latrans")},
			wantName: "Canis latrans", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "a sound class with a category", model: speciesTestBirdnet, request: "Dog",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, "Dog_Dog"), speciesTestKnownBy(speciesTestModelPerch, "Dog")},
			wantName: "Dog", wantType: entities.LabelTypeAnimal,
		},
		{
			name: "a multi-word sound class", model: speciesTestBirdnet, request: "Power_tool",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelPerch, "Power_tool")},
			wantName: "Power_tool", wantType: entities.LabelTypeMechanical,
		},
		{
			name: "a BirdNET class without a category", model: speciesTestBirdnet, request: "Human vocal",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, "Human vocal_Human vocal")},
			wantName: "Human vocal", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "no model knows it", model: speciesTestBirdnet, request: "Corvus corax",
			offer:    nil,
			wantName: "Corvus corax", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "the caller's case does not win over the own model", model: speciesTestBirdnet, request: "anas platyrhynchos",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, "Anas platyrhynchos_Mallard")},
			wantName: "Anas platyrhynchos", wantType: entities.LabelTypeSpecies, wantClass: entities.TaxonomicClassAves,
		},
		{
			name: "the caller's case does not win over a model that knows it", model: speciesTestBirdnet, request: "canis familiaris",
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelPerch, "Canis familiaris")},
			wantName: "Canis familiaris", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "the own model's legacy spelling is stored under the accepted name", model: speciesTestBirdnet, request: speciesTestDoveNew,
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, speciesTestDoveOld)},
			wantName: speciesTestDoveNew, wantType: entities.LabelTypeSpecies, wantClass: entities.TaxonomicClassAves,
		},
		{
			name: "a legacy request spelling no model knows", model: speciesTestBirdnet, request: speciesTestDoveOld,
			offer:    nil,
			wantName: speciesTestDoveNew, wantType: entities.LabelTypeSpecies,
		},
		{
			name: "a request with a common name and no model", model: speciesTestBirdnet, request: "Corvus corax_Common Raven",
			offer:    nil,
			wantName: "Corvus corax", wantType: entities.LabelTypeSpecies,
		},
		{
			name: "the detection's model row is missing", model: missingModel, request: speciesTestMallard,
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, speciesTestMallard)},
			wantName: speciesTestMallard, wantType: entities.LabelTypeSpecies, wantClass: entities.TaxonomicClassAves,
		},
		{
			name: "another model's row of the species is not copied", model: speciesTestBirdnet, request: speciesTestGadwall,
			offer:    []OfferedModel{speciesTestKnownBy(speciesTestModelBirdnet, speciesTestGadwall)},
			other:    &entities.Label{ScientificName: speciesTestGadwall, ModelID: speciesTestPerch, LabelTypeID: 5},
			wantName: speciesTestGadwall, wantType: entities.LabelTypeSpecies, wantClass: entities.TaxonomicClassAves,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			db, repo := speciesTestDB(t)
			ctx := t.Context()
			old := createTestLabel(t, db, speciesTestTeal, tt.model)
			det := speciesTestDetection(t, db, old)
			if tt.other != nil {
				require.NoError(t, db.Table(tableLabels).Create(tt.other).Error)
			}
			var offer *SpeciesOffer
			if tt.offer != nil {
				offer = speciesTestOffer(tt.offer...)
			} else {
				offer = speciesTestOffer()
			}

			require.NoError(t, repo.ChangeSpecies(ctx, det.ID, tt.request, offer))

			got, err := repo.Get(ctx, det.ID)
			require.NoError(t, err)
			assert.Equal(t, tt.model, got.ModelID, "the detection keeps its model")
			require.NotNil(t, got.OriginalLabelID)
			assert.Equal(t, old.ID, *got.OriginalLabelID)
			var created entities.Label
			require.NoError(t, db.Table(tableLabels).First(&created, got.LabelID).Error)
			assert.NotEqual(t, old.ID, created.ID)
			if tt.other != nil {
				assert.NotEqual(t, tt.other.ID, created.ID, "the detection must not take another model's label")
			}
			assert.Equal(t, tt.wantName, created.ScientificName)
			assert.Equal(t, tt.model, created.ModelID, "the label belongs to the detection's model")
			typeName, className := speciesTestLabelAttributes(t, db, &created)
			assert.Equal(t, tt.wantType, typeName)
			assert.Equal(t, tt.wantClass, className)
			review, err := repo.GetReview(ctx, det.ID)
			require.NoError(t, err)
			assert.Equal(t, entities.VerificationCorrect, review.Verified)
		})
	}
}

// TestDetectionRepository_ChangeSpecies_OfferedLabelIsReused follows a created
// label through a repeated request, a second detection of the model and the save
// path's own lookup: all of them must land on the one row.
func TestDetectionRepository_ChangeSpecies_OfferedLabelIsReused(t *testing.T) {
	db, repo := speciesTestDB(t)
	ctx := t.Context()
	teal := createTestLabel(t, db, speciesTestTeal, speciesTestBirdnet)
	detA := speciesTestDetection(t, db, teal)
	detB := speciesTestDetection(t, db, teal)
	offer := speciesTestOffer(speciesTestKnownBy(speciesTestModelBirdnet, speciesTestDoveOld))

	require.NoError(t, repo.ChangeSpecies(ctx, detA.ID, speciesTestDoveOld, offer))
	gotA, err := repo.Get(ctx, detA.ID)
	require.NoError(t, err)
	var created entities.Label
	require.NoError(t, db.Table(tableLabels).First(&created, gotA.LabelID).Error)
	assert.Equal(t, speciesTestDoveNew, created.ScientificName)
	labelsAfterFirst := speciesTestLabelCount(t, db)

	// The same request again writes nothing.
	reviewBefore, err := repo.GetReview(ctx, detA.ID)
	require.NoError(t, err)
	require.NoError(t, repo.ChangeSpecies(ctx, detA.ID, speciesTestDoveOld, offer))
	assert.Equal(t, labelsAfterFirst, speciesTestLabelCount(t, db))
	reviewAfter, err := repo.GetReview(ctx, detA.ID)
	require.NoError(t, err)
	assert.Equal(t, reviewBefore, reviewAfter)

	// A second detection changed to the accepted name reuses the row.
	require.NoError(t, repo.ChangeSpecies(ctx, detB.ID, speciesTestDoveNew, speciesTestOffer()))
	gotB, err := repo.Get(ctx, detB.ID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, gotB.LabelID)
	assert.Equal(t, labelsAfterFirst, speciesTestLabelCount(t, db))

	// The save path's GetOrCreate for the species finds the same row.
	saved, err := NewLabelRepository(db, nil, false, false).GetOrCreate(ctx, speciesTestDoveNew, speciesTestBirdnet, created.LabelTypeID, created.TaxonomicClassID)
	require.NoError(t, err)
	assert.Equal(t, created.ID, saved.ID)
	assert.Equal(t, labelsAfterFirst, speciesTestLabelCount(t, db))
}
