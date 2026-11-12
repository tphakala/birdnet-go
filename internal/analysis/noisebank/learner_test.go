package noisebank

import (
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
)

// learnerHarness is a service with no database, enough to run the embedding loop of
// a job on a real clip. It must be built inside a synctest bubble so the pauses run
// on fake time.
func learnerHarness(t *testing.T, emb *fakeEmbedder, windows int) (*Service, *job) {
	t.Helper()
	svc := NewService(Config{Embedder: emb})
	spec, ok := emb.ModelSpecFor("")
	require.True(t, ok)
	dir := t.TempDir()
	writeTestClip(t, filepath.Join(dir, "clip.wav"), svc.gridFor(&spec), windows)
	j := &job{
		detectionID: 1, clipName: "clip.wav", phase: phaseQueued,
		open: func(name string) (*os.File, error) { return os.Open(filepath.Join(dir, name)) }, //nolint:gosec // G304: test clip in a temp dir
	}
	svc.jobs[j.detectionID] = j
	return svc, j
}

// TestLearn_PausesAfterEveryWindow pins the pacing: consecutive embedder calls are
// separated by at least the larger of the previous call's two reported durations,
// so a saturated model gets a longer break.
func TestLearn_PausesAfterEveryWindow(t *testing.T) {
	tests := []struct {
		name   string
		timing classifier.PredictTiming
		want   time.Duration
	}{
		{"inference time dominates", classifier.PredictTiming{Predict: 400 * time.Millisecond, LockWait: 100 * time.Millisecond}, 400 * time.Millisecond},
		{"lock wait dominates", classifier.PredictTiming{Predict: 100 * time.Millisecond, LockWait: 700 * time.Millisecond}, 700 * time.Millisecond},
		{"no timing reported", classifier.PredictTiming{}, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				emb := newFakeEmbedder()
				emb.timing = tt.timing
				emb.setScores(0.9, 0.9, 0.9, 0.9)
				svc, j := learnerHarness(t, emb, 4)

				_, found, reason := svc.learn(t.Context(), j)

				require.Empty(t, reason)
				assert.Len(t, found, examplesPerClip)
				require.Len(t, emb.times, 4)
				for i := 1; i < len(emb.times); i++ {
					assert.Equal(t, tt.want, emb.times[i].Sub(emb.times[i-1]), "gap before window %d", i)
				}
			})
		})
	}
}

// TestBestCandidates_TiesKeepTheEarlierWindow pins the selection after a window
// was replaced in place: of equal scores the later window is the one evicted, not
// whichever sits in the last slot.
func TestBestCandidates_TiesKeepTheEarlierWindow(t *testing.T) {
	var best bestCandidates
	for _, c := range []candidate{
		{offsetMs: 0, score: 0.9}, {offsetMs: 1, score: 0.6}, {offsetMs: 2, score: 0.8}, {offsetMs: 3, score: 0.9},
		{offsetMs: 4, score: 0.8}, // replaces the 0.6 window, later than the other 0.8
		{offsetMs: 5, score: 0.85},
	} {
		best.offer(c)
	}

	kept := best.sorted()
	offsets := make([]int, 0, len(kept))
	for _, c := range kept {
		offsets = append(offsets, c.offsetMs)
	}
	assert.Equal(t, []int{0, 3, 5, 2}, offsets, "the 0.8 window at offset 4 is evicted, not the earlier one at offset 2")
}

// TestRunJob_TimeoutStoresNothing pins the job time limit: a job that runs past it
// fails with the timeout reason and never reaches its commit.
func TestRunJob_TimeoutStoresNothing(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		emb := newFakeEmbedder()
		emb.timing = classifier.PredictTiming{Predict: learnJobTimeout / 3}
		emb.setScores(0.9, 0.9, 0.9, 0.9, 0.9, 0.9)
		svc, j := learnerHarness(t, emb, 6)

		svc.runJob(j)

		assert.Equal(t, phaseFailed, j.phase)
		assert.Equal(t, ReasonTimeout, j.failure)
		assert.Less(t, emb.callCount(), 6, "the job stopped before the last window")
	})
}

// TestStop_CancelsARunningJob pins that stopping the service ends a job that is
// waiting in the model, and that it stores nothing.
func TestStop_CancelsARunningJob(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9)
	id, unblock := blockedJob(t, e, "clip.wav", 2)

	stopped := make(chan error, 1)
	go func() { stopped <- e.svc.Stop(t.Context()) }()
	unblock()

	require.NoError(t, <-stopped)
	assert.Zero(t, e.rowCount())
	st, err := e.svc.Status(t.Context(), id, nil)
	require.NoError(t, err)
	assert.Equal(t, StateFailed, st.State)
	assert.Equal(t, ReasonCancelled, st.FailureReason)
}
