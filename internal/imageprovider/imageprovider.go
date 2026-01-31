// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"context"
	"fmt"
	"maps"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// ErrImageNotFound indicates that the image provider could not find an image for the requested species.
var ErrImageNotFound = errors.Newf("image not found by provider").
	Component("imageprovider").
	Category(errors.CategoryImageFetch).
	Context("error_type", "not_found").
	Build()

// ErrCacheMiss indicates that the requested image was not found in the cache.
// This sentinel error is used instead of returning nil, nil to avoid nilnil linter violations
// while maintaining clear error semantics.
var ErrCacheMiss = errors.Newf("image not found in cache").
	Component("imageprovider").
	Category(errors.CategoryImageCache).
	Context("error_type", "cache_miss").
	Build()

// ErrProviderNotConfigured indicates that the provider is not configured for use.
// This is a normal operational state, not an error - the provider correctly identifies
// that it should not be used based on current configuration.
var ErrProviderNotConfigured = errors.Newf("provider not configured for current settings").
	Component("imageprovider").
	Category(errors.CategoryConfiguration).
	Context("error_type", "provider_not_configured").
	Context("operational_state", "normal").
	Build()

// ErrProviderNil indicates that no image provider has been set.
var ErrProviderNil = errors.Newf("image provider is nil").
	Component("imageprovider").
	Category(errors.CategoryConfiguration).
	Context("error_type", "provider_nil").
	Build()

// contextKey is a type used for context keys to avoid collisions
type contextKey string

// backgroundOperationKey is the context key for background operations
const backgroundOperationKey contextKey = "background"

// isRealError checks if an error is a genuine error (not a cache miss)
func isRealError(err error) bool {
	return err != nil && !errors.Is(err, ErrCacheMiss)
}

// ImageProvider defines the interface for fetching bird images.
type ImageProvider interface {
	Fetch(scientificName string) (BirdImage, error)
}

// ProviderStatusChecker defines an interface for checking if a provider should actively
// perform operations (like cache refreshes) without requiring full initialization.
// This allows providers to be registered for UI discovery while being operationally inactive.
type ProviderStatusChecker interface {
	ShouldRefreshCache() bool
}

// BirdImage represents a cached bird image with its metadata and attribution information
type BirdImage struct {
	URL            string    // Direct URL to the bird image
	ScientificName string    // Scientific name of the bird species
	LicenseName    string    // Name of the content license (e.g., "CC BY-SA 4.0")
	LicenseURL     string    // URL to the full license text
	AuthorName     string    // Name of the image author/photographer
	AuthorURL      string    // URL to the author's profile or homepage
	CachedAt       time.Time // Timestamp when the image was cached
	SourceProvider string    // Name of the provider that supplied the image (e.g., "wikimedia", "avicommons")
}

// IsNegativeEntry checks if this is a negative cache entry (not found)
func (b *BirdImage) IsNegativeEntry() bool {
	return b.URL == negativeEntryMarker
}

// GetTTL returns the appropriate TTL for this cache entry
func (b *BirdImage) GetTTL() time.Duration {
	if b.IsNegativeEntry() {
		return negativeCacheTTL
	}
	return defaultCacheTTL
}

// BirdImageCache represents a cache for storing and retrieving bird images.
//
// Thread Safety: BirdImageCache is safe for concurrent use. The provider field can be
// changed at runtime using SetImageProvider/SetNonBirdImageProvider methods, and is
// protected using atomic operations. This is necessary because a background refresh
// goroutine may be accessing the provider while tests or other code changes it.
type BirdImageCache struct {
	provider     atomic.Pointer[ImageProvider] // Atomic pointer for lock-free concurrent access
	providerName string                        // Added: Name of the provider (e.g., "wikimedia")
	dataMap      sync.Map
	metrics      *metrics.ImageProviderMetrics
	debug        bool
	store        datastore.Interface
	quit         chan struct{}                         // Channel to signal shutdown
	Initializing sync.Map                              // Track which species are being initialized
	registry     atomic.Pointer[ImageProviderRegistry] // Use atomic pointer
}

// GetLogger returns the package logger for the imageprovider module
func GetLogger() logger.Logger {
	return logger.Global().Module("imageprovider")
}

// imageProviderLogger is a package-level logger for convenience in functions
// that don't have access to *BirdImageCache context.
// Note: This uses slog-style API for backward compatibility with existing code.
var imageProviderLogger = GetLogger()

// emptyImageProvider is an ImageProvider that always returns an empty BirdImage.
type emptyImageProvider struct{}

func (l *emptyImageProvider) Fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

// SetNonBirdImageProvider allows setting a custom ImageProvider for non-bird entries
func (c *BirdImageCache) SetNonBirdImageProvider(provider ImageProvider) {
	GetLogger().Debug("Setting non-bird image provider",
		logger.String("provider_type", fmt.Sprintf("%T", provider)))
	c.provider.Store(&provider)
}

// SetImageProvider allows setting a custom ImageProvider for testing purposes.
func (c *BirdImageCache) SetImageProvider(provider ImageProvider) {
	GetLogger().Debug("Setting image provider (test override)",
		logger.String("provider_type", fmt.Sprintf("%T", provider)))
	c.provider.Store(&provider)
}

const (
	defaultCacheTTL     = 14 * 24 * time.Hour // 14 days for positive entries
	negativeCacheTTL    = 15 * time.Minute    // 15 minutes for negative entries
	refreshInterval     = 1 * time.Hour       // Check for stale entries every hour in production
	refreshBatchSize    = 10                  // Number of entries to refresh in one batch
	refreshDelay        = 2 * time.Second     // Delay between refreshing individual entries
	negativeEntryMarker = "__NOT_FOUND__"     // Special URL marker for negative cache entries

	// Configuration constants
	fallbackPolicyAll = "all" // Fallback policy to allow all providers
	percentMultiplier = 100   // Multiplier for percentage calculations

	// Performance threshold constants
	dbCacheLookupSlowThreshold   = 50 * time.Millisecond  // Threshold for slow DB cache lookups
	providerFetchSlowThreshold   = 100 * time.Millisecond // Threshold for slow provider fetch operations
	totalFetchSlowThreshold      = 200 * time.Millisecond // Threshold for slow total fetch operations
)

// fallbackProviders defines the ordered list of providers to try when the primary provider fails.
// The order matters: avicommons is tried first as it's faster (local data), then wikimedia (remote API).
var fallbackProviders = []string{"avicommons", "wikimedia"}

// --- Shared Helper Functions ---

// shouldQuit checks if the cache's quit channel has been signaled.
// Returns true if shutdown was requested, false otherwise.
func (c *BirdImageCache) shouldQuit() bool {
	select {
	case <-c.quit:
		return true
	default:
		return false
	}
}

