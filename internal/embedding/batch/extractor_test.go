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
	e.ffmpegPath = "unused"
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

	rec, err := store.Get(t.Context(), "a.wav@0")
	require.NoError(t, err)
	assert.Equal(t, "BirdNET_V2.4", rec.Model)
	assert.Equal(t, "2.4", rec.Version)
	assert.Equal(t, "file:a.wav", rec.Source)
	assert.Equal(t, 4, rec.Dim)
	_, err = store.Get(t.Context(), "a.wav@6")
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
	_, err = store.Get(t.Context(), "d.wav@0")
	assert.ErrorIs(t, err, embedding.ErrNotFound)
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
