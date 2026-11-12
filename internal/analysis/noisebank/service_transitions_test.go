package noisebank

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// deleteDetection deletes a detection the way the API does: under the delete
// guard, which is released after the delete.
func (e *testEnv) deleteDetection(id uint) {
	e.t.Helper()
	release, ok := e.svc.GuardDelete(id)
	require.True(e.t, ok)
	defer release()
	require.NoError(e.t, e.dets.Delete(e.t.Context(), id))
}

// TestService_ExamplesOutliveTheirDetection walks the lifetime transitions: learn,
// restart, delete the detection, restart, then a new detection that gets the same
// ID. After every step the bank must equal what a fresh start would load.
func TestService_ExamplesOutliveTheirDetection(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8, 0.7, 0.6)
	id := e.markedDetection("clip.wav", 4)

	e.markAndLearn(id)
	e.assertBankMatchesDatabase("learned")
	learned := dumpBank(e.svc.Bank())
	require.Len(t, learned, 4)

	e.restart()
	assert.Equal(t, learned, dumpBank(e.svc.Bank()), "a restart rebuilds the same bank")

	e.deleteDetection(id)
	assert.Equal(t, 4, e.rowCount(), "deleting the detection removes no example")
	assert.Zero(t, e.attached(id), "the link is cleared by the database")
	e.assertBankMatchesDatabase("after the delete")
	sim, ok := e.svc.Bank().Match(testSpace, basis(0))
	require.True(t, ok)
	assert.InDelta(t, 1, sim, similarityTolerance, "the examples still match")

	e.restart()
	e.assertBankMatchesDatabase("after the delete and a restart")
	assert.Equal(t, 4, e.svc.Bank().Len(testSpace))

	// A rebuilt table can hand the same ID to a new detection.
	clip := "reused.wav"
	e.writeClip(clip, 4)
	require.NoError(t, e.dets.SaveWithID(t.Context(), &entities.Detection{
		ID: id, LabelID: e.label.ID, ModelID: e.model.ID, DetectedAt: 2000, Confidence: 0.8, ClipName: &clip,
	}))
	require.NoError(t, e.dets.SaveModelContributions(t.Context(), id, []*entities.DetectionModelContribution{
		{ModelID: e.model.ID, HitCount: 1, MaxConfidence: 0.8},
	}))
	st := e.status(id)
	assert.Zero(t, st.Examples, "the new detection owns nothing")
	assert.False(t, st.Marked)

	st = e.markAndLearn(id)
	assert.Equal(t, 4, st.Examples)
	assert.Equal(t, 8, e.svc.Bank().Len(testSpace), "its own rows join the older detached ones")
	e.assertBankMatchesDatabase("reused ID learned")

	require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))
	assert.Equal(t, 4, e.rowCount(), "only its own rows are removed")
	assert.Equal(t, 4, e.svc.Bank().Len(testSpace), "and only its own bank entries")
	e.assertBankMatchesDatabase("reused ID left noise")
	e.assertAllClosed()
}

// TestService_ClipRemovedKeepsExamples pins that retention or a manual removal of
// the clip changes nothing: the examples stay attached and active.
func TestService_ClipRemovedKeepsExamples(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8)
	id := e.markedDetection("clip.wav", 2)
	e.markAndLearn(id)

	require.NoError(t, os.Remove(filepath.Join(e.clipDir, "clip.wav")))

	st := e.status(id)
	assert.Equal(t, 2, st.Examples)
	assert.True(t, st.Marked)
	assert.False(t, st.Available)
	assert.Equal(t, ReasonClipMissing, st.UnavailableReason)
	e.assertBankMatchesDatabase("clip removed")
}

// TestService_StartDetachesDanglingLinks pins the startup step: a delete made with
// foreign keys off leaves a dangling link, the start clears it and removes no row.
func TestService_StartDetachesDanglingLinks(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8)
	id := e.markedDetection("clip.wav", 2)
	e.markAndLearn(id)

	require.NoError(t, e.db.Connection(func(tx *gorm.DB) error {
		require.NoError(t, tx.Exec("PRAGMA foreign_keys = OFF").Error)
		require.NoError(t, tx.Exec("DELETE FROM detections WHERE id = ?", id).Error)
		return tx.Exec("PRAGMA foreign_keys = ON").Error
	}))
	require.Equal(t, 2, e.attached(id), "the link dangles")

	e.restart()

	assert.Zero(t, e.attached(id))
	assert.Equal(t, 2, e.rowCount(), "no row is removed")
	assert.Equal(t, 2, e.svc.Bank().Len(testSpace))
	e.assertBankMatchesDatabase("after the start")
}

