// Package migration provides background migration of legacy data to the v2 schema.
package migration

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
)

// DefaultBatchSize is the default number of records processed per batch.
const DefaultBatchSize = 100

// DefaultSleepBetweenBatches is the default sleep duration between batches
// to reduce database load and allow other operations.
const DefaultSleepBetweenBatches = 100 * time.Millisecond

// ErrMigrationPaused is returned when migration is paused by user.
var ErrMigrationPaused = errors.New("migration paused")

// ErrMigrationCancelled is returned when migration is cancelled.
var ErrMigrationCancelled = errors.New("migration cancelled")

// Worker performs background migration of legacy records to v2 schema.
type Worker struct {
	legacy       datastore.DetectionRepository
	v2Detection  repository.DetectionRepository
	labelRepo    repository.LabelRepository
	modelRepo    repository.ModelRepository
	sourceRepo   repository.AudioSourceRepository
	stateManager *datastoreV2.StateManager
	logger       *slog.Logger
	batchSize    int
	sleepBetween time.Duration
	timezone     *time.Location

	// Control channels
	pauseCh  chan struct{}
	resumeCh chan struct{}
	stopCh   chan struct{}

	// State
	mu        sync.RWMutex
	running   bool
	paused    bool
	lastError error
}

// WorkerConfig configures the migration worker.
type WorkerConfig struct {
	Legacy       datastore.DetectionRepository
	V2Detection  repository.DetectionRepository
	LabelRepo    repository.LabelRepository
	ModelRepo    repository.ModelRepository
	SourceRepo   repository.AudioSourceRepository
	StateManager *datastoreV2.StateManager
	Logger       *slog.Logger
	BatchSize    int
	Timezone     *time.Location
}

// NewWorker creates a new migration worker.
func NewWorker(cfg *WorkerConfig) *Worker {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	tz := cfg.Timezone
	if tz == nil {
		tz = time.Local
	}

	return &Worker{
		legacy:       cfg.Legacy,
		v2Detection:  cfg.V2Detection,
		labelRepo:    cfg.LabelRepo,
		modelRepo:    cfg.ModelRepo,
		sourceRepo:   cfg.SourceRepo,
		stateManager: cfg.StateManager,
		logger:       logger,
		batchSize:    batchSize,
		sleepBetween: DefaultSleepBetweenBatches,
		timezone:     tz,
		pauseCh:      make(chan struct{}),
		resumeCh:     make(chan struct{}),
		stopCh:       make(chan struct{}),
	}
}

// Start begins the background migration process.
// It should be called after the state manager transitions to DUAL_WRITE.
func (w *Worker) Start(ctx context.Context) error {
	w.mu.Lock()
	if w.running {
		w.mu.Unlock()
		return errors.New("worker already running")
	}
	w.running = true
	w.paused = false
	w.lastError = nil
	w.mu.Unlock()

	go w.run(ctx)
	return nil
}

// Pause temporarily stops migration processing.
func (w *Worker) Pause() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running || w.paused {
		return
	}

	w.paused = true
	close(w.pauseCh)
	w.pauseCh = make(chan struct{})
}

// Resume continues migration after a pause.
func (w *Worker) Resume() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if !w.running || !w.paused {
		return
	}

	w.paused = false
	close(w.resumeCh)
	w.resumeCh = make(chan struct{})
}

// Stop permanently stops the migration worker.
func (w *Worker) Stop() {
	w.mu.Lock()
	if !w.running {
		w.mu.Unlock()
		return
	}

	w.running = false
	close(w.stopCh)
	w.mu.Unlock()
}

// IsRunning returns whether the worker is currently running.
func (w *Worker) IsRunning() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.running
}

// IsPaused returns whether the worker is currently paused.
func (w *Worker) IsPaused() bool {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.paused
}

// LastError returns the last error encountered during migration.
func (w *Worker) LastError() error {
	w.mu.RLock()
	defer w.mu.RUnlock()
	return w.lastError
}

// run is the main migration loop.
func (w *Worker) run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
	}()

	w.logger.Info("migration worker started")

	for {
		// Check for stop signal
		select {
		case <-ctx.Done():
			w.logger.Info("migration worker stopped: context cancelled")
			return
		case <-w.stopCh:
			w.logger.Info("migration worker stopped: stop requested")
			return
		default:
		}

		// Check for pause
		w.mu.RLock()
		paused := w.paused
		w.mu.RUnlock()

		if paused {
			w.logger.Info("migration worker paused")
			select {
			case <-ctx.Done():
				return
			case <-w.stopCh:
				return
			case <-w.resumeCh:
				w.logger.Info("migration worker resumed")
				continue
			}
		}

		// Check migration state
		state, err := w.stateManager.GetState()
		if err != nil {
			w.setError(err)
			w.logger.Error("failed to get migration state", "error", err)
			time.Sleep(5 * time.Second) // Back off on errors
			continue
		}

		// Only migrate during DUAL_WRITE or MIGRATING states
		if state.State != entities.MigrationStatusDualWrite && state.State != entities.MigrationStatusMigrating {
			w.logger.Debug("migration not active", "status", state.State)
			time.Sleep(time.Second)
			continue
		}

		// Process a batch
		batch, err := w.processBatch(ctx)
		if err != nil {
			if errors.Is(err, ErrMigrationPaused) || errors.Is(err, ErrMigrationCancelled) {
				// Update progress even on pause/cancel so we can resume from the right place
				if batch.lastID > 0 {
					if updateErr := w.stateManager.IncrementProgress(batch.lastID, int64(batch.migrated)); updateErr != nil {
						w.logger.Warn("failed to update progress on pause", "error", updateErr)
					}
				}
				continue
			}
			w.setError(err)
			w.logger.Error("batch processing failed", "error", err)
			time.Sleep(5 * time.Second) // Back off on errors
			continue
		}

		if batch.migrated == 0 && batch.lastID == 0 {
			// No more records to migrate - transition to validation
			w.logger.Info("migration complete, starting validation")
			if err := w.stateManager.TransitionToValidating(); err != nil {
				w.logger.Error("failed to transition to validating", "error", err)
			}
			return
		}

		// Update progress with the highest ID processed in this batch
		if batch.lastID > 0 {
			if err := w.stateManager.IncrementProgress(batch.lastID, int64(batch.migrated)); err != nil {
				w.logger.Warn("failed to update progress", "error", err)
			}
		}

		// Sleep between batches to reduce load
		time.Sleep(w.sleepBetween)
	}
}

