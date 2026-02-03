package repository

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
	apperrors "github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// parseDetectionID converts a string ID to uint.
// Returns 0 and error if the ID is invalid.
func parseDetectionID(id string) (uint, error) {
	parsed, err := strconv.ParseUint(id, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid detection ID %q: %w", id, err)
	}
	return uint(parsed), nil
}

// Concurrency control constants for dual-write operations.
const (
	// defaultMaxConcurrentWrites limits concurrent V2 writes to prevent resource exhaustion.
	defaultMaxConcurrentWrites = 10
	// defaultWriteTimeout is the maximum time for a V2 write operation.
	defaultWriteTimeout = 30 * time.Second
	// reconcileInterval is how often the dirty ID reconciliation loop runs.
	reconcileInterval = 60 * time.Second
	// reconcileBatchSize is how many dirty IDs to process per reconciliation tick.
	reconcileBatchSize = 50
)

// DualWriteRepository wraps legacy and v2 repositories for migration.
// It implements the datastore.DetectionRepository interface while writing
// to both legacy (authoritative) and v2 databases during migration.
type DualWriteRepository struct {
	legacy       datastore.DetectionRepository
	v2           DetectionRepository
	stateManager *v2.StateManager
	labelRepo    LabelRepository
	modelRepo    ModelRepository
	sourceRepo   AudioSourceRepository
	logger       logger.Logger
	semaphore    chan struct{} // Limits concurrent V2 writes
	writeTimeout time.Duration // Timeout for V2 write operations
	shutdownCh   chan struct{} // Signals shutdown to in-flight goroutines
	shutdownOnce sync.Once     // Ensures shutdown is called only once

	reconcileTicker *time.Ticker  // Periodic dirty ID reconciliation
	reconcileDone   chan struct{} // Signals reconciliation goroutine has exited
	reconcileOnce   sync.Once     // Ensures StartReconciliation is called only once

	// Cached lookup table IDs
	speciesLabelTypeID uint
	avesClassID        uint
	chiropteraClassID  uint
}

// DualWriteConfig configures the dual-write repository.
type DualWriteConfig struct {
	Legacy       datastore.DetectionRepository
	V2           DetectionRepository
	StateManager *v2.StateManager
	LabelRepo    LabelRepository
	ModelRepo    ModelRepository
	SourceRepo   AudioSourceRepository
	Logger       logger.Logger

	// Cached lookup table IDs (required)
	SpeciesLabelTypeID uint
	AvesClassID        uint
	ChiropteraClassID  uint
}

// NewDualWriteRepository creates a new dual-write repository.
// Logger is required - caller must provide a valid logger.Logger instance.
// Cached lookup table IDs (SpeciesLabelTypeID, AvesClassID, ChiropteraClassID) must be initialized.
func NewDualWriteRepository(cfg *DualWriteConfig) *DualWriteRepository {
	return &DualWriteRepository{
		legacy:             cfg.Legacy,
		v2:                 cfg.V2,
		stateManager:       cfg.StateManager,
		labelRepo:          cfg.LabelRepo,
		modelRepo:          cfg.ModelRepo,
		sourceRepo:         cfg.SourceRepo,
		logger:             cfg.Logger,
		semaphore:          make(chan struct{}, defaultMaxConcurrentWrites),
		writeTimeout:       defaultWriteTimeout,
		shutdownCh:         make(chan struct{}),
		speciesLabelTypeID: cfg.SpeciesLabelTypeID,
		avesClassID:        cfg.AvesClassID,
		chiropteraClassID:  cfg.ChiropteraClassID,
	}
}

// Shutdown signals all goroutines to stop and waits for in-flight operations to drain.
// Any pending writes that haven't started will be marked as dirty for later reconciliation.
func (dw *DualWriteRepository) Shutdown() {
	dw.shutdownOnce.Do(func() {
		close(dw.shutdownCh)

		// Stop reconciliation ticker and wait for goroutine to exit
		if dw.reconcileTicker != nil {
			dw.reconcileTicker.Stop()
			<-dw.reconcileDone
		}

		// Wait for all in-flight goroutines to release the semaphore
		// by filling it completely (blocking until all slots are available)
		for range defaultMaxConcurrentWrites {
			dw.semaphore <- struct{}{}
		}
		dw.logger.Info("dual-write repository shutdown complete")
	})
}

