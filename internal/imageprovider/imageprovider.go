// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/telemetry/metrics"
)

// ImageProvider defines the interface for fetching bird images.
type ImageProvider interface {
	fetch(scientificName string) (BirdImage, error)
}

// BirdImage represents the metadata of a bird image.
type BirdImage struct {
	URL         string // The URL of the image
	LicenseName string // The name of the license for the image
	LicenseURL  string // The URL of the license details
	AuthorName  string // The name of the image author
	AuthorURL   string // The URL of the author's page or profile
}

// BirdImageCache represents a cache for storing and retrieving bird images.
type BirdImageCache struct {
	dataMap              sync.Map
	dataMutexMap         sync.Map
	birdImageProvider    ImageProvider
	nonBirdImageProvider ImageProvider
	metrics              *metrics.ImageProviderMetrics
}

// emptyImageProvider is an ImageProvider that always returns an empty BirdImage.
type emptyImageProvider struct{}

func (l *emptyImageProvider) fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

// initCache initializes a new BirdImageCache with the given ImageProvider.
func initCache(e ImageProvider, t *telemetry.Metrics) *BirdImageCache {
	return &BirdImageCache{
		birdImageProvider:    e,
		nonBirdImageProvider: &emptyImageProvider{},
		metrics:              t.ImageProvider,
	}
}

// CreateDefaultCache creates a new BirdImageCache with the default WikiMedia image provider.
func CreateDefaultCache(metrics *telemetry.Metrics) (*BirdImageCache, error) {
	provider, err := NewWikiMediaProvider()
	if err != nil {
		return nil, err
	}
	return initCache(provider, metrics), nil
}

// Get retrieves the BirdImage for a given scientific name from the cache,
// fetching it from the provider if not already cached.
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	if c.metrics == nil {
		log.Println("Warning: ImageProviderMetrics is nil")
	}

	if birdImage, ok := c.dataMap.Load(scientificName); ok {
		if c.metrics != nil {
			c.metrics.IncrementCacheHits()
			//log.Printf("Cache hit for %s", scientificName)
		}
		return birdImage.(BirdImage), nil
	}

	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
		//log.Printf("Cache miss for %s", scientificName)
	}

	mu, _ := c.dataMutexMap.LoadOrStore(scientificName, &sync.Mutex{})
	mutex := mu.(*sync.Mutex)

	mutex.Lock()
	defer mutex.Unlock()

	if birdImage, ok := c.dataMap.Load(scientificName); ok {
		c.metrics.IncrementCacheHits()
		return birdImage.(BirdImage), nil
	}

	fetchedBirdImage, err := c.fetch(scientificName)
	if err != nil {
		c.dataMap.Store(scientificName, BirdImage{})
		c.metrics.IncrementDownloadErrors()
		return BirdImage{}, err
	}

	c.dataMap.Store(scientificName, fetchedBirdImage)
	c.metrics.IncrementImageDownloads()
	c.updateMetrics()

	return fetchedBirdImage, nil
}

// fetch retrieves the BirdImage for a given scientific name from the appropriate provider.
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

	startTime := time.Now()
	birdImage, err := imageProviderToUse.fetch(scientificName)
	duration := time.Since(startTime).Seconds()
	c.metrics.ObserveDownloadDuration(duration)

	return birdImage, err
}

// EstimateSize estimates the memory size of a BirdImage instance in bytes.
func (img *BirdImage) EstimateSize() int {
	return int(unsafe.Sizeof(*img)) +
		len(img.URL) + len(img.LicenseName) +
		len(img.LicenseURL) + len(img.AuthorName) +
		len(img.AuthorURL)
}

// MemoryUsage returns the approximate memory usage of the image cache in bytes.
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

// updateMetrics updates all metrics associated with the image cache.
func (c *BirdImageCache) updateMetrics() {
	if c.metrics != nil {
		size := float64(c.MemoryUsage())
		c.metrics.SetCacheSize(size)
		//log.Printf("Updated cache size: %f bytes", size)
	} else {
		log.Println("Warning: Unable to update metrics, ImageProviderMetrics is nil")
	}
}
