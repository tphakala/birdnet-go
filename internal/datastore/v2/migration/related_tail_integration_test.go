//go:build integration

package migration_test

import (
	"context"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/migration/testutil"
	"gorm.io/gorm"
)

// Legacy ids used by the related data tail tests. Notes 1 to relatedSeedNotes
// exist when the migration runs; the late note is written afterwards.
const (
	relatedSeedNotes   = 10
	relatedLateNote    = uint(11)
	relatedReviewNote  = uint(1) // review flipped after the copy
	relatedNewReview   = uint(2) // review added after the copy
	relatedCommentNote = uint(3) // second comment added after the copy
	relatedUnlockNote  = uint(4) // locked at copy time, unlocked after
	relatedLockNote    = uint(5) // locked after the copy
)

// relatedSnapshot is the related data in v2, ordered by id.
type relatedSnapshot struct {
	Reviews     []entities.DetectionReview
	Comments    []entities.DetectionComment
	Locks       []entities.DetectionLock
	Predictions []entities.DetectionPrediction
}

func snapshotRelated(t *testing.T, db *gorm.DB) relatedSnapshot {
	t.Helper()
	var s relatedSnapshot
	require.NoError(t, db.Order("id ASC").Find(&s.Reviews).Error)
	require.NoError(t, db.Order("id ASC").Find(&s.Comments).Error)
	require.NoError(t, db.Order("id ASC").Find(&s.Locks).Error)
	require.NoError(t, db.Order("id ASC").Find(&s.Predictions).Error)
	return s
}

// seedRelatedLegacy seeds notes 1..relatedSeedNotes with one result each, a
// review on note 1, a comment on note 3 and a lock on note 4.
func seedRelatedLegacy(t *testing.T, ctx *testutil.TestContext) {
	t.Helper()

	require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetections(relatedSeedNotes)))
	results := make([]datastore.Results, 0, relatedSeedNotes)
	for i := range relatedSeedNotes {
		id := uint(i + 1) //nolint:gosec // G115: test data uses small values
		results = append(results, testutil.NewResultsBuilder().
			WithID(id).WithNoteID(id).WithSpecies("Secondary Species A").WithConfidence(0.6).Build())
	}
	require.NoError(t, ctx.Seeder.SeedResults(results))
	require.NoError(t, ctx.Seeder.SeedReviews([]datastore.NoteReview{
		testutil.NewReviewBuilder().WithID(1).WithNoteID(relatedReviewNote).WithVerified("correct").Build(),
	}))
	require.NoError(t, ctx.Seeder.SeedComments([]datastore.NoteComment{
		testutil.NewCommentBuilder().WithID(1).WithNoteID(relatedCommentNote).WithEntry("first").Build(),
	}))
	require.NoError(t, ctx.Seeder.SeedLocks([]datastore.NoteLock{
		testutil.NewLockBuilder().WithID(1).WithNoteID(relatedUnlockNote).Build(),
	}))
}

// writeRelatedLegacyAfterCopy changes the legacy store the way the running
// application does between the copy and the restart: through the real legacy
// write methods, plus a detection saved late with its results.
func writeRelatedLegacyAfterCopy(t *testing.T, ctx *testutil.TestContext) {
	t.Helper()

	legacy := &datastore.DataStore{DB: ctx.LegacyGorm}
	require.NoError(t, legacy.SaveNoteReview(&datastore.NoteReview{NoteID: relatedReviewNote, Verified: "false_positive"}))
	require.NoError(t, legacy.SaveNoteReview(&datastore.NoteReview{NoteID: relatedNewReview, Verified: "correct"}))
	require.NoError(t, legacy.SaveNoteComment(&datastore.NoteComment{NoteID: relatedCommentNote, Entry: "second"}))
	require.NoError(t, legacy.UnlockNote("4"))
	require.NoError(t, legacy.LockNote("5"))

	require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetectionsWithIDOffset(1, relatedLateNote)))
	require.NoError(t, ctx.Seeder.SeedResults([]datastore.Results{
		testutil.NewResultsBuilder().WithID(11).WithNoteID(relatedLateNote).WithSpecies("Secondary Species B").WithConfidence(0.7).Build(),
		testutil.NewResultsBuilder().WithID(12).WithNoteID(relatedLateNote).WithSpecies("Secondary Species C").WithConfidence(0.5).Build(),
	}))
	require.NoError(t, legacy.SaveNoteComment(&datastore.NoteComment{NoteID: relatedLateNote, Entry: "late"}))
	require.NoError(t, legacy.LockNote("11"))
}