// StartReconciliation starts a background goroutine that periodically processes
// dirty IDs. Dirty IDs accumulate when v2 writes fail during dual-write mode.
// Legacy is used as the source of truth: if a record exists in legacy, it is
// synced to v2; if it was deleted from legacy, the v2 ghost is removed.
func (dw *DualWriteRepository) StartReconciliation() {
	dw.reconcileOnce.Do(func() {
		dw.reconcileTicker = time.NewTicker(reconcileInterval)
		dw.reconcileDone = make(chan struct{})

		go func() {
			defer close(dw.reconcileDone)
			for {
				select {
				case <-dw.shutdownCh:
					return
				case <-dw.reconcileTicker.C:
					dw.reconcileDirtyIDs()
				}
			}
		}()

		dw.logger.Info("dirty ID reconciliation started", logger.Int("interval_seconds", int(reconcileInterval.Seconds())))
	})
}

// reconcileDirtyIDs processes a batch of dirty IDs using legacy as source of truth.
func (dw *DualWriteRepository) reconcileDirtyIDs() {
	count, err := dw.stateManager.GetDirtyIDCount()
	if err != nil {
		dw.logger.Warn("reconciliation: failed to get dirty ID count", logger.Error(err))
		return
	}
	if count == 0 {
		return
	}

	ids, err := dw.stateManager.GetDirtyIDsBatch(reconcileBatchSize)
	if err != nil {
		dw.logger.Warn("reconciliation: failed to get dirty IDs batch", logger.Error(err))
		return
	}

	reconciled := 0
	for _, id := range ids {
		// Check for shutdown between iterations
		select {
		case <-dw.shutdownCh:
			return
		default:
		}

		// Wrap per-ID logic in a function so defer cancel() handles all exit paths.
		func() {
			ctx, cancel := context.WithTimeout(context.Background(), dw.writeTimeout)
			defer cancel()
			idStr := strconv.FormatUint(uint64(id), 10)

			// Fetch from legacy (source of truth)
			result, err := dw.legacy.Get(ctx, idStr)
			if err != nil {
				if apperrors.IsNotFound(err) {
					// Record genuinely deleted from legacy — remove v2 ghost
					if delErr := dw.v2.Delete(ctx, id); delErr != nil && !errors.Is(delErr, ErrDetectionNotFound) {
						dw.logger.Warn("reconciliation: v2 delete failed", logger.Uint64("id", uint64(id)), logger.Error(delErr))
						return
					}
					// Ghost removed (or never existed in v2) — clear dirty ID
					if rmErr := dw.stateManager.RemoveDirtyID(id); rmErr != nil {
						dw.logger.Warn("reconciliation: failed to remove dirty ID", logger.Uint64("id", uint64(id)), logger.Error(rmErr))
					} else {
						reconciled++
					}
					return
				}
				// Transient legacy error — skip, retry next cycle
				dw.logger.Warn("reconciliation: legacy fetch failed", logger.Uint64("id", uint64(id)), logger.Error(err))
				return
			}

			// Record exists in legacy — fetch additional results and sync to v2
			additionalResults, err := dw.legacy.GetAdditionalResults(ctx, idStr)
			if err != nil {
				dw.logger.Warn("reconciliation: legacy additional results fetch failed", logger.Uint64("id", uint64(id)), logger.Error(err))
				return
			}

			if syncErr := dw.syncToV2(ctx, result, additionalResults); syncErr != nil {
				dw.logger.Warn("reconciliation: sync to v2 failed", logger.Uint64("id", uint64(id)), logger.Error(syncErr))
				return
			}

			// Sync succeeded — clear dirty ID
			if rmErr := dw.stateManager.RemoveDirtyID(id); rmErr != nil {
				dw.logger.Warn("reconciliation: failed to remove dirty ID", logger.Uint64("id", uint64(id)), logger.Error(rmErr))
			} else {
				reconciled++
			}
		}()
	}

	remaining := max(int(count)-reconciled, 0)
	dw.logger.Info("reconciliation complete", logger.Int("reconciled", reconciled), logger.Int("remaining", remaining))
}

// ============================================================================
// datastore.DetectionRepository Implementation
// ============================================================================

