// Package guideprovider provides functionality for fetching and caching species guide text.
package guideprovider

import (
	"context"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"golang.org/x/sync/singleflight"
)

// Provider name constants.
const (
	WikipediaProviderName = "wikipedia" // Wikipedia REST API provider
	EBirdProviderName     = "ebird"     // eBird taxonomy enrichment provider
)

// Sentinel errors for guide operations.
var (
	// ErrGuideNotFound indicates the provider could not find guide data for the species.
	ErrGuideNotFound = errors.Newf("species guide not found").
				Component("guideprovider").
				Category(errors.CategoryNotFound).
				Context("error_type", "not_found").
				Build()

	// ErrProviderNotConfigured indicates the provider is disabled or missing credentials.
	ErrProviderNotConfigured = errors.Newf("guide provider not configured").
					Component("guideprovider").
					Category(errors.CategoryConfiguration).
					Context("error_type", "provider_not_configured").
					Build()

	// ErrAllProvidersUnavailable indicates all providers failed (circuit breakers open).
	ErrAllProvidersUnavailable = errors.Newf("all guide providers unavailable").
					Component("guideprovider").
					Category(errors.CategoryNetwork).
					Context("error_type", "all_unavailable").
					Build()
)

const (
	defaultCacheTTL     = 7 * 24 * time.Hour // 7 days for positive entries
	negativeCacheTTL    = 30 * time.Minute   // 30 minutes for negative entries
	refreshInterval     = 2 * time.Hour      // Check for stale entries every 2 hours
	refreshBatchSize    = 10                 // Number of entries to refresh in one batch
	refreshDelay        = 2 * time.Second    // Delay between refreshing individual entries
	negativeEntryMarker = "__NOT_FOUND__"    // Sentinel marker for negative cache entries

	fallbackPolicyAll = "all" // Fallback policy to try all providers

	providerTimeout = 10 * time.Second // Per-provider fetch timeout

	maxDescriptionLength = 2000 // Maximum description length to cache
)

// defaultProviderName is the provider used when settings are unavailable.
const defaultProviderName = WikipediaProviderName

// defaultFallbackOrder defines providers to try in order.
var defaultFallbackOrder = []string{WikipediaProviderName, EBirdProviderName}

// GetLogger returns the package logger for the guideprovider module.
func GetLogger() logger.Logger {
	return logger.Global().Module("guideprovider")
}

// SpeciesGuide represents cached species guide text with metadata and attribution.
type SpeciesGuide struct {
	ScientificName     string    // Lookup key, e.g. "Turdus merula"
	CommonName         string    // e.g. "Common Blackbird"
	Description        string    // Wikipedia extract (plain text)
	ConservationStatus string    // e.g. "Least Concern" (if available)
	SourceProvider     string    // Which provider supplied this data
	SourceURL          string    // Attribution link
	LicenseName        string    // e.g. "CC BY-SA 4.0"
	LicenseURL         string    // URL to the license text
	CachedAt           time.Time // When this entry was cached
	Partial            bool      // True if only some fields populated
}

// IsNegativeEntry checks if this is a negative cache entry (not found).
func (g *SpeciesGuide) IsNegativeEntry() bool {
	return g.SourceProvider == negativeEntryMarker
}

// GuideProvider defines the interface for fetching species guide text.
type GuideProvider interface {
	// Fetch retrieves guide information for a species by scientific name.
	// Returns a partial SpeciesGuide if some fields are unavailable.
	// Returns ErrGuideNotFound if the species cannot be found at all.
	Fetch(ctx context.Context, scientificName string) (SpeciesGuide, error)
}

// GuideStore defines the datastore interface needed by the guide cache.
type GuideStore interface {
	GetGuideCache(ctx context.Context, scientificName, providerName string) (*GuideCacheEntry, error)
	SaveGuideCache(ctx context.Context, entry *GuideCacheEntry) error
	GetAllGuideCaches(ctx context.Context, providerName string) ([]GuideCacheEntry, error)
}

