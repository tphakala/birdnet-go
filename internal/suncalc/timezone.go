package suncalc

import (
	"fmt"
	"sync"
	"time"

	tzf "github.com/ringsaturn/tzf"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// tzCache maps a rounded "lat,lon" key to the resolved *time.Location.
// Rounding to four decimal places (~11 m) is coarser than any real
// timezone boundary, so stable home coordinates always land on the same
// key across restarts and minor config edits.
//
// Access is guarded by tzMu. Readers may hold the read lock for cache
// hits; writers acquire the write lock across the (expensive) cache
// miss path, which also serialises concurrent startup callers so the
// 32 MB tzf polygon dataset is loaded at most once per unique key.
//
// Per-key singleflight (golang.org/x/sync/singleflight) was evaluated and
// intentionally not adopted: every caller resolves the single configured
// home coordinate at startup or reconfigure, so there is never more than
// one distinct key in flight and a write lock held across an unrelated
// key's miss cannot occur. Revisit only if a call site begins resolving
// non-home or per-request coordinates, or if tzMu shows up in contention
// profiles.
var (
	tzMu    sync.RWMutex
	tzCache = make(map[string]*time.Location)
)

// tzCacheKey builds the cache key for a coordinate pair.
func tzCacheKey(latitude, longitude float64) string {
	return fmt.Sprintf("%.4f,%.4f", latitude, longitude)
}

// resolveTimezone returns the IANA timezone for the given coordinates.
//
// The tzf finder carries ~32 MB of worldwide polygon data and only
// needs to answer one question per unique coordinate pair. To avoid
// pinning that dataset for the lifetime of the process, the finder is
// created inside a local scope, consulted once, and dropped so the
// next GC cycle can reclaim its memory. The resolved *time.Location is
// cached by rounded coordinate so repeat callers never trigger another
// load.
//
// Cache reads use an RLock for concurrent hits; only cache misses
// acquire the write lock. A double-check after promoting to the write
// lock ensures a concurrent caller that already resolved the same key
// wins the race.
//
// Falls back to time.Local with a warning log (and does NOT cache the
// fallback) if the finder cannot be created, the coordinate is not
// covered by the polygon database, or the IANA name cannot be loaded
// by the Go runtime. Skipping the cache on failure lets a subsequent
// call retry once the underlying cause is resolved.
func resolveTimezone(latitude, longitude float64) *time.Location {
	key := tzCacheKey(latitude, longitude)

	tzMu.RLock()
	if loc, ok := tzCache[key]; ok {
		tzMu.RUnlock()
		return loc
	}
	tzMu.RUnlock()

	tzMu.Lock()
	defer tzMu.Unlock()

	// Double-check: another goroutine may have populated the cache
	// while we were waiting for the write lock.
	if loc, ok := tzCache[key]; ok {
		return loc
	}
	loc, ok := lookupLocationLocked(latitude, longitude)
	if ok {
		tzCache[key] = loc
	}
	return loc
}

// lookupLocationLocked resolves a single coordinate pair using a
// short-lived tzf finder. The caller must hold the write lock of
// tzMu, which serialises the expensive first load across concurrent
// startup callers.
//
// Returns (loc, true) on a real resolution and (time.Local, false)
// on any failure. The caller uses the bool to decide whether to
// cache the result so transient failures (missing tzdata, OS errors
// inside time.LoadLocation) do not stick for the lifetime of the
// process.
//
// The finder escapes this function only as unreferenced memory: once
// the call returns, no goroutine holds a pointer to it, so its
// polygon backing store becomes eligible for collection.
func lookupLocationLocked(latitude, longitude float64) (*time.Location, bool) {
	log := GetLogger()
	finder, err := tzf.NewDefaultFinder()
	if err != nil {
		log.Warn("timezone finder init failed, falling back to system timezone",
			logger.Error(err))
		return time.Local, false
	}
	// tzf API takes (longitude, latitude) -- longitude first.
	tzName := finder.GetTimezoneName(longitude, latitude)
	if tzName == "" {
		// Coordinates are PII (see internal/weather/weather.go), so the
		// configured home location is intentionally not logged here.
		log.Warn("no timezone found for coordinates, falling back to system timezone")
		return time.Local, false
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		// The resolved IANA name is enough to debug a load failure without
		// logging the PII coordinates that produced it.
		log.Warn("failed to load timezone, falling back to system timezone",
			logger.String("timezone", tzName),
			logger.Error(err))
		return time.Local, false
	}
	return loc, true
}
