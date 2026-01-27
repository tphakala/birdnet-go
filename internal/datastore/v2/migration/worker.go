// Package migration provides background migration of legacy data to the v2 schema.
package migration

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	datastoreV2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/repository"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// DefaultBatchSize is the default number of records processed per batch.
const DefaultBatchSize = 100

// DefaultSleepBetweenBatches is the default sleep duration between batches
// to reduce database load and allow other operations.
const DefaultSleepBetweenBatches = 100 * time.Millisecond

// DefaultErrorBackoff is the sleep duration after encountering errors.
const DefaultErrorBackoff = 5 * time.Second

// DefaultMaxConsecutiveErrors is the threshold for auto-pausing on repeated failures.
const DefaultMaxConsecutiveErrors = 10

// DefaultRateWindowSize is the number of samples for rate calculation.
const DefaultRateWindowSize = 10

// ProgressLogInterval is the minimum time between progress log entries.
const ProgressLogInterval = 10 * time.Second

// ProgressLogRecordThreshold is the minimum records between progress log entries.
const ProgressLogRecordThreshold = 1000

// ErrMigrationPaused is returned when migration is paused by user.
var ErrMigrationPaused = errors.New("migration paused")

// ErrMigrationCancelled is returned when migration is cancelled.
var ErrMigrationCancelled = errors.New("migration cancelled")

// ErrTooManyErrors is returned when too many consecutive errors occur.
var ErrTooManyErrors = errors.New("too many consecutive errors")

// rateSample records a batch's timing for rate calculation.
type rateSample struct {
	records  int64
	duration time.Duration
}

// Worker performs background migration of legacy records to v2 schema.
type Worker struct {
	legacy          datastore.DetectionRepository
	v2Detection     repository.DetectionRepository
	labelRepo       repository.LabelRepository
	modelRepo       repository.ModelRepository
	sourceRepo      repository.AudioSourceRepository
	stateManager    *datastoreV2.StateManager
	relatedMigrator *RelatedDataMigrator
	logger          logger.Logger
	batchSize       int
	sleepBetween    time.Duration
	timezone        *time.Location

	// Control channels
	pauseCh  chan struct{}
	resumeCh chan struct{}
	stopCh   chan struct{}

	// State
	mu                sync.RWMutex
	running           bool
	paused            bool
	lastError         error
	consecutiveErrors int
	maxConsecErrors   int

	// Rate tracking for time estimates
	rateSamples []rateSample
	rateIndex   int

	// Progress logging tracking
	lastProgressLog     time.Time
	recordsSinceLastLog int64
}

// WorkerConfig configures the migration worker.
type WorkerConfig struct {
	Legacy            datastore.DetectionRepository
	V2Detection       repository.DetectionRepository
	LabelRepo         repository.LabelRepository
	ModelRepo         repository.ModelRepository
	SourceRepo        repository.AudioSourceRepository
	StateManager      *datastoreV2.StateManager
	RelatedMigrator   *RelatedDataMigrator // Optional: migrates reviews, comments, locks, predictions
	Logger            logger.Logger
	BatchSize         int
	Timezone          *time.Location
	MaxConsecErrors   int // Optional: defaults to DefaultMaxConsecutiveErrors
}