// getProvider safely retrieves the image provider, returning an error if nil.
func (c *BirdImageCache) getProvider() (ImageProvider, error) {
	providerPtr := c.provider.Load()
	if providerPtr == nil {
		return nil, ErrProviderNil
	}
	return *providerPtr, nil
}

// isCacheEntryStale checks if a cache entry has exceeded its TTL.
// Negative entries (not found) have a shorter TTL than positive entries.
func isCacheEntryStale(cachedAt time.Time, isNegative bool) bool {
	var ttl time.Duration
	if isNegative {
		ttl = negativeCacheTTL
	} else {
		ttl = defaultCacheTTL
	}
	cutoff := time.Now().Add(-ttl)
	return cachedAt.Before(cutoff)
}

// dbEntryToBirdImage converts a database cache entry to a BirdImage struct.
func dbEntryToBirdImage(entry *datastore.ImageCache) BirdImage {
	return BirdImage{
		URL:            entry.URL,
		ScientificName: entry.ScientificName,
		LicenseName:    entry.LicenseName,
		LicenseURL:     entry.LicenseURL,
		AuthorName:     entry.AuthorName,
		AuthorURL:      entry.AuthorURL,
		CachedAt:       entry.CachedAt,
		SourceProvider: entry.ProviderName,
	}
}

// waitWithQuit waits for the specified duration, returning true if quit was signaled.
func (c *BirdImageCache) waitWithQuit(d time.Duration) bool {
	timer := time.NewTimer(d)
	select {
	case <-c.quit:
		timer.Stop()
		return true
	case <-timer.C:
		return false
	}
}

// shouldSkipRefresh checks if the provider wants to skip cache refresh operations.
func (c *BirdImageCache) shouldSkipRefresh() bool {
	provider, err := c.getProvider()
	if err != nil {
		return false // No provider, but don't skip - let caller handle nil store
	}
	if statusChecker, ok := provider.(ProviderStatusChecker); ok {
		return !statusChecker.ShouldRefreshCache()
	}
	return false
}

// findStaleEntries returns scientific names of entries that have exceeded their TTL.
func (c *BirdImageCache) findStaleEntries(entries []datastore.ImageCache) []string {
	log := GetLogger().With(logger.String("provider", c.providerName))
	var staleEntries []string

	for i := range entries {
		isNegative := entries[i].URL == negativeEntryMarker
		if isCacheEntryStale(entries[i].CachedAt, isNegative) {
			if isNegative {
				log.Debug("Found stale negative entry",
					logger.String("scientific_name", entries[i].ScientificName),
					logger.Time("cached_at", entries[i].CachedAt))
			}
			staleEntries = append(staleEntries, entries[i].ScientificName)
		}
	}
	return staleEntries
}

// --- End Shared Helper Functions ---

// startCacheRefresh starts the background cache refresh routine
func (c *BirdImageCache) startCacheRefresh(quit chan struct{}) {
	log := GetLogger().With(logger.String("provider", c.providerName))
	log.Info("Starting cache refresh routine",
		logger.Duration("ttl", defaultCacheTTL),
		logger.Duration("interval", refreshInterval))

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		// Run an immediate refresh when starting
		log.Info("Running initial cache refresh check")
		c.refreshStaleEntries()

		for {
			select {
			case <-quit:
				log.Info("Stopping cache refresh routine")
				return
			case <-ticker.C:
				log.Debug("Ticker interval elapsed, checking for stale entries")
				c.refreshStaleEntries()
			}
		}
	}()
}

// refreshStaleEntries refreshes cache entries that are older than TTL
func (c *BirdImageCache) refreshStaleEntries() {
	log := GetLogger().With(logger.String("provider", c.providerName))

	if c.store == nil {
		log.Debug("DB store is nil, skipping cache refresh")
		return
	}

	if c.shouldSkipRefresh() {
		log.Debug("Provider configured to skip cache refresh operations")
		return
	}

	entries, err := c.store.GetAllImageCaches(c.providerName)
	if err != nil {
		c.logRefreshError(err)
		return
	}

	log.Debug("Checking entries for staleness",
		logger.Int("entry_count", len(entries)),
		logger.Duration("ttl", defaultCacheTTL))
	staleEntries := c.findStaleEntries(entries)

	if len(staleEntries) == 0 {
		log.Debug("No stale entries found")
		return
	}

	log.Info("Found stale cache entries to refresh",
		logger.Int("count", len(staleEntries)))
	c.processStaleEntriesInBatches(staleEntries)
	log.Info("Finished processing stale entries")
}

// logRefreshError logs an error that occurred during cache refresh.
func (c *BirdImageCache) logRefreshError(err error) {
	log := GetLogger().With(logger.String("provider", c.providerName))
	enhancedErr := errors.New(err).
		Component("imageprovider").
		Category(errors.CategoryImageCache).
		Context("provider", c.providerName).
		Context("operation", "get_cached_entries_for_refresh").
		Build()
	log.Error("Failed to get cached entries for refresh",
		logger.Error(enhancedErr))
	if c.metrics != nil {
		c.metrics.IncrementDownloadErrorsWithCategory("image-cache", c.providerName, "get_cached_entries_for_refresh")
	}
}

// processStaleEntriesInBatches processes stale entries in batches with rate limiting.
func (c *BirdImageCache) processStaleEntriesInBatches(staleEntries []string) {
	log := GetLogger().With(logger.String("provider", c.providerName))
	log.Debug("Processing stale entries",
		logger.Int("batch_size", refreshBatchSize),
		logger.Duration("delay_between_entries", refreshDelay))

	for i := 0; i < len(staleEntries); i += refreshBatchSize {
		end := min(i+refreshBatchSize, len(staleEntries))
		batch := staleEntries[i:end]

		log.Debug("Processing batch of stale entries",
			logger.Int("batch_start_index", i),
			logger.Int("batch_end_index", end),
			logger.Int("batch_size", len(batch)))

		for _, scientificName := range batch {
			if c.shouldQuit() {
				log.Info("Cache refresh routine quit signal received")
				return
			}
			if c.waitWithQuit(refreshDelay) {
				log.Info("Cache refresh routine quit signal received during wait")
				return
			}
			c.refreshEntry(scientificName)
		}
	}
}