// GuideCacheEntry represents a guide cache entry in the database.
type GuideCacheEntry struct {
	ID                 uint      `gorm:"primaryKey"`
	ProviderName       string    `gorm:"uniqueIndex:idx_guidecache_provider_species;size:50;not null;default:wikipedia"`
	ScientificName     string    `gorm:"uniqueIndex:idx_guidecache_provider_species;not null"`
	SourceProvider     string    `gorm:"size:50;not null;default:wikipedia"`
	CommonName         string    `gorm:"size:200"`
	Description        string    `gorm:"type:text"`
	ConservationStatus string    `gorm:"size:100"`
	SourceURL          string    `gorm:"size:2048"`
	LicenseName        string    `gorm:"size:200"`
	LicenseURL         string    `gorm:"size:2048"`
	CachedAt           time.Time `gorm:"index"`
}

// TableName returns the table name for GORM.
func (GuideCacheEntry) TableName() string {
	return "guide_caches"
}

// GuideCache manages species guide data with a two-tier cache (memory + database).
type GuideCache struct {
	providers map[string]GuideProvider
	dataMap   sync.Map
	store     GuideStore
	sfGroup   singleflight.Group
	quit      chan struct{}
	mu        sync.RWMutex // protects providers map
}

// NewGuideCache creates a new GuideCache with the given store.
func NewGuideCache(store GuideStore) *GuideCache {
	return &GuideCache{
		providers: make(map[string]GuideProvider),
		store:     store,
		quit:      make(chan struct{}),
	}
}

// RegisterProvider adds a named provider to the cache.
func (c *GuideCache) RegisterProvider(name string, provider GuideProvider) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.providers[name] = provider
}

// Start begins the background cache refresh routine.
func (c *GuideCache) Start() {
	c.loadFromDB()
	c.startCacheRefresh()
}

// Close stops background routines.
func (c *GuideCache) Close() {
	close(c.quit)
}

// Get retrieves a species guide, checking memory cache, DB cache, and providers.
func (c *GuideCache) Get(ctx context.Context, scientificName string) (*SpeciesGuide, error) {
	// Tier 1: Memory cache
	if cached, ok := c.dataMap.Load(scientificName); ok {
		guide := cached.(*SpeciesGuide)
		if guide.IsNegativeEntry() {
			if !isCacheEntryStale(guide.CachedAt, true) {
				return nil, ErrGuideNotFound
			}
			// Stale negative entry, fall through to re-fetch
		} else if !isCacheEntryStale(guide.CachedAt, false) {
			return guide, nil
		}
		// Stale positive entry, fall through to re-fetch
	}

	// Tier 2: DB cache
	if c.store != nil {
		settings := conf.GetSettings()
		providerName := defaultProviderName
		if settings != nil {
			providerName = settings.Realtime.Dashboard.SpeciesGuide.Provider
		}
		entry, err := c.store.GetGuideCache(ctx, scientificName, providerName)
		if err == nil && entry != nil {
			guide := dbEntryToGuide(entry)
			if !isCacheEntryStale(guide.CachedAt, guide.IsNegativeEntry()) {
				c.dataMap.Store(scientificName, guide)
				if guide.IsNegativeEntry() {
					return nil, ErrGuideNotFound
				}
				return guide, nil
			}
		}
	}

	// Tier 3: Fetch from providers (deduplicated)
	result, err, _ := c.sfGroup.Do(scientificName, func() (any, error) {
		return c.fetchFromProviders(ctx, scientificName)
	})
	if err != nil {
		return nil, err
	}

	guide := result.(*SpeciesGuide)
	return guide, nil
}