// NewWorker creates a new migration worker.
// Returns an error if required dependencies are not provided.
func NewWorker(cfg *WorkerConfig) (*Worker, error) {
	// Validate required dependencies
	if cfg == nil {
		return nil, errors.New("worker config is required")
	}
	if cfg.Legacy == nil {
		return nil, errors.New("legacy repository is required")
	}
	if cfg.V2Detection == nil {
		return nil, errors.New("v2 detection repository is required")
	}
	if cfg.LabelRepo == nil {
		return nil, errors.New("label repository is required")
	}
	if cfg.ModelRepo == nil {
		return nil, errors.New("model repository is required")
	}
	if cfg.SourceRepo == nil {
		return nil, errors.New("source repository is required")
	}
	if cfg.StateManager == nil {
		return nil, errors.New("state manager is required")
	}
	if cfg.Logger == nil {
		return nil, errors.New("logger is required")
	}

	batchSize := cfg.BatchSize
	if batchSize <= 0 {
		batchSize = DefaultBatchSize
	}

	tz := cfg.Timezone
	if tz == nil {
		tz = time.Local
	}

	maxConsecErrors := cfg.MaxConsecErrors
	if maxConsecErrors <= 0 {
		maxConsecErrors = DefaultMaxConsecutiveErrors
	}

	return &Worker{
		legacy:          cfg.Legacy,
		v2Detection:     cfg.V2Detection,
		labelRepo:       cfg.LabelRepo,
		modelRepo:       cfg.ModelRepo,
		sourceRepo:      cfg.SourceRepo,
		stateManager:    cfg.StateManager,
		relatedMigrator: cfg.RelatedMigrator,
		logger:          cfg.Logger,
		batchSize:       batchSize,
		sleepBetween:    DefaultSleepBetweenBatches,
		timezone:        tz,
		pauseCh:         make(chan struct{}),
		resumeCh:        make(chan struct{}),
		stopCh:          make(chan struct{}),
		maxConsecErrors: maxConsecErrors,
		rateSamples:     make([]rateSample, DefaultRateWindowSize),
	}, nil
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
	w.consecutiveErrors = 0

	// Reinitialize control channels in case they were closed by a previous Stop()
	w.stopCh = make(chan struct{})
	w.pauseCh = make(chan struct{})
	w.resumeCh = make(chan struct{})

	// Reset rate samples for fresh estimates
	w.rateSamples = make([]rateSample, DefaultRateWindowSize)
	w.rateIndex = 0
	w.lastProgressLog = time.Time{}
	w.recordsSinceLastLog = 0

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

// runAction represents the result of processing a single iteration.
type runAction int

const (
	runActionContinue runAction = iota
	runActionReturn
)

// run is the main migration loop.
func (w *Worker) run(ctx context.Context) {
	defer func() {
		w.mu.Lock()
		w.running = false
		w.mu.Unlock()
	}()

	w.logger.Info("migration worker started")

	for {
		action := w.runIteration(ctx)
		if action == runActionReturn {
			return
		}
	}
}

// runIteration processes one iteration of the migration loop.
// Returns runActionReturn if the loop should exit, runActionContinue otherwise.
func (w *Worker) runIteration(ctx context.Context) runAction {
	// Check for stop signal
	if w.checkStopSignal(ctx) {
		return runActionReturn
	}

	// Handle paused state
	if action := w.handlePausedState(ctx); action == runActionReturn {
		return runActionReturn
	}

	// Check migration state
	state, err := w.stateManager.GetState()
	if err != nil {
		if w.handleError(err, "failed to get migration state") {
			return runActionReturn
		}
		time.Sleep(DefaultErrorBackoff)
		return runActionContinue
	}

	// Handle different states
	switch state.State {
	case entities.MigrationStatusValidating:
		return w.handleValidatingState(ctx)
	case entities.MigrationStatusCutover:
		return w.handleCutoverState(ctx)
	case entities.MigrationStatusCompleted:
		// Zombie prevention: worker may wake up after migration completed
		w.logger.Info("migration already completed")
		return runActionReturn
	case entities.MigrationStatusDualWrite, entities.MigrationStatusMigrating:
		return w.handleMigratingState(ctx)
	default:
		w.logger.Debug("migration not active", logger.String("status", string(state.State)))
		time.Sleep(time.Second)
		return runActionContinue
	}
}

// checkStopSignal checks for context cancellation or stop request.
func (w *Worker) checkStopSignal(ctx context.Context) bool {
	select {
	case <-ctx.Done():
		w.logger.Info("migration worker stopped: context cancelled")
		return true
	case <-w.stopCh:
		w.logger.Info("migration worker stopped: stop requested")
		return true
	default:
		return false
	}
}

// handlePausedState handles the paused state, waiting for resume or stop.
func (w *Worker) handlePausedState(ctx context.Context) runAction {
	w.mu.RLock()
	paused := w.paused
	resumeCh := w.resumeCh // Capture under lock to avoid race with Resume()
	stopCh := w.stopCh     // Capture under lock to avoid race with Stop()
	w.mu.RUnlock()

	if !paused {
		return runActionContinue
	}

	w.logger.Info("migration worker paused")
	select {
	case <-ctx.Done():
		return runActionReturn
	case <-stopCh: // Use captured channel
		return runActionReturn
	case <-resumeCh: // Use captured channel
		w.logger.Info("migration worker resumed")
		w.resetConsecutiveErrors()
		return runActionContinue
	}
}

// handleValidatingState runs validation and completes migration.
func (w *Worker) handleValidatingState(ctx context.Context) runAction {
	w.logger.Info("running validation")
	if err := w.validate(ctx); err != nil {
		w.logger.Error("validation failed", logger.Error(err))
		if setErr := w.stateManager.SetError(fmt.Sprintf("validation failed: %v", err)); setErr != nil {
			w.logger.Warn("failed to set error in state", logger.Error(setErr))
		}
		w.pauseOnValidationFailure()
		return runActionContinue
	}

	// Validation passed - transition to cutover and complete
	w.logger.Info("validation passed, transitioning to cutover")
	if err := w.stateManager.TransitionToCutover(); err != nil {
		w.logger.Error("failed to transition to cutover", logger.Error(err))
		return runActionContinue
	}
	if err := w.stateManager.Complete(); err != nil {
		w.logger.Error("failed to complete migration", logger.Error(err))
		return runActionContinue
	}
	w.logger.Info("migration completed successfully")

	// Send notification that migration has completed
	if notifService := notification.GetService(); notifService != nil {
		if _, err := notifService.CreateWithComponent(
			notification.TypeSystem,
			notification.PriorityMedium,
			"Database Migration Completed",
			"Database migration has completed successfully. Your system is now ready for upcoming features.",
			"database",
		); err != nil {
			w.logger.Warn("failed to send migration completion notification", logger.Error(err))
		}
	}

	return runActionReturn
}

// handleCutoverState handles the cutover state by attempting to complete migration.
// This handles recovery when TransitionToCutover succeeded but Complete failed.
func (w *Worker) handleCutoverState(ctx context.Context) runAction {
	w.logger.Info("in cutover state, attempting to complete migration")

	if err := w.stateManager.Complete(); err != nil {
		w.logger.Error("failed to complete migration from cutover state", logger.Error(err))
		// Use select for responsive backoff - allows stop signals during wait
		select {
		case <-ctx.Done():
			return runActionReturn
		case <-w.stopCh:
			return runActionReturn
		case <-time.After(DefaultErrorBackoff):
			return runActionContinue
		}
	}

	w.logger.Info("migration completed successfully from cutover state")

	// Send notification that migration has completed
	if notifService := notification.GetService(); notifService != nil {
		if _, err := notifService.CreateWithComponent(
			notification.TypeSystem,
			notification.PriorityMedium,
			"Database Migration Completed",
			"Database migration has completed successfully. Your system is now ready for upcoming features.",
			"database",
		); err != nil {
			w.logger.Warn("failed to send migration completion notification", logger.Error(err))
		}
	}

	return runActionReturn
}

// handleMigratingState processes a batch of records.
func (w *Worker) handleMigratingState(ctx context.Context) runAction {
	batchStart := time.Now()
	batch, err := w.processBatch(ctx)
	batchDuration := time.Since(batchStart)

	if err != nil {
		return w.handleBatchError(err, batch)
	}

	w.resetConsecutiveErrors()

	if batch.migrated == 0 && batch.lastID == 0 {
		w.transitionToValidation()
		return runActionContinue
	}

	w.updateBatchProgress(batch, batchDuration)
	time.Sleep(w.sleepBetween)
	return runActionContinue
}

// handleBatchError handles errors from batch processing.
func (w *Worker) handleBatchError(err error, batch batchResult) runAction {
	if errors.Is(err, ErrMigrationPaused) || errors.Is(err, ErrMigrationCancelled) {
		if batch.lastID > 0 {
			if updateErr := w.stateManager.IncrementProgress(batch.lastID, int64(batch.migrated)); updateErr != nil {
				w.logger.Warn("failed to update progress on pause", logger.Error(updateErr))
			}
		}
		return runActionContinue
	}
	if w.handleError(err, "batch processing failed") {
		return runActionReturn
	}
	time.Sleep(DefaultErrorBackoff)
	return runActionContinue
}

// resetConsecutiveErrors resets the consecutive error counter.
func (w *Worker) resetConsecutiveErrors() {
	w.mu.Lock()
	w.consecutiveErrors = 0
	w.mu.Unlock()
}

// pauseOnValidationFailure pauses migration after validation fails.
func (w *Worker) pauseOnValidationFailure() {
	if pauseErr := w.stateManager.Pause(); pauseErr != nil {
		w.logger.Warn("failed to pause after validation failure", logger.Error(pauseErr))
	}
	w.mu.Lock()
	w.paused = true
	w.mu.Unlock()
}

// transitionToValidation transitions to the validating state.
// Handles the case where we're still in dual_write by transitioning through migrating first.
func (w *Worker) transitionToValidation() {
	w.logger.Info("migration complete, starting validation")

	// Get current state to handle dual_write → migrating → validating transition
	state, err := w.stateManager.GetState()
	if err != nil {
		w.logger.Error("failed to get state for validation transition", logger.Error(err))
		return
	}

	// If still in dual_write, transition to migrating first
	if state.State == entities.MigrationStatusDualWrite {
		if err := w.stateManager.TransitionToMigrating(); err != nil {
			w.logger.Error("failed to transition to migrating", logger.Error(err))
			return
		}
		w.logger.Info("transitioned from dual_write to migrating")
	}

	// Migrate related data (reviews, comments, locks, predictions) before validation
	if w.relatedMigrator != nil {
		ctx := context.Background()
		if err := w.relatedMigrator.MigrateAll(ctx); err != nil {
			w.logger.Error("related data migration failed", logger.Error(err))
			// Continue to validation - related data is non-fatal
		}
	}

	// Now transition to validating
	if err := w.stateManager.TransitionToValidating(); err != nil {
		w.logger.Error("failed to transition to validating", logger.Error(err))
	}
}

// updateBatchProgress records rate sample and updates progress.
func (w *Worker) updateBatchProgress(batch batchResult, duration time.Duration) {
	if batch.migrated > 0 {
		w.recordRateSample(int64(batch.migrated), duration)
	}
	if batch.lastID > 0 {
		if err := w.stateManager.IncrementProgress(batch.lastID, int64(batch.migrated)); err != nil {
			w.logger.Warn("failed to update progress", logger.Error(err))
		}
	}

	// Track records for periodic progress logging
	w.recordsSinceLastLog += int64(batch.migrated)

	// Log progress periodically (every 10 seconds or 1000 records)
	shouldLog := time.Since(w.lastProgressLog) >= ProgressLogInterval ||
		w.recordsSinceLastLog >= ProgressLogRecordThreshold

	if shouldLog && batch.migrated > 0 {
		state, err := w.stateManager.GetState()
		if err == nil {
			percent := float64(0)
			if state.TotalRecords > 0 {
				percent = float64(state.MigratedRecords) / float64(state.TotalRecords) * 100
			}
			rate := w.GetMigrationRate()
			w.logger.Info("migration progress",
				logger.Int64("migrated_records", state.MigratedRecords),
				logger.Int64("total_records", state.TotalRecords),
				logger.Float64("percent", percent),
				logger.Float64("records_per_second", rate))
		}
		w.lastProgressLog = time.Now()
		w.recordsSinceLastLog = 0
	}
}

// handleError increments the consecutive error counter and auto-pauses if threshold exceeded.
// Returns true if migration was auto-paused.
func (w *Worker) handleError(err error, msg string) bool {
	w.mu.Lock()
	w.consecutiveErrors++
	count := w.consecutiveErrors
	maxErrors := w.maxConsecErrors
	w.lastError = err
	w.mu.Unlock()

	w.logger.Error(msg, logger.Error(err), logger.Int("consecutive_errors", count))

	if count >= maxErrors {
		w.logger.Error("too many consecutive errors, auto-pausing migration",
			logger.Int("count", count), logger.Int("threshold", maxErrors))

		// Record the error in state
		errMsg := fmt.Sprintf("auto-paused after %d consecutive errors: %v", count, err)
		if setErr := w.stateManager.SetError(errMsg); setErr != nil {
			w.logger.Warn("failed to set error in state", logger.Error(setErr))
		}

		// Pause via state manager
		if pauseErr := w.stateManager.Pause(); pauseErr != nil {
			w.logger.Warn("failed to pause via state manager", logger.Error(pauseErr))
		}

		// Send notification about the error
		if notifService := notification.GetService(); notifService != nil {
			if _, notifErr := notifService.CreateWithComponent(
				notification.TypeWarning,
				notification.PriorityHigh,
				"Database Migration Error",
				fmt.Sprintf("Migration paused due to repeated errors. Please check the logs and try resuming. Error: %v", err),
				"database",
			); notifErr != nil {
				w.logger.Warn("failed to send migration error notification", logger.Error(notifErr))
			}
		}

		// Set local paused state
		w.mu.Lock()
		w.paused = true
		w.mu.Unlock()

		return true
	}
	return false
}

// validate performs validation checks to ensure migration completeness.
func (w *Worker) validate(ctx context.Context) error {
	w.logger.Info("starting migration validation")

	// Get migration state for totals
	state, err := w.stateManager.GetState()
	if err != nil {
		return fmt.Errorf("failed to get state for validation: %w", err)
	}

	// Check 1: Record count comparison
	// Count legacy records
	legacyCount, err := w.countLegacyRecords(ctx)
	if err != nil {
		return fmt.Errorf("failed to count legacy records: %w", err)
	}

	// Count V2 records
	v2Count, err := w.v2Detection.CountAll(ctx)
	if err != nil {
		return fmt.Errorf("failed to count v2 records: %w", err)
	}

	w.logger.Info("validation record counts",
		logger.Int64("legacy", legacyCount),
		logger.Int64("v2", v2Count),
		logger.Int64("migrated", state.MigratedRecords))

	// Allow for small discrepancy due to new records during migration
	// V2 should have at least as many records as legacy (could have more from dual-write)
	if v2Count < legacyCount {
		return fmt.Errorf("v2 count (%d) is less than legacy count (%d)", v2Count, legacyCount)
	}

	// Check 2: No dirty IDs remaining
	dirtyCount, err := w.stateManager.GetDirtyIDCount()
	if err != nil {
		return fmt.Errorf("failed to get dirty ID count: %w", err)
	}

	if dirtyCount > 0 {
		return fmt.Errorf("%d records failed to migrate (dirty IDs)", dirtyCount)
	}

	w.logger.Info("validation passed",
		logger.Int64("legacy_count", legacyCount),
		logger.Int64("v2_count", v2Count),
		logger.Int64("dirty_count", dirtyCount))

	return nil
}

// countLegacyRecords returns the total number of records in the legacy database.
func (w *Worker) countLegacyRecords(ctx context.Context) (int64, error) {
	// Use CountAll for a lightweight count that doesn't trigger preloads
	return w.legacy.CountAll(ctx)
}

// recordRateSample records a batch timing for rate calculation.
func (w *Worker) recordRateSample(records int64, duration time.Duration) {
	w.mu.Lock()
	defer w.mu.Unlock()

	w.rateSamples[w.rateIndex] = rateSample{
		records:  records,
		duration: duration,
	}
	w.rateIndex = (w.rateIndex + 1) % len(w.rateSamples)
}

// GetMigrationRate returns the current migration rate in records per second.
// Uses a sliding window for accurate estimates that ignore past pauses.
func (w *Worker) GetMigrationRate() float64 {
	w.mu.RLock()
	defer w.mu.RUnlock()

	var totalRecords int64
	var totalDuration time.Duration

	for _, sample := range w.rateSamples {
		if sample.duration > 0 {
			totalRecords += sample.records
			totalDuration += sample.duration
		}
	}

	if totalDuration == 0 {
		return 0
	}

	return float64(totalRecords) / totalDuration.Seconds()
}

// EstimateRemainingTime returns the estimated remaining time based on current rate.
// Returns nil if rate cannot be calculated.
func (w *Worker) EstimateRemainingTime() *time.Duration {
	rate := w.GetMigrationRate()
	if rate <= 0 {
		return nil
	}

	state, err := w.stateManager.GetState()
	if err != nil {
		return nil
	}

	remaining := state.TotalRecords - state.MigratedRecords
	if remaining <= 0 {
		zero := time.Duration(0)
		return &zero
	}

	seconds := float64(remaining) / rate
	duration := time.Duration(seconds * float64(time.Second))
	return &duration
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
			w.logger.Warn("failed to migrate record", logger.Uint64("id", uint64(r.ID)), logger.Error(err))
			// Track failed records for later reconciliation
			if addErr := w.stateManager.AddDirtyID(r.ID); addErr != nil {
				w.logger.Error("failed to track dirty ID", logger.Uint64("id", uint64(r.ID)), logger.Error(addErr))
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