// refreshEntry refreshes a single cache entry
func (c *BirdImageCache) refreshEntry(scientificName string) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Info("Refreshing cache entry")

	// Check if provider is set
	providerPtr := c.provider.Load()
	if providerPtr == nil {
		log.Warn("Cannot refresh entry: provider is nil")
		return
	}
	provider := *providerPtr

	// Fetch new image with background context to use more restrictive rate limiting
	log.Debug("Fetching new image data from provider (background refresh)")

	// Check if provider supports context-aware fetching
	var birdImage BirdImage
	var err error

	if ctxProvider, ok := provider.(interface {
		FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error)
	}); ok {
		// Use background context for refresh operations
		ctx := context.WithValue(context.Background(), backgroundOperationKey, true)
		birdImage, err = ctxProvider.FetchWithContext(ctx, scientificName)
	} else {
		// Fallback to regular fetch
		birdImage, err = provider.Fetch(scientificName)
	}

	if err != nil {
		// Check if it's already an enhanced error, if not enhance it
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.New(err).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", c.providerName).
				Context("scientific_name", scientificName).
				Context("operation", "cache_refresh_fetch").
				Build()
		}

		// Use appropriate log levels based on error type:
		// No logging: Provider not configured (normal operational state)
		// WARN: "Not found" errors
		// ERROR: Actual system failures
		switch {
		case errors.Is(err, ErrImageNotFound):
			log.Warn("Failed to fetch image during refresh",
				logger.Error(enhancedErr))
		case errors.Is(err, ErrProviderNotConfigured):
			// This is normal - provider correctly identified it's not configured for use
			// No logging needed as this is expected operational behavior
		default:
			log.Error("Failed to fetch image during refresh",
				logger.Error(enhancedErr))
		}

		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-fetch", c.providerName, "cache_refresh_fetch")
		}
		return
	}

	// Update memory cache
	log.Debug("Updating memory cache with refreshed image")
	c.dataMap.Store(scientificName, &birdImage)

	// Update database cache
	log.Debug("Updating database cache with refreshed image")
	c.saveToDB(&birdImage)

	if c.metrics != nil {
		c.metrics.IncrementImageDownloads()
	}
	log.Info("Successfully refreshed cache entry")
}

// Close stops the cache refresh routine and performs cleanup
func (c *BirdImageCache) Close() error {
	GetLogger().Info("Closing image provider cache",
		logger.String("provider", c.providerName))
	if c.quit != nil {
		select {
		case <-c.quit:
			// Already closed
			GetLogger().Debug("Quit channel already closed")
		default:
			GetLogger().Debug("Closing quit channel")
			close(c.quit)
		}
	}
	return nil
}

// initCache initializes a new BirdImageCache with the given ImageProvider.
func InitCache(providerName string, e ImageProvider, t *observability.Metrics, store datastore.Interface) *BirdImageCache {
	log := GetLogger().With(logger.String("provider", providerName))
	log.Info("Initializing image cache")
	settings := conf.Setting()

	quit := make(chan struct{})

	var imageProviderMetrics *metrics.ImageProviderMetrics
	if t != nil {
		imageProviderMetrics = t.ImageProvider
	}

	cache := &BirdImageCache{
		providerName: providerName, // Set provider name
		metrics:      imageProviderMetrics,
		debug:        settings.Realtime.Dashboard.Thumbnails.Debug, // Keep for potential checks
		store:        store,
		quit:         quit,
	}

	// Store the provider using atomic pointer
	cache.provider.Store(&e)

	// Load cached images into memory only if store is available
	if store != nil {
		log.Info("DB store available, loading cached images")
		if err := cache.loadCachedImages(); err != nil {
			log.Error("Error loading cached images",
				logger.Error(err))
		}
	} else {
		log.Info("DB store not available, skipping loading cached images")
	}

	// Start cache refresh routine
	cache.startCacheRefresh(quit)

	log.Info("Image cache initialization complete")
	return cache
}

// loadFromDBCache loads a BirdImage from the database cache
func (c *BirdImageCache) loadFromDBCache(scientificName string) (*BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Attempting to load image from DB cache")
	// Check if store is nil to prevent nil pointer dereference
	if c.store == nil {
		log.Warn("Cannot load from DB cache: DB store is nil")
		return nil, ErrCacheMiss
	}

	var cachedImage *datastore.ImageCache // Correct type based on GetImageCache return
	var err error
	query := datastore.ImageCacheQuery{ // Pass query by value
		ScientificName: scientificName,
		ProviderName:   c.providerName, // Query based on *this* cache's provider name
	}
	log.Debug("Querying DB for cached image")
	cachedImage, err = c.store.GetImageCache(query) // Use GetImageCache and handle two return values
	if err != nil {
		// Check if it's a record not found error (which is expected for cache misses)
		if errors.Is(err, datastore.ErrImageCacheNotFound) {
			log.Debug("Image not found in DB cache (GetImageCache returned ErrImageCacheNotFound)")
			return nil, ErrCacheMiss
		}
		// Log database errors for other errors
		log.Error("Failed to get image from DB cache",
			logger.Error(err))
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "query_image_cache").
			Build()
		return nil, enhancedErr
	}

	log.Debug("Image found in DB cache",
		logger.Time("cached_at", cachedImage.CachedAt))
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

// batchLoadFromDB loads multiple BirdImages from the database cache in a single query
func (c *BirdImageCache) batchLoadFromDB(scientificNames []string) (map[string]BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.Int("batch_size", len(scientificNames)))
	log.Debug("Attempting batch load from DB cache")

	if c.store == nil {
		log.Warn("Cannot batch load from DB cache: DB store is nil")
		return nil, ErrCacheMiss
	}

	dbImages, err := c.fetchFromDBWithFallback(scientificNames)
	if err != nil {
		return nil, err
	}

	result := c.convertDBImagesToValidBirdImages(dbImages)
	log.Debug("Batch DB load completed",
		logger.Int("found", len(result)),
		logger.Int("requested", len(scientificNames)))
	return result, nil
}

// fetchFromDBWithFallback fetches images from DB, trying fallback providers if needed.
func (c *BirdImageCache) fetchFromDBWithFallback(scientificNames []string) (map[string]*datastore.ImageCache, error) {
	log := GetLogger().With(logger.String("provider", c.providerName))
	settings := conf.Setting()
	debug := settings.Realtime.Dashboard.Thumbnails.Debug

	if debug {
		log.Debug("Calling GetImageCacheBatch",
			logger.String("provider_name", c.providerName),
			logger.Int("species_count", len(scientificNames)))
	}

	dbImages, err := c.store.GetImageCacheBatch(c.providerName, scientificNames)
	if err != nil {
		log.Error("Failed to batch load from DB cache",
			logger.Error(err))
		return nil, err
	}

	if debug {
		log.Debug("GetImageCacheBatch completed",
			logger.String("provider_name", c.providerName),
			logger.Int("found_count", len(dbImages)))
	}

	// Try fallback providers if no images found
	if len(dbImages) == 0 && len(scientificNames) > 0 {
		dbImages = c.tryBatchFallbackProviders(scientificNames, debug)
	}

	return dbImages, nil
}

