// Package imports provides a transport-agnostic engine for importing detections
// from external sources into the birdnet-go datastore.
package imports

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const (
	// DefaultSourceNode is the provenance tag written to SourceNode for imported rows.
	DefaultSourceNode = "birdnet-pi"

	// defaultBatchSize is the number of source rows read per Iterate call.
	defaultBatchSize = 500
)

// SourceDetection holds the raw fields from a source database row.
// Each adapter maps its native schema to this neutral struct.
type SourceDetection struct {
	Date           string
	Time           string
	ScientificName string
	CommonName     string
	Confidence     float64
	Latitude       float64
	Longitude      float64
	Cutoff         float64
	Sensitivity    float64
	FileName       string
}

// Source is the interface a source adapter must implement.
type Source interface {
	// Validate confirms the source is readable and has the expected schema.
	Validate(ctx context.Context) error

	// Count returns the total number of rows in the source.
	Count(ctx context.Context) (int, error)

	// Iterate streams rows in batches ordered by the source's internal row order.
	// fn is called once per batch; returning an error from fn stops iteration.
	Iterate(ctx context.Context, batchSize int, fn func([]SourceDetection) error) error

	// Close releases the source's resources.
	Close() error
}

// ImportStats tracks progress and outcome of an import run.
type ImportStats struct {
	Total     int
	Processed int
	Inserted  int
	Skipped   int
	Errors    int
	Phase     string
}

// ProgressReporter receives periodic ImportStats updates from the engine.
// A nil reporter is safe; the engine performs a nil check before calling.
type ProgressReporter interface {
	Report(ImportStats)
}

// ImportOptions controls engine behaviour.
type ImportOptions struct {
	// SourceNode is the provenance tag written to every imported detection.
	// Defaults to DefaultSourceNode.
	SourceNode string

	// Location is the timezone used when parsing Date + Time strings.
	// Defaults to time.Local.
	Location *time.Location

	// BatchSize controls how many source rows are read per Iterate call.
	// Defaults to defaultBatchSize.
	BatchSize int

	// IncludeAudio controls whether source audio files are copied alongside detection data.
	// When true, audio clips are copied from AudioSourceDir into ClipExportPath alongside detection data.
	IncludeAudio bool

	// AudioSourceDir is the directory containing the BirdNET-Pi source audio tree.
	// It must contain an "Extracted/By_Date" subtree. Used only when IncludeAudio is true.
	AudioSourceDir string

	// ClipExportPath is the root directory where audio clips are written.
	// Used only when IncludeAudio is true.
	ClipExportPath string

	// DiskSpaceFunc is called to check available disk space before audio copying.
	// If nil, diskmanager.GetAvailableSpace is used. Inject in tests to avoid
	// filesystem dependencies.
	DiskSpaceFunc func(path string) (uint64, error)
}

func (o *ImportOptions) withDefaults() {
	if o.SourceNode == "" {
		o.SourceNode = DefaultSourceNode
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaultBatchSize
	}
}

// Engine runs an import from a Source into a DetectionRepository.
type Engine struct {
	repo datastore.DetectionRepository
	log  logger.Logger
}

// NewEngine creates a new Engine.
// repo is used both for saving new detections and for querying existing ones.
func NewEngine(repo datastore.DetectionRepository) *Engine {
	return &Engine{
		repo: repo,
		log:  logger.Global().Module("imports"),
	}
}

// detectionKey returns a stable composite key for duplicate detection.
// Components: wall-clock timestamp (timezone-independent), scientific name, confidence rounded to 4 decimal places.
// Using the wall-clock representation ensures idempotency even when opts.Location differs
// from the timezone used during read-back (the datastore reconstructs Timestamp from
// the stored Date/Time strings in its own timezone).
func detectionKey(ts time.Time, scientificName string, confidence float64) string {
	return fmt.Sprintf("%s|%s|%.4f", ts.Format(time.DateTime), scientificName, confidence)
}

