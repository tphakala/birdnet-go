// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"errors"
	"fmt"
	"log"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logging"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"github.com/tphakala/birdnet-go/internal/telemetry/metrics"
)

// ErrImageNotFound indicates that the image provider could not find an image for the requested species.
var ErrImageNotFound = errors.New("image not found by provider")

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

// Package-level logger for image provider related events
var (
	imageProviderLogger *slog.Logger
	// imageProviderLogCloser func() error // Optional closer func
	// TODO: Call imageProviderLogCloser during graceful shutdown if needed
)

func init() {
	var err error
	// Default level is Info. Set to Debug for more detailed cache/provider info.
	imageProviderLogger, _, err = logging.NewFileLogger("logs/imageprovider.log", "imageprovider", slog.LevelInfo)
	if err != nil {
		logging.Error("Failed to initialize imageprovider file logger", "error", err)
		// Fallback to the default structured logger
		imageProviderLogger = logging.Structured().With("service", "imageprovider")
		if imageProviderLogger == nil {
			panic(fmt.Sprintf("Failed to initialize any logger for imageprovider service: %v", err))
		}
		logging.Warn("Imageprovider service falling back to default logger due to file logger initialization error.")
	} else {
		logging.Info("Imageprovider file logger initialized successfully", "path", "logs/imageprovider.log")
	}
	// imageProviderLogCloser = closer
}

// emptyImageProvider is an ImageProvider that always returns an empty BirdImage.
type emptyImageProvider struct{}

func (l *emptyImageProvider) Fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

// SetNonBirdImageProvider allows setting a custom ImageProvider for non-bird entries
func (c *BirdImageCache) SetNonBirdImageProvider(provider ImageProvider) {
	imageProviderLogger.Debug("Setting non-bird image provider", "provider_type", fmt.Sprintf("%T", provider))
	c.provider = provider
}

// SetImageProvider allows setting a custom ImageProvider for testing purposes.
func (c *BirdImageCache) SetImageProvider(provider ImageProvider) {
	imageProviderLogger.Debug("Setting image provider (test override)", "provider_type", fmt.Sprintf("%T", provider))
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
	logger := imageProviderLogger.With("provider", c.providerName)
	logger.Info("Starting cache refresh routine", "ttl", defaultCacheTTL, "interval", refreshInterval)
	// if c.debug {
	// 	log.Printf("Debug: Starting cache refresh routine with TTL of %v", defaultCacheTTL)
	// }

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		// Run an immediate refresh when starting
		logger.Info("Running initial cache refresh check")
		c.refreshStaleEntries()

		for {
			select {
			case <-quit:
				logger.Info("Stopping cache refresh routine")
				// if c.debug {
				// 	log.Printf("Debug: Stopping cache refresh routine")
				// }
				return
			case <-ticker.C:
				logger.Debug("Ticker interval elapsed, checking for stale entries")
				c.refreshStaleEntries()
			}
		}
	}()
}

// refreshStaleEntries refreshes cache entries that are older than TTL
func (c *BirdImageCache) refreshStaleEntries() {
	logger := imageProviderLogger.With("provider", c.providerName)
	if c.store == nil {
		logger.Debug("DB store is nil, skipping cache refresh")
		return
	}

	logger.Debug("Getting all cached entries for refresh check")
	entries, err := c.store.GetAllImageCaches(c.providerName) // Use provider name
	if err != nil {
		logger.Error("Failed to get cached entries for refresh", "error", err)
		// if c.debug {
		// 	log.Printf("Debug: [%s] Failed to get cached entries for refresh: %v", c.providerName, err)
		// }
		return
	}

	logger.Debug("Checking entries for staleness", "entry_count", len(entries), "ttl", defaultCacheTTL)
	// if c.debug {
	// 	log.Printf("Debug: [%s] Checking %d entries for staleness", c.providerName, len(entries))
	// }

	// Find stale entries
	var staleEntries []string // Store only scientific names instead of full entries
	cutoff := time.Now().Add(-defaultCacheTTL)
	for i := range entries {
		if entries[i].CachedAt.Before(cutoff) {
			logger.Debug("Found stale entry", "scientific_name", entries[i].ScientificName, "cached_at", entries[i].CachedAt, "cutoff", cutoff)
			// if c.debug {
			// 	log.Printf("Debug: [%s] Found stale entry: %s (CachedAt: %v)", c.providerName, entries[i].ScientificName, entries[i].CachedAt)
			// }
			staleEntries = append(staleEntries, entries[i].ScientificName)
		}
	}

	if len(staleEntries) == 0 {
		logger.Debug("No stale entries found")
		// if c.debug {
		// 	log.Printf("Debug: [%s] No stale entries found", c.providerName)
		// }
		return
	}

	logger.Info("Found stale cache entries to refresh", "count", len(staleEntries))
	// if c.debug {
	// 	log.Printf("Debug: [%s] Found %d stale cache entries to refresh", c.providerName, len(staleEntries))
	// }

	// Process stale entries in batches with rate limiting
	logger.Debug("Processing stale entries", "batch_size", refreshBatchSize, "delay_between_entries", refreshDelay)
	for i := 0; i < len(staleEntries); i += refreshBatchSize {
		end := i + refreshBatchSize
		if end > len(staleEntries) {
			end = len(staleEntries)
		}

		batch := staleEntries[i:end]
		logger.Debug("Processing batch of stale entries", "batch_start_index", i, "batch_end_index", end, "batch_size", len(batch))
		for _, scientificName := range batch {
			select {
			case <-c.quit:
				logger.Info("Cache refresh routine quit signal received during batch processing")
				return // Exit if we're shutting down
			case <-time.After(refreshDelay):
				c.refreshEntry(scientificName)
			}
		}
	}
	logger.Info("Finished processing stale entries")
}

