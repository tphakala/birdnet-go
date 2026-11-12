package noisebank

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// blockedJob starts a job for a new detection and holds it inside the embedder
// until the returned function is called. It waits for a call made by that job,
// so it also works in an environment whose embedder was already used.
func blockedJob(t *testing.T, e *testEnv, clip string, windows int) (id uint, unblock func()) {
	t.Helper()
	id = e.markedDetection(clip, windows)
	unblock = e.emb.blockCalls()
	t.Cleanup(unblock)
	before := e.emb.callCount()
	e.mustMark(id)
	e.waitCalls(before + 1)
	return id, unblock
}

func assertNoNoise(t *testing.T, e *testEnv, id uint) {
	t.Helper()
	assert.Zero(t, e.attached(id), "no rows attached to the detection")
	assert.Zero(t, e.svc.Bank().Len(testSpace), "no bank entry")
	review := e.review(id)
	require.NotNil(t, review)
	assert.Nil(t, review.Reason, "no reason")
}

// TestRace_LeaveNoiseAgainstCommit drives LeaveNoise against the commit of the
// job's examples in both orders.
func TestRace_LeaveNoiseAgainstCommit(t *testing.T) {
	t.Run("LeaveNoise first: the commit sees the cancellation and stores nothing", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id, unblock := blockedJob(t, e, "clip.wav", 2)

		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))
		unblock()

		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonCancelled, st.FailureReason)
		assertNoNoise(t, e, id)
		e.assertBankMatchesDatabase("leave before commit")
		e.assertAllClosed()
	})

	t.Run("the commit first: LeaveNoise deletes what it stored", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id, unblock := blockedJob(t, e, "clip.wav", 2)
		unblock()
		e.waitState(id, StateLearned)
		require.Equal(t, 2, e.attached(id))

		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))

		assertNoNoise(t, e, id)
		e.assertBankMatchesDatabase("leave after commit")
	})
}

// TestRace_MarkAgainstLeaveNoise covers a verdict change and a mark of the same
// detection arriving together. They serialise on the service mutex, so the end
// state is that of whichever came last, never a verdict of correct with active
// examples.
func TestRace_MarkAgainstLeaveNoise(t *testing.T) {
	t.Run("the mark first, then the verdict", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id, unblock := blockedJob(t, e, "clip.wav", 2)

		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))
		unblock()
		st := e.waitState(id, StateFailed)

		assert.Equal(t, ReasonCancelled, st.FailureReason, "the mark's job was cancelled")
		assert.Equal(t, entities.VerificationCorrect, e.review(id).Verified)
		assertNoNoise(t, e, id)
	})

	t.Run("the verdict first, then the mark", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id := e.markedDetection("clip.wav", 2)

		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))
		e.markAndLearn(id)

		review := e.review(id)
		assert.Equal(t, entities.VerificationFalsePositive, review.Verified)
		require.NotNil(t, review.Reason)
		assert.Equal(t, 2, e.attached(id))
		e.assertBankMatchesDatabase("mark after verdict")
	})
}

// TestRace_MarkRightAfterLeaveNoise pins that a cancelled job that is still
// finishing its window does not block a new mark: the review is marked again, a
// new job runs and stores its examples, and the cancelled job stores nothing.
func TestRace_MarkRightAfterLeaveNoise(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8, 0.7)
	id, unblock := blockedJob(t, e, "clip.wav", 3)

	require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationFalsePositive))
	st, err := e.mark(id)
	require.NoError(t, err, "a cancelled job does not count as in flight")
	assert.Equal(t, StateQueued, st.State)
	review := e.review(id)
	require.NotNil(t, review.Reason, "the review is marked again")

	unblock()
	st = e.waitState(id, StateLearned)

	assert.Equal(t, 3, st.Examples, "the new job stored its examples")
	assert.Equal(t, 3, e.attached(id), "and the cancelled one stored none")
	e.assertBankMatchesDatabase("remark")
	e.assertAllClosed()
}

