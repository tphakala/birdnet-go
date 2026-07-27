// imageprovider.go: Package imageprovider provides functionality for fetching and caching bird images.
package imageprovider

import (
	"context"
	"fmt"
	"maps"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unsafe"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/labels/nonbird"
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

// imageNotFoundFor wraps ErrImageNotFound with species and provider context so that
// Sentry events and logs identify which lookup failed. The returned error still
// satisfies errors.Is(err, ErrImageNotFound).
func imageNotFoundFor(scientificName, provider, operation string) *errors.EnhancedError {
	return errors.New(ErrImageNotFound).
		Component("imageprovider").
		Category(errors.CategoryImageFetch).
		Context("scientific_name", scientificName).
		Context("provider", provider).
		Context("operation", operation).
		Build()
}

// contextKey is a type used for context keys to avoid collisions
type contextKey string

// backgroundOperationKey is the context key for background operations
const backgroundOperationKey contextKey = "background"

// isBackgroundContext reports whether ctx was created by the background refresh path.
// The key has an unexported named type, so this is the only correct way to read it:
// context.Value compares key dynamic types, and an untyped string never matches.
func isBackgroundContext(ctx context.Context) bool {
	if ctx == nil {
		return false
	}
	bg, ok := ctx.Value(backgroundOperationKey).(bool)
	return ok && bg
}

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

// contextFetcher is implemented by providers that accept a context. It is not part of
// ImageProvider because that interface is the stable extension point for third-party
// providers; this is an optional capability probed at call time.
type contextFetcher interface {
	FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error)
}

// thumbnailSettings returns the dashboard thumbnail configuration, or its zero
// value when settings are unavailable.
//
// conf.Setting() returns nil when the configuration fails to load, and a hot
// reload can publish nil, so every read in this package has to tolerate it.
// Several sites dereferenced the result directly, which panics on a path that
// is reachable from any non-main entry point.
func thumbnailSettings() conf.Thumbnails {
	settings := conf.Setting()
	if settings == nil {
		return conf.Thumbnails{}
	}
	return settings.Realtime.Dashboard.Thumbnails
}

// normalizedFallbackPolicy reads the configured fallback policy, trimmed and
// lowercased, so that "All" or a stray trailing space is not read as "off".
func normalizedFallbackPolicy() string {
	return normalizeProviderName(thumbnailSettings().FallbackPolicy)
}

// normalizeProviderName folds a configured provider name for comparison.
func normalizeProviderName(name string) string {
	return strings.ToLower(strings.TrimSpace(name))
}

// normalizedImageProvider reads the configured image provider, folded.
//
// It exists for the same reason as normalizedFallbackPolicy: the fetch gate and
// the refresh gate must agree about which provider is configured, and they
// previously each folded the field their own way. The refresh gate has to stay
// a subset of the fetch gate, and two independent normalizations are how that
// relationship silently breaks.
func normalizedImageProvider() string {
	return normalizeProviderName(thumbnailSettings().ImageProvider)
}