// refreshEntry refreshes a single cache entry
func (c *BirdImageCache) refreshEntry(scientificName string) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)
	logger.Info("Refreshing cache entry")
	// if c.debug {
	// 	log.Printf("Debug: Refreshing cache entry for %s", scientificName)
	// }

	// Check if provider is set
	if c.provider == nil {
		logger.Warn("Cannot refresh entry: provider is nil")
		// if c.debug {
		// 	log.Printf("Debug: No provider available for %s", scientificName)
		// }
		return
	}

	// Fetch new image
	logger.Debug("Fetching new image data from provider")
	birdImage, err := c.provider.Fetch(scientificName)
	if err != nil {
		logger.Error("Failed to fetch image during refresh", "error", err)
		// if c.debug {
		// 	log.Printf("Debug: Failed to refresh image for %s: %v", scientificName, err)
		// }
		return
	}

	// Update memory cache
	logger.Debug("Updating memory cache with refreshed image")
	c.dataMap.Store(scientificName, &birdImage)

	// Update database cache
	logger.Debug("Updating database cache with refreshed image")
	c.saveToDB(&birdImage)

	if c.metrics != nil {
		c.metrics.IncrementImageDownloads()
	}
	logger.Info("Successfully refreshed cache entry")
}

// Close stops the cache refresh routine and performs cleanup
func (c *BirdImageCache) Close() error {
	imageProviderLogger.Info("Closing image provider cache", "provider", c.providerName)
	if c.quit != nil {
		select {
		case <-c.quit:
			// Already closed
			imageProviderLogger.Debug("Quit channel already closed")
		default:
			imageProviderLogger.Debug("Closing quit channel")
			close(c.quit)
		}
	}
	return nil
}

// initCache initializes a new BirdImageCache with the given ImageProvider.
func InitCache(providerName string, e ImageProvider, t *telemetry.Metrics, store datastore.Interface) *BirdImageCache {
	logger := imageProviderLogger.With("provider", providerName)
	logger.Info("Initializing image cache")
	settings := conf.Setting()

	quit := make(chan struct{})
	cache := &BirdImageCache{
		provider:     e,
		providerName: providerName, // Set provider name
		metrics:      t.ImageProvider,
		debug:        settings.Realtime.Dashboard.Thumbnails.Debug, // Keep for potential checks
		store:        store,
		// logger:       log.Default(), // Replaced by package logger
		quit: quit,
	}

	// Load cached images into memory only if store is available
	if store != nil {
		logger.Info("DB store available, loading cached images")
		if err := cache.loadCachedImages(); err != nil {
			logger.Error("Error loading cached images", "error", err)
			// if cache.debug {
			// 	log.Printf("Debug: Error loading cached images: %v", err)
			// }
		}
	} else {
		logger.Info("DB store not available, skipping loading cached images")
	}

	// Start cache refresh routine
	cache.startCacheRefresh(quit)

	logger.Info("Image cache initialization complete")
	return cache
}

