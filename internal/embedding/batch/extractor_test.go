package batch

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/embedding"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// fakePredict returns a fixed embedding whose first component encodes the
// window index, and a single result with confidence rising per window so
// best-window selection is deterministic.
type fakePredict struct {
	calls   int
	species string
}

func (f *fakePredict) predict(ctx context.Context, window []float32) ([]datastore.Results, []float32, error) {
	f.calls++
	conf := float32(f.calls) * 0.1
	emb := []float32{float32(f.calls), 2, 3, 4}
	return []datastore.Results{{Species: f.species, Confidence: conf}}, emb, nil
}

func newTestStore(t *testing.T) *embedding.Store {
	t.Helper()
	s, err := embedding.NewStore(filepath.Join(t.TempDir(), "emb.db"))
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// stubFFmpegPath is a placeholder; decodeStub never invokes ffmpeg.
const stubFFmpegPath = "unused"

// decodeStub bypasses ffmpeg: emits n windows of silence.
func decodeStub(n int) decodeFunc {
	return func(ctx context.Context, ffmpegPath, filePath string, sampleRate, windowSamples int, fn windowFunc) error {
		w := make([]float32, windowSamples)
		for i := range n {
			if err := fn(w, time.Duration(i)*3*time.Second); err != nil {
				return err
			}
		}
		return nil
	}
}

func newTestExtractor(t *testing.T, fp *fakePredict, store *embedding.Store, windows int, opts Options) *Extractor {
	t.Helper()
	e := New(fp.predict, store, Tags{Model: "BirdNET_V2.4", Version: "2.4", Format: embedding.FormatFP16},
		Spec{SampleRate: 48000, WindowSamples: 48000 * 3}, opts)
	e.decode = decodeStub(windows)
	e.ffmpegPath = stubFFmpegPath
	return e
}

func TestRunDirectoryModePutsPerWindow(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "Turdus merula_Eurasian Blackbird"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 3, Options{})

	stats, err := e.Run(t.Context(), []Item{{Path: "/x/a.wav", Key: "a.wav"}})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Files)
	assert.Equal(t, 3, stats.Records)

	rec, err := store.Get(t.Context(), windowKey("a.wav", 0))
	require.NoError(t, err)
	assert.Equal(t, "BirdNET_V2.4", rec.Model)
	assert.Equal(t, "2.4", rec.Version)
	assert.Equal(t, "file:a.wav", rec.Source)
	assert.Equal(t, 4, rec.Dim)
	_, err = store.Get(t.Context(), windowKey("a.wav", 6))
	require.NoError(t, err)
}

func TestRunBackfillModePutsBestWindow(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "Turdus merula_Eurasian Blackbird"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 3, Options{})

	began := time.Date(2026, 6, 1, 6, 0, 0, 0, time.UTC)
	stats, err := e.Run(t.Context(), []Item{{
		Path: "/x/clip.wav", Key: "clip.wav",
		DetectionID: "42", Species: "Turdus merula",
		Source: "rtsp-cam1", CapturedAt: began,
	}})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Records)

	rec, err := store.Get(t.Context(), "42")
	require.NoError(t, err)
	// Confidence rises per window, so the best window is the last (3rd) call:
	// its embedding first component is 3.
	assert.InDelta(t, 3.0, float64(rec.Vector[0]), 1e-6)
	assert.Equal(t, "rtsp-cam1", rec.Source)
	assert.Equal(t, began, rec.CapturedAt.UTC())
}

func TestRunSkipsExistingUnlessOverwrite(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "S_C"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 2, Options{})

	item := Item{Path: "/x/c.wav", Key: "c.wav", DetectionID: "7", Species: "S"}
	_, err := e.Run(t.Context(), []Item{item})
	require.NoError(t, err)
	callsAfterFirst := fp.calls

	stats, err := e.Run(t.Context(), []Item{item})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Skipped)
	assert.Equal(t, callsAfterFirst, fp.calls, "skip must not run inference")

	e.opts.Overwrite = true
	stats, err = e.Run(t.Context(), []Item{item})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Records)
	assert.Greater(t, fp.calls, callsAfterFirst)
}

func TestRunHonorsLimit(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "S_C"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 1, Options{Limit: 2})

	items := []Item{
		{Path: "/x/1.wav", Key: "1.wav"},
		{Path: "/x/2.wav", Key: "2.wav"},
		{Path: "/x/3.wav", Key: "3.wav"},
	}
	stats, err := e.Run(t.Context(), items)
	require.NoError(t, err)
	assert.Equal(t, 2, stats.Files)
}

func TestRunDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "S_C"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 2, Options{DryRun: true})

	stats, err := e.Run(t.Context(), []Item{{Path: "/x/d.wav", Key: "d.wav"}})
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Records)
	assert.Equal(t, 1, stats.Files)
	_, err = store.Get(t.Context(), windowKey("d.wav", 0))
	assert.ErrorIs(t, err, embedding.ErrNotFound)
}