// Save persists a detection result and its additional predictions.
func (dw *DualWriteRepository) Save(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) error {
	// 1. Save to legacy (authoritative, gets ID assigned)
	if err := dw.legacy.Save(ctx, result, additionalResults); err != nil {
		return err
	}

	// 2. If dual-write enabled, save to v2 with same ID
	dualWrite, err := dw.stateManager.IsInDualWriteMode()
	if err != nil {
		dw.logger.Warn("failed to check dual-write mode", logger.Error(err))
		return nil // Don't fail the save
	}

	if dualWrite {
		// Check for shutdown BEFORE attempting to acquire semaphore to prevent deadlock.
		// If we check shutdownCh inside the select with semaphore acquisition, we could
		// deadlock: Shutdown() fills semaphore while Save() blocks trying to acquire it.
		select {
		case <-dw.shutdownCh:
			// Shutdown in progress - mark as dirty for later reconciliation
			if err := dw.stateManager.AddDirtyID(result.ID); err != nil {
				dw.logger.Warn("failed to persist dirty ID during shutdown", logger.Uint64("id", uint64(result.ID)), logger.Error(err))
			}
			return nil
		default:
		}

		// Try to acquire semaphore to limit concurrent V2 writes
		select {
		case dw.semaphore <- struct{}{}:
			// Acquired semaphore - spawn bounded goroutine
			go func() {
				defer func() { <-dw.semaphore }()
				// Double-check shutdown after acquiring semaphore
				select {
				case <-dw.shutdownCh:
					// Shutdown signaled after we acquired - mark dirty and exit
					if err := dw.stateManager.AddDirtyID(result.ID); err != nil {
						dw.logger.Warn("failed to persist dirty ID during shutdown", logger.Uint64("id", uint64(result.ID)), logger.Error(err))
					}
					return
				default:
				}
				ctx, cancel := context.WithTimeout(context.Background(), dw.writeTimeout)
				defer cancel()
				dw.saveToV2(ctx, result, additionalResults)
			}()
		default:
			// Semaphore full - persist to dirty table for later processing
			if err := dw.stateManager.AddDirtyID(result.ID); err != nil {
				dw.logger.Warn("failed to persist dirty ID", logger.Uint64("id", uint64(result.ID)), logger.Error(err))
			}
		}
	}

	return nil
}

// syncToV2 performs the core v2 save/upsert logic and returns an error on failure.
// This is the inner logic used by both the async save path and the reconciliation loop.
func (dw *DualWriteRepository) syncToV2(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) error {
	// Convert domain model to v2 entity
	det, err := dw.convertToV2Detection(ctx, result)
	if err != nil {
		return fmt.Errorf("v2 conversion failed: %w", err)
	}

	// Check if detection already exists (upsert pattern)
	exists, err := dw.v2.Exists(ctx, det.ID)
	if err != nil {
		return fmt.Errorf("v2 existence check failed: %w", err)
	}

	if exists {
		// Update existing record
		updates := map[string]any{
			"label_id":    det.LabelID,
			"model_id":    det.ModelID,
			"confidence":  det.Confidence,
			"detected_at": det.DetectedAt,
		}
		if det.SourceID != nil {
			updates["source_id"] = *det.SourceID
		}
		if det.ClipName != nil {
			updates["clip_name"] = *det.ClipName
		}
		if err := dw.v2.Update(ctx, det.ID, updates); err != nil {
			// ErrDetectionLocked is acceptable - record is protected
			if !errors.Is(err, ErrDetectionLocked) {
				return fmt.Errorf("v2 update failed: %w", err)
			}
		}
	} else {
		// Save new record with preserved ID
		if err := dw.v2.SaveWithID(ctx, det); err != nil {
			return fmt.Errorf("v2 save failed: %w", err)
		}
	}

	// Save additional predictions
	if len(additionalResults) > 0 {
		// Get model type for predictions (default to bird if not available)
		modelType := entities.ModelTypeBird
		if model, err := dw.modelRepo.GetByID(ctx, det.ModelID); err == nil {
			modelType = model.ModelType
		}

		preds, err := dw.convertToPredictions(ctx, det.ID, det.ModelID, modelType, additionalResults)
		if err != nil {
			return fmt.Errorf("v2 prediction conversion failed: %w", err)
		}
		if err := dw.v2.SavePredictions(ctx, det.ID, preds); err != nil {
			return fmt.Errorf("v2 predictions save failed: %w", err)
		}
	}

	return nil
}

