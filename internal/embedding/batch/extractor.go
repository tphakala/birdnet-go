package batch

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/embedding"
	"github.com/tphakala/birdnet-go/internal/errors"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// PredictFunc runs one analysis window through the embedding-capable model.
// The window slice is reused between calls and must not be retained.
type PredictFunc func(ctx context.Context, window []float32) ([]datastore.Results, []float32, error)

// StoreAPI is the slice of embedding.Store the extractor needs.
type StoreAPI interface {
	Put(ctx context.Context, rec *embedding.Record) error
	Get(ctx context.Context, detectionID string) (embedding.Record, error)
}

// decodeFunc matches decodeWindows; swapped out in tests.
type decodeFunc func(ctx context.Context, ffmpegPath, filePath string, sampleRate, windowSamples int, fn windowFunc) error

// Item is one unit of batch work: an audio file plus optional detection
// identity. When DetectionID is set (backfill) the extractor stores one
// record for the whole file, keyed by DetectionID, using the window that
// best matches Species. When DetectionID is empty (directory corpus) it
// stores one record per window under the synthetic key "<Key>@<offsetSec>".
type Item struct {
	Path        string    // absolute path to the audio file
	Key         string    // stable corpus-relative key (directory mode)
	DetectionID string    // datastore note id (backfill mode), "" otherwise
	Species     string    // scientific name for best-window match (backfill)
	Source      string    // Record.Source override; defaults to "file:<Key>"
	CapturedAt  time.Time // Record.CapturedAt override; defaults to now per run
}

// Tags carry the embedding-space partition: model id, version, vector format.
type Tags struct {
	Model   string
	Version string
	Format  embedding.Format
}

// Spec is the per-model audio geometry.
type Spec struct {
	SampleRate    int
	WindowSamples int
}

// Options bound and shape a run. Zero values mean: no limit, no pacing,
// skip existing, write records.
type Options struct {
	Limit     int           // max files processed this run; 0 = unlimited
	Pace      time.Duration // sleep between inference calls
	Overwrite bool          // re-embed even when the key already exists
	DryRun    bool          // decode + predict but never write
	Progress  func(stats Stats, item Item)
}

// Stats summarize a run.
type Stats struct {
	Files   int // files fully processed (excludes skipped)
	Windows int // inference calls made
	Records int // records written
	Skipped int // items skipped because already embedded
	Errors  int // items that failed (decode or predict); run continues
}

// Extractor drives decode -> predict -> put for a sequence of items.
type Extractor struct {
	predict    PredictFunc
	store      StoreAPI
	tags       Tags
	spec       Spec
	opts       Options
	decode     decodeFunc
	ffmpegPath string
	now        func() time.Time
}

// New builds an Extractor. Callers must call SetFFmpegPath before Run when
// using the real decoder.
func New(predict PredictFunc, store StoreAPI, tags Tags, spec Spec, opts Options) *Extractor {
	return &Extractor{
		predict: predict,
		store:   store,
		tags:    tags,
		spec:    spec,
		opts:    opts,
		decode:  decodeWindows,
		now:     time.Now,
	}
}

// SetFFmpegPath sets the resolved ffmpeg binary used for decoding.
func (e *Extractor) SetFFmpegPath(path string) { e.ffmpegPath = path }

// Run processes items in order until the list, the limit, or the context is
// exhausted. Per-item failures are counted; they do not abort the run.
// Context cancellation returns promptly with the context error.
func (e *Extractor) Run(ctx context.Context, items []Item) (Stats, error) {
	var stats Stats
	for _, item := range items {
		if err := ctx.Err(); err != nil {
			return stats, err
		}
		if e.opts.Limit > 0 && stats.Files+stats.Errors >= e.opts.Limit {
			break
		}
		skipped, err := e.processItem(ctx, &item, &stats)
		switch {
		case errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded):
			return stats, err
		case err != nil:
			stats.Errors++
		case skipped:
			stats.Skipped++
		default:
			stats.Files++
		}
		if e.opts.Progress != nil {
			e.opts.Progress(stats, item)
		}
	}
	return stats, nil
}

