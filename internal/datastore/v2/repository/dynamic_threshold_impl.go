package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// dynamicThresholdRepository implements DynamicThresholdRepository.
type dynamicThresholdRepository struct {
	db          *gorm.DB
	metrics     *datastore.Metrics
	labelRepo   LabelRepository
	useV2Prefix bool
	isMySQL     bool // For API consistency; currently unused here (used by detection_impl.go for dialect-specific SQL)
}

// NewDynamicThresholdRepository creates a new DynamicThresholdRepository.
// Parameters:
//   - db: GORM database connection
//   - metrics: optional DatastoreMetrics for retry observability (nil-safe)
//   - labelRepo: LabelRepository for resolving species names to label IDs
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewDynamicThresholdRepository(db *gorm.DB, metrics *datastore.Metrics, labelRepo LabelRepository, useV2Prefix, isMySQL bool) DynamicThresholdRepository {
	return &dynamicThresholdRepository{
		db:          db,
		metrics:     metrics,
		labelRepo:   labelRepo,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

func (r *dynamicThresholdRepository) thresholdTable() string {
	if r.useV2Prefix {
		return tableV2DynamicThresholds
	}
	return tableDynamicThresholds
}

func (r *dynamicThresholdRepository) eventTable() string {
	if r.useV2Prefix {
		return tableV2ThresholdEvents
	}
	return tableThresholdEvents
}

// ensureLabelRepo returns an error if labelRepo is nil.
// This guards against misconfiguration that would cause nil pointer panics.
func (r *dynamicThresholdRepository) ensureLabelRepo() error {
	if r.labelRepo == nil {
		return errors.NewStd("label repository not configured for threshold repository")
	}
	return nil
}

// SaveDynamicThreshold saves or updates a dynamic threshold (upsert).
// The threshold.LabelID must be set by the caller.
func (r *dynamicThresholdRepository) SaveDynamicThreshold(ctx context.Context, threshold *entities.DynamicThreshold) error {
	if threshold.LabelID == 0 {
		return errors.NewStd("dynamic threshold LabelID must be set before saving")
	}
	return datastore.RetryOnLock(ctx, "v2_save_dynamic_threshold", func() error {
		return r.db.WithContext(ctx).Table(r.thresholdTable()).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "label_id"}},
				UpdateAll: true,
			}).
			Create(threshold).Error
	}, r.metrics)
}

// GetDynamicThreshold retrieves a threshold by species name (scientific name).
// Internally resolves the species name to label IDs (cross-model) for the lookup.
// Returns the first matching threshold if multiple models have thresholds for this species.
func (r *dynamicThresholdRepository) GetDynamicThreshold(ctx context.Context, speciesName string) (*entities.DynamicThreshold, error) {
	if err := r.ensureLabelRepo(); err != nil {
		return nil, err
	}
	// Resolve species name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, speciesName)
	if err != nil {
		return nil, err
	}
	if len(labelIDs) == 0 {
		return nil, ErrDynamicThresholdNotFound
	}

	var threshold entities.DynamicThreshold
	err = r.db.WithContext(ctx).Table(r.thresholdTable()).
		Preload("Label").
		Where("label_id IN ?", labelIDs).
		First(&threshold).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrDynamicThresholdNotFound
	}
	if err != nil {
		return nil, err
	}

	return &threshold, nil
}

// GetAllDynamicThresholds retrieves all thresholds with optional limit.
func (r *dynamicThresholdRepository) GetAllDynamicThresholds(ctx context.Context, limit ...int) ([]entities.DynamicThreshold, error) {
	var thresholds []entities.DynamicThreshold
	query := r.db.WithContext(ctx).Table(r.thresholdTable()).
		Preload("Label").
		Order("label_id ASC")
	if len(limit) > 0 && limit[0] > 0 {
		query = query.Limit(limit[0])
	}
	err := query.Find(&thresholds).Error
	return thresholds, err
}

