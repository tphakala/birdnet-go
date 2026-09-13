// internal/suncalc/suncalc.go

// Package suncalc provides solar event calculations with support for high-latitude locations.
// For locations experiencing polar day conditions (like midsummer in Finland), where civil
// twilight cannot be calculated, the package gracefully falls back to sunrise/sunset times
// to prevent calculation errors from breaking application functionality.
package suncalc

import (
	"sync"
	"time"

	"github.com/sj14/astral/pkg/astral"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// SunEventTimes holds the calculated sun event times in local time
type SunEventTimes struct {
	CivilDawn time.Time // Civil dawn in local time
	Sunrise   time.Time // Sunrise in local time
	Sunset    time.Time // Sunset in local time
	CivilDusk time.Time // Civil dusk in local time
}

// cacheEntry holds the cached sun event times for a given date, stamped with the
// location generation they were computed under. A calculation that starts before
// an UpdateLocation can finish after it and insert into the freshly cleared
// cache; stamping lets that stale entry be recognized and ignored on read rather
// than served until the next eviction.
type cacheEntry struct {
	times      SunEventTimes // Sun event times in local time
	generation uint64        // Location generation these times were computed under
}

// maxCacheEntries caps the number of cached dates to prevent unbounded
// memory growth. 400 entries covers over a year of daily lookups; when
// exceeded the entire cache is cleared so the working set rebuilds
// organically from live traffic.
const maxCacheEntries = 400

// sunEventsOperation is the operation label used for all GetSunEventTimes
// metrics. Using a single constant keeps the Prometheus series consistent; a
// typo in any one call site would otherwise split the series.
const sunEventsOperation = "get_sun_events"

// SunCalc handles caching and calculation of sun event times
type SunCalc struct {
	cache    map[string]cacheEntry // Cache of sun event times for dates
	lock     sync.RWMutex          // Lock for cache access
	observer astral.Observer       // Observer for sun event calculations
	location *time.Location        // Timezone derived from observer coordinates
	// generation increments on every location change. Cache entries carry the
	// generation they were computed under so a result calculated against a
	// superseded location can never be read back as a hit.
	generation uint64
	metrics    *metrics.SunCalcMetrics // Metrics for observability
}

// NewSunCalc creates a new SunCalc instance
func NewSunCalc(latitude, longitude float64) *SunCalc {
	return &SunCalc{
		cache:    make(map[string]cacheEntry),
		observer: astral.Observer{Latitude: latitude, Longitude: longitude},
		location: resolveTimezone(latitude, longitude),
	}
}

// SetMetrics sets the metrics instance for observability
func (sc *SunCalc) SetMetrics(m *metrics.SunCalcMetrics) {
	sc.lock.Lock()
	defer sc.lock.Unlock()
	sc.metrics = m
}

// UpdateLocation repoints the calculator at new station coordinates and reports
// whether anything changed. It exists so that a location edit made in the UI
// takes effect without a restart: holders keep their *SunCalc pointer and the
// observer is swapped underneath them, which avoids having to thread a pointer
// swap through every long-lived struct that shares the instance.
//
// Cached sun events belong to the old observer, so they are discarded. Passing
// the current coordinates is a no-op and keeps the cache, which lets callers
// invoke this from a broader "settings changed" path without paying for a
// needless cache rebuild.
func (sc *SunCalc) UpdateLocation(latitude, longitude float64) bool {
	sc.lock.Lock()
	defer sc.lock.Unlock()

	if sc.observer.Latitude == latitude && sc.observer.Longitude == longitude {
		return false
	}

	sc.observer = astral.Observer{Latitude: latitude, Longitude: longitude}
	sc.location = resolveTimezone(latitude, longitude)
	sc.generation++
	clear(sc.cache)
	return true
}

// locationSnapshot is one coherent view of where the calculator is pointed.
// Every public entry point takes exactly one and threads it through, so a
// concurrent UpdateLocation can never make a single call mix coordinates from
// one location with the timezone or cached events of another.
type locationSnapshot struct {
	observer   astral.Observer
	location   *time.Location
	generation uint64
}

// snapshot reads the observer, its derived timezone and the current generation
// under one lock. The three are replaced together by UpdateLocation, so they are
// never read from the struct individually.
func (sc *SunCalc) snapshot() locationSnapshot {
	sc.lock.RLock()
	defer sc.lock.RUnlock()
	return locationSnapshot{observer: sc.observer, location: sc.location, generation: sc.generation}
}

// GetSunEventTimes returns the sun event times for a given date, using cache if available
func (sc *SunCalc) GetSunEventTimes(date time.Time) (SunEventTimes, error) {
	return sc.sunEventTimes(date, sc.snapshot())
}

// sunEventTimes resolves date against one already-taken location snapshot.
// Every public entry point funnels through here so the cache key, the
// calculation and the cache write all belong to the same location.
func (sc *SunCalc) sunEventTimes(date time.Time, snap locationSnapshot) (SunEventTimes, error) {
	start := time.Now()

	// Normalize date to observer timezone before generating cache key.
	// This ensures requests for the same local date hit the same cache entry,
	// even if the input time has a different timezone (e.g., UTC).
	localDate := date.In(snap.location)
	dateKey := localDate.Format(time.DateOnly)

	// Acquire a read lock and check if the date is in the cache.
	// Snapshot the metrics pointer under the same lock into a local so every
	// metrics access below operates on a stable value. SetMetrics writes
	// sc.metrics under the write lock, so reading it here (under RLock) is the
	// only synchronized read of the field; capturing it once also closes the
	// nil-panic window where a concurrent SetMetrics(nil) could land between a
	// "!= nil" check and the subsequent method call.
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	m := sc.metrics
	// Update cache size metric while holding the lock to avoid race condition
	if m != nil {
		m.UpdateCacheSize(float64(len(sc.cache)))
	}
	sc.lock.RUnlock()

	// An entry from a superseded location is not a hit, however it got there.
	if exists && entry.generation == snap.generation {
		if m != nil {
			m.RecordSunCalcCacheHit(sunEventsOperation)
			m.RecordSunCalcOperation(sunEventsOperation, "success")
			m.RecordSunCalcDuration(sunEventsOperation, time.Since(start).Seconds())
		}
		return entry.times, nil
	}

	// Double-check under a read lock: another goroutine may have populated
	// the cache between the first RLock check and now. This reduces (but does
	// not fully eliminate) redundant calculations under high concurrency,
	// which is acceptable since calculateSunEventTimes is pure math. A read
	// lock suffices here because this branch only reads; the authoritative
	// insert double-check in the store path below runs under the write lock.
	sc.lock.RLock()
	entry, exists = sc.cache[dateKey]
	sc.lock.RUnlock()
	if exists && entry.generation == snap.generation {
		if m != nil {
			m.RecordSunCalcCacheHit(sunEventsOperation)
			m.RecordSunCalcOperation(sunEventsOperation, "success")
			m.RecordSunCalcDuration(sunEventsOperation, time.Since(start).Seconds())
		}
		return entry.times, nil
	}

	// Record cache miss only after the double-check confirms it
	if m != nil {
		m.RecordSunCalcCacheMiss(sunEventsOperation)
	}

	// Calculate outside the lock to avoid blocking readers.
	times, err := calculateSunEventTimes(snap.observer, snap.location, localDate)
	if err != nil {
		if m != nil {
			m.RecordSunCalcOperation(sunEventsOperation, "error")
			m.RecordSunCalcError(sunEventsOperation, "calculation_error")
		}
		return SunEventTimes{}, err
	}

	// Store result and enforce cache size limit. Double-check for an existing
	// entry first: when several goroutines compute the same missing date
	// concurrently, a later one must not clear() the cache and wipe the entry
	// an earlier one just inserted. Mirroring the read double-check above, if
	// another goroutine already populated dateKey we reuse its value and skip
	// the clear/insert entirely. All callers for the date then return the same
	// cached times.
	//
	// The entry carries snap.generation, so if UpdateLocation ran while this
	// call was calculating, what lands here is stamped stale and no later
	// caller can read it back as a hit. This caller still returns the value it
	// computed: it asked before the location moved, and the next call gets the
	// new location's events.
	sc.lock.Lock()
	if existing, ok := sc.cache[dateKey]; ok && existing.generation == snap.generation {
		times = existing.times
	} else {
		if len(sc.cache) >= maxCacheEntries {
			clear(sc.cache)
		}
		sc.cache[dateKey] = cacheEntry{times: times, generation: snap.generation}
	}
	if m != nil {
		m.UpdateCacheSize(float64(len(sc.cache)))
	}
	sc.lock.Unlock()

	// Record successful operation and update sun time gauges
	if m != nil {
		m.RecordSunCalcOperation(sunEventsOperation, "success")
		m.RecordSunCalcDuration(sunEventsOperation, time.Since(start).Seconds())

		// Update sun time gauges for current day
		if dateKey == time.Now().In(snap.location).Format(time.DateOnly) {
			m.UpdateSunTimes(
				float64(times.Sunrise.Unix()),
				float64(times.Sunset.Unix()),
				float64(times.CivilDawn.Unix()),
				float64(times.CivilDusk.Unix()),
			)
		}
	}

	return times, nil
}

// calculateSunEventTimes calculates the sun event times for a given date.
// The observer and its timezone are passed in rather than read from the
// receiver so the whole calculation uses one consistent snapshot even if
// UpdateLocation runs concurrently.
func calculateSunEventTimes(observer astral.Observer, location *time.Location, date time.Time) (SunEventTimes, error) {
	// Calculate sunrise
	sunrise, err := astral.Sunrise(observer, date)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "calculate_sunrise").
			Build()
	}

	// Calculate sunset
	sunset, err := astral.Sunset(observer, date)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "calculate_sunset").
			Build()
	}

	// Convert sunrise and sunset from UTC to observer's local timezone
	localSunrise := sunrise.In(location)
	localSunset := sunset.In(location)

	// Try to calculate civil dawn, but fall back to sunrise if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDawn, err := astral.Dawn(observer, date, astral.DepressionCivil)
	var localCivilDawn time.Time
	if err != nil {
		localCivilDawn = localSunrise
	} else {
		localCivilDawn = civilDawn.In(location)
	}

	// Try to calculate civil dusk, but fall back to sunset if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDusk, err := astral.Dusk(observer, date, astral.DepressionCivil)
	var localCivilDusk time.Time
	if err != nil {
		localCivilDusk = localSunset
	} else {
		localCivilDusk = civilDusk.In(location)
	}

	return SunEventTimes{
		CivilDawn: localCivilDawn,
		Sunrise:   localSunrise,
		Sunset:    localSunset,
		CivilDusk: localCivilDusk,
	}, nil
}

