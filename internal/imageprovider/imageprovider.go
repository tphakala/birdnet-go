package imageprovider

import (
	"sync"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/telemetry"
)

type ImageProvider interface {
	fetch(scientificName string) (BirdImage, error)
}

type BirdImage struct {
	Url         string
	LicenseName string
	LicenseUrl  string
	AuthorName  string
	AuthorUrl   string
}

// BirdImageCache represents a cache for bird images.
type BirdImageCache struct {
	dataMap              sync.Map
	dataMutexMap         sync.Map
	birdImageProvider    ImageProvider
	nonBirdImageProvider ImageProvider
	Metrics              *telemetry.Metrics
}

type emptyImageProvider struct {
}

// fetch returns an empty BirdImage
func (l *emptyImageProvider) fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

// initCache initializes the bird image cache with the given image provider
func initCache(e ImageProvider) *BirdImageCache {
	return &BirdImageCache{
		birdImageProvider:    e,
		nonBirdImageProvider: &emptyImageProvider{}, // TODO: Use a real image provider for non-birds
	}
}

// CreateDefaultCache creates a new bird image cache with the default image provider
func CreateDefaultCache() (*BirdImageCache, error) {
	provider, err := NewWikiMediaProvider()
	if err != nil {
		return nil, err
	}
	return initCache(provider), nil
}

// Get retrieves the bird image for a given scientific name from the cache or fetches it if not present
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	// Check if the bird image is already in the cache
	if birdImage, ok := c.dataMap.Load(scientificName); ok {
		return birdImage.(BirdImage), nil
	}

	// Use a per-item mutex to ensure only one query is performed per item
	mu, _ := c.dataMutexMap.LoadOrStore(scientificName, &sync.Mutex{})
	mutex := mu.(*sync.Mutex)

	mutex.Lock()
	defer mutex.Unlock()

	// Check again if bird image is cached after acquiring the lock
	if birdImage, ok := c.dataMap.Load(scientificName); ok {
		return birdImage.(BirdImage), nil
	}

	// Fetch the bird image from the image provider
	fetchedBirdImage, err := c.fetch(scientificName)
	if err != nil {
		// TODO for now store an empty result in the cache to avoid future queries that would fail.
		// In the future, look at the error and decide if it was caused by networking and is recoverable.
		// And if it was, do not store the empty result in the cache.
		c.dataMap.Store(scientificName, BirdImage{})
		return BirdImage{}, err
	}

	// Store the fetched image information in the cache
	c.dataMap.Store(scientificName, fetchedBirdImage)

	return fetchedBirdImage, nil
}

// fetch retrieves the bird image for a given scientific name
func (c *BirdImageCache) fetch(scientificName string) (BirdImage, error) {
	nonBirdScientificNames := map[string]struct{}{
		"Dog": {}, "Engine": {}, "Environmental": {}, "Fireworks": {},
		"Gun": {}, "Human non-vocal": {}, "Human vocal": {}, "Human whistle": {},
		"Noise": {}, "Power tools": {}, "Siren": {},
	}

	var imageProviderToUse ImageProvider

	if _, isNonBird := nonBirdScientificNames[scientificName]; isNonBird {
		imageProviderToUse = c.nonBirdImageProvider
	} else {
		imageProviderToUse = c.birdImageProvider
	}

	// Fetch the image from the image provider
	return imageProviderToUse.fetch(scientificName)
}

// EstimateSize estimates the memory size of a BirdImage instance in bytes
func (img *BirdImage) EstimateSize() int {
	return int(unsafe.Sizeof(*img)) +
		len(img.Url) + len(img.LicenseName) +
		len(img.LicenseUrl) + len(img.AuthorName) +
		len(img.AuthorUrl)
}

// MemoryUsage returns the approximate memory usage of the image cache in bytes
func (c *BirdImageCache) MemoryUsage() int {
	totalSize := 0
	c.dataMap.Range(func(key, value interface{}) bool {
		if img, ok := value.(BirdImage); ok {
			totalSize += img.EstimateSize()
		}
		return true
	})
	return totalSize
}

// UpdateMetrics updates all metrics associated with the image cache.
func (c *BirdImageCache) UpdateMetrics() {
	if c.Metrics != nil {
		c.Metrics.SetImageCacheSize(c.MemoryUsage())
	}
}