// loadFromDBCache loads a BirdImage from the database cache
func (c *BirdImageCache) loadFromDBCache(scientificName string) (*BirdImage, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)
	logger.Debug("Attempting to load image from DB cache")
	// Check if store is nil to prevent nil pointer dereference
	if c.store == nil {
		logger.Warn("Cannot load from DB cache: DB store is nil")
		// if c.debug {
		// 	log.Printf("Debug [%s]: DB store is nil, cannot load from cache for %s", c.providerName, scientificName)
		// }
		return nil, nil
	}

	var cachedImage *datastore.ImageCache // Correct type based on GetImageCache return
	var err error
	query := datastore.ImageCacheQuery{ // Pass query by value
		ScientificName: scientificName,
		ProviderName:   c.providerName, // Query based on *this* cache's provider name
	}
	logger.Debug("Querying DB for cached image")
	cachedImage, err = c.store.GetImageCache(query) // Use GetImageCache and handle two return values
	if err != nil {
		// Log database errors, but don't treat 'not found' as an error for the cache
		logger.Error("Failed to get image from DB cache", "error", err)
		// A specific not found error isn't exported, so we'll return nil, nil later if cachedImage is nil
		// For now, just return the error to indicate a DB problem occurred
		return nil, fmt.Errorf("failed to query image cache for %s (%s): %w", scientificName, c.providerName, err)
	}

	// Check if cachedImage is nil (indicates 'not found' since err was nil)
	if cachedImage == nil {
		logger.Debug("Image not found in DB cache (GetImageCache returned nil)")
		return nil, nil // Return nil, nil to indicate cache miss
	}

	logger.Debug("Image found in DB cache", "cached_at", cachedImage.CachedAt)
	// Convert datastore.ImageCache to imageprovider.BirdImage
	birdImage := &BirdImage{
		URL:            cachedImage.URL,
		ScientificName: cachedImage.ScientificName,
		LicenseName:    cachedImage.LicenseName,
		LicenseURL:     cachedImage.LicenseURL,
		AuthorName:     cachedImage.AuthorName,
		AuthorURL:      cachedImage.AuthorURL,
		CachedAt:       cachedImage.CachedAt,
		SourceProvider: cachedImage.ProviderName, // Store the original provider
	}
	return birdImage, nil
}

// saveToDB saves a BirdImage to the database cache
func (c *BirdImageCache) saveToDB(image *BirdImage) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", image.ScientificName)
	// Check if store is nil
	if c.store == nil {
		logger.Warn("Cannot save to DB cache: DB store is nil")
		return
	}

	// Check if image URL is empty - don't save empty entries
	if image.URL == "" {
		logger.Debug("Skipping save to DB: image URL is empty")
		return
	}

	logger.Debug("Saving image to DB cache", "url", image.URL, "source_provider", image.SourceProvider)
	dbEntry := &datastore.ImageCache{
		ScientificName: image.ScientificName,
		ProviderName:   image.SourceProvider, // Save the actual provider that gave the image
		URL:            image.URL,
		LicenseName:    image.LicenseName,
		LicenseURL:     image.LicenseURL,
		AuthorName:     image.AuthorName,
		AuthorURL:      image.AuthorURL,
		CachedAt:       time.Now(), // Update cached timestamp
	}

	if err := c.store.SaveImageCache(dbEntry); err != nil {
		logger.Error("Failed to save image to DB cache", "error", err)
	}
}

// loadCachedImages loads all relevant cached images from the DB into memory
func (c *BirdImageCache) loadCachedImages() error {
	logger := imageProviderLogger.With("provider", c.providerName)
	logger.Info("Loading all cached images from DB into memory")
	if c.store == nil {
		logger.Warn("Cannot load cached images: DB store is nil")
		return errors.New("datastore is nil")
	}

	entries, err := c.store.GetAllImageCaches(c.providerName) // Get entries specific to this provider
	if err != nil {
		logger.Error("Failed to get all image caches from DB", "error", err)
		return fmt.Errorf("failed to get all image caches for %s: %w", c.providerName, err)
	}

	loadedCount := 0
	now := time.Now()
	cutoff := now.Add(-defaultCacheTTL)

	for i := range entries {
		// Only load non-stale entries into memory
		if entries[i].CachedAt.After(cutoff) {
			birdImage := &BirdImage{
				URL:            entries[i].URL,
				ScientificName: entries[i].ScientificName,
				LicenseName:    entries[i].LicenseName,
				LicenseURL:     entries[i].LicenseURL,
				AuthorName:     entries[i].AuthorName,
				AuthorURL:      entries[i].AuthorURL,
				CachedAt:       entries[i].CachedAt,
				SourceProvider: entries[i].ProviderName,
			}
			c.dataMap.Store(birdImage.ScientificName, birdImage)
			loadedCount++
		} else {
			logger.Debug("Skipping load of stale DB entry into memory cache",
				"scientific_name", entries[i].ScientificName,
				"cached_at", entries[i].CachedAt)
		}
	}

	logger.Info("Finished loading cached images into memory", "loaded_count", loadedCount, "total_db_entries_checked", len(entries))
	return nil
}