// tryBatchFallbackProviders attempts to load images from fallback providers for batch operations.
func (c *BirdImageCache) tryBatchFallbackProviders(scientificNames []string, debug bool) map[string]*datastore.ImageCache {
	log := GetLogger().With(logger.String("provider", c.providerName))
	settings := conf.Setting()

	if settings.Realtime.Dashboard.Thumbnails.FallbackPolicy != fallbackPolicyAll {
		if debug {
			log.Debug("No images found with primary provider, but fallback policy is 'none'")
		}
		return nil
	}

	if debug {
		log.Debug("No images found with primary provider, trying fallback providers (policy: all)")
	}

	for _, fallbackProvider := range fallbackProviders {
		if fallbackProvider == c.providerName {
			continue
		}
		fallbackImages, err := c.store.GetImageCacheBatch(fallbackProvider, scientificNames)
		if err == nil && len(fallbackImages) > 0 {
			if debug {
				log.Info("Found images using fallback provider",
					logger.String("fallback_provider", fallbackProvider),
					logger.Int("found_count", len(fallbackImages)))
			}
			return fallbackImages
		}
	}
	return nil
}

// convertDBImagesToValidBirdImages converts DB entries to BirdImages, filtering out stale entries.
func (c *BirdImageCache) convertDBImagesToValidBirdImages(dbImages map[string]*datastore.ImageCache) map[string]BirdImage {
	log := GetLogger().With(logger.String("provider", c.providerName))
	result := make(map[string]BirdImage)
	now := time.Now()

	for name, dbImage := range dbImages {
		if dbImage == nil {
			continue
		}

		birdImage := dbEntryToBirdImage(dbImage)
		cutoff := now.Add(-birdImage.GetTTL())

		if dbImage.CachedAt.After(cutoff) {
			result[name] = birdImage
			if birdImage.IsNegativeEntry() {
				log.Debug("Loaded negative cache entry from DB batch",
					logger.String("scientific_name", name),
					logger.Time("cached_at", dbImage.CachedAt))
			}
		} else {
			log.Debug("Skipping stale DB entry from batch",
				logger.String("scientific_name", name),
				logger.Time("cached_at", dbImage.CachedAt),
				logger.Bool("is_negative", birdImage.IsNegativeEntry()))
		}
	}
	return result
}

// saveToDB saves a BirdImage to the database cache
func (c *BirdImageCache) saveToDB(image *BirdImage) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", image.ScientificName))
	// Check if store is nil
	if c.store == nil {
		log.Warn("Cannot save to DB cache: DB store is nil")
		return
	}

	// Check if image URL is empty - don't save empty entries
	if image.URL == "" {
		log.Debug("Skipping save to DB: image URL is empty")
		return
	}

	// For negative cache entries, we'll save them to DB with the special marker
	// This allows them to be loaded on restart (though they'll likely be expired)
	if image.IsNegativeEntry() {
		log.Debug("Saving negative cache entry to DB")
	}

	log.Debug("Saving image to DB cache",
		logger.String("url", image.URL),
		logger.String("source_provider", image.SourceProvider))

	// Ensure provider name is not empty, falling back to the cache's own name if needed
	providerNameToSave := image.SourceProvider
	if providerNameToSave == "" {
		log.Warn("SourceProvider field was empty in BirdImage, falling back to cache provider name for DB save",
			logger.String("fallback_provider", c.providerName))
		providerNameToSave = c.providerName
	}

	dbEntry := &datastore.ImageCache{
		ScientificName: image.ScientificName,
		ProviderName:   providerNameToSave,
		URL:            image.URL,
		LicenseName:    image.LicenseName,
		LicenseURL:     image.LicenseURL,
		AuthorName:     image.AuthorName,
		AuthorURL:      image.AuthorURL,
		CachedAt:       time.Now(), // Update cached timestamp
	}

	if err := c.store.SaveImageCache(dbEntry); err != nil {
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("scientific_name", image.ScientificName).
			Context("operation", "save_image_cache").
			Build()
		log.Error("Failed to save image to DB cache",
			logger.Error(enhancedErr))
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-cache", c.providerName, "save_image_cache")
		}
	}
}

// loadCachedImages loads all relevant cached images from the DB into memory
func (c *BirdImageCache) loadCachedImages() error {
	log := GetLogger().With(logger.String("provider", c.providerName))
	log.Info("Loading all cached images from DB into memory")
	if c.store == nil {
		log.Warn("Cannot load cached images: DB store is nil")
		enhancedErr := errors.Newf("datastore is nil").
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("operation", "load_cached_images").
			Build()
		return enhancedErr
	}

	entries, err := c.store.GetAllImageCaches(c.providerName) // Get entries specific to this provider
	if err != nil {
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("operation", "get_all_image_caches").
			Build()
		log.Error("Failed to get all image caches from DB",
			logger.Error(enhancedErr))
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-cache", c.providerName, "get_all_image_caches")
		}
		return enhancedErr
	}

	loadedCount := 0
	now := time.Now()

	for i := range entries {
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

		// Check if entry is still valid based on its TTL
		cutoff := now.Add(-birdImage.GetTTL())

		// Only load non-stale entries into memory
		if entries[i].CachedAt.After(cutoff) {
			c.dataMap.Store(birdImage.ScientificName, &birdImage)
			loadedCount++
			if birdImage.IsNegativeEntry() {
				log.Debug("Loaded negative cache entry from DB",
					logger.String("scientific_name", entries[i].ScientificName),
					logger.Time("cached_at", entries[i].CachedAt))
			}
		} else {
			log.Debug("Skipping load of stale DB entry into memory cache",
				logger.String("scientific_name", entries[i].ScientificName),
				logger.Time("cached_at", entries[i].CachedAt),
				logger.Bool("is_negative", birdImage.IsNegativeEntry()))
		}
	}

	log.Info("Finished loading cached images into memory",
		logger.Int("loaded_count", loadedCount),
		logger.Int("total_db_entries_checked", len(entries)))
	return nil
}

