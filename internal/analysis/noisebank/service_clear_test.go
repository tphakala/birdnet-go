package noisebank

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

const clearTestTimeout = 5 * time.Second

// isClearing reports whether the clearing barrier is up.
func (e *testEnv) isClearing() bool {
	e.svc.mu.Lock()
	defer e.svc.mu.Unlock()
	return e.svc.clearing
}

// clearResult is what Clear answered.
type clearResult struct {
	deleted int64
	err     error
}

// startClear runs Clear on its own goroutine and returns what it answers.
func (e *testEnv) startClear(ctx context.Context) <-chan clearResult {
	out := make(chan clearResult, 1)
	go func() {
		n, err := e.svc.Clear(ctx)
		out <- clearResult{n, err}
	}()
	return out
}

// learnedDetached learns a detection and then deletes the detection, leaving
// detached examples behind.
func (e *testEnv) learnedDetached(clip string) {
	e.t.Helper()
	id := e.markedDetection(clip, 2)
	e.markAndLearn(id)
	require.NoError(e.t, e.dets.Delete(e.t.Context(), id))
}

// TestClear drives a clear against a job that is blocked inside the embedder.
func TestClear(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9)
	kept := e.markedDetection("kept.wav", 2)
	e.markAndLearn(kept)
	e.learnedDetached("gone.wav")
	require.NoError(t, e.examples.InsertDetached(t.Context(), []entities.NoiseExample{
		{WindowOffsetMs: 0, EmbeddingSpace: otherSpace, Dim: 1, Vector: encodeVector([]float32{1}), CreatedAt: 1},
	}))
	e.restart() // loads the row of the other space too
	require.Equal(t, 5, e.rowCount())

	busy, unblock := blockedJob(t, e, "busy.wav", 2)
	late := e.markedDetection("late.wav", 2)
	queued := e.markedDetection("queued.wav", 2)
	e.mustMark(queued) // waits in the queue behind the blocked job

	ctx, cancel := context.WithTimeout(t.Context(), clearTestTimeout)
	defer cancel()
	first := e.startClear(ctx)
	e.waitUntil("the clearing barrier", e.isClearing)

	t.Run("a mark during the clear is refused and writes nothing", func(t *testing.T) {
		_, err := e.mark(late)
		var refusal *RefusalError
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, ReasonClearing, refusal.Reason)
		assert.Nil(t, e.review(late))
	})

	t.Run("a verdict change is not held back by the barrier", func(t *testing.T) {
		require.NoError(t, e.svc.LeaveNoise(t.Context(), late, entities.VerificationCorrect))
		review := e.review(late)
		require.NotNil(t, review)
		assert.Equal(t, entities.VerificationCorrect, review.Verified)
		assert.True(t, e.isClearing(), "the clear is still waiting")
	})

	t.Run("a second clear is refused and the barrier stays up", func(t *testing.T) {
		_, err := e.svc.Clear(ctx)
		var refusal *RefusalError
		require.ErrorAs(t, err, &refusal)
		assert.Equal(t, ReasonClearing, refusal.Reason)
		assert.True(t, e.isClearing(), "the refused clear must not lift the first one's barrier")
		select {
		case res := <-first:
			require.FailNow(t, "the first clear finished while its job was still blocked", "%+v", res)
		default:
		}
	})

	unblock()
	res := <-first
	require.NoError(t, res.err)
	assert.Equal(t, int64(5), res.deleted)

	assert.False(t, e.isClearing())
	assert.Zero(t, e.rowCount())
	assert.Zero(t, e.svc.Bank().Len(testSpace))
	e.assertBankMatchesDatabase("after clear")

	t.Run("reviews keep their mark and show no examples", func(t *testing.T) {
		for _, id := range []uint{kept, busy, queued} {
			review := e.review(id)
			require.NotNil(t, review)
			assert.True(t, isMarked(review))
		}
		st := e.status(kept)
		assert.True(t, st.Marked)
		assert.Zero(t, st.Examples)
		assert.Equal(t, StateIdle, st.State, "the finished job is forgotten, not reported as learned")
	})

	t.Run("the running and the queued job stored nothing and are forgotten", func(t *testing.T) {
		for _, id := range []uint{busy, queued} {
			st := e.status(id)
			assert.True(t, st.Marked)
			assert.Zero(t, st.Examples)
			assert.Equal(t, StateIdle, st.State)
		}
	})

	t.Run("a mark after the clear is accepted", func(t *testing.T) {
		e.markAndLearn(late)
		assert.Equal(t, 2, e.attached(late))
	})
}

