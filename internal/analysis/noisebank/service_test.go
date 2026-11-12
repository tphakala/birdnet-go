package noisebank

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestMark_SelectsByRawTopScore pins which windows become examples: those with a
// raw top score of at least the floor, the best four, the earlier window winning
// ties, whatever the model's thresholded results say.
func TestMark_SelectsByRawTopScore(t *testing.T) {
	// offsetOf is the stored offset of window n of the model's grid.
	grid := newTestEnv(t).grid
	offsetOf := func(windows ...int) []int {
		out := make([]int, len(windows))
		for i, n := range windows {
			out[i] = grid.offsetMs(int64(n) * int64(grid.hop))
		}
		return out
	}

	tests := []struct {
		name        string
		windows     int
		scores      []float32
		wantOffsets []int
		wantFailure Reason
	}{
		{
			name:        "ties go to the earlier window",
			windows:     7,
			scores:      []float32{0.3, 0.9, 0.9, 0.9, 0.9, 0.9, 0.5},
			wantOffsets: offsetOf(1, 2, 3, 4),
		},
		{
			name:        "the best four of many",
			windows:     6,
			scores:      []float32{0.6, 0.95, 0.7, 0.99, 0.8, 0.5},
			wantOffsets: offsetOf(1, 2, 3, 4),
		},
		{
			name:        "windows below any live threshold still yield examples",
			windows:     6,
			scores:      []float32{0.55, 0.55, 0.55, 0.55, 0.55, 0.55},
			wantOffsets: offsetOf(0, 1, 2, 3),
		},
		{
			name:        "a window exactly at the floor is stored",
			windows:     2,
			scores:      []float32{0.5, 0.49},
			wantOffsets: offsetOf(0),
		},
		{
			name:        "fewer candidates than the limit",
			windows:     5,
			scores:      []float32{0.1, 0.6, 0.2, 0.7, 0.1},
			wantOffsets: offsetOf(1, 3),
		},
		{
			name:        "all windows below the floor",
			windows:     5,
			scores:      []float32{0.1, 0.49, 0.2, 0.3, 0.1},
			wantFailure: ReasonNoCandidates,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.emb.setScores(tt.scores...)
			id := e.markedDetection("clip.wav", tt.windows)

			e.mustMark(id)
			if tt.wantFailure != "" {
				st := e.waitState(id, StateFailed)
				assert.Equal(t, tt.wantFailure, st.FailureReason)
				assert.Zero(t, e.rowCount(), "a job without candidates stores nothing")
				assert.Equal(t, 0, e.svc.Bank().Len(testSpace))
				return
			}
			st := e.waitState(id, StateLearned)
			assert.Equal(t, tt.wantOffsets, e.offsets(id))
			assert.Equal(t, len(tt.wantOffsets), st.Examples)
			assert.Equal(t, tt.windows, e.emb.callCount(), "every window is embedded")
			assert.Equal(t, len(tt.wantOffsets), e.svc.Bank().Len(testSpace))
			e.assertBankMatchesDatabase("after learning")
			e.assertAllClosed()
		})
	}
}

// TestMark_ResultsDoNotChangeTheExamples pins that the stored rows depend on the
// clip and the model only: the thresholded results of the windows, which move with
// the detection threshold, play no part.
func TestMark_ResultsDoNotChangeTheExamples(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.6, 0.9, 0.7, 0.95, 0.8)
	id := e.markedDetection("clip.wav", 5)

	e.markAndLearn(id)
	first := e.offsets(id)

	require.NoError(t, e.svc.LeaveNoise(t.Context(), id, entities.VerificationFalsePositive))
	require.Zero(t, e.attached(id))
	e.emb.mu.Lock()
	e.emb.results = []datastoreResult{{Species: "Eptesicus nilssonii", Confidence: 0.99}}
	e.emb.mu.Unlock()

	e.markAndLearn(id)
	assert.Equal(t, first, e.offsets(id))
}

// TestMark_GridComesFromTheModelSpec pins the window and hop: the model's clip
// length in samples and the live analysis step, from the spec, not literals.
func TestMark_GridComesFromTheModelSpec(t *testing.T) {
	e := newTestEnv(t)
	spec := classifier.ModelRegistry[classifier.RegistryIDBat].Spec
	overlap := classifier.ResolveModelOverlap(classifier.RegistryIDBat, spec, 0)
	_, _, readBytes := spec.BufferDimensions(overlap)

	assert.Equal(t, spec.ClipSampleCount(), e.grid.window)
	assert.Equal(t, readBytes/2, e.grid.hop, "the hop is the live read size in 16-bit samples")
	assert.Equal(t, spec.EffectiveSampleRate(), e.grid.rate)
	assert.Less(t, e.grid.hop, e.grid.window)

	e.emb.setScores(0.9, 0.9, 0.9, 0.9)
	id := e.markedDetection("clip.wav", 4)
	e.markAndLearn(id)
	e.emb.mu.Lock()
	defer e.emb.mu.Unlock()
	require.Len(t, e.emb.lens, 4)
	for _, n := range e.emb.lens {
		assert.Equal(t, e.grid.window, n)
	}
}