// checkCachedEntryAfterLock checks if the image is already in memory cache after acquiring the lock.
// Returns (image, foundInCache, shouldReturnError, error).
func (c *BirdImageCache) checkCachedEntryAfterLock(scientificName string, log logger.Logger) (img BirdImage, foundInCache, shouldReturnError bool, err error) {
	val, ok := c.dataMap.Load(scientificName)
	if !ok {
		return BirdImage{}, false, false, nil
	}

	imgPtr, ok := val.(*BirdImage)
	if !ok || imgPtr == nil || imgPtr.URL == "" {
		return BirdImage{}, false, false, nil
	}

	if !imgPtr.IsNegativeEntry() {
		log.Debug("Initialization check: found in memory cache after acquiring lock")
		return *imgPtr, true, false, nil
	}

	// Handle negative entry
	cutoff := time.Now().Add(-imgPtr.GetTTL())
	if imgPtr.CachedAt.Before(cutoff) {
		log.Debug("Negative cache entry expired, removing from memory")
		c.dataMap.Delete(scientificName)
		return BirdImage{}, false, false, nil
	}

	log.Debug("Returning valid negative cache entry after lock")
	return BirdImage{}, true, true, ErrImageNotFound
}

// tryInitialize ensures only one goroutine initializes a species image using mutexes.
// It returns the image, a boolean indicating if it was found in cache (true) or fetched (false), and an error.
func (c *BirdImageCache) tryInitialize(scientificName string) (BirdImage, bool, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	muInterface, _ := c.Initializing.LoadOrStore(scientificName, &sync.Mutex{})
	mu := muInterface.(*sync.Mutex)
	mu.Lock()
	defer func() {
		mu.Unlock()
		c.Initializing.Delete(scientificName)
		log.Debug("Unlocked and cleaned up mutex")
	}()

	log.Debug("Acquired initialization lock")

	img, foundInCache, shouldReturn, err := c.checkCachedEntryAfterLock(scientificName, log)
	if foundInCache || shouldReturn {
		return img, foundInCache, err
	}

	log.Debug("Not in cache after lock, proceeding to fetch/store")
	img, err = c.fetchAndStore(scientificName)
	return img, false, err
}

// logInitializeError logs the initialization error if it's not ErrImageNotFound.
func (c *BirdImageCache) logInitializeError(err error, scientificName string, log logger.Logger) {
	if errors.Is(err, ErrImageNotFound) {
		return
	}

	var enhancedErr *errors.EnhancedError
	if !errors.As(err, &enhancedErr) {
		enhancedErr = errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageProvider).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "try_initialize").
			Build()
	}
	log.Error("Failed to initialize or fetch image (tryInitialize returned error)",
		logger.Error(enhancedErr))
}

// tryFallbackOnGetError attempts to get the image from fallback providers on error.
// Returns (image, found).
func (c *BirdImageCache) tryFallbackOnGetError(err error, scientificName string, log logger.Logger) (BirdImage, bool) {
	settings := conf.Setting()
	if settings.Realtime.Dashboard.Thumbnails.FallbackPolicy != fallbackPolicyAll {
		log.Debug("Primary provider failed but fallback policy is 'none'",
			logger.Error(err))
		return BirdImage{}, false
	}

	registry := c.GetRegistry()
	if registry == nil {
		return BirdImage{}, false
	}

	triedProviders := map[string]bool{c.providerName: true}
	log.Info("Primary provider failed, attempting fallback (policy: all)",
		logger.Error(err))
	fallbackImg, found := c.tryFallbackProviders(scientificName, triedProviders)
	if found {
		log.Info("Image found via fallback provider",
			logger.String("fallback_provider", fallbackImg.SourceProvider))
		return fallbackImg, true
	}
	log.Warn("Image not found via fallback providers either")
	return BirdImage{}, false
}

// Get retrieves a bird image from the cache, fetching if necessary.
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Get image request received")

	img, foundInCache, err := c.tryInitialize(scientificName)
	if err != nil {
		c.logInitializeError(err, scientificName, log)
		if fallbackImg, found := c.tryFallbackOnGetError(err, scientificName, log); found {
			return fallbackImg, nil
		}
		return BirdImage{}, err
	}

	if foundInCache {
		log.Debug("Image found in cache, returning cached result")
		if c.metrics != nil {
			c.metrics.IncrementCacheHits()
		}
		return img, nil
	}

	log.Debug("Image initialized by this goroutine (cache miss), returning fetched/loaded result")
	return img, nil
}

// fetchAndStore tries to load from DB, then fetches from the provider if necessary, and stores the result.
func (c *BirdImageCache) fetchAndStore(scientificName string) (BirdImage, error) {
	fetchStart := time.Now()
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Fetching and storing image (memory cache miss)")

	// 1. Try loading from DB cache first
	dbStart := time.Now()
	dbImage, dbErr := c.loadFromDBCache(scientificName)
	dbDuration := time.Since(dbStart)

	c.logSlowOperation("DB cache lookup", scientificName, dbDuration, dbCacheLookupSlowThreshold)

	if isRealError(dbErr) {
		log.Warn("Error loading from DB cache, proceeding to fetch from provider",
			logger.Error(dbErr))
	}

	if dbImage != nil {
		if result, done := c.handleDBCacheHit(scientificName, dbImage); done {
			err := c.getDBCacheError(&result)
			return result, err
		}
	}

	// 2. Not in DB or DB load failed, fetch from the actual provider
	return c.fetchSingleFromProvider(scientificName, fetchStart)
}

// getDBCacheError returns the appropriate error for a DB cache result.
func (c *BirdImageCache) getDBCacheError(result *BirdImage) error {
	if result.URL == "" || result.IsNegativeEntry() {
		return ErrImageNotFound
	}
	return nil
}

// handleDBCacheHit processes a DB cache hit and returns whether to continue or return.
// Returns (result, true) if we should return, (_, false) if we should continue to provider fetch.
func (c *BirdImageCache) handleDBCacheHit(scientificName string, dbImage *BirdImage) (BirdImage, bool) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	if dbImage.IsNegativeEntry() {
		return c.handleNegativeDBEntry(scientificName, dbImage)
	}

	// Regular positive entry - check staleness
	if isCacheEntryStale(dbImage.CachedAt, false) {
		// Check if shutdown was signaled before spawning new goroutine
		if c.shouldQuit() {
			log.Debug("Skipping background refresh - shutdown in progress")
		} else {
			log.Info("DB cache entry is stale, returning stale data and triggering background refresh",
				logger.Time("cached_at", dbImage.CachedAt))
			go c.refreshEntry(scientificName)
		}
	} else {
		log.Info("Image loaded from DB cache")
	}

	c.dataMap.Store(scientificName, dbImage)
	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
	}
	return *dbImage, true
}

// handleNegativeDBEntry handles a negative cache entry from the DB.
func (c *BirdImageCache) handleNegativeDBEntry(scientificName string, dbImage *BirdImage) (BirdImage, bool) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	if isCacheEntryStale(dbImage.CachedAt, true) {
		log.Debug("Negative cache entry from DB is expired, will re-fetch")
		return BirdImage{}, false // Continue to provider fetch
	}

	log.Info("Valid negative cache entry loaded from DB")
	c.dataMap.Store(scientificName, dbImage)
	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
	}
	return BirdImage{}, true // Return with ErrImageNotFound
}