// requireRelatedDataInV2 asserts the v2 related data after the post-copy writes
// of writeRelatedLegacyAfterCopy reached it exactly once.
func requireRelatedDataInV2(t *testing.T, snap *relatedSnapshot) {
	t.Helper()

	reviews := map[uint]entities.VerificationStatus{}
	for i := range snap.Reviews {
		reviews[snap.Reviews[i].DetectionID] = snap.Reviews[i].Verified
	}
	assert.Equal(t, map[uint]entities.VerificationStatus{
		relatedReviewNote: entities.VerificationFalsePositive,
		relatedNewReview:  entities.VerificationCorrect,
	}, reviews, "a changed verdict must replace the copied one and a new review must arrive")

	commentsByDetection := map[uint][]string{}
	legacyIDs := map[uint]struct{}{}
	for i := range snap.Comments {
		c := &snap.Comments[i]
		commentsByDetection[c.DetectionID] = append(commentsByDetection[c.DetectionID], c.Entry)
		require.NotNil(t, c.LegacyID, "a copied comment must carry its legacy id")
		legacyIDs[*c.LegacyID] = struct{}{}
	}
	assert.Equal(t, map[uint][]string{
		relatedCommentNote: {"first", "second"},
		relatedLateNote:    {"late"},
	}, commentsByDetection, "every legacy comment must be in v2 exactly once")
	assert.Len(t, legacyIDs, len(snap.Comments), "legacy ids must be unique")

	locked := map[uint]struct{}{}
	for i := range snap.Locks {
		locked[snap.Locks[i].DetectionID] = struct{}{}
	}
	assert.Equal(t, map[uint]struct{}{relatedLockNote: {}, relatedLateNote: {}}, locked,
		"the unlocked detection must be unlocked in v2 and the new locks must arrive")

	lateRanks := map[int]string{}
	var latePredictions int
	for i := range snap.Predictions {
		if snap.Predictions[i].DetectionID == relatedLateNote {
			latePredictions++
			lateRanks[snap.Predictions[i].Rank] = "seen"
		}
	}
	assert.Equal(t, 2, latePredictions, "both results of the late detection must be in v2")
	assert.Equal(t, map[int]string{2: "seen", 3: "seen"}, lateRanks, "secondary ranks start at 2 and are not repeated")
	assert.Len(t, snap.Predictions, relatedSeedNotes+2, "the copied predictions must not be duplicated")
}

// stopWorkerWithDrain stops the worker gracefully, which runs the final drain.
func stopWorkerWithDrain(t *testing.T, ctx *testutil.TestContext) {
	t.Helper()
	stopCtx, cancel := context.WithTimeout(t.Context(), workerStopTimeout)
	defer cancel()
	require.NoError(t, ctx.Worker.Stop(stopCtx))
}

// assertRelatedDataAfterCopyReachesV2Once completes a migration, changes the
// legacy related data the way the running application does, lets the worker's
// final drain run, and checks that v2 holds every row once. It then reruns the
// worker and the one-time related copy for the same attempt and checks that
// nothing changed.
func assertRelatedDataAfterCopyReachesV2Once(t *testing.T, ctx *testutil.TestContext, timeout time.Duration) {
	t.Helper()

	seedRelatedLegacy(t, ctx)
	ctx.StartMigration(t, relatedSeedNotes)
	ctx.WaitForCompletion(t, timeout)

	writeRelatedLegacyAfterCopy(t, ctx)
	stopWorkerWithDrain(t, ctx)

	db := ctx.V2Manager.DB()
	first := snapshotRelated(t, db)
	requireRelatedDataInV2(t, &first)

	state, err := ctx.StateManager.GetState()
	require.NoError(t, err)
	cursors, ok := state.RelatedSyncCursors()
	require.True(t, ok, "the cursors must be bound to the migration attempt")
	assert.Equal(t, uint(3), cursors.CommentID, "comment cursor must sit on the last legacy comment")
	assert.Equal(t, relatedLateNote, cursors.PredictionNoteID)
	assert.Equal(t, uint(12), cursors.PredictionResultID)
	assert.NotNil(t, state.RelatedReconciledAt, "the completed review and lock reconcile must be recorded")

	// Rerun: restart the worker (drain again), then repeat the one-time copy.
	ctx.RecreateWorker(t)
	require.NoError(t, ctx.Worker.Start(t.Context()))
	stopWorkerWithDrain(t, ctx)
	require.NoError(t, ctx.RelatedMigrator.MigrateAll(t.Context()))

	second := snapshotRelated(t, db)
	assert.Equal(t, first, second, "reruns must add and change nothing")
}

