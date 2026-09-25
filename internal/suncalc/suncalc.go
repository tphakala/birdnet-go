// internal/suncalc/suncalc.go

// Package suncalc provides solar event calculations with support for high-latitude locations.
// For locations experiencing polar day conditions (like midsummer in Finland), where civil
// twilight cannot be calculated, the package gracefully falls back to sunrise/sunset times
// to prevent calculation errors from breaking application functionality.
package suncalc

import (
	"math"
	"sync"
	"sync/atomic"
	"time"

	"github.com/sj14/astral/pkg/astral"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// SunEventTimes holds the calculated sun event times in local time
type SunEventTimes struct {
	CivilDawn time.Time // Civil dawn in local time
	Sunrise   time.Time // Sunrise in local time
	Sunset    time.Time // Sunset in local time
	CivilDusk time.Time // Civil dusk in local time
}

// cacheEntry holds the cached sun event times for a given date
type cacheEntry struct {
	times SunEventTimes // Sun event times in local time
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

// CoordinateSource returns the observer coordinates a SunCalc should use right now. A SunCalc
// built with NewSunCalcWithSource calls it on every operation, so a station location changed at
// runtime (for example through the settings UI) takes effect on the next call without a restart.
// Implementations must be cheap and safe for concurrent use, and must not call back into the
// SunCalc: the source is also read while the SunCalc holds its swap lock.
type CoordinateSource func() (latitude, longitude float64)

// sunState is everything derived from one pair of observer coordinates: the astral observer, the
// timezone resolved from the coordinates, and the per-date cache of sun event times. Only the
// cache changes after construction. A location change swaps in a whole new state, so a
// calculation that started against the old coordinates can only store its result in the old,
// discarded cache.
type sunState struct {
	latitude  float64
	longitude float64
	observer  astral.Observer       // Observer for sun event calculations
	location  *time.Location        // Timezone derived from observer coordinates
	lock      sync.RWMutex          // Guards cache
	cache     map[string]cacheEntry // Cache of sun event times for dates
}

// newSunState builds the state for one coordinate pair, resolving its timezone.
func newSunState(latitude, longitude float64) *sunState {
	return &sunState{
		latitude:  latitude,
		longitude: longitude,
		observer:  astral.Observer{Latitude: latitude, Longitude: longitude},
		location:  resolveTimezone(latitude, longitude),
		cache:     make(map[string]cacheEntry),
	}
}

// SunCalc handles caching and calculation of sun event times
type SunCalc struct {
	source    CoordinateSource         // Live coordinates; nil for a fixed-location instance
	state     atomic.Pointer[sunState] // Coordinates, timezone and cache currently in effect
	swapMu    sync.Mutex               // Serializes state rebuilds after a location change
	metricsMu sync.RWMutex             // Guards metrics
	metrics   *metrics.SunCalcMetrics  // Metrics for observability
}

// NewSunCalc creates a SunCalc for a fixed observer location.
func NewSunCalc(latitude, longitude float64) *SunCalc {
	sc := &SunCalc{}
	sc.state.Store(newSunState(latitude, longitude))
	return sc
}

// NewSunCalcWithSource creates a SunCalc that follows the coordinates reported by source. The
// source is consulted on every operation; when it reports different coordinates, the SunCalc
// switches to them (resolving the new timezone and starting a fresh cache) before answering.
// Non-finite coordinates are ignored: initially they fall back to (0, 0), later the previous
// location is kept. A nil source yields a fixed-location instance at (0, 0).
func NewSunCalcWithSource(source CoordinateSource) *SunCalc {
	if source == nil {
		return NewSunCalc(0, 0)
	}
	latitude, longitude := source()
	if !finiteCoordinates(latitude, longitude) {
		latitude, longitude = 0, 0
	}
	sc := &SunCalc{source: source}
	sc.state.Store(newSunState(latitude, longitude))
	return sc
}

// finiteCoordinates reports whether both coordinates are finite numbers.
func finiteCoordinates(latitude, longitude float64) bool {
	return !math.IsNaN(latitude) && !math.IsInf(latitude, 0) &&
		!math.IsNaN(longitude) && !math.IsInf(longitude, 0)
}

// current returns the state for the coordinates in effect right now. When a coordinate source
// is set and reports a different, finite location, it builds and publishes a new state first.
func (sc *SunCalc) current() *sunState {
	st := sc.state.Load()
	if sc.source == nil {
		return st
	}
	latitude, longitude := sc.source()
	if (latitude == st.latitude && longitude == st.longitude) || !finiteCoordinates(latitude, longitude) {
		return st
	}

	sc.swapMu.Lock()
	defer sc.swapMu.Unlock()
	// Double-check against a fresh read: a concurrent caller may already have switched states
	// while this one waited, and the coordinates read before the lock may be stale by now.
	// Publishing them would overwrite a newer location, so decide on what the source reports now.
	st = sc.state.Load()
	latitude, longitude = sc.source()
	if (latitude == st.latitude && longitude == st.longitude) || !finiteCoordinates(latitude, longitude) {
		return st
	}
	st = newSunState(latitude, longitude)
	sc.state.Store(st)
	// Coordinates are PII, so the change is logged without the values themselves.
	GetLogger().Info("Observer location changed, rebuilt sun calculator state",
		logger.String("timezone", st.location.String()))
	return st
}

// currentMetrics returns the metrics instance, or nil when none is set.
func (sc *SunCalc) currentMetrics() *metrics.SunCalcMetrics {
	sc.metricsMu.RLock()
	defer sc.metricsMu.RUnlock()
	return sc.metrics
}

// SetMetrics sets the metrics instance for observability
func (sc *SunCalc) SetMetrics(m *metrics.SunCalcMetrics) {
	sc.metricsMu.Lock()
	defer sc.metricsMu.Unlock()
	sc.metrics = m
}

// GetSunEventTimes returns the sun event times for a given date, using cache if available
func (sc *SunCalc) GetSunEventTimes(date time.Time) (SunEventTimes, error) {
	start := time.Now()

	// Take one state snapshot so the observer, timezone and cache used below all
	// belong to the same coordinates, even if the location changes mid-call.
	st := sc.current()

	// Snapshot the metrics pointer once into a local so every metrics access
	// below operates on a stable value. SetMetrics writes sc.metrics under
	// metricsMu, so currentMetrics is the only synchronized read of the field;
	// capturing it once also closes the nil-panic window where a concurrent
	// SetMetrics(nil) could land between a "!= nil" check and the subsequent
	// method call.
	m := sc.currentMetrics()

	// Normalize date to observer timezone before generating cache key.
	// This ensures requests for the same local date hit the same cache entry,
	// even if the input time has a different timezone (e.g., UTC).
	localDate := date.In(st.location)
	dateKey := localDate.Format(time.DateOnly)

	// Acquire a read lock and check if the date is in the cache.
	st.lock.RLock()
	entry, exists := st.cache[dateKey]
	// Update cache size metric while holding the lock to avoid race condition
	if m != nil {
		m.UpdateCacheSize(float64(len(st.cache)))
	}
	st.lock.RUnlock()

	// If the date exists in the cache, return the cached times
	if exists {
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
	st.lock.RLock()
	if entry, ok := st.cache[dateKey]; ok {
		st.lock.RUnlock()
		if m != nil {
			m.RecordSunCalcCacheHit(sunEventsOperation)
			m.RecordSunCalcOperation(sunEventsOperation, "success")
			m.RecordSunCalcDuration(sunEventsOperation, time.Since(start).Seconds())
		}
		return entry.times, nil
	}
	st.lock.RUnlock()

	// Record cache miss only after the double-check confirms it
	if m != nil {
		m.RecordSunCalcCacheMiss(sunEventsOperation)
	}

	// Calculate outside the lock to avoid blocking readers.
	times, err := st.calculateSunEventTimes(localDate)
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
	st.lock.Lock()
	if existing, ok := st.cache[dateKey]; ok {
		times = existing.times
	} else {
		if len(st.cache) >= maxCacheEntries {
			clear(st.cache)
		}
		st.cache[dateKey] = cacheEntry{times: times}
	}
	if m != nil {
		m.UpdateCacheSize(float64(len(st.cache)))
	}
	st.lock.Unlock()

	// Record successful operation and update sun time gauges
	if m != nil {
		m.RecordSunCalcOperation(sunEventsOperation, "success")
		m.RecordSunCalcDuration(sunEventsOperation, time.Since(start).Seconds())

		// Update sun time gauges for current day
		if dateKey == time.Now().In(st.location).Format(time.DateOnly) {
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

// calculateSunEventTimes calculates the sun event times for a given date
func (st *sunState) calculateSunEventTimes(date time.Time) (SunEventTimes, error) {
	// Calculate sunrise
	sunrise, err := astral.Sunrise(st.observer, date)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "calculate_sunrise").
			Build()
	}

	// Calculate sunset
	sunset, err := astral.Sunset(st.observer, date)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "calculate_sunset").
			Build()
	}

	// Convert sunrise and sunset from UTC to observer's local timezone
	localSunrise := sunrise.In(st.location)
	localSunset := sunset.In(st.location)

	// Try to calculate civil dawn, but fall back to sunrise if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDawn, err := astral.Dawn(st.observer, date, astral.DepressionCivil)
	var localCivilDawn time.Time
	if err != nil {
		localCivilDawn = localSunrise
	} else {
		localCivilDawn = civilDawn.In(st.location)
	}

	// Try to calculate civil dusk, but fall back to sunset if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDusk, err := astral.Dusk(st.observer, date, astral.DepressionCivil)
	var localCivilDusk time.Time
	if err != nil {
		localCivilDusk = localSunset
	} else {
		localCivilDusk = civilDusk.In(st.location)
	}

	return SunEventTimes{
		CivilDawn: localCivilDawn,
		Sunrise:   localSunrise,
		Sunset:    localSunset,
		CivilDusk: localCivilDusk,
	}, nil
}

// LocationName returns the IANA timezone name for the observer's current location
// (e.g., "Australia/Sydney", "America/Los_Angeles"). For a SunCalc with a coordinate
// source it reflects the location the source reports at the time of the call, unless that
// location is non-finite, in which case the previous location is kept.
func (sc *SunCalc) LocationName() string {
	return sc.current().location.String()
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