// DeleteDynamicThreshold deletes thresholds by species name (scientific name).
// Deletes across all models that have thresholds for this species.
func (r *dynamicThresholdRepository) DeleteDynamicThreshold(ctx context.Context, speciesName string) error {
	if err := r.ensureLabelRepo(); err != nil {
		return err
	}
	// Resolve species name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, speciesName)
	if err != nil {
		return err
	}
	if len(labelIDs) == 0 {
		return ErrDynamicThresholdNotFound
	}

	var rowsAffected int64
	err = datastore.RetryOnLock(ctx, "v2_delete_dynamic_threshold", func() error {
		result := r.db.WithContext(ctx).Table(r.thresholdTable()).
			Where("label_id IN ?", labelIDs).
			Delete(&entities.DynamicThreshold{})
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	}, r.metrics)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrDynamicThresholdNotFound
	}
	return nil
}

// DeleteExpiredDynamicThresholds deletes thresholds that have expired.
func (r *dynamicThresholdRepository) DeleteExpiredDynamicThresholds(ctx context.Context, before time.Time) (int64, error) {
	var rowsAffected int64
	err := datastore.RetryOnLock(ctx, "v2_delete_expired_dynamic_thresholds", func() error {
		result := r.db.WithContext(ctx).Table(r.thresholdTable()).
			Where("expires_at < ?", before).
			Delete(&entities.DynamicThreshold{})
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	}, r.metrics)
	return rowsAffected, err
}

// UpdateDynamicThresholdExpiry updates the expiry time for thresholds.
// Updates across all models that have thresholds for this species.
func (r *dynamicThresholdRepository) UpdateDynamicThresholdExpiry(ctx context.Context, speciesName string, expiresAt time.Time) error {
	if err := r.ensureLabelRepo(); err != nil {
		return err
	}
	// Resolve species name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, speciesName)
	if err != nil {
		return err
	}
	if len(labelIDs) == 0 {
		return ErrDynamicThresholdNotFound
	}

	var rowsAffected int64
	err = datastore.RetryOnLock(ctx, "v2_update_dynamic_threshold_expiry", func() error {
		result := r.db.WithContext(ctx).Table(r.thresholdTable()).
			Where("label_id IN ?", labelIDs).
			Update("expires_at", expiresAt)
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	}, r.metrics)
	if err != nil {
		return err
	}
	if rowsAffected == 0 {
		return ErrDynamicThresholdNotFound
	}
	return nil
}

// BatchSaveDynamicThresholds saves multiple thresholds in a batch (upsert).
// All thresholds must have LabelID set.
func (r *dynamicThresholdRepository) BatchSaveDynamicThresholds(ctx context.Context, thresholds []entities.DynamicThreshold) error {
	if len(thresholds) == 0 {
		return nil
	}
	// Validate all have LabelID set
	for i := range thresholds {
		if thresholds[i].LabelID == 0 {
			return errors.NewStd("all thresholds must have LabelID set before batch save")
		}
	}
	return datastore.RetryOnLock(ctx, "v2_batch_save_dynamic_thresholds", func() error {
		return r.db.WithContext(ctx).Table(r.thresholdTable()).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "label_id"}},
				UpdateAll: true,
			}).
			CreateInBatches(thresholds, 100).Error
	}, r.metrics)
}

// DeleteAllDynamicThresholds deletes all thresholds.
func (r *dynamicThresholdRepository) DeleteAllDynamicThresholds(ctx context.Context) (int64, error) {
	var rowsAffected int64
	err := datastore.RetryOnLock(ctx, "v2_delete_all_dynamic_thresholds", func() error {
		result := r.db.WithContext(ctx).Table(r.thresholdTable()).
			Where("1 = 1").
			Delete(&entities.DynamicThreshold{})
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	}, r.metrics)
	return rowsAffected, err
}