// fetchSingleFromProvider fetches an image from the provider when not found in cache.
func (c *BirdImageCache) fetchSingleFromProvider(scientificName string, fetchStart time.Time) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Info("Image not found in DB cache, fetching from provider")

	provider, err := c.getProvider()
	if err != nil {
		return c.handleProviderNilError(scientificName)
	}

	providerStart := time.Now()
	fetchedImage, fetchErr := provider.Fetch(scientificName)
	providerDuration := time.Since(providerStart)

	c.logSlowOperation("Provider fetch", scientificName, providerDuration, providerFetchSlowThreshold)

	if fetchErr != nil {
		return c.handleProviderFetchError(scientificName, fetchErr)
	}

	if fetchedImage.URL == "" {
		log.Warn("Provider returned success but with an empty image URL")
		return BirdImage{}, ErrImageNotFound
	}

	result := c.storeSuccessfulFetch(scientificName, &fetchedImage)

	totalDuration := time.Since(fetchStart)
	c.logSlowOperation("Total fetch", scientificName, totalDuration, totalFetchSlowThreshold)

	return result, nil
}

// handleProviderNilError creates an error for when the provider is nil.
func (c *BirdImageCache) handleProviderNilError(scientificName string) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	enhancedErr := errors.Newf("image provider for %s is not configured", c.providerName).
		Component("imageprovider").
		Category(errors.CategoryImageProvider).
		Context("provider", c.providerName).
		Context("scientific_name", scientificName).
		Context("operation", "fetch_and_store").
		Build()
	log.Error("Cannot fetch image: provider is nil", logger.Error(enhancedErr))
	return BirdImage{}, enhancedErr
}

// handleProviderFetchError handles errors from provider fetch operations.
func (c *BirdImageCache) handleProviderFetchError(scientificName string, fetchErr error) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	enhancedErr := c.enhanceFetchError(fetchErr, scientificName)
	log.Error("Failed to fetch image from provider", logger.Error(enhancedErr))

	if c.metrics != nil {
		c.metrics.IncrementDownloadErrorsWithCategory("image-fetch", c.providerName, "provider_fetch")
	}

	if errors.Is(fetchErr, ErrImageNotFound) {
		return c.storeNegativeCacheEntry(scientificName, fetchErr)
	}

	log.Warn("Provider error (not caching)", logger.Error(enhancedErr))
	return BirdImage{}, enhancedErr
}

// enhanceFetchError wraps a fetch error with context if needed.
func (c *BirdImageCache) enhanceFetchError(fetchErr error, scientificName string) *errors.EnhancedError {
	var enhancedErr *errors.EnhancedError
	if !errors.As(fetchErr, &enhancedErr) {
		enhancedErr = errors.New(fetchErr).
			Component("imageprovider").
			Category(errors.CategoryImageFetch).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "provider_fetch").
			Build()
	}
	return enhancedErr
}

// storeNegativeCacheEntry stores a negative cache entry for a not-found image.
func (c *BirdImageCache) storeNegativeCacheEntry(scientificName string, fetchErr error) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Info("Image not found by provider, storing negative cache entry")

	negativeEntry := BirdImage{
		URL:            negativeEntryMarker,
		ScientificName: scientificName,
		CachedAt:       time.Now(),
		SourceProvider: c.providerName,
	}

	c.dataMap.Store(scientificName, &negativeEntry)
	c.saveToDB(&negativeEntry)

	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
	}

	return BirdImage{}, fetchErr
}

// storeSuccessfulFetch stores a successfully fetched image in both caches.
func (c *BirdImageCache) storeSuccessfulFetch(scientificName string, fetchedImage *BirdImage) BirdImage {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	fetchedImage.CachedAt = time.Now()
	fetchedImage.SourceProvider = c.providerName
	log.Info("Image successfully fetched from provider", logger.String("url", fetchedImage.URL))

	c.dataMap.Store(scientificName, fetchedImage)
	c.saveToDB(fetchedImage)

	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
		c.metrics.IncrementImageDownloads()
	}

	return *fetchedImage
}

// logSlowOperation logs if an operation exceeds the threshold.
func (c *BirdImageCache) logSlowOperation(operation, scientificName string, duration, threshold time.Duration) {
	if c.debug && duration > threshold {
		GetLogger().Warn("Slow operation detected",
			logger.String("operation", operation),
			logger.String("scientific_name", scientificName),
			logger.Duration("duration", duration),
			logger.Duration("threshold", threshold),
			logger.String("provider", c.providerName))
	}
}

// tryFallbackProviders attempts to get the image from other registered providers.
func (c *BirdImageCache) tryFallbackProviders(scientificName string, triedProviders map[string]bool) (BirdImage, bool) {
	log := GetLogger().With(logger.String("scientific_name", scientificName))
	log.Info("Trying fallback providers")
	registry := c.GetRegistry()
	if registry == nil {
		log.Warn("Cannot try fallback providers: registry is not set")
		return BirdImage{}, false
	}

	var foundImage BirdImage
	found := false

	// Create a local copy of triedProviders to avoid modifying the caller's map
	localTriedProviders := make(map[string]bool, len(triedProviders))
	for provider := range triedProviders {
		localTriedProviders[provider] = true
	}

	registry.RangeProviders(func(name string, cache *BirdImageCache) bool {
		if localTriedProviders[name] {
			log.Debug("Skipping already tried provider", logger.String("provider", name))
			return true // Continue ranging
		}

		log.Info("Attempting fallback fetch from provider", logger.String("provider", name))
		localTriedProviders[name] = true // Mark as tried

		// Instead of calling Get (which would recursively try fallbacks), use fetchAndStore directly
		// to avoid the fallback chain and potential infinite loop
		img, err := cache.fetchAndStore(scientificName)
		if err != nil {
			// Log error but continue trying other fallbacks
			log.Warn("Fallback provider failed to get image",
				logger.String("provider", name),
				logger.Error(err))
			return true // Continue ranging
		}

		// Check if a valid image was found (URL is not empty)
		if img.URL != "" {
			log.Info("Image found via fallback provider",
				logger.String("provider", name),
				logger.String("url", img.URL))
			foundImage = img
			found = true
			return false // Stop ranging, we found one
		} else {
			log.Debug("Fallback provider returned empty image", logger.String("provider", name))
			// Continue ranging if this provider returned an empty image
			return true
		}
	})

	if found {
		log.Info("Fallback successful", logger.String("found_provider", foundImage.SourceProvider))
	} else {
		log.Info("Fallback unsuccessful, image not found in any provider")
	}
	return foundImage, found
}

