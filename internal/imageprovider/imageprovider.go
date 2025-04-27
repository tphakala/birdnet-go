// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"context"
	"errors"
	"fmt"
	"log"
	"sync"
	"sync/atomic"
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
	SourceProvider string // The actual provider that supplied the image
}

// BirdImageCache represents a cache for storing and retrieving bird images.
type BirdImageCache struct {
	provider     ImageProvider
	providerName string // Added: Name of the provider (e.g., "wikimedia")
	dataMap      sync.Map
	metrics      *metrics.ImageProviderMetrics
	debug        bool
	store        datastore.Interface
	logger       *log.Logger
	quit         chan struct{}                         // Channel to signal shutdown
	Initializing sync.Map                              // Track which species are being initialized
	registry     atomic.Pointer[ImageProviderRegistry] // Use atomic pointer
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
	if c.debug {
		log.Printf("Debug: Starting cache refresh routine with TTL of %v", defaultCacheTTL)
	}

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		// Run an immediate refresh when starting
		c.refreshStaleEntries()

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

	// Get all cached entries for this provider
	entries, err := c.store.GetAllImageCaches(c.providerName) // Use provider name
	if err != nil {
		if c.debug {
			log.Printf("Debug: [%s] Failed to get cached entries for refresh: %v", c.providerName, err)
		}
		return
	}

	if c.debug {
		log.Printf("Debug: [%s] Checking %d entries for staleness", c.providerName, len(entries))
	}

	// Find stale entries
	var staleEntries []string // Store only scientific names instead of full entries
	cutoff := time.Now().Add(-defaultCacheTTL)
	for i := range entries {
		if entries[i].CachedAt.Before(cutoff) {
			if c.debug {
				log.Printf("Debug: [%s] Found stale entry: %s (CachedAt: %v)", c.providerName, entries[i].ScientificName, entries[i].CachedAt)
			}
			staleEntries = append(staleEntries, entries[i].ScientificName)
		}
	}

	if len(staleEntries) == 0 {
		if c.debug {
			log.Printf("Debug: [%s] No stale entries found", c.providerName)
		}
		return
	}

	if c.debug {
		log.Printf("Debug: [%s] Found %d stale cache entries to refresh", c.providerName, len(staleEntries))
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
	if c.debug {
		log.Printf("Debug: Refreshing cache entry for %s", scientificName)
	}

	// Check if provider is set
	if c.provider == nil {
		if c.debug {
			log.Printf("Debug: No provider available for %s", scientificName)
		}
		return
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
func InitCache(providerName string, e ImageProvider, t *telemetry.Metrics, store datastore.Interface) *BirdImageCache {
	settings := conf.Setting()

	quit := make(chan struct{})
	cache := &BirdImageCache{
		provider:     e,
		providerName: providerName, // Set provider name
		metrics:      t.ImageProvider,
		debug:        settings.Realtime.Dashboard.Thumbnails.Debug,
		store:        store,
		logger:       log.Default(),
		quit:         quit,
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
	// Check if store is nil to prevent nil pointer dereference
	if c.store == nil {
		if c.debug {
			log.Printf("Debug [%s]: DB store is nil, cannot load from cache for %s", c.providerName, scientificName)
		}
		return nil, nil
	}

	var cachedImage *datastore.ImageCache // Correct type based on GetImageCache return
	var err error
	query := datastore.ImageCacheQuery{ // Pass query by value
		ScientificName: scientificName,
		ProviderName:   c.providerName, // Query based on *this* cache's provider name
	}
	cachedImage, err = c.store.GetImageCache(query) // Use GetImageCache and handle two return values
	if err != nil {
		if c.debug {
			log.Printf("Debug [%s]: DB cache miss or error for %s: %v", c.providerName, scientificName, err)
		}
		// Propagate actual DB errors
		return nil, err
	}
	// Handle case where GetImageCache returns nil, nil (not found but no error)
	if cachedImage == nil {
		if c.debug {
			log.Printf("Debug [%s]: DB cache miss (nil result) for %s", c.providerName, scientificName)
		}
		// Return nil, nil to indicate not found without an error, matching previous behavior
		return nil, nil
	}

	if c.debug {
		log.Printf("Debug [%s]: DB cache hit for %s", c.providerName, scientificName)
	}

	// Convert datastore model to BirdImage
	birdImage := BirdImage{
		URL:            cachedImage.URL,
		ScientificName: cachedImage.ScientificName,
		LicenseName:    cachedImage.LicenseName,
		LicenseURL:     cachedImage.LicenseURL,
		AuthorName:     cachedImage.AuthorName,
		AuthorURL:      cachedImage.AuthorURL,
		CachedAt:       cachedImage.CachedAt,
		SourceProvider: cachedImage.SourceProvider, // Load the source provider
	}

	// Update memory cache
	c.dataMap.Store(scientificName, &birdImage)

	if c.metrics != nil {
		c.metrics.IncrementCacheHits()
	}

	return &birdImage, nil
}

// saveToDB saves a BirdImage to the database cache
func (c *BirdImageCache) saveToDB(image *BirdImage) {
	if c.store == nil {
		return // Datastore is not configured
	}

	// Validate the image has a scientific name
	if image == nil || image.ScientificName == "" {
		if c.debug {
			log.Printf("Debug [%s]: Attempted to save image with empty scientific name, skipping", c.providerName)
		}
		return // Skip saving images with empty scientific names
	}

	// Determine the CachedAt time
	cachedAt := image.CachedAt
	if cachedAt.IsZero() {
		cachedAt = time.Now()
	}

	// Convert BirdImage to datastore model
	cacheEntry := &datastore.ImageCache{ // Use pointer type for SaveImageCache
		ProviderName:   c.providerName, // Save under *this* cache's provider name
		ScientificName: image.ScientificName,
		SourceProvider: image.SourceProvider, // Save the actual source provider
		URL:            image.URL,
		LicenseName:    image.LicenseName,
		LicenseURL:     image.LicenseURL,
		AuthorName:     image.AuthorName,
		AuthorURL:      image.AuthorURL,
		CachedAt:       cachedAt, // Use determined timestamp
	}

	if err := c.store.SaveImageCache(cacheEntry); err != nil { // Use SaveImageCache
		if c.debug {
			log.Printf("Error saving image %s for provider %s to DB cache: %v", image.ScientificName, c.providerName, err)
		}
		c.logger.Printf("Error saving image %s for provider %s to DB cache: %v", image.ScientificName, c.providerName, err)
	}
}

// loadCachedImages loads all cached images from database into memory
func (c *BirdImageCache) loadCachedImages() error {
	if c.store == nil {
		if c.debug {
			log.Printf("Debug: [%s] Database store not available, starting with empty cache", c.providerName)
		}
		return nil
	}

	cached, err := c.store.GetAllImageCaches(c.providerName) // Use provider name
	if err != nil {
		if c.debug {
			log.Printf("Debug: [%s] Failed to load cached images: %v", c.providerName, err)
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
			SourceProvider: entry.SourceProvider,
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
			if c.debug {
				log.Printf("Debug: No image provider available for: %s", scientificName)
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
	// Validate scientific name is not empty
	if scientificName == "" {
		return BirdImage{}, fmt.Errorf("scientific name cannot be empty")
	}

	// Check memory cache first for quick return
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

	startTime := time.Now()
	maxTotalTime := 2 * time.Second // Maximum total time including all retries and final fetch

	// Try to acquire initialization lock
	initAttempts := 0
	maxAttempts := 3 // Maximum number of initialization attempts
	for initAttempts < maxAttempts {
		// Check if we've exceeded total time
		if time.Since(startTime) > maxTotalTime {
			if c.debug {
				log.Printf("Debug: Total time exceeded for %s, proceeding with direct fetch", scientificName)
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
				if c.debug {
					log.Printf("Debug: Found image in memory cache for: %s after waiting", scientificName)
				}
				if c.metrics != nil {
					c.metrics.IncrementCacheHits()
				}
				return *image, nil
			}
		}
		if c.debug {
			log.Printf("Debug: Initialization wait timeout for %s, attempt %d", scientificName, initAttempts+1)
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
		if c.debug {
			log.Printf("Debug: Context timeout, attempting direct fetch for: %s", scientificName)
		}
		return c.fetchAndStore(scientificName)
	case <-done:
		return result, fetchErr
	}
}

// fetchAndStore handles the fetching and storing of an image
func (c *BirdImageCache) fetchAndStore(scientificName string) (BirdImage, error) {
	if c.debug {
		log.Printf("Debug: Fetching image for species: %s", scientificName)
	}

	// Validate scientific name is not empty
	if scientificName == "" {
		return BirdImage{}, fmt.Errorf("scientific name cannot be empty")
	}

	settings := conf.Setting()
	preferredProvider := settings.Realtime.Dashboard.Thumbnails.ImageProvider
	fallbackPolicy := settings.Realtime.Dashboard.Thumbnails.FallbackPolicy

	// Try to fetch using the configured provider preference
	var birdImage BirdImage
	var err error

	// Check if we should directly use a specific provider first
	if c.GetRegistry() != nil && preferredProvider != "auto" && preferredProvider != c.providerName {
		// Try the user's preferred provider first (if it exists and it's not the current provider)
		if preferredCache, ok := c.GetRegistry().GetCache(preferredProvider); ok {
			if c.debug {
				log.Printf("Debug: Using preferred provider '%s' for %s", preferredProvider, scientificName)
			}

			startTime := time.Now()
			preferredImage, preferredErr := preferredCache.Get(scientificName)
			duration := time.Since(startTime).Seconds()

			if preferredErr == nil {
				// Successfully retrieved from preferred provider
				if c.metrics != nil {
					c.metrics.ObserveDownloadDuration(duration)
					c.metrics.IncrementImageDownloads()
				}

				// Save to memory cache
				c.dataMap.Store(scientificName, &preferredImage)

				// Save to database cache using the *preferred* provider's cache
				// This maintains correct attribution of the image source
				preferredCache.saveToDB(&preferredImage)

				return preferredImage, nil
			}

			if c.debug {
				log.Printf("Debug: Preferred provider '%s' failed for %s: %v", preferredProvider, scientificName, preferredErr)
			}

			// If fallback is disabled, return the error from preferred provider
			if fallbackPolicy == "none" {
				if c.metrics != nil {
					c.metrics.IncrementDownloadErrors()
				}
				return BirdImage{}, preferredErr
			}
		}
	}

	// Use this provider (either it's the preferred one or we're falling back)
	startTime := time.Now()
	birdImage, err = c.provider.Fetch(scientificName)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrors()
		}

		// If registry is available and fallback is enabled, try other providers
		registry := c.GetRegistry() // Load registry pointer
		if registry != nil && fallbackPolicy == "all" {
			// Create a map of providers already tried to avoid duplicates
			triedProviders := make(map[string]bool)
			triedProviders[c.providerName] = true
			if preferredProvider != "auto" {
				triedProviders[preferredProvider] = true
			}

			// Try all other available providers
			var fallbackSuccessful bool // Declare without initial assignment
			birdImage, fallbackSuccessful = c.tryFallbackProviders(scientificName, triedProviders)

			if fallbackSuccessful {
				return birdImage, nil
			}
		}

		return BirdImage{}, err
	}

	if c.metrics != nil {
		c.metrics.ObserveDownloadDuration(duration)
		c.metrics.IncrementImageDownloads()
	}

	// Set the source provider before saving
	birdImage.SourceProvider = c.providerName

	// Save to memory cache
	c.dataMap.Store(scientificName, &birdImage)

	// Save to database cache
	c.saveToDB(&birdImage)

	return birdImage, nil
}

// tryFallbackProviders attempts to fetch an image from other providers in the registry.
func (c *BirdImageCache) tryFallbackProviders(scientificName string, triedProviders map[string]bool) (BirdImage, bool) {
	var birdImage BirdImage
	fallbackSuccessful := false

	registry := c.GetRegistry() // Load registry pointer
	if registry == nil {
		return birdImage, false // No registry for fallback
	}

	registry.RangeProviders(func(name string, cache *BirdImageCache) bool {
		if name == c.providerName || triedProviders[name] {
			return true // Skip self or already tried providers
		}

		if c.debug {
			log.Printf("Debug [%s]: Trying fallback provider '%s' for %s", c.providerName, name, scientificName)
		}

		triedProviders[name] = true // Mark as tried
		// Use fetchDirect for fallback to avoid recursion
		fallbackImage, fallbackErr := cache.fetchDirect(scientificName)

		if fallbackErr == nil {
			// Successfully retrieved from fallback
			// metrics are already updated inside cache.fetchDirect

			// Set the source provider to the fallback provider's name
			fallbackImage.SourceProvider = cache.providerName

			// Save to the *original* caller's memory cache
			c.dataMap.Store(scientificName, &fallbackImage)

			// Save ONLY to the *original* (calling) provider's database cache
			// The source_provider field indicates where it actually came from.
			c.saveToDB(&fallbackImage)

			birdImage = fallbackImage
			fallbackSuccessful = true
			return false // stop ranging
		}

		if c.debug {
			log.Printf("Debug: Fallback to '%s' failed for %s: %v", name, scientificName, fallbackErr)
		}

		return true // continue ranging
	})

	return birdImage, fallbackSuccessful
}

// fetchDirect attempts to fetch an image directly using this cache's provider,
// bypassing complex locking and fallback logic found in Get.
// It updates the current cache instance if successful.
func (c *BirdImageCache) fetchDirect(scientificName string) (BirdImage, error) {
	if c.provider == nil {
		return BirdImage{}, fmt.Errorf("provider not set for %s cache", c.providerName)
	}

	// Validate scientific name is not empty
	if scientificName == "" {
		return BirdImage{}, fmt.Errorf("scientific name cannot be empty")
	}

	if c.debug {
		log.Printf("Debug [%s]: Direct fetching image for species: %s", c.providerName, scientificName)
	}

	startTime := time.Now()
	birdImage, err := c.provider.Fetch(scientificName)
	duration := time.Since(startTime).Seconds()

	if err != nil {
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrors()
		}
		if c.debug {
			log.Printf("Debug [%s]: Direct fetch failed for %s: %v", c.providerName, scientificName, err)
		}
		return BirdImage{}, err
	}

	if c.metrics != nil {
		c.metrics.ObserveDownloadDuration(duration)
		c.metrics.IncrementImageDownloads()
	}

	// Set the source provider before saving
	birdImage.SourceProvider = c.providerName

	// Save to this cache's memory and DB
	c.dataMap.Store(scientificName, &birdImage)
	c.saveToDB(&birdImage)

	if c.debug {
		log.Printf("Debug [%s]: Direct fetch successful for %s, updated cache.", c.providerName, scientificName)
	}
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
// The provider name is fixed to "wikimedia".
func CreateDefaultCache(metrics *telemetry.Metrics, store datastore.Interface) (*BirdImageCache, error) {
	// Create the default WikiMedia provider
	provider, err := NewWikiMediaProvider()
	if err != nil {
		return nil, fmt.Errorf("failed to create WikiMedia provider: %w", err)
	}

	// Use a fixed provider name for the default cache
	const defaultProviderName = "wikimedia"

	return InitCache(defaultProviderName, provider, metrics, store), nil
}

// --- Image Provider Registry ---

// ImageProviderRegistry holds multiple named BirdImageCache instances.
type ImageProviderRegistry struct {
	caches map[string]*BirdImageCache
	mu     sync.RWMutex
}

// NewImageProviderRegistry creates a new registry.
func NewImageProviderRegistry() *ImageProviderRegistry {
	return &ImageProviderRegistry{
		caches: make(map[string]*BirdImageCache),
	}
}

// Register adds a new cache instance to the registry.
// It returns an error if a cache with the same name already exists.
func (r *ImageProviderRegistry) Register(name string, cache *BirdImageCache) error {
	// Validate inputs
	if name == "" {
		return fmt.Errorf("provider name cannot be empty")
	}
	if cache == nil {
		return fmt.Errorf("cannot register nil cache for provider '%s'", name)
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.caches[name]; exists {
		return fmt.Errorf("image provider cache named '%s' already registered", name)
	}
	r.caches[name] = cache
	return nil
}

// GetCache retrieves a cache instance by name.
func (r *ImageProviderRegistry) GetCache(name string) (*BirdImageCache, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cache, ok := r.caches[name]
	return cache, ok
}

// GetImage retrieves an image using the specified provider cache.
// It returns an error if the provider name is not registered.
func (r *ImageProviderRegistry) GetImage(providerName, scientificName string) (BirdImage, error) {
	// Validate inputs
	if providerName == "" {
		return BirdImage{}, fmt.Errorf("provider name cannot be empty")
	}
	if scientificName == "" {
		return BirdImage{}, fmt.Errorf("scientific name cannot be empty")
	}

	cache, ok := r.GetCache(providerName)
	if !ok {
		return BirdImage{}, fmt.Errorf("no image provider cache registered for name '%s'", providerName)
	}
	return cache.Get(scientificName)
}

// CloseAll gracefully shuts down all registered caches.
func (r *ImageProviderRegistry) CloseAll() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	var errs []error
	for name, cache := range r.caches {
		if err := cache.Close(); err != nil {
			errs = append(errs, fmt.Errorf("failed to close cache '%s': %w", name, err))
		}
	}
	return errors.Join(errs...)
}

// SetRegistry allows setting the provider registry for fallback providers
func (c *BirdImageCache) SetRegistry(registry *ImageProviderRegistry) {
	c.registry.Store(registry) // Use atomic Store
}

// GetRegistry returns the associated provider registry
func (c *BirdImageCache) GetRegistry() *ImageProviderRegistry {
	return c.registry.Load() // Use atomic Load
}

// RangeProviders iterates over all registered caches, applying the callback function.
// It creates a snapshot of the cache map to avoid concurrent modification issues
// during iteration.
func (r *ImageProviderRegistry) RangeProviders(cb func(name string, cache *BirdImageCache) bool) {
	r.mu.RLock()
	snapshot := make(map[string]*BirdImageCache, len(r.caches))
	for k, v := range r.caches {
		snapshot[k] = v
	}
	r.mu.RUnlock()

	for name, cache := range snapshot {
		if !cb(name, cache) {
			return // Callback requested stop
		}
	}
}

// GetCaches returns a copy of the internal cache map.
// This is primarily for testing or diagnostic purposes where a snapshot is needed.
func (r *ImageProviderRegistry) GetCaches() map[string]*BirdImageCache {
	r.mu.RLock()
	defer r.mu.RUnlock()
	cachesCopy := make(map[string]*BirdImageCache, len(r.caches))
	for name, cache := range r.caches {
		cachesCopy[name] = cache
	}
	return cachesCopy
}

// GetBatch fetches multiple bird images at once and returns them as a map
// This is more efficient than multiple individual Get calls when many images are needed
func (c *BirdImageCache) GetBatch(scientificNames []string) map[string]BirdImage {
	result := make(map[string]BirdImage, len(scientificNames))

	// First check memory cache for all items (fast path)
	missingNames := make([]string, 0, len(scientificNames))

	for _, name := range scientificNames {
		if name == "" {
			continue
		}

		// Check memory cache first
		if value, ok := c.dataMap.Load(name); ok {
			if image, ok := value.(*BirdImage); ok {
				if c.debug {
					log.Printf("Debug: Found image in batch memory cache for: %s", name)
				}
				if c.metrics != nil {
					c.metrics.IncrementCacheHits()
				}
				result[name] = *image
				continue
			}
		}

		// If not in memory cache, add to list for fetching
		missingNames = append(missingNames, name)
	}

	// If all were in memory cache, return early
	if len(missingNames) == 0 {
		return result
	}

	// For each missing name, fetch individually
	// We could potentially parallelize this in the future
	for _, name := range missingNames {
		image, err := c.Get(name)
		if err == nil {
			result[name] = image
		}
	}

	return result
}
