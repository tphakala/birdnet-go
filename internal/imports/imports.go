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
	// DefaultSourceNode is the provenance tag set as SourceNode on imported rows.
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
	// Reattached counts existing detections that were stored clip-less (for
	// example by a db-only import) and got a clip attached during this db-audio
	// run. Their source rows are also counted in Skipped: no new detection row
	// is inserted for them.
	Reattached int
	Errors     int
	Phase      string
}

// ProgressReporter receives periodic ImportStats updates from the engine.
// A nil reporter is safe; the engine performs a nil check before calling.
type ProgressReporter interface {
	Report(ImportStats)
}

// ImportOptions controls engine behaviour.
type ImportOptions struct {
	// SourceNode is the provenance tag set on every imported detection. Only the
	// legacy store persists it (see mapToResult).
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
	// sumClipSizes estimates the source bytes a batch's clips need; the
	// disk-space guard in copyAudioBatch charges exactly its result. It is a
	// field so tests can observe the estimate's input without depending on
	// free disk space.
	sumClipSizes func(audioSourceDir string, rows []SourceDetection) uint64
}

// NewEngine creates a new Engine.
// repo is used both for saving new detections and for querying existing ones.
func NewEngine(repo datastore.DetectionRepository) *Engine {
	return &Engine{
		repo:         repo,
		log:          logger.Global().Module("imports"),
		sumClipSizes: sumSourceClipSizes,
	}
}

// clipNameSetter is the narrow repository capability the engine needs to
// re-attach audio clips to detections that a previous db-only import already
// stored: it sets the clip name of an existing detection whose clip name is
// still empty and reports whether it changed the row, so an existing clip is
// never overwritten. The v2-only production adapter implements it by forwarding
// to the v2 detection repository (internal/datastore/v2only); a repository
// without it (the legacy-store bridge) gets no clip re-attach, and the engine
// logs that once per run.
type clipNameSetter interface {
	SetClipName(ctx context.Context, id uint, clipName string) (bool, error)
}

// pendingRow is a source row that is not stored yet and will be inserted.
type pendingRow struct {
	row *SourceDetection
	ts  time.Time
}

// reattachRow is a source row that matches an already stored clip-less
// detection: its clip is copied and attached to that stored row instead of
// inserting a duplicate.
type reattachRow struct {
	row *SourceDetection
	ts  time.Time
	ids []uint
}

// detectionKey returns a stable composite key for duplicate detection: the
// wall-clock text of ts in its own location, the scientific name, and the
// confidence rounded to 4 decimal places. Run builds it from each source row
// parsed in the import location, so the text is the row's own wall time except
// in a DST gap (see parseTimestamp); loadExistingKeys builds the keys of the
// stored rows it is compared with.
func detectionKey(ts time.Time, scientificName string, confidence float64) string {
	return fmt.Sprintf("%s|%s|%.4f", ts.Format(time.DateTime), scientificName, confidence)
}

