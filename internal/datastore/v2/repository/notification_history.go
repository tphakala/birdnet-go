package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// NotificationHistoryRepository handles notification history operations.
type NotificationHistoryRepository interface {
	SaveNotificationHistory(ctx context.Context, history *entities.NotificationHistory) error
	GetNotificationHistory(ctx context.Context, scientificName, notificationType string) (*entities.NotificationHistory, error)
	GetActiveNotificationHistory(ctx context.Context, after time.Time) ([]entities.NotificationHistory, error)
	DeleteExpiredNotificationHistory(ctx context.Context, before time.Time) (int64, error)
}
