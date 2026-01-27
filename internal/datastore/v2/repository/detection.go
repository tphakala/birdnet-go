package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// DetectionRepository provides access to the normalized v2 detections schema.
// All timestamps are Unix int64 for consistency and indexing performance.
type DetectionRepository interface {
	// === CRUD Operations ===

	// Save persists a new detection. The detection's ID is auto-generated.
	Save(ctx context.Context, det *entities.Detection) error

	// SaveWithID persists a detection with a specific ID (for migration).
	// Use this when preserving legacy IDs during migration.
	SaveWithID(ctx context.Context, det *entities.Detection) error

	// Get retrieves a detection by ID.
	// Returns ErrDetectionNotFound if not found.
	Get(ctx context.Context, id uint) (*entities.Detection, error)

	// GetWithRelations retrieves a detection with preloaded Label, Model, and Source.
	// Returns ErrDetectionNotFound if not found.
	GetWithRelations(ctx context.Context, id uint) (*entities.Detection, error)

	// Update modifies specific fields of a detection.
	// Returns ErrDetectionNotFound if not found, ErrDetectionLocked if locked.
	Update(ctx context.Context, id uint, updates map[string]any) error

	// Delete removes a detection by ID.
	// Returns ErrDetectionNotFound if not found, ErrDetectionLocked if locked.
	Delete(ctx context.Context, id uint) error

	// === Batch Operations (for migration) ===

	// SaveBatch persists multiple detections in a single transaction.
	SaveBatch(ctx context.Context, dets []*entities.Detection) error

	// DeleteBatch removes multiple detections by ID.
	DeleteBatch(ctx context.Context, ids []uint) error

	// === Query Methods ===

	// GetRecent retrieves the most recent detections with preloaded relations.
	GetRecent(ctx context.Context, limit int) ([]*entities.Detection, error)

	// GetByLabel retrieves detections for a specific label.
	// Returns results, total count, and any error.
	GetByLabel(ctx context.Context, labelID uint, limit, offset int) ([]*entities.Detection, int64, error)

	// GetByModel retrieves detections for a specific AI model.
	// Returns results, total count, and any error.
	GetByModel(ctx context.Context, modelID uint, limit, offset int) ([]*entities.Detection, int64, error)

	// GetByDateRange retrieves detections within a Unix timestamp range.
	// Returns results, total count, and any error.
	GetByDateRange(ctx context.Context, start, end int64, limit, offset int) ([]*entities.Detection, int64, error)

	// GetByHour retrieves detections starting at a specific Unix timestamp hour.
	// The hourStart should be the beginning of the hour (e.g., 1640000000 for a full hour).
	// Returns results, total count, and any error.
	GetByHour(ctx context.Context, hourStart int64, limit, offset int) ([]*entities.Detection, int64, error)

	// GetByAudioSource retrieves detections for a specific audio source.
	// Returns results, total count, and any error.
	GetByAudioSource(ctx context.Context, sourceID uint, limit, offset int) ([]*entities.Detection, int64, error)

	// === Search ===

	// Search finds detections matching the given filters.
	// Returns results, total count, and any error.
	Search(ctx context.Context, filters *SearchFilters) ([]*entities.Detection, int64, error)

	// === Counts ===

	// CountAll returns the total number of detections.
	CountAll(ctx context.Context) (int64, error)

	// CountByLabel returns the count of detections for a specific label.
	CountByLabel(ctx context.Context, labelID uint) (int64, error)

	// CountByModel returns the count of detections for a specific model.
	CountByModel(ctx context.Context, modelID uint) (int64, error)

	// CountByDateRange returns the count of detections in a Unix timestamp range.
	CountByDateRange(ctx context.Context, start, end int64) (int64, error)

	// CountByHour returns the count of detections in a specific hour.
	CountByHour(ctx context.Context, hourStart int64) (int64, error)

	// === Aggregations ===

	// GetTopSpecies returns the most frequently detected species in a time range.
	// modelID is optional; pass nil to include all models.
	GetTopSpecies(ctx context.Context, start, end int64, minConfidence float64, modelID *uint, limit int) ([]SpeciesCount, error)

	// GetHourlyOccurrences returns detection counts by hour (0-23) for a label.
	GetHourlyOccurrences(ctx context.Context, labelID uint, start, end int64) ([24]int, error)

	// GetDailyOccurrences returns daily detection counts for a label.
	GetDailyOccurrences(ctx context.Context, labelID uint, start, end int64) ([]DailyCount, error)

	// GetSpeciesFirstDetection returns the first-ever detection of a species.
	// Returns ErrDetectionNotFound if the species has never been detected.
	GetSpeciesFirstDetection(ctx context.Context, labelID uint) (*entities.Detection, error)

	// GetAllDetectedLabels returns IDs of all labels that have at least one detection.
	GetAllDetectedLabels(ctx context.Context) ([]uint, error)

	// === Model-Specific Statistics (NEW for v2) ===

	// GetModelStats returns aggregate statistics for a specific model.
	// Returns ErrModelNotFound if the model doesn't exist.
	GetModelStats(ctx context.Context, modelID uint) (*ModelStats, error)

	// GetSpeciesStatsByModel returns species statistics for a model.
	GetSpeciesStatsByModel(ctx context.Context, labelID, modelID uint) (*SpeciesModelStats, error)

	// GetTopSpeciesByModel returns top species for a specific model.
	GetTopSpeciesByModel(ctx context.Context, modelID uint, limit int) ([]SpeciesCount, error)

	// === Predictions ===

	// SavePredictions stores additional predictions for a detection.
	// Replaces any existing predictions for the detection.
	SavePredictions(ctx context.Context, detectionID uint, preds []*entities.DetectionPrediction) error

	// SavePredictionsBatch stores predictions for multiple detections efficiently.
	// Used during migration for bulk operations.
	SavePredictionsBatch(ctx context.Context, preds []*entities.DetectionPrediction) error

	// GetPredictions retrieves all predictions for a detection.
	GetPredictions(ctx context.Context, detectionID uint) ([]*entities.DetectionPrediction, error)

	// DeletePredictions removes all predictions for a detection.
	DeletePredictions(ctx context.Context, detectionID uint) error

	// === Reviews ===

	// SaveReview creates or updates a review for a detection.
	SaveReview(ctx context.Context, review *entities.DetectionReview) error

	// GetReview retrieves the review for a detection.
	// Returns ErrReviewNotFound if no review exists.
	GetReview(ctx context.Context, detectionID uint) (*entities.DetectionReview, error)

	// UpdateReview updates the verification status for a detection.
	// Returns ErrReviewNotFound if no review exists.
	UpdateReview(ctx context.Context, detectionID uint, verified entities.VerificationStatus) error

	// DeleteReview removes the review for a detection.
	// Returns ErrReviewNotFound if no review exists.
	DeleteReview(ctx context.Context, detectionID uint) error

	// SaveReviewsBatch saves multiple reviews efficiently.
	SaveReviewsBatch(ctx context.Context, reviews []*entities.DetectionReview) error

	// === Comments ===

	// SaveComment adds a comment to a detection.
	SaveComment(ctx context.Context, comment *entities.DetectionComment) error

	// GetComments retrieves all comments for a detection.
	GetComments(ctx context.Context, detectionID uint) ([]*entities.DetectionComment, error)

	// UpdateComment modifies a comment's content.
	// Returns ErrCommentNotFound if not found.
	UpdateComment(ctx context.Context, commentID uint, entry string) error

	// DeleteComment removes a specific comment.
	// Returns ErrCommentNotFound if not found.
	DeleteComment(ctx context.Context, commentID uint) error

	// SaveCommentsBatch saves multiple comments efficiently.
	SaveCommentsBatch(ctx context.Context, comments []*entities.DetectionComment) error

	// === Locks ===

	// Lock prevents modification/deletion of a detection.
	// Returns ErrDetectionNotFound if detection doesn't exist.
	Lock(ctx context.Context, detectionID uint) error

	// Unlock removes the lock from a detection.
	// Returns ErrLockNotFound if not locked.
	Unlock(ctx context.Context, detectionID uint) error

	// IsLocked checks if a detection is locked.
	IsLocked(ctx context.Context, detectionID uint) (bool, error)

	// GetLockedClipPaths returns clip paths for all locked detections.
	GetLockedClipPaths(ctx context.Context) ([]string, error)

	// SaveLocksBatch saves multiple locks efficiently.
	SaveLocksBatch(ctx context.Context, locks []*entities.DetectionLock) error

	// === Analytics (model-aware) ===

	// GetSpeciesSummary returns summary statistics for all species.
	// modelID is optional; pass nil to include all models.
	GetSpeciesSummary(ctx context.Context, start, end int64, modelID *uint) ([]SpeciesSummaryData, error)

	// GetHourlyDistribution returns detection counts by hour.
	// labelID and modelID are optional filters.
	GetHourlyDistribution(ctx context.Context, start, end int64, labelID, modelID *uint) ([]HourlyDistributionData, error)

	// GetDailyAnalytics returns daily statistics.
	// labelID and modelID are optional filters.
	GetDailyAnalytics(ctx context.Context, start, end int64, labelID, modelID *uint) ([]DailyAnalyticsData, error)

	// GetDetectionTrends returns detection trends over time.
	// period is "day", "week", or "month".
	// modelID is optional.
	GetDetectionTrends(ctx context.Context, period string, limit int, modelID *uint) ([]DailyAnalyticsData, error)

	// GetNewSpecies returns species detected for the first time ever within the range.
	GetNewSpecies(ctx context.Context, start, end int64, limit, offset int) ([]NewSpeciesData, error)

	// GetSpeciesFirstDetectionInPeriod returns the first detection of each species
	// within a specific date range (e.g., "First Robin of Spring 2024").
	// This is distinct from GetNewSpecies (lifetime firsts) and
	// GetSpeciesFirstDetection (per-species first ever).
	GetSpeciesFirstDetectionInPeriod(ctx context.Context, start, end int64, limit, offset int) ([]SpeciesFirstSeen, error)

	// === Utilities ===

	// GetClipPath returns the clip path for a detection.
	// Returns ErrDetectionNotFound if not found.
	GetClipPath(ctx context.Context, id uint) (string, error)

	// Exists checks if a detection with the given ID exists.
	Exists(ctx context.Context, id uint) (bool, error)

	// GetLastMigratedID returns the highest legacy_id that has been migrated.
	// Used by the migration worker to track progress.
	GetLastMigratedID(ctx context.Context) (uint, error)
}
