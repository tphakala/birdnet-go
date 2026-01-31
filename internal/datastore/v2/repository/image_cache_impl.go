package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
	"github.com/tphakala/birdnet-go/internal/errors"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

// imageCacheRepository implements ImageCacheRepository.
type imageCacheRepository struct {
	db          *gorm.DB
	labelRepo   LabelRepository
	useV2Prefix bool
	isMySQL     bool // For API consistency; currently unused here (used by detection_impl.go for dialect-specific SQL)
}

// NewImageCacheRepository creates a new ImageCacheRepository.
// Parameters:
//   - db: GORM database connection
//   - labelRepo: LabelRepository for resolving scientific names to label IDs
//   - useV2Prefix: true to use v2_ table prefix (MySQL migration mode)
//   - isMySQL: true for MySQL dialect (affects date/time SQL expressions)
func NewImageCacheRepository(db *gorm.DB, labelRepo LabelRepository, useV2Prefix, isMySQL bool) ImageCacheRepository {
	return &imageCacheRepository{
		db:          db,
		labelRepo:   labelRepo,
		useV2Prefix: useV2Prefix,
		isMySQL:     isMySQL,
	}
}

func (r *imageCacheRepository) tableName() string {
	if r.useV2Prefix {
		return tableV2ImageCaches
	}
	return tableImageCaches
}

// GetImageCache retrieves an image cache entry by provider and scientific name.
// Internally resolves scientific name to label ID for the lookup.
func (r *imageCacheRepository) GetImageCache(ctx context.Context, providerName, scientificName string) (*entities.ImageCache, error) {
	// Resolve scientific name to label ID
	label, err := r.labelRepo.GetByScientificName(ctx, scientificName)
	if err != nil {
		if errors.Is(err, ErrLabelNotFound) {
			return nil, ErrImageCacheNotFound
		}
		return nil, err
	}

	var cache entities.ImageCache
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Where("provider_name = ? AND label_id = ?", providerName, label.ID).
		First(&cache).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrImageCacheNotFound
	}
	if err != nil {
		return nil, err
	}

	// Attach the label for callers that need scientific name
	cache.Label = label
	return &cache, nil
}

// SaveImageCache saves or updates an image cache entry (upsert).
// The cache.LabelID must be set by the caller (resolved via LabelRepository).
func (r *imageCacheRepository) SaveImageCache(ctx context.Context, cache *entities.ImageCache) error {
	if cache.LabelID == 0 {
		return errors.NewStd("image cache LabelID must be set before saving")
	}
	return r.db.WithContext(ctx).Table(r.tableName()).
		Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "provider_name"}, {Name: "label_id"}},
			UpdateAll: true,
		}).
		Create(cache).Error
}

// GetAllImageCaches retrieves all image cache entries for a provider.
func (r *imageCacheRepository) GetAllImageCaches(ctx context.Context, providerName string) ([]entities.ImageCache, error) {
	var caches []entities.ImageCache
	err := r.db.WithContext(ctx).Table(r.tableName()).
		Preload("Label").
		Where("provider_name = ?", providerName).
		Find(&caches).Error
	return caches, err
}

// GetImageCacheBatch retrieves multiple image cache entries by scientific names.
// Returns a map keyed by scientific name for convenient lookup.
func (r *imageCacheRepository) GetImageCacheBatch(ctx context.Context, providerName string, scientificNames []string) (map[string]*entities.ImageCache, error) {
	if len(scientificNames) == 0 {
		return make(map[string]*entities.ImageCache), nil
	}

	// Batch lookup labels by scientific names
	labels, err := r.labelRepo.GetByScientificNames(ctx, scientificNames)
	if err != nil {
		return nil, err
	}

	// Build label ID list and create reverse mapping
	labelIDs := make([]uint, 0, len(labels))
	labelIDToSciName := make(map[uint]string, len(labels))
	for _, label := range labels {
		labelIDs = append(labelIDs, label.ID)
		if label.ScientificName != nil {
			labelIDToSciName[label.ID] = *label.ScientificName
		}
	}

	if len(labelIDs) == 0 {
		return make(map[string]*entities.ImageCache), nil
	}

	var caches []entities.ImageCache
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Preload("Label").
		Where("provider_name = ? AND label_id IN ?", providerName, labelIDs).
		Find(&caches).Error
	if err != nil {
		return nil, err
	}

	// Build result map keyed by scientific name
	result := make(map[string]*entities.ImageCache, len(caches))
	for i := range caches {
		if sciName, ok := labelIDToSciName[caches[i].LabelID]; ok {
			result[sciName] = &caches[i]
		}
	}
	return result, nil
}
