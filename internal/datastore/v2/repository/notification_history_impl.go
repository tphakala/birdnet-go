package repository

import (
	"context"
	"errors"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// notificationHistoryRepository implements NotificationHistoryRepository.
type notificationHistoryRepository struct {
	db          *gorm.DB
	useV2Prefix bool
	isMySQL     bool
}

// NewNotificationHistoryRepository creates a new NotificationHistoryRepository.
// Parameters:
//   - db: GORM database connection
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewNotificationHistoryRepository(db *gorm.DB, useV2Prefix, isMySQL bool) NotificationHistoryRepository {
	return &notificationHistoryRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

func (r *notificationHistoryRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2NotificationHistory
	}
	return tableNotificationHistory
}

// SaveNotificationHistory saves or updates a notification history entry (upsert).
func (r *notificationHistoryRepository) SaveNotificationHistory(ctx context.Context, history *entities.NotificationHistory) error {
	return r.db.WithContext(ctx).Table(r.tableName()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "scientific_name"}, {Name: "notification_type"}},
			UpdateAll: true,
		}).
		Create(history).Error
}

// GetNotificationHistory retrieves a notification history entry.
func (r *notificationHistoryRepository) GetNotificationHistory(ctx context.Context, scientificName, notificationType string) (*entities.NotificationHistory, error) {
	var history entities.NotificationHistory
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("scientific_name = ? AND notification_type = ?", scientificName, notificationType).
		First(&history).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNotificationHistoryNotFound
	}
	if err != nil {
		return nil, err
	}
	return &history, nil
}

// GetActiveNotificationHistory retrieves all non-expired notification history entries.
func (r *notificationHistoryRepository) GetActiveNotificationHistory(ctx context.Context, after time.Time) ([]entities.NotificationHistory, error) {
	var histories []entities.NotificationHistory
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("expires_at > ?", after).
		Find(&histories).Error
	return histories, err
}

// DeleteExpiredNotificationHistory deletes expired entries.
func (r *notificationHistoryRepository) DeleteExpiredNotificationHistory(ctx context.Context, before time.Time) (int64, error) {
	result := r.db.WithContext(ctx).Table(r.tableName()).
		Where("expires_at < ?", before).
		Delete(&entities.NotificationHistory{})
	return result.RowsAffected, result.Error
}
