// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"context"
	"fmt"
	"io"
	"log"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logging"
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

// Package-level logger for image provider related events
var (
	imageProviderLogger   *slog.Logger
	imageProviderLevelVar = new(slog.LevelVar) // Dynamic level control
	// imageProviderLogCloser func() error // Optional closer func
	// TODO: Call imageProviderLogCloser during graceful shutdown if needed
)

// SetDebugLogging enables or disables debug logging for the image provider
func SetDebugLogging(enable bool) {
	if enable {
		imageProviderLevelVar.Set(slog.LevelDebug)
		imageProviderLogger.Info("Debug logging enabled for image provider")
	} else {
		imageProviderLevelVar.Set(slog.LevelInfo)
		imageProviderLogger.Info("Debug logging disabled for image provider")
	}
}

func init() {
	var err error
	// Check if debug mode is enabled in configuration
	settings := conf.Setting()
	initialLevel := slog.LevelInfo
	if settings != nil && settings.Realtime.Dashboard.Thumbnails.Debug {
		initialLevel = slog.LevelDebug
	}
	imageProviderLevelVar.Set(initialLevel)

	// Default level is Info. Set to Debug for more detailed cache/provider info.
	imageProviderLogger, _, err = logging.NewFileLogger("logs/imageprovider.log", "imageprovider", imageProviderLevelVar)
	if err != nil {
		descriptiveErr := errors.Newf("imageprovider: failed to initialize file logger: %v", err).
			Component("imageprovider").
			Category(errors.CategoryFileIO).
			Context("log_file", "logs/imageprovider.log").
			Context("operation", "logger_initialization").
			Build()
		logging.Error("Failed to initialize imageprovider file logger", "error", descriptiveErr)
		// Fallback to a disabled logger (writes to io.Discard) but respects the level var
		logging.Warn("Imageprovider service falling back to a disabled logger due to initialization error.")
		fbHandler := slog.NewJSONHandler(io.Discard, &slog.HandlerOptions{Level: imageProviderLevelVar})
		imageProviderLogger = slog.New(fbHandler).With("service", "imageprovider")
	}
}

// emptyImageProvider is an ImageProvider that always returns an empty BirdImage.
type emptyImageProvider struct{}

func (l *emptyImageProvider) Fetch(scientificName string) (BirdImage, error) {
	return BirdImage{}, nil
}

// SetNonBirdImageProvider allows setting a custom ImageProvider for non-bird entries
func (c *BirdImageCache) SetNonBirdImageProvider(provider ImageProvider) {
	imageProviderLogger.Debug("Setting non-bird image provider", "provider_type", fmt.Sprintf("%T", provider))
	c.provider.Store(&provider)
}

// SetImageProvider allows setting a custom ImageProvider for testing purposes.
func (c *BirdImageCache) SetImageProvider(provider ImageProvider) {
	imageProviderLogger.Debug("Setting image provider (test override)", "provider_type", fmt.Sprintf("%T", provider))
	c.provider.Store(&provider)
}

const (
	defaultCacheTTL     = 14 * 24 * time.Hour // 14 days for positive entries
	negativeCacheTTL    = 15 * time.Minute    // 15 minutes for negative entries
	refreshInterval     = 1 * time.Hour       // Check for stale entries every hour in production
	refreshBatchSize    = 10                  // Number of entries to refresh in one batch
	refreshDelay        = 2 * time.Second     // Delay between refreshing individual entries
	negativeEntryMarker = "__NOT_FOUND__"     // Special URL marker for negative cache entries
)

