package suncalc

import (
	"fmt"
	"log"
	"sync"
	"time"

	tzf "github.com/ringsaturn/tzf"
)

// tzCache maps a rounded "lat,lon" key to the resolved *time.Location.
// Rounding to four decimal places (~11 m) is coarser than any real
// timezone boundary, so stable home coordinates always land on the same
// key across restarts and minor config edits.
var (
	tzMu    sync.Mutex
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
// Falls back to time.Local with a warning log if the finder cannot be
// created, the coordinate is not covered by the polygon database, or
// the IANA name cannot be loaded by the Go runtime.
func resolveTimezone(latitude, longitude float64) *time.Location {
	key := tzCacheKey(latitude, longitude)

	tzMu.Lock()
	defer tzMu.Unlock()

	if loc, ok := tzCache[key]; ok {
		return loc
	}
	loc := lookupLocationLocked(latitude, longitude)
	tzCache[key] = loc
	return loc
}

// lookupLocationLocked resolves a single coordinate pair using a
// short-lived tzf finder. The caller must hold tzMu, which serialises
// the expensive first load across concurrent startup callers.
//
// The finder escapes this function only as unreferenced memory: once
// the call returns, no goroutine holds a pointer to it, so its
// polygon backing store becomes eligible for collection.
func lookupLocationLocked(latitude, longitude float64) *time.Location {
	finder, err := tzf.NewDefaultFinder()
	if err != nil {
		log.Printf("suncalc: timezone finder init failed, falling back to system timezone: %v", err)
		return time.Local
	}
	// tzf API takes (longitude, latitude) -- longitude first.
	tzName := finder.GetTimezoneName(longitude, latitude)
	if tzName == "" {
		log.Printf("suncalc: no timezone found for coordinates (%.4f, %.4f), falling back to system timezone", latitude, longitude)
		return time.Local
	}
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		log.Printf("suncalc: failed to load timezone %q for coordinates (%.4f, %.4f), falling back to system timezone: %v",
			tzName, latitude, longitude, err)
		return time.Local
	}
	return loc
}