// TestRace_MarkRefusalsWriteNothing pins that a mark refused for a full queue, a
// full bank, a clearing barrier or a delete guard writes no review.
func TestRace_MarkRefusalsWriteNothing(t *testing.T) {
	t.Run("full queue", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		e.writeClip("clip.wav", 2)
		unblock := e.emb.blockCalls()
		t.Cleanup(unblock)
		// One job runs and maxQueuedJobs wait.
		first := e.detection("clip.wav")
		e.mustMark(first)
		e.waitCalls(1)
		for range maxQueuedJobs {
			e.mustMark(e.detection("clip.wav"))
		}

		extra := e.detection("clip.wav")
		_, err := e.mark(extra)

		require.ErrorIs(t, err, ErrBusy)
		assert.Nil(t, e.review(extra))
		e.assertAllClosed()
	})

	t.Run("bank at its cap", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		id := e.markedDetection("clip.wav", 2)
		fillBank(e, MaxExamplesPerSpace-examplesPerClip+1)

		_, err := e.mark(id)

		refusal, ok := errAsRefusal(err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, ReasonBankFull, refusal.Reason)
		assert.Nil(t, e.review(id))
		assert.Equal(t, 0, e.emb.callCount())
		st := e.status(id)
		assert.False(t, st.Available)
		assert.Equal(t, ReasonBankFull, st.UnavailableReason)

		// One example less leaves room for this clip.
		e.svc.Bank().Load(nil)
		fillBank(e, MaxExamplesPerSpace-examplesPerClip)
		e.markAndLearn(id)
	})

	t.Run("jobs in flight count against the cap", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		fillBank(e, MaxExamplesPerSpace-examplesPerClip)
		unblock := e.emb.blockCalls()
		t.Cleanup(unblock)
		first := e.markedDetection("a.wav", 2)
		second := e.markedDetection("b.wav", 2)

		e.mustMark(first)
		_, err := e.mark(second)

		refusal, ok := errAsRefusal(err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, ReasonBankFull, refusal.Reason, "the first job's examples are already spoken for")
		assert.Nil(t, e.review(second))
	})

	t.Run("a cancelled job does not count against the cap", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		fillBank(e, MaxExamplesPerSpace-examplesPerClip)
		first, unblock := blockedJob(t, e, "a.wav", 2)
		second := e.markedDetection("b.wav", 2)
		require.NoError(t, e.svc.LeaveNoise(t.Context(), first, entities.VerificationCorrect))

		e.mustMark(second)
		unblock()
		e.waitState(second, StateLearned)
	})

	t.Run("clearing barrier", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.markedDetection("clip.wav", 2)
		e.svc.mu.Lock()
		e.svc.clearing = true
		e.svc.mu.Unlock()

		_, err := e.mark(id)

		refusal, ok := errAsRefusal(err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, ReasonClearing, refusal.Reason)
		assert.Nil(t, e.review(id))
		e.assertAllClosed()
	})

	t.Run("delete guard", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.markedDetection("clip.wav", 2)
		release, ok := e.svc.GuardDelete(id)
		require.True(t, ok)

		_, err := e.mark(id)

		refusal, isRefusal := errAsRefusal(err)
		require.True(t, isRefusal, "got %v", err)
		assert.Equal(t, ReasonDetectionBeingDeleted, refusal.Reason)
		assert.Nil(t, e.review(id))

		release()
		release() // a second call is harmless
		e.emb.setScores(0.9)
		e.markAndLearn(id)
	})
}

// fillBank puts n detached examples of the current space into the bank.
func fillBank(e *testEnv, n int) {
	vectors := make([][]float32, n)
	for i := range vectors {
		vectors[i] = basis(i)
	}
	e.svc.Bank().Add(testSpace, vectors)
}

func errAsRefusal(err error) (*RefusalError, bool) {
	return errors.AsType[*RefusalError](err)
}

// TestGuardDelete pins the delete guard: it refuses while a job is queued or
// running, admits a delete once the job ended or was cancelled, and detaches the
// bank entries of a detection that is gone when it is released.
func TestGuardDelete(t *testing.T) {
	t.Run("refused while a job runs, then allowed", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id, unblock := blockedJob(t, e, "clip.wav", 2)

		release, ok := e.svc.GuardDelete(id)
		assert.False(t, ok)
		assert.Nil(t, release)

		unblock()
		e.waitState(id, StateLearned)
		release, ok = e.svc.GuardDelete(id)
		require.True(t, ok)
		release()
	})

	t.Run("refused while a job is queued", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		_, _ = blockedJob(t, e, "a.wav", 2)
		queued := e.markedDetection("b.wav", 2)
		e.mustMark(queued)

		_, ok := e.svc.GuardDelete(queued)
		assert.False(t, ok)
	})

	t.Run("a cancelled job does not hold the delete", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		id, _ := blockedJob(t, e, "clip.wav", 2)
		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))

		release, ok := e.svc.GuardDelete(id)
		require.True(t, ok, "the cancelled job stores nothing and needs no file")
		release()
	})

	t.Run("a failed delete keeps the bank owner", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id := e.markedDetection("clip.wav", 2)
		e.markAndLearn(id)

		release, ok := e.svc.GuardDelete(id)
		require.True(t, ok)
		release() // the delete did not happen

		e.assertBankMatchesDatabase("guard released without a delete")
		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))
		assert.Zero(t, e.svc.Bank().Len(testSpace), "the detection still owns its examples")
	})
}

