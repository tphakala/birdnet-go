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
	OpenFaunaProviderName = conf.SpeciesGuideProviderOpenFauna
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

// localePattern restricts locale codes to BCP-47-ish forms (e.g. "en", "pt-br",
// "be-tarask", "zh-min-nan"). It allows a 2–3 letter primary subtag followed by
// up to two "-subtag" parts of 2–10 lowercase letters each, which covers the
// non-standard Wikipedia language subdomains (e.g. zh-classical) that the older,
// tighter pattern silently dropped to "en". It still admits only values that form
// a "<locale>.wikipedia.org" host and bounds the cache key space; anything else
// (underscores, dots, paths, host-injection) falls back to "en".
var localePattern = regexp.MustCompile(`^[a-z]{2,3}(-[a-z]{2,10}){0,2}$`)

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
	// GetRecent returns up to limit entries, most-recently-cached first. The warm
	// load uses it instead of GetAll to bound the startup result set (rows are
	// capped only by time-based retention, so a flood of short-lived negative
	// entries could otherwise materialize a very large slice at boot).
	GetRecent(ctx context.Context, limit int) ([]GuideCacheEntry, error)
	Delete(ctx context.Context, scientificName, locale, provider string) error
	// DeleteAll removes every cached entry. It is used to invalidate the whole
	// cache when the registered provider set changes (e.g. the user toggles
	// Wikipedia descriptions), so guides produced under the old set are re-fetched
	// rather than served stale until their TTL expires.
	DeleteAll(ctx context.Context) error
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
	memory sync.Map // key "scientificName|locale" -> *SpeciesGuide
	// memWriteMu serializes structural writes to the memory tier — every operation
	// that inserts/removes a key AND adjusts memCount (storeMemory, storeMemoryGen,
	// InvalidateAll's sweep, the stale-refresh eviction). It does NOT guard reads:
	// Get's memory.Load stays lock-free (sync.Map is safe for concurrent reads
	// during writes), so the hot path is unaffected. Serializing writers is what
	// keeps memCount an exact count of live entries — a purely lock-free scheme
	// cannot, because a concurrent writer can replace a just-inserted key between
	// the insert and its cap-rollback, stranding an uncounted entry.
	memWriteMu sync.Mutex
	memCount   atomic.Int64 // count of memory entries (hard cap guard; written under memWriteMu)
	// invalidateGen is bumped by InvalidateAll. A Tier-2 DB rehydrate captures it
	// before reading and drops its write if it changed, so a pre-invalidation row
	// read cannot re-seed memory after InvalidateAll's sweep has passed.
	invalidateGen atomic.Int64
	store         GuideStore
	metrics       GuideCacheMetrics
	providers     []registeredProvider

	fallbackPolicy string // conf.SpeciesGuideFallback{All,None}
	warmTopN       int
	// warmLocale is the dashboard locale that cache warming (startup WarmForSpecies +
	// per-detection PreFetch) targets, so warmed entries key to the same locale the UI
	// requests. Set once via SetWarmLocale before Start() and treated as immutable for
	// the cache's lifetime (a reconfigure rebuilds the cache), so background warm
	// goroutines read it without a lock. Empty falls back to defaultLocale.
	warmLocale string

	sf singleflight.Group

	ctx    context.Context
	cancel context.CancelFunc
	wg     sync.WaitGroup

	// lifecycleMu serializes the closed-check + wg.Add in goIfOpen against
	// Close setting closed and calling wg.Wait, so a background spawn can never
	// race the Wait (which would let a goroutine outlive Close or panic the
	// WaitGroup).
	lifecycleMu sync.Mutex

	// providersMu guards provider use against provider teardown. fetchFromProviders
	// holds it for reading while it calls into the providers; Close takes it for
	// writing before releasing per-provider resources. The synchronous Tier-3 fetch
	// runs on a singleflight-spawned goroutine that the wait group does not track,
	// so wg.Wait alone cannot guarantee no fetch is still calling a provider when
	// Close releases its resources — this lock closes that window.
	providersMu sync.RWMutex

	startOnce sync.Once
	closeOnce sync.Once
	closed    atomic.Bool
}