// LocationName returns the IANA timezone name for the observer's location
// (e.g., "Australia/Sydney", "America/Los_Angeles").
func (sc *SunCalc) LocationName() string {
	return sc.snapshot().location.String()
}

// noonHour anchors a calendar date in the middle of its day. Midnight is the
// wrong anchor: GetSunEventTimes re-derives the calendar date in the observer's
// own timezone, so an instant on a day boundary resolves to the neighbouring
// date as soon as the caller's zone differs from the station's.
const noonHour = 12

// GetSunEventTimesForDate returns the sun events for a calendar date read in the
// station's own timezone. Only the year, month and day of date are used; its
// clock time and location are ignored.
//
// Callers that mean "the station's day of 2025-03-20" should prefer this over
// GetSunEventTimes, which takes an instant: handing that a date parsed in the
// server's timezone silently resolves to the previous or next station date
// whenever the two zones differ.
func (sc *SunCalc) GetSunEventTimesForDate(date time.Time) (SunEventTimes, error) {
	// One snapshot builds the anchor and resolves it, so the anchor can never be
	// constructed in one station's timezone and then evaluated against another's.
	snap := sc.snapshot()
	// Build noon on the station's calendar date directly rather than adding 12h to
	// its midnight, which lands at 11:00 or 13:00 across a DST transition.
	anchor := time.Date(date.Year(), date.Month(), date.Day(), noonHour, 0, 0, 0, snap.location)
	return sc.sunEventTimes(anchor, snap)
}

