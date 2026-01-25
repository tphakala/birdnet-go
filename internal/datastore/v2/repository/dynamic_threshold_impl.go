package repository

import (
	"context"
	"errors"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// dynamicThresholdRepository implements DynamicThresholdRepository.
type dynamicThresholdRepository struct {
	db          *gorm.DB
	useV2Prefix bool
}

// NewDynamicThresholdRepository creates a new DynamicThresholdRepository.
func NewDynamicThresholdRepository(db *gorm.DB, useV2Prefix bool) DynamicThresholdRepository {
	return &dynamicThresholdRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
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

// SaveDynamicThreshold saves or updates a dynamic threshold (upsert).
func (r *dynamicThresholdRepository) SaveDynamicThreshold(ctx context.Context, threshold *entities.DynamicThreshold) error {
	return r.db.WithContext(ctx).Table(r.thresholdTable()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "species_name"}},
			UpdateAll: true,
		}).
		Create(threshold).Error
}

// GetDynamicThreshold retrieves a threshold by species name.
func (r *dynamicThresholdRepository) GetDynamicThreshold(ctx context.Context, speciesName string) (*entities.DynamicThreshold, error) {
	var threshold entities.DynamicThreshold
	err := r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("species_name = ?", speciesName).
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
	query := r.db.WithContext(ctx).Table(r.thresholdTable()).Order("species_name ASC")
	if len(limit) > 0 && limit[0] > 0 {
		query = query.Limit(limit[0])
	}
	err := query.Find(&thresholds).Error
	return thresholds, err
}

// DeleteDynamicThreshold deletes a threshold by species name.
func (r *dynamicThresholdRepository) DeleteDynamicThreshold(ctx context.Context, speciesName string) error {
	result := r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("species_name = ?", speciesName).
		Delete(&entities.DynamicThreshold{})
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDynamicThresholdNotFound
	}
	return nil
}

// DeleteExpiredDynamicThresholds deletes thresholds that have expired.
func (r *dynamicThresholdRepository) DeleteExpiredDynamicThresholds(ctx context.Context, before time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("expires_at < ?", before).
		Delete(&entities.DynamicThreshold{})
	return result.RowsAffected, result.Error
}

// UpdateDynamicThresholdExpiry updates the expiry time for a threshold.
func (r *dynamicThresholdRepository) UpdateDynamicThresholdExpiry(ctx context.Context, speciesName string, expiresAt time.Time) error {
	result := r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("species_name = ?", speciesName).
		Update("expires_at", expiresAt)
	if result.Error != nil {
		return result.Error
	}
	if result.RowsAffected == 0 {
		return ErrDynamicThresholdNotFound
	}
	return nil
}

// BatchSaveDynamicThresholds saves multiple thresholds in a batch (upsert).
func (r *dynamicThresholdRepository) BatchSaveDynamicThresholds(ctx context.Context, thresholds []entities.DynamicThreshold) error {
	if len(thresholds) == 0 {
		return nil
	}
	return r.db.WithContext(ctx).Table(r.thresholdTable()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "species_name"}},
			UpdateAll: true,
		}).
		CreateInBatches(thresholds, 100).Error
}

// DeleteAllDynamicThresholds deletes all thresholds.
func (r *dynamicThresholdRepository) DeleteAllDynamicThresholds(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Table(r.thresholdTable()).
		Where("1 = 1").
		Delete(&entities.DynamicThreshold{})
	return result.RowsAffected, result.Error
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
func (r *dynamicThresholdRepository) SaveThresholdEvent(ctx context.Context, event *entities.ThresholdEvent) error {
	return r.db.WithContext(ctx).Table(r.eventTable()).Create(event).Error
}

// GetThresholdEvents retrieves events for a species.
func (r *dynamicThresholdRepository) GetThresholdEvents(ctx context.Context, speciesName string, limit int) ([]entities.ThresholdEvent, error) {
	var events []entities.ThresholdEvent
	query := r.db.WithContext(ctx).Table(r.eventTable()).
		Where("species_name = ?", speciesName).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&events).Error
	return events, err
}

// GetRecentThresholdEvents retrieves the most recent events across all species.
func (r *dynamicThresholdRepository) GetRecentThresholdEvents(ctx context.Context, limit int) ([]entities.ThresholdEvent, error) {
	var events []entities.ThresholdEvent
	query := r.db.WithContext(ctx).Table(r.eventTable()).
		Order("created_at DESC")
	if limit > 0 {
		query = query.Limit(limit)
	}
	err := query.Find(&events).Error
	return events, err
}

// DeleteThresholdEvents deletes all events for a species.
func (r *dynamicThresholdRepository) DeleteThresholdEvents(ctx context.Context, speciesName string) error {
	return r.db.WithContext(ctx).Table(r.eventTable()).
		Where("species_name = ?", speciesName).
		Delete(&entities.ThresholdEvent{}).Error
}

// DeleteAllThresholdEvents deletes all threshold events.
func (r *dynamicThresholdRepository) DeleteAllThresholdEvents(ctx context.Context) (int64, error) {
	result := r.db.WithContext(ctx).Table(r.eventTable()).
		Where("1 = 1").
		Delete(&entities.ThresholdEvent{})
	return result.RowsAffected, result.Error
}