// TestClear_WaitTimesOut pins the failure path: nothing is deleted, the barrier
// is lifted, and the job that was cancelled reports it and can be marked again.
func TestClear_WaitTimesOut(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9)
	other := e.markedDetection("other.wav", 2)
	e.markAndLearn(other)
	id, unblock := blockedJob(t, e, "busy.wav", 2)

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	deleted, err := e.svc.Clear(ctx)

	require.ErrorIs(t, err, ErrClearTimeout)
	assert.Zero(t, deleted)
	assert.Equal(t, 2, e.rowCount(), "nothing is deleted")
	assert.Equal(t, 2, e.svc.Bank().Len(testSpace))
	assert.False(t, e.isClearing(), "the barrier is lifted")

	unblock()
	st := e.waitState(id, StateFailed)
	assert.Equal(t, ReasonCancelled, st.FailureReason)
	assert.Equal(t, 2, e.rowCount(), "the cancelled job stored nothing")

	e.markAndLearn(id)
	assert.Equal(t, 2, e.attached(id), "the cancelled detection can be marked again")
}

// TestClear_DatabaseFailure lifts the barrier and reports the error; a count
// fails the same way.
func TestClear_DatabaseFailure(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9)
	require.NoError(t, e.db.Migrator().DropTable(&entities.NoiseExample{}))

	_, err := e.svc.Clear(t.Context())

	require.Error(t, err)
	require.NotErrorIs(t, err, ErrClearTimeout)
	assert.False(t, e.isClearing())

	_, err = e.svc.Counts(t.Context())
	require.Error(t, err)
}

// TestCounts reports the rows by embedding space, and every row as current when
// the model is not loaded.
func TestCounts(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9)
	e.markAndLearn(e.markedDetection("a.wav", 2))
	require.NoError(t, e.examples.InsertDetached(t.Context(), []entities.NoiseExample{
		{WindowOffsetMs: 0, EmbeddingSpace: otherSpace, Dim: 1, Vector: encodeVector([]float32{1}), CreatedAt: 1},
	}))

	counts, err := e.svc.Counts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Counts{Examples: 2, InactiveExamples: 1}, counts)

	e.emb.mu.Lock()
	e.emb.loaded = false
	e.emb.mu.Unlock()
	counts, err = e.svc.Counts(t.Context())
	require.NoError(t, err)
	assert.Equal(t, Counts{Examples: 3, Space: SpaceUnknown}, counts)
}

// TestClear_ForgetsAJobTheWorkerHoldsButHasNotStarted covers the moment between
// the worker taking a job off the queue and starting it: the job is no longer
// queued and the worker does not look busy yet, so Clear does not wait for it.
// The job was cancelled with the rest, stores nothing, and its entry goes with
// the other finished ones, so the status of its detection reads the database.
func TestClear_ForgetsAJobTheWorkerHoldsButHasNotStarted(t *testing.T) {
	e := newTestEnv(t)
	id := e.markedDetection("held.wav", 2)
	held := &job{detectionID: id, clipName: "held.wav", open: e.open, phase: phaseQueued}
	e.svc.mu.Lock()
	e.svc.jobs[id] = held
	e.svc.mu.Unlock()

	_, err := e.svc.Clear(t.Context())
	require.NoError(t, err)

	e.svc.mu.Lock()
	_, tracked := e.svc.jobs[id]
	e.svc.mu.Unlock()
	assert.False(t, tracked, "a cancelled job must not outlive the clear in the job table")
	assert.Equal(t, StateIdle, e.status(id).State)
}

// expiredClearAttempts repeats the call because with an idle worker and an ended
// context both outcomes of the wait are ready and the runtime picks either; the
// answer must be the timeout whichever it picks.
const expiredClearAttempts = 40

// TestClear_ExpiredContextIsATimeout pins that a clear whose context has ended is
// reported as the timeout and deletes nothing, also when the wait itself was won
// by an idle worker.
func TestClear_ExpiredContextIsATimeout(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9)
	e.markAndLearn(e.markedDetection("a.wav", 2))
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	for range expiredClearAttempts {
		deleted, err := e.svc.Clear(ctx)

		require.ErrorIs(t, err, ErrClearTimeout)
		assert.Zero(t, deleted)
		require.False(t, e.isClearing(), "the barrier is lifted")
	}
	assert.Equal(t, 2, e.rowCount(), "nothing is deleted")
	assert.Equal(t, 2, e.svc.Bank().Len(testSpace))
}
