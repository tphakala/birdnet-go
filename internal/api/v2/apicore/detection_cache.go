package apicore

import (
	"context"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/ttlcache"
)

// DetectionPage is one cached page of detections together with the total
// number of matches for the query that produced it.
type DetectionPage struct {
	Notes []datastore.Note
	Total int64
}

// Page kinds used as DetectionPageKey.Kind. The detections handler picks the
// kind from the datastore call a request resolves to, so two kinds never share
// an entry unless they run the identical call.
const (
	// DetectionPageHourly keys GetHourlyDetections pages.
	DetectionPageHourly = "hourly"
	// DetectionPageSpecies keys SpeciesDetections pages.
	DetectionPageSpecies = "species"
	// DetectionPageSearch keys SearchNotes and its common-name variant,
	// including the empty search that lists everything.
	DetectionPageSearch = "search"
	// DetectionPageAdvanced keys SearchNotesAdvanced pages built from the
	// request filters.
	DetectionPageAdvanced = "advanced"
)

// DetectionPageKey identifies one cached detection page. It is a flat
// comparable struct: every field a loader reads must be set by the key
// builder, and fields a kind does not use stay zero. SearchScientific is the
// NUL-joined list of scientific-name alternatives.
type DetectionPageKey struct {
	Kind             string
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

// Detection cache sizing.
//
// The detections list endpoint is public, so the cache must be bounded: a
// client that walks offsets or varies filters would otherwise grow it without
// limit for the five-minute TTL. A page can hold up to
// detectionCacheWorstPageNotes notes, so the entry bound is derived from the
// heap that such a page retains.
//
// Measured with TestDetectionCacheMaxEntries_WorstCaseStaysWithinBudget (Go
// 1.27, linux/amd64; every note carries a review, a lock, one 160 byte comment,
// source and model metadata and 24 to 60 byte strings, which is generous for
// real rows). Three runs agreed within 0.1 percent:
//
//	  25 notes per page:     26 KB retained per page (about 1.0 KB per note)
//	 100 notes per page:    144 KB retained per page (about 1.4 KB per note)
//	1000 notes per page:  1365 KB retained per page (about 1.4 KB per note)
//
// The budget is 8 MiB (detectionCacheMaxBytes) for the worst case, which is
// detectionCacheMaxEntries pages of 1000 notes: 8 MiB / 1.365 MB = 6.1, so 6
// entries (8.18 MB measured). Typical 100-note pages then cost under 1 MB in
// total. A 64-bit ARM measurement is NOT MEASURED; pointer and int sizes match
// amd64, so the figures should carry over. A byte-weighted bound is a separate
// follow-up; this count bound only caps the worst case.
const (
	detectionCacheExpiry = 5 * time.Minute // Default detection-query cache expiration

	// detectionCacheMaxEntries is the maximum number of cached detection pages.
	detectionCacheMaxEntries = 6

	// detectionCacheMaxBytes is the memory budget for the worst case on a
	// Raspberry Pi.
	detectionCacheMaxBytes = 8 << 20

	// detectionCacheWorstPageNotes is the largest page a request can cache. It
	// mirrors maxNumResults in the detections package.
	detectionCacheWorstPageNotes = 1000
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
// The key is a pointer only because the struct is large; it is copied and must
// not be nil.
func (c *DetectionPageCache) GetOrLoad(ctx context.Context, key *DetectionPageKey, load func(context.Context) (DetectionPage, error)) (DetectionPage, error) {
	if c == nil {
		return load(ctx)
	}
	return c.cache.GetOrLoad(ctx, *key, load)
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
