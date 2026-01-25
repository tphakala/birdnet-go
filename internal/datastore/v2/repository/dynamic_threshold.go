package repository

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// DynamicThresholdRepository handles threshold operations.
type DynamicThresholdRepository interface {
	// DynamicThreshold CRUD
	SaveDynamicThreshold(ctx context.Context, threshold *entities.DynamicThreshold) error
	GetDynamicThreshold(ctx context.Context, speciesName string) (*entities.DynamicThreshold, error)
	GetAllDynamicThresholds(ctx context.Context, limit ...int) ([]entities.DynamicThreshold, error)
	DeleteDynamicThreshold(ctx context.Context, speciesName string) error
	DeleteExpiredDynamicThresholds(ctx context.Context, before time.Time) (int64, error)
	UpdateDynamicThresholdExpiry(ctx context.Context, speciesName string, expiresAt time.Time) error
	BatchSaveDynamicThresholds(ctx context.Context, thresholds []entities.DynamicThreshold) error
	DeleteAllDynamicThresholds(ctx context.Context) (int64, error)
	GetDynamicThresholdStats(ctx context.Context) (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error)

	// ThresholdEvents
	SaveThresholdEvent(ctx context.Context, event *entities.ThresholdEvent) error
	GetThresholdEvents(ctx context.Context, speciesName string, limit int) ([]entities.ThresholdEvent, error)
	GetRecentThresholdEvents(ctx context.Context, limit int) ([]entities.ThresholdEvent, error)
	DeleteThresholdEvents(ctx context.Context, speciesName string) error
	DeleteAllThresholdEvents(ctx context.Context) (int64, error)
}