// saveToV2 performs the v2 save asynchronously, marking dirty on failure.
func (dw *DualWriteRepository) saveToV2(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) {
	if err := dw.syncToV2(ctx, result, additionalResults); err != nil {
		dw.logger.Warn("v2 write failed", logger.Uint64("id", uint64(result.ID)), logger.Error(err))
		dw.markDirty(result.ID)
	}
}

// markDirty persists a detection ID to the dirty table for later reconciliation.
func (dw *DualWriteRepository) markDirty(id uint) {
	if err := dw.stateManager.AddDirtyID(id); err != nil {
		dw.logger.Warn("failed to persist dirty ID", logger.Uint64("id", uint64(id)), logger.Error(err))
	}
}

// Get retrieves a detection by ID.
func (dw *DualWriteRepository) Get(ctx context.Context, id string) (*detection.Result, error) {
	// Check if we should read from v2
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		dw.logger.Warn("failed to check read source", logger.Error(err))
		readFromV2 = false
	}

	if readFromV2 {
		return dw.getFromV2(ctx, id)
	}

	return dw.legacy.Get(ctx, id)
}

// getFromV2 retrieves a detection from v2 and converts to domain model.
func (dw *DualWriteRepository) getFromV2(ctx context.Context, id string) (*detection.Result, error) {
	// Parse ID
	uid, err := parseDetectionID(id)
	if err != nil {
		return nil, err
	}

	det, err := dw.v2.GetWithRelations(ctx, uid)
	if err != nil {
		if errors.Is(err, ErrDetectionNotFound) {
			// Fall back to legacy if not found in v2
			return dw.legacy.Get(ctx, id)
		}
		return nil, err
	}

	return dw.convertFromV2Detection(det)
}

// Delete removes a detection by ID.
func (dw *DualWriteRepository) Delete(ctx context.Context, id string) error {
	// 1. Delete from legacy
	if err := dw.legacy.Delete(ctx, id); err != nil {
		return err
	}

	// 2. If dual-write enabled, delete from v2
	dualWrite, err := dw.stateManager.IsInDualWriteMode()
	if err != nil {
		dw.logger.Warn("failed to check dual-write mode", logger.Error(err))
		return nil
	}

	if dualWrite {
		uid, err := parseDetectionID(id)
		if err == nil {
			if err := dw.v2.Delete(ctx, uid); err != nil {
				if !errors.Is(err, ErrDetectionNotFound) {
					dw.logger.Warn("v2 delete failed", logger.String("id", id), logger.Error(err))
					// Track for reconciliation - V2 record may still exist after legacy deletion.
					// The reconciliation loop in StartReconciliation handles these dirty IDs.
					dw.markDirty(uid)
				}
			}
		}
	}

	return nil
}

// GetRecent retrieves the most recent detections.
func (dw *DualWriteRepository) GetRecent(ctx context.Context, limit int) ([]*detection.Result, error) {
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		readFromV2 = false
	}

	if readFromV2 {
		dets, err := dw.v2.GetRecent(ctx, limit)
		if err != nil {
			return nil, err
		}
		return dw.convertFromV2Detections(dets)
	}

	return dw.legacy.GetRecent(ctx, limit)
}

// Search finds detections matching the given filters.
func (dw *DualWriteRepository) Search(ctx context.Context, filters *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		readFromV2 = false
	}

	if readFromV2 {
		v2Filters := dw.convertFilters(filters)
		dets, total, err := dw.v2.Search(ctx, v2Filters)
		if err != nil {
			return nil, 0, err
		}
		results, err := dw.convertFromV2Detections(dets)
		return results, total, err
	}

	return dw.legacy.Search(ctx, filters)
}