// TestService_LeaveNoise pins the verdicts that take a detection out of noise: the
// examples, the reason and the bank entries go together, for a marked detection
// and for one that was never marked, and they stay gone after a restart.
func TestService_LeaveNoise(t *testing.T) {
	for _, verdict := range []entities.VerificationStatus{entities.VerificationCorrect, entities.VerificationFalsePositive} {
		t.Run("marked then "+string(verdict), func(t *testing.T) {
			e := newTestEnv(t)
			e.emb.setScores(0.9, 0.8, 0.7)
			id := e.markedDetection("clip.wav", 3)
			other := e.markedDetection("other.wav", 3)
			e.markAndLearn(id)
			e.markAndLearn(other)

			require.NoError(t, e.svc.LeaveNoise(t.Context(), id, verdict))

			review := e.review(id)
			require.NotNil(t, review)
			assert.Equal(t, verdict, review.Verified)
			assert.Nil(t, review.Reason)
			assert.Zero(t, e.attached(id))
			assert.Equal(t, 3, e.attached(other), "another detection's examples are untouched")
			assert.Equal(t, 3, e.svc.Bank().Len(testSpace))
			st := e.status(id)
			assert.False(t, st.Marked)
			assert.Equal(t, StateIdle, st.State, "a finished job is forgotten once the detection leaves noise")
			e.assertBankMatchesDatabase("left noise")

			e.restart()
			assert.Equal(t, 3, e.svc.Bank().Len(testSpace), "still absent after a restart")
		})
	}

	t.Run("a detection that was never marked", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.detection("")

		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))

		review := e.review(id)
		require.NotNil(t, review)
		assert.Equal(t, entities.VerificationCorrect, review.Verified)
		assert.Nil(t, review.Reason)
		assert.Zero(t, e.rowCount())

		// A second save over an existing review changes only the status.
		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationFalsePositive))
		assert.Equal(t, entities.VerificationFalsePositive, e.review(id).Verified)
	})

	t.Run("an unknown verdict is refused", func(t *testing.T) {
		e := newTestEnv(t)
		err := e.svc.LeaveNoise(t.Context(), e.detection(""), entities.VerificationStatus("maybe"))
		require.ErrorIs(t, err, ErrInvalidVerdict)
	})

	t.Run("a caller that went away still gets the cleanup", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.8)
		id := e.markedDetection("clip.wav", 2)
		e.markAndLearn(id)
		ctx, cancel := context.WithCancel(t.Context())
		cancel()

		require.NoError(t, e.svc.LeaveNoise(ctx, id, entities.VerificationCorrect))

		assert.Zero(t, e.attached(id))
		assert.Equal(t, entities.VerificationCorrect, e.review(id).Verified)
	})
}

// TestService_TwoValueSaveKeepsTheMark pins that a save that keeps false_positive
// and names no reason, as the review modal does, leaves the reason, the examples
// and the bank alone.
func TestService_TwoValueSaveKeepsTheMark(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8)
	id := e.markedDetection("clip.wav", 2)
	e.markAndLearn(id)

	require.NoError(t, e.dets.SaveReview(t.Context(), &entities.DetectionReview{
		DetectionID: id, Verified: entities.VerificationFalsePositive,
	}))

	st := e.status(id)
	assert.True(t, st.Marked)
	assert.Equal(t, 2, st.Examples)
	e.assertBankMatchesDatabase("two-value save")
}

// failingLeave is an example repository whose transaction for a verdict fails.
type failingLeave struct {
	repository.NoiseExampleRepository
}

var errLeaveFailed = errors.NewStd("leave failed")

func (failingLeave) RemoveForVerdict(context.Context, uint, entities.VerificationStatus) (int64, error) {
	return 0, errLeaveFailed
}