// Run imports all detections from src that are not already present in the store.
// It returns a final ImportStats summary. Partial results are consistent because
// the dedup set makes a re-run safe.
// opts may be nil; a nil pointer is replaced with a zero-value ImportOptions so
// callers are not required to allocate one.
func (e *Engine) Run(ctx context.Context, src Source, opts *ImportOptions, reporter ProgressReporter) (ImportStats, error) {
	if opts == nil {
		opts = &ImportOptions{}
	}
	opts.withDefaults()

	stats := ImportStats{Phase: "validate"}
	e.report(reporter, stats)

	if err := src.Validate(ctx); err != nil {
		return stats, errors.New(err).
			Component("imports").
			Category(errors.CategoryValidation).
			Context("phase", "validate").
			Build()
	}

	total, err := src.Count(ctx)
	if err != nil {
		return stats, errors.New(err).
			Component("imports").
			Category(errors.CategoryDatabase).
			Context("phase", "count").
			Build()
	}
	stats.Total = total
	stats.Phase = "dedup"
	e.report(reporter, stats)

	// Pre-load existing import-source keys to avoid per-row queries.
	seen, err := e.loadExistingKeys(ctx, opts.SourceNode)
	if err != nil {
		return stats, errors.New(err).
			Component("imports").
			Category(errors.CategoryDatabase).
			Context("phase", "dedup").
			Context("source_node", opts.SourceNode).
			Build()
	}

	e.log.Info("starting import",
		logger.String("source_node", opts.SourceNode),
		logger.Int("total_rows", total),
		logger.Int("existing_keys", len(seen)))

	stats.Phase = "import"
	e.report(reporter, stats)

	iterErr := src.Iterate(ctx, opts.BatchSize, func(batch []SourceDetection) error {
		// First pass: classify rows into pending (new) vs. skipped/errored.
		// Keys are added to seen eagerly so within-batch duplicates are caught here.
		type pendingRow struct {
			row *SourceDetection
			ts  time.Time
		}
		pending := make([]pendingRow, 0, len(batch))

		for i := range batch {
			if err := ctx.Err(); err != nil {
				return err
			}
			row := &batch[i]
			ts, parseErr := parseTimestamp(row.Date, row.Time, opts.Location)
			if parseErr != nil {
				e.log.Debug("skipping row with unparseable timestamp",
					logger.String("date", row.Date),
					logger.String("time", row.Time),
					logger.Error(parseErr))
				stats.Errors++
				stats.Processed++
				continue
			}
			key := detectionKey(ts, row.ScientificName, row.Confidence)
			if _, ok := seen[key]; ok {
				stats.Skipped++
				stats.Processed++
				continue
			}
			// Mark seen eagerly to deduplicate within-batch duplicates.
			seen[key] = struct{}{}
			pending = append(pending, pendingRow{row: row, ts: ts})
		}

		// Second pass: copy audio clips when requested.
		clipNames := make([]string, len(pending))
		if opts.IncludeAudio && opts.ClipExportPath != "" && len(pending) > 0 {
			rows := make([]SourceDetection, len(pending))
			tss := make([]time.Time, len(pending))
			for i, p := range pending {
				rows[i] = *p.row
				tss[i] = p.ts
			}
			if err := e.copyAudioBatch(ctx, opts, rows, tss, clipNames); err != nil {
				return err
			}
		}

		// Third pass: save detections with clip names resolved above.
		for i, p := range pending {
			if err := ctx.Err(); err != nil {
				return err
			}
			result := mapToResult(p.row, p.ts, opts.SourceNode)
			result.ClipName = clipNames[i]
			if saveErr := e.repo.Save(ctx, result, nil); saveErr != nil {
				if ctx.Err() != nil {
					// Context was cancelled or timed out during Save; propagate
					// the context error so the caller gets a clean abort signal.
					return ctx.Err()
				}
				e.log.Error("failed to save detection",
					logger.String("scientific_name", p.row.ScientificName),
					logger.Error(saveErr))
				stats.Errors++
				stats.Processed++
				continue
			}
			stats.Inserted++
			stats.Processed++
		}

		e.report(reporter, stats)
		return nil
	})

	if iterErr != nil {
		if ctx.Err() != nil {
			e.log.Info("import cancelled",
				logger.Int("inserted", stats.Inserted),
				logger.Int("skipped", stats.Skipped))
			return stats, iterErr
		}
		// A disk-space failure from the audio pre-check is already a fully built
		// error with the disk-usage category. Pass it through unchanged rather than
		// re-wrapping it as a database error, so telemetry and category stay correct.
		if errors.IsCategory(iterErr, errors.CategoryDiskUsage) {
			return stats, iterErr
		}
		return stats, errors.New(iterErr).
			Component("imports").
			Category(errors.CategoryDatabase).
			Context("phase", "import").
			Build()
	}

	stats.Phase = "done"
	e.report(reporter, stats)

	e.log.Info("import complete",
		logger.Int("total", stats.Total),
		logger.Int("inserted", stats.Inserted),
		logger.Int("skipped", stats.Skipped),
		logger.Int("errors", stats.Errors))

	return stats, nil
}

