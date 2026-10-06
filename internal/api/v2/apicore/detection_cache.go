package apicore

import (
	"context"
	"runtime/debug"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/ttlcache"
)

// DetectionPage is one cached page of detections together with the total
// number of matches for the query that produced it. Notes is shared by every
// caller served from the same entry and must be treated as read-only.
type DetectionPage struct {
	Notes []datastore.Note
	Total int64
}

// DetectionPageKind names the datastore call a cached detection page comes
// from.
type DetectionPageKind string

// Page kinds used as DetectionPageKey.Kind. The detections handler picks the
// kind from the datastore call a request resolves to, so two kinds never share
// an entry unless they run the identical call.
const (
	// DetectionPageHourly keys GetHourlyDetections pages.
	DetectionPageHourly DetectionPageKind = "hourly"
	// DetectionPageSpecies keys SpeciesDetections pages.
	DetectionPageSpecies DetectionPageKind = "species"
	// DetectionPageSearch keys SearchNotes and its common-name variant,
	// including the empty search that lists everything.
	DetectionPageSearch DetectionPageKind = "search"
	// DetectionPageAdvanced keys SearchNotesAdvanced pages built from the
	// request filters.
	DetectionPageAdvanced DetectionPageKind = "advanced"
)

// DetectionPageKey identifies one cached detection page. It is a flat
// comparable struct: every field a loader reads must be set by the key
// builder, and fields a kind does not use stay zero. SearchScientific is the
// list of scientific-name alternatives joined with DetectionPageNameSeparator.
type DetectionPageKey struct {
	Kind             DetectionPageKind
	Date, Hour       string
	Duration         int
	Species          string
	Search           string
	SearchScientific string
	StartDate        string
	EndDate          string
	Confidence       string
	TimeOfDay        string
	HourRange        string
	Verified         string
	Location         string
	Source           string
	Locked           string
	SortBy           string
	Limit, Offset    int
}

// DetectionPageNameSeparator joins scientific-name alternatives in
// DetectionPageKey.SearchScientific. NUL cannot occur in a name, so two
// different lists never join to the same string.
const DetectionPageNameSeparator = "\x00"

// Detection cache sizing.
//
// The detections list endpoint is public, so the cache is bounded twice: pages
// larger than detectionCacheMaxPageNotes are never cached, and at most
// detectionCacheMaxEntries pages are kept. The UI pages in 25 to 100 notes, so
// larger pages (exports, batch selection) are rare and go straight to the
// datastore.
//
// TestDetectionCacheMaxEntries_WorstCaseStaysWithinBudget measures the heap a
// page retains: about 144 KB for 100 fully populated notes on linux/amd64. The
// worst case of 48 such pages is about 6.9 MB, within the 8 MiB budget
// (detectionCacheMaxBytes) for a Raspberry Pi. Bounding the entry count alone
// would have allowed only six 1000-note pages (about 1.4 MB each), too few to
// help normal paging.
const (
	detectionCacheExpiry = 5 * time.Minute // Detection-query cache expiration

	// detectionCacheMaxEntries is the maximum number of cached detection pages.
	detectionCacheMaxEntries = 48

	// detectionCacheMaxBytes is the memory budget for the worst case on a
	// Raspberry Pi.
	detectionCacheMaxBytes = 8 << 20

	// detectionCacheMaxPageNotes is the largest page that is cached. Requests
	// for more notes per page bypass the cache.
	detectionCacheMaxPageNotes = 100
)

// DetectionPageCache caches detection pages. All methods are safe on a nil
// receiver: a nil cache loads every page directly and caches nothing, so a
// Core built without one (as in tests) keeps working.
type DetectionPageCache struct {
	cache *ttlcache.Cache[DetectionPageKey, DetectionPage]
}

// NewDetectionPageCache creates a cache with the detection TTL and entry
// bound. Extra options are applied after them, so a test can inject a clock.
func NewDetectionPageCache(opts ...ttlcache.Option) *DetectionPageCache {
	all := make([]ttlcache.Option, 0, len(opts)+1)
	all = append(all, ttlcache.WithMaxEntries(detectionCacheMaxEntries))
	all = append(all, opts...)
	return &DetectionPageCache{
		cache: ttlcache.New[DetectionPageKey, DetectionPage](detectionCacheExpiry, all...),
	}
}

// GetOrLoad returns the cached page for key or loads it once for all
// concurrent callers. See ttlcache.Cache.GetOrLoad for the context contract:
// the loader's context is not cancelled when the caller's is.
//
// Pages with a Limit above detectionCacheMaxPageNotes are loaded directly and
// not cached, which keeps the worst-case memory of the cache bounded. A nil
// cache loads every page directly. On both direct paths a context that is
// already done returns its error without calling load, and a panic in load is
// returned as *ttlcache.PanicError, as on the cached path.
//
// The key is a pointer only because the struct is large; it is copied and must
// not be nil.
func (c *DetectionPageCache) GetOrLoad(ctx context.Context, key *DetectionPageKey, load func(context.Context) (DetectionPage, error)) (DetectionPage, error) {
	if c == nil || key.Limit > detectionCacheMaxPageNotes {
		// Match the cached path: a caller that has already gone away gets its
		// context error instead of starting a datastore query.
		if err := ctx.Err(); err != nil {
			return DetectionPage{}, err
		}
		return loadDirect(ctx, load)
	}
	return c.cache.GetOrLoad(ctx, *key, load)
}

// loadDirect runs load on the caller's goroutine and turns a panic into a
// *ttlcache.PanicError, so callers handle a panicking loader the same way
// whether or not the page was cacheable.
func loadDirect(ctx context.Context, load func(context.Context) (DetectionPage, error)) (page DetectionPage, err error) {
	defer func() {
		if r := recover(); r != nil {
			page, err = DetectionPage{}, &ttlcache.PanicError{Value: r, Stack: debug.Stack()}
		}
	}()
	return load(ctx)
}

// Invalidate drops every cached page and discards the results of loads in
// flight. Call it after any write that changes detection data.
func (c *DetectionPageCache) Invalidate() {
	if c == nil {
		return
	}
	c.cache.Clear()
}

// Len returns the number of live cached pages.
func (c *DetectionPageCache) Len() int {
	if c == nil {
		return 0
	}
	return c.cache.Len()
}

// Stats returns the cumulative hit, miss and eviction counters.
func (c *DetectionPageCache) Stats() ttlcache.Stats {
	if c == nil {
		return ttlcache.Stats{}
	}
	return c.cache.Stats()
}
