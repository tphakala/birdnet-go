// notification_history.go: Database operations for persisting notification history
// This prevents duplicate "new species" notifications after application restarts (BG-17)
//
// TODO(BG-17): Add comprehensive test coverage in notification_history_test.go:
//   - TestSaveNotificationHistory: Verify upsert behavior
//   - TestGetActiveNotificationHistory: Verify bulk loading with time filtering
//   - TestDeleteExpiredNotificationHistory: Verify cleanup logic
//   - TestNotificationHistoryCompositeUniqueness: Verify constraint behavior
//   - TestNotificationHistoryRestartCycle: Integration test for save → restart → load
package datastore

import (
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// SaveNotificationHistory saves or updates a notification history record in the database
// This uses an upsert operation to either create a new record or update an existing one
// The combination of (ScientificName, NotificationType) is unique
func (ds *DataStore) SaveNotificationHistory(history *NotificationHistory) error {
	if history == nil {
		return validationError("notification history cannot be nil", "history", nil)
	}
	if history.ScientificName == "" {
		return validationError("scientific name cannot be empty", "scientific_name", "")
	}
	if history.NotificationType == "" {
		history.NotificationType = "new_species" // Default value
	}

	// Timestamps
	now := time.Now()
	history.UpdatedAt = now

	// Upsert: Use GORM's OnConflict clause for efficient upsert
	// This handles the composite unique index on (scientific_name, notification_type)
	result := ds.DB.Clauses(clause.OnConflict{
		Columns: []clause.Column{
			{Name: "scientific_name"},
			{Name: "notification_type"},
		},
		DoUpdates: clause.AssignmentColumns([]string{
			"last_sent",
			"expires_at",
			"updated_at",
		}),
	}).Create(history)

	if result.Error != nil {
		return dbError(result.Error, "save_notification_history", errors.PriorityMedium,
			"species", history.ScientificName,
			"notification_type", history.NotificationType,
			"table", "notification_histories",
			"action", "persist_notification_suppression")
	}

	return nil
}

// GetNotificationHistory retrieves a notification history record for a specific species and type
func (ds *DataStore) GetNotificationHistory(scientificName, notificationType string) (*NotificationHistory, error) {
	if scientificName == "" {
		return nil, validationError("scientific name cannot be empty", "scientific_name", "")
	}
	if notificationType == "" {
		notificationType = "new_species" // Default value
	}

	var history NotificationHistory
	err := ds.DB.Where("scientific_name = ? AND notification_type = ?", scientificName, notificationType).
		First(&history).Error

	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrNotificationHistoryNotFound
		}
		return nil, dbError(err, "get_notification_history", errors.PriorityMedium,
			"species", scientificName,
			"notification_type", notificationType,
			"action", "retrieve_notification_suppression")
	}

	return &history, nil
}

// GetActiveNotificationHistory retrieves all notification history records that were sent after the specified time
// This is used during initialization to load recent notification history into memory
// Typical usage: Load notifications from past 2x suppression window (14 days)
func (ds *DataStore) GetActiveNotificationHistory(after time.Time) ([]NotificationHistory, error) {
	var histories []NotificationHistory

	err := ds.DB.Where("last_sent >= ?", after).
		Order("last_sent DESC").
		Find(&histories).Error

	if err != nil {
		return nil, dbError(err, "get_active_notification_history", errors.PriorityMedium,
			"after", after.Format(time.RFC3339),
			"table", "notification_histories",
			"action", "restore_notification_suppression")
	}

	return histories, nil
}

// DeleteExpiredNotificationHistory removes all notification history records that have expired
// Returns the count of deleted records
// This is typically called periodically by a cleanup job
func (ds *DataStore) DeleteExpiredNotificationHistory(before time.Time) (int64, error) {
	result := ds.DB.Where("expires_at < ?", before).Delete(&NotificationHistory{})
	if result.Error != nil {
		return 0, dbError(result.Error, "delete_expired_notification_history", errors.PriorityLow,
			"before", before.Format(time.RFC3339),
			"action", "cleanup_expired_notifications")
	}

	if result.RowsAffected > 0 {
		getLogger().Info("Cleaned up expired notification history",
			"count", result.RowsAffected,
			"before", before.Format(time.RFC3339))
	}

	return result.RowsAffected, nil
}