// fetchDirect performs a direct fetch from the provider without cache interaction.
func (c *BirdImageCache) fetchDirect(scientificName string) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Performing direct fetch from provider (bypassing cache checks)")

	providerPtr := c.provider.Load()
	if providerPtr == nil {
		enhancedErr := errors.Newf("image provider %s is not configured", c.providerName).
			Component("imageprovider").
			Category(errors.CategoryImageProvider).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "fetch_direct").
			Build()
		log.Error("Cannot perform direct fetch: provider is nil", logger.Error(enhancedErr))
		return BirdImage{}, enhancedErr
	}
	provider := *providerPtr

	img, err := provider.Fetch(scientificName)
	if err != nil {
		// Check if it's already an enhanced error, if not enhance it
		var enhancedErr *errors.EnhancedError
		if !errors.As(err, &enhancedErr) {
			enhancedErr = errors.New(err).
				Component("imageprovider").
				Category(errors.CategoryImageFetch).
				Context("provider", c.providerName).
				Context("scientific_name", scientificName).
				Context("operation", "direct_fetch").
				Build()
		}
		log.Error("Direct fetch failed", logger.Error(enhancedErr))
		return BirdImage{}, enhancedErr
	}

	img.CachedAt = time.Now() // Set time even though it's not 'cached'
	img.SourceProvider = c.providerName
	log.Debug("Direct fetch successful", logger.String("url", img.URL))
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
	c.dataMap.Range(func(key, value any) bool {
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
	GetLogger().Debug("Updated cache metrics",
		logger.String("provider", c.providerName),
		logger.Float64("size_bytes", sizeBytes))
	// c.metrics.SetMemoryCacheEntries(float64(count)) // Method doesn't exist
	// c.metrics.SetMemoryCacheSizeBytes(float64(c.MemoryUsage())) // Method doesn't exist
}

// CreateDefaultCache creates the default BirdImageCache (currently Wikimedia Commons via Wikipedia API).
func CreateDefaultCache(metricsCollector *observability.Metrics, store datastore.Interface) (*BirdImageCache, error) {
	// Use the lazy-initialized provider to avoid race conditions during startup
	// where conf.Setting() might not be fully initialized yet
	provider := NewLazyWikiMediaProvider()

	// Using "wikimedia" as the provider name aligns with the constructor used
	// The LazyWikiMediaProvider will handle actual provider creation when first used
	return InitCache("wikimedia", provider, metricsCollector, store), nil
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
		enhancedErr := errors.Newf("provider name cannot be empty").
			Component("imageprovider").
			Category(errors.CategoryValidation).
			Context("operation", "register_provider").
			Build()
		return enhancedErr
	}
	if cache == nil {
		enhancedErr := errors.Newf("cannot register nil cache for provider '%s'", name).
			Component("imageprovider").
			Category(errors.CategoryValidation).
			Context("provider", name).
			Context("operation", "register_provider").
			Build()
		return enhancedErr
	}

	r.mu.Lock()
	defer r.mu.Unlock()
	if _, exists := r.caches[name]; exists {
		enhancedErr := errors.Newf("image provider cache named '%s' already registered", name).
			Component("imageprovider").
			Category(errors.CategoryValidation).
			Context("provider", name).
			Context("operation", "register_provider").
			Build()
		return enhancedErr
	}
	r.caches[name] = cache
	return nil
}

