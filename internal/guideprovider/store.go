package guideprovider

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
	gormlogger "gorm.io/gorm/logger"
)

// GORMGuideStore implements GuideStore using a GORM database connection.
type GORMGuideStore struct {
	db *gorm.DB
}

// NewGORMGuideStore creates a new GORMGuideStore and runs auto-migration.
func NewGORMGuideStore(db *gorm.DB) (*GORMGuideStore, error) {
	if err := db.AutoMigrate(&GuideCacheEntry{}); err != nil {
		return nil, errors.Newf("failed to migrate guide_caches table: %w", err).
			Component("guideprovider").
			Category(errors.CategoryDatabase).
			Build()
	}
	return &GORMGuideStore{db: db}, nil
}

// GetGuideCache retrieves a guide cache entry by scientific name and provider.
func (s *GORMGuideStore) GetGuideCache(ctx context.Context, scientificName, providerName string) (*GuideCacheEntry, error) {
	var entry GuideCacheEntry
	err := s.db.WithContext(ctx).
		Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).
		Where("scientific_name = ? AND provider_name = ?", scientificName, providerName).
		First(&entry).Error

	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &entry, nil
}

// SaveGuideCache saves or updates a guide cache entry (upsert).
func (s *GORMGuideStore) SaveGuideCache(ctx context.Context, entry *GuideCacheEntry) error {
	return s.db.WithContext(ctx).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_name"}, {Name: "scientific_name"}},
			DoUpdates: clause.AssignmentColumns([]string{
				"source_provider", "common_name", "description",
				"conservation_status", "source_url", "license_name",
				"license_url", "cached_at",
			}),
		}).
		Create(entry).Error
}

// GetAllGuideCaches retrieves all guide cache entries for a specific provider.
func (s *GORMGuideStore) GetAllGuideCaches(ctx context.Context, providerName string) ([]GuideCacheEntry, error) {
	var entries []GuideCacheEntry
	err := s.db.WithContext(ctx).
		Session(&gorm.Session{Logger: gormlogger.Default.LogMode(gormlogger.Silent)}).
		Where("provider_name = ?", providerName).
		Find(&entries).Error
	if err != nil {
		GetLogger().Warn("Failed to query guide caches",
			logger.String("provider", providerName),
			logger.Any("error", err))
		return nil, err
	}
	return entries, nil
}