// GetBySpecies retrieves detections for a specific species.
// Uses cross-model lookup to find detections across all models.
func (dw *DualWriteRepository) GetBySpecies(ctx context.Context, species string, filters *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		readFromV2 = false
	}

	if readFromV2 {
		// Resolve species name to label IDs (cross-model lookup)
		labelIDs, err := dw.labelRepo.GetLabelIDsByScientificName(ctx, species)
		if err != nil {
			return nil, 0, err
		}
		if len(labelIDs) == 0 {
			// Fall back to legacy if no labels found
			return dw.legacy.GetBySpecies(ctx, species, filters)
		}

		limit := 100
		offset := 0
		if filters != nil {
			if filters.Limit > 0 {
				limit = filters.Limit
			}
			offset = filters.Offset
		}

		// Query across all label IDs for this species (multi-model support)
		searchFilters := &SearchFilters{
			LabelIDs: labelIDs,
			Limit:    limit,
			Offset:   offset,
			SortDesc: true,
		}
		dets, total, err := dw.v2.Search(ctx, searchFilters)
		if err != nil {
			return nil, 0, err
		}
		results, err := dw.convertFromV2Detections(dets)
		return results, total, err
	}

	return dw.legacy.GetBySpecies(ctx, species, filters)
}

// GetByDateRange retrieves detections within a date range.
func (dw *DualWriteRepository) GetByDateRange(ctx context.Context, startDate, endDate string, limit, offset int) ([]*detection.Result, int64, error) {
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		readFromV2 = false
	}

	if readFromV2 {
		// Parse date strings to Unix timestamps using local timezone for consistency with GetHourly
		start, err := time.ParseInLocation("2006-01-02", startDate, time.Local)
		if err != nil {
			return dw.legacy.GetByDateRange(ctx, startDate, endDate, limit, offset)
		}
		end, err := time.ParseInLocation("2006-01-02", endDate, time.Local)
		if err != nil {
			return dw.legacy.GetByDateRange(ctx, startDate, endDate, limit, offset)
		}
		// End of day
		end = end.Add(24*time.Hour - time.Nanosecond)

		dets, total, err := dw.v2.GetByDateRange(ctx, start.Unix(), end.Unix(), limit, offset)
		if err != nil {
			return nil, 0, err
		}
		results, err := dw.convertFromV2Detections(dets)
		return results, total, err
	}

	return dw.legacy.GetByDateRange(ctx, startDate, endDate, limit, offset)
}

// GetHourly retrieves detections for a specific hour on a date.
func (dw *DualWriteRepository) GetHourly(ctx context.Context, date, hour string, duration, limit, offset int) ([]*detection.Result, int64, error) {
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		readFromV2 = false
	}

	if readFromV2 {
		// Parse date and hour to Unix timestamp
		hourInt, err := strconv.Atoi(hour)
		if err != nil {
			return dw.legacy.GetHourly(ctx, date, hour, duration, limit, offset)
		}

		t, err := time.Parse(time.DateOnly, date)
		if err != nil {
			return dw.legacy.GetHourly(ctx, date, hour, duration, limit, offset)
		}
		hourStart := time.Date(t.Year(), t.Month(), t.Day(), hourInt, 0, 0, 0, time.Local)

		dets, total, err := dw.v2.GetByHour(ctx, hourStart.Unix(), limit, offset)
		if err != nil {
			return nil, 0, err
		}
		results, err := dw.convertFromV2Detections(dets)
		return results, total, err
	}

	return dw.legacy.GetHourly(ctx, date, hour, duration, limit, offset)
}

// Lock prevents modification of a detection.
func (dw *DualWriteRepository) Lock(ctx context.Context, id string) error {
	if err := dw.legacy.Lock(ctx, id); err != nil {
		return err
	}

	dualWrite, _ := dw.stateManager.IsInDualWriteMode()
	if dualWrite {
		uid, err := parseDetectionID(id)
		if err == nil {
			if err := dw.v2.Lock(ctx, uid); err != nil {
				dw.logger.Warn("v2 lock failed", logger.String("id", id), logger.Error(err))
			}
		}
	}

	return nil
}

// Unlock removes the lock from a detection.
func (dw *DualWriteRepository) Unlock(ctx context.Context, id string) error {
	if err := dw.legacy.Unlock(ctx, id); err != nil {
		return err
	}

	dualWrite, _ := dw.stateManager.IsInDualWriteMode()
	if dualWrite {
		uid, err := parseDetectionID(id)
		if err == nil {
			if err := dw.v2.Unlock(ctx, uid); err != nil && !errors.Is(err, ErrLockNotFound) {
				dw.logger.Warn("v2 unlock failed", logger.String("id", id), logger.Error(err))
			}
		}
	}

	return nil
}