// fetchFromProviders fetches guide data from configured providers with fallback.
func (c *GuideCache) fetchFromProviders(ctx context.Context, scientificName string) (*SpeciesGuide, error) {
	log := GetLogger()
	settings := conf.GetSettings()

	primaryProvider := defaultProviderName
	fallbackPolicy := fallbackPolicyAll
	if settings != nil {
		primaryProvider = settings.Realtime.Dashboard.SpeciesGuide.Provider
		fallbackPolicy = settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy
	}

	c.mu.RLock()
	provider, hasPrimary := c.providers[primaryProvider]
	c.mu.RUnlock()

	var primaryResult *SpeciesGuide

	// Try primary provider
	if hasPrimary {
		providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
		guide, err := provider.Fetch(providerCtx, scientificName)
		cancel()

		if err == nil {
			primaryResult = &guide
			log.Debug("Primary provider returned guide",
				logger.String("provider", primaryProvider),
				logger.String("species", scientificName),
				logger.Bool("partial", guide.Partial))
		} else if !errors.Is(err, ErrGuideNotFound) {
			log.Warn("Primary provider failed",
				logger.String("provider", primaryProvider),
				logger.String("species", scientificName),
				logger.Any("error", err))
		}
	}

	// Try fallback providers if policy allows
	if fallbackPolicy == fallbackPolicyAll {
		c.mu.RLock()
		providers := make(map[string]GuideProvider, len(c.providers))
		for k, v := range c.providers {
			providers[k] = v
		}
		c.mu.RUnlock()

		for _, name := range defaultFallbackOrder {
			if name == primaryProvider {
				continue
			}
			fbProvider, ok := providers[name]
			if !ok {
				continue
			}

			providerCtx, cancel := context.WithTimeout(ctx, providerTimeout)
			guide, err := fbProvider.Fetch(providerCtx, scientificName)
			cancel()

			if err != nil {
				continue
			}

			if primaryResult == nil {
				primaryResult = &guide
			} else {
				merged := mergeGuides(*primaryResult, guide)
				primaryResult = &merged
			}
		}
	}

	// Cache the result
	if primaryResult != nil {
		primaryResult.CachedAt = time.Now()
		c.dataMap.Store(scientificName, primaryResult)
		c.saveToDB(primaryResult, primaryProvider)
		return primaryResult, nil
	}

	// All providers failed: cache negative entry
	negative := &SpeciesGuide{
		ScientificName: scientificName,
		SourceProvider: negativeEntryMarker,
		CachedAt:       time.Now(),
	}
	c.dataMap.Store(scientificName, negative)
	c.saveToDB(negative, primaryProvider)
	return nil, ErrGuideNotFound
}

// mergeGuides merges two guide results, with primary taking precedence.
func mergeGuides(primary, secondary SpeciesGuide) SpeciesGuide {
	result := primary
	if result.Description == "" {
		result.Description = secondary.Description
	}
	if result.CommonName == "" {
		result.CommonName = secondary.CommonName
	}
	if result.ConservationStatus == "" {
		result.ConservationStatus = secondary.ConservationStatus
	}
	if result.SourceURL == "" {
		result.SourceURL = secondary.SourceURL
	}
	result.Partial = result.Description == ""
	return result
}

// isCacheEntryStale checks if a cache entry has exceeded its TTL.
func isCacheEntryStale(cachedAt time.Time, isNegative bool) bool {
	ttl := defaultCacheTTL
	if isNegative {
		ttl = negativeCacheTTL
	}
	return time.Now().After(cachedAt.Add(ttl))
}

// dbEntryToGuide converts a database cache entry to a SpeciesGuide.
func dbEntryToGuide(entry *GuideCacheEntry) *SpeciesGuide {
	return &SpeciesGuide{
		ScientificName:     entry.ScientificName,
		CommonName:         entry.CommonName,
		Description:        entry.Description,
		ConservationStatus: entry.ConservationStatus,
		SourceProvider:     entry.SourceProvider,
		SourceURL:          entry.SourceURL,
		LicenseName:        entry.LicenseName,
		LicenseURL:         entry.LicenseURL,
		CachedAt:           entry.CachedAt,
		Partial:            entry.Description == "",
	}
}