// tryInitialize ensures only one goroutine initializes a species image using mutexes.
// It returns the image, a boolean indicating if it was found in cache (true) or fetched (false), and an error.
func (c *BirdImageCache) tryInitialize(scientificName string) (BirdImage, bool, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)

	// Fast path: check if already loaded
	if val, ok := c.dataMap.Load(scientificName); ok {
		if imgPtr, ok := val.(*BirdImage); ok && imgPtr != nil {
			logger.Debug("Initialization check: already in memory cache (fast path)")
			return *imgPtr, true, nil
		}
	}

	// Use a mutex for this specific scientific name to prevent concurrent fetches
	muInterface, _ := c.Initializing.LoadOrStore(scientificName, &sync.Mutex{})
	mu := muInterface.(*sync.Mutex)
	mu.Lock()
	defer func() {
		mu.Unlock()
		// Clean up the mutex from the map once the operation is done.
		// It's okay if another goroutine added it again between Unlock and Delete.
		c.Initializing.Delete(scientificName)
		logger.Debug("Unlocked and cleaned up mutex")
	}()

	logger.Debug("Acquired initialization lock")

	// Double check: check cache again *after* acquiring the lock,
	// in case another goroutine finished initializing while we were waiting.
	if val, ok := c.dataMap.Load(scientificName); ok {
		if imgPtr, ok := val.(*BirdImage); ok && imgPtr != nil {
			logger.Debug("Initialization check: found in memory cache after acquiring lock")
			return *imgPtr, true, nil // Indicate it was found in cache
		}
	}

	logger.Debug("Not in cache after lock, proceeding to fetch/store")
	// Fetch and store the image
	img, err := c.fetchAndStore(scientificName)
	return img, false, err // false indicates this goroutine attempted the fetch
}

// Get retrieves a bird image from the cache, fetching if necessary.
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)
	logger.Debug("Get image request received")
	// Use tryInitialize to handle concurrent initialization
	img, foundInCache, err := c.tryInitialize(scientificName)
	if err != nil {
		if !errors.Is(err, ErrImageNotFound) {
			logger.Error("Failed to initialize or fetch image (tryInitialize returned error)", "error", err)
		}
		// Even if initialization failed, maybe a fallback provider has it?
		// This requires the registry to be set.
		registry := c.GetRegistry()
		if registry != nil {
			triedProviders := map[string]bool{c.providerName: true}
			logger.Info("Primary provider failed, attempting fallback", "initial_error", err)
			fallbackImg, found := c.tryFallbackProviders(scientificName, triedProviders)
			if found {
				logger.Info("Image found via fallback provider", "fallback_provider", fallbackImg.SourceProvider)
				// Optionally store the fallback result in this cache's memory map?
				// c.dataMap.Store(scientificName, &fallbackImg)
				return fallbackImg, nil
			}
			logger.Warn("Image not found via fallback providers either")
		}
		// Return the original error if no fallback worked or registry wasn't set
		return BirdImage{}, err
	}

	if foundInCache {
		logger.Debug("Image found in cache, returning cached result")
		if c.metrics != nil {
			c.metrics.IncrementCacheHits()
		}
		return img, nil
	}

	logger.Debug("Image initialized by this goroutine (cache miss), returning fetched/loaded result")
	// Note: Cache miss tracking is already handled in fetchAndStore
	// if loaded from DB or when fetched from provider
	return img, nil
}

