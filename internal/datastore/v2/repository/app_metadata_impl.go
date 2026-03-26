package repository

import (
	"context"
	"fmt"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// appMetadataRepository implements AppMetadataRepository.
type appMetadataRepository struct {
	db          *gorm.DB
	metrics     *datastore.Metrics
	useV2Prefix bool
	isMySQL     bool
}

// NewAppMetadataRepository creates a new AppMetadataRepository.
// Parameters:
//   - db: GORM database connection
//   - metrics: optional DatastoreMetrics for retry observability (nil-safe)
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewAppMetadataRepository(db *gorm.DB, metrics *datastore.Metrics, useV2Prefix, isMySQL bool) AppMetadataRepository {
	return &appMetadataRepository{
		db:          db,
		metrics:     metrics,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

// tableName returns the appropriate table name.
func (r *appMetadataRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2AppMetadata
	}
	return tableAppMetadata
}

// Get retrieves the value for the given key.
// Returns an empty string and nil error if the key does not exist.
func (r *appMetadataRepository) Get(ctx context.Context, key string) (string, error) {
	var meta entities.AppMetadata
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("`key` = ?", key).
		First(&meta).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get app metadata key %q: %w", key, err)
	}
	return meta.Value, nil
}

// Set creates or updates the value for the given key (upsert).
func (r *appMetadataRepository) Set(ctx context.Context, key, value string) error {
	meta := entities.AppMetadata{
		Key:   key,
		Value: value,
	}
	return datastore.RetryOnLock("v2_set_app_metadata", func() error {
		if err := r.db.WithContext(ctx).Table(r.tableName()).
			Clauses(clause.OnConflict{
				Columns:   []clause.Column{{Name: "key"}},
				DoUpdates: clause.AssignmentColumns([]string{"value"}),
			}).
			Create(&meta).Error; err != nil {
			return fmt.Errorf("set app metadata key %q: %w", key, err)
		}
		return nil
	}, r.metrics)
}
