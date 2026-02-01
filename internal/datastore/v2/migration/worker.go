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

// MySQLBatchSize is the batch size for MySQL migrations.
// MySQL handles larger batches more efficiently than SQLite.
const MySQLBatchSize = 1000

// DefaultSleepBetweenBatches is the default sleep duration between batches
// to reduce database load and allow other operations (used for SQLite).
const DefaultSleepBetweenBatches = 100 * time.Millisecond

// MySQLSleepBetweenBatches is the sleep duration for MySQL migrations.
// MySQL handles concurrent access better than SQLite, so we use minimal throttling.
const MySQLSleepBetweenBatches = 10 * time.Millisecond

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

// validationMaxRetries is the maximum number of validation retry attempts.
const validationMaxRetries = 5

// validationCatchUpThreshold is the max record difference to attempt auto-recovery.
const validationCatchUpThreshold = 100

// validationPreDelay is the delay before validation to let dual-writes complete.
const validationPreDelay = 3 * time.Second

// catchUpMaxBatches is the safety limit for catch-up iterations per validation attempt.
// Prevents infinite loop if records are created faster than migration can process.
// With batch size of 100, this allows scanning up to 1M records (10000 * 100).
const catchUpMaxBatches = 10000

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
	legacy            datastore.DetectionRepository
	v2Detection       repository.DetectionRepository
	labelRepo         repository.LabelRepository
	modelRepo         repository.ModelRepository
	sourceRepo        repository.AudioSourceRepository
	stateManager      *datastoreV2.StateManager
	relatedMigrator   *RelatedDataMigrator
	auxiliaryMigrator *AuxiliaryMigrator
	logger            logger.Logger
	batchSize         int
	sleepBetween      time.Duration
	timezone          *time.Location
	useBatchMode      bool // Use efficient batch inserts (for MySQL)

	// Lookup table IDs for label creation
	speciesLabelTypeID uint  // "species" label type ID
	avesClassID        *uint // "Aves" taxonomic class ID (optional)
	chiropteraClassID  *uint // "Chiroptera" taxonomic class ID (optional)

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

	// Rate tracking for time estimates (sliding window for display)
	rateSamples []rateSample
	rateIndex   int

	// Cumulative rate tracking for stable ETA calculations
	phaseStartTime       time.Time // When current phase started
	phaseRecordsAtStart  int64     // Records already migrated when phase started
	cumulativeRate       float64   // Running average rate (records/second)
	cumulativeRateSamples int      // Number of samples contributing to cumulative rate

	// Progress logging tracking
	lastProgressLog     time.Time
	recordsSinceLastLog int64
}

