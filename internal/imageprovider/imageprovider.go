// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"fmt"
	"log"
	"sync"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/telemetry/metrics"
)

// ImageProvider defines the interface for fetching bird images.
type ImageProvider interface {
	Fetch(scientificName string) (BirdImage, error)
}

// BirdImage represents a cached bird image with its metadata
type BirdImage struct {
	URL            string
	ScientificName string
	LicenseName    string
	LicenseURL     string
	AuthorName     string
	AuthorURL      string
	CachedAt       time.Time
}

// BirdImageCache represents a cache for storing and retrieving bird images.
type BirdImageCache struct {
	provider     ImageProvider
	dataMap      sync.Map
	metrics      *metrics.ImageProviderMetrics
	debug        bool
	store        datastore.Interface
	logger       *log.Logger
	quit         chan struct{} // Channel to signal shutdown
	Initializing sync.Map      // Track which species are being initialized
}

// emptyImageProvider is an ImageProvider that always returns an empty BirdImage.
type emptyImageProvider struct{}

func (l *emptyImageProvider) Fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

// SetNonBirdImageProvider allows setting a custom ImageProvider for non-bird entries
func (c *BirdImageCache) SetNonBirdImageProvider(provider ImageProvider) {
	c.provider = provider
}

// SetImageProvider allows setting a custom ImageProvider for testing purposes.
func (c *BirdImageCache) SetImageProvider(provider ImageProvider) {
	c.provider = provider
}

const (
	defaultCacheTTL  = 14 * 24 * time.Hour // 14 days
	refreshInterval  = 1 * time.Hour       // How often to check for stale entries
	refreshBatchSize = 10                  // Number of entries to refresh in one batch
	refreshDelay     = 5 * time.Second     // Delay between refreshing individual entries
)

// startCacheRefresh starts the background cache refresh routine
func (c *BirdImageCache) startCacheRefresh(quit chan struct{}) {
	if c.debug {
		log.Printf("Debug: Starting cache refresh routine with TTL of %v", defaultCacheTTL)
	}

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-quit:
				if c.debug {
					log.Printf("Debug: Stopping cache refresh routine")
				}
				return
			case <-ticker.C:
				c.refreshStaleEntries()
			}
		}
	}()
}

// refreshStaleEntries refreshes cache entries that are older than TTL
func (c *BirdImageCache) refreshStaleEntries() {
	if c.store == nil {
		return
	}

	// Get all cached entries
	entries, err := c.store.GetAllImageCaches()
	if err != nil {
		if c.debug {
			log.Printf("Debug: Failed to get cached entries for refresh: %v", err)
		}
		return
	}

	// Find stale entries
	var staleEntries []datastore.ImageCache
	cutoff := time.Now().Add(-defaultCacheTTL)
	for _, entry := range entries {
		if entry.CachedAt.Before(cutoff) {
			staleEntries = append(staleEntries, entry)
		}
	}

	if len(staleEntries) == 0 {
		return
	}

	if c.debug {
		log.Printf("Debug: Found %d stale cache entries to refresh", len(staleEntries))
	}

	// Process stale entries in batches with rate limiting
	for i := 0; i < len(staleEntries); i += refreshBatchSize {
		end := i + refreshBatchSize
		if end > len(staleEntries) {
			end = len(staleEntries)
		}

		batch := staleEntries[i:end]
		for _, entry := range batch {
			select {
			case <-c.quit:
				return // Exit if we're shutting down
			case <-time.After(refreshDelay):
				c.refreshEntry(entry.ScientificName)
			}
		}
	}
}

// refreshEntry refreshes a single cache entry
func (c *BirdImageCache) refreshEntry(scientificName string) {
	if c.debug {
		log.Printf("Debug: Refreshing cache entry for %s", scientificName)
	}

	// Fetch new image
	birdImage, err := c.provider.Fetch(scientificName)
	if err != nil {
		if c.debug {
			log.Printf("Debug: Failed to refresh image for %s: %v", scientificName, err)
		}
		return
	}

	// Update memory cache
	c.dataMap.Store(scientificName, &birdImage)

	// Update database cache
	c.saveToDB(&birdImage)

	if c.metrics != nil {
		c.metrics.IncrementImageDownloads()
	}
}

// Close stops the cache refresh routine and performs cleanup
func (c *BirdImageCache) Close() error {
	if c.quit != nil {
		close(c.quit)
	}
	return nil
}

// initCache initializes a new BirdImageCache with the given ImageProvider.
func InitCache(e ImageProvider, t *telemetry.Metrics, store datastore.Interface) *BirdImageCache {
	settings := conf.Setting()

	quit := make(chan struct{})
	cache := &BirdImageCache{
		provider: e,
		metrics:  t.ImageProvider,
		debug:    settings.Realtime.Dashboard.Thumbnails.Debug,
		store:    store,
		logger:   log.Default(),
		quit:     quit,
	}

	// Load cached images into memory only if store is available
	if store != nil {
		if err := cache.loadCachedImages(); err != nil && cache.debug {
			log.Printf("Debug: Error loading cached images: %v", err)
		}
	}

	// Start cache refresh routine
	cache.startCacheRefresh(quit)

	return cache
}