// TestService_LeaveNoiseFailureChangesNothing pins that a failed transaction leaves
// the examples, the review and the bank as they were and returns the error.
func TestService_LeaveNoiseFailureChangesNothing(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8)
	id := e.markedDetection("clip.wav", 2)
	e.markAndLearn(id)
	before := dumpBank(e.svc.Bank())

	ctx, cancel := e.stopContext()
	defer cancel()
	require.NoError(t, e.svc.Stop(ctx))
	e.svc = e.start(NewService(Config{Embedder: e.emb, Examples: failingLeave{e.examples}, Detections: e.dets}))

	err := e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect)

	require.ErrorIs(t, err, errLeaveFailed)
	assert.Equal(t, 2, e.attached(id))
	assert.Equal(t, entities.VerificationFalsePositive, e.review(id).Verified)
	require.NotNil(t, e.review(id).Reason)
	assert.Equal(t, before, dumpBank(e.svc.Bank()))
}

// TestService_JobOutcomes pins the ways a job ends without learning: nothing is
// stored and the status says why.
func TestService_JobOutcomes(t *testing.T) {
	t.Run("the model changes its embedding space during the job", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9, 0.9)
		e.emb.spaceAt = func(call int) string {
			if call >= 2 {
				return otherSpace
			}
			return testSpace
		}
		id := e.markedDetection("clip.wav", 3)
		e.mustMark(id)
		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonModelChanged, st.FailureReason)
		assert.Zero(t, e.rowCount())
		e.assertAllClosed()
	})

	t.Run("the model fails on a window", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9, 0.9)
		e.emb.failAt = map[int]error{1: errors.NewStd("inference failed")}
		id := e.markedDetection("clip.wav", 3)
		e.mustMark(id)
		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonInferenceFailed, st.FailureReason)
		assert.Zero(t, e.rowCount())
	})

	t.Run("the clip is gone when the job starts", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		id := e.markedDetection("clip.wav", 2)
		unblock := e.emb.blockCalls()
		defer unblock()
		blocker := e.markedDetection("blocker.wav", 2)
		e.mustMark(blocker)
		e.waitCalls(1)
		e.mustMark(id)
		require.NoError(t, os.Remove(filepath.Join(e.clipDir, "clip.wav")))
		unblock()

		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonClipMissing, st.FailureReason)
		assert.Equal(t, 0, e.attached(id))
		e.assertAllClosed()
	})

	t.Run("the clip cannot be decoded when the job starts", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		id := e.markedDetection("clip.wav", 2)
		unblock := e.emb.blockCalls()
		defer unblock()
		blocker := e.markedDetection("blocker.wav", 2)
		e.mustMark(blocker)
		e.waitCalls(1)
		e.mustMark(id)
		require.NoError(t, os.WriteFile(filepath.Join(e.clipDir, "clip.wav"), []byte("garbage"), 0o600))
		unblock()

		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonDecodeFailed, st.FailureReason)
		e.assertAllClosed()
	})
}

// failingStore is an example repository with injectable failures.
type failingStore struct {
	repository.NoiseExampleRepository
	replaceErr error
	listErr    error
}

func (f failingStore) ReplaceForDetection(ctx context.Context, id uint, rows []entities.NoiseExample) error {
	if f.replaceErr != nil {
		return f.replaceErr
	}
	return f.NoiseExampleRepository.ReplaceForDetection(ctx, id, rows)
}

func (f failingStore) ListAll(ctx context.Context) ([]entities.NoiseExample, error) {
	if f.listErr != nil {
		return nil, f.listErr
	}
	return f.NoiseExampleRepository.ListAll(ctx)
}

// withStore replaces the service by one over the same database whose examples go
// through store.
func (e *testEnv) withStore(store repository.NoiseExampleRepository) {
	e.t.Helper()
	ctx, cancel := e.stopContext()
	defer cancel()
	require.NoError(e.t, e.svc.Stop(ctx))
	e.svc = e.start(NewService(Config{Embedder: e.emb, Examples: store, Detections: e.dets}))
}

