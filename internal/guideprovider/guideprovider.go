// Package guideprovider implements a two-tier (in-memory + database) cache of
// species guide information sourced from external providers (Wikipedia primary,
// eBird taxonomy enrichment). It provides stale-while-revalidate semantics,
// background refresh, and startup warming of the most-detected species.
package guideprovider

import (
	"context"
	"regexp"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Provider name constants, exported for wiring. They mirror the conf package
// values so callers can register providers without importing both packages.
const (
	WikipediaProviderName = conf.SpeciesGuideProviderWikipedia
	EBirdProviderName     = conf.SpeciesGuideProviderEBird
)

// Cache freshness and retention policy.
const (
	// PositiveTTL is how long a successfully-fetched guide stays fresh.
	PositiveTTL = 7 * 24 * time.Hour
	// NegativeTTL is how long a "not found" marker stays fresh before retrying.
	NegativeTTL = 30 * time.Minute
	// DBRetention is how long positive entries are kept in the DB before cleanup.
	DBRetention = 30 * 24 * time.Hour
	// NegativeDBRetention is how long negative (not-found) entries are kept in the
	// DB before cleanup. Far shorter than positive retention so that requests for
	// never-present species (e.g. a flood of distinct names) cannot accumulate
	// long-lived rows.
	NegativeDBRetention = 24 * time.Hour
	// refreshInterval is the background refresh loop cadence.
	refreshInterval = 2 * time.Hour
	// maxDescriptionLength caps stored descriptions (trimmed on a UTF-8 boundary).
	maxDescriptionLength = 10_000
	// defaultLocale is used when FetchOptions.Locale is empty or invalid.
	defaultLocale = "en"
	// maxMemoryEntries bounds the in-memory tier so an attacker passing many
	// distinct keys cannot grow it without limit. Once reached, new entries are
	// served from (and persisted to) the DB tier but not added to memory.
	maxMemoryEntries = 5000
)

// localePattern restricts locale codes to BCP-47-ish forms (e.g. "en", "pt-br").
// It bounds the cache key space and prevents arbitrary input from selecting a
// Wikipedia subdomain or inflating the cache. Invalid values fall back to "en".
var localePattern = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]{2,4})?$`)

// Cache tier labels for metrics.
const (
	tierMemory = "memory"
	tierDB     = "db"
)

// Guide quality labels for metrics (a coarse classification; the API layer
// computes its own user-facing quality classification independently).
const (
	qualityNegative  = "negative"
	qualityStub      = "stub"
	qualityIntroOnly = "intro_only"
	qualityFull      = "full"
)

// Provider fetch outcome labels for metrics.
const (
	outcomeSuccess   = "success"
	outcomeNotFound  = "not_found"
	outcomeError     = "error"
	outcomeTransient = "transient_error"
)

// Sentinel errors.
var (
	// ErrGuideNotFound indicates a provider definitively found nothing for a
	// species; the cache persists this as a (short-lived) negative entry.
	ErrGuideNotFound = errors.Newf("species guide not found").
				Component("guideprovider").
				Category(errors.CategoryNotFound).
				Build()
	// ErrCacheEntryNotFound indicates a store lookup found no row.
	ErrCacheEntryNotFound = errors.Newf("guide cache entry not found").
				Component("guideprovider").
				Category(errors.CategoryNotFound).
				Build()
	// ErrCacheUnavailable indicates the cache pointer is nil/unusable.
	ErrCacheUnavailable = errors.Newf("guide cache unavailable").
				Component("guideprovider").
				Category(errors.CategorySystem).
				Build()
)

// GetLogger returns the package logger scoped to the guideprovider module.
func GetLogger() logger.Logger {
	return logger.Global().Module("guideprovider")
}

// SimilarSpecies is a single related species reference within a guide.
type SimilarSpecies struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	Relationship   string `json:"relationship"` // same_genus | same_family | similar
}

// SpeciesGuide is the domain model returned to callers.
type SpeciesGuide struct {
	ScientificName string           `json:"scientific_name"`
	CommonName     string           `json:"common_name"`
	Description    string           `json:"description"` // may contain "## Section" markdown
	Genus          string           `json:"genus"`
	Family         string           `json:"family"`
	SourceProvider string           `json:"source_provider"`
	SourceURL      string           `json:"source_url"`
	License        string           `json:"license"`
	LicenseURL     string           `json:"license_url"`
	SimilarSpecies []SimilarSpecies `json:"similar_species,omitempty"`
	CachedAt       time.Time        `json:"cached_at"`
	Partial        bool             `json:"partial"`  // some providers failed; data may be incomplete
	Negative       bool             `json:"negative"` // provider found nothing
}

// IsNegativeEntry reports whether this guide is a negative (not-found) marker.
func (g *SpeciesGuide) IsNegativeEntry() bool { return g != nil && g.Negative }

// FetchOptions controls a provider fetch.
type FetchOptions struct {
	Locale string // BCP-47 / Wikipedia language code; drives locale-aware caching
}

// GuideProvider is a single source of guide data.
type GuideProvider interface {
	Name() string
	Fetch(ctx context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error)
}

// GuideStore is the persistence backend for the DB cache tier. The composite
// key is (scientificName, locale, provider).
type GuideStore interface {
	Get(ctx context.Context, scientificName, locale, provider string) (*GuideCacheEntry, error)
	Save(ctx context.Context, entry *GuideCacheEntry) error
	GetAll(ctx context.Context) ([]GuideCacheEntry, error)
	Delete(ctx context.Context, scientificName, locale, provider string) error
}

// GuideCacheMetrics is the metrics sink, implemented by observability/metrics.
type GuideCacheMetrics interface {
	RecordCacheHit(tier, quality string)
	RecordCacheMiss(tier string)
	RecordFetch(provider, outcome string, seconds float64)
	RecordDBError(errorType, operation string)
	RecordNegativeEntry()
	UpdateCachePopulationRatio(ratio float64)
}

// registeredProvider couples a provider with its registration name.
type registeredProvider struct {
	name     string
	provider GuideProvider
}

// GuideCache orchestrates the two-tier cache and provider fallback.
type GuideCache struct {
	memory    sync.Map     // key "scientificName|locale" -> *SpeciesGuide
	memCount  atomic.Int64 // approximate count of memory entries (soft cap guard)
	store     GuideStore
	metrics   GuideCacheMetrics
	providers []registeredProvider

	fallbackPolicy string // conf.SpeciesGuideFallback{All,None}
	warmTopN       int

	sf singleflight.Group

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// lifecycleMu serializes the closed-check + wg.Add in goIfOpen against
	// Close setting closed and calling wg.Wait, so a background spawn can never
	// race the Wait (which would let a goroutine outlive Close or panic the
	// WaitGroup).
	lifecycleMu sync.Mutex

	startOnce sync.Once
	closeOnce sync.Once
	closed    atomic.Bool
}

// NewGuideCache creates a cache backed by store and metrics. Register providers
// with RegisterProvider (registration order defines priority), then call Start.
func NewGuideCache(store GuideStore, metrics GuideCacheMetrics) *GuideCache {
	ctx, cancel := context.WithCancel(context.Background())
	return &GuideCache{
		store:          store,
		metrics:        metrics,
		fallbackPolicy: conf.SpeciesGuideFallbackAll,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// RegisterProvider adds a provider. The first registered provider is the primary
// (used as the DB composite-key provider and the merge base).
//
// Configuration methods (RegisterProvider, SetFallbackPolicy, SetWarmTopN) are
// NOT concurrency-safe and must all be called during setup, before Start() and
// before any Get(); they mutate state that concurrent reads do not lock.
func (c *GuideCache) RegisterProvider(name string, provider GuideProvider) {
	if c == nil || provider == nil || name == "" {
		return
	}
	c.providers = append(c.providers, registeredProvider{name: name, provider: provider})
}

// SetFallbackPolicy sets the provider fallback policy (all|none).
// Call before Start(); see RegisterProvider for the concurrency contract.
func (c *GuideCache) SetFallbackPolicy(policy string) {
	if c == nil || policy == "" {
		return
	}
	c.fallbackPolicy = policy
}

// SetWarmTopN records the configured warm target used for the population ratio.
// Call before Start(); see RegisterProvider for the concurrency contract.
func (c *GuideCache) SetWarmTopN(n int) {
	if c == nil {
		return
	}
	c.warmTopN = n
}

// storeMemory writes an entry to the memory tier with a soft size cap. Existing
// keys are always updated; new keys are only added while under maxMemoryEntries,
// so a flood of distinct keys cannot grow memory without bound (those entries
// still live in the DB tier).
func (c *GuideCache) storeMemory(key string, g *SpeciesGuide) {
	if _, loaded := c.memory.Load(key); loaded {
		c.memory.Store(key, g)
		return
	}
	if c.memCount.Load() >= maxMemoryEntries {
		return
	}
	if _, loaded := c.memory.LoadOrStore(key, g); loaded {
		c.memory.Store(key, g)
	} else {
		c.memCount.Add(1)
	}
}

// Start loads existing DB entries into memory and launches the refresh loop.
// Safe to call once; subsequent calls are no-ops.
func (c *GuideCache) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		c.loadFromDB()
		c.wg.Add(1)
		go c.startCacheRefresh()
	})
}

// Close cancels background work. Reads via Get remain safe afterwards.
// Safe to call multiple times.
func (c *GuideCache) Close() {
	if c == nil {
		return
	}
	c.closeOnce.Do(func() {
		c.lifecycleMu.Lock()
		c.closed.Store(true)
		c.lifecycleMu.Unlock()
		c.cancel()
		c.wg.Wait()
	})
}

// resolveProviderName returns the primary provider name (used for DB keys).
func (c *GuideCache) resolveProviderName() string {
	if len(c.providers) == 0 {
		return WikipediaProviderName
	}
	return c.providers[0].name
}

// loadFromDB populates the memory tier from all persisted entries.
func (c *GuideCache) loadFromDB() {
	if c.store == nil {
		return
	}
	entries, err := c.store.GetAll(c.ctx)
	if err != nil {
		c.metrics.RecordDBError("read", "get_all")
		GetLogger().Warn("Failed to load guide cache from DB", logger.Error(err))
		return
	}
	for i := range entries {
		g := entryToGuide(&entries[i])
		c.storeMemory(cacheKey(g.ScientificName, entries[i].Locale), g)
	}
	c.updateCachePopulationRatio()
	GetLogger().Debug("Loaded guide cache from DB", logger.Int("entries", len(entries)))
}

// Get returns a species guide using stale-while-revalidate semantics:
// memory tier, then DB tier (serving stale immediately and refreshing in the
// background), then a synchronous provider fetch on a miss.
func (c *GuideCache) Get(ctx context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error) {
	if c == nil {
		return nil, ErrCacheUnavailable
	}
	name := normalizeScientificName(scientificName)
	if name == "" {
		return nil, errors.Newf("empty scientific name").
			Component("guideprovider").
			Category(errors.CategoryValidation).
			Build()
	}
	locale := normalizeLocale(opts.Locale)
	opts.Locale = locale
	key := cacheKey(name, locale)

	// Tier 1: memory. Fresh entries are returned immediately; a stale entry is
	// served stale-while-revalidate (returned now, refreshed in the background)
	// so a stale memory hit doesn't incur a redundant DB round-trip on every call.
	if v, ok := c.memory.Load(key); ok {
		if g, ok := v.(*SpeciesGuide); ok {
			c.metrics.RecordCacheHit(tierMemory, entryQuality(g))
			if c.isCacheEntryStale(g) {
				c.triggerAsyncRefresh(name, locale)
			}
			return g, nil
		}
	}
	c.metrics.RecordCacheMiss(tierMemory)

	// Tier 2: DB.
	providerName := c.resolveProviderName()
	entry, err := c.store.Get(ctx, name, locale, providerName)
	switch {
	case err == nil && entry != nil:
		g := entryToGuide(entry)
		c.storeMemory(key, g)
		c.metrics.RecordCacheHit(tierDB, entryQuality(g))
		if c.isCacheEntryStale(g) {
			c.triggerAsyncRefresh(name, locale)
		}
		return g, nil
	case err != nil && !errors.Is(err, ErrCacheEntryNotFound):
		// DB error (not a clean miss): fall through to a live fetch without
		// recording a cache miss, so error and miss metrics stay distinct.
		c.metrics.RecordDBError("read", "get")
	default:
		// Clean miss (no row): record and fall through to a live fetch.
		c.metrics.RecordCacheMiss(tierDB)
	}

	// Tier 3: fetch from providers (singleflight collapses concurrent fetches).
	v, err, _ := c.sf.Do(key, func() (any, error) {
		return c.fetchAndStore(ctx, name, locale)
	})
	if err != nil {
		return nil, err
	}
	g, _ := v.(*SpeciesGuide)
	return g, nil
}

// fetchAndStore fetches from providers and persists the result (including
// negative entries). Transient errors are returned without being persisted.
func (c *GuideCache) fetchAndStore(ctx context.Context, name, locale string) (*SpeciesGuide, error) {
	g, err := c.fetchFromProviders(ctx, name, locale)
	if err != nil {
		if errors.Is(err, ErrGuideNotFound) {
			// Definitive not-found: persist a short-lived negative entry.
			neg := &SpeciesGuide{
				ScientificName: name,
				SourceProvider: c.resolveProviderName(),
				CachedAt:       time.Now(),
				Negative:       true,
			}
			c.metrics.RecordNegativeEntry()
			c.saveGuide(ctx, name, locale, neg)
			c.storeMemory(cacheKey(name, locale), neg)
			return neg, nil
		}
		// Transient/other errors: do not persist; surface to caller.
		return nil, err
	}
	c.saveGuide(ctx, name, locale, g)
	c.storeMemory(cacheKey(name, locale), g)
	return g, nil
}

// fetchFromProviders runs the configured provider(s) and merges results.
func (c *GuideCache) fetchFromProviders(ctx context.Context, name, locale string) (*SpeciesGuide, error) {
	if len(c.providers) == 0 {
		return nil, ErrCacheUnavailable
	}
	opts := FetchOptions{Locale: locale}

	var merged *SpeciesGuide
	var transient error
	failedCount := 0 // providers that failed for a non-definitive reason

	providers := c.providers
	if c.fallbackPolicy != conf.SpeciesGuideFallbackAll {
		providers = c.providers[:1] // primary only
	}

	for i := range providers {
		rp := providers[i]
		start := time.Now()
		g, err := rp.provider.Fetch(ctx, name, opts)
		elapsed := time.Since(start).Seconds()
		switch {
		case err == nil && g != nil:
			c.metrics.RecordFetch(rp.name, outcomeSuccess, elapsed)
			g.SourceProvider = c.resolveProviderName()
			g.ScientificName = name
			g.CachedAt = time.Now()
			g.Description = truncateDescription(g.Description)
			if merged == nil {
				merged = g
			} else {
				merged = mergeGuides(merged, g)
			}
		case errors.Is(err, ErrGuideNotFound):
			// A provider definitively having no entry is not a failure: an
			// enrichment-only provider (eBird taxonomy) lacks many species and
			// must not downgrade an otherwise-complete primary guide.
			c.metrics.RecordFetch(rp.name, outcomeNotFound, elapsed)
		case IsTransient(err):
			c.metrics.RecordFetch(rp.name, outcomeTransient, elapsed)
			transient = err
			failedCount++
		default:
			c.metrics.RecordFetch(rp.name, outcomeError, elapsed)
			failedCount++
			GetLogger().Debug("Provider fetch failed",
				logger.String("provider", rp.name),
				logger.String("species", name),
				logger.Error(err))
		}
	}

	if merged != nil {
		// Mark partial only when a provider genuinely failed (transient or
		// error), not when a secondary provider simply had no entry — otherwise
		// a complete Wikipedia guide would be flagged partial (and classified
		// "intro_only") whenever eBird lacks the species.
		if c.fallbackPolicy == conf.SpeciesGuideFallbackAll && failedCount > 0 {
			merged.Partial = true
		}
		merged.CachedAt = time.Now()
		return merged, nil
	}

	// No data. Prefer surfacing a transient error so we don't cache a negative
	// entry for a temporary outage.
	if transient != nil {
		return nil, transient
	}
	return nil, ErrGuideNotFound
}

// saveGuide persists a guide to the DB store, guarding against a closed cache.
func (c *GuideCache) saveGuide(ctx context.Context, name, locale string, g *SpeciesGuide) {
	if c == nil || c.store == nil || g == nil {
		return
	}
	entry := guideToEntry(name, locale, c.resolveProviderName(), g)
	if err := c.store.Save(ctx, entry); err != nil {
		c.metrics.RecordDBError("write", "save")
		GetLogger().Debug("Failed to save guide to DB",
			logger.String("species", name), logger.Error(err))
		return
	}
	c.updateCachePopulationRatio()
}

// isCacheEntryStale reports whether a guide needs refreshing. Negative entries
// have a much shorter TTL than positive ones.
func (c *GuideCache) isCacheEntryStale(g *SpeciesGuide) bool {
	if g == nil {
		return true
	}
	ttl := PositiveTTL
	if g.IsNegativeEntry() {
		ttl = NegativeTTL
	}
	return time.Since(g.CachedAt) > ttl
}

// goIfOpen starts fn on a wait-group-tracked background goroutine unless the
// cache is closed. The closed-check and wg.Add are serialized against Close
// under lifecycleMu, so once Close has flipped closed no new Add can race the
// wg.Wait that follows it.
func (c *GuideCache) goIfOpen(fn func()) {
	c.lifecycleMu.Lock()
	if c.closed.Load() {
		c.lifecycleMu.Unlock()
		return
	}
	c.wg.Add(1)
	c.lifecycleMu.Unlock()
	go func() {
		defer c.wg.Done()
		fn()
	}()
}

// triggerAsyncRefresh re-fetches a stale entry in the background.
func (c *GuideCache) triggerAsyncRefresh(name, locale string) {
	c.goIfOpen(func() {
		if c.shouldQuit() {
			return
		}
		_, _ = c.fetchAndStore(c.ctx, name, locale)
	})
}

// PreFetch fires a single fire-and-forget warm for a species (e.g. on a new
// detection). It never blocks the caller and is a no-op on a closed cache.
func (c *GuideCache) PreFetch(ctx context.Context, scientificName string) {
	if c == nil || c.closed.Load() {
		return
	}
	name := normalizeScientificName(scientificName)
	if name == "" {
		return
	}
	c.goIfOpen(func() {
		if c.shouldQuit() {
			return
		}
		// Use the caller context but fall back to the cache context for cancel.
		fetchCtx := ctx
		if fetchCtx == nil {
			fetchCtx = c.ctx
		}
		_, _ = c.Get(fetchCtx, name, FetchOptions{})
	})
}

// WarmForSpecies warms the cache for the given species in the background.
func (c *GuideCache) WarmForSpecies(speciesNames []string) {
	if c == nil || c.closed.Load() || len(speciesNames) == 0 {
		return
	}
	names := make([]string, 0, len(speciesNames))
	for _, n := range speciesNames {
		if nn := normalizeScientificName(n); nn != "" {
			names = append(names, nn)
		}
	}
	if len(names) == 0 {
		return
	}
	c.goIfOpen(func() {
		for _, n := range names {
			if c.shouldQuit() {
				return
			}
			_, _ = c.Get(c.ctx, n, FetchOptions{})
		}
		c.updateCachePopulationRatio()
	})
}

// startCacheRefresh runs the periodic stale-entry refresh loop until Close.
func (c *GuideCache) startCacheRefresh() {
	defer c.wg.Done()
	ticker := time.NewTicker(refreshInterval)
	defer ticker.Stop()
	for {
		select {
		case <-c.ctx.Done():
			return
		case <-ticker.C:
			c.refreshStaleEntries()
		}
	}
}

// refreshStaleEntries refreshes every stale entry currently in memory.
func (c *GuideCache) refreshStaleEntries() {
	type staleKey struct{ name, locale string }
	var stale []staleKey
	c.memory.Range(func(k, v any) bool {
		g, ok := v.(*SpeciesGuide)
		if !ok || !c.isCacheEntryStale(g) {
			return true
		}
		name, locale := splitCacheKey(k.(string))
		stale = append(stale, staleKey{name: name, locale: locale})
		return true
	})
	for _, s := range stale {
		if c.shouldQuit() {
			return
		}
		_, _ = c.fetchAndStore(c.ctx, s.name, s.locale)
	}

	// Opportunistic retention cleanup of long-expired DB rows.
	if cl, ok := c.store.(cleaner); ok && !c.shouldQuit() {
		if err := cl.Cleanup(c.ctx); err != nil {
			GetLogger().Debug("Guide cache cleanup failed", logger.Error(err))
		}
	}
}

// cleaner is an optional GuideStore capability for retention cleanup.
type cleaner interface {
	Cleanup(ctx context.Context) error
}

// updateCachePopulationRatio updates the population ratio gauge against WarmTopN.
func (c *GuideCache) updateCachePopulationRatio() {
	if c.warmTopN <= 0 {
		return
	}
	count := 0
	c.memory.Range(func(_, v any) bool {
		if g, ok := v.(*SpeciesGuide); ok && !g.IsNegativeEntry() {
			count++
		}
		return true
	})
	ratio := float64(count) / float64(c.warmTopN)
	if ratio > 1 {
		ratio = 1
	}
	c.metrics.UpdateCachePopulationRatio(ratio)
}

// shouldQuit reports whether background work should stop.
func (c *GuideCache) shouldQuit() bool {
	if c.closed.Load() {
		return true
	}
	select {
	case <-c.ctx.Done():
		return true
	default:
		return false
	}
}

// --- helpers ---

func cacheKey(name, locale string) string { return name + "|" + locale }

func splitCacheKey(key string) (name, locale string) {
	if i := strings.LastIndex(key, "|"); i >= 0 {
		return key[:i], key[i+1:]
	}
	return key, defaultLocale
}

// normalizeScientificName trims surrounding whitespace for consistent lookups.
func normalizeScientificName(name string) string {
	return strings.TrimSpace(name)
}

// normalizeLocale returns a validated, lowercased locale, defaulting to English
// for empty or non-conforming input. Validation bounds the cache key space and
// prevents arbitrary input from selecting a Wikipedia subdomain.
func normalizeLocale(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if l == "" || !localePattern.MatchString(l) {
		return defaultLocale
	}
	return l
}

// entryQuality classifies a guide for metrics labeling.
func entryQuality(g *SpeciesGuide) string {
	switch {
	case g.IsNegativeEntry():
		return qualityNegative
	case g.Partial || len(g.Description) < 200:
		if g.Description == "" {
			return qualityStub
		}
		return qualityIntroOnly
	default:
		return qualityFull
	}
}

// mergeGuides merges secondary into primary: primary wins on conflicts,
// secondary fills empty fields (e.g. eBird taxonomy on top of Wikipedia prose).
func mergeGuides(primary, secondary *SpeciesGuide) *SpeciesGuide {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	if primary.CommonName == "" {
		primary.CommonName = secondary.CommonName
	}
	if primary.Description == "" {
		primary.Description = secondary.Description
	}
	if primary.Genus == "" {
		primary.Genus = secondary.Genus
	}
	if primary.Family == "" {
		primary.Family = secondary.Family
	}
	if len(primary.SimilarSpecies) == 0 {
		primary.SimilarSpecies = secondary.SimilarSpecies
	}
	return primary
}

// truncateDescription caps a description at maxDescriptionLength, trimming on a
// UTF-8 boundary so the stored string is never split mid-rune.
func truncateDescription(s string) string {
	if len(s) <= maxDescriptionLength {
		return s
	}
	return trimToUTF8Boundary(s, maxDescriptionLength)
}

// trimToUTF8Boundary returns s[:n] backed off to the nearest valid UTF-8 rune
// boundary so no partial rune remains.
func trimToUTF8Boundary(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