// TestMark_Refusals pins every reason a mark is refused before anything is
// written.
func TestMark_Refusals(t *testing.T) {
	denoised := true
	notDenoised := false
	tests := []struct {
		name   string
		setup  func(t *testing.T, e *testEnv) uint
		reason Reason
	}{
		{"detection without a bat contribution", func(t *testing.T, e *testEnv) uint {
			t.Helper()
			e.writeClip("clip.wav", 3)
			id := e.detection("clip.wav")
			other := entities.AIModel{Name: "Other", Version: "1", ModelType: entities.ModelTypeBird}
			require.NoError(t, e.db.Create(&other).Error)
			require.NoError(t, e.db.Exec("DELETE FROM detection_model_contributions").Error)
			require.NoError(t, e.dets.SaveModelContributions(t.Context(), id, []*entities.DetectionModelContribution{
				{ModelID: other.ID, HitCount: 2, MaxConfidence: 0.9},
			}))
			return id
		}, ReasonModelUnsupported},
		{"model not loaded", func(t *testing.T, e *testEnv) uint {
			t.Helper()
			e.emb.mu.Lock()
			e.emb.loaded = false
			e.emb.mu.Unlock()
			return e.markedDetection("clip.wav", 3)
		}, ReasonModelNotLoaded},
		{"no clip name", func(t *testing.T, e *testEnv) uint { t.Helper(); return e.detection("") }, ReasonClipMissing},
		{"clip file gone", func(t *testing.T, e *testEnv) uint { t.Helper(); return e.detection("gone.wav") }, ReasonClipMissing},
		{"clip that is not audio", func(t *testing.T, e *testEnv) uint {
			t.Helper()
			require.NoError(t, os.WriteFile(filepath.Join(e.clipDir, "junk.wav"), []byte("not audio at all"), 0o600))
			return e.detection("junk.wav")
		}, ReasonClipUnusable},
		{"clip denoised on export", func(t *testing.T, e *testEnv) uint {
			t.Helper()
			id := e.markedDetection("clip.wav", 3)
			require.NoError(t, e.dets.RecordClipDenoised(t.Context(), id, "clip.wav", denoised))
			return id
		}, ReasonClipDenoised},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			id := tt.setup(t, e)

			_, err := e.mark(id)

			refusal, ok := errors.AsType[*RefusalError](err)
			require.True(t, ok, "got %v", err)
			assert.Equal(t, tt.reason, refusal.Reason)
			assert.Nil(t, e.review(id), "a refused mark writes no review")
			assert.Equal(t, 0, e.emb.callCount())
			e.assertAllClosed()

			st := e.status(id)
			assert.False(t, st.Available)
			assert.Equal(t, tt.reason, st.UnavailableReason)
			assert.Equal(t, tt.reason == ReasonClipDenoised, st.ClipDenoised)
		})
	}

	t.Run("a clip marked not denoised or never marked is learned", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		id := e.markedDetection("a.wav", 2)
		require.NoError(t, e.dets.RecordClipDenoised(t.Context(), id, "a.wav", notDenoised))
		e.markAndLearn(id)

		other := e.markedDetection("b.wav", 2)
		e.markAndLearn(other)
	})

	t.Run("a missing detection", func(t *testing.T) {
		e := newTestEnv(t)
		_, err := e.mark(9999)
		require.ErrorIs(t, err, ErrNotFound)
	})

	t.Run("a detection whose model is only known from its row", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9)
		e.writeClip("clip.wav", 2)
		id := e.detection("clip.wav")
		require.NoError(t, e.db.Exec("DELETE FROM detection_model_contributions").Error)
		e.markAndLearn(id)
	})
}

// TestMark_IsIdempotent pins that marking a marked detection with examples, or one
// whose job is in flight, changes nothing and starts no second job.
func TestMark_IsIdempotent(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9, 0.9)
	id := e.markedDetection("clip.wav", 3)

	unblock := e.emb.blockCalls()
	e.mustMark(id)
	e.waitCalls(1)
	st := e.mustMark(id)
	assert.Equal(t, StateRunning, st.State, "a second mark while the job runs only reports it")
	unblock()
	e.waitState(id, StateLearned)
	assert.Equal(t, 3, e.emb.callCount(), "one job ran")

	before := e.review(id).UpdatedAt
	st = e.mustMark(id)
	assert.Equal(t, StateLearned, st.State)
	assert.Equal(t, 3, e.emb.callCount(), "a mark of a learned detection runs nothing")
	assert.Equal(t, before, e.review(id).UpdatedAt, "and writes nothing")
	e.assertAllClosed()
}