// copyAudioBatch creates the export directory, checks disk space, and copies all
// audio clips for one pending batch. clipNames is updated in-place.
func (e *Engine) copyAudioBatch(ctx context.Context, opts *ImportOptions, rows []SourceDetection, tss []time.Time, clipNames []string) error {
	// Ensure export directory exists before the disk-space check so that
	// GetAvailableSpace does not error on a freshly configured path.
	if mkErr := os.MkdirAll(opts.ClipExportPath, 0o755); mkErr != nil {
		return errors.New(mkErr).
			Component("imports").
			Category(errors.CategoryFileIO).
			Context("operation", "mkdir_export_path").
			Context("path", opts.ClipExportPath).
			Build()
	}

	// Disk-space guard: ensure the export volume can hold this batch's clips
	// before copying any of them.
	requiredBytes := sumSourceClipSizes(opts.AudioSourceDir, rows)
	if requiredBytes > 0 {
		if spaceErr := checkDiskSpace(opts.ClipExportPath, requiredBytes, opts.DiskSpaceFunc); spaceErr != nil {
			return spaceErr
		}
	}

	missCount := 0
	e.copyCandidateClips(ctx, opts, rows, tss, clipNames, &missCount)
	if missCount > 0 {
		e.log.Info("some audio clips could not be copied",
			logger.Int("miss_count", missCount),
			logger.Int("batch_size", len(rows)))
	}
	return nil
}

// loadExistingKeys pages through all detections attributed to sourceNode
// and returns their dedup keys in a set.
func (e *Engine) loadExistingKeys(ctx context.Context, sourceNode string) (map[string]struct{}, error) {
	const pageSize = 1000
	seen := make(map[string]struct{})
	var minID uint

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		filters := datastore.NewDetectionFilters().
			WithMinID(minID).
			WithLimit(pageSize)
		filters.Location = []string{sourceNode}

		results, _, err := e.repo.Search(ctx, filters)
		if err != nil {
			return nil, errors.New(err).
				Component("imports").
				Category(errors.CategoryDatabase).
				Context("operation", "load_existing_keys").
				Build()
		}

		for _, r := range results {
			key := detectionKey(r.Timestamp, r.Species.ScientificName, r.Confidence)
			seen[key] = struct{}{}
			if r.ID > minID {
				minID = r.ID
			}
		}

		if len(results) < pageSize {
			break
		}
	}

	return seen, nil
}

// parseTimestamp parses "YYYY-MM-DD HH:MM:SS" in loc.
func parseTimestamp(date, timeStr string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", date+" "+timeStr, loc)
}

// mapToResult converts a SourceDetection to a detection.Result.
//
// Field mapping decisions:
//   - BeginTime and EndTime are left as zero values: BirdNET-Pi stores detections
//     as point-in-time events without clip offsets, so there is no timing data to map.
//   - ClipName: DB-only imports leave ClipName empty; DB+audio imports set it before
//     Save when a matching source clip is found. An empty ClipName avoids broken
//     "audio not available" links.
//   - Model is set to a synthetic marker so imported rows are distinguishable from
//     live detections in queries or analytics.
//   - Provenance is carried solely by SourceNode (a persisted column). AudioSource is
//     gorm:"-" runtime-only and is not persisted on the legacy save path, so it is left
//     zero rather than used as a provenance marker.
//   - Week and Overlap from BirdNET-Pi are dropped; birdnet-go does not use them.
func mapToResult(row *SourceDetection, ts time.Time, sourceNode string) *detection.Result {
	return &detection.Result{
		Timestamp:  ts,
		SourceNode: sourceNode,
		Species: detection.Species{
			ScientificName: row.ScientificName,
			CommonName:     row.CommonName,
			Code:           "",
		},
		Confidence:  row.Confidence,
		Latitude:    row.Latitude,
		Longitude:   row.Longitude,
		Threshold:   row.Cutoff,
		Sensitivity: row.Sensitivity,
		Model: detection.ModelInfo{
			Name:    "BirdNET",
			Version: "birdnet-pi",
			Variant: "import",
		},
		ClipName: "",
	}
}

// report calls reporter.Report if reporter is not nil.
func (e *Engine) report(reporter ProgressReporter, stats ImportStats) {
	if reporter != nil {
		reporter.Report(stats)
	}
}