// NewGuideCache creates a cache backed by store and metrics. Register providers
// with RegisterProvider (registration order defines priority), then call Start.
func NewGuideCache(store GuideStore, metrics GuideCacheMetrics) *GuideCache {
	ctx, cancel := context.WithCancel(context.Background())
	if metrics == nil {
		metrics = noopMetrics{}
	}
	return &GuideCache{
		store:          store,
		metrics:        metrics,
		fallbackPolicy: conf.SpeciesGuideFallbackAll,
		ctx:            ctx,
		cancel:         cancel,
	}
}

// noopMetrics is a GuideCacheMetrics sink that discards everything. It is the
// default when NewGuideCache is constructed without a metrics implementation
// (e.g. in tests), so the cache never has to nil-check the sink on the hot path.
type noopMetrics struct{}

func (noopMetrics) RecordCacheHit(_, _ string)           {}
func (noopMetrics) RecordCacheMiss(_ string)             {}
func (noopMetrics) RecordFetch(_, _ string, _ float64)   {}
func (noopMetrics) RecordDBError(_, _ string)            {}
func (noopMetrics) RecordNegativeEntry()                 {}
func (noopMetrics) UpdateCachePopulationRatio(_ float64) {}

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

// SetWarmLocale records the dashboard locale that startup warming and per-detection
// pre-fetch should target, so warmed entries key to the locale the UI will request
// rather than the default "en". Call before Start(); see RegisterProvider for the
// concurrency contract.
func (c *GuideCache) SetWarmLocale(locale string) {
	if c == nil {
		return
	}
	c.warmLocale = locale
}

// storeMemory writes an entry to the memory tier with a hard size cap. Existing
// keys are always updated; new keys are only added while under maxMemoryEntries,
// so a flood of distinct keys cannot grow memory without bound (those entries
// still live in the DB tier).
func (c *GuideCache) storeMemory(key string, g *SpeciesGuide) {
	c.memWriteMu.Lock()
	defer c.memWriteMu.Unlock()
	c.storeMemoryLocked(key, g)
}

// storeMemoryLocked performs the insert-or-update; the caller MUST hold memWriteMu.
// Because writers are serialized, the Load/Store/count sequence is atomic against
// every other structural writer, so memCount stays an exact count of live entries
// (no lost updates, no stranded uncounted entries). An update-in-place leaves the
// count unchanged; a new key is admitted only while under the cap (overflow stays
// in the DB tier).
func (c *GuideCache) storeMemoryLocked(key string, g *SpeciesGuide) {
	if _, loaded := c.memory.Load(key); loaded {
		c.memory.Store(key, g) // update in place: the slot is already counted
		return
	}
	if c.memCount.Load() >= maxMemoryEntries {
		return // at cap: do not admit a new key
	}
	c.memory.Store(key, g)
	c.memCount.Add(1)
}

// storeMemoryGen writes like storeMemory but undoes the write if InvalidateAll ran
// since gen was captured (i.e. the value derives from a pre-invalidation read). The
// store-then-verify ordering closes the window in both directions: if the sweep
// already passed this key, the post-store generation check sees the bump and removes
// the entry; if the sweep runs after our store, it removes the entry itself. Used by
// the Tier-2 DB rehydrate in Get, the only path that can resurrect stale content
// (Tier-3 fetches run under the current provider set, so their writes are fresh).
//
// The whole insert-then-verify runs under memWriteMu so the generation check and the
// possible rollback are atomic with the insert — no concurrent writer can replace
// the value between them and desync the count.
//
// The rollback's memCount.Add(-1) is guarded by LoadAndDelete's loaded, so it
// decrements ONLY when it actually removes an entry. storeMemoryLocked is the sole
// inserter and increments once per new key, so the invariant "every live entry is
// counted exactly once" holds; removing an entry therefore always warrants one
// decrement, whether this call newly inserted it or updated a pre-existing (already
// counted) key. Making the decrement conditional on "did THIS call insert" would be
// wrong: deleting a pre-existing entry without decrementing would OVERcount.
func (c *GuideCache) storeMemoryGen(key string, g *SpeciesGuide, gen int64) {
	c.memWriteMu.Lock()
	defer c.memWriteMu.Unlock()
	c.storeMemoryLocked(key, g)
	if c.invalidateGen.Load() != gen {
		if _, loaded := c.memory.LoadAndDelete(key); loaded {
			c.memCount.Add(-1)
		}
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
		// Release per-provider resources for any provider implementing an optional
		// Close. Without this a hot-reload that rebuilds the cache would leak the
		// previous providers' resources. Done after wg.Wait, and under the
		// providers write lock so an untracked singleflight Tier-3 fetch (which
		// holds the read lock around provider.Fetch) cannot still be using a
		// provider when its resources are released.
		c.providersMu.Lock()
		for i := range c.providers {
			if closer, ok := c.providers[i].provider.(interface{ Close() error }); ok {
				_ = closer.Close()
			}
		}
		c.providersMu.Unlock()
	})
}

