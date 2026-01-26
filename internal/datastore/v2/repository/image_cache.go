package repository

import (
	"context"

	"github.com/tphakala/birdnet-go/internal/datastore/v2/entities"
)

// ImageCacheRepository handles image cache operations.
type ImageCacheRepository interface {
	GetImageCache(ctx context.Context, providerName, scientificName string) (*entities.ImageCache, error)
	SaveImageCache(ctx context.Context, cache *entities.ImageCache) error
	GetAllImageCaches(ctx context.Context, providerName string) ([]entities.ImageCache, error)
	GetImageCacheBatch(ctx context.Context, providerName string, scientificNames []string) (map[string]*entities.ImageCache, error)
}