// WorkerConfig configures the migration worker.
type WorkerConfig struct {
	Legacy              datastore.DetectionRepository
	V2Detection         repository.DetectionRepository
	LabelRepo           repository.LabelRepository
	ModelRepo           repository.ModelRepository
	SourceRepo          repository.AudioSourceRepository
	StateManager        *datastoreV2.StateManager
	RelatedMigrator     *RelatedDataMigrator // Optional: migrates reviews, comments, locks, predictions
	AuxiliaryMigrator   *AuxiliaryMigrator   // Optional: migrates weather, thresholds, image cache, notifications
	Logger              logger.Logger
	BatchSize           int
	SleepBetweenBatches time.Duration // Optional: defaults to DefaultSleepBetweenBatches; use MySQLSleepBetweenBatches for MySQL
	Timezone            *time.Location
	MaxConsecErrors     int  // Optional: defaults to DefaultMaxConsecutiveErrors
	UseBatchMode        bool // Use efficient batch inserts (recommended for MySQL)

	// Lookup table IDs for label creation (required for V2 normalized schema)
	SpeciesLabelTypeID uint  // "species" label type ID
	AvesClassID        *uint // "Aves" taxonomic class ID (optional)
	ChiropteraClassID  *uint // "Chiroptera" taxonomic class ID (optional)
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
	if cfg.SpeciesLabelTypeID == 0 {
		return nil, errors.New("species label type ID is required")
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

	sleepBetween := cfg.SleepBetweenBatches
	if sleepBetween <= 0 {
		sleepBetween = DefaultSleepBetweenBatches
	}

	return &Worker{
		legacy:             cfg.Legacy,
		v2Detection:        cfg.V2Detection,
		labelRepo:          cfg.LabelRepo,
		modelRepo:          cfg.ModelRepo,
		sourceRepo:         cfg.SourceRepo,
		stateManager:       cfg.StateManager,
		relatedMigrator:    cfg.RelatedMigrator,
		auxiliaryMigrator:  cfg.AuxiliaryMigrator,
		logger:             cfg.Logger,
		batchSize:          batchSize,
		sleepBetween:       sleepBetween,
		timezone:           tz,
		useBatchMode:       cfg.UseBatchMode,
		speciesLabelTypeID: cfg.SpeciesLabelTypeID,
		avesClassID:        cfg.AvesClassID,
		chiropteraClassID:  cfg.ChiropteraClassID,
		pauseCh:            make(chan struct{}),
		resumeCh:           make(chan struct{}),
		stopCh:             make(chan struct{}),
		maxConsecErrors:    maxConsecErrors,
		rateSamples:        make([]rateSample, DefaultRateWindowSize),
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
		// Clear phase on exit
		_ = w.stateManager.SetCurrentPhase(entities.MigrationPhaseNone)
	}()

	w.logger.Info("migration worker started")

	// Set initial phase to detections
	if err := w.stateManager.SetCurrentPhase(entities.MigrationPhaseDetections); err != nil {
		w.logger.Warn("failed to set migration phase", logger.Error(err))
	}

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

// handleValidatingState runs validation with auto-retry and catch-up logic.
// If validation fails due to small count differences (from records added during migration),
// it attempts to catch up by migrating the new records and retrying validation.
func (w *Worker) handleValidatingState(ctx context.Context) runAction {
	for attempt := 1; attempt <= validationMaxRetries; attempt++ {
		w.logger.Info("running validation", logger.Int("attempt", attempt))

		// Wait for in-flight dual-writes to complete
		select {
		case <-ctx.Done():
			return runActionReturn
		case <-time.After(validationPreDelay):
		}

		// Run validation and get counts for recovery logic
		legacyCount, v2Count, err := w.validateWithCounts(ctx)
		if err == nil {
			// Validation passed - complete the migration
			return w.completeValidation(ctx)
		}

		// Check if this is a recoverable count mismatch
		diff := legacyCount - v2Count
		if diff > 0 && diff <= validationCatchUpThreshold {
			w.logger.Info("validation: count mismatch, attempting catch-up",
				logger.Int64("legacy_count", legacyCount),
				logger.Int64("v2_count", v2Count),
				logger.Int64("difference", diff),
				logger.Int("attempt", attempt))

			// Run catch-up to migrate new records
			caught, catchErr := w.runCatchUp(ctx)
			if catchErr != nil {
				w.logger.Warn("catch-up failed", logger.Error(catchErr))
			} else {
				w.logger.Info("catch-up completed", logger.Int64("records_caught", caught))
			}
			continue // Retry validation
		}

		// Non-recoverable error or large difference - fail
		w.logger.Error("validation failed", logger.Error(err),
			logger.Int64("legacy_count", legacyCount),
			logger.Int64("v2_count", v2Count))
		if setErr := w.stateManager.SetError(fmt.Sprintf("validation failed: %v", err)); setErr != nil {
			w.logger.Warn("failed to set error in state", logger.Error(setErr))
		}
		w.pauseOnValidationFailure()
		return runActionContinue
	}

	// Exhausted retries
	w.logger.Error("validation failed after max retries",
		logger.Int("max_retries", validationMaxRetries))
	if setErr := w.stateManager.SetError("validation failed after maximum retry attempts"); setErr != nil {
		w.logger.Warn("failed to set error in state", logger.Error(setErr))
	}
	w.pauseOnValidationFailure()
	return runActionContinue
}

// completeValidation handles the successful validation path.
func (w *Worker) completeValidation(ctx context.Context) runAction {
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

	// Send completion notification
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

// runCatchUp finds and migrates records that exist in legacy but not in V2.
// Unlike processBatch which continues from LastMigratedID, this function
// scans the entire legacy database to find actually missing records.
// Returns the number of records caught up.
// Has a safety limit (catchUpMaxBatches) to prevent infinite loops.
func (w *Worker) runCatchUp(ctx context.Context) (int64, error) {
	var totalCaught int64
	var lastID uint

	for batch := range catchUpMaxBatches {
		// Check for cancellation
		select {
		case <-ctx.Done():
			return totalCaught, ctx.Err()
		default:
		}

		// Fetch a batch of legacy records starting from lastID
		filters := datastore.NewDetectionFilters().
			WithMinID(lastID).
			WithLimit(w.batchSize)

		results, _, err := w.legacy.Search(ctx, filters)
		if err != nil {
			return totalCaught, fmt.Errorf("failed to fetch legacy records for catch-up: %w", err)
		}

		if len(results) == 0 {
			break // No more records
		}

		// Collect IDs from this batch
		ids := make([]uint, len(results))
		idToResult := make(map[uint]*detection.Result, len(results))
		for i, r := range results {
			ids[i] = r.ID
			idToResult[r.ID] = r
			lastID = r.ID // Track for next batch
		}

		// Find which IDs already exist in V2
		existingIDs, err := w.v2Detection.FilterExistingIDs(ctx, ids)
		if err != nil {
			return totalCaught, fmt.Errorf("failed to filter existing IDs: %w", err)
		}

		// Create set of existing IDs for quick lookup
		existingSet := make(map[uint]bool, len(existingIDs))
		for _, id := range existingIDs {
			existingSet[id] = true
		}

		// Find missing IDs and migrate them
		var migratedInBatch int64
		for _, id := range ids {
			if existingSet[id] {
				continue // Already exists in V2
			}

			// This record is missing from V2 - migrate it
			r := idToResult[id]
			if err := w.migrateRecord(ctx, r); err != nil {
				w.logger.Warn("failed to migrate missing record during catch-up",
					logger.Uint64("id", uint64(id)),
					logger.Error(err))
				if addErr := w.stateManager.AddDirtyID(id); addErr != nil {
					w.logger.Error("failed to track dirty ID", logger.Uint64("id", uint64(id)), logger.Error(addErr))
				}
				continue
			}
			migratedInBatch++
			totalCaught++
		}

		if migratedInBatch > 0 {
			w.logger.Info("catch-up batch found missing records",
				logger.Int64("migrated", migratedInBatch),
				logger.Int("batch_size", len(results)),
				logger.Int("batch_number", batch+1))
		}

		// If we processed fewer than batch size, we're done
		if len(results) < w.batchSize {
			break
		}
	}

	return totalCaught, nil
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
		w.transitionToValidation(ctx)
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
// The ctx parameter is used as the parent context for related data migration, ensuring
// cancellation from Start(ctx) propagates to the migration.
func (w *Worker) transitionToValidation(ctx context.Context) {
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
		// Note: MigrateAll handles phase transitions via SetPhaseWithProgress
		// which properly sets both phase name and phase_number/total_phases

		// Create context that cancels on shutdown OR when caller context is cancelled
		migrateCtx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() {
			select {
			case <-w.stopCh:
				cancel()
			case <-migrateCtx.Done():
				// Parent context cancelled
			case <-done:
			}
		}()

		migrateErr := w.relatedMigrator.MigrateAll(migrateCtx)
		close(done)
		cancel() // Cleanup context resources

		if migrateErr != nil {
			w.logger.Error("related data migration failed", logger.Error(migrateErr))
			// Persist error in state so operators can see it in API/UI
			if setErr := w.stateManager.SetRelatedDataError(migrateErr.Error()); setErr != nil {
				w.logger.Warn("failed to persist related data error", logger.Error(setErr))
			}
			// Continue to validation - related data is non-fatal but now tracked
		}
	}

	// Migrate auxiliary data (weather, thresholds, image cache, notifications)
	if w.auxiliaryMigrator != nil {
		w.logger.Info("starting auxiliary data migration")

		// Create context that cancels on shutdown OR when caller context is cancelled
		auxCtx, auxCancel := context.WithCancel(ctx)
		auxDone := make(chan struct{})
		go func() {
			select {
			case <-w.stopCh:
				auxCancel()
			case <-auxCtx.Done():
				// Parent context cancelled
			case <-auxDone:
			}
		}()

		auxResult, auxErr := w.auxiliaryMigrator.MigrateAll(auxCtx)
		close(auxDone)
		auxCancel() // Cleanup context resources

		if auxErr != nil {
			w.logger.Error("auxiliary data migration failed", logger.Error(auxErr))
		} else if auxResult != nil {
			w.logger.Info("auxiliary data migration completed",
				logger.Int("image_caches_migrated", auxResult.ImageCaches.Migrated),
				logger.Int("thresholds_migrated", auxResult.Thresholds.Migrated),
				logger.Int("weather_daily_migrated", auxResult.Weather.DailyEventsMigrated),
				logger.Int("weather_hourly_migrated", auxResult.Weather.HourlyWeatherMigrated),
				logger.Int("notifications_migrated", auxResult.Notifications.Migrated))

			if auxResult.HasErrors() {
				// Log individual errors for each section that failed
				if auxResult.ImageCaches.Error != nil {
					w.logger.Warn("image cache migration had errors",
						logger.Int("total", auxResult.ImageCaches.Total),
						logger.Int("migrated", auxResult.ImageCaches.Migrated),
						logger.Error(auxResult.ImageCaches.Error))
				}
				if auxResult.Thresholds.Error != nil {
					w.logger.Warn("threshold migration had errors",
						logger.Int("total", auxResult.Thresholds.Total),
						logger.Int("migrated", auxResult.Thresholds.Migrated),
						logger.Error(auxResult.Thresholds.Error))
				}
				if auxResult.ThresholdEvents.Error != nil {
					w.logger.Warn("threshold events migration had errors",
						logger.Int("total", auxResult.ThresholdEvents.Total),
						logger.Int("migrated", auxResult.ThresholdEvents.Migrated),
						logger.Error(auxResult.ThresholdEvents.Error))
				}
				if auxResult.Notifications.Error != nil {
					w.logger.Warn("notifications migration had errors",
						logger.Int("total", auxResult.Notifications.Total),
						logger.Int("migrated", auxResult.Notifications.Migrated),
						logger.Error(auxResult.Notifications.Error))
				}
				if auxResult.Weather.Error != nil {
					w.logger.Warn("weather migration had errors",
						logger.Int("daily_total", auxResult.Weather.DailyEventsTotal),
						logger.Int("daily_migrated", auxResult.Weather.DailyEventsMigrated),
						logger.Int("hourly_total", auxResult.Weather.HourlyWeatherTotal),
						logger.Int("hourly_migrated", auxResult.Weather.HourlyWeatherMigrated),
						logger.Error(auxResult.Weather.Error))
				}
			}
		}
	}

	// Clear the phase after related data migration (also resets phase_number/total_phases)
	if err := w.stateManager.SetPhaseWithProgress(entities.MigrationPhaseNone, 0, 0, 0); err != nil {
		w.logger.Warn("failed to clear migration phase", logger.Error(err))
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

// validateWithCounts performs validation and returns the counts for recovery logic.
// Returns legacy count, v2 count, and error if validation fails.
func (w *Worker) validateWithCounts(ctx context.Context) (legacyCount, v2Count int64, err error) {
	w.logger.Info("starting migration validation")

	// Count legacy records
	legacyCount, err = w.countLegacyRecords(ctx)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to count legacy records: %w", err)
	}

	// Count V2 records
	v2Count, err = w.v2Detection.CountAll(ctx)
	if err != nil {
		return legacyCount, 0, fmt.Errorf("failed to count v2 records: %w", err)
	}

	w.logger.Info("validation record counts",
		logger.Int64("legacy", legacyCount),
		logger.Int64("v2", v2Count))

	// V2 should have at least as many records as legacy (could have more from dual-write)
	if v2Count < legacyCount {
		return legacyCount, v2Count, fmt.Errorf("v2 count (%d) is less than legacy count (%d)", v2Count, legacyCount)
	}

	// Check for dirty IDs
	dirtyCount, err := w.stateManager.GetDirtyIDCount()
	if err != nil {
		return legacyCount, v2Count, fmt.Errorf("failed to get dirty ID count: %w", err)
	}

	if dirtyCount > 0 {
		return legacyCount, v2Count, fmt.Errorf("%d records failed to migrate (dirty IDs)", dirtyCount)
	}

	w.logger.Info("validation passed",
		logger.Int64("legacy_count", legacyCount),
		logger.Int64("v2_count", v2Count),
		logger.Int64("dirty_count", dirtyCount))

	return legacyCount, v2Count, nil
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

	// Use batch mode for MySQL (much more efficient with network latency)
	if w.useBatchMode {
		return w.processBatchEfficient(ctx, results)
	}

	// Default: process each record individually (works well for SQLite)
	return w.processBatchSequential(ctx, results)
}

// processBatchSequential processes records one at a time (efficient for SQLite).
func (w *Worker) processBatchSequential(ctx context.Context, results []*detection.Result) (batchResult, error) {
	var result batchResult
	var batchFailures int
	var firstError error

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
			batchFailures++
			if firstError == nil {
				firstError = err
			}
			w.logger.Warn("failed to migrate record", logger.Uint64("id", uint64(r.ID)), logger.Error(err))
			if addErr := w.stateManager.AddDirtyID(r.ID); addErr != nil {
				w.logger.Error("failed to track dirty ID", logger.Uint64("id", uint64(r.ID)), logger.Error(addErr))
			}
			result.lastID = r.ID
			continue
		}
		result.migrated++
		result.lastID = r.ID
	}

	if batchFailures == len(results) && len(results) > 0 {
		return result, fmt.Errorf("systemic failure: all %d records in batch failed: %w", len(results), firstError)
	}

	return result, nil
}

// processBatchEfficient uses bulk operations to minimize database round-trips.
// This is much faster for MySQL where network latency dominates.
func (w *Worker) processBatchEfficient(ctx context.Context, results []*detection.Result) (batchResult, error) {
	var result batchResult

	// Check for pause/stop before processing
	if interrupted, err := w.checkBatchInterruption(ctx); interrupted {
		result.incomplete = true
		return result, err
	}

	// Collect all IDs and set last ID
	ids := make([]uint, len(results))
	for i, r := range results {
		ids[i] = r.ID
		result.lastID = r.ID
	}

	// Query which IDs exist and which are locked (2 queries vs N*2)
	existingIDs, lockedIDs, err := w.v2Detection.GetExistingAndLockedIDs(ctx, ids)
	if err != nil {
		return result, fmt.Errorf("failed to check existing IDs: %w", err)
	}

	// Categorize records
	newRecords, updateRecords, skippedLocked := w.categorizeRecords(results, existingIDs, lockedIDs)
	result.migrated += skippedLocked // Locked records count as migrated

	// Batch insert new records
	if len(newRecords) > 0 {
		migrated, err := w.batchInsertNewRecords(ctx, newRecords)
		result.migrated += migrated
		if err != nil {
			return result, err
		}
	}

	// Update existing unlocked records
	result.migrated += w.updateExistingRecords(ctx, updateRecords)

	if skippedLocked > 0 {
		w.logger.Debug("skipped locked detections in batch", logger.Int("count", skippedLocked))
	}

	return result, nil
}

// checkBatchInterruption checks if batch should be interrupted due to pause/stop/cancel.
func (w *Worker) checkBatchInterruption(ctx context.Context) (bool, error) {
	select {
	case <-ctx.Done():
		return true, ctx.Err()
	case <-w.stopCh:
		return true, ErrMigrationCancelled
	default:
	}

	w.mu.RLock()
	paused := w.paused
	w.mu.RUnlock()
	if paused {
		return true, ErrMigrationPaused
	}
	return false, nil
}

// categorizeRecords separates records into new, existing-unlocked, and locked categories.
func (w *Worker) categorizeRecords(results []*detection.Result, existingIDs, lockedIDs map[uint]bool) (
	newRecords, updateRecords []*detection.Result, skippedLocked int,
) {
	for _, r := range results {
		if lockedIDs[r.ID] {
			skippedLocked++
			continue
		}
		if existingIDs[r.ID] {
			updateRecords = append(updateRecords, r)
		} else {
			newRecords = append(newRecords, r)
		}
	}
	return
}

// batchInsertNewRecords converts and inserts new records in batch.
// Returns number migrated and any systemic error.
func (w *Worker) batchInsertNewRecords(ctx context.Context, records []*detection.Result) (int, error) {
	newDets := make([]*entities.Detection, 0, len(records))
	var conversionErrors int
	var firstConvErr error

	for _, r := range records {
		det, convErr := w.convertToV2Detection(ctx, r)
		if convErr != nil {
			conversionErrors++
			if firstConvErr == nil {
				firstConvErr = convErr
			}
			w.logger.Warn("failed to convert record", logger.Uint64("id", uint64(r.ID)), logger.Error(convErr))
			w.trackDirtyID(r.ID)
			continue
		}
		newDets = append(newDets, det)
	}

	// Batch insert all new detections
	migrated := 0
	if len(newDets) > 0 {
		if insertErr := w.v2Detection.SaveBatchWithIDs(ctx, newDets); insertErr != nil {
			// Fall back to individual inserts to identify which ones fail
			w.logger.Warn("batch insert failed, falling back to individual inserts", logger.Error(insertErr))
			migrated = w.insertRecordsIndividually(ctx, newDets)
		} else {
			migrated = len(newDets)
		}
	}

	// Handle systemic conversion failures
	if conversionErrors == len(records) && len(records) > 0 {
		return migrated, fmt.Errorf("systemic failure: all %d new records failed conversion: %w", len(records), firstConvErr)
	}

	return migrated, nil
}

// insertRecordsIndividually saves records one at a time when batch insert fails.
func (w *Worker) insertRecordsIndividually(ctx context.Context, dets []*entities.Detection) int {
	migrated := 0
	for _, det := range dets {
		if saveErr := w.v2Detection.SaveWithID(ctx, det); saveErr != nil {
			w.logger.Warn("failed to save record", logger.Uint64("id", uint64(det.ID)), logger.Error(saveErr))
			w.trackDirtyID(det.ID)
		} else {
			migrated++
		}
	}
	return migrated
}

// updateExistingRecords updates existing unlocked records. Returns count migrated.
func (w *Worker) updateExistingRecords(ctx context.Context, records []*detection.Result) int {
	migrated := 0
	for _, r := range records {
		det, convErr := w.convertToV2Detection(ctx, r)
		if convErr != nil {
			w.logger.Warn("failed to convert record for update", logger.Uint64("id", uint64(r.ID)), logger.Error(convErr))
			w.trackDirtyID(r.ID)
			continue
		}

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

		if updateErr := w.v2Detection.Update(ctx, r.ID, updates); updateErr != nil {
			w.logger.Warn("failed to update record", logger.Uint64("id", uint64(r.ID)), logger.Error(updateErr))
			w.trackDirtyID(r.ID)
			continue
		}
		migrated++
	}
	return migrated
}

// trackDirtyID adds an ID to the dirty ID list for later reconciliation.
func (w *Worker) trackDirtyID(id uint) {
	if addErr := w.stateManager.AddDirtyID(id); addErr != nil {
		w.logger.Error("failed to track dirty ID", logger.Uint64("id", uint64(id)), logger.Error(addErr))
	}
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
		// Record already exists - check if it's locked
		locked, lockErr := w.v2Detection.IsLocked(ctx, result.ID)
		if lockErr == nil && locked {
			// Locked detections are user-verified; skip update and consider migrated
			w.logger.Debug("skipping update for locked detection",
				logger.Uint64("id", uint64(result.ID)))
			return nil
		}

		// Update existing unlocked record - legacy is source of truth during migration
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
	deps := &repository.ConversionDeps{
		LabelRepo:          w.labelRepo,
		ModelRepo:          w.modelRepo,
		SourceRepo:         w.sourceRepo,
		Logger:             w.logger,
		SpeciesLabelTypeID: w.speciesLabelTypeID,
	}
	if w.avesClassID != nil {
		deps.AvesClassID = *w.avesClassID
	}
	if w.chiropteraClassID != nil {
		deps.ChiropteraClassID = *w.chiropteraClassID
	}
	return deps
}

// convertToV2Detection converts a domain Result to a v2 Detection entity.
func (w *Worker) convertToV2Detection(ctx context.Context, result *detection.Result) (*entities.Detection, error) {
	return repository.ConvertToV2Detection(ctx, result, w.conversionDeps())
}