// Run imports all detections from src that are not already present in the store.
// It returns a final ImportStats summary. Partial results are consistent because
// the dedup set makes a re-run safe.
// When opts.IncludeAudio is true, a source row that matches an already stored
// detection whose ClipName is still empty (a db-only import leaves it empty)
// additionally gets its audio clip copied and attached to that stored row
// through the repository's SetClipName; no duplicate row is inserted.
// opts may be nil; a nil pointer is replaced with a zero-value ImportOptions so
// callers are not required to allocate one.
func (e *Engine) Run(ctx context.Context, src Source, opts *ImportOptions, reporter ProgressReporter) (ImportStats, error) {
	if opts == nil {
		opts = &ImportOptions{}
	}
	opts.withDefaults()

	stats := ImportStats{Phase: "validate"}
	e.report(reporter, stats)

	// When audio import is requested, both the source directory and the export path must
	// be configured. Failing fast here turns a misconfiguration into a clear error instead
	// of silently importing detections without their audio (or reading a path relative to
	// the working directory).
	if opts.IncludeAudio && (opts.AudioSourceDir == "" || opts.ClipExportPath == "") {
		return stats, errors.Newf("audio import requires both an audio source directory and a clip export path").
			Component("imports").
			Category(errors.CategoryValidation).
			Context("phase", "validate").
			Build()
	}

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

	// Pre-load the keys of every stored detection to avoid per-row queries. When
	// audio is requested, also collect the stored clip-less detections per key,
	// so a matching source row can have its clip re-attached.
	seen, clipLess, err := e.loadExistingKeys(ctx, opts.Location, opts.IncludeAudio)
	if err != nil {
		return stats, errors.New(err).
			Component("imports").
			Category(errors.CategoryDatabase).
			Context("phase", "dedup").
			Build()
	}

	// Re-attaching clips to existing rows needs a repository that can set a clip
	// name on them; without that capability (the legacy store) the run continues
	// and simply attaches nothing.
	setter := e.resolveClipNameSetter(opts.IncludeAudio)
	// queued holds the stored detection IDs a re-attach candidate was queued for
	// during this run. An ID is claimed at queue time, so no other candidate for
	// that same ID is queued or copied in this run — within one batch or across
	// batches — and a stored row reachable under several dedup keys is copied
	// and counted once whatever the batch boundaries.
	queued := make(map[uint]struct{})

	e.log.Info("starting import",
		logger.String("source_node", opts.SourceNode),
		logger.Int("total_rows", total),
		logger.Int("existing_keys", len(seen)))

	stats.Phase = "import"
	e.report(reporter, stats)

	iterErr := src.Iterate(ctx, opts.BatchSize, func(batch []SourceDetection) error {
		// First pass: classify rows into pending (new) vs. skipped/errored, and
		// queue clip re-attach for stored clip-less rows.
		pending, reattach, err := e.classifyBatch(ctx, batch, opts.Location, seen, clipLess, setter, queued, &stats)
		if err != nil {
			return err
		}

		// Second pass: copy audio clips when requested. Pending (new) rows and
		// re-attach rows go through the same copyAudioBatch path, so both get the
		// same os.Root containment and disk-space check.
		clipNames, err := e.copyClipsForBatch(ctx, opts, pending, reattach)
		if err != nil {
			return err
		}

		// Third pass: save detections with clip names resolved above.
		if err := e.savePending(ctx, opts, pending, clipNames, &stats); err != nil {
			return err
		}

		// Fourth pass: attach the copied clips to the existing clip-less
		// detections whose source row matched; no new row is created.
		if err := e.attachClips(ctx, setter, reattach, clipNames, len(pending), &stats); err != nil {
			return err
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
		logger.Int("reattached", stats.Reattached),
		logger.Int("errors", stats.Errors))

	return stats, nil
}

// resolveClipNameSetter returns the repository's clip-name capability when audio
// is requested, or nil when the repository does not have it (the legacy store):
// the run then continues without re-attaching clips and the gap is logged once
// per run.
func (e *Engine) resolveClipNameSetter(includeAudio bool) clipNameSetter {
	if !includeAudio {
		return nil
	}
	if setter, ok := e.repo.(clipNameSetter); ok {
		return setter
	}
	e.log.Warn("detection repository cannot set clip names on existing detections, skipping clip re-attach",
		logger.String("repository_type", fmt.Sprintf("%T", e.repo)))
	return nil
}

// classifyBatch runs the first pass over one source batch: rows whose key is
// already stored are counted Skipped (and queue a clip re-attach when audio is
// requested and the stored row is clip-less), all other rows become pending.
// Keys are added to seen eagerly so within-batch duplicates are caught here.
func (e *Engine) classifyBatch(ctx context.Context, batch []SourceDetection, loc *time.Location, seen map[string]struct{}, clipLess map[string][]uint, setter clipNameSetter, queued map[uint]struct{}, stats *ImportStats) ([]pendingRow, []reattachRow, error) {
	pending := make([]pendingRow, 0, len(batch))
	var reattach []reattachRow

	for i := range batch {
		if err := ctx.Err(); err != nil {
			return nil, nil, err
		}
		row := &batch[i]
		ts, parseErr := parseTimestamp(row.Date, row.Time, loc)
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
			// A stored clip-less detection with this key can get its clip
			// re-attached; queue it at most once per key and never for an ID
			// already queued in this run, in this batch or an earlier one.
			if r, ok := e.queueReattach(key, row, ts, clipLess, setter, queued); ok {
				reattach = append(reattach, r)
			}
			continue
		}
		// Mark seen eagerly to deduplicate within-batch duplicates.
		seen[key] = struct{}{}
		pending = append(pending, pendingRow{row: row, ts: ts})
	}
	return pending, reattach, nil
}

// queueReattach queues a clip re-attach for the stored clip-less detections
// recorded under key, if the repository can set clip names and none of the IDs
// is already queued in this run. The IDs are claimed at queue time, so no other
// candidate for the same ID is queued or copied later in this run, within the
// same batch or a later one. The key's entry is consumed either way, so a
// duplicate source row later in the run does not queue it again.
func (e *Engine) queueReattach(key string, row *SourceDetection, ts time.Time, clipLess map[string][]uint, setter clipNameSetter, queued map[uint]struct{}) (reattachRow, bool) {
	if setter == nil {
		return reattachRow{}, false
	}
	ids, ok := clipLess[key]
	if !ok {
		return reattachRow{}, false
	}
	delete(clipLess, key)
	remaining := make([]uint, 0, len(ids))
	for _, id := range ids {
		if _, alreadyQueued := queued[id]; alreadyQueued {
			continue
		}
		queued[id] = struct{}{}
		remaining = append(remaining, id)
	}
	if len(remaining) == 0 {
		return reattachRow{}, false
	}
	return reattachRow{row: row, ts: ts, ids: remaining}, true
}

// copyClipsForBatch runs the second pass over one batch: when audio is
// requested it copies the clips of the pending (new) and re-attach rows through
// the same copyAudioBatch path and returns their clip names, with the pending
// rows' names in the first len(pending) slots. Without audio the returned slice
// is zero-filled.
func (e *Engine) copyClipsForBatch(ctx context.Context, opts *ImportOptions, pending []pendingRow, reattach []reattachRow) ([]string, error) {
	clipNames := make([]string, len(pending)+len(reattach))
	if !opts.IncludeAudio || opts.ClipExportPath == "" || len(clipNames) == 0 {
		return clipNames, nil
	}
	rows := make([]SourceDetection, len(clipNames))
	tss := make([]time.Time, len(clipNames))
	for i, p := range pending {
		rows[i] = *p.row
		tss[i] = p.ts
	}
	for j, r := range reattach {
		rows[len(pending)+j] = *r.row
		tss[len(pending)+j] = r.ts
	}
	if err := e.copyAudioBatch(ctx, opts, rows, tss, clipNames); err != nil {
		return nil, err
	}
	return clipNames, nil
}

// savePending runs the third pass: it saves every pending row with the clip
// name resolved for it and counts the outcome in stats.
func (e *Engine) savePending(ctx context.Context, opts *ImportOptions, pending []pendingRow, clipNames []string, stats *ImportStats) error {
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
	return nil
}

// attachClips runs the fourth pass: it attaches the copied clips to the existing
// clip-less detections collected in reattach, through the repository's
// SetClipName. clipNames holds the copied clip paths, with the pending rows'
// names in its first pendingCount slots. Only rows the guarded update actually
// changed are counted as re-attached. Because queueReattach claims each target
// ID at queue time, every ID appears in at most one reattach row of the whole
// run, so it is attached and counted at most once whatever the batch
// boundaries. No new detection row is created; a clip miss leaves the stored
// row untouched.
func (e *Engine) attachClips(ctx context.Context, setter clipNameSetter, reattach []reattachRow, clipNames []string, pendingCount int, stats *ImportStats) error {
	if setter == nil {
		return nil
	}
	for j, r := range reattach {
		clipName := clipNames[pendingCount+j]
		if clipName == "" {
			// The clip was a miss; leave the stored row untouched.
			continue
		}
		for _, id := range r.ids {
			if err := ctx.Err(); err != nil {
				return err
			}
			changed, err := setter.SetClipName(ctx, id, clipName)
			if err != nil {
				if ctx.Err() != nil {
					// Context was cancelled or timed out during the update;
					// propagate the context error for a clean abort signal.
					return ctx.Err()
				}
				e.log.Error("failed to attach clip to existing detection",
					logger.Uint64("detection_id", uint64(id)),
					logger.String("clip_name", clipName),
					logger.Error(err))
				stats.Errors++
				continue
			}
			if changed {
				stats.Reattached++
			}
			// A successful no-op means another writer filled the clip name
			// between the preload and this update; nothing to count.
		}
	}
	return nil
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
	requiredBytes := e.sumClipSizes(opts.AudioSourceDir, rows)
	if requiredBytes > 0 {
		if spaceErr := checkDiskSpace(opts.ClipExportPath, requiredBytes, opts.DiskSpaceFunc); spaceErr != nil {
			return spaceErr
		}
	}

	missCount := 0
	e.copyCandidateClips(ctx, opts, rows, tss, clipNames, &missCount)
	// Only report the miss count for a batch that ran to completion. On cancellation
	// copyCandidateClips stops launching copies early and the batch is discarded before
	// any detection is saved, so a partial miss count would understate reality and
	// mislead; the separate "import cancelled" log is the accurate signal in that case.
	if missCount > 0 && ctx.Err() == nil {
		e.log.Info("some audio clips could not be copied",
			logger.Int("miss_count", missCount),
			logger.Int("batch_size", len(rows)))
	}
	return nil
}

// loadExistingKeys pages through every stored detection and returns their dedup
// keys in a set. The search is not limited to imported rows: the v2 database
// keeps a node name only on an audio source and imported rows have none, so a
// node filter would find nothing there, and a stored row that matches a source
// row on wall time, species and confidence is the same detection whatever
// wrote it.
//
// The read-back timestamp is, on a v2 database, the stored instant in the
// station zone; on the legacy store it is the stored wall-clock text read in
// the station zone. Rows reach the store in two representations, so each
// stored row adds two keys:
//   - r.Timestamp matches rows whose source wall time was stored as station
//     wall-clock time: the legacy store, and v2 rows imported before the
//     detection's own instant was stored.
//   - r.Timestamp.In(loc) matches v2 rows that store the detection's own
//     instant, parsed in the import location loc.
//
// When loc is the station zone the two keys are equal.
//
// When collectClipLess is true, the second return value maps each dedup key to
// the IDs of the stored detections with an empty ClipName under that key (a row
// appears under both of its keys), so a matching source row can have its clip
// re-attached to those rows.
//
// Known limits, accepted: when loc differs from the station zone, a row in the
// station zone's skipped DST hour (a gap wall time that reads back shifted) can be
// imported again on the legacy store and for v2 rows imported before the detection's
// own instant was stored, and so can a re-import whose location differs from that of
// the first import. A source row can also
// be skipped as a duplicate of a different stored row that has the same wall
// second, species and confidence.
func (e *Engine) loadExistingKeys(ctx context.Context, loc *time.Location, collectClipLess bool) (seen map[string]struct{}, clipLess map[string][]uint, err error) {
	// TestImport_ReimportV2_LoadsEveryPage stores pageSize+1 rows; keep its
	// row count above this value.
	const pageSize = 1000
	seen = make(map[string]struct{})
	if collectClipLess {
		clipLess = make(map[string][]uint)
	}
	var minID uint

	for {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, nil, ctxErr
		}

		filters := datastore.NewDetectionFilters().
			WithMinID(minID).
			WithLimit(pageSize)

		var results []*detection.Result
		results, _, err = e.repo.Search(ctx, filters)
		if err != nil {
			return nil, nil, errors.New(err).
				Component("imports").
				Category(errors.CategoryDatabase).
				Context("operation", "load_existing_keys").
				Build()
		}

		for _, r := range results {
			key := detectionKey(r.Timestamp, r.Species.ScientificName, r.Confidence)
			keyInLoc := detectionKey(r.Timestamp.In(loc), r.Species.ScientificName, r.Confidence)
			seen[key] = struct{}{}
			seen[keyInLoc] = struct{}{}
			if collectClipLess && r.ClipName == "" {
				clipLess[key] = append(clipLess[key], r.ID)
				clipLess[keyInLoc] = append(clipLess[keyInLoc], r.ID)
			}
			if r.ID > minID {
				minID = r.ID
			}
		}

		if len(results) < pageSize {
			break
		}
	}

	return seen, clipLess, nil
}