// TestMigration_RelatedDataWrittenAfterCopyReachesV2Once pins that reviews,
// comments, locks and secondary predictions written to the legacy store after
// the related data copy reach v2 once, and that reruns are idempotent.
func TestMigration_RelatedDataWrittenAfterCopyReachesV2Once(t *testing.T) {
	t.Parallel()

	ctx := testutil.SetupIntegrationTest(t)
	assertRelatedDataAfterCopyReachesV2Once(t, ctx, 30*time.Second)
}

// abandonedCompletedMigration returns a context whose migration is completed
// and whose worker is stopped without a drain, so a test drives the related
// migrator directly.
func abandonedCompletedMigration(t *testing.T) *testutil.TestContext {
	t.Helper()

	ctx := testutil.SetupIntegrationTest(t)
	seedRelatedLegacy(t, ctx)
	ctx.StartMigration(t, relatedSeedNotes)
	ctx.WaitForCompletion(t, 30*time.Second)

	stopCtx, cancel := context.WithTimeout(t.Context(), workerStopTimeout)
	defer cancel()
	require.NoError(t, ctx.Worker.Abandon(stopCtx))
	return ctx
}

func countCommentsOf(t *testing.T, ctx *testutil.TestContext, detectionID uint) int64 {
	t.Helper()
	var n int64
	require.NoError(t, ctx.V2Manager.DB().Model(&entities.DetectionComment{}).Where("detection_id = ?", detectionID).Count(&n).Error)
	return n
}

func countPredictionsOf(t *testing.T, ctx *testutil.TestContext, detectionID uint) int64 {
	t.Helper()
	var n int64
	require.NoError(t, ctx.V2Manager.DB().Model(&entities.DetectionPrediction{}).Where("detection_id = ?", detectionID).Count(&n).Error)
	return n
}

func relatedCursors(t *testing.T, ctx *testutil.TestContext) entities.RelatedSyncCursors {
	t.Helper()
	state, err := ctx.StateManager.GetState()
	require.NoError(t, err)
	cursors, ok := state.RelatedSyncCursors()
	require.True(t, ok)
	return cursors
}

