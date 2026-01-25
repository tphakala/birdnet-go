package repository

import (
	"context"
	"errors"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// imageCacheRepository implements ImageCacheRepository.
type imageCacheRepository struct {
	db          *gorm.DB
	useV2Prefix bool
}

// NewImageCacheRepository creates a new ImageCacheRepository.
func NewImageCacheRepository(db *gorm.DB, useV2Prefix bool) ImageCacheRepository {
	return &imageCacheRepository{
		db:          db,
		useV2Prefix: useV2Prefix,
	}
}

func (r *imageCacheRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2ImageCaches
	}
	return tableImageCaches
}

// GetImageCache retrieves an image cache entry by provider and scientific name.
func (r *imageCacheRepository) GetImageCache(ctx context.Context, providerName, scientificName string) (*entities.ImageCache, error) {
	var cache entities.ImageCache
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("provider_name = ? AND scientific_name = ?", providerName, scientificName).
		First(&cache).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrImageCacheNotFound
	}
	if err != nil {
		return nil, err
	}
	return &cache, nil
}

// SaveImageCache saves or updates an image cache entry (upsert).
func (r *imageCacheRepository) SaveImageCache(ctx context.Context, cache *entities.ImageCache) error {
	return r.db.WithContext(ctx).Table(r.tableName()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_name"}, {Name: "scientific_name"}},
			UpdateAll: true,
		}).
		Create(cache).Error
}

// GetAllImageCaches retrieves all image cache entries for a provider.
func (r *imageCacheRepository) GetAllImageCaches(ctx context.Context, providerName string) ([]entities.ImageCache, error) {
	var caches []entities.ImageCache
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("provider_name = ?", providerName).
		Find(&caches).Error
	return caches, err
}

// GetImageCacheBatch retrieves multiple image cache entries by scientific names.
func (r *imageCacheRepository) GetImageCacheBatch(ctx context.Context, providerName string, scientificNames []string) (map[string]*entities.ImageCache, error) {
	if len(scientificNames) == 0 {
		return make(map[string]*entities.ImageCache), nil
	}

	var caches []entities.ImageCache
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Where("provider_name = ? AND scientific_name IN ?", providerName, scientificNames).
		Find(&caches).Error
	if err != nil {
		return nil, err
	}

	result := make(map[string]*entities.ImageCache, len(caches))
	for i := range caches {
		result[caches[i].ScientificName] = &caches[i]
	}
	return result, nil
}
