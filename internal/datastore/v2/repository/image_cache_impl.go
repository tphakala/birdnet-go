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

// ensureLabelRepo returns an error if labelRepo is nil.
// This guards against misconfiguration that would cause nil pointer panics.
func (r *imageCacheRepository) ensureLabelRepo() error {
	if r.labelRepo == nil {
		return errors.NewStd("label repository not configured for image cache repository")
	}
	return nil
}

// GetImageCache retrieves an image cache entry by provider and scientific name.
// Internally resolves scientific name to label IDs (cross-model) for the lookup.
// Images are model-agnostic, so returns the first matching cache entry.
func (r *imageCacheRepository) GetImageCache(ctx context.Context, providerName, scientificName string) (*entities.ImageCache, error) {
	if err := r.ensureLabelRepo(); err != nil {
		return nil, err
	}
	// Resolve scientific name to label IDs (cross-model lookup)
	labelIDs, err := r.labelRepo.GetLabelIDsByScientificName(ctx, scientificName)
	if err != nil {
		return nil, err
	}
	if len(labelIDs) == 0 {
		return nil, ErrImageCacheNotFound
	}

	var cache entities.ImageCache
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Preload("Label").
		Where("provider_name = ? AND label_id IN ?", providerName, labelIDs).
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
// Uses cross-model lookup - images are model-agnostic.
func (r *imageCacheRepository) GetImageCacheBatch(ctx context.Context, providerName string, scientificNames []string) (map[string]*entities.ImageCache, error) {
	if len(scientificNames) == 0 {
		return make(map[string]*entities.ImageCache), nil
	}

	if err := r.ensureLabelRepo(); err != nil {
		return nil, err
	}

	// Batch fetch all labels for scientific names (avoids N+1 queries)
	labelsByName, err := r.labelRepo.GetByScientificNames(ctx, scientificNames)
	if err != nil {
		return nil, err
	}

	// Collect all label IDs and build reverse mapping
	var allLabelIDs []uint
	labelIDToSciName := make(map[uint]string)
	for sciName, labels := range labelsByName {
		for _, label := range labels {
			allLabelIDs = append(allLabelIDs, label.ID)
			labelIDToSciName[label.ID] = sciName
		}
	}

	if len(allLabelIDs) == 0 {
		return make(map[string]*entities.ImageCache), nil
	}

	var caches []entities.ImageCache
	err = r.db.WithContext(ctx).Table(r.tableName()).
		Preload("Label").
		Where("provider_name = ? AND label_id IN ?", providerName, allLabelIDs).
		Find(&caches).Error
	if err != nil {
		return nil, err
	}

	// Build result map keyed by scientific name
	// For duplicate sci names (multiple models), first cache wins
	result := make(map[string]*entities.ImageCache, len(caches))
	for i := range caches {
		if sciName, ok := labelIDToSciName[caches[i].LabelID]; ok {
			if _, exists := result[sciName]; !exists {
				result[sciName] = &caches[i]
			}
		}
	}
	return result, nil
}