// startCacheRefresh starts the background cache refresh routine
func (c *BirdImageCache) startCacheRefresh(quit chan struct{}) {
	logger := imageProviderLogger.With("provider", c.providerName)
	logger.Info("Starting cache refresh routine", "ttl", defaultCacheTTL, "interval", refreshInterval)

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
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("operation", "get_cached_entries_for_refresh").
			Build()
		logger.Error("Failed to get cached entries for refresh", "error", enhancedErr)
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-cache", c.providerName, "get_cached_entries_for_refresh")
		}
		return
	}

	logger.Debug("Checking entries for staleness", "entry_count", len(entries), "ttl", defaultCacheTTL)

	// Find stale entries
	var staleEntries []string // Store only scientific names instead of full entries
	now := time.Now()

	for i := range entries {
		// Check if it's a negative entry - use shorter TTL
		var cutoff time.Time
		if entries[i].URL == negativeEntryMarker {
			cutoff = now.Add(-negativeCacheTTL)
			logger.Debug("Checking negative entry staleness", "scientific_name", entries[i].ScientificName, "cached_at", entries[i].CachedAt, "ttl", negativeCacheTTL)
		} else {
			cutoff = now.Add(-defaultCacheTTL)
		}

		if entries[i].CachedAt.Before(cutoff) {
			logger.Debug("Found stale entry", "scientific_name", entries[i].ScientificName, "cached_at", entries[i].CachedAt, "cutoff", cutoff, "is_negative", entries[i].URL == negativeEntryMarker)
			staleEntries = append(staleEntries, entries[i].ScientificName)
		}
	}

	if len(staleEntries) == 0 {
		logger.Debug("No stale entries found")
		return
	}

	logger.Info("Found stale cache entries to refresh", "count", len(staleEntries))

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

	// Check if provider is set
	providerPtr := c.provider.Load()
	if providerPtr == nil {
		logger.Warn("Cannot refresh entry: provider is nil")
		return
	}
	provider := *providerPtr

	// Fetch new image with background context to use more restrictive rate limiting
	logger.Debug("Fetching new image data from provider (background refresh)")

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
			logger.Warn("Failed to fetch image during refresh", "error", enhancedErr)
		case errors.Is(err, ErrProviderNotConfigured):
			// This is normal - provider correctly identified it's not configured for use
			// No logging needed as this is expected operational behavior
		default:
			logger.Error("Failed to fetch image during refresh", "error", enhancedErr)
		}
		
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-fetch", c.providerName, "cache_refresh_fetch")
		}
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
func InitCache(providerName string, e ImageProvider, t *observability.Metrics, store datastore.Interface) *BirdImageCache {
	logger := imageProviderLogger.With("provider", providerName)
	logger.Info("Initializing image cache")
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
		// logger:       log.Default(), // Replaced by package logger
		quit: quit,
	}

	// Store the provider using atomic pointer
	cache.provider.Store(&e)

	// Load cached images into memory only if store is available
	if store != nil {
		logger.Info("DB store available, loading cached images")
		if err := cache.loadCachedImages(); err != nil {
			logger.Error("Error loading cached images", "error", err)
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
		return nil, ErrCacheMiss
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
		// Check if it's a record not found error (which is expected for cache misses)
		if errors.Is(err, datastore.ErrImageCacheNotFound) {
			logger.Debug("Image not found in DB cache (GetImageCache returned ErrImageCacheNotFound)")
			return nil, ErrCacheMiss
		}
		// Log database errors for other errors
		logger.Error("Failed to get image from DB cache", "error", err)
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "query_image_cache").
			Build()
		return nil, enhancedErr
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

// batchLoadFromDB loads multiple BirdImages from the database cache in a single query
func (c *BirdImageCache) batchLoadFromDB(scientificNames []string) (map[string]BirdImage, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "batch_size", len(scientificNames))
	logger.Debug("Attempting batch load from DB cache")

	if c.store == nil {
		logger.Warn("Cannot batch load from DB cache: DB store is nil")
		return nil, ErrCacheMiss
	}

	// Use the new batch query method for efficient loading
	settings := conf.Setting()
	if settings.Realtime.Dashboard.Thumbnails.Debug {
		logger.Debug("Calling GetImageCacheBatch", "provider_name", c.providerName, "species_count", len(scientificNames), "first_species", scientificNames[0])
	}
	dbImages, err := c.store.GetImageCacheBatch(c.providerName, scientificNames)
	if err != nil {
		logger.Error("Failed to batch load from DB cache", "error", err)
		return nil, err
	}
	if settings.Realtime.Dashboard.Thumbnails.Debug {
		logger.Debug("GetImageCacheBatch completed", "provider_name", c.providerName, "found_count", len(dbImages))
	}

	// If no images found with this provider, check fallback policy
	if len(dbImages) == 0 && len(scientificNames) > 0 {
		// Only try fallback providers if FallbackPolicy is "all"
		if settings.Realtime.Dashboard.Thumbnails.FallbackPolicy == "all" {
			if settings.Realtime.Dashboard.Thumbnails.Debug {
				logger.Debug("No images found with primary provider, trying fallback providers (policy: all)")
			}
			// Try common provider names as fallback
			fallbackProviders := []string{"avicommons", "wikimedia"}
			for _, fallbackProvider := range fallbackProviders {
				if fallbackProvider == c.providerName {
					continue // Skip our own provider name
				}
				fallbackImages, fallbackErr := c.store.GetImageCacheBatch(fallbackProvider, scientificNames)
				if fallbackErr == nil && len(fallbackImages) > 0 {
					if settings.Realtime.Dashboard.Thumbnails.Debug {
						logger.Info("Found images using fallback provider", "fallback_provider", fallbackProvider, "found_count", len(fallbackImages))
					}
					dbImages = fallbackImages
					break
				}
			}
		} else if settings.Realtime.Dashboard.Thumbnails.Debug {
			logger.Debug("No images found with primary provider, but fallback policy is 'none'")
		}
	}

	// Convert to BirdImage map
	result := make(map[string]BirdImage)
	now := time.Now()

	for name, dbImage := range dbImages {
		if dbImage == nil {
			continue
		}

		birdImage := BirdImage{
			URL:            dbImage.URL,
			ScientificName: dbImage.ScientificName,
			LicenseName:    dbImage.LicenseName,
			LicenseURL:     dbImage.LicenseURL,
			AuthorName:     dbImage.AuthorName,
			AuthorURL:      dbImage.AuthorURL,
			CachedAt:       dbImage.CachedAt,
			SourceProvider: dbImage.ProviderName,
		}

		// Check if entry is still valid based on its TTL
		cutoff := now.Add(-birdImage.GetTTL())

		// Only include non-stale entries
		if dbImage.CachedAt.After(cutoff) {
			result[name] = birdImage
			if birdImage.IsNegativeEntry() {
				logger.Debug("Loaded negative cache entry from DB batch",
					"scientific_name", name,
					"cached_at", dbImage.CachedAt)
			}
		} else {
			logger.Debug("Skipping stale DB entry from batch",
				"scientific_name", name,
				"cached_at", dbImage.CachedAt,
				"is_negative", birdImage.IsNegativeEntry())
		}
	}

	logger.Debug("Batch DB load completed", "found", len(result), "requested", len(scientificNames))
	return result, nil
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

	// For negative cache entries, we'll save them to DB with the special marker
	// This allows them to be loaded on restart (though they'll likely be expired)
	if image.IsNegativeEntry() {
		logger.Debug("Saving negative cache entry to DB")
	}

	logger.Debug("Saving image to DB cache", "url", image.URL, "source_provider", image.SourceProvider)

	// Ensure provider name is not empty, falling back to the cache's own name if needed
	providerNameToSave := image.SourceProvider
	if providerNameToSave == "" {
		logger.Warn("SourceProvider field was empty in BirdImage, falling back to cache provider name for DB save", "fallback_provider", c.providerName)
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
		logger.Error("Failed to save image to DB cache", "error", enhancedErr)
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-cache", c.providerName, "save_image_cache")
		}
	}
}