// fetchAndStore tries to load from DB, then fetches from the provider if necessary, and stores the result.
func (c *BirdImageCache) fetchAndStore(scientificName string) (BirdImage, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)
	logger.Debug("Fetching and storing image (memory cache miss)")

	// 1. Try loading from DB cache first
	dbImage, err := c.loadFromDBCache(scientificName)
	if err != nil {
		// Logged within loadFromDBCache
		// Continue to provider fetch, but log this DB error
		logger.Warn("Error loading from DB cache, proceeding to fetch from provider", "db_error", err)
	}
	if dbImage != nil {
		// Check if the DB entry is stale - if so, trigger refresh but return stale data for now
		// Or should we block and wait for refresh? For now, return stale and let background refresh handle it.
		cutoff := time.Now().Add(-defaultCacheTTL)
		if dbImage.CachedAt.Before(cutoff) {
			logger.Info("DB cache entry is stale, returning stale data and triggering background refresh", "cached_at", dbImage.CachedAt)
			// Trigger background refresh non-blockingly
			go c.refreshEntry(scientificName)
		} else {
			logger.Info("Image loaded from DB cache")
		}
		// Store in memory cache and return
		c.dataMap.Store(scientificName, dbImage)
		if c.metrics != nil {
			c.metrics.IncrementCacheMisses() // It was a memory miss, but DB hit
		}
		return *dbImage, nil
	}

	// 2. Not in DB or DB load failed, fetch from the actual provider
	logger.Info("Image not found in DB cache, fetching from provider")
	if c.provider == nil {
		logger.Error("Cannot fetch image: provider is nil")
		return BirdImage{}, fmt.Errorf("image provider for %s is not configured", c.providerName)
	}

	// Check if this provider is specifically disabled in config
	// This requires access to the main settings, maybe pass relevant part to InitCache?
	// For now, assume provider passed to InitCache is enabled.

	fetchedImage, fetchErr := c.provider.Fetch(scientificName)
	if fetchErr != nil {
		logger.Error("Failed to fetch image from provider", "error", fetchErr)
		// Store a negative cache entry? For now, just return error.
		// Check if it's a 'not found' error vs a transient error
		if errors.Is(fetchErr, ErrImageNotFound) {
			// Store negative cache result to avoid refetching known misses?
			// Maybe store an empty BirdImage with a timestamp?
			logger.Warn("Image explicitly not found by provider")
			emptyResult := BirdImage{ScientificName: scientificName, SourceProvider: c.providerName, CachedAt: time.Now()} // Mark as checked
			c.dataMap.Store(scientificName, &emptyResult)                                                                  // Store placeholder in memory
			// Don't save empty results to DB? Or save with empty URL?
			// c.saveToDB(&emptyResult) // Decide on persistence strategy for misses
			return BirdImage{}, fetchErr // Return the specific ErrImageNotFound
		}
		// For other errors, don't store anything and return the error
		return BirdImage{}, fmt.Errorf("failed to fetch image for %s from %s: %w", scientificName, c.providerName, fetchErr)
	}

	// If fetch was successful but returned an empty URL (provider couldn't find it)
	if fetchedImage.URL == "" {
		logger.Warn("Provider returned success but with an empty image URL")
		emptyResult := BirdImage{ScientificName: scientificName, SourceProvider: c.providerName, CachedAt: time.Now()} // Mark as checked
		c.dataMap.Store(scientificName, &emptyResult)                                                                  // Store placeholder in memory
		// c.saveToDB(&emptyResult) // Decide on persistence
		return BirdImage{}, ErrImageNotFound // Treat empty URL as not found
	}

	// 3. Successfully fetched from provider
	fetchedImage.CachedAt = time.Now()           // Set cache time
	fetchedImage.SourceProvider = c.providerName // Ensure provider name is set
	logger.Info("Image successfully fetched from provider", "url", fetchedImage.URL)

	// Store in memory cache
	c.dataMap.Store(scientificName, &fetchedImage)
	// Store in DB cache
	c.saveToDB(&fetchedImage)

	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()    // Memory miss
		c.metrics.IncrementImageDownloads() // Fetched from external provider
	}

	return fetchedImage, nil
}

