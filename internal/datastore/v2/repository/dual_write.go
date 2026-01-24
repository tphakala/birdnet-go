package repository

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	v2 "github.com/tphakala/birdnet-go/internal/datastore/v2"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/detection"
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
	logger       *slog.Logger
	semaphore    chan struct{}   // Limits concurrent V2 writes
	writeTimeout time.Duration   // Timeout for V2 write operations
	shutdownCh   chan struct{}   // Signals shutdown to in-flight goroutines
	shutdownOnce sync.Once       // Ensures shutdown is called only once
}

// DualWriteConfig configures the dual-write repository.
type DualWriteConfig struct {
	Legacy       datastore.DetectionRepository
	V2           DetectionRepository
	StateManager *v2.StateManager
	LabelRepo    LabelRepository
	ModelRepo    ModelRepository
	SourceRepo   AudioSourceRepository
	Logger       *slog.Logger
}

// NewDualWriteRepository creates a new dual-write repository.
func NewDualWriteRepository(cfg *DualWriteConfig) *DualWriteRepository {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &DualWriteRepository{
		legacy:       cfg.Legacy,
		v2:           cfg.V2,
		stateManager: cfg.StateManager,
		labelRepo:    cfg.LabelRepo,
		modelRepo:    cfg.ModelRepo,
		sourceRepo:   cfg.SourceRepo,
		logger:       logger,
		semaphore:    make(chan struct{}, defaultMaxConcurrentWrites),
		writeTimeout: defaultWriteTimeout,
		shutdownCh:   make(chan struct{}),
	}
}

// Shutdown signals all goroutines to stop and waits for in-flight operations to drain.
// Any pending writes that haven't started will be marked as dirty for later reconciliation.
func (dw *DualWriteRepository) Shutdown() {
	dw.shutdownOnce.Do(func() {
		close(dw.shutdownCh)
		// Wait for all in-flight goroutines to release the semaphore
		// by filling it completely (blocking until all slots are available)
		for range defaultMaxConcurrentWrites {
			dw.semaphore <- struct{}{}
		}
		dw.logger.Info("dual-write repository shutdown complete")
	})
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
		dw.logger.Warn("failed to check dual-write mode", "error", err)
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
				dw.logger.Warn("failed to persist dirty ID during shutdown", "id", result.ID, "error", err)
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
						dw.logger.Warn("failed to persist dirty ID during shutdown", "id", result.ID, "error", err)
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
				dw.logger.Warn("failed to persist dirty ID", "id", result.ID, "error", err)
			}
		}
	}

	return nil
}

// saveToV2 performs the v2 save asynchronously.
func (dw *DualWriteRepository) saveToV2(ctx context.Context, result *detection.Result, additionalResults []detection.AdditionalResult) {
	// Convert domain model to v2 entity
	det, err := dw.convertToV2Detection(ctx, result)
	if err != nil {
		dw.logger.Warn("v2 conversion failed", "id", result.ID, "error", err)
		dw.markDirty(result.ID)
		return
	}

	// Check if detection already exists (upsert pattern)
	exists, err := dw.v2.Exists(ctx, det.ID)
	if err != nil {
		dw.logger.Warn("v2 existence check failed", "id", result.ID, "error", err)
		dw.markDirty(result.ID)
		return
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
				dw.logger.Warn("v2 update failed", "id", result.ID, "error", err)
				dw.markDirty(result.ID)
			}
		}
	} else {
		// Save new record with preserved ID
		if err := dw.v2.SaveWithID(ctx, det); err != nil {
			dw.logger.Warn("v2 save failed", "id", result.ID, "error", err)
			dw.markDirty(result.ID)
			return
		}
	}

	// Save additional predictions
	if len(additionalResults) > 0 {
		preds, err := dw.convertToPredictions(ctx, det.ID, additionalResults)
		if err != nil {
			dw.logger.Warn("v2 prediction conversion failed", "id", result.ID, "error", err)
			return
		}
		if err := dw.v2.SavePredictions(ctx, det.ID, preds); err != nil {
			dw.logger.Warn("v2 predictions save failed", "id", result.ID, "error", err)
		}
	}
}

// markDirty persists a detection ID to the dirty table for later reconciliation.
func (dw *DualWriteRepository) markDirty(id uint) {
	if err := dw.stateManager.AddDirtyID(id); err != nil {
		dw.logger.Warn("failed to persist dirty ID", "id", id, "error", err)
	}
}

// Get retrieves a detection by ID.
func (dw *DualWriteRepository) Get(ctx context.Context, id string) (*detection.Result, error) {
	// Check if we should read from v2
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		dw.logger.Warn("failed to check read source", "error", err)
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
		dw.logger.Warn("failed to check dual-write mode", "error", err)
		return nil
	}

	if dualWrite {
		uid, err := parseDetectionID(id)
		if err == nil {
			if err := dw.v2.Delete(ctx, uid); err != nil {
				if !errors.Is(err, ErrDetectionNotFound) {
					dw.logger.Warn("v2 delete failed", "id", id, "error", err)
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
func (dw *DualWriteRepository) GetBySpecies(ctx context.Context, species string, filters *datastore.DetectionFilters) ([]*detection.Result, int64, error) {
	readFromV2, err := dw.stateManager.ShouldReadFromV2()
	if err != nil {
		readFromV2 = false
	}

	if readFromV2 {
		// Resolve species name to label ID
		label, err := dw.labelRepo.GetByScientificName(ctx, species)
		if err != nil {
			// Fall back to legacy if label not found
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

		dets, total, err := dw.v2.GetByLabel(ctx, label.ID, limit, offset)
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

		t, err := time.Parse("2006-01-02", date)
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
				dw.logger.Warn("v2 lock failed", "id", id, "error", err)
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
				dw.logger.Warn("v2 unlock failed", "id", id, "error", err)
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
				dw.logger.Warn("v2 review save failed", "id", id, "error", err)
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
				dw.logger.Warn("v2 comment save failed", "id", id, "error", err)
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
			dw.logger.Warn("v2 comment update failed", "commentID", commentID, "error", err)
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
			dw.logger.Warn("v2 comment delete failed", "commentID", commentID, "error", err)
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
		LabelRepo:  dw.labelRepo,
		ModelRepo:  dw.modelRepo,
		SourceRepo: dw.sourceRepo,
		Logger:     dw.logger,
	}
}

// convertToV2Detection converts a domain Result to a v2 Detection entity.
func (dw *DualWriteRepository) convertToV2Detection(ctx context.Context, result *detection.Result) (*entities.Detection, error) {
	return ConvertToV2Detection(ctx, result, dw.conversionDeps())
}

// convertToPredictions converts additional results to v2 prediction entities.
func (dw *DualWriteRepository) convertToPredictions(ctx context.Context, detectionID uint, additional []detection.AdditionalResult) ([]*entities.DetectionPrediction, error) {
	return ConvertToPredictions(ctx, detectionID, additional, dw.labelRepo)
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
		dw.logger.Warn("failed to get dirty IDs", "error", err)
		return nil
	}
	return ids
}

// ClearDirtyID removes an ID from the dirty set after successful re-sync.
func (dw *DualWriteRepository) ClearDirtyID(id uint) {
	if err := dw.stateManager.RemoveDirtyID(id); err != nil {
		dw.logger.Warn("failed to remove dirty ID", "id", id, "error", err)
	}
}