func (e *Extractor) processItem(ctx context.Context, item *Item, stats *Stats) (skipped bool, err error) {
	backfill := item.DetectionID != ""

	if backfill && !e.opts.Overwrite {
		if _, err := e.store.Get(ctx, item.DetectionID); err == nil {
			return true, nil
		} else if !errors.Is(err, embedding.ErrNotFound) {
			return false, err
		}
	}
	// Directory mode skip: probe the first window key only; a partially
	// embedded file is re-run (Put is an idempotent upsert).
	if !backfill && !e.opts.Overwrite {
		if _, err := e.store.Get(ctx, windowKey(item.Key, 0)); err == nil {
			return true, nil
		} else if !errors.Is(err, embedding.ErrNotFound) {
			return false, err
		}
	}

	capturedAt := item.CapturedAt
	if capturedAt.IsZero() {
		capturedAt = e.now()
	}
	source := item.Source
	if source == "" {
		source = "file:" + item.Key
	}

	var best struct {
		conf   float32
		vector []float32
		found  bool
	}

	decodeErr := e.decode(ctx, e.ffmpegPath, item.Path, e.spec.SampleRate, e.spec.WindowSamples,
		func(window []float32, offset time.Duration) error {
			if err := ctx.Err(); err != nil {
				return err
			}
			results, emb, err := e.predict(ctx, window)
			if err != nil {
				return err
			}
			stats.Windows++
			if e.opts.Pace > 0 {
				select {
				case <-ctx.Done():
					return ctx.Err()
				case <-time.After(e.opts.Pace):
				}
			}
			if len(emb) == 0 {
				return errEmbeddingUnavailable
			}
			if backfill {
				conf := bestConfidenceFor(results, item.Species)
				if !best.found || conf > best.conf {
					best.conf = conf
					// Clone: the next predict call may reuse the backing array.
					best.vector = slices.Clone(emb)
					best.found = true
				}
				return nil
			}
			// Directory mode: one record per window.
			if e.opts.DryRun {
				return nil
			}
			putErr := e.store.Put(ctx, &embedding.Record{
				DetectionID: windowKey(item.Key, int(offset.Seconds())),
				Model:       e.tags.Model,
				Source:      source,
				CapturedAt:  capturedAt,
				Format:      e.tags.Format,
				Dim:         len(emb),
				Version:     e.tags.Version,
				Vector:      emb,
			})
			if putErr != nil {
				return putErr
			}
			stats.Records++
			return nil
		})
	if decodeErr != nil {
		return false, decodeErr
	}

	if !backfill {
		return false, nil
	}

	if !best.found {
		return false, fmt.Errorf("no usable window in %s", item.Path)
	}
	if e.opts.DryRun {
		return false, nil
	}
	if err := e.store.Put(ctx, &embedding.Record{
		DetectionID: item.DetectionID,
		Model:       e.tags.Model,
		Source:      source,
		CapturedAt:  capturedAt,
		Format:      e.tags.Format,
		Dim:         len(best.vector),
		Version:     e.tags.Version,
		Vector:      best.vector,
	}); err != nil {
		return false, err
	}
	stats.Records++
	return false, nil
}

// errEmbeddingUnavailable is returned when the model produced an empty
// embedding vector; this indicates the model lacks an embedding path.
var errEmbeddingUnavailable = errors.NewStd("model returned no embedding; needs an ONNX embeddings model")

func windowKey(key string, offsetSeconds int) string {
	return fmt.Sprintf("%s@%d", key, offsetSeconds)
}

// bestConfidenceFor returns the highest confidence among results whose label
// matches the scientific name (labels are "Scientific_Common"). Falls back
// to the overall top confidence when no label matches, so clips whose
// re-analysis disagrees with the stored detection still embed something.
func bestConfidenceFor(results []datastore.Results, scientific string) float32 {
	var matched, top float32
	matchedFound := false
	for i := range results {
		c := results[i].Confidence
		if c > top {
			top = c
		}
		label := results[i].Species
		if sci, _, ok := strings.Cut(label, "_"); ok && strings.EqualFold(sci, scientific) {
			if c > matched {
				matched = c
			}
			matchedFound = true
		}
	}
	if matchedFound {
		return matched
	}
	return top
}
