// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"context"
	"fmt" // Kept for interface compatibility
	"sync"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
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
	logger       *logger.Logger
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
	defaultCacheTTL  = 14 * 24 * time.Hour    // 14 days
	refreshInterval  = 1 * time.Second        // How often to check for stale entries (shortened for testing)
	refreshBatchSize = 10                     // Number of entries to refresh in one batch
	refreshDelay     = 100 * time.Millisecond // Delay between refreshing individual entries (shortened for testing)
)

// startCacheRefresh starts the background cache refresh routine
func (c *BirdImageCache) startCacheRefresh(quit chan struct{}) {
	if c.debug && c.logger != nil {
		c.logger.Debug("Starting cache refresh routine",
			"ttl", defaultCacheTTL)
	}

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		// Run an immediate refresh when starting
		c.refreshStaleEntries()

		for {
			select {
			case <-quit:
				if c.debug && c.logger != nil {
					c.logger.Debug("Stopping cache refresh routine")
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
		if c.debug && c.logger != nil {
			c.logger.Debug("Failed to get cached entries for refresh",
				"error", err)
		}
		return
	}

	if c.debug && c.logger != nil {
		c.logger.Debug("Checking entries for staleness",
			"count", len(entries))
	}

	// Find stale entries
	var staleEntries []string // Store only scientific names instead of full entries
	cutoff := time.Now().Add(-defaultCacheTTL)
	for i := range entries {
		if entries[i].CachedAt.Before(cutoff) {
			if c.debug && c.logger != nil {
				c.logger.Debug("Found stale entry",
					"species", entries[i].ScientificName,
					"cached_at", entries[i].CachedAt)
			}
			staleEntries = append(staleEntries, entries[i].ScientificName)
		}
	}

	if len(staleEntries) == 0 {
		if c.debug && c.logger != nil {
			c.logger.Debug("No stale entries found")
		}
		return
	}

	if c.debug && c.logger != nil {
		c.logger.Debug("Found stale cache entries to refresh",
			"count", len(staleEntries))
	}

	// Process stale entries in batches with rate limiting
	for i := 0; i < len(staleEntries); i += refreshBatchSize {
		end := i + refreshBatchSize
		if end > len(staleEntries) {
			end = len(staleEntries)
		}

		batch := staleEntries[i:end]
		for _, scientificName := range batch {
			select {
			case <-c.quit:
				return // Exit if we're shutting down
			case <-time.After(refreshDelay):
				c.refreshEntry(scientificName)
			}
		}
	}
}

// refreshEntry refreshes a single cache entry
func (c *BirdImageCache) refreshEntry(scientificName string) {
	if c.debug && c.logger != nil {
		c.logger.Debug("Refreshing cache entry",
			"species", scientificName)
	}

	// Check if provider is set
	if c.provider == nil {
		if c.debug && c.logger != nil {
			c.logger.Debug("No provider available",
				"species", scientificName)
		}
		return
	}

	// Fetch new image
	birdImage, err := c.provider.Fetch(scientificName)
	if err != nil {
		if c.debug && c.logger != nil {
			c.logger.Debug("Failed to refresh image",
				"species", scientificName,
				"error", err)
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
func InitCache(e ImageProvider, t *telemetry.Metrics, store datastore.Interface, parentLogger *logger.Logger) *BirdImageCache {
	settings := conf.Setting()

	// Use the parent logger or fall back to global logger
	var componentLogger *logger.Logger
	if parentLogger != nil {
		componentLogger = parentLogger.Named("imageprovider.cache")
	} else {
		// Fallback to global logger (will be removed after migration)
		componentLogger = logger.GetGlobal().Named("imageprovider.cache")
	}

	quit := make(chan struct{})
	cache := &BirdImageCache{
		provider: e,
		metrics:  t.ImageProvider,
		debug:    settings.Realtime.Dashboard.Thumbnails.Debug,
		store:    store,
		logger:   componentLogger,
		quit:     quit,
	}

	// Start a goroutine to periodically refresh the cache
	go cache.startCacheRefresh(quit)

	// Attempt to initialize by loading from the database
	if err := cache.loadCachedImages(); err != nil && cache.debug {
		componentLogger.Error("Failed to load images from database", "error", err)
	}

	return cache
}

// loadFromDBCache loads a BirdImage from the database cache
func (c *BirdImageCache) loadFromDBCache(scientificName string) (*BirdImage, error) {
	if c.store == nil {
		if c.debug && c.logger != nil {
			c.logger.Debug("Database store not available, skipping cache load",
				"species", scientificName)
		}
		return nil, nil
	}

	cached, err := c.store.GetImageCache(scientificName)
	if err != nil {
		if c.debug && c.logger != nil {
			c.logger.Debug("Failed to get image from cache",
				"species", scientificName,
				"error", err)
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
		if c.debug && c.logger != nil {
			c.logger.Debug("Database store not available, skipping cache save",
				"species", image.ScientificName)
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
		if c.debug && c.logger != nil {
			c.logger.Debug("Failed to save image to cache",
				"species", image.ScientificName,
				"error", err)
		}
		// Continue without caching
	}
}

// loadCachedImages loads all cached images from database into memory
func (c *BirdImageCache) loadCachedImages() error {
	if c.store == nil {
		if c.debug && c.logger != nil {
			c.logger.Debug("Database store not available, starting with empty cache")
		}
		return nil
	}

	cached, err := c.store.GetAllImageCaches()
	if err != nil {
		if c.debug && c.logger != nil {
			c.logger.Debug("Failed to load cached images", "error", err)
		}
		return nil // Continue with empty cache
	}

	for i := range cached {
		entry := &cached[i] // Use pointer to avoid copying
		c.dataMap.Store(entry.ScientificName, &BirdImage{
			URL:            entry.URL,
			ScientificName: entry.ScientificName,
			LicenseName:    entry.LicenseName,
			LicenseURL:     entry.LicenseURL,
			AuthorName:     entry.AuthorName,
			AuthorURL:      entry.AuthorURL,
			CachedAt:       entry.CachedAt,
		})
	}

	return nil
}

// tryInitialize attempts to initialize the cache entry for a species
func (c *BirdImageCache) tryInitialize(scientificName string) (BirdImage, bool, error) {
	// Try to acquire the lock
	if _, initializing := c.Initializing.LoadOrStore(scientificName, true); !initializing {
		defer c.Initializing.Delete(scientificName)

		// Check database cache first
		if image, err := c.loadFromDBCache(scientificName); err == nil && image != nil {
			c.dataMap.Store(scientificName, image)
			if c.metrics != nil {
				c.metrics.IncrementCacheHits()
			}
			return *image, true, nil
		}

		if c.metrics != nil {
			c.metrics.IncrementCacheMisses()
		}

		// Check if provider is set
		if c.provider == nil {
			if c.debug && c.logger != nil {
				c.logger.Debug("No image provider available",
					"species", scientificName)
			}
			return BirdImage{}, false, fmt.Errorf("image provider not available")
		}

		image, err := c.fetchAndStore(scientificName)
		return image, true, err
	}
	return BirdImage{}, false, nil
}

// Get retrieves a bird image from the cache or fetches it if not found
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	// Check memory cache first for quick return
	if value, ok := c.dataMap.Load(scientificName); ok {
		if image, ok := value.(*BirdImage); ok {
			if c.debug && c.logger != nil {
				c.logger.Debug("Found image in memory cache",
					"species", scientificName)
			}
			if c.metrics != nil {
				c.metrics.IncrementCacheHits()
			}
			return *image, nil
		}
	}

	startTime := time.Now()
	maxTotalTime := 2 * time.Second // Maximum total time including all retries and final fetch

	// Try to acquire initialization lock
	initAttempts := 0
	maxAttempts := 3 // Maximum number of initialization attempts
	for initAttempts < maxAttempts {
		// Check if we've exceeded total time
		if time.Since(startTime) > maxTotalTime {
			if c.debug && c.logger != nil {
				c.logger.Debug("Total time exceeded, proceeding with direct fetch",
					"species", scientificName)
			}
			break
		}

		// Try to initialize
		if image, ok, err := c.tryInitialize(scientificName); ok {
			return image, err
		}

		// Someone else has the lock, wait with timeout
		timer := time.NewTimer(300 * time.Millisecond)
		<-timer.C
		// Check if the entry is now in cache before trying again
		if value, ok := c.dataMap.Load(scientificName); ok {
			if image, ok := value.(*BirdImage); ok {
				if c.debug && c.logger != nil {
					c.logger.Debug("Found image in memory cache after waiting",
						"species", scientificName)
				}
				if c.metrics != nil {
					c.metrics.IncrementCacheHits()
				}
				return *image, nil
			}
		}
		if c.debug && c.logger != nil {
			c.logger.Debug("Initialization wait timeout",
				"species", scientificName,
				"attempt", initAttempts+1)
		}
		timer.Stop()
		initAttempts++
	}

	// Force clear any stale initialization state
	c.Initializing.Delete(scientificName)

	// Final attempt with remaining time
	remainingTime := maxTotalTime - time.Since(startTime)
	if remainingTime < 0 {
		remainingTime = 100 * time.Millisecond // Minimum time for final attempt
	}

	// Create a context with the remaining time as timeout
	ctx, cancel := context.WithTimeout(context.Background(), remainingTime)
	defer cancel()

	// Try one final time with the remaining time budget
	done := make(chan struct{})
	var result BirdImage
	var fetchErr error

	go func() {
		result, fetchErr = c.fetchAndStore(scientificName)
		close(done)
	}()

	select {
	case <-ctx.Done():
		// Instead of returning an error, try one last direct fetch
		if c.debug && c.logger != nil {
			c.logger.Debug("Context timeout, attempting direct fetch",
				"species", scientificName)
		}
		return c.fetchAndStore(scientificName)
	case <-done:
		return result, fetchErr
	}
}

// fetchAndStore handles the fetching and storing of an image
func (c *BirdImageCache) fetchAndStore(scientificName string) (BirdImage, error) {
	if c.debug && c.logger != nil {
		c.logger.Debug("Fetching image for species",
			"species", scientificName)
	}

	startTime := time.Now()
	birdImage, err := c.provider.Fetch(scientificName)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrors()
		}
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
	}
}

// CreateDefaultCache creates a new BirdImageCache with the default WikiMedia image provider.
func CreateDefaultCache(metrics *telemetry.Metrics, store datastore.Interface, parentLogger *logger.Logger) (*BirdImageCache, error) {
	// Create the default WikiMedia provider
	provider, err := NewWikiMediaProvider(parentLogger)
	if err != nil {
		return nil, fmt.Errorf("failed to create WikiMedia provider: %w", err)
	}

	// Use InitCache which now handles logger dependency injection
	return InitCache(provider, metrics, store, parentLogger), nil
}