// IsLocked checks if a detection is locked.
func (dw *DualWriteRepository) IsLocked(ctx context.Context, id string) (bool, error) {
	return dw.legacy.IsLocked(ctx, id)
}

// SetReview sets the verification status for a detection.
func (dw *DualWriteRepository) SetReview(ctx context.Context, id, verified string) error {
	if err := dw.legacy.SetReview(ctx, id, verified); err != nil {
		return err
	}

	dualWrite, _ := dw.stateManager.IsInDualWriteMode()
	if dualWrite {
		uid, err := parseDetectionID(id)
		if err == nil {
			review := &entities.DetectionReview{
				DetectionID: uid,
				Verified:    entities.VerificationStatus(verified),
			}
			if err := dw.v2.SaveReview(ctx, review); err != nil {
				dw.logger.Warn("v2 review save failed", logger.String("id", id), logger.Error(err))
			}
		}
	}

	return nil
}

// GetReview retrieves the verification status for a detection.
func (dw *DualWriteRepository) GetReview(ctx context.Context, id string) (string, error) {
	return dw.legacy.GetReview(ctx, id)
}

// AddComment adds a comment to a detection.
func (dw *DualWriteRepository) AddComment(ctx context.Context, id, comment string) error {
	if err := dw.legacy.AddComment(ctx, id, comment); err != nil {
		return err
	}

	dualWrite, _ := dw.stateManager.IsInDualWriteMode()
	if dualWrite {
		uid, err := parseDetectionID(id)
		if err == nil {
			c := &entities.DetectionComment{
				DetectionID: uid,
				Entry:       comment,
			}
			if err := dw.v2.SaveComment(ctx, c); err != nil {
				dw.logger.Warn("v2 comment save failed", logger.String("id", id), logger.Error(err))
			}
		}
	}

	return nil
}

// GetComments retrieves comments for a detection.
func (dw *DualWriteRepository) GetComments(ctx context.Context, id string) ([]detection.Comment, error) {
	return dw.legacy.GetComments(ctx, id)
}

// UpdateComment updates a comment's content.
func (dw *DualWriteRepository) UpdateComment(ctx context.Context, commentID uint, entry string) error {
	if err := dw.legacy.UpdateComment(ctx, commentID, entry); err != nil {
		return err
	}

	dualWrite, _ := dw.stateManager.IsInDualWriteMode()
	if dualWrite {
		if err := dw.v2.UpdateComment(ctx, commentID, entry); err != nil && !errors.Is(err, ErrCommentNotFound) {
			dw.logger.Warn("v2 comment update failed", logger.Uint64("commentID", uint64(commentID)), logger.Error(err))
		}
	}

	return nil
}

// DeleteComment removes a comment.
func (dw *DualWriteRepository) DeleteComment(ctx context.Context, commentID uint) error {
	if err := dw.legacy.DeleteComment(ctx, commentID); err != nil {
		return err
	}

	dualWrite, _ := dw.stateManager.IsInDualWriteMode()
	if dualWrite {
		if err := dw.v2.DeleteComment(ctx, commentID); err != nil && !errors.Is(err, ErrCommentNotFound) {
			dw.logger.Warn("v2 comment delete failed", logger.Uint64("commentID", uint64(commentID)), logger.Error(err))
		}
	}

	return nil
}

// GetClipPath returns the audio clip path for a detection.
func (dw *DualWriteRepository) GetClipPath(ctx context.Context, id string) (string, error) {
	return dw.legacy.GetClipPath(ctx, id)
}

// GetAdditionalResults returns the secondary predictions for a detection.
func (dw *DualWriteRepository) GetAdditionalResults(ctx context.Context, id string) ([]detection.AdditionalResult, error) {
	return dw.legacy.GetAdditionalResults(ctx, id)
}

// ============================================================================
// Conversion Helpers
// ============================================================================

