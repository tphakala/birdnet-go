package repository

import (
	"context"
	"fmt"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
)

// appEventRepository implements AppEventRepository.
type appEventRepository struct {
	db          *gorm.DB
	metrics     *datastore.Metrics
	useV2Prefix bool
	isMySQL     bool
}

// NewAppEventRepository creates a new AppEventRepository.
func NewAppEventRepository(db *gorm.DB, metrics *datastore.Metrics, useV2Prefix, isMySQL bool) AppEventRepository {
	return &appEventRepository{
		db:          db,
		metrics:     metrics,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

func (r *appEventRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2AppEvents
	}
	return tableAppEvents
}

// Save persists an application event.
func (r *appEventRepository) Save(ctx context.Context, event *entities.AppEvent) error {
	if event == nil {
		return fmt.Errorf("app event cannot be nil")
	}
	return datastore.RetryOnLock(ctx, "v2_save_app_event", func() error {
		event.ID = 0
		if err := r.db.WithContext(ctx).Table(r.tableName()).Create(event).Error; err != nil {
			return fmt.Errorf("save app event: %w", err)
		}
		return nil
	}, r.metrics)
}

// GetRecent returns the most recent events, ordered newest first.
func (r *appEventRepository) GetRecent(ctx context.Context, limit int) ([]entities.AppEvent, error) {
	var events []entities.AppEvent
	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Order("timestamp DESC, id DESC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, fmt.Errorf("get recent app events: %w", err)
	}
	return events, nil
}

// GetByCategory returns events matching the given category, ordered newest first.
func (r *appEventRepository) GetByCategory(ctx context.Context, category string, limit int) ([]entities.AppEvent, error) {
	var events []entities.AppEvent
	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("category = ?", category).
		Order("timestamp DESC, id DESC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, fmt.Errorf("get app events by category %q: %w", category, err)
	}
	return events, nil
}

// GetSince returns events since the given timestamp (inclusive), ordered newest first.
func (r *appEventRepository) GetSince(ctx context.Context, since time.Time, limit int) ([]entities.AppEvent, error) {
	var events []entities.AppEvent
	if err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("timestamp >= ?", since).
		Order("timestamp DESC, id DESC").
		Limit(limit).
		Find(&events).Error; err != nil {
		return nil, fmt.Errorf("get app events since %s: %w", since.Format(time.RFC3339), err)
	}
	return events, nil
}

// DeleteBefore removes events older than the given timestamp.
func (r *appEventRepository) DeleteBefore(ctx context.Context, before time.Time) (int64, error) {
	var deleted int64
	err := datastore.RetryOnLock(ctx, "v2_delete_app_events", func() error {
		result := r.db.WithContext(ctx).Table(r.tableName()).
			Where("timestamp < ?", before).
			Delete(&entities.AppEvent{})
		if result.Error != nil {
			return fmt.Errorf("delete app events before %s: %w", before.Format(time.RFC3339), result.Error)
		}
		deleted = result.RowsAffected
		return nil
	}, r.metrics)
	return deleted, err
}

// Count returns the total number of stored events.
func (r *appEventRepository) Count(ctx context.Context) (int64, error) {
	var count int64
	if err := r.db.WithContext(ctx).Table(r.tableName()).Count(&count).Error; err != nil {
		return 0, fmt.Errorf("count app events: %w", err)
	}
	return count, nil
}