// TestRelatedSyncTail_BlocksOnPendingDetectionsAndSkipsOrphans pins the rule
// that a cursor never passes a row whose detection is expected in v2 but is not
// there yet, and does pass rows whose detection is gone for good.
func TestRelatedSyncTail_BlocksOnPendingDetectionsAndSkipsOrphans(t *testing.T) {
	t.Parallel()

	t.Run("detection above the watermark blocks until it is migrated", func(t *testing.T) {
		t.Parallel()
		ctx := abandonedCompletedMigration(t)
		before := relatedCursors(t, ctx)

		legacy := &datastore.DataStore{DB: ctx.LegacyGorm}
		require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetectionsWithIDOffset(1, relatedLateNote)))
		require.NoError(t, ctx.Seeder.SeedResults([]datastore.Results{
			testutil.NewResultsBuilder().WithID(11).WithNoteID(relatedLateNote).WithSpecies("Secondary Species B").WithConfidence(0.7).Build(),
		}))
		require.NoError(t, legacy.SaveNoteComment(&datastore.NoteComment{NoteID: relatedLateNote, Entry: "late"}))

		result, err := ctx.RelatedMigrator.SyncTail(t.Context(), false)
		require.NoError(t, err)
		assert.True(t, result.Blocked)
		assert.Equal(t, before, relatedCursors(t, ctx), "no cursor may pass rows of a detection that is not in v2")
		assert.Zero(t, countCommentsOf(t, ctx, relatedLateNote))
		assert.Zero(t, countPredictionsOf(t, ctx, relatedLateNote))

		// The worker's drain migrates the detection first, then the rows arrive once.
		require.NoError(t, ctx.Worker.Start(t.Context()))
		stopWorkerWithDrain(t, ctx)
		assert.Equal(t, int64(1), countCommentsOf(t, ctx, relatedLateNote))
		assert.Equal(t, int64(1), countPredictionsOf(t, ctx, relatedLateNote))
	})

	t.Run("dirty detection blocks the comment cursor", func(t *testing.T) {
		t.Parallel()
		ctx := abandonedCompletedMigration(t)
		before := relatedCursors(t, ctx)

		const dirtyNote = uint(6)
		require.NoError(t, ctx.V2Manager.DB().Delete(&entities.Detection{}, dirtyNote).Error)
		require.NoError(t, ctx.StateManager.AddDirtyID(dirtyNote))
		require.NoError(t, ctx.Seeder.SeedComments([]datastore.NoteComment{
			testutil.NewCommentBuilder().WithID(2).WithNoteID(dirtyNote).WithEntry("pending").Build(),
			testutil.NewCommentBuilder().WithID(3).WithNoteID(relatedCommentNote).WithEntry("behind it").Build(),
		}))

		result, err := ctx.RelatedMigrator.SyncTail(t.Context(), false)
		require.NoError(t, err)
		assert.True(t, result.Blocked)
		assert.Equal(t, before.CommentID, relatedCursors(t, ctx).CommentID, "the comment cursor must stay before the pending row")
		assert.Zero(t, countCommentsOf(t, ctx, dirtyNote))
		assert.Equal(t, int64(1), countCommentsOf(t, ctx, relatedCommentNote), "rows behind a blocked row wait for the next pass")
	})

	t.Run("orphan row is skipped and later rows arrive", func(t *testing.T) {
		t.Parallel()
		ctx := abandonedCompletedMigration(t)

		const orphanNote = uint(6)
		require.NoError(t, ctx.V2Manager.DB().Delete(&entities.Detection{}, orphanNote).Error)
		require.NoError(t, ctx.Seeder.SeedComments([]datastore.NoteComment{
			testutil.NewCommentBuilder().WithID(2).WithNoteID(orphanNote).WithEntry("orphan").Build(),
			testutil.NewCommentBuilder().WithID(3).WithNoteID(2).WithEntry("after the orphan").Build(),
		}))

		result, err := ctx.RelatedMigrator.SyncTail(t.Context(), false)
		require.NoError(t, err)
		assert.False(t, result.Blocked)
		assert.Equal(t, uint(3), relatedCursors(t, ctx).CommentID, "the cursor must pass the orphan")
		assert.Zero(t, countCommentsOf(t, ctx, orphanNote))
		assert.Equal(t, int64(1), countCommentsOf(t, ctx, 2))
	})
}

