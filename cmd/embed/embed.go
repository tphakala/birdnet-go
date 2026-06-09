// Package embed implements the hidden "embed" subcommand: offline batch
// embedding extraction for stored detection clips or arbitrary audio files.
package embed

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/embedding"
	"github.com/tphakala/birdnet-go/internal/embedding/batch"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// defaultBackfillLimit bounds one backfill run unless the operator overrides
// it. Large clip libraries (100 GB+) must never be swept in a single
// invocation; reruns continue incrementally because existing note ids skip.
const defaultBackfillLimit = 1000

// defaultPace is the sleep between inference calls so a concurrently running
// live server keeps inference headroom.
const defaultPace = 100 * time.Millisecond

// progressEvery throttles progress lines to one per this many handled items.
const progressEvery = 25

// Command returns the hidden embed subcommand.
func Command(settings *conf.Settings) *cobra.Command {
	var (
		dir       string
		backfill  bool
		limit     int
		since     string
		species   string
		pace      time.Duration
		overwrite bool
		dryRun    bool
	)
	cmd := &cobra.Command{
		Use:    "embed",
		Short:  "Batch embedding extraction for stored audio (hidden)",
		Hidden: true,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// true when both flags are missing or both are set; exactly one mode is required.
			if (dir == "") == !backfill {
				return errors.NewStd("exactly one of --dir or --backfill is required")
			}
			if backfill && !cmd.Flags().Changed("limit") {
				limit = defaultBackfillLimit
			}
			var sinceTime time.Time
			if since != "" {
				var err error
				sinceTime, err = time.Parse(time.DateOnly, since)
				if err != nil {
					return fmt.Errorf("invalid --since (want YYYY-MM-DD): %w", err)
				}
			}
			return run(cmd.Context(), settings, &runConfig{
				dir: dir, backfill: backfill, limit: limit,
				since: sinceTime, species: species, pace: pace,
				overwrite: overwrite, dryRun: dryRun,
			})
		},
	}
	cmd.Flags().StringVar(&dir, "dir", "", "embed every audio file under this directory")
	cmd.Flags().BoolVar(&backfill, "backfill", false, "embed stored detections that still have a clip on disk")
	cmd.Flags().IntVar(&limit, "limit", 0, "max files this run (backfill default 1000; 0 = unlimited)")
	cmd.Flags().StringVar(&since, "since", "", "backfill only detections on/after this date (YYYY-MM-DD)")
	cmd.Flags().StringVar(&species, "species", "", "backfill only this scientific name")
	cmd.Flags().DurationVar(&pace, "pace", defaultPace, "sleep between inference calls")
	cmd.Flags().BoolVar(&overwrite, "overwrite", false, "re-embed entries that already exist in the store")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "decode and infer but write nothing")
	return cmd
}

// runConfig carries validated flag values into run.
type runConfig struct {
	dir       string
	backfill  bool
	limit     int
	since     time.Time
	species   string
	pace      time.Duration
	overwrite bool
	dryRun    bool
}

func run(ctx context.Context, settings *conf.Settings, cfg *runConfig) error {
	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	lowerPriority() // best effort; logs nothing on failure

	bn, err := classifier.NewOrchestrator(settings)
	if err != nil {
		return fmt.Errorf("model init: %w", err)
	}
	// Safe direct read: the orchestrator is single-owner here and ModelInfo
	// only changes via ReloadModel, which this CLI never calls.
	modelID := bn.ModelInfo.ID
	if bn.ModelEmbeddingDim(modelID) == 0 {
		return fmt.Errorf("model %s cannot extract embeddings; needs an ONNX embeddings model", modelID)
	}
	spec, ok := bn.ModelSpecFor(modelID)
	if !ok {
		return fmt.Errorf("no model spec for %s", modelID)
	}

	ffmpegPath := settings.Realtime.Audio.FfmpegPath
	if ffmpegPath == "" {
		var resolveErr error
		ffmpegPath, resolveErr = ffmpeg.ResolveBinary()
		if resolveErr != nil {
			return fmt.Errorf("ffmpeg required for batch decode: %w", resolveErr)
		}
	}

	storePath := settings.Embeddings.Storage.Path
	if storePath == "" {
		storePath = filepath.Join(filepath.Dir(settings.Output.SQLite.Path), "embeddings.db")
	}
	maxRows := settings.Embeddings.Storage.MaxRows
	if maxRows <= 0 {
		maxRows = int(embedding.DefaultMaxRows)
	}
	store, err := embedding.NewStore(storePath, embedding.WithMaxRows(maxRows))
	if err != nil {
		return fmt.Errorf("open embedding store %s: %w", storePath, err)
	}
	defer func() { _ = store.Close() }()

	var items []batch.Item
	if cfg.backfill {
		ds := datastore.New(settings)
		if err := ds.Open(); err != nil {
			return fmt.Errorf("open datastore: %w", err)
		}
		defer func() { _ = ds.Close() }()
		items, err = batch.BackfillItems(ctx, ds, settings.Realtime.Audio.Export.Path, batch.BackfillFilter{
			Species: cfg.species, Since: cfg.since, Limit: cfg.limit,
		})
	} else {
		items, err = batch.DirectoryItems(cfg.dir)
	}
	if err != nil {
		return err
	}
	fmt.Printf("embed: %d candidate files, model %s (dim %d), store %s\n",
		len(items), modelID, bn.ModelEmbeddingDim(modelID), storePath)

	predict := func(ctx context.Context, window []float32) ([]datastore.Results, []float32, error) {
		return bn.PredictModelWithEmbeddings(ctx, modelID, [][]float32{window})
	}
	ex := batch.New(predict, store,
		batch.Tags{Model: modelID, Version: bn.ModelInfo.DetectionVersion,
			Format: embedding.Format(settings.Embeddings.Storage.Format)},
		batch.Spec{SampleRate: spec.SampleRate,
			WindowSamples: int(int64(spec.SampleRate) * int64(spec.ClipLength) / int64(time.Second))},
		batch.Options{
			Limit: cfg.limit, Pace: cfg.pace,
			Overwrite: cfg.overwrite, DryRun: cfg.dryRun,
			Progress: func(s batch.Stats, _ batch.Item) {
				if (s.Files+s.Skipped+s.Errors)%progressEvery == 0 {
					fmt.Printf("  %d done, %d skipped, %d errors, %d windows\n",
						s.Files, s.Skipped, s.Errors, s.Windows)
				}
			},
			OnError: func(item batch.Item, err error) {
				fmt.Printf("  failed: %s: %v\n", item.Key, err)
			},
		})
	ex.SetFFmpegPath(ffmpegPath)

	stats, err := ex.Run(ctx, items)
	fmt.Printf("embed: files=%d records=%d skipped=%d errors=%d windows=%d\n",
		stats.Files, stats.Records, stats.Skipped, stats.Errors, stats.Windows)
	if err != nil {
		return err
	}
	if _, err := store.Prune(ctx); err != nil {
		return fmt.Errorf("prune: %w", err)
	}
	return nil
}