// conversionDeps returns the dependencies for shared conversion functions.
func (dw *DualWriteRepository) conversionDeps() *ConversionDeps {
	return &ConversionDeps{
		LabelRepo:          dw.labelRepo,
		ModelRepo:          dw.modelRepo,
		SourceRepo:         dw.sourceRepo,
		Logger:             dw.logger,
		SpeciesLabelTypeID: dw.speciesLabelTypeID,
		AvesClassID:        dw.avesClassID,
		ChiropteraClassID:  dw.chiropteraClassID,
	}
}

// convertToV2Detection converts a domain Result to a v2 Detection entity.
func (dw *DualWriteRepository) convertToV2Detection(ctx context.Context, result *detection.Result) (*entities.Detection, error) {
	return ConvertToV2Detection(ctx, result, dw.conversionDeps())
}

// convertToPredictions converts additional results to v2 prediction entities.
// modelID is the ID of the model that produced the detection.
// taxonomicClassID is determined from the model type (Aves for birds, Chiroptera for bats).
func (dw *DualWriteRepository) convertToPredictions(ctx context.Context, detectionID, modelID uint, modelType entities.ModelType, additional []detection.AdditionalResult) ([]*entities.DetectionPrediction, error) {
	var taxonomicClassID *uint
	switch modelType {
	case entities.ModelTypeBird:
		if dw.avesClassID != 0 {
			taxonomicClassID = &dw.avesClassID
		}
	case entities.ModelTypeBat:
		if dw.chiropteraClassID != 0 {
			taxonomicClassID = &dw.chiropteraClassID
		}
	case entities.ModelTypeMulti:
		// Multi-type models can detect multiple taxonomic classes; no default
	}
	return ConvertToPredictions(ctx, detectionID, modelID, dw.speciesLabelTypeID, taxonomicClassID, additional, dw.labelRepo)
}

// convertFromV2Detection converts a v2 Detection entity to a domain Result.
func (dw *DualWriteRepository) convertFromV2Detection(det *entities.Detection) (*detection.Result, error) {
	return ConvertFromV2Detection(det), nil
}

// convertFromV2Detections converts multiple v2 Detection entities to domain Results.
func (dw *DualWriteRepository) convertFromV2Detections(dets []*entities.Detection) ([]*detection.Result, error) {
	return ConvertFromV2Detections(dets), nil
}

// convertFilters converts datastore filters to v2 SearchFilters.
func (dw *DualWriteRepository) convertFilters(filters *datastore.DetectionFilters) *SearchFilters {
	sf := &SearchFilters{
		Query:    filters.Query,
		Limit:    filters.Limit,
		Offset:   filters.Offset,
		SortDesc: !filters.SortAscending,
	}

	// Convert confidence filter
	if filters.Confidence != nil {
		switch filters.Confidence.Operator {
		case ">=", ">":
			sf.MinConfidence = &filters.Confidence.Value
		case "<=", "<":
			sf.MaxConfidence = &filters.Confidence.Value
		case "=":
			sf.MinConfidence = &filters.Confidence.Value
			sf.MaxConfidence = &filters.Confidence.Value
		}
	}

	// Convert locked filter
	if filters.Locked != nil {
		sf.IsLocked = filters.Locked
	}

	// Convert verified filter
	if filters.Verified != nil {
		if *filters.Verified {
			v := VerificationFilter(entities.VerificationCorrect)
			sf.Verified = &v
		}
	}

	return sf
}

// ============================================================================
// Migration Support
// ============================================================================

// GetDirtyIDs returns IDs that need re-sync due to failed v2 writes.
// These are persisted in the database and survive restarts.
// Returns up to batchSize IDs (default 1000 if batchSize <= 0).
func (dw *DualWriteRepository) GetDirtyIDs(batchSize int) []uint {
	if batchSize <= 0 {
		batchSize = 1000 // Sensible default to prevent OOM
	}
	ids, err := dw.stateManager.GetDirtyIDs(batchSize, 0)
	if err != nil {
		dw.logger.Warn("failed to get dirty IDs", logger.Error(err))
		return nil
	}
	return ids
}

// ClearDirtyID removes an ID from the dirty set after successful re-sync.
func (dw *DualWriteRepository) ClearDirtyID(id uint) {
	if err := dw.stateManager.RemoveDirtyID(id); err != nil {
		dw.logger.Warn("failed to remove dirty ID", logger.Uint64("id", uint64(id)), logger.Error(err))
	}
}