// resolveProviderName returns the primary provider name (used for DB keys).
func (c *GuideCache) resolveProviderName() string {
	if len(c.providers) == 0 {
		return WikipediaProviderName
	}
	return c.providers[0].name
}

// HasProvider reports whether a provider with the given registration name is
// registered. The provider set is established during setup (RegisterProvider)
// before Start and is not mutated afterwards, so this reads without a lock,
// mirroring resolveProviderName.
func (c *GuideCache) HasProvider(name string) bool {
	if c == nil {
		return false
	}
	for i := range c.providers {
		if c.providers[i].name == name {
			return true
		}
	}
	return false
}

// InvalidateAll clears every cached guide from both the memory and DB tiers. It is
// used on reconfiguration when the registered provider set changes, so guides
// produced under the previous providers are re-fetched under the new set instead of
// being served stale until their TTL expires. Background warming (when configured)
// re-populates the hottest species immediately afterward.
func (c *GuideCache) InvalidateAll(ctx context.Context) error {
	if c == nil {
		return nil
	}
	// Bump the generation up front, before clearing either tier. A concurrent Get
	// may have read a pre-invalidation row from the DB tier and be about to write it
	// into memory after the sweep below passes that key; the rehydrate path captures
	// this generation before its read and drops such a write, so the "full"
	// invalidate cannot leave a stale entry behind once it returns.
	c.invalidateGen.Add(1)
	// Clear the persistent tier first. If it fails we leave the memory tier intact
	// and return the error, so the two tiers stay consistent (both populated) rather
	// than ending up memory-empty while stale rows survive in the DB to reload on the
	// next restart. The caller logs the failure; the stale content then simply ages
	// out on its normal TTL, the same outcome as if invalidation had not run.
	if c.store != nil {
		if err := c.store.DeleteAll(ctx); err != nil {
			return err
		}
	}
	// Persistent tier is clear (or absent): drop the memory tier, keeping memCount
	// accurate by decrementing per removal. Under memWriteMu so the sweep can't
	// race a concurrent storeMemory into an inexact count.
	c.memWriteMu.Lock()
	c.memory.Range(func(k, _ any) bool {
		if _, loaded := c.memory.LoadAndDelete(k); loaded {
			c.memCount.Add(-1)
		}
		return true
	})
	c.memWriteMu.Unlock()
	c.updateCachePopulationRatio()
	return nil
}