// loadCachedImages loads all relevant cached images from the DB into memory
func (c *BirdImageCache) loadCachedImages() error {
	logger := imageProviderLogger.With("provider", c.providerName)
	logger.Info("Loading all cached images from DB into memory")
	if c.store == nil {
		logger.Warn("Cannot load cached images: DB store is nil")
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
		logger.Error("Failed to get all image caches from DB", "error", enhancedErr)
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
				logger.Debug("Loaded negative cache entry from DB",
					"scientific_name", entries[i].ScientificName,
					"cached_at", entries[i].CachedAt)
			}
		} else {
			logger.Debug("Skipping load of stale DB entry into memory cache",
				"scientific_name", entries[i].ScientificName,
				"cached_at", entries[i].CachedAt,
				"is_negative", birdImage.IsNegativeEntry())
		}
	}

	logger.Info("Finished loading cached images into memory", "loaded_count", loadedCount, "total_db_entries_checked", len(entries))
	return nil
}

// tryInitialize ensures only one goroutine initializes a species image using mutexes.
// It returns the image, a boolean indicating if it was found in cache (true) or fetched (false), and an error.
func (c *BirdImageCache) tryInitialize(scientificName string) (BirdImage, bool, error) {
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)

	// Use a mutex for this specific scientific name to prevent concurrent fetches
	// We get the mutex BEFORE any cache checks to prevent race conditions
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

	// Check if already loaded after acquiring lock
	if val, ok := c.dataMap.Load(scientificName); ok {
		if imgPtr, ok := val.(*BirdImage); ok && imgPtr != nil {
			// Check if it's a valid entry (including negative cache entries)
			if imgPtr.URL != "" {
				// Check if negative entry is still valid
				if imgPtr.IsNegativeEntry() {
					cutoff := time.Now().Add(-imgPtr.GetTTL())
					if imgPtr.CachedAt.Before(cutoff) {
						logger.Debug("Negative cache entry expired, removing from memory")
						c.dataMap.Delete(scientificName)
						// Continue to re-fetch
					} else {
						logger.Debug("Returning valid negative cache entry after lock")
						return BirdImage{}, true, ErrImageNotFound
					}
				} else {
					logger.Debug("Initialization check: found in memory cache after acquiring lock")
					return *imgPtr, true, nil // Indicate it was found in cache
				}
			}
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
			// Check if it's already an enhanced error, if not enhance it
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
			logger.Error("Failed to initialize or fetch image (tryInitialize returned error)", "error", enhancedErr)
		}
		// Check if fallback is allowed by policy
		settings := conf.Setting()
		if settings.Realtime.Dashboard.Thumbnails.FallbackPolicy == "all" {
			// Even if initialization failed, maybe a fallback provider has it?
			// This requires the registry to be set.
			registry := c.GetRegistry()
			if registry != nil {
				triedProviders := map[string]bool{c.providerName: true}
				logger.Info("Primary provider failed, attempting fallback (policy: all)", "initial_error", err)
				fallbackImg, found := c.tryFallbackProviders(scientificName, triedProviders)
				if found {
					logger.Info("Image found via fallback provider", "fallback_provider", fallbackImg.SourceProvider)
					// Optionally store the fallback result in this cache's memory map?
					// c.dataMap.Store(scientificName, &fallbackImg)
					return fallbackImg, nil
				}
				logger.Warn("Image not found via fallback providers either")
			}
		} else {
			logger.Debug("Primary provider failed but fallback policy is 'none'", "initial_error", err)
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
	fetchStart := time.Now()
	logger := imageProviderLogger.With("provider", c.providerName, "scientific_name", scientificName)
	logger.Debug("Fetching and storing image (memory cache miss)")

	// 1. Try loading from DB cache first
	dbStart := time.Now()
	dbImage, err := c.loadFromDBCache(scientificName)
	dbDuration := time.Since(dbStart)

	if c.debug && dbDuration > 50*time.Millisecond {
		log.Printf("fetchAndStore: DB cache lookup for %s took %v", scientificName, dbDuration)
	}

	if isRealError(err) {
		// Logged within loadFromDBCache
		// Continue to provider fetch, but log this DB error
		logger.Warn("Error loading from DB cache, proceeding to fetch from provider", "db_error", err)
	}
	if dbImage != nil {
		// Check if it's a negative cache entry
		if dbImage.IsNegativeEntry() {
			cutoff := time.Now().Add(-dbImage.GetTTL())
			if dbImage.CachedAt.Before(cutoff) {
				logger.Debug("Negative cache entry from DB is expired, will re-fetch")
				// Don't return the expired negative entry, continue to fetch
			} else {
				logger.Info("Valid negative cache entry loaded from DB")
				// Store in memory cache
				c.dataMap.Store(scientificName, &dbImage)
				if c.metrics != nil {
					c.metrics.IncrementCacheMisses() // It was a memory miss, but DB hit
				}
				return BirdImage{}, ErrImageNotFound
			}
		} else {
			// Regular positive entry - check staleness with regular TTL
			cutoff := time.Now().Add(-defaultCacheTTL)
			if dbImage.CachedAt.Before(cutoff) {
				logger.Info("DB cache entry is stale, returning stale data and triggering background refresh", "cached_at", dbImage.CachedAt)
				// Trigger background refresh non-blockingly
				go c.refreshEntry(scientificName)
			} else {
				logger.Info("Image loaded from DB cache")
			}
			// Store in memory cache and return
			c.dataMap.Store(scientificName, &dbImage)
			if c.metrics != nil {
				c.metrics.IncrementCacheMisses() // It was a memory miss, but DB hit
			}
			return *dbImage, nil
		}
	}

	// 2. Not in DB or DB load failed, fetch from the actual provider
	logger.Info("Image not found in DB cache, fetching from provider")

	providerPtr := c.provider.Load()
	if providerPtr == nil {
		enhancedErr := errors.Newf("image provider for %s is not configured", c.providerName).
			Component("imageprovider").
			Category(errors.CategoryImageProvider).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "fetch_and_store").
			Build()
		logger.Error("Cannot fetch image: provider is nil", "error", enhancedErr)
		return BirdImage{}, enhancedErr
	}
	provider := *providerPtr

	// Check if this provider is specifically disabled in config
	// This requires access to the main settings, maybe pass relevant part to InitCache?
	// For now, assume provider passed to InitCache is enabled.

	providerStart := time.Now()
	fetchedImage, fetchErr := provider.Fetch(scientificName)
	providerDuration := time.Since(providerStart)

	if c.debug && providerDuration > 100*time.Millisecond {
		log.Printf("fetchAndStore: Provider fetch for %s took %v", scientificName, providerDuration)
	}

	if fetchErr != nil {
		// Check if it's already an enhanced error, if not enhance it
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
		logger.Error("Failed to fetch image from provider", "error", enhancedErr)
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-fetch", c.providerName, "provider_fetch")
		}
		// Check if it's a 'not found' error vs a transient error
		if errors.Is(fetchErr, ErrImageNotFound) {
			// Store negative cache entry to avoid refetching known misses
			logger.Info("Image not found by provider, storing negative cache entry")

			negativeEntry := BirdImage{
				URL:            negativeEntryMarker,
				ScientificName: scientificName,
				CachedAt:       time.Now(),
				SourceProvider: c.providerName,
			}

			// Store in memory cache
			c.dataMap.Store(scientificName, &negativeEntry)

			// Store in DB cache with negative marker
			c.saveToDB(&negativeEntry)

			if c.metrics != nil {
				c.metrics.IncrementCacheMisses() // It's still a cache miss
			}

			return BirdImage{}, fetchErr // Return the specific ErrImageNotFound
		}
		// For other errors (network, etc), don't cache and return the error
		logger.Warn("Provider error (not caching)", "error", enhancedErr)
		return BirdImage{}, enhancedErr
	}

	// If fetch was successful but returned an empty URL (provider couldn't find it)
	if fetchedImage.URL == "" {
		logger.Warn("Provider returned success but with an empty image URL")
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

	totalDuration := time.Since(fetchStart)
	if c.debug && totalDuration > 200*time.Millisecond {
		log.Printf("fetchAndStore: Total time for %s was %v (DB: %v, Provider: %v)",
			scientificName, totalDuration, dbDuration, providerDuration)
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

	// Create a local copy of triedProviders to avoid modifying the caller's map
	localTriedProviders := make(map[string]bool, len(triedProviders))
	for provider := range triedProviders {
		localTriedProviders[provider] = true
	}

	registry.RangeProviders(func(name string, cache *BirdImageCache) bool {
		if localTriedProviders[name] {
			logger.Debug("Skipping already tried provider", "provider", name)
			return true // Continue ranging
		}

		logger.Info("Attempting fallback fetch from provider", "provider", name)
		localTriedProviders[name] = true // Mark as tried

		// Instead of calling Get (which would recursively try fallbacks), use fetchAndStore directly
		// to avoid the fallback chain and potential infinite loop
		img, err := cache.fetchAndStore(scientificName)
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

	providerPtr := c.provider.Load()
	if providerPtr == nil {
		enhancedErr := errors.Newf("image provider %s is not configured", c.providerName).
			Component("imageprovider").
			Category(errors.CategoryImageProvider).
			Context("provider", c.providerName).
			Context("scientific_name", scientificName).
			Context("operation", "fetch_direct").
			Build()
		logger.Error("Cannot perform direct fetch: provider is nil", "error", enhancedErr)
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
		logger.Error("Direct fetch failed", "error", enhancedErr)
		return BirdImage{}, enhancedErr
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
		log.Printf("GetBatchCachedOnly: Completed in %v - found %d/%d in cache",
			time.Since(batchStart), len(result), len(scientificNames))
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
		memoryCacheDuration := time.Since(memoryCacheStart)
		log.Printf("GetBatch: Memory cache check completed in %v - found %d/%d in cache",
			memoryCacheDuration, len(result), len(scientificNames))
	}

	return missingNames
}

// checkDatabaseCache checks database cache for missing images and populates result map
// Returns list of names not found in database cache
func (c *BirdImageCache) checkDatabaseCache(missingNames []string, result map[string]BirdImage) []string {
	dbBatchStart := time.Now()
	if c.debug {
		log.Printf("GetBatch: Attempting batch DB cache lookup for %d items", len(missingNames))
	}

	dbImages, err := c.batchLoadFromDB(missingNames)
	if isRealError(err) {
		if c.debug {
			log.Printf("GetBatch: Batch DB load error: %v", err)
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
		dbBatchDuration := time.Since(dbBatchStart)
		hitRate := float64(dbHitCount) / float64(len(missingNames)) * 100
		log.Printf("GetBatch: Batch DB lookup completed in %v - found %d images in DB (hit rate: %.1f%%), %d still need provider fetch",
			dbBatchDuration, dbHitCount, hitRate, len(stillMissing))
	}

	return stillMissing
}

// fetchFromProvider fetches missing images from the provider in parallel
func (c *BirdImageCache) fetchFromProvider(missingNames []string, result map[string]BirdImage) {
	if c.debug {
		log.Printf("GetBatch: Need to fetch %d images from provider", len(missingNames))
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
		fetchDuration := time.Since(fetchStart)
		log.Printf("GetBatch: Provider fetch phase completed in %v (parallel with %d workers)",
			fetchDuration, maxWorkers)
	}
}

// fetchSingleImage fetches a single image from the provider
func (c *BirdImageCache) fetchSingleImage(scientificName string, sem chan struct{}, resultChan chan<- batchFetchResult) {
	sem <- struct{}{}        // Acquire semaphore
	defer func() { <-sem }() // Release semaphore

	singleFetchStart := time.Now()
	image, err := c.fetchAndStore(scientificName)
	singleFetchDuration := time.Since(singleFetchStart)

	if c.debug && singleFetchDuration > 100*time.Millisecond {
		log.Printf("GetBatch: Slow provider fetch for %s took %v", scientificName, singleFetchDuration)
	}

	resultChan <- batchFetchResult{
		name:  scientificName,
		image: image,
		err:   err,
	}
}

// collectFetchResults collects results from parallel fetches
func (c *BirdImageCache) collectFetchResults(count int, resultChan <-chan batchFetchResult, result map[string]BirdImage) {
	for i := 0; i < count; i++ {
		res := <-resultChan
		if res.err == nil {
			result[res.name] = res.image
		} else {
			if c.debug {
				log.Printf("GetBatch: Failed to fetch %s from provider: %v", res.name, res.err)
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
				imageProviderLogger.Error("Failed to fetch image in batch operation",
					"error", enhancedErr,
					"scientific_name", res.name,
					"provider", c.providerName)
			}
		}

		if c.debug && (i+1)%10 == 0 {
			log.Printf("GetBatch: Progress %d/%d images fetched from provider", i+1, count)
		}
	}
}

// logBatchComplete logs the completion of a batch operation
func (c *BirdImageCache) logBatchComplete(startTime time.Time, resultCount, requestCount int) {
	if c.debug {
		totalDuration := time.Since(startTime)
		log.Printf("GetBatch: Completed batch operation - returned %d/%d images in %v",
			resultCount, requestCount, totalDuration)
	}
}