// TestStatus_ReportsMarkAndSpaces pins the database derived part of the status:
// the mark, the examples in the current space and the inactive ones in others.
func TestStatus_ReportsMarkAndSpaces(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.8, 0.7)
	id := e.markedDetection("clip.wav", 3)

	st := e.status(id)
	assert.True(t, st.Available)
	assert.False(t, st.Marked)
	assert.Equal(t, StateIdle, st.State)

	st = e.markAndLearn(id)
	assert.True(t, st.Marked)
	assert.Equal(t, 3, st.Examples)
	assert.Zero(t, st.InactiveExamples)
	assert.Equal(t, 3, st.WindowsDone)
	assert.Equal(t, 3, st.WindowsTotal)

	e.emb.setSpace(otherSpace)
	st = e.status(id)
	assert.Zero(t, st.Examples, "examples of another space are not active")
	assert.Equal(t, 3, st.InactiveExamples)
	assert.True(t, st.Marked)
	assert.Equal(t, 3, e.rowCount(), "and they are kept")

	// A restart keeps the mark and the examples: the status comes from the database.
	e.emb.setSpace(testSpace)
	e.restart()
	st = e.status(id)
	assert.True(t, st.Marked)
	assert.Equal(t, 3, st.Examples)
	assert.Equal(t, StateIdle, st.State, "the job table is not persistent")
}

// failingOpener is an opener that must not be called.
func failingOpener(t *testing.T) OpenClipFunc {
	t.Helper()
	return func(string) (*os.File, error) {
		t.Error("the clip was opened")
		return nil, os.ErrNotExist
	}
}

// TestMark_RepeatWinsOverClipAndModelState pins that a mark of a detection that is
// already marked with examples, or whose job is in flight, returns its status
// without opening the clip, whatever happened to the clip or the model since.
func TestMark_RepeatWinsOverClipAndModelState(t *testing.T) {
	t.Run("learned, clip removed by retention", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.8)
		id := e.markedDetection("clip.wav", 2)
		e.markAndLearn(id)
		before := e.review(id).UpdatedAt
		calls := e.emb.callCount()
		require.NoError(t, os.Remove(filepath.Join(e.clipDir, "clip.wav")))

		st, err := e.svc.Mark(t.Context(), MarkRequest{DetectionID: id, OpenClip: failingOpener(t)})

		require.NoError(t, err)
		assert.Equal(t, StateLearned, st.State)
		assert.True(t, st.Marked)
		assert.Equal(t, 2, st.Examples)
		assert.Equal(t, before, e.review(id).UpdatedAt, "nothing is written")
		assert.Equal(t, calls, e.emb.callCount(), "no job runs")
	})

	t.Run("learned, model not loaded", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.8)
		id := e.markedDetection("clip.wav", 2)
		e.markAndLearn(id)
		e.emb.mu.Lock()
		e.emb.loaded = false
		e.emb.mu.Unlock()

		st, err := e.svc.Mark(t.Context(), MarkRequest{DetectionID: id, OpenClip: failingOpener(t)})

		require.NoError(t, err)
		assert.True(t, st.Marked)
		assert.Equal(t, 2, st.InactiveExamples, "with no model the space is unknown and the examples are counted apart")
	})

	t.Run("job in flight, clip removed", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.8)
		id, unblock := blockedJob(t, e, "clip.wav", 2)
		require.NoError(t, os.Remove(filepath.Join(e.clipDir, "clip.wav")))

		st, err := e.svc.Mark(t.Context(), MarkRequest{DetectionID: id, OpenClip: failingOpener(t)})

		require.NoError(t, err)
		assert.Equal(t, StateRunning, st.State)
		unblock()
	})

	t.Run("a mark that is not a repeat is still refused for the clip", func(t *testing.T) {
		e := newTestEnv(t)
		id := e.detection("gone.wav")
		_, err := e.mark(id)
		refusal, ok := errors.AsType[*RefusalError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, ReasonClipMissing, refusal.Reason)
	})
}

// TestMark_RepeatDoesNotBypassTheBarriers pins that a clearing barrier and a delete
// guard refuse a repeat mark too: the repeat is answered only when neither holds.
func TestMark_RepeatDoesNotBypassTheBarriers(t *testing.T) {
	setup := func(t *testing.T) (*testEnv, uint) {
		t.Helper()
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.8)
		id := e.markedDetection("clip.wav", 2)
		e.markAndLearn(id)
		return e, id
	}

	t.Run("clearing", func(t *testing.T) {
		e, id := setup(t)
		e.svc.mu.Lock()
		e.svc.clearing = true
		e.svc.mu.Unlock()

		_, err := e.mark(id)

		refusal, ok := errors.AsType[*RefusalError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, ReasonClearing, refusal.Reason)
	})

	t.Run("delete guard", func(t *testing.T) {
		e, id := setup(t)
		release, ok := e.svc.GuardDelete(id)
		require.True(t, ok)
		defer release()

		_, err := e.mark(id)

		refusal, isRefusal := errors.AsType[*RefusalError](err)
		require.True(t, isRefusal, "got %v", err)
		assert.Equal(t, ReasonDetectionBeingDeleted, refusal.Reason)
	})
}
