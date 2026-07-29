// Package guideprovider implements a two-tier (in-memory + database) cache of
// species guide information. OpenFauna is the primary provider: it answers from
// the embedded dataset with no network call, supplying taxonomy, localized common
// names, and external links. Wikipedia is an optional secondary provider,
// registered only when the user opts in, and contributes the prose description —
// the one thing the offline dataset cannot. The cache provides
// stale-while-revalidate semantics, singleflight deduplication, negative caching,
// background refresh, and startup warming of the most-detected species.
//
// Upstream failures are contained by classification rather than by a circuit
// breaker: transient failures (5xx, 429, 408, network errors) are surfaced without
// being persisted so they retry, while a definitive not-found or a persistent
// refusal is persisted as a short-lived negative entry so it is not re-attempted
// on every request. See wikipedia.go for the status classification.
package guideprovider

import (
	"context"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"golang.org/x/sync/singleflight"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/openfauna"
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
	// maxConcurrentPreFetches bounds how many detection-driven pre-fetches may be in
	// flight at once. singleflight collapses repeats of the SAME species but not
	// distinct ones, so a burst introducing many new species would otherwise park one
	// goroutine per species inside the provider's rate limiter — with no deadline,
	// since the fetch runs on the cache's own context. Pre-fetch is opportunistic
	// warming, so shedding the overflow is correct: the species is fetched on demand
	// when the user actually opens its guide. Mirrors the API layer's bound on the
	// similar-species fan-out.
	maxConcurrentPreFetches = 4
	// providerSetKeyPrefix namespaces the provider-set cache key so it can never
	// collide with a value written by an older build.
	//
	// This is load-bearing, not cosmetic. Before this scheme the provider column
	// held resolveProviderName() — providers[0].name — and OpenFauna is always
	// registered first (see guide_cache_init.go), so EVERY row an older build wrote
	// reads "openfauna", whatever actually produced the prose. An upgraded install
	// running OpenFauna-only would derive the identical unprefixed key, match those
	// legacy rows, and keep serving Wikipedia prose — credited to OpenFauna, under a
	// CC BY-SA licence URL — for a full PositiveTTL. That is precisely the bug
	// keying on the provider set exists to prevent, surviving in exactly the upgrade
	// case that matters. The prefix makes legacy rows unmatchable, so they age out
	// on DBRetention instead of being served.
	providerSetKeyPrefix = "set:"
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
	Get(ctx context.Context, scientificName, locale, providerSet string) (*GuideCacheEntry, error)
	Save(ctx context.Context, entry *GuideCacheEntry) error
	// GetRecent returns up to limit entries produced by providerSet, most-recently-
	// cached first. The warm load uses it to bound the startup result set: rows are
	// capped only by time-based retention, so a flood of short-lived negative entries
	// could otherwise materialize a very large slice at boot. The providerSet filter
	// keeps rows written under a retired set out of the memory tier, whose key has no
	// provider component and so cannot distinguish them.
	GetRecent(ctx context.Context, limit int, providerSet string) ([]GuideCacheEntry, error)
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

// memEntry is one memory-tier slot: the cached guide plus the recency stamp the
// sampled-LRU eviction orders by. tick is atomic so a reader can refresh it while
// holding only the read lock.
type memEntry struct {
	guide *SpeciesGuide
	tick  atomic.Int64
}

// GuideCache orchestrates the two-tier cache and provider fallback.
type GuideCache struct {
	// memory is the in-process tier, keyed "scientificName|locale".
	//
	// A plain map under an RWMutex, not a sync.Map: the cap needs an exact live-entry
	// count and the ability to pick a victim, and len(map) gives the first for free
	// while ranging gives the second. The previous sync.Map needed a separate atomic
	// counter plus a writer mutex to keep that counter exact, and four call sites had
	// to pair every insert and delete with the right delta by hand — a drift above the
	// cap would have silently stopped the cache admitting anything, with no error, no
	// metric and no log.
	memMu  sync.RWMutex
	memory map[string]*memEntry
	// memTick is a monotonic counter stamped onto every entry on write and on read,
	// ordering entries by recency of use so the cap can EVICT rather than refuse.
	memTick   atomic.Int64
	store     GuideStore
	metrics   GuideCacheMetrics
	providers []registeredProvider
	// providerSetKey is derived from providers, which are fixed during setup, so it
	// is computed once on first use rather than rebuilt (alloc + sort + join) on
	// every DB read and every save. Latched on first call, which is always after
	// registration — RegisterProvider is documented as setup-only.
	providerSetKeyOnce sync.Once
	providerSetKeyVal  string

	warmTopN int
	// warmLocale is the dashboard locale that cache warming (startup WarmForSpecies +
	// per-detection PreFetch) targets, so warmed entries key to the same locale the UI
	// requests. Set once via SetWarmLocale before Start() and treated as immutable for
	// the cache's lifetime (a reconfigure rebuilds the cache), so background warm
	// goroutines read it without a lock. Empty falls back to defaultLocale.
	warmLocale string

	sf singleflight.Group

	// preFetchSem bounds concurrent detection-driven pre-fetches (see
	// maxConcurrentPreFetches). A non-blocking acquire sheds the overflow rather than
	// queueing it, so a detection burst cannot accumulate goroutines.
	preFetchSem chan struct{}

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
		store:       store,
		metrics:     metrics,
		memory:      make(map[string]*memEntry),
		preFetchSem: make(chan struct{}, maxConcurrentPreFetches),
		ctx:         ctx,
		cancel:      cancel,
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
// Configuration methods (RegisterProvider, SetWarmTopN, SetWarmLocale) are
// NOT concurrency-safe and must all be called during setup, before Start() and
// before any Get(); they mutate state that concurrent reads do not lock.
func (c *GuideCache) RegisterProvider(name string, provider GuideProvider) {
	if c == nil || provider == nil || name == "" {
		return
	}
	c.providers = append(c.providers, registeredProvider{name: name, provider: provider})
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

// lookupMemory returns a cached guide and marks it recently used. The recency
// stamp is an atomic store under the read lock, so concurrent readers still do not
// serialize on each other.
func (c *GuideCache) lookupMemory(key string) (*SpeciesGuide, bool) {
	c.memMu.RLock()
	defer c.memMu.RUnlock()
	e, ok := c.memory[key]
	if !ok {
		return nil, false
	}
	e.tick.Store(c.memTick.Add(1))
	return e.guide, true
}

// storeMemory writes an entry to the memory tier, evicting the least-recently-used
// entry of a small random sample when the tier is full.
//
// Eviction is what makes the cap survivable. Previously a full tier simply REFUSED
// every new key: positive entries are refreshed in place and never removed, so once
// 5000 slots filled the tier froze permanently — every subsequent species became a
// guaranteed Tier-1 miss paying a DB round-trip forever, with no way for a hot
// species to displace a cold one and no recovery short of a restart.
//
// Sampled (rather than exact) LRU keeps the write path O(1): Go randomizes map
// iteration start, so evicting the oldest of a handful of entries approximates true
// LRU closely enough for a cache whose working set is far below the cap in normal
// use, without maintaining an intrusive list.
func (c *GuideCache) storeMemory(key string, g *SpeciesGuide) {
	c.memMu.Lock()
	defer c.memMu.Unlock()
	if e, ok := c.memory[key]; ok {
		e.guide = g
		e.tick.Store(c.memTick.Add(1))
		return
	}
	if len(c.memory) >= maxMemoryEntries {
		c.evictLRULocked()
	}
	e := &memEntry{guide: g}
	e.tick.Store(c.memTick.Add(1))
	c.memory[key] = e
}

// evictLRULocked removes the least-recently-used entry among a bounded random
// sample. Caller must hold memMu for writing.
func (c *GuideCache) evictLRULocked() {
	const sampleSize = 8
	var (
		victim string
		oldest int64
		seen   int
	)
	for k, e := range c.memory {
		t := e.tick.Load()
		if seen == 0 || t < oldest {
			oldest, victim = t, k
		}
		seen++
		if seen >= sampleSize {
			break
		}
	}
	if victim != "" {
		delete(c.memory, victim)
	}
}

// Start loads existing DB entries into memory and launches the refresh loop.
// Safe to call once; subsequent calls are no-ops.
func (c *GuideCache) Start() {
	if c == nil {
		return
	}
	c.startOnce.Do(func() {
		// The DB pre-load is detached, for the same reason the top-N warm is: it reads
		// up to maxMemoryEntries rows, each carrying a description of up to
		// maxDescriptionLength bytes, and nothing waits on the result — a key it has
		// not loaded yet is simply a miss that falls through to the DB tier. Run
		// inline it delayed the HTTP listener at startup, and stalled the single
		// control-monitor goroutine on every cache rebuild.
		c.goIfOpen(c.loadFromDB)
		c.wg.Go(c.startCacheRefresh)
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

// resolveProviderName returns the primary provider name, used as the user-facing
// attribution label on a guide.
func (c *GuideCache) resolveProviderName() string {
	if len(c.providers) == 0 {
		return WikipediaProviderName
	}
	return c.providers[0].name
}

// providerSetKey returns a canonical identifier for the WHOLE registered provider
// set, used as the DB row's key component.
//
// This is what makes a provider change self-invalidating: a guide fetched with
// Wikipedia enabled is stored under "openfauna+wikipedia" and one fetched without it
// under "openfauna", so turning Wikipedia off simply stops matching the old rows and
// they age out on retention. Keying on the primary provider alone made both look
// identical, which is why disabling Wikipedia used to keep serving its prose (and its
// CC BY-SA attribution) for a full PositiveTTL after a restart.
//
// The set is fixed during setup and never mutated afterwards (see RegisterProvider),
// so this reads without a lock, mirroring resolveProviderName.
func (c *GuideCache) providerSetKey() string {
	c.providerSetKeyOnce.Do(func() {
		c.providerSetKeyVal = providerSetKeyPrefix + c.computeProviderSetKey()
	})
	return c.providerSetKeyVal
}

// computeProviderSetKey builds the unprefixed set identifier. Split out so the
// memoization above stays readable and so tests can exercise the derivation.
func (c *GuideCache) computeProviderSetKey() string {
	if len(c.providers) == 0 {
		return WikipediaProviderName
	}
	names := make([]string, 0, len(c.providers))
	for i := range c.providers {
		names = append(names, c.providers[i].name)
	}
	// Sorted so registration order cannot change the key and needlessly orphan rows.
	slices.Sort(names)
	return strings.Join(names, "+")
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

// loadFromDB populates the memory tier from all persisted entries.
func (c *GuideCache) loadFromDB() {
	if c.store == nil {
		return
	}
	// Bound the warm load to the in-memory cap, freshest first. storeMemory
	// discards anything beyond maxMemoryEntries anyway, so reading more is wasted
	// work, and an unbounded read could materialize a large transient slice when
	// many short-lived negative rows have accrued since the last retention cleanup.
	entries, err := c.store.GetRecent(c.ctx, maxMemoryEntries, c.providerSetKey())
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
	// The normalized locale is threaded onward as a plain string; fetchFromProviders
	// rebuilds FetchOptions from it. Assigning it back onto the by-value opts copy
	// here was a dead store, and a trap: adding a second field to FetchOptions and
	// passing opts through would look right while silently discarding this
	// normalization.
	locale := normalizeLocale(opts.Locale)
	key := cacheKey(name, locale)

	// Tier 1: memory. Fresh entries are returned immediately; a stale POSITIVE entry
	// is served stale-while-revalidate (returned now, refreshed in the background) so
	// a stale memory hit doesn't incur a redundant DB round-trip on every call.
	if g, ok := c.lookupMemory(key); ok && !c.isStaleNegative(g) {
		c.metrics.RecordCacheHit(tierMemory, entryQuality(g))
		if c.isCacheEntryStale(g) {
			c.triggerAsyncRefresh(name, locale)
		}
		return g, nil
	}
	c.metrics.RecordCacheMiss(tierMemory)

	// Tier 2: DB. Skipped when no store is wired — the memory and provider tiers
	// still function, and guarding here (mirroring the nil-store checks in
	// loadFromDB and saveGuide) prevents a nil dereference if a cache is ever
	// constructed without a store. Production wiring always supplies one.
	if c.store != nil {
		entry, err := c.store.Get(ctx, name, locale, c.providerSetKey())
		// Convert once: the switch guard and the body need the same value, and
		// entryToGuide allocates a fresh SpeciesGuide each call.
		var g *SpeciesGuide
		if entry != nil {
			g = entryToGuide(entry)
		}
		switch {
		case err == nil && g != nil && !c.isStaleNegative(g):
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

	// Every registered provider runs and the results merge. There is no "primary
	// only" mode: SetFallbackPolicy had no production caller, so the branch that
	// sliced the set down to providers[:1] was unreachable, as was the
	// conf.SpeciesGuideFallbackNone constant that selected it. Registering fewer
	// providers is how a caller narrows the set.
	providers := c.providers

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
		if failedCount > 0 {
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
	// Never persist once this cache's context is done.
	//
	// Every fetchAndStore runs on c.ctx, which Close cancels before it returns, so this
	// is the explicit form of an invariant a reconfigure depends on: singleflight
	// spawns its own goroutines, which the wait group does NOT track, so Close can
	// return while a straggler sits between fetchFromProviders (whose providersMu read
	// lock Close does wait on) and this write. The row it would insert is keyed by the
	// retired provider set, so the incoming cache will not read it — but it is still a
	// write issued on behalf of a cache the process has already torn down, landing
	// after Close returned, and it costs a DB round-trip plus a row that lingers until
	// retention. The guard makes "a closed cache performs no further writes" explicit
	// rather than incidental.
	//
	// database/sql would also refuse a cancelled context, but leaning on that makes
	// correctness a property of driver behaviour and would break silently if a caller's
	// context were ever threaded through here instead of the cache's own.
	if ctx.Err() != nil {
		GetLogger().Debug("Skipping guide save on a cancelled cache context",
			logger.String("species", name), logger.String("locale", locale))
		return
	}
	entry := guideToEntry(name, locale, c.providerSetKey(), g)
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

// isStaleNegative reports whether g is an EXPIRED not-found marker, which must be
// treated as a cache miss rather than served.
//
// Stale-while-revalidate is right for a stale positive — slightly old prose is far
// better than a blocking upstream fetch — but wrong for a negative. Serving an
// expired "not found" hands the caller a known-expired WRONG answer: the API layer
// maps it to HTTP 404, so a species whose article has since been created is reported
// as having no guide, and only a later request (after the background refresh landed)
// sees the truth. Falling through to the next tier costs one fetch and returns the
// right answer now.
func (c *GuideCache) isStaleNegative(g *SpeciesGuide) bool {
	return g.IsNegativeEntry() && c.isCacheEntryStale(g)
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
//
// It reports whether fn was started, so a caller that acquired a resource for fn
// (e.g. PreFetch's semaphore slot, released by a defer inside fn) can release it
// itself when fn will never run.
func (c *GuideCache) goIfOpen(fn func()) bool {
	c.lifecycleMu.Lock()
	if c.closed.Load() {
		c.lifecycleMu.Unlock()
		return false
	}
	c.wg.Add(1)
	c.lifecycleMu.Unlock()
	go func() {
		defer c.wg.Done()
		fn()
	}()
	return true
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
	// preFetchSem is nil only on a GuideCache built by hand rather than through
	// NewGuideCache (which sizes it). Decline explicitly instead of relying on the
	// select below, where a nil channel is simply never ready and would silently
	// shed every pre-fetch — the same observable behavior, but by accident.
	if c.preFetchSem == nil {
		return
	}
	name := normalizeScientificName(scientificName)
	if name == "" {
		return
	}
	// Bound the fan-out before spawning: a non-blocking acquire means a detection
	// burst sheds the overflow instead of queueing a goroutine per species. Acquired
	// here rather than inside the goroutine so the shed costs nothing.
	select {
	case c.preFetchSem <- struct{}{}:
	default:
		return
	}
	started := c.goIfOpen(func() {
		defer func() { <-c.preFetchSem }()
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
	if !started {
		// The cache closed between the check above and the spawn; the goroutine's
		// defer will never run, so release the slot here.
		<-c.preFetchSem
	}
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
		// Resolve the whole warm set against the embedded dataset in one pass per
		// blob before fetching species-by-species. Each per-name lookup would
		// otherwise decompress and scan the ~20 MB translations blob on its memo
		// miss, so a default 50-species warm paid dozens of full scans.
		//
		// Unconditional: PrimeCaches drops already-memoized names and skips a pass
		// with nothing left to resolve, and each of its scans early-exits once the
		// batch is complete — so even a one-species warm costs no more than the
		// single-name path it replaces.
		openfauna.PrimeCaches(names, c.warmLocale)
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
	// Snapshot under the read lock, then act without holding it: the refresh loop
	// below makes network calls and must not block readers.
	c.memMu.RLock()
	for key, e := range c.memory {
		g := e.guide
		if !c.isCacheEntryStale(g) {
			continue
		}
		if g.IsNegativeEntry() {
			evict = append(evict, key)
			continue
		}
		name, locale := splitCacheKey(key)
		stale = append(stale, staleKey{name: name, locale: locale})
	}
	c.memMu.RUnlock()
	if len(evict) > 0 {
		c.memMu.Lock()
		for _, key := range evict {
			// Re-check under the write lock before deleting: a concurrent fetchAndStore
			// may have replaced this stale negative with a fresh positive between the
			// snapshot above and here. Only evict when the CURRENT value is still a
			// stale negative, so a freshly-stored positive is never dropped on the
			// strength of an old snapshot.
			e, ok := c.memory[key]
			if !ok || !e.guide.IsNegativeEntry() || !c.isCacheEntryStale(e.guide) {
				continue
			}
			delete(c.memory, key)
		}
		c.memMu.Unlock()
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
	c.memMu.RLock()
	for _, e := range c.memory {
		if !e.guide.IsNegativeEntry() {
			count++
		}
	}
	c.memMu.RUnlock()
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

// BaseLanguage extracts the lowercase base-language subtag from a UI locale
// ("pt-br"/"pt_PT" -> "pt", "zh-cn" -> "zh"), validating it as a 2-3 letter code and
// falling back to defaultLocale ("en") for anything else.
//
// Exported because three call sites need exactly this and each used to carry its own
// character-for-character copy (guideprovider's wikipediaSubdomain, the API layer's
// baseLanguage, and the link resolver). It lives in this leaf package for the same
// reason ClassifyQuality does: both consumers need it and the API domain packages may
// not import each other.
func BaseLanguage(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	// Split on either separator and keep the primary subtag.
	if i := strings.IndexAny(l, "-_"); i >= 0 {
		l = l[:i]
	}
	if len(l) < 2 || len(l) > 3 {
		return defaultLocale
	}
	for _, r := range l {
		if r < 'a' || r > 'z' {
			return defaultLocale
		}
	}
	return l
}

// openFaunaLocaleSet is the embedded dataset's locale codes as a set, built once.
// normalizeLocale consults it per request, so a linear scan of the slice Locales()
// returns would be needless work on the hot path.
var openFaunaLocaleSet = sync.OnceValue(func() map[string]struct{} {
	available := openfauna.Locales()
	set := make(map[string]struct{}, len(available))
	for _, l := range available {
		set[l] = struct{}{}
	}
	return set
})

// normalizeLocale canonicalizes a locale to the one that actually changes the
// result, and is the cache key's second component.
//
// Shape validation alone is not enough to bound the key space. localePattern admits
// an enormous number of well-formed regional subtags, and every distinct one used to
// become a distinct memory key AND a distinct DB row — while wikipediaSubdomain
// collapsed all of them to the same base subtag, so the rows held identical content.
// A client varying the locale could therefore mint unbounded near-duplicate rows
// (retained for DBRetention, and Cleanup only prunes by age) and fill the bounded
// memory tier with copies of one guide.
//
// So a regional subtag is preserved only when it can genuinely change the answer:
// when it names a real hyphenated Wikipedia edition (zh-classical, be-tarask), or a
// locale the embedded dataset actually carries (which supplies regional common
// names). Every other regional subtag collapses to its base language, making the key
// space finite and each key's content distinct.
func normalizeLocale(locale string) string {
	l := strings.ToLower(strings.TrimSpace(locale))
	if l == "" {
		return defaultLocale
	}
	// Accept either separator, then reject anything not locale-shaped: this is also
	// what stops arbitrary input from reaching a Wikipedia host.
	l = strings.ReplaceAll(l, "_", "-")
	if !localePattern.MatchString(l) {
		return defaultLocale
	}
	if strings.IndexByte(l, '-') < 0 {
		return BaseLanguage(l) // already a bare subtag; validate its length/charset
	}
	if _, ok := wikipediaHyphenatedSubdomains[l]; ok {
		return l
	}
	if _, ok := openFaunaLocaleSet()[strings.ReplaceAll(l, "-", "_")]; ok {
		return l
	}
	return BaseLanguage(l)
}

// entryQuality classifies a guide for metrics labeling. It shares its thresholds and
// its section marker with the API layer's user-facing classification (see
// ClassifyQuality) so the cache-quality metric and the UI badge cannot disagree about
// the same guide.
func entryQuality(g *SpeciesGuide) string {
	if g.IsNegativeEntry() {
		return qualityNegative
	}
	return ClassifyQuality(g.Description, g.Partial)
}

// SectionMarker is the markdown heading prefix that separates a guide description
// into comparison sections. A description carrying at least one is "full" prose
// rather than a bare intro. Exported so the API layer classifies against the same
// marker the Wikipedia section converter emits.
const SectionMarker = "## "

// DescriptionStubMaxLength is the byte length under which a description is treated
// as a stub rather than prose.
const DescriptionStubMaxLength = 80

// ClassifyQuality classifies a guide description's completeness: stub when there is
// too little text to be useful, intro_only when there is prose but no sections (or a
// provider failed, making the content incomplete), full otherwise.
//
// This is the single authority for guide quality. It lives here, in the leaf package,
// because both consumers need it and the API domain packages may not import each
// other; the API layer re-exports the label strings it serves.
func ClassifyQuality(description string, partial bool) string {
	trimmed := strings.TrimSpace(description)
	switch {
	case len(trimmed) < DescriptionStubMaxLength:
		return qualityStub
	case partial || !strings.Contains(trimmed, SectionMarker):
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