// loadFromDBCache loads a BirdImage from the database cache
func (c *BirdImageCache) loadFromDBCache(scientificName string) (*BirdImage, error) {
	if c.store == nil {
		if c.debug {
			log.Printf("Debug: Database store not available, skipping cache load for %s", scientificName)
		}
		return nil, nil
	}

	cached, err := c.store.GetImageCache(scientificName)
	if err != nil {
		if c.debug {
			log.Printf("Debug: Failed to get image from cache for %s: %v", scientificName, err)
		}
		return nil, nil
	}

	if cached == nil {
		return nil, nil
	}

	return &BirdImage{
		URL:            cached.URL,
		ScientificName: cached.ScientificName,
		LicenseName:    cached.LicenseName,
		LicenseURL:     cached.LicenseURL,
		AuthorName:     cached.AuthorName,
		AuthorURL:      cached.AuthorURL,
		CachedAt:       cached.CachedAt,
	}, nil
}

// saveToDB saves a BirdImage to the database cache with retries
func (c *BirdImageCache) saveToDB(image *BirdImage) {
	if c.store == nil {
		if c.debug {
			log.Printf("Debug: Database store not available, skipping cache save for %s", image.ScientificName)
		}
		return
	}

	cached := &datastore.ImageCache{
		URL:            image.URL,
		ScientificName: image.ScientificName,
		LicenseName:    image.LicenseName,
		LicenseURL:     image.LicenseURL,
		AuthorName:     image.AuthorName,
		AuthorURL:      image.AuthorURL,
		CachedAt:       time.Now(),
	}

	if err := c.store.SaveImageCache(cached); err != nil {
		if c.debug {
			log.Printf("Debug: Failed to save image to cache for %s: %v", image.ScientificName, err)
		}
		// Continue without caching
	}
}

// loadCachedImages loads all cached images from database into memory
func (c *BirdImageCache) loadCachedImages() error {
	if c.store == nil {
		if c.debug {
			log.Printf("Debug: Database store not available, starting with empty cache")
		}
		return nil
	}

	cached, err := c.store.GetAllImageCaches()
	if err != nil {
		if c.debug {
			log.Printf("Debug: Failed to load cached images: %v", err)
		}
		return nil // Continue with empty cache
	}

	for _, cache := range cached {
		c.dataMap.Store(cache.ScientificName, &BirdImage{
			URL:            cache.URL,
			ScientificName: cache.ScientificName,
			LicenseName:    cache.LicenseName,
			LicenseURL:     cache.LicenseURL,
			AuthorName:     cache.AuthorName,
			AuthorURL:      cache.AuthorURL,
			CachedAt:       cache.CachedAt,
		})
	}

	return nil
}

// Get retrieves a bird image from the cache or fetches it if not found
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	// Check if this species is being initialized
	if _, initializing := c.Initializing.Load(scientificName); initializing {
		// Skip database lookup during initialization to avoid double lookups
		if c.debug {
			log.Printf("Debug: Species %s is being initialized, skipping database lookup", scientificName)
		}
	} else {
		// Check memory cache first
		if value, ok := c.dataMap.Load(scientificName); ok {
			if image, ok := value.(*BirdImage); ok {
				if c.debug {
					log.Printf("Debug: Found image in memory cache for: %s", scientificName)
				}
				if c.metrics != nil {
					c.metrics.IncrementCacheHits()
				}
				return *image, nil
			}
		}

		// Check database cache if not being initialized
		if image, err := c.loadFromDBCache(scientificName); err == nil && image != nil {
			c.dataMap.Store(scientificName, image)
			if c.metrics != nil {
				c.metrics.IncrementCacheHits()
			}
			return *image, nil
		}
	}

	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
	}

	// Check if provider is set
	if c.provider == nil {
		if c.debug {
			log.Printf("Debug: No image provider available for: %s", scientificName)
		}
		return BirdImage{}, fmt.Errorf("image provider not available")
	}

	if c.debug {
		log.Printf("Debug: Fetching image for species: %s", scientificName)
	}

	startTime := time.Now()
	birdImage, err := c.provider.Fetch(scientificName)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrors()
		}
		// Pass through the user-friendly error from the provider
		return BirdImage{}, err
	}

	if c.metrics != nil {
		c.metrics.ObserveDownloadDuration(duration)
		c.metrics.IncrementImageDownloads()
	}

	// Save to memory cache
	c.dataMap.Store(scientificName, &birdImage)

	// Save to database cache
	c.saveToDB(&birdImage)

	return birdImage, nil
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
		if img, ok := value.(*BirdImage); ok {
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
	} else {
		log.Println("Warning: Unable to update metrics, ImageProviderMetrics is nil")
	}
}

// CreateDefaultCache creates a new BirdImageCache with the default WikiMedia image provider.
func CreateDefaultCache(metrics *telemetry.Metrics, store datastore.Interface) (*BirdImageCache, error) {
	// Create the default WikiMedia provider
	provider, err := NewWikiMediaProvider()
	if err != nil {
		return nil, fmt.Errorf("failed to create WikiMedia provider: %w", err)
	}

	cache := &BirdImageCache{
		provider: provider,
		metrics:  metrics.ImageProvider,
		debug:    false,
		store:    store,
		logger:   log.Default(),
		quit:     make(chan struct{}),
	}

	return cache, nil
}
