// Package imports provides a transport-agnostic engine for importing detections
// from external sources into the birdnet-go datastore.
package imports

import (
	"context"
	"fmt"
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

	// Iterate streams rows in batches ordered by Date, Time.
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

	// IncludeAudio is reserved for B2 (audio copy). Ignored in B1.
	IncludeAudio bool
}

func (o ImportOptions) withDefaults() ImportOptions {
	if o.SourceNode == "" {
		o.SourceNode = DefaultSourceNode
	}
	if o.Location == nil {
		o.Location = time.Local
	}
	if o.BatchSize <= 0 {
		o.BatchSize = defaultBatchSize
	}
	return o
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
func (e *Engine) Run(ctx context.Context, src Source, opts ImportOptions, reporter ProgressReporter) (ImportStats, error) {
	opts = opts.withDefaults()

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
			if seen[key] {
				stats.Skipped++
				stats.Processed++
				continue
			}

			result := mapToResult(row, ts, opts.SourceNode)
			if saveErr := e.repo.Save(ctx, result, nil); saveErr != nil {
				if ctx.Err() != nil {
					// Context was cancelled or timed out during Save; propagate
					// the context error so the caller gets a clean abort signal.
					return ctx.Err()
				}
				e.log.Error("failed to save detection",
					logger.String("scientific_name", row.ScientificName),
					logger.Error(saveErr))
				stats.Errors++
				stats.Processed++
				continue
			}

			// Mark as seen so within-source duplicates are also deduplicated.
			seen[key] = true
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

// loadExistingKeys pages through all detections attributed to sourceNode
// and returns their dedup keys in a set.
func (e *Engine) loadExistingKeys(ctx context.Context, sourceNode string) (map[string]bool, error) {
	const pageSize = 1000
	seen := make(map[string]bool)
	var minID uint

	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}

		filters := datastore.NewDetectionFilters().
			WithMinID(minID)
		filters.Location = []string{sourceNode}
		filters.Limit = pageSize

		results, _, err := e.repo.Search(ctx, filters)
		if err != nil {
			return nil, fmt.Errorf("querying existing detections: %w", err)
		}

		for _, r := range results {
			key := detectionKey(r.Timestamp, r.Species.ScientificName, r.Confidence)
			seen[key] = true
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
//   - ClipName is left empty in DB-only mode (B1); B2 will set it when audio is copied.
//     An empty ClipName avoids broken "audio not available" links.
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