// GetSunriseTime returns the sunrise time for a given date
func (sc *SunCalc) GetSunriseTime(date time.Time) (time.Time, error) {
	sunEventTimes, err := sc.GetSunEventTimes(date)
	if err != nil {
		return time.Time{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "get_sunrise_time").
			Build()
	}
	return sunEventTimes.Sunrise, nil
}

// GetSunsetTime returns the sunset time for a given date
func (sc *SunCalc) GetSunsetTime(date time.Time) (time.Time, error) {
	sunEventTimes, err := sc.GetSunEventTimes(date)
	if err != nil {
		return time.Time{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "get_sunset_time").
			Build()
	}
	return sunEventTimes.Sunset, nil
}

// GetCivilDawn returns civil dawn for the given date and whether civil dawn is astronomically
// defined for it. ok is false when civil dawn does not occur: during polar day / white nights
// (civil twilight never happens, and GetSunEventTimes substitutes sunrise for civil dawn), or
// during polar night (the sun does not rise and the underlying calculation errors).
//
// Callers that must distinguish a genuine civil dawn from the sunrise fallback (for example the
// dawn-chorus onset analytics, which treat a day with no civil dawn as a gap) use this instead of
// reading GetSunEventTimes().CivilDawn directly. It reuses the GetSunEventTimes cache rather than
// recalculating, and detects the fallback by the fact that a genuine civil dawn is always strictly
// before sunrise, while GetSunEventTimes assigns CivilDawn = Sunrise (exact equality) when civil
// twilight cannot be computed.
func (sc *SunCalc) GetCivilDawn(date time.Time) (time.Time, bool) {
	times, err := sc.GetSunEventTimes(date)
	if err != nil {
		return time.Time{}, false
	}
	if !times.CivilDawn.Before(times.Sunrise) {
		return time.Time{}, false
	}
	return times.CivilDawn, true
}