// GetOrRegister atomically retrieves or registers a cache.
// This eliminates the check-then-act race condition between GetCache and Register.
// The factory function is only called if the cache doesn't exist.
// Returns an error if name is empty or factory is nil.
func (r *ImageProviderRegistry) GetOrRegister(name string, factory func() (*BirdImageCache, error)) (*BirdImageCache, error) {
	// Validate inputs before acquiring lock
	if name == "" {
		return nil, fmt.Errorf("provider name cannot be empty")
	}
	if factory == nil {
		return nil, fmt.Errorf("factory function cannot be nil")
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if existing, ok := r.caches[name]; ok {
		return existing, nil
	}

	// Factory might fail (e.g., database error during CreateDefaultCache)
	cache, err := factory()
	if err != nil {
		return nil, err
	}

	r.caches[name] = cache
	return cache, nil
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
		enhancedErr := errors.Newf("provider name cannot be empty").
			Component("imageprovider").
			Category(errors.CategoryValidation).
			Context("operation", "get_image").
			Build()
		return BirdImage{}, enhancedErr
	}
	if scientificName == "" {
		enhancedErr := errors.Newf("scientific name cannot be empty").
			Component("imageprovider").
			Category(errors.CategoryValidation).
			Context("provider", providerName).
			Context("operation", "get_image").
			Build()
		return BirdImage{}, enhancedErr
	}

	cache, ok := r.GetCache(providerName)
	if !ok {
		enhancedErr := errors.Newf("no image provider cache registered for name '%s'", providerName).
			Component("imageprovider").
			Category(errors.CategoryImageProvider).
			Context("provider", providerName).
			Context("scientific_name", scientificName).
			Context("operation", "get_image").
			Build()
		return BirdImage{}, enhancedErr
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
			enhancedErr := errors.New(err).
				Component("imageprovider").
				Category(errors.CategorySystem).
				Context("operation", "close_cache").
				Context("cache_name", name).
				Build()
			errs = append(errs, enhancedErr)
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
	maps.Copy(snapshot, r.caches)
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
	maps.Copy(cachesCopy, r.caches)
	return cachesCopy
}

// batchFetchResult holds the result of fetching a single image
type batchFetchResult struct {
	name  string
	image BirdImage
	err   error
}

// GetBatch fetches multiple bird images at once and returns them as a map
// This is more efficient than multiple individual Get calls when many images are needed
func (c *BirdImageCache) GetBatch(scientificNames []string) map[string]BirdImage {
	batchStart := time.Now()
	result := make(map[string]BirdImage, len(scientificNames))

	// Phase 1: Check memory cache
	missingNames := c.checkMemoryCache(scientificNames, result)

	// Early return if all found in memory
	if len(missingNames) == 0 {
		c.logBatchComplete(batchStart, len(result), len(scientificNames))
		return result
	}

	// Phase 2: Check database cache
	if c.store != nil && len(missingNames) > 0 {
		missingNames = c.checkDatabaseCache(missingNames, result)
	}

	// Phase 3: Fetch from provider
	if len(missingNames) > 0 {
		c.fetchFromProvider(missingNames, result)
	}

	c.logBatchComplete(batchStart, len(result), len(scientificNames))
	return result
}

// GetBatchCachedOnly retrieves multiple bird images from cache only (memory + database)
// without triggering any provider fetches. This is useful for fast initial page loads.
// Missing images will simply not be included in the result map.
func (c *BirdImageCache) GetBatchCachedOnly(scientificNames []string) map[string]BirdImage {
	batchStart := time.Now()
	result := make(map[string]BirdImage, len(scientificNames))

	// Phase 1: Check memory cache
	missingNames := c.checkMemoryCache(scientificNames, result)

	// Phase 2: Check database cache (if there are missing names)
	if c.store != nil && len(missingNames) > 0 {
		_ = c.checkDatabaseCache(missingNames, result)
	}

	// Note: We do NOT fetch from provider - just return what we have cached
	if c.debug {
		GetLogger().Debug("GetBatchCachedOnly: Completed",
			logger.Duration("duration", time.Since(batchStart)),
			logger.Int("found", len(result)),
			logger.Int("total", len(scientificNames)))
	}

	return result
}

// checkMemoryCache checks memory cache for requested images and populates result map
// Returns list of names not found in memory cache
func (c *BirdImageCache) checkMemoryCache(scientificNames []string, result map[string]BirdImage) []string {
	memoryCacheStart := time.Now()
	missingNames := make([]string, 0, len(scientificNames))

	for _, name := range scientificNames {
		if name == "" {
			continue
		}

		// Check memory cache first
		if value, ok := c.dataMap.Load(name); ok {
			if image, ok := value.(*BirdImage); ok {
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

	if c.debug {
		GetLogger().Debug("GetBatch: Memory cache check completed",
			logger.Duration("duration", time.Since(memoryCacheStart)),
			logger.Int("found", len(result)),
			logger.Int("total", len(scientificNames)))
	}

	return missingNames
}

// checkDatabaseCache checks database cache for missing images and populates result map
// Returns list of names not found in database cache
func (c *BirdImageCache) checkDatabaseCache(missingNames []string, result map[string]BirdImage) []string {
	dbBatchStart := time.Now()
	if c.debug {
		GetLogger().Debug("GetBatch: Attempting batch DB cache lookup",
			logger.Int("item_count", len(missingNames)))
	}

	dbImages, err := c.batchLoadFromDB(missingNames)
	if isRealError(err) {
		if c.debug {
			GetLogger().Debug("GetBatch: Batch DB load error", logger.Error(err))
		}
		// On error, return original list to continue with provider fetch
		return missingNames
	}

	// Process DB results
	stillMissing := make([]string, 0, len(missingNames))
	dbHitCount := 0

	for _, name := range missingNames {
		if img, found := dbImages[name]; found {
			result[name] = img
			// Store in memory cache for future requests
			copyImg := img // Make a copy to store pointer to
			c.dataMap.Store(name, &copyImg)
			if c.metrics != nil {
				c.metrics.IncrementCacheMisses() // Memory miss but DB hit
			}
			dbHitCount++
		} else {
			stillMissing = append(stillMissing, name)
		}
	}

	if c.debug {
		hitRate := float64(dbHitCount) / float64(len(missingNames)) * percentMultiplier
		GetLogger().Debug("GetBatch: Batch DB lookup completed",
			logger.Duration("duration", time.Since(dbBatchStart)),
			logger.Int("db_hits", dbHitCount),
			logger.Float64("hit_rate_percent", hitRate),
			logger.Int("still_missing", len(stillMissing)))
	}

	return stillMissing
}

// fetchFromProvider fetches missing images from the provider in parallel
func (c *BirdImageCache) fetchFromProvider(missingNames []string, result map[string]BirdImage) {
	if c.debug {
		GetLogger().Debug("GetBatch: Need to fetch images from provider",
			logger.Int("count", len(missingNames)))
	}
	fetchStart := time.Now()

	// Use goroutines for parallel fetching with a worker pool
	const maxWorkers = 5 // Limit concurrent requests to avoid overwhelming the provider
	sem := make(chan struct{}, maxWorkers)
	resultChan := make(chan batchFetchResult, len(missingNames))

	// Launch goroutines for parallel fetching
	for _, name := range missingNames {
		go c.fetchSingleImage(name, sem, resultChan)
	}

	// Collect results
	c.collectFetchResults(len(missingNames), resultChan, result)
	close(resultChan)

	if c.debug {
		GetLogger().Debug("GetBatch: Provider fetch phase completed",
			logger.Duration("duration", time.Since(fetchStart)),
			logger.Int("max_workers", maxWorkers))
	}
}

// fetchSingleImage fetches a single image from the provider
func (c *BirdImageCache) fetchSingleImage(scientificName string, sem chan struct{}, resultChan chan<- batchFetchResult) {
	sem <- struct{}{}        // Acquire semaphore
	defer func() { <-sem }() // Release semaphore

	singleFetchStart := time.Now()
	image, err := c.fetchAndStore(scientificName)
	singleFetchDuration := time.Since(singleFetchStart)

	if c.debug && singleFetchDuration > providerFetchSlowThreshold {
		GetLogger().Warn("GetBatch: Slow provider fetch",
			logger.String("scientific_name", scientificName),
			logger.Duration("duration", singleFetchDuration))
	}

	resultChan <- batchFetchResult{
		name:  scientificName,
		image: image,
		err:   err,
	}
}

// collectFetchResults collects results from parallel fetches
func (c *BirdImageCache) collectFetchResults(count int, resultChan <-chan batchFetchResult, result map[string]BirdImage) {
	for i := range count {
		res := <-resultChan
		if res.err == nil {
			result[res.name] = res.image
		} else {
			if c.debug {
				GetLogger().Debug("GetBatch: Failed to fetch from provider",
					logger.String("scientific_name", res.name),
					logger.Error(res.err))
			}
			// Report critical errors to telemetry
			if !errors.Is(res.err, ErrImageNotFound) {
				enhancedErr := errors.New(res.err).
					Component("imageprovider").
					Category(errors.CategoryImageProvider).
					Context("provider", c.providerName).
					Context("scientific_name", res.name).
					Context("operation", "batch_fetch_single").
					Build()
				GetLogger().Error("Failed to fetch image in batch operation",
					logger.Error(enhancedErr),
					logger.String("scientific_name", res.name),
					logger.String("provider", c.providerName))
			}
		}

		if c.debug && (i+1)%10 == 0 {
			GetLogger().Debug("GetBatch: Progress",
				logger.Int("completed", i+1),
				logger.Int("total", count))
		}
	}
}

// logBatchComplete logs the completion of a batch operation
func (c *BirdImageCache) logBatchComplete(startTime time.Time, resultCount, requestCount int) {
	if c.debug {
		GetLogger().Debug("GetBatch: Completed batch operation",
			logger.Int("returned", resultCount),
			logger.Int("requested", requestCount),
			logger.Duration("duration", time.Since(startTime)))
	}
}