// GetDynamicThresholdStats returns statistics about dynamic thresholds.
func (r *dynamicThresholdRepository) GetDynamicThresholdStats(ctx context.Context) (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	// Total count
	if err = r.db.WithContext(ctx).Table(r.thresholdTable()).Count(&totalCount).Error; err != nil {
		return
	}

	// Active count (not expired)
	now := time.Now()
	if err = r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("expires_at > ?", now).
		Count(&activeCount).Error; err != nil {
		return
	}

	// At minimum count (level = 3, which is the minimum threshold)
	if err = r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("level = 3").
		Count(&atMinimumCount).Error; err != nil {
		return
	}

	// Level distribution
	levelDistribution = make(map[int]int64)
	type levelCount struct {
		Level int
		Count int64
	}
	var levels []levelCount
	if err = r.db.WithContext(ctx).Table(r.thresholdTable()).
		Select("level, COUNT(*) as count").
		Group("level").
		Scan(&levels).Error; err != nil {
		return
	}
	for _, lc := range levels {
		levelDistribution[lc.Level] = lc.Count
	}

	return
}

// SaveThresholdEvent saves a threshold event.
// The event.LabelID must be set by the caller.
func (r *dynamicThresholdRepository) SaveThresholdEvent(ctx context.Context, event *entities.ThresholdEvent) error {
	if event.LabelID == 0 {
		return errors.NewStd("threshold event LabelID must be set before saving")
	}
	return datastore.RetryOnLock(ctx, "v2_save_threshold_event", func() error {
		return r.db.WithContext(ctx).Table(r.eventTable()).Create(event).Error
	}, r.metrics)
}

// GetThresholdEvents retrieves events for a species (by scientific name).
// Retrieves events across all models that have events for this species.
func (r *dynamicThresholdRepository) GetThresholdEvents(ctx context.Context, speciesName string, limit int) ([]entities.ThresholdEvent, error) {
	if err := r.ensureLabelRepo(); err != nil {
		return nil, err
	}
	// Resolve species name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, speciesName)
	if err != nil {
		return nil, err
	}
	if len(labelIDs) == 0 {
		return []entities.ThresholdEvent{}, nil
	}

	var events []entities.ThresholdEvent
	query := r.db.WithContext(ctx).Table(r.eventTable()).
		Preload("Label").
		Where("label_id IN ?", labelIDs).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err = query.Find(&events).Error
	return events, err
}

// GetRecentThresholdEvents retrieves the most recent events across all species.
func (r *dynamicThresholdRepository) GetRecentThresholdEvents(ctx context.Context, limit int) ([]entities.ThresholdEvent, error) {
	var events []entities.ThresholdEvent
	query := r.db.WithContext(ctx).Table(r.eventTable()).
		Preload("Label").
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&events).Error
	return events, err
}

// DeleteThresholdEvents deletes all events for a species (by scientific name).
// Deletes events across all models that have events for this species.
func (r *dynamicThresholdRepository) DeleteThresholdEvents(ctx context.Context, speciesName string) error {
	if err := r.ensureLabelRepo(); err != nil {
		return err
	}
	// Resolve species name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, speciesName)
	if err != nil {
		return err
	}
	if len(labelIDs) == 0 {
		return nil // Nothing to delete
	}

	return datastore.RetryOnLock(ctx, "v2_delete_threshold_events", func() error {
		return r.db.WithContext(ctx).Table(r.eventTable()).
			Where("label_id IN ?", labelIDs).
			Delete(&entities.ThresholdEvent{}).Error
	}, r.metrics)
}

// DeleteAllThresholdEvents deletes all threshold events.
func (r *dynamicThresholdRepository) DeleteAllThresholdEvents(ctx context.Context) (int64, error) {
	var rowsAffected int64
	err := datastore.RetryOnLock(ctx, "v2_delete_all_threshold_events", func() error {
		result := r.db.WithContext(ctx).Table(r.eventTable()).
			Where("1 = 1").
			Delete(&entities.ThresholdEvent{})
		if result.Error != nil {
			return result.Error
		}
		rowsAffected = result.RowsAffected
		return nil
	}, r.metrics)
	return rowsAffected, err
}