// loadFromDB populates the memory tier from all persisted entries.
func (c *GuideCache) loadFromDB() {
	if c.store == nil {
		return
	}
	// Bound the warm load to the in-memory cap, freshest first. storeMemory
	// discards anything beyond maxMemoryEntries anyway, so reading more is wasted
	// work, and an unbounded read could materialize a large transient slice when
	// many short-lived negative rows have accrued since the last retention cleanup.
	entries, err := c.store.GetRecent(c.ctx, maxMemoryEntries)
	if err != nil {
		c.metrics.RecordDBError("read", "get_recent")
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

	// Tier 2: DB. Skipped when no store is wired — the memory and provider tiers
	// still function, and guarding here (mirroring the nil-store checks in
	// loadFromDB and saveGuide) prevents a nil dereference if a cache is ever
	// constructed without a store. Production wiring always supplies one.
	if c.store != nil {
		providerName := c.resolveProviderName()
		// Capture the invalidation generation before the read so a row fetched just
		// before a concurrent InvalidateAll is not rehydrated into memory after its
		// sweep (see storeMemoryGen).
		gen := c.invalidateGen.Load()
		entry, err := c.store.Get(ctx, name, locale, providerName)
		switch {
		case err == nil && entry != nil:
			g := entryToGuide(entry)
			c.storeMemoryGen(key, g, gen)
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
	}

	// Tier 3: fetch from providers (singleflight collapses concurrent fetches).
	// The shared fetch runs on the cache's background context, not the caller's,
	// so one caller cancelling (e.g. a closed browser tab) cannot abort the fetch
	// for the other callers sharing this singleflight execution. Each caller still
	// honours its own deadline via the select below, and the detached fetch
	// completes and populates the cache for everyone.
	ch := c.sf.DoChan(key, func() (any, error) {
		return c.fetchAndStore(c.ctx, name, locale)
	})
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case res := <-ch:
		if res.Err != nil {
			return nil, res.Err
		}
		g, _ := res.Val.(*SpeciesGuide)
		return g, nil
	}
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
	// Hold the read lock for the whole provider loop so Close cannot release a
	// provider's resources mid-fetch. Concurrent fetches share the read lock;
	// only provider teardown (Close) contends. Close cancels c.ctx first, so any
	// in-flight provider.Fetch here observes cancellation and returns promptly
	// rather than blocking shutdown.
	c.providersMu.RLock()
	defer c.providersMu.RUnlock()
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
			// SourceProvider is the cache's canonical/primary provider (it also keys
			// the DB row), not necessarily the origin of every field. Licensing
			// attribution for the description rides on SourceURL/License/LicenseURL,
			// which mergeGuides carries from whichever provider supplied the prose
			// (e.g. Wikipedia under an OpenFauna-primary setup) — so the displayed
			// source link and license stay correct even when this label is the primary.
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
	// A provider failed for a non-definitive, non-transient reason (e.g. a 4xx
	// other than 404 such as a 403 UA rejection, or a response-decode error).
	// Surface a non-NotFound error so fetchAndStore does NOT persist a 30-minute
	// negative entry for a species that may well exist — the next request retries
	// instead of being suppressed. Only a clean not-found (no failures at all)
	// should become a negative entry.
	if failedCount > 0 {
		return nil, errors.Newf("all guide providers failed for %q", name).
			Component("guideprovider").
			Category(errors.CategoryHTTP).
			Build()
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
	// The population-ratio gauge is intentionally NOT updated here: it would cost
	// a full memory-map scan on every cache write (O(n) per save, O(n^2) during
	// warm-up and under pre-fetch load). It is recomputed at startup, after warm
	// completes, and once per refresh cycle (refreshStaleEntries) instead.
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

// triggerAsyncRefresh re-fetches a stale entry in the background. The fetch is
// routed through the singleflight group keyed on the cache key (the same key the
// synchronous Tier-3 path uses), so a burst of concurrent requests for one stale
// species collapses to a single provider fetch instead of spawning a redundant
// external call per request (thundering-herd guard).
func (c *GuideCache) triggerAsyncRefresh(name, locale string) {
	key := cacheKey(name, locale)
	c.goIfOpen(func() {
		if c.shouldQuit() {
			return
		}
		_, _, _ = c.sf.Do(key, func() (any, error) {
			return c.fetchAndStore(c.ctx, name, locale)
		})
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
		// Warm the dashboard locale (not the default), so the pre-fetched entry keys to
		// the locale the UI actually requests.
		_, _ = c.Get(fetchCtx, name, FetchOptions{Locale: c.warmLocale})
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
			// Warm the dashboard locale (not the default) so the warmed entries key to
			// the locale the UI will request.
			_, _ = c.Get(c.ctx, n, FetchOptions{Locale: c.warmLocale})
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

// refreshStaleEntries refreshes stale positive entries in memory and evicts stale
// negative ones. Positive entries are re-fetched in place so the warm cache stays
// fresh; expired negative (not-found) entries are dropped from memory rather than
// perpetually re-fetched, so a flood of distinct never-present names cannot pin
// memory slots or re-hit providers every cycle. Evicted negatives are recreated
// on demand if requested again.
func (c *GuideCache) refreshStaleEntries() {
	type staleKey struct{ name, locale string }
	var stale []staleKey
	var evict []string
	c.memory.Range(func(k, v any) bool {
		g, ok := v.(*SpeciesGuide)
		if !ok || !c.isCacheEntryStale(g) {
			return true
		}
		key := k.(string)
		if g.IsNegativeEntry() {
			evict = append(evict, key)
			return true
		}
		name, locale := splitCacheKey(key)
		stale = append(stale, staleKey{name: name, locale: locale})
		return true
	})
	if len(evict) > 0 {
		// Under memWriteMu so eviction can't race a concurrent storeMemory into an
		// inexact count.
		c.memWriteMu.Lock()
		for _, key := range evict {
			// Re-check under the lock before deleting: a concurrent fetchAndStore may
			// have replaced this stale negative with a fresh positive between the
			// lock-free Range scan above and here. Only evict when the CURRENT value is
			// still a stale negative, so we never drop a freshly-stored positive by
			// acting on a stale snapshot. (storeMemoryLocked also holds memWriteMu, so
			// this Load/LoadAndDelete pair cannot race a structural write.)
			cur, ok := c.memory.Load(key)
			if !ok {
				continue
			}
			g, isGuide := cur.(*SpeciesGuide)
			if !isGuide || !g.IsNegativeEntry() || !c.isCacheEntryStale(g) {
				continue
			}
			if _, loaded := c.memory.LoadAndDelete(key); loaded {
				c.memCount.Add(-1)
			}
		}
		c.memWriteMu.Unlock()
	}
	for _, s := range stale {
		if c.shouldQuit() {
			return
		}
		// Route the background refresh through the same singleflight group (keyed on
		// the cache key) as triggerAsyncRefresh and the synchronous Tier-3 path, so a
		// periodic refresh that coincides with a user-triggered fetch for the same
		// species collapses to one provider call instead of a redundant external hit.
		key := cacheKey(s.name, s.locale)
		_, _, _ = c.sf.Do(key, func() (any, error) {
			return c.fetchAndStore(c.ctx, s.name, s.locale)
		})
	}

	// Opportunistic retention cleanup of long-expired DB rows.
	if cl, ok := c.store.(cleaner); ok && !c.shouldQuit() {
		if err := cl.Cleanup(c.ctx); err != nil {
			GetLogger().Debug("Guide cache cleanup failed", logger.Error(err))
		}
	}

	// Refresh the population-ratio gauge once per cycle. This is the home for the
	// O(n) scan that used to run on every cache write (see saveGuide); doing it
	// here keeps the gauge current as pre-fetch adds entries without taxing the
	// write path.
	c.updateCachePopulationRatio()
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

// mergeGuides merges secondary into primary: primary wins on conflicts, secondary
// fills empty fields. With OpenFauna primary and Wikipedia secondary, OpenFauna's
// taxonomy and localized common name win, while Wikipedia fills the description.
//
// The source URL and license travel with the description: when the primary lacks
// them (OpenFauna sets no source/license), they are taken from the secondary so the
// Wikipedia prose keeps its CC BY-SA attribution (URL + license) in the merged and
// persisted guide.
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
	// Attribution for the prose: fill from the secondary when the primary has none.
	if primary.SourceURL == "" {
		primary.SourceURL = secondary.SourceURL
	}
	if primary.License == "" {
		primary.License = secondary.License
	}
	if primary.LicenseURL == "" {
		primary.LicenseURL = secondary.LicenseURL
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
	return TrimToUTF8Boundary(s, maxDescriptionLength)
}

// TrimToUTF8Boundary returns s[:n] backed off to the nearest valid UTF-8 rune
// boundary so no partial rune remains.
func TrimToUTF8Boundary(s string, n int) string {
	if n >= len(s) {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