// TestService_JobFailuresInTheModelAndTheStore pins the remaining ways a job ends
// without learning, and that the worker keeps serving after each.
func TestService_JobFailuresInTheModelAndTheStore(t *testing.T) {
	t.Run("the examples cannot be written", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		e.withStore(failingStore{NoiseExampleRepository: e.examples, replaceErr: errors.NewStd("disk full")})
		id := e.markedDetection("clip.wav", 2)

		e.mustMark(id)

		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonStoreFailed, st.FailureReason)
		assert.Zero(t, e.rowCount())
		assert.Zero(t, e.svc.Bank().Len(testSpace), "the bank is not ahead of the table")
	})

	t.Run("the model returns no embedding", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		e.emb.mu.Lock()
		e.emb.noVector = true
		e.emb.mu.Unlock()
		id := e.markedDetection("clip.wav", 2)

		e.mustMark(id)

		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonModelChanged, st.FailureReason)
	})

	t.Run("the model panics and the worker carries on", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		e.emb.panicAt = map[int]bool{0: true}
		id := e.markedDetection("clip.wav", 2)
		e.mustMark(id)
		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonInferenceFailed, st.FailureReason)

		e.emb.mu.Lock()
		e.emb.panicAt = nil
		e.emb.mu.Unlock()
		other := e.markedDetection("other.wav", 2)
		e.markAndLearn(other)
	})
}

// TestService_Start pins the startup load: stored rows with a malformed vector are
// skipped and kept, a failing load is returned, and nothing is deleted.
func TestService_Start(t *testing.T) {
	t.Run("a malformed row is skipped and kept", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id := e.markedDetection("clip.wav", 2)
		e.markAndLearn(id)
		require.NoError(t, e.examples.InsertDetached(t.Context(), []entities.NoiseExample{
			{WindowOffsetMs: 0, EmbeddingSpace: testSpace, Dim: testDim, Vector: []byte{1, 2, 3}, CreatedAt: 1},
		}))

		e.restart()

		assert.Equal(t, 2, e.svc.Bank().Len(testSpace))
		assert.Equal(t, 3, e.rowCount(), "the malformed row stays in the table")
	})

	t.Run("a failing load is returned", func(t *testing.T) {
		e := newTestEnv(t)
		svc := NewService(Config{Embedder: e.emb, Examples: failingStore{NoiseExampleRepository: e.examples, listErr: errors.NewStd("unreadable")}, Detections: e.dets})
		require.Error(t, svc.Start(t.Context()))
	})
}

// TestService_DeleteForgetsTheJob pins that a deleted detection leaves no job
// behind: a later detection that gets the same ID, in the same run, starts idle.
func TestService_DeleteForgetsTheJob(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8)
	id := e.markedDetection("clip.wav", 2)
	e.markAndLearn(id)

	e.deleteDetection(id)
	clip := "reused.wav"
	e.writeClip(clip, 2)
	require.NoError(t, e.dets.SaveWithID(t.Context(), &entities.Detection{
		ID: id, LabelID: e.label.ID, ModelID: e.model.ID, DetectedAt: 2000, Confidence: 0.8, ClipName: &clip,
	}))
	require.NoError(t, e.dets.SaveModelContributions(t.Context(), id, []*entities.DetectionModelContribution{
		{ModelID: e.model.ID, HitCount: 1, MaxConfidence: 0.8},
	}))

	st := e.status(id)
	assert.Equal(t, StateIdle, st.State, "the old job's state is not inherited")
	assert.Zero(t, st.WindowsTotal)
}

// TestService_LeaveNoiseFailureCancelsTheRunningJob pins what a failed verdict does
// to a job in flight: the database and the bank are untouched, the job is
// cancelled, the detection stays marked, and a new mark relearns.
func TestService_LeaveNoiseFailureCancelsTheRunningJob(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8, 0.7)
	e.withStore(failingLeave{e.examples})
	id, unblock := blockedJob(t, e, "clip.wav", 3)

	err := e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect)
	require.ErrorIs(t, err, errLeaveFailed)
	unblock()

	st := e.waitState(id, StateFailed)
	assert.Equal(t, ReasonCancelled, st.FailureReason)
	assert.True(t, st.Marked, "the review was not changed")
	assert.Zero(t, st.Examples)

	st = e.markAndLearn(id)
	assert.Equal(t, 3, st.Examples, "a new mark relearns")
}
