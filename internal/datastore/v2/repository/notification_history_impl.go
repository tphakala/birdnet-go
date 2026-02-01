package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// notificationHistoryRepository implements NotificationHistoryRepository.
type notificationHistoryRepository struct {
	db          *gorm.DB
	labelRepo   LabelRepository
	useV2Prefix bool
	isMySQL     bool // For API consistency; currently unused here (used by detection_impl.go for dialect-specific SQL)
}

// NewNotificationHistoryRepository creates a new NotificationHistoryRepository.
// Parameters:
//   - db: GORM database connection
//   - labelRepo: LabelRepository for resolving scientific names to label IDs
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewNotificationHistoryRepository(db *gorm.DB, labelRepo LabelRepository, useV2Prefix, isMySQL bool) NotificationHistoryRepository {
	return &notificationHistoryRepository{
		db:          db,
		labelRepo:   labelRepo,
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

// ensureLabelRepo returns an error if labelRepo is nil.
// This guards against misconfiguration that would cause nil pointer panics.
func (r *notificationHistoryRepository) ensureLabelRepo() error {
	if r.labelRepo == nil {
		return errors.NewStd("label repository not configured for notification history repository")
	}
	return nil
}

// SaveNotificationHistory saves or updates a notification history entry (upsert).
// The history.LabelID must be set by the caller.
func (r *notificationHistoryRepository) SaveNotificationHistory(ctx context.Context, history *entities.NotificationHistory) error {
	if history.LabelID == 0 {
		return errors.NewStd("notification history LabelID must be set before saving")
	}
	return r.db.WithContext(ctx).Table(r.tableName()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "label_id"}, {Name: "notification_type"}},
			UpdateAll: true,
		}).
		Create(history).Error
}

// GetNotificationHistory retrieves a notification history entry by scientific name.
// Internally resolves the scientific name to label IDs (cross-model) for the lookup.
// Returns the first matching entry if multiple models have history for this species.
func (r *notificationHistoryRepository) GetNotificationHistory(ctx context.Context, scientificName, notificationType string) (*entities.NotificationHistory, error) {
	if err := r.ensureLabelRepo(); err != nil {
		return nil, err
	}
	// Resolve scientific name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, scientificName)
	if err != nil {
		return nil, err
	}
	if len(labelIDs) == 0 {
		return nil, ErrNotificationHistoryNotFound
	}

	var history entities.NotificationHistory
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Preload("Label").
		Where("label_id IN ? AND notification_type = ?", labelIDs, notificationType).
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
		Preload("Label").
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
