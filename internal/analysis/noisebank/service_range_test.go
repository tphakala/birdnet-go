package noisebank

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// samplesToSeconds converts a sample count of the test clips to seconds.
func (e *testEnv) samplesToSeconds(samples int64) float64 {
	return float64(samples) / float64(e.grid.rate)
}

// rangeOfWindows is the range that starts with window first of the grid and ends
// with window last, so those windows and the ones between lie wholly inside it.
func (e *testEnv) rangeOfWindows(first, last int) *ClipRange {
	return &ClipRange{
		Start: e.samplesToSeconds(int64(first) * int64(e.grid.hop)),
		End:   e.samplesToSeconds(int64(last)*int64(e.grid.hop) + int64(e.grid.window)),
	}
}

// offsetsOfWindows returns the stored offsets of the given windows of the grid.
func (e *testEnv) offsetsOfWindows(windows ...int) []int {
	out := make([]int, len(windows))
	for i, n := range windows {
		out[i] = e.grid.offsetMs(int64(n) * int64(e.grid.hop))
	}
	return out
}

func (e *testEnv) markRange(id uint, r *ClipRange) (Status, error) {
	e.t.Helper()
	return e.svc.Mark(e.t.Context(), MarkRequest{DetectionID: id, OpenClip: e.open, Range: r})
}

// markRangeAndLearn marks with a range and waits for that job to store.
func (e *testEnv) markRangeAndLearn(id uint, r *ClipRange) Status {
	e.t.Helper()
	st, err := e.markRange(id, r)
	require.NoError(e.t, err)
	require.Equal(e.t, StateQueued, st.State, "the mark started a job")
	return e.waitState(id, StateLearned)
}

func TestMark_RangeEmbedsOnlyWholeGridWindows(t *testing.T) {
	const windows = 8
	allHigh := []float32{0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9}
	// nudge moves a range end by less than one hop, off the grid lines.
	nudge := func(e *testEnv) float64 { return e.samplesToSeconds(int64(e.grid.hop) / 4) }

	tests := []struct {
		name         string
		rng          func(e *testEnv) *ClipRange
		wantWindows  []int // windows whose examples are stored
		wantEmbedded int   // windows the model ran on
	}{
		{
			name:        "a range that matches windows 2 to 5",
			rng:         func(e *testEnv) *ClipRange { return e.rangeOfWindows(2, 5) },
			wantWindows: []int{2, 3, 4, 5}, wantEmbedded: 4,
		},
		{
			name: "a start inside window 2 leaves it out",
			rng: func(e *testEnv) *ClipRange {
				r := e.rangeOfWindows(2, 5)
				r.Start += nudge(e)
				return r
			},
			wantWindows: []int{3, 4, 5}, wantEmbedded: 3,
		},
		{
			name: "an end inside window 5 leaves it out",
			rng: func(e *testEnv) *ClipRange {
				r := e.rangeOfWindows(2, 5)
				r.End -= nudge(e)
				return r
			},
			wantWindows: []int{2, 3, 4}, wantEmbedded: 3,
		},
		{
			name:        "more windows than the limit keep the earliest of equal scores",
			rng:         func(e *testEnv) *ClipRange { return e.rangeOfWindows(1, 6) },
			wantWindows: []int{1, 2, 3, 4}, wantEmbedded: 6,
		},
		{
			name: "an end past the clip still uses the windows that exist",
			rng: func(e *testEnv) *ClipRange {
				r := e.rangeOfWindows(5, windows-1)
				r.End += 100
				return r
			},
			wantWindows: []int{5, 6, 7}, wantEmbedded: 3,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			e.emb.setScores(allHigh...)
			id := e.markedDetection("clip.wav", windows)

			st := e.markRangeAndLearn(id, tt.rng(e))

			assert.Equal(t, e.offsetsOfWindows(tt.wantWindows...), e.offsets(id))
			assert.Equal(t, len(tt.wantWindows), st.Examples)
			assert.Equal(t, tt.wantEmbedded, e.emb.callCount(), "only the windows inside the range are embedded")
			assert.Equal(t, tt.wantEmbedded, st.WindowsTotal, "progress counts the windows inside the range")
			assert.Equal(t, tt.wantEmbedded, st.WindowsDone)
			e.assertBankMatchesDatabase("after a range")
			e.assertAllClosed()
		})
	}
}

func TestMark_SecondRangeReplacesTheFirst(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9, 0.9)
	id := e.markedDetection("clip.wav", 10)

	first := e.rangeOfWindows(0, 3)
	st := e.markRangeAndLearn(id, first)
	require.Equal(t, e.offsetsOfWindows(0, 1, 2, 3), e.offsets(id))
	assert.Equal(t, first, st.Range, "the status reports the range of the job")

	second := e.rangeOfWindows(6, 9)
	st = e.markRangeAndLearn(id, second)

	assert.Equal(t, e.offsetsOfWindows(6, 7, 8, 9), e.offsets(id), "the first range's examples are gone")
	assert.Equal(t, 4, st.Examples)
	assert.Equal(t, second, st.Range)
	assert.Equal(t, 4, e.svc.Bank().Len(testSpace))
	e.assertBankMatchesDatabase("after the second range")
	e.assertAllClosed()
}

