package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// AppEventRepository provides access to the app_events table.
type AppEventRepository interface {
	// Save persists an application event.
	Save(ctx context.Context, event *entities.AppEvent) error

	// GetRecent returns the most recent events, ordered newest first.
	GetRecent(ctx context.Context, limit int) ([]entities.AppEvent, error)

	// GetByCategory returns events matching the given category, ordered newest first.
	GetByCategory(ctx context.Context, category string, limit int) ([]entities.AppEvent, error)

	// GetSince returns events after the given timestamp, ordered newest first.
	GetSince(ctx context.Context, since time.Time, limit int) ([]entities.AppEvent, error)

	// DeleteBefore removes events older than the given timestamp.
	// Returns the number of rows deleted.
	DeleteBefore(ctx context.Context, before time.Time) (int64, error)

	// Count returns the total number of stored events.
	Count(ctx context.Context) (int64, error)
}