// TestRelatedSyncTail_ReconcilesLocksAcrossBatches pins the range merge of the
// lock reconcile across legacy batch boundaries: only the locks the legacy store
// no longer has are removed, and locks at the batch edges survive.
func TestRelatedSyncTail_ReconcilesLocksAcrossBatches(t *testing.T) {
	t.Parallel()

	const (
		notes = 2300 // more than two reconcile batches of 1000
	)
	ctx := testutil.SetupIntegrationTest(t)
	require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetections(notes)))
	locks := make([]datastore.NoteLock, 0, notes)
	for id := uint(1); id <= notes; id++ {
		locks = append(locks, testutil.NewLockBuilder().WithID(id).WithNoteID(id).Build())
	}
	require.NoError(t, ctx.Seeder.SeedLocks(locks))

	ctx.StartMigration(t, notes)
	ctx.WaitForCompletion(t, 60*time.Second)
	stopCtx, cancel := context.WithTimeout(t.Context(), workerStopTimeout)
	defer cancel()
	require.NoError(t, ctx.Worker.Abandon(stopCtx))

	var locked int64
	require.NoError(t, ctx.V2Manager.DB().Model(&entities.DetectionLock{}).Count(&locked).Error)
	require.Equal(t, int64(notes), locked)

	legacy := &datastore.DataStore{DB: ctx.LegacyGorm}
	unlocked := []uint{1, 999, 1000, 1001, 2000, 2001, notes}
	for _, id := range unlocked {
		require.NoError(t, legacy.UnlockNote(uintString(id)))
	}

	result, err := ctx.RelatedMigrator.SyncTail(t.Context(), true)
	require.NoError(t, err)
	assert.Equal(t, len(unlocked), result.LocksRemoved)
	assert.Zero(t, result.LocksInserted)
	assert.True(t, result.FullScanCompleted)

	require.NoError(t, ctx.V2Manager.DB().Model(&entities.DetectionLock{}).Count(&locked).Error)
	assert.Equal(t, int64(notes-len(unlocked)), locked)
	for _, id := range unlocked {
		isLocked, lockErr := ctx.DetectionRepo.IsLocked(t.Context(), id)
		require.NoError(t, lockErr)
		assert.False(t, isLocked, "detection %d must be unlocked", id)
	}
}

// uintString formats a note id the way the legacy API takes it.
func uintString(id uint) string {
	return strconv.FormatUint(uint64(id), 10)
}

// TestRelatedSync_PredictionRanksSurviveBatchesSplittingDetections reads the
// legacy results in batches smaller than a detection's results, in the copy and
// in the tail, and pins that every detection gets the ranks 2..n once.
func TestRelatedSync_PredictionRanksSurviveBatchesSplittingDetections(t *testing.T) {
	t.Parallel()

	const (
		notes             = 10
		resultsPerNote    = 4
		predictionsBatch  = 3 // splits every detection across batches
		lateNote          = uint(notes + 1)
		resultsInTotal    = notes * resultsPerNote
		secondaryRankBase = 2
	)
	ctx := testutil.SetupIntegrationTest(t)
	ctx.PredictionsBatchSize = predictionsBatch
	ctx.RecreateWorker(t)

	seedResults := func(noteID, firstResultID uint) []datastore.Results {
		rows := make([]datastore.Results, 0, resultsPerNote)
		for j := range uint(resultsPerNote) {
			rows = append(rows, testutil.NewResultsBuilder().
				WithID(firstResultID+j).WithNoteID(noteID).
				WithSpecies("Secondary Species "+string(rune('A'+j))).WithConfidence(0.6).Build())
		}
		return rows
	}
	require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetections(notes)))
	all := make([]datastore.Results, 0, resultsInTotal)
	for id := uint(1); id <= notes; id++ {
		all = append(all, seedResults(id, (id-1)*resultsPerNote+1)...)
	}
	require.NoError(t, ctx.Seeder.SeedResults(all))

	ctx.StartMigration(t, notes)
	ctx.WaitForCompletion(t, 30*time.Second)

	require.NoError(t, ctx.Seeder.SeedDetections(testutil.GenerateDetectionsWithIDOffset(1, lateNote)))
	require.NoError(t, ctx.Seeder.SeedResults(seedResults(lateNote, resultsInTotal+1)))
	stopWorkerWithDrain(t, ctx)

	var predictions []entities.DetectionPrediction
	require.NoError(t, ctx.V2Manager.DB().Order("detection_id ASC, rank ASC").Find(&predictions).Error)
	require.Len(t, predictions, resultsInTotal+resultsPerNote)

	ranks := map[uint][]int{}
	for i := range predictions {
		ranks[predictions[i].DetectionID] = append(ranks[predictions[i].DetectionID], predictions[i].Rank)
	}
	wantRanks := make([]int, 0, resultsPerNote)
	for j := range resultsPerNote {
		wantRanks = append(wantRanks, secondaryRankBase+j)
	}
	for id := uint(1); id <= lateNote; id++ {
		assert.Equal(t, wantRanks, ranks[id], "detection %d", id)
	}
	cursors := relatedCursors(t, ctx)
	assert.Equal(t, lateNote, cursors.PredictionNoteID)
	assert.Equal(t, uint(resultsInTotal+resultsPerNote), cursors.PredictionResultID)
}