// tryFallbackProviders attempts to get the image from other registered providers.
func (c *BirdImageCache) tryFallbackProviders(scientificName string, triedProviders map[string]bool) (BirdImage, bool) {
	logger := imageProviderLogger.With("scientific_name", scientificName)
	logger.Info("Trying fallback providers")
	registry := c.GetRegistry()
	if registry == nil {
		logger.Warn("Cannot try fallback providers: registry is not set")
		return BirdImage{}, false
	}

	var foundImage BirdImage
	found := false

	registry.RangeProviders(func(name string, cache *BirdImageCache) bool {
		if triedProviders[name] {
			logger.Debug("Skipping already tried provider", "provider", name)
			return true // Continue ranging
		}

		logger.Info("Attempting fallback fetch from provider", "provider", name)
		triedProviders[name] = true // Mark as tried

		// Use the fallback cache's Get method - it handles its own caching/fetching
		img, err := cache.Get(scientificName)
		if err != nil {
			// Log error but continue trying other fallbacks
			logger.Warn("Fallback provider failed to get image", "provider", name, "error", err)
			return true // Continue ranging
		}

		// Check if a valid image was found (URL is not empty)
		if img.URL != "" {
			logger.Info("Image found via fallback provider", "provider", name, "url", img.URL)
			foundImage = img
			found = true
			return false // Stop ranging, we found one
		} else {
			logger.Debug("Fallback provider returned empty image", "provider", name)
			// Continue ranging if this provider returned an empty image
			return true
		}
	})

	if found {
		logger.Info("Fallback successful", "found_provider", foundImage.SourceProvider)
	} else {
		logger.Info("Fallback unsuccessful, image not found in any provider")
	}
	return foundImage, found
}

// fetchDirect performs a direct fetch from the provider without cache interaction.
func (c *BirdImageCache) fetchDirect(scientificName string) (BirdImage, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)
	logger.Debug("Performing direct fetch from provider (bypassing cache checks)")
	if c.provider == nil {
		logger.Error("Cannot perform direct fetch: provider is nil")
		return BirdImage{}, fmt.Errorf("image provider %s is not configured", c.providerName)
	}

	img, err := c.provider.Fetch(scientificName)
	if err != nil {
		logger.Error("Direct fetch failed", "error", err)
		return BirdImage{}, err
	}

	img.CachedAt = time.Now() // Set time even though it's not 'cached'
	img.SourceProvider = c.providerName
	logger.Debug("Direct fetch successful", "url", img.URL)
	return img, nil
}

// EstimateSize estimates the size of the BirdImage struct.
func (img *BirdImage) EstimateSize() int {
	// Basic estimation, adjust as needed
	size := int(unsafe.Sizeof(*img))
	size += len(img.URL)
	size += len(img.ScientificName)
	size += len(img.LicenseName)
	size += len(img.LicenseURL)
	size += len(img.AuthorName)
	size += len(img.AuthorURL)
	size += len(img.SourceProvider)
	return size
}

// MemoryUsage estimates the total memory usage of the cache map.
func (c *BirdImageCache) MemoryUsage() int {
	totalSize := 0
	c.dataMap.Range(func(key, value interface{}) bool {
		if scientificName, ok := key.(string); ok {
			totalSize += len(scientificName) // Add key size
		}
		if img, ok := value.(*BirdImage); ok && img != nil {
			totalSize += img.EstimateSize() // Add value size
		}
		return true
	})
	return totalSize
}

// updateMetrics updates prometheus metrics based on cache state.
func (c *BirdImageCache) updateMetrics() {
	if c.metrics == nil {
		return
	}
	// Revert to using the single SetCacheSize metric based on previous implementation
	sizeBytes := float64(c.MemoryUsage())
	c.metrics.SetCacheSize(sizeBytes)
	imageProviderLogger.Debug("Updated cache metrics", "provider", c.providerName, "size_bytes", sizeBytes)
	// c.metrics.SetMemoryCacheEntries(float64(count)) // Method doesn't exist
	// c.metrics.SetMemoryCacheSizeBytes(float64(c.MemoryUsage())) // Method doesn't exist
}

// CreateDefaultCache creates the default BirdImageCache (currently Wikimedia Commons via Wikipedia API).
func CreateDefaultCache(metrics *telemetry.Metrics, store datastore.Interface) (*BirdImageCache, error) {
	// Use the correct constructor name from wikipedia.go
	provider, err := NewWikiMediaProvider()
	if err != nil {
		imageProviderLogger.Error("Failed to create WikiMedia image provider", "error", err)
		return nil, fmt.Errorf("failed to create wikimedia image provider: %w", err)
	}
	// Using "wikimedia" as the provider name aligns with the constructor used
	return InitCache("wikimedia", provider, metrics, store), nil
}

// --- Image Provider Registry ---

// ImageProviderRegistry holds multiple named ImageProvider caches.
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