// reusePredict simulates a model whose returned embedding aliases ONE reused
// backing slice across calls. Confidence is highest on the first window and
// strictly decreases after, so the first window must win best-window
// selection while the buffer keeps being overwritten by later calls.
type reusePredict struct {
	calls int
	buf   []float32
}

func (f *reusePredict) predict(ctx context.Context, window []float32) ([]datastore.Results, []float32, error) {
	f.calls++
	for i := range f.buf {
		f.buf[i] = float32(f.calls * 10)
	}
	conf := 1.0 / float32(f.calls)
	return []datastore.Results{{Species: "S_C", Confidence: conf}}, f.buf, nil
}

func TestRunBackfillCloneSurvivesBufferReuse(t *testing.T) {
	t.Parallel()
	fp := &reusePredict{buf: make([]float32, 4)}
	store := newTestStore(t)
	e := New(fp.predict, store, Tags{Model: "BirdNET_V2.4", Version: "2.4", Format: embedding.FormatFP16},
		Spec{SampleRate: 48000, WindowSamples: 48000 * 3}, Options{})
	e.decode = decodeStub(3)
	e.ffmpegPath = stubFFmpegPath

	_, err := e.Run(t.Context(), []Item{{
		Path: "/x/clip.wav", Key: "clip.wav",
		DetectionID: "9", Species: "S",
	}})
	require.NoError(t, err)

	rec, err := store.Get(t.Context(), "9")
	require.NoError(t, err)
	// The first window won (confidence 1.0), so the stored vector must hold
	// that window's values (10s), not the buffer's final contents (30s).
	for i, v := range rec.Vector {
		assert.InDelta(t, 10.0, float64(v), 1e-6, "component %d aliased the reused buffer", i)
	}
}

func TestRunContextCancelStops(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "S_C"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 1, Options{})

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err := e.Run(ctx, []Item{{Path: "/x/e.wav", Key: "e.wav"}})
	require.ErrorIs(t, err, context.Canceled)
}

func TestRunOnErrorFiresAndRunContinues(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	errBoom := errors.NewStd("predict boom")

	// Fail only the very first predict call so item one errors and item two
	// completes, proving the run continues past a failure.
	failed := false
	predict := func(ctx context.Context, window []float32) ([]datastore.Results, []float32, error) {
		if !failed {
			failed = true
			return nil, nil, errBoom
		}
		return []datastore.Results{{Species: "S_C", Confidence: 0.5}}, []float32{1, 2, 3, 4}, nil
	}

	var gotItems []Item
	var gotErrs []error
	e := New(predict, store, Tags{Model: "M", Version: "1", Format: embedding.FormatFP16},
		Spec{SampleRate: 48000, WindowSamples: 48000 * 3}, Options{
			OnError: func(item Item, err error) {
				gotItems = append(gotItems, item)
				gotErrs = append(gotErrs, err)
			},
		})
	e.decode = decodeStub(1)
	e.ffmpegPath = stubFFmpegPath

	stats, err := e.Run(t.Context(), []Item{
		{Path: "/x/bad.wav", Key: "bad.wav"},
		{Path: "/x/good.wav", Key: "good.wav"},
	})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Errors)
	assert.Equal(t, 1, stats.Files, "run must continue to the next item")

	require.Len(t, gotItems, 1, "OnError must fire exactly once")
	assert.Equal(t, "bad.wav", gotItems[0].Key)
	require.ErrorIs(t, gotErrs[0], errBoom)
}

func TestRunDirectoryModeSkipsExisting(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "S_C"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 2, Options{})

	item := Item{Path: "/x/skip.wav", Key: "skip.wav"}
	_, err := e.Run(t.Context(), []Item{item})
	require.NoError(t, err)
	callsAfterFirst := fp.calls

	// Second run without Overwrite: window 0 already exists, should skip.
	stats, err := e.Run(t.Context(), []Item{item})
	require.NoError(t, err)
	assert.Equal(t, 1, stats.Skipped, "second run must skip the already-embedded item")
	assert.Equal(t, callsAfterFirst, fp.calls, "skip must not run inference")
}

func TestRunBackfillDryRunWritesNothing(t *testing.T) {
	t.Parallel()
	fp := &fakePredict{species: "Turdus merula_Eurasian Blackbird"}
	store := newTestStore(t)
	e := newTestExtractor(t, fp, store, 2, Options{DryRun: true})

	item := Item{
		Path: "/x/dryrun.wav", Key: "dryrun.wav",
		DetectionID: "99", Species: "Turdus merula",
	}
	stats, err := e.Run(t.Context(), []Item{item})
	require.NoError(t, err)
	assert.Equal(t, 0, stats.Records, "dry-run must write zero records")
	assert.Equal(t, 1, stats.Files, "dry-run item must still count as processed")
	_, getErr := store.Get(t.Context(), "99")
	assert.ErrorIs(t, getErr, embedding.ErrNotFound, "store must remain empty after dry-run")
}