// TestRace_JobOutlivesItsMark pins the commit rules for a detection that changed
// under a running job.
func TestRace_JobOutlivesItsMark(t *testing.T) {
	t.Run("another writer removed the mark", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id, unblock := blockedJob(t, e, "clip.wav", 2)
		require.NoError(t, e.dets.UpdateReview(t.Context(), id, entities.VerificationCorrect))

		unblock()

		st := e.waitState(id, StateFailed)
		assert.Equal(t, ReasonCancelled, st.FailureReason)
		assert.Zero(t, e.rowCount())
		assert.Zero(t, e.svc.Bank().Len(testSpace))
	})

	t.Run("the detection vanished: the examples are stored detached", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id, unblock := blockedJob(t, e, "clip.wav", 2)
		require.NoError(t, e.dets.Delete(t.Context(), id), "a deleter outside the API")

		unblock()
		e.waitUntil("the detached commit", func() bool { return e.rowCount() == 2 })

		assert.Zero(t, e.attached(id), "the rows have no owner")
		assert.Equal(t, 2, e.svc.Bank().Len(testSpace))
		e.assertBankMatchesDatabase("detached commit")

		// A later detection with the same ID cannot reach them.
		clip := "later.wav"
		e.writeClip(clip, 2)
		require.NoError(t, e.dets.SaveWithID(t.Context(), &entities.Detection{
			ID: id, LabelID: e.label.ID, ModelID: e.model.ID, DetectedAt: 3000, Confidence: 0.7, ClipName: &clip,
		}))
		require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationCorrect))
		assert.Equal(t, 2, e.svc.Bank().Len(testSpace))
		assert.Equal(t, 2, e.rowCount())
	})
}

// TestService_Opener pins file handle ownership: the preflight opens and closes
// once, the job opens once when it runs and closes, and a job cancelled while
// queued opens nothing.
func TestService_Opener(t *testing.T) {
	t.Run("exactly one open for the preflight and one for the job", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		id := e.markedDetection("clip.wav", 2)
		e.mustMark(id)
		e.waitUntil("the job", func() bool { return e.emb.callCount() >= 2 })
		e.waitUntil("the commit", func() bool { return e.attached(id) > 0 })
		assert.Equal(t, 2, e.openCount())
		e.assertAllClosed()
	})

	t.Run("cancelled while queued", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9)
		_, unblock := blockedJob(t, e, "a.wav", 2)
		queued := e.markedDetection("b.wav", 2)
		e.mustMark(queued)
		opensBefore := e.openCount()
		require.NoError(t, e.svc.LeaveNoise(t.Context(), queued, entities.VerificationCorrect))

		unblock()
		st := e.waitState(queued, StateFailed)

		assert.Equal(t, ReasonCancelled, st.FailureReason)
		assert.Equal(t, opensBefore, e.openCount(), "the cancelled job opened no file")
		e.assertAllClosed()
	})
}

// TestCommit_RefusesACancelledOrSupersededJob pins the commit rule: only the
// current, uncancelled job of a detection stores its examples.
func TestCommit_RefusesACancelledOrSupersededJob(t *testing.T) {
	found := []candidate{{offsetMs: 0, score: 0.9, vector: basis(0)}}
	for name, arrange := range map[string]func(s *Service, j *job){
		"cancelled":  func(_ *Service, j *job) { j.cancelled = true },
		"superseded": func(s *Service, j *job) { s.jobs[j.detectionID] = &job{detectionID: j.detectionID} },
	} {
		t.Run(name, func(t *testing.T) {
			e := newTestEnv(t)
			id := e.markedDetection("clip.wav", 2)
			rev := entities.ReviewReasonNoAnimal
			require.NoError(t, e.dets.SaveReview(t.Context(), &entities.DetectionReview{DetectionID: id, Verified: entities.VerificationFalsePositive, Reason: &rev}))

			j := &job{detectionID: id, phase: phaseRunning}
			e.svc.mu.Lock()
			e.svc.jobs[id] = j
			arrange(e.svc, j)
			e.svc.mu.Unlock()

			e.svc.commit(j, testSpace, found)

			assert.Zero(t, e.attached(id), "a job without the right to store stores nothing")
			assert.Equal(t, phaseFailed, j.phase)
			assert.Equal(t, ReasonCancelled, j.failure)
		})
	}
}