// fetchFromProvider calls the provider's context-aware Fetch when it has one and falls
// back to the context-free Fetch otherwise. Without this, a cancelled caller still
// waits out the provider's full retry and rate-limit budget.
//
// A provider that only implements the context-free Fetch cannot be interrupted, so its
// call runs on its own goroutine and the caller abandons it when ctx is done. That
// goroutine may outlive the caller, but the alternative is worse: every waiter,
// including Close's WaitGroup and one of the bounded prefetch slots, would be held for
// as long as a third-party provider chooses to block. The abandoned result is dropped
// via a buffered channel so the goroutine always completes rather than parking on an
// unread send.
func fetchFromProvider(ctx context.Context, provider ImageProvider, scientificName string) (BirdImage, error) {
	if ctxProvider, ok := provider.(contextFetcher); ok {
		return ctxProvider.FetchWithContext(ctx, scientificName)
	}

	type fetchOutcome struct {
		img BirdImage
		err error
	}
	done := make(chan fetchOutcome, 1)
	go func() {
		// This goroutine can outlive its caller, so a panic here has no request
		// handler above it to recover: it would take the process down. The
		// startup warm-up installs the same guard around this call.
		defer func() {
			if r := recover(); r != nil {
				GetLogger().Error("Recovered from a panic in an image provider fetch",
					logger.String("scientific_name", scientificName),
					logger.Any("panic", r))
				done <- fetchOutcome{err: errors.Newf("image provider panicked: %v", r).
					Component("imageprovider").
					Category(errors.CategoryImageProvider).
					Context("scientific_name", scientificName).
					Build()}
			}
		}()
		img, err := provider.Fetch(scientificName)
		done <- fetchOutcome{img: img, err: err}
	}()

	select {
	case outcome := <-done:
		return outcome.img, outcome.err
	case <-ctx.Done():
		GetLogger().Debug("Abandoning a context-free provider fetch after cancellation",
			logger.String("scientific_name", scientificName),
			logger.Error(ctx.Err()))
		return BirdImage{}, ctx.Err()
	}
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

// GetTTL returns the appropriate TTL for this cache entry.
// Non-avian classes (legacy BirdNET names like "Siren", "Dog", and Perch v2/FSD50K
// classes like "power_tool", "speech") get nonAvianCacheTTL (~10 years, effectively
// permanent) since they will never have images from bird image providers.
func (b *BirdImage) GetTTL() time.Duration {
	if b.IsNegativeEntry() {
		if isNonAvianClass(b.ScientificName) {
			return nonAvianCacheTTL
		}
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
//
// Shutdown Safety: The closed flag and wg WaitGroup coordinate graceful shutdown.
// closed is set first to prevent new DB operations, then Close waits for in-flight
// operations to complete via wg before returning. This prevents "database is closed"
// errors from goroutines that outlive the DB connection. All goroutine spawns MUST use
// tryGo() instead of bare wg.Go() to prevent Add-after-Wait panics.
type BirdImageCache struct {
	provider     atomic.Pointer[ImageProvider] // Atomic pointer for lock-free concurrent access
	providerName string                        // Added: Name of the provider (e.g., "wikimedia")
	dataMap      sync.Map
	metrics      *metrics.ImageProviderMetrics
	debug        bool
	store        datastore.Interface
	fileCache    *ImageFileCache
	quit         chan struct{} // Channel to signal shutdown
	closeMu      sync.Mutex    // Guards closed + wg.Go atomicity in tryGo to prevent Add-after-Wait panic
	closed       atomic.Bool   // Set during shutdown to reject new DB operations
	// dbCorrupted is latched the first time the datastore reports a SQLite
	// corruption error ("database disk image is malformed"). Once latched,
	// loadFromDBCache, saveToDB and loadCachedImages all return ErrCacheMiss
	// (and the refresh path short-circuits too) without touching the DB, so the cache continues
	// to serve fresh fetches from the provider instead of generating an
	// unbounded stream of Sentry events (Forgejo #762, BIRDNET-GO-ZR/ZS).
	dbCorrupted atomic.Bool
	wg          sync.WaitGroup // Tracks in-flight DB and background operations
	// initializing holds the per-species initialization lock (see initLock). It is
	// unexported because its value is a synchronization primitive whose type is an
	// implementation detail: exporting it invited an unchecked type assertion on a map
	// any caller could write a different type into.
	initializing sync.Map
	registry     atomic.Pointer[ImageProviderRegistry] // Use atomic pointer
	// exhaustedSpecies tracks species whose primary + fallback providers have
	// all returned "not found" within the current TTL window. It maps a
	// scientific name to the time.Time the exhaustion was recorded. This lets
	// Get() short-circuit the fallback chain for species like "Siren" or
	// "Human vocal" that no image provider will ever resolve, eliminating
	// repeated SQLite reads on every detection cycle.
	exhaustedSpecies sync.Map
	// bgCtx is the parent context for every fetch this cache runs on its own
	// goroutines. It is deliberately detached from any request context: a
	// prefetch scheduled by an HTTP handler must outlive the response, which a
	// derived request context would not. bgCancel runs first in Close() so
	// wg.Wait() cannot block for the full prefetchTimeout.
	bgCtx    context.Context
	bgCancel context.CancelFunc
	// prefetching deduplicates in-flight background prefetches by scientific
	// name, so a dashboard asking for thirty uncached thumbnails schedules at
	// most one fetch per species rather than one per request.
	prefetching sync.Map
	// recentAttempts maps a scientific name to the time.Time its last background
	// resolution failed. It is the backoff the retry contract needs: the proxy
	// answers 503 + Retry-After and the client polls, so without it every poll
	// scheduled a fresh goroutine and a fresh provider attempt, and the retry
	// rate was set by the client rather than by us.
	recentAttempts sync.Map
	// dbAbsent maps a scientific name to the time.Time a database read last found
	// no row for it at all. It exists to keep the same client polling from
	// costing one SQLite SELECT per poll for a species nothing is known about.
	//
	// Deliberately recorded only on that exact observation, and not on "a
	// resolution is in progress": a species with a stale but perfectly servable
	// row also has a refresh in flight, and suppressing its database read would
	// answer 503 for an image we hold.
	dbAbsent sync.Map
	// recentAttemptsCount and dbAbsentCount count insertions since each map was
	// last cleared, so the maps can be bounded without per-entry accounting.
	// See maxMarkerEntries for why a bound is needed at all.
	recentAttemptsCount atomic.Int64
	dbAbsentCount       atomic.Int64
	// prefetchQueued counts registered prefetches (queued plus running) so an
	// unbounded species list cannot spawn an unbounded number of goroutines.
	prefetchQueued atomic.Int64
	// refreshQueued does the same for background refreshes of entries that were
	// served from cache. See maxQueuedRefreshes for why it is a separate budget.
	refreshQueued atomic.Int64
	// prefetchSem bounds how many prefetches contact a provider at once. The
	// providers are themselves rate limited, so a small number is enough to keep
	// the pipeline busy without piling up connections.
	prefetchSem chan struct{}
}

// GetFileCache returns the file cache instance, or nil if not configured.
func (c *BirdImageCache) GetFileCache() *ImageFileCache {
	return c.fileCache
}

// GetProviderName returns the name of the primary image provider (e.g. "wikimedia").
func (c *BirdImageCache) GetProviderName() string {
	return c.providerName
}

// proxyImagePathPrefix is the route prefix ProxyImageURL builds on.
const proxyImagePathPrefix = "/api/v2/media/image/"

// ProxyImageURL generates the proxy URL for serving a cached bird image.
//
// An empty or whitespace-only name yields an empty string rather than
// "/api/v2/media/image/", which matches no route and which the handler answers with
// 400. Guarding at this single choke point covers every producer, several of which
// emit the URL unconditionally and have no name check of their own.
func ProxyImageURL(scientificName string) string {
	trimmed := strings.TrimSpace(scientificName)
	if trimmed == "" {
		return ""
	}
	return proxyImagePathPrefix + url.PathEscape(trimmed)
}

// GetLogger returns the package logger for the imageprovider module
func GetLogger() logger.Logger {
	return logger.Global().Module("imageprovider")
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
	defaultCacheTTL     = 30 * 24 * time.Hour       // 30 days for positive entries
	negativeCacheTTL    = 15 * time.Minute          // 15 minutes for negative entries
	nonAvianCacheTTL    = 10 * 365 * 24 * time.Hour // ~10 years: effectively permanent for non-bird classes
	refreshInterval     = 1 * time.Hour             // Check for stale entries every hour in production
	refreshBatchSize    = 10                        // Number of entries to refresh in one batch
	refreshDelay        = 2 * time.Second           // Delay between refreshing individual entries
	negativeEntryMarker = "__NOT_FOUND__"           // Special URL marker for negative cache entries

	// Configuration constants
	fallbackPolicyAll = "all"  // Fallback policy to allow all providers
	providerAuto      = "auto" // Image provider setting meaning "pick one"

	// Performance threshold constants
	dbCacheLookupSlowThreshold = 50 * time.Millisecond  // Threshold for slow DB cache lookups
	providerFetchSlowThreshold = 100 * time.Millisecond // Threshold for slow provider fetch operations
	totalFetchSlowThreshold    = 200 * time.Millisecond // Threshold for slow total fetch operations

	// Background prefetch constants.
	//
	// prefetchTimeout bounds one background species fetch. It is generous
	// because the worst realistic case is three MediaWiki calls behind a
	// process-global 1 req/s limiter, each retried with backoff; the point of
	// the bound is that a fetch cannot live forever, not that it be quick.
	prefetchTimeout = 3 * time.Minute
	// maxConcurrentPrefetches bounds provider concurrency. The Wikipedia
	// provider serializes on a 1 req/s global limiter regardless, so a larger
	// number would only park more goroutines on the same lock.
	maxConcurrentPrefetches = 4
	// maxQueuedPrefetches caps registered-but-unfinished prefetches. It is a
	// backstop against a pathological caller (a page listing thousands of
	// species), not an expected limit.
	maxQueuedPrefetches = 256
)

// fallbackProviders defines the ordered list of providers to try when the primary provider fails.
// The order matters: avicommons is tried first as it's faster (local data), then wikimedia (remote API).
var fallbackProviders = []string{"avicommons", "wikimedia"}

// nonAvianClasses lists legacy BirdNET model output class names that are not bird species.
// These are the Title-case-with-spaces names produced by BirdNET v2.x split labels (e.g.
// "Power tools", "Human vocal"). They are kept as a fallback because the nonbird package
// matches lowercase underscore-joined FSD50K names and their first tokens, but does NOT
// match these BirdNET-specific Title-case strings.
// All entries will never have images from bird image providers, so their negative cache
// entries should never expire to avoid futile re-fetch attempts.
var nonAvianClasses = map[string]bool{
	"Siren":           true,
	"Dog":             true,
	"Power tools":     true,
	"Human vocal":     true,
	"Human non-vocal": true,
	"Human whistle":   true,
	"Jet":             true,
	"Gun":             true,
	"Fireworks":       true,
	"Noise":           true,
	"Environmental":   true,
	"Engine":          true,
}

// isNonAvianClass returns true if scientificName is a non-bird class that will never
// have images available from bird image providers.
// It checks two sources:
//   - nonAvianClasses: legacy BirdNET v2.x Title-case-with-spaces split names.
//   - nonbird.IsNonBirdName: Perch v2 (FSD50K) classes and their underscore-split
//     first tokens (e.g. "Power" from "power_tool", "speech"). Case-insensitive.
func isNonAvianClass(scientificName string) bool {
	return nonAvianClasses[scientificName] || nonbird.IsNonBirdName(scientificName)
}

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
		if entries[i].ScientificName == "" {
			log.Warn("Skipping image cache entry with empty scientific name",
				logger.Int("id", int(entries[i].ID)))
			continue
		}
		isNegative := entries[i].URL == negativeEntryMarker
		// Negative entries expire for direct user-triggered lookups, but the
		// hourly background refresh must not re-query providers for known misses.
		if isNegative {
			continue
		}
		if isCacheEntryStale(entries[i].CachedAt, false) {
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

	c.wg.Go(func() {
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
	})
}

// refreshStaleEntries refreshes cache entries that are older than TTL
func (c *BirdImageCache) refreshStaleEntries() {
	log := GetLogger().With(logger.String("provider", c.providerName))

	if c.closed.Load() {
		log.Debug("Cache is shutting down, skipping cache refresh")
		return
	}

	// Skip the hourly refresh once the DB has been latched corrupted; otherwise
	// the ticker would keep emitting one Sentry event per hour for the lifetime
	// of the process (Forgejo #762).
	if c.dbCorrupted.Load() {
		log.Debug("Skipping cache refresh: image cache database is corrupted")
		return
	}

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
		// Latch on corruption so the refresh ticker stops issuing read errors
		// (Forgejo #762).
		if c.handleDBCorruption(err, "get_cached_entries_for_refresh") {
			return
		}
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

// maxQueuedRefreshes bounds background refreshes of entries that were served
// from cache.
//
// Refreshes do not pass through the prefetch semaphore, so without a bound a
// dashboard rendering many stale species spawns one goroutine per species, each
// parked on the provider's global rate limiter. The bound is separate from
// maxQueuedPrefetches rather than shared with it so a burst of refreshes cannot
// starve the prefetches that resolve species nothing is known about, which are
// what a user is actually waiting on.
const maxQueuedRefreshes = 64

// scheduleRefresh registers a background refresh for a species already served
// from cache, deduplicated against any prefetch already in flight. The dedup
// entry and the queue slot are both rolled back if the goroutine could not be
// started, so a species is never left marked as refreshing when nothing is.
//
// Declining is safe: the entry was served, and the hourly sweep and the next
// request both remain able to refresh it.
func (c *BirdImageCache) scheduleRefresh(scientificName string) {
	if _, alreadyQueued := c.prefetching.LoadOrStore(scientificName, struct{}{}); alreadyQueued {
		return
	}
	if c.refreshQueued.Add(1) > maxQueuedRefreshes {
		c.refreshQueued.Add(-1)
		c.prefetching.Delete(scientificName)
		return
	}
	if !c.tryGo(func() {
		defer func() {
			c.refreshQueued.Add(-1)
			c.prefetching.Delete(scientificName)
		}()
		c.refreshEntry(scientificName)
	}) {
		c.refreshQueued.Add(-1)
		c.prefetching.Delete(scientificName)
	}
}

// refreshEntry refreshes a single cache entry
func (c *BirdImageCache) refreshEntry(scientificName string) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	if c.closed.Load() {
		log.Debug("Skipping cache entry refresh: cache is shutting down")
		return
	}

	log.Debug("Refreshing cache entry")

	// Check if provider is set
	providerPtr := c.provider.Load()
	if providerPtr == nil {
		log.Warn("Cannot refresh entry: provider is nil")
		return
	}
	provider := *providerPtr

	// Fetch new image with background context to use more restrictive rate limiting
	log.Debug("Fetching new image data from provider (background refresh)")

	// Refreshes are marked as background operations so the provider applies its
	// more conservative background rate limiter on top of the global one.
	ctx := context.WithValue(c.backgroundContext(), backgroundOperationKey, true)
	birdImage, err := fetchFromProvider(ctx, provider, scientificName)

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
			log.Debug("Image not found during cache refresh",
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

		// Try fallback providers before giving up
		if fallbackImg, found := c.tryRefreshFallback(scientificName); found {
			// Store in primary cache's memory with original SourceProvider for correct attribution
			c.dataMap.Store(scientificName, &fallbackImg)
			// Save to DB under primary provider name so loadFromDBCache finds it on restart
			// and refreshStaleEntries won't keep re-triggering for this species
			dbCopy := fallbackImg
			dbCopy.SourceProvider = c.providerName
			c.saveToDB(&dbCopy)
			log.Debug("Background refresh: using fallback provider image",
				logger.String("source_provider", fallbackImg.SourceProvider))
			if c.metrics != nil {
				c.metrics.IncrementImageDownloads()
			}
			// Download fallback image to file cache using dbCopy which has the corrected SourceProvider
			if c.fileCache != nil {
				c.tryGo(func() {
					c.downloadImageToFileCache(scientificName, &dbCopy)
				})
			}
		}
		return
	}

	// Ensure ScientificName is populated from the request before persisting,
	// mirroring storeSuccessfulFetch. Providers can return a BirdImage with an
	// empty ScientificName, which would otherwise cause a NOT NULL constraint
	// violation on image_caches.scientific_name during background refresh too
	// (Forgejo #756 sibling-callsite fix).
	birdImage.ScientificName = scientificName

	// Update memory cache
	log.Debug("Updating memory cache with refreshed image")
	c.dataMap.Store(scientificName, &birdImage)

	// Update database cache
	log.Debug("Updating database cache with refreshed image")
	c.saveToDB(&birdImage)

	// Download refreshed image to file cache
	if c.fileCache != nil && birdImage.URL != "" && !birdImage.IsNegativeEntry() {
		c.tryGo(func() {
			c.downloadImageToFileCache(scientificName, &birdImage)
		})
	}

	if c.metrics != nil {
		c.metrics.IncrementImageDownloads()
	}
	log.Debug("Successfully refreshed cache entry")
}

// tryRefreshFallback attempts to find an image from fallback providers during background refresh.
// It uses a two-tier approach to minimize network requests:
// Tier 1: Check fallback providers' DB cache (no network request needed)
// Tier 2: Fetch from fallback providers via network (only if DB had nothing valid)
func (c *BirdImageCache) tryRefreshFallback(scientificName string) (BirdImage, bool) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	if normalizedFallbackPolicy() != fallbackPolicyAll {
		return BirdImage{}, false
	}

	registry := c.GetRegistry()
	if registry == nil {
		return BirdImage{}, false
	}

	// Tier 1: Check fallback providers' DB cache (no network)
	if c.store != nil && !c.closed.Load() && !c.dbCorrupted.Load() {
		for _, providerName := range fallbackProviders {
			if providerName == c.providerName {
				continue
			}
			query := datastore.ImageCacheQuery{
				ScientificName: scientificName,
				ProviderName:   providerName,
			}
			cachedImage, err := c.store.GetImageCache(query)
			if err != nil {
				// Latch on corruption and stop trying further fallback DBs.
				if c.handleDBCorruption(err, "tier1_fallback_lookup") {
					break
				}
				continue
			}
			if cachedImage == nil {
				continue
			}
			img := dbEntryToBirdImage(cachedImage)
			if img.URL != "" && !img.IsNegativeEntry() && !isCacheEntryStale(img.CachedAt, false) {
				log.Debug("Background refresh: found valid fallback image in DB cache",
					logger.String("fallback_provider", providerName))
				return img, true
			}
		}
	}

	// Tier 2: Fetch from fallback providers via network (only if DB had nothing valid)
	log.Debug("Background refresh: no valid fallback in DB, trying network fetch")
	triedProviders := map[string]bool{c.providerName: true}
	return c.tryFallbackProviders(c.backgroundContext(), scientificName, triedProviders)
}

// Close stops the cache refresh routine, waits for in-flight DB operations to
// finish, and performs cleanup. The closed flag is set first so that new DB
// operations are rejected, then the quit channel is closed to stop the
// background refresh goroutine, and finally we wait for any in-flight
// operations tracked by the WaitGroup to drain.
func (c *BirdImageCache) Close() error {
	log := GetLogger().With(logger.String("provider", c.providerName))
	log.Info("Closing image provider cache")

	// Set closed flag under mutex to prevent tryGo from racing with wg.Wait.
	// The atomic store lets fast-path checks (saveToDB, etc.) bail out without
	// the mutex. The mutex ensures tryGo sees the flag before wg.Wait runs.
	// Early return if already closed prevents double-close of quit channel.
	c.closeMu.Lock()
	if c.closed.Load() {
		c.closeMu.Unlock()
		return nil
	}
	c.closed.Store(true)
	c.closeMu.Unlock()

	// Cancel background fetches before waiting on the WaitGroup. A prefetch may be
	// parked on the provider's rate limiter for minutes; without this, Close would
	// block for the full prefetchTimeout.
	if c.bgCancel != nil {
		c.bgCancel()
	}

	if c.quit != nil {
		log.Debug("Closing quit channel")
		close(c.quit)
	}

	// Wait for in-flight DB and background operations to finish.
	log.Debug("Waiting for in-flight operations to complete")
	c.wg.Wait()
	log.Info("All in-flight operations completed, image provider cache closed")

	return nil
}

// tryGo safely spawns a tracked goroutine. It returns false (and does not spawn)
// if the cache is shutting down. The mutex ensures the closed check and wg.Go are
// atomic with respect to Close(), preventing Add-after-Wait panics.
func (c *BirdImageCache) tryGo(fn func()) bool {
	c.closeMu.Lock()
	if c.closed.Load() {
		c.closeMu.Unlock()
		return false
	}
	c.wg.Go(fn)
	c.closeMu.Unlock()
	return true
}

// initCache initializes a new BirdImageCache with the given ImageProvider.
func InitCache(providerName string, e ImageProvider, t *observability.Metrics, store datastore.Interface) *BirdImageCache {
	log := GetLogger().With(logger.String("provider", providerName))
	log.Info("Initializing image cache")

	quit := make(chan struct{})

	var imageProviderMetrics *metrics.ImageProviderMetrics
	if t != nil {
		imageProviderMetrics = t.ImageProvider
	}

	bgCtx, bgCancel := context.WithCancel(context.Background())

	cache := &BirdImageCache{
		providerName: providerName, // Set provider name
		metrics:      imageProviderMetrics,
		debug:        thumbnailSettings().Debug, // Keep for potential checks
		store:        store,
		fileCache:    NewImageFileCache(imageCacheDir),
		quit:         quit,
		bgCtx:        bgCtx,
		bgCancel:     bgCancel,
		prefetchSem:  make(chan struct{}, maxConcurrentPrefetches),
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

// handleDBCorruption inspects err for SQLite corruption ("database disk image
// is malformed", "file is not a database", etc.) and, the first time it is
// seen, latches the dbCorrupted flag so subsequent DB operations short-circuit
// instead of repeatedly failing. Returns true when corruption is detected so
// callers can treat the read/write as a cache miss without surfacing further
// errors.
//
// Why latch instead of recover: the image cache shares the main SQLite file
// in v2-only mode, so we cannot safely DROP or recreate the table from here.
// A latch keeps the service healthy (fresh fetches from the provider) and
// stops a single bad file from generating thousands of Sentry events
// (Forgejo #762: 1,763 events from one corrupted system over 2+ months).
func (c *BirdImageCache) handleDBCorruption(err error, operation string) bool {
	if !datastore.IsDatabaseCorruption(err) {
		return false
	}
	if c.dbCorrupted.CompareAndSwap(false, true) {
		log := GetLogger().With(
			logger.String("provider", c.providerName),
			logger.String("operation", operation))
		enhancedErr := errors.New(err).
			Component("imageprovider").
			Category(errors.CategoryImageCache).
			Context("provider", c.providerName).
			Context("operation", operation).
			Context("recovery_action", "image_cache_db_disabled_for_session").
			Build()
		log.Error("Image cache database is corrupted, disabling further DB operations for this session",
			logger.Error(enhancedErr))
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-cache", c.providerName, "db_corruption")
		}
	}
	return true
}

// loadFromDBCache loads a BirdImage from the database cache
func (c *BirdImageCache) loadFromDBCache(scientificName string) (*BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Attempting to load image from DB cache")
	// Reject DB reads after shutdown to avoid "database is closed" errors
	if c.closed.Load() {
		log.Debug("Skipping DB load: cache is shutting down")
		return nil, ErrCacheMiss
	}
	// Skip DB reads once corruption has been detected to avoid Sentry spam.
	if c.dbCorrupted.Load() {
		log.Debug("Skipping DB load: image cache database is corrupted")
		return nil, ErrCacheMiss
	}
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
		// Treat SQLite corruption as a permanent cache miss and disable future
		// DB reads/writes for this session (Forgejo #762).
		if c.handleDBCorruption(err, "query_image_cache") {
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

// saveToDB saves a BirdImage to the database cache
func (c *BirdImageCache) saveToDB(image *BirdImage) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", image.ScientificName))
	// Reject DB writes after shutdown to avoid "database is closed" errors
	if c.closed.Load() {
		log.Debug("Skipping DB save: cache is shutting down")
		return
	}
	// Skip DB writes once corruption has been detected to avoid Sentry spam.
	if c.dbCorrupted.Load() {
		log.Debug("Skipping DB save: image cache database is corrupted")
		return
	}
	// Check if store is nil
	if c.store == nil {
		log.Warn("Cannot save to DB cache: DB store is nil")
		return
	}

	// Check if scientific name is empty - can't save without it
	if image.ScientificName == "" {
		log.Warn("Cannot save to DB cache: scientific name is empty")
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
		// SQLite corruption is permanent at this point; latch the flag so
		// future saves short-circuit rather than re-reporting (Forgejo #762).
		if c.handleDBCorruption(err, "save_image_cache") {
			return
		}
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
		// On corruption at startup, latch the flag and skip the warmup load
		// without surfacing an error so init can still complete (Forgejo #762).
		if c.handleDBCorruption(err, "get_all_image_caches") {
			return nil
		}
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
			// birdImage is already a *BirdImage; store it directly. Storing
			// &birdImage would put a **BirdImage in the map, which every reader
			// (checkCachedEntryAfterLock, MemoryUsage) fails to type-assert as
			// *BirdImage, silently defeating the startup warmup (Forgejo #1311).
			c.dataMap.Store(birdImage.ScientificName, birdImage)
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

	cutoff := time.Now().Add(-imgPtr.GetTTL())
	expired := imgPtr.CachedAt.Before(cutoff)

	if !imgPtr.IsNegativeEntry() {
		// A stale positive entry is still served, exactly as a stale DB row is;
		// the caller schedules the refresh that re-derives it. Deleting it here
		// instead would make every request for a species older than the TTL pay
		// a fresh SQLite read, because the DB row is the same age and gets
		// promoted back into memory with the same timestamp, so the next lookup
		// expires it again. That loop never converges while the provider is
		// unreachable, and on a store-less or corruption-latched cache it would
		// discard the only copy of the image the process has.
		log.Debug("Initialization check: found in memory cache after acquiring lock")
		return *imgPtr, true, false, nil
	}

	// Handle negative entry
	if expired {
		log.Debug("Negative cache entry expired, removing from memory")
		c.dataMap.Delete(scientificName)
		return BirdImage{}, false, false, nil
	}

	log.Debug("Returning valid negative cache entry after lock")
	return BirdImage{}, true, true, imageNotFoundFor(scientificName, c.providerName, "negative_cache_hit")
}

// tryInitialize ensures only one goroutine initializes a species image using a
// per-species lock. It returns the image, a boolean indicating if it was found in
// cache (true) or fetched (false), and an error.
func (c *BirdImageCache) tryInitialize(ctx context.Context, scientificName string) (BirdImage, bool, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	release, err := c.acquireInitLock(ctx, scientificName)
	if err != nil {
		return BirdImage{}, false, err
	}
	defer release()

	log.Debug("Acquired initialization lock")

	img, foundInCache, shouldReturn, err := c.checkCachedEntryAfterLock(scientificName, log)
	if foundInCache || shouldReturn {
		return img, foundInCache, err
	}

	log.Debug("Not in cache after lock, proceeding to fetch/store")
	img, err = c.fetchAndStore(ctx, scientificName)
	return img, false, err
}

// initLock returns the per-species initialization lock, creating it on first use.
//
// A buffered channel is used rather than a sync.Mutex so that waiting for the lock
// can be abandoned when the caller's context is cancelled. Without that, a request
// arriving while another goroutine holds the lock inherits the holder's full,
// uncancellable fetch duration.
//
// Do not delete the entry from the map on release. A goroutine that has already run
// LoadOrStore but not yet acquired the lock holds a reference to this channel;
// deleting it lets a later goroutine LoadOrStore a fresh one and fetch concurrently
// with that waiter, defeating the single-initialization guarantee. The map is bounded
// by the number of distinct species ever queried, so the retained channels are a
// negligible, fixed cost.
func (c *BirdImageCache) initLock(scientificName string) chan struct{} {
	// Load before LoadOrStore. The channel is an argument, so LoadOrStore evaluates
	// make() on every call even when the entry already exists, allocating a channel
	// that is immediately garbage. Measured at 128 B / 2 allocs / ~96ns per call
	// versus 0 B / 0 allocs / ~10ns for the Load-first form, on a path that now runs
	// once per thumbnail request. LoadOrStore still resolves the create race.
	if val, ok := c.initializing.Load(scientificName); ok {
		return val.(chan struct{})
	}
	val, _ := c.initializing.LoadOrStore(scientificName, make(chan struct{}, 1))
	return val.(chan struct{})
}

// acquireInitLock blocks until the per-species initialization lock is held or ctx is
// done. The returned function releases the lock and must be called exactly once.
func (c *BirdImageCache) acquireInitLock(ctx context.Context, scientificName string) (release func(), err error) {
	lock := c.initLock(scientificName)
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

// tryAcquireInitLock acquires the per-species initialization lock without blocking.
// A failed acquisition means another goroutine is already fetching this species.
func (c *BirdImageCache) tryAcquireInitLock(scientificName string) (release func(), ok bool) {
	lock := c.initLock(scientificName)
	select {
	case lock <- struct{}{}:
		return func() { <-lock }, true
	default:
		return nil, false
	}
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
func (c *BirdImageCache) tryFallbackOnGetError(ctx context.Context, err error, scientificName string, log logger.Logger) (BirdImage, bool) {
	if normalizedFallbackPolicy() != fallbackPolicyAll {
		log.Debug("Primary provider failed but fallback policy is 'none'",
			logger.Error(err))
		return BirdImage{}, false
	}

	registry := c.GetRegistry()
	if registry == nil {
		return BirdImage{}, false
	}

	triedProviders := map[string]bool{c.providerName: true}
	log.Debug("Primary provider failed, attempting fallback (policy: all)",
		logger.Error(err))
	fallbackImg, found := c.tryFallbackProviders(ctx, scientificName, triedProviders)
	if found {
		log.Debug("Image found via fallback provider",
			logger.String("fallback_provider", fallbackImg.SourceProvider))
		return fallbackImg, true
	}
	log.Debug("Image not found via fallback providers either")
	return BirdImage{}, false
}

// Get retrieves a bird image from the cache, fetching if necessary.
//
// Get can block for as long as the provider chain takes, which for a cold species is
// bounded only by the provider's own retry and rate-limit budget. Do NOT call it from
// an HTTP handler or from inside a lock; use GetCached plus PrefetchAsync there.
func (c *BirdImageCache) Get(scientificName string) (BirdImage, error) {
	return c.GetWithContext(context.Background(), scientificName)
}

// GetWithContext is Get with cancellation. The context bounds the wait for the
// per-species initialization lock and is passed to providers that implement
// FetchWithContext.
func (c *BirdImageCache) GetWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	if scientificName == "" {
		return BirdImage{}, imageNotFoundFor("", c.providerName, "get_empty_name")
	}

	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Get image request received")

	img, foundInCache, err := c.tryInitialize(ctx, scientificName)
	if err != nil {
		c.logInitializeError(err, scientificName, log)
		// If every provider has already been exhausted for this species
		// within the TTL window, short-circuit the fallback chain instead
		// of re-querying each fallback's database and provider on every
		// call. The exhausted entry was recorded inside tryFallbackProviders
		// the last time the chain ran to completion. Metrics are deliberately
		// unchanged here to mirror the pre-fix behavior for the primary
		// negative-cache path (which also did not emit a hit/miss metric).
		//
		// IMPORTANT: only short-circuit when the primary itself returned
		// ErrImageNotFound. Transient failures (network errors, DB errors,
		// provider initialization errors) must NOT be silently converted
		// into a 15-minute "not found" response — those should still try
		// the fallback chain so a working provider can serve the request.
		if errors.Is(err, ErrImageNotFound) && c.isSpeciesExhausted(scientificName) {
			log.Debug("Species already exhausted by all providers, skipping fallback chain")
			return c.synthesizeExhaustedResponse(scientificName)
		}
		if fallbackImg, found := c.tryFallbackOnGetError(ctx, err, scientificName, log); found {
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

// backgroundContext returns the cache's detached parent context for work that must
// outlive any request. A cache assembled outside InitCache (only tests do this) has
// none, so fall back to a plain background context rather than panicking.
func (c *BirdImageCache) backgroundContext() context.Context {
	if c.bgCtx == nil {
		return context.Background()
	}
	return c.bgCtx
}

// GetCached resolves a species image without ever contacting a provider, so it is
// safe on an HTTP request goroutine and inside a lock.
//
// It reports one of three states:
//   - found=true, negative=false: img is a usable image (memory or DB cache).
//   - negative=true: the species is known to have no image for now. Callers should
//     answer "not found" rather than schedule work.
//   - found=false, negative=false: nothing is cached. Callers should answer "try
//     again shortly" and schedule PrefetchAsync.
//
// A species another goroutine is currently fetching reports not-cached rather than
// waiting for that fetch, which is the whole point: waiting is what blocked the
// request path.
func (c *BirdImageCache) GetCached(scientificName string) (img BirdImage, found, negative bool) {
	img, found, negative = c.getCachedLocal(scientificName)
	if found || !negative {
		return img, found, negative
	}

	// This cache says "no image". Under fallbackpolicy: all that is not the final
	// answer, because the blocking path this replaces consulted the other registered
	// providers on exactly this outcome (Get -> tryFallbackOnGetError). Reporting the
	// primary's negative entry as definitive would answer 404 with a 24h browser
	// cache for every species the primary lacks but a fallback has, permanently
	// hiding images the user enabled fallbacks to get.
	if normalizedFallbackPolicy() != fallbackPolicyAll {
		return img, found, negative
	}
	registry := c.GetRegistry()
	if registry == nil {
		return img, found, negative
	}

	var fallbackImg BirdImage
	var fallbackFound bool
	registry.RangeProviders(func(name string, other *BirdImageCache) bool {
		if other == nil || name == c.providerName {
			return true
		}
		// getCachedLocal, not GetCached: the sweep must not recurse back into
		// another cache's own fallback sweep.
		if otherImg, otherFound, _ := other.getCachedLocal(scientificName); otherFound {
			fallbackImg, fallbackFound = otherImg, true
			return false
		}
		return true
	})
	if fallbackFound {
		return fallbackImg, true, false
	}

	// No fallback has it cached either. Only an exhausted marker, recorded once the
	// whole chain has actually run to completion, makes "no image" definitive.
	// Otherwise report indeterminate so the caller schedules a prefetch, which runs
	// the full chain including the fallbacks.
	if c.isSpeciesExhausted(scientificName) {
		return BirdImage{}, false, true
	}
	return BirdImage{}, false, false
}

// getCachedLocal resolves a species image from THIS cache's memory and DB only. It
// never contacts a provider and never consults another registered provider.
func (c *BirdImageCache) getCachedLocal(scientificName string) (img BirdImage, found, negative bool) {
	if scientificName == "" {
		return BirdImage{}, false, false
	}

	release, ok := c.tryAcquireInitLock(scientificName)
	if !ok {
		// A fetch is in flight. Report not-cached without scheduling anything: the
		// in-flight fetch will populate the cache.
		return BirdImage{}, false, false
	}
	defer release()

	// Built here rather than at the top of the function: the early returns above are
	// the common case once the cache is warm, and logger.With concatenates its fields
	// eagerly with no level gate, so constructing it up front cost more than the rest
	// of the lookup on a path that now runs once per thumbnail request.
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	// checkCachedEntryAfterLock's third result is shouldReturnError, which today is
	// true only for a valid negative entry. Named for what this function does with it.
	memImg, foundInMemory, isNegativeEntry, _ := c.checkCachedEntryAfterLock(scientificName, log)
	switch {
	case isNegativeEntry:
		return BirdImage{}, false, true
	case foundInMemory:
		// Serving a stale entry without scheduling its refresh would leave a
		// wrong image resident for the process lifetime, which is the whole
		// condition the memory TTL exists to end. Both sibling sites that serve
		// a stale positive pair it with this same scheduling.
		if isCacheEntryStale(memImg.CachedAt, false) {
			log.Debug("Serving a stale memory cache entry and refreshing it in the background",
				logger.Time("cached_at", memImg.CachedAt))
			c.scheduleRefresh(scientificName)
		}
		return memImg, true, false
	}

	// Nothing in memory, and a read a moment ago found no row either. Under the
	// 503 + Retry-After contract the client polls, so without this every poll for
	// a species nothing is known about costs its own SQLite SELECT: on a cold
	// dashboard with ~20 unresolved species, several per second on a 1-core Pi.
	//
	// The exhausted marker is re-checked here rather than skipped with the read.
	// It is the one verdict the shortcut can lose: it is consulted inside the
	// no-row branch below, so bypassing that branch would turn a definitive "no
	// image" (404, cacheable) into "not resolved yet" (503 + a scheduled
	// prefetch) for the marker's lifetime, and would skip GetCached's fallback
	// sweep, which runs only on a negative verdict.
	if c.dbKnownAbsent(scientificName) {
		if c.isSpeciesExhausted(scientificName) {
			return BirdImage{}, false, true
		}
		return BirdImage{}, false, false
	}

	dbImage, dbErr := c.loadFromDBCache(scientificName)
	if isRealError(dbErr) {
		// Do not fold a DB fault into "no image": that answer is cached by the
		// browser for a day. Report indeterminate so the caller says "try again"
		// instead, and make the fault visible rather than silently degrading every
		// thumbnail request.
		log.Warn("Error reading the image DB cache, reporting the species as unresolved",
			logger.Error(dbErr))
		return BirdImage{}, false, false
	}
	if dbImage == nil {
		// Remember the miss so the client's polling does not repeat this read
		// every few seconds. Only a genuine "the database answered, and it holds
		// no row" counts: loadFromDBCache also reports a miss when it did not
		// consult the database at all (shutting down, corruption latched, no
		// store configured), and recording those would assert an observation
		// that never happened and would re-arm the marker on every lookup.
		if c.dbWasConsulted() {
			c.recordDBAbsent(scientificName)
		}

		// Nothing is known yet. The exhausted marker is consulted only here, AFTER
		// memory and the DB: it records that every provider failed within the TTL
		// window, and it must never pre-empt a live cache entry for the species.
		if c.isSpeciesExhausted(scientificName) {
			return BirdImage{}, false, true
		}
		return BirdImage{}, false, false
	}

	// A row exists, so any earlier "no row" observation is stale.
	c.dbAbsent.Delete(scientificName)

	if dbImage.IsNegativeEntry() {
		// An expired negative entry is not an answer; let the caller re-fetch.
		if !isNonAvianClass(scientificName) && isCacheEntryStale(dbImage.CachedAt, true) {
			return BirdImage{}, false, false
		}
		c.dataMap.Store(scientificName, dbImage)
		return BirdImage{}, false, true
	}

	if dbImage.URL == "" {
		return BirdImage{}, false, false
	}

	// Mirrors handleDBCacheHit: a memory miss served from the DB is a cache miss.
	// Without this the metric would read zero, since GetCached is now the only
	// lookup on the request, SSE and MQTT paths.
	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
	}

	// Promote to memory so subsequent lookups skip the DB. Stored BEFORE spawning the
	// refresh below: the go statement is a happens-before edge, so the refresh's own
	// store is guaranteed to land after this one and cannot be clobbered back to
	// stale. Same ordering, and same reason, as handleDBCacheHit.
	c.dataMap.Store(scientificName, dbImage)

	// A stale entry is still served, but it must also be refreshed, exactly as the
	// blocking path did. Callers see found=true and therefore schedule nothing
	// themselves, so without this a stale image would only ever be corrected by the
	// hourly sweep.
	if isCacheEntryStale(dbImage.CachedAt, false) {
		log.Debug("Serving a stale DB cache entry and refreshing it in the background",
			logger.Time("cached_at", dbImage.CachedAt))
		c.scheduleRefresh(scientificName)
	}

	return *dbImage, true, false
}

// PrefetchAsync resolves a species image on a background goroutine and, on success,
// makes sure its bytes are on disk. It never blocks the caller and is deduplicated by
// species, so thirty concurrent requests for the same cold species schedule one fetch.
//
// It returns true when a prefetch is now in flight for the species (whether scheduled
// by this call or already running).
func (c *BirdImageCache) PrefetchAsync(scientificName string) bool {
	if scientificName == "" {
		return false
	}
	if c.closed.Load() {
		return false
	}
	// A cache assembled outside InitCache (only tests do this) has neither the
	// bounding semaphore nor the cancellable parent context, so a prefetch there
	// would be unbounded and un-stoppable. Decline rather than degrade.
	if c.prefetchSem == nil || c.bgCtx == nil {
		return false
	}
	// The previous attempt failed too recently to be worth repeating. Without
	// this the client's retry schedule became the provider retry schedule, since
	// nothing but a not-found answer was ever recorded.
	if c.recentlyAttempted(scientificName) {
		return false
	}

	// Reserve a queue slot BEFORE claiming the dedup entry. Claiming first would let a
	// second caller be told "already queued" by the very registration that is about to
	// be rolled back for exceeding the cap, so the species would be reported as in
	// flight while nothing ran.
	if c.prefetchQueued.Add(1) > maxQueuedPrefetches {
		c.prefetchQueued.Add(-1)
		GetLogger().Debug("Prefetch queue is full, dropping request",
			logger.String("provider", c.providerName),
			logger.String("scientific_name", scientificName),
			logger.Int("max_queued", maxQueuedPrefetches))
		return false
	}

	if _, alreadyQueued := c.prefetching.LoadOrStore(scientificName, struct{}{}); alreadyQueued {
		c.prefetchQueued.Add(-1)
		return true
	}

	// Only now, past both early returns, is the logger worth building: it is captured
	// by the goroutine below and used for the whole prefetch.
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	scheduled := c.tryGo(func() {
		defer func() {
			c.prefetchQueued.Add(-1)
			c.prefetching.Delete(scientificName)
		}()
		c.runPrefetch(scientificName, log)
	})
	if !scheduled {
		c.prefetchQueued.Add(-1)
		c.prefetching.Delete(scientificName)
		return false
	}
	return true
}

// runPrefetch performs one background species resolution. It runs on a goroutine
// tracked by the cache WaitGroup, so every wait it performs must be cancellable via
// bgCtx or Close would hang.
func (c *BirdImageCache) runPrefetch(scientificName string, log logger.Logger) {
	// A provider panic on this goroutine has no handler above it to recover.
	defer func() {
		if r := recover(); r != nil {
			log.Error("Recovered from a panic during a background image prefetch",
				logger.Any("panic", r))
		}
	}()

	bg := c.backgroundContext()
	if c.prefetchSem != nil {
		select {
		case c.prefetchSem <- struct{}{}:
			defer func() { <-c.prefetchSem }()
		case <-bg.Done():
			return
		}
	}

	// Another goroutine is already fetching this species (a foreground Get, or the
	// hourly refresh sweep). GetCached reports "not cached" while that lock is held,
	// so the handler schedules a prefetch that would otherwise sit on one of the few
	// slots for the whole prefetchTimeout waiting for work that is already underway.
	if release, ok := c.tryAcquireInitLock(scientificName); ok {
		release()
	} else {
		log.Debug("Skipping prefetch: a fetch for this species is already in flight")
		return
	}

	// The timeout starts here rather than at scheduling time so a request queued
	// behind the semaphore is not charged for its wait.
	ctx, cancel := context.WithTimeout(bg, prefetchTimeout)
	defer cancel()

	img, err := c.GetWithContext(ctx, scientificName)
	if err != nil {
		// A not-found answer is already durable in the negative cache; anything
		// else is a failed attempt and needs its own short backoff, or the next
		// client poll schedules an identical attempt immediately.
		//
		// Cancellation, and only cancellation, is exempt: ctx is bgCtx plus
		// prefetchTimeout, so a Canceled means this cache is shutting down and
		// recording it would back the species off for the first 30 seconds of
		// the next run. A DeadlineExceeded is the opposite case - the provider
		// chain took longer than prefetchTimeout, which is a hung or throttled
		// upstream and precisely what the backoff is for.
		if !errors.Is(ctx.Err(), context.Canceled) && !errors.Is(err, ErrImageNotFound) {
			c.recordFailedAttempt(scientificName)
		}
		log.Debug("Background image prefetch did not resolve", logger.Error(err))
		return
	}
	c.clearResolutionMarkers(scientificName)
	if img.URL == "" || img.IsNegativeEntry() || c.fileCache == nil {
		return
	}

	provider := img.SourceProvider
	if provider == "" {
		provider = c.providerName
	}

	// storeSuccessfulFetch already downloads the bytes for a freshly fetched image,
	// so only download here when the file is actually missing or stale. Without this
	// check a cache hit in GetWithContext would re-download on every prefetch.
	if path, _, fresh, getErr := c.fileCache.Get(provider, scientificName); getErr == nil && path != "" && fresh {
		return
	}

	if _, _, dlErr := c.fileCache.DownloadAndStore(ctx, provider, scientificName, img.URL); dlErr != nil {
		log.Info("Background image prefetch failed to download image bytes", logger.Error(dlErr))
		return
	}
	log.Debug("Background image prefetch completed")
}

// fetchAndStore tries to load from DB, then fetches from the provider if necessary, and stores the result.
func (c *BirdImageCache) fetchAndStore(ctx context.Context, scientificName string) (BirdImage, error) {
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
			err := c.getDBCacheError(&result, scientificName)
			return result, err
		}
	}

	// 2. Not in DB or DB load failed, fetch from the actual provider
	return c.fetchSingleFromProvider(ctx, scientificName, fetchStart)
}

// getDBCacheError returns the appropriate error for a DB cache result.
func (c *BirdImageCache) getDBCacheError(result *BirdImage, scientificName string) error {
	if result.URL == "" || result.IsNegativeEntry() {
		return imageNotFoundFor(scientificName, c.providerName, "db_cache_negative")
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

	// Store the (possibly stale) DB entry in memory BEFORE spawning any background
	// refresh. The go statement is a happens-before edge for the goroutine's start,
	// so a refresh goroutine spawned afterwards is guaranteed to run its own
	// dataMap.Store(fresh) AFTER this store. Storing here first prevents this stale
	// store from racing the refresh and clobbering the fresh value back to stale in
	// memory while the DB already holds the fresh entry.
	c.dataMap.Store(scientificName, dbImage)

	// Regular positive entry - check staleness
	if isCacheEntryStale(dbImage.CachedAt, false) {
		log.Debug("DB cache entry is stale, returning stale data and triggering background refresh",
			logger.Time("cached_at", dbImage.CachedAt))
		c.scheduleRefresh(scientificName)
	} else {
		log.Debug("Image loaded from DB cache")
	}

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

	if !isNonAvianClass(scientificName) && isCacheEntryStale(dbImage.CachedAt, true) {
		log.Debug("Negative cache entry from DB is expired, will re-fetch")
		return BirdImage{}, false // Continue to provider fetch
	}

	log.Debug("Valid negative cache entry loaded from DB")
	c.dataMap.Store(scientificName, dbImage)
	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
	}
	return BirdImage{}, true // Return with ErrImageNotFound
}

// fetchSingleFromProvider fetches an image from the provider when not found in cache.
func (c *BirdImageCache) fetchSingleFromProvider(ctx context.Context, scientificName string, fetchStart time.Time) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Image not found in DB cache, fetching from provider")

	provider, err := c.getProvider()
	if err != nil {
		return c.handleProviderNilError(scientificName)
	}

	providerStart := time.Now()
	fetchedImage, fetchErr := fetchFromProvider(ctx, provider, scientificName)
	providerDuration := time.Since(providerStart)

	c.logSlowOperation("Provider fetch", scientificName, providerDuration, providerFetchSlowThreshold)

	if fetchErr != nil {
		return c.handleProviderFetchError(scientificName, fetchErr)
	}

	if fetchedImage.URL == "" {
		log.Warn("Provider returned success but with an empty image URL")
		return BirdImage{}, imageNotFoundFor(scientificName, c.providerName, "provider_empty_url")
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

	// Check for expected "not found" condition first — this is not an error.
	// Many species legitimately have no images in AviCommons or Wikipedia.
	if errors.Is(fetchErr, ErrImageNotFound) {
		if c.metrics != nil {
			c.metrics.IncrementDownloadErrorsWithCategory("image-fetch", c.providerName, "not_found")
		}
		return c.storeNegativeCacheEntry(scientificName, fetchErr)
	}

	// Actual provider errors — log at error level
	enhancedErr := c.enhanceFetchError(fetchErr, scientificName)
	log.Error("Failed to fetch image from provider", logger.Error(enhancedErr))

	if c.metrics != nil {
		c.metrics.IncrementDownloadErrorsWithCategory("image-fetch", c.providerName, "provider_error")
	}

	return BirdImage{}, enhancedErr
}

// enhanceFetchError wraps a fetch error with context if needed.
// For errors that are already EnhancedError with context (e.g., from imageNotFoundFor),
// returns as-is. For enhanced errors missing context or plain errors, creates a new
// wrapped error preserving the original category to avoid false positives in
// category-based errors.Is matching.
func (c *BirdImageCache) enhanceFetchError(fetchErr error, scientificName string) *errors.EnhancedError {
	var enhancedErr *errors.EnhancedError
	if errors.As(fetchErr, &enhancedErr) {
		// Already enhanced — check if it has species context
		if _, hasName := enhancedErr.Context["scientific_name"]; hasName {
			return enhancedErr
		}
	}

	// Wrap with full context, preserving whatever category the cause carries so
	// a CategoryNetwork throttle is not re-tagged as CategoryImageFetch (which
	// would cause false positives in errors.Is(err, ErrImageNotFound)).
	return errors.New(fetchErr).
		Component("imageprovider").
		Category(causeCategory(fetchErr, errors.CategoryImageFetch)).
		Context("provider", c.providerName).
		Context("scientific_name", scientificName).
		Context("operation", "provider_fetch").
		Build()
}

// storeNegativeCacheEntry stores a negative cache entry for a not-found image.
func (c *BirdImageCache) storeNegativeCacheEntry(scientificName string, fetchErr error) (BirdImage, error) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))
	log.Debug("Image not found by provider, storing negative cache entry")

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

	// Ensure ScientificName is populated from the request before persisting,
	// mirroring storeNegativeCacheEntry. Some providers return a BirdImage with
	// an empty ScientificName, which would otherwise cause a NOT NULL constraint
	// violation on image_caches.scientific_name when saved via the fetch path
	// (Forgejo #756).
	fetchedImage.ScientificName = scientificName
	fetchedImage.CachedAt = time.Now()
	fetchedImage.SourceProvider = c.providerName
	log.Debug("Image successfully fetched from provider", logger.String("url", fetchedImage.URL))

	c.dataMap.Store(scientificName, fetchedImage)
	c.saveToDB(fetchedImage)

	// Download image to disk cache
	if c.fileCache != nil && fetchedImage.URL != "" && !fetchedImage.IsNegativeEntry() {
		c.tryGo(func() {
			c.downloadImageToFileCache(scientificName, fetchedImage)
		})
	}

	if c.metrics != nil {
		c.metrics.IncrementCacheMisses()
		c.metrics.IncrementImageDownloads()
	}

	return *fetchedImage
}

// downloadImageToFileCache fetches the image bytes from the URL and stores in the file cache.
func (c *BirdImageCache) downloadImageToFileCache(scientificName string, img *BirdImage) {
	log := GetLogger().With(
		logger.String("provider", c.providerName),
		logger.String("scientific_name", scientificName))

	provider := img.SourceProvider
	if provider == "" {
		provider = c.providerName
	}

	if _, _, err := c.fileCache.DownloadAndStore(c.backgroundContext(), provider, scientificName, img.URL); err != nil {
		// File cache populates lazily; the image is already in the in-memory and
		// DB caches, so a download failure here just means the proxy will refetch
		// on the next request. Logged at info to keep the diagnostics health
		// check from flagging an "elevated error count" on transient upstream
		// failures (404s, throttling, etc.).
		log.Info("Failed to download image to file cache", logger.Error(err))
		return
	}
	log.Debug("Image downloaded to file cache")
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

// exhaustedSpeciesTTL is the lifetime of an exhausted-species cache entry.
// Reuses negativeCacheTTL so the two negative-cache layers stay in sync: an
// entry remains short-circuited exactly as long as the underlying negative DB
// entries remain valid. Using the existing constant avoids drift between
// layers.
const exhaustedSpeciesTTL = negativeCacheTTL

// recordSpeciesExhausted marks a species as having exhausted every registered
// image provider within the current TTL window. Subsequent Get() calls for
// the same species will short-circuit the fallback chain until the entry
// ages out.
//
// Concurrency: sync.Map.Store is safe for concurrent writes; racing callers
// will simply overwrite each other's timestamp with near-identical values,
// which is harmless because the TTL window is coarse (minutes).
func (c *BirdImageCache) recordSpeciesExhausted(scientificName string) {
	if scientificName == "" {
		return
	}
	c.exhaustedSpecies.Store(scientificName, time.Now())
}

// isSpeciesExhausted reports whether the species has a live exhaustion entry.
// It performs lazy expiration: if the stored timestamp is older than the TTL,
// the entry is deleted and false is returned so the fallback chain can retry.
//
// Concurrency: sync.Map.Load/Delete are safe for concurrent access. A race
// between "isSpeciesExhausted observed entry as fresh" and another goroutine
// deleting it is fine — the caller just treats it as exhausted for this call,
// and the next call will re-check. No double-fetch of providers can occur as
// a result of this read, because recording only happens after all providers
// have already been tried (see tryFallbackProviders).
func (c *BirdImageCache) isSpeciesExhausted(scientificName string) bool {
	if scientificName == "" {
		return false
	}
	v, ok := c.exhaustedSpecies.Load(scientificName)
	if !ok {
		return false
	}
	stamp, ok := v.(time.Time)
	if !ok {
		// Defensive: unexpected type, drop the bogus entry.
		c.exhaustedSpecies.Delete(scientificName)
		return false
	}
	if time.Since(stamp) > exhaustedSpeciesTTL {
		c.exhaustedSpecies.Delete(scientificName) // lazy expiration
		return false
	}
	return true
}

// recentAttemptTTL bounds how often a species whose last background resolution
// failed is attempted again, and how long a database miss for it is trusted.
//
// It is deliberately much shorter than negativeCacheTTL: a negative entry means
// "the providers answered, and the answer is no image", which is durable, while
// these markers mean "the attempt failed" and "there was no row a moment ago",
// which are both usually transient.
const recentAttemptTTL = 30 * time.Second

// markerLive reports whether a marker map holds a timestamp for this species
// that is still within recentAttemptTTL, expiring it lazily if not.
func markerLive(markers *sync.Map, scientificName string) bool {
	if scientificName == "" {
		return false
	}
	v, ok := markers.Load(scientificName)
	if !ok {
		return false
	}
	stamp, ok := v.(time.Time)
	if !ok {
		// Defensive: unexpected type, drop the bogus entry.
		markers.Delete(scientificName)
		return false
	}
	if time.Since(stamp) > recentAttemptTTL {
		markers.Delete(scientificName) // lazy expiration
		return false
	}
	return true
}

// maxMarkerEntries bounds each marker map.
//
// The keys are scientific names supplied by the caller: the media endpoints
// hand the client's string straight to GetCached and PrefetchAsync, so the key
// space is request traffic rather than the model's label set, and a marker map
// with only lazy expiry would grow at request rate. Measured at ~150 B per
// distinct name, an unbounded map reaches ~100 MB in 250k distinct requests,
// which a 512 MB target does not survive.
//
// Both markers are pure optimizations: declining to record one, or dropping the
// whole set, only costs a database read or an earlier retry. So the bound is
// enforced by clearing rather than by evicting, which needs no per-entry
// accounting and cannot itself become a source of error.
const maxMarkerEntries = 8192

// recordMarker stamps a marker map, clearing it whole once the insertion count
// since the last clear passes the bound.
//
// count is not the live size: deletions do not decrement it, and concurrent
// callers can both trip the bound and clear. So the map is kept near the bound
// rather than strictly under it, which is all that is needed here, since both
// markers are optimizations whose loss costs one database read.
func recordMarker(markers *sync.Map, count *atomic.Int64, scientificName string) {
	if scientificName == "" {
		return
	}
	if count.Add(1) > maxMarkerEntries {
		markers.Clear()
		count.Store(1)
	}
	markers.Store(scientificName, time.Now())
}

// recordFailedAttempt notes that a background resolution for this species just
// failed, so the next attempt is deferred by recentAttemptTTL.
func (c *BirdImageCache) recordFailedAttempt(scientificName string) {
	recordMarker(&c.recentAttempts, &c.recentAttemptsCount, scientificName)
}

// clearResolutionMarkers drops both short-lived markers after a resolution
// succeeds, so neither the prefetch backoff nor the database-miss shortcut
// outlives the condition it was recorded for.
func (c *BirdImageCache) clearResolutionMarkers(scientificName string) {
	c.recentAttempts.Delete(scientificName)
	c.dbAbsent.Delete(scientificName)
}

// recentlyAttempted reports whether a background resolution for this species
// failed within the backoff window.
func (c *BirdImageCache) recentlyAttempted(scientificName string) bool {
	return markerLive(&c.recentAttempts, scientificName)
}

// dbWasConsulted reports whether a database read could actually have reached the
// store. loadFromDBCache reports the same ErrCacheMiss whether the row is
// genuinely absent or the read never happened, and only the former is an
// observation about the species.
func (c *BirdImageCache) dbWasConsulted() bool {
	return c.store != nil && !c.closed.Load() && !c.dbCorrupted.Load()
}

// recordDBAbsent notes that a database read found no row for this species.
func (c *BirdImageCache) recordDBAbsent(scientificName string) {
	recordMarker(&c.dbAbsent, &c.dbAbsentCount, scientificName)
}

// dbKnownAbsent reports whether a recent database read found no row for this
// species, so the request path can answer "unresolved" without repeating it.
func (c *BirdImageCache) dbKnownAbsent(scientificName string) bool {
	return markerLive(&c.dbAbsent, scientificName)
}

// synthesizeExhaustedResponse returns the short-circuit response used when a
// species is already known to be exhausted. It mirrors the value returned by
// the primary-provider negative path so callers do not need to distinguish
// the two.
func (c *BirdImageCache) synthesizeExhaustedResponse(scientificName string) (BirdImage, error) {
	return BirdImage{}, imageNotFoundFor(scientificName, c.providerName, "exhausted_species_cache")
}

// tryFallbackProviders attempts to get the image from other registered providers.
func (c *BirdImageCache) tryFallbackProviders(ctx context.Context, scientificName string, triedProviders map[string]bool) (BirdImage, bool) {
	log := GetLogger().With(logger.String("scientific_name", scientificName))
	log.Debug("Trying fallback providers")
	registry := c.GetRegistry()
	if registry == nil {
		log.Warn("Cannot try fallback providers: registry is not set")
		return BirdImage{}, false
	}

	var foundImage BirdImage
	found := false

	// Track whether every fallback failure we observed was specifically
	// ErrImageNotFound. Only when ALL fallbacks (and the primary, by virtue
	// of having been tried before this function is called) report
	// "not found" do we record the species as exhausted. Transient failures
	// (network errors, DB errors, provider init failures) must not poison
	// the exhausted-species cache for the TTL window — they should be
	// retried on the next Get().
	allFallbacksNotFound := true
	anyFallbackTried := false

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

		log.Debug("Attempting fallback fetch from provider", logger.String("provider", name))
		localTriedProviders[name] = true // Mark as tried
		anyFallbackTried = true

		// Instead of calling Get (which would recursively try fallbacks), use fetchAndStore directly
		// to avoid the fallback chain and potential infinite loop
		img, err := cache.fetchAndStore(ctx, scientificName)
		if err != nil {
			if errors.Is(err, ErrImageNotFound) {
				log.Info("Fallback provider has no image",
					logger.String("provider", name),
					logger.String("species", scientificName))
			} else {
				allFallbacksNotFound = false
				log.Warn("Fallback provider failed to get image",
					logger.String("provider", name),
					logger.Error(err))
			}
			return true // Continue ranging
		}

		// Check if a valid image was found (URL is not empty)
		if img.URL != "" {
			log.Debug("Image found via fallback provider",
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
		log.Debug("Fallback successful", logger.String("found_provider", foundImage.SourceProvider))
	} else {
		log.Debug("Fallback unsuccessful, image not found in any provider")
		// Record exhaustion on the primary cache so future Get() calls for
		// this species short-circuit before re-running the fallback chain.
		// The entry is keyed by species only and does not preserve which
		// provider failed; by construction, every registered provider has
		// been tried for this species in this invocation.
		//
		// CORRECTNESS GATE: only record exhaustion when at least one
		// fallback was actually tried AND every failure was an
		// ErrImageNotFound.
		//
		// The gate cannot be relaxed on the premise that the primary already
		// returned ErrImageNotFound: Get calls tryFallbackOnGetError for any
		// primary error, not only a not-found one. Only the sole consumer's own
		// additional check keeps that premise true today, so the gate is what
		// actually guarantees it here.
		//
		// Without it, a transient outage on a fallback (network timeout, DB
		// error, provider init failure) would mask the species for the full TTL
		// window, hiding real issues and suppressing legitimate retries.
		if anyFallbackTried && allFallbacksNotFound {
			c.recordSpeciesExhausted(scientificName)
		}
	}
	return foundImage, found
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

// MemoryUsage estimates the total memory usage of the cache's per-species maps:
// the image data map plus the three timestamp maps (exhausted species, and the
// two short-lived resolution markers).
//
// The in-flight bookkeeping maps (initializing, prefetching) are not counted;
// they hold only entries for work currently in progress.
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
	// Each timestamp map entry stores a time.Time (24 bytes on 64-bit). Use a
	// constant here to avoid an unsafe.Sizeof import for one constant value.
	const timeStructBytes = 24
	for _, markers := range []*sync.Map{&c.exhaustedSpecies, &c.recentAttempts, &c.dbAbsent} {
		markers.Range(func(key, value any) bool {
			if scientificName, ok := key.(string); ok {
				totalSize += len(scientificName)
			}
			totalSize += timeStructBytes
			return true
		})
	}
	return totalSize
}

// CreateDefaultCache creates a Wikimedia Commons BirdImageCache via the Wikipedia API.
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
	if cache == nil {
		return nil, fmt.Errorf("factory returned nil cache for provider %s", name)
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