// saveToDB persists a guide entry to the database.
func (c *GuideCache) saveToDB(guide *SpeciesGuide, providerName string) {
	if c.store == nil {
		return
	}

	entry := &GuideCacheEntry{
		ProviderName:       providerName,
		ScientificName:     guide.ScientificName,
		SourceProvider:     guide.SourceProvider,
		CommonName:         guide.CommonName,
		Description:        guide.Description,
		ConservationStatus: guide.ConservationStatus,
		SourceURL:          guide.SourceURL,
		LicenseName:        guide.LicenseName,
		LicenseURL:         guide.LicenseURL,
		CachedAt:           guide.CachedAt,
	}

	if err := c.store.SaveGuideCache(context.Background(), entry); err != nil {
		GetLogger().Warn("Failed to save guide cache to database",
			logger.String("species", guide.ScientificName),
			logger.Any("error", err))
	}
}

// loadFromDB loads all cached entries from the database into memory.
func (c *GuideCache) loadFromDB() {
	if c.store == nil {
		return
	}

	settings := conf.GetSettings()
	providerName := defaultProviderName
	if settings != nil {
		providerName = settings.Realtime.Dashboard.SpeciesGuide.Provider
	}

	entries, err := c.store.GetAllGuideCaches(context.Background(), providerName)
	if err != nil {
		GetLogger().Warn("Failed to load guide caches from database",
			logger.Any("error", err))
		return
	}

	loaded := 0
	for i := range entries {
		guide := dbEntryToGuide(&entries[i])
		c.dataMap.Store(entries[i].ScientificName, guide)
		loaded++
	}

	GetLogger().Info("Loaded guide cache entries from database",
		logger.Int("count", loaded))
}

// startCacheRefresh starts the background cache refresh routine.
func (c *GuideCache) startCacheRefresh() {
	log := GetLogger()
	log.Info("Starting guide cache refresh routine",
		logger.Duration("ttl", defaultCacheTTL),
		logger.Duration("interval", refreshInterval))

	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-c.quit:
				log.Info("Stopping guide cache refresh routine")
				return
			case <-ticker.C:
				c.refreshStaleEntries()
			}
		}
	}()
}

// refreshStaleEntries refreshes cache entries that have exceeded their TTL.
func (c *GuideCache) refreshStaleEntries() {
	if c.store == nil {
		return
	}

	log := GetLogger()
	settings := conf.GetSettings()
	providerName := defaultProviderName
	if settings != nil {
		providerName = settings.Realtime.Dashboard.SpeciesGuide.Provider
	}

	entries, err := c.store.GetAllGuideCaches(context.Background(), providerName)
	if err != nil {
		log.Warn("Failed to get guide caches for refresh", logger.Any("error", err))
		return
	}

	var staleEntries []string
	for i := range entries {
		isNegative := entries[i].SourceProvider == negativeEntryMarker
		if isCacheEntryStale(entries[i].CachedAt, isNegative) {
			staleEntries = append(staleEntries, entries[i].ScientificName)
		}
	}

	if len(staleEntries) == 0 {
		return
	}

	log.Info("Found stale guide cache entries to refresh",
		logger.Int("count", len(staleEntries)))

	ctx := context.Background()
	refreshed := 0
	for i, name := range staleEntries {
		if c.shouldQuit() {
			break
		}
		if i > 0 && i%refreshBatchSize == 0 {
			if c.waitWithQuit(refreshDelay) {
				break
			}
		}

		if _, err := c.fetchFromProviders(ctx, name); err == nil {
			refreshed++
		}
	}

	log.Info("Finished refreshing stale guide entries",
		logger.Int("refreshed", refreshed),
		logger.Int("total", len(staleEntries)))
}

// shouldQuit checks if the cache's quit channel has been signaled.
func (c *GuideCache) shouldQuit() bool {
	select {
	case <-c.quit:
		return true
	default:
		return false
	}
}

// waitWithQuit waits for the specified duration, returning true if quit was signaled.
func (c *GuideCache) waitWithQuit(d time.Duration) bool {
	timer := time.NewTimer(d)
	select {
	case <-c.quit:
		timer.Stop()
		return true
	case <-timer.C:
		return false
	}
}