// parseTimestamp parses "YYYY-MM-DD HH:MM:SS" in loc.
//
// BirdNET-Pi rows carry no UTC offset. A wall time in the hour that repeats
// when DST ends names two instants and cannot be resolved from the row, and
// one in the hour skipped when DST starts names none. time.ParseInLocation
// picks one of the two instants (which one depends on the zone) and moves a
// skipped time to a real instant whose wall text differs from the row's;
// internal/wallclock resolves both differently. The parse is kept as it is on
// purpose: its result is formatted into the dedup key (detectionKey), so
// resolving these times another way would change the key of a skipped-time
// row imported earlier, and a re-import would insert it again.
func parseTimestamp(date, timeStr string, loc *time.Location) (time.Time, error) {
	return time.ParseInLocation("2006-01-02 15:04:05", date+" "+timeStr, loc)
}

// mapToResult converts a SourceDetection to a detection.Result.
//
// Field mapping decisions:
//   - BeginTime and EndTime are left as zero values: BirdNET-Pi stores detections
//     as point-in-time events without clip offsets, so there is no timing data to map.
//   - ClipName: DB-only imports leave ClipName empty; DB+audio imports set it before
//     Save when a matching source clip is found, and re-attach it to an already
//     stored clip-less row through the repository's SetClipName. An empty ClipName
//     avoids broken "audio not available" links.
//   - Model is set to a synthetic marker so imported rows are distinguishable from
//     live detections in queries or analytics.
//   - Provenance: SourceNode is set, but only the legacy store persists it (the
//     notes.source_node column). The v2 store keeps a node name only on an
//     audio source, and AudioSource is left zero (it is runtime-only on the
//     legacy save path), so on v2 imported rows carry no node name. Duplicate
//     detection does not depend on it (see loadExistingKeys).
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