// batchResult contains the results of a migration batch.
type batchResult struct {
	migrated   int
	lastID     uint
	incomplete bool // true if batch was interrupted by pause/stop
}

// processBatch migrates a batch of records from legacy to v2.
// Returns the number of records migrated and the highest ID processed.
func (w *Worker) processBatch(ctx context.Context) (batchResult, error) {
	// Get the last migrated ID from the state manager (NOT from V2 table).
	// CRITICAL: We must use state.LastMigratedID because during dual-write mode,
	// new detections are written to V2 with high IDs. Querying MAX(legacy_id)
	// from V2 would return the newest detection's ID, causing the worker to
	// incorrectly believe all historical records have been migrated.
	state, err := w.stateManager.GetState()
	if err != nil {
		return batchResult{}, fmt.Errorf("failed to get migration state: %w", err)
	}
	lastID := state.LastMigratedID

	// Use cursor-based pagination with MinID to fetch records after lastID
	// This ensures we get the next batch of records ordered by ID ascending
	filters := datastore.NewDetectionFilters().
		WithMinID(lastID).
		WithLimit(w.batchSize)

	results, _, err := w.legacy.Search(ctx, filters)
	if err != nil {
		return batchResult{}, fmt.Errorf("failed to fetch legacy records: %w", err)
	}

	if len(results) == 0 {
		return batchResult{}, nil
	}

	// Migrate each record (no in-memory filtering needed - DB already filtered)
	var result batchResult
	for _, r := range results {
		// Check for pause/stop
		select {
		case <-ctx.Done():
			result.incomplete = true
			return result, ctx.Err()
		case <-w.stopCh:
			result.incomplete = true
			return result, ErrMigrationCancelled
		default:
		}

		w.mu.RLock()
		paused := w.paused
		w.mu.RUnlock()
		if paused {
			result.incomplete = true
			return result, ErrMigrationPaused
		}

		if err := w.migrateRecord(ctx, r); err != nil {
			w.logger.Warn("failed to migrate record", "id", r.ID, "error", err)
			// Track failed records for later reconciliation
			if addErr := w.stateManager.AddDirtyID(r.ID); addErr != nil {
				w.logger.Error("failed to track dirty ID", "id", r.ID, "error", addErr)
			}
			// Still update lastID to avoid re-processing this record
			result.lastID = r.ID
			continue
		}
		result.migrated++
		result.lastID = r.ID
	}

	return result, nil
}

// migrateRecord converts and saves a single legacy record to v2.
func (w *Worker) migrateRecord(ctx context.Context, result *detection.Result) error {
	// Convert to v2 entity
	det, err := w.convertToV2Detection(ctx, result)
	if err != nil {
		return fmt.Errorf("conversion failed: %w", err)
	}

	// Check if record already exists in V2 (from dual-write)
	existing, err := w.v2Detection.Get(ctx, result.ID)
	if err == nil && existing != nil {
		// Update existing record - legacy is source of truth during migration
		updates := map[string]any{
			"label_id":   det.LabelID,
			"model_id":   det.ModelID,
			"confidence": det.Confidence,
		}
		if det.SourceID != nil {
			updates["source_id"] = *det.SourceID
		}
		if det.ClipName != nil {
			updates["clip_name"] = *det.ClipName
		}
		return w.v2Detection.Update(ctx, result.ID, updates)
	}

	// Create new record with specific ID
	return w.v2Detection.SaveWithID(ctx, det)
}

// conversionDeps returns the dependencies for shared conversion functions.
func (w *Worker) conversionDeps() *repository.ConversionDeps {
	return &repository.ConversionDeps{
		LabelRepo:  w.labelRepo,
		ModelRepo:  w.modelRepo,
		SourceRepo: w.sourceRepo,
		Logger:     w.logger,
	}
}

// convertToV2Detection converts a domain Result to a v2 Detection entity.
func (w *Worker) convertToV2Detection(ctx context.Context, result *detection.Result) (*entities.Detection, error) {
	return repository.ConvertToV2Detection(ctx, result, w.conversionDeps())
}

func (w *Worker) setError(err error) {
	w.mu.Lock()
	w.lastError = err
	w.mu.Unlock()
}