func TestMark_RangeWithoutCandidatesKeepsEarlierExamples(t *testing.T) {
	e := newTestEnv(t)
	// Windows 0 to 3 score high; windows 4 to 7 stay under the floor.
	e.emb.setScores(0.9, 0.9, 0.9, 0.9, 0.2, 0.49, 0.1, 0.3)
	id := e.markedDetection("clip.wav", 8)
	e.markAndLearn(id)
	before := e.offsets(id)
	require.Len(t, before, 4)

	_, err := e.markRange(id, e.rangeOfWindows(4, 7))
	require.NoError(t, err)
	st := e.waitState(id, StateFailed)

	assert.Equal(t, ReasonNoCandidates, st.FailureReason)
	assert.Equal(t, before, e.offsets(id), "the earlier examples stay")
	assert.Equal(t, 4, st.Examples)
	assert.Equal(t, 4, e.svc.Bank().Len(testSpace))
	e.assertBankMatchesDatabase("after a range without candidates")
}

func TestMark_RangeRelearnsWhereAMarkWithoutOneIsANoop(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9, 0.9, 0.9, 0.9, 0.9)
	id := e.markedDetection("clip.wav", 6)
	e.markAndLearn(id)
	require.Equal(t, 6, e.emb.callCount())

	st := e.mustMark(id)
	assert.Equal(t, StateLearned, st.State, "without a range a learned detection is left alone")
	assert.Equal(t, 6, e.emb.callCount())

	e.markRangeAndLearn(id, e.rangeOfWindows(2, 3))
	assert.Equal(t, 8, e.emb.callCount(), "with a range it relearns, and embeds only the range")
	assert.Equal(t, e.offsetsOfWindows(2, 3), e.offsets(id))
}

func TestMark_RangeDuringAJobOnlyReportsIt(t *testing.T) {
	e := newTestEnv(t)
	e.emb.setScores(0.9, 0.9, 0.9, 0.9)
	id, unblock := blockedJob(t, e, "clip.wav", 4)

	st, err := e.markRange(id, e.rangeOfWindows(1, 2))

	require.NoError(t, err)
	assert.Equal(t, StateRunning, st.State)
	assert.Nil(t, st.Range, "the running job has no range")
	unblock()
	e.waitState(id, StateLearned)
	assert.Equal(t, 4, e.emb.callCount(), "no second job ran")
	assert.Len(t, e.offsets(id), 4)
}

func TestMark_RangeRefusals(t *testing.T) {
	// A clip of 8 windows ends at windowSamples + 7 hops.
	tests := []struct {
		name   string
		rng    func(e *testEnv) *ClipRange
		reason Reason
	}{
		{
			name: "shorter than a window",
			rng: func(e *testEnv) *ClipRange {
				return &ClipRange{Start: 0, End: e.samplesToSeconds(int64(e.grid.window)) - 0.01}
			},
			reason: ReasonRangeTooShort,
		},
		{
			name: "long enough but between grid lines",
			rng: func(e *testEnv) *ClipRange {
				// One window long, starting half a hop off the grid: no whole window fits.
				start := e.samplesToSeconds(int64(e.grid.hop) / 2)
				return &ClipRange{Start: start, End: start + e.samplesToSeconds(int64(e.grid.window))}
			},
			reason: ReasonRangeTooShort,
		},
		{
			name: "starts at the end of the clip",
			rng: func(e *testEnv) *ClipRange {
				end := e.samplesToSeconds(int64(e.grid.window) + 7*int64(e.grid.hop))
				return &ClipRange{Start: end, End: end + 5}
			},
			reason: ReasonRangeOutsideClip,
		},
		{
			name: "starts past the end of the clip",
			rng: func(e *testEnv) *ClipRange {
				return &ClipRange{Start: 60, End: 70}
			},
			reason: ReasonRangeOutsideClip,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := newTestEnv(t)
			id := e.markedDetection("clip.wav", 8)

			_, err := e.markRange(id, tt.rng(e))

			refusal, ok := errors.AsType[*RefusalError](err)
			require.True(t, ok, "got %v", err)
			assert.Equal(t, tt.reason, refusal.Reason)
			assert.Nil(t, e.review(id), "a refused mark writes nothing")
			assert.Zero(t, e.emb.callCount())
			e.assertAllClosed()
		})
	}

	t.Run("a range relearn still needs the clip", func(t *testing.T) {
		e := newTestEnv(t)
		e.emb.setScores(0.9, 0.9, 0.9)
		id := e.markedDetection("clip.wav", 3)
		e.markAndLearn(id)
		require.NoError(t, os.Remove(filepath.Join(e.clipDir, "clip.wav")))

		_, err := e.markRange(id, e.rangeOfWindows(0, 1))

		refusal, ok := errors.AsType[*RefusalError](err)
		require.True(t, ok, "got %v", err)
		assert.Equal(t, ReasonClipMissing, refusal.Reason)
		assert.Equal(t, 3, e.attached(id), "the examples stay")
	})
}
