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

// cacheEntry holds the cached sun event times for a given date
type cacheEntry struct {
	times SunEventTimes // Sun event times in local time
}

// maxCacheEntries caps the number of cached dates to prevent unbounded
// memory growth. 400 entries covers over a year of daily lookups; when
// exceeded the entire cache is cleared so the working set rebuilds
// organically from live traffic.
const maxCacheEntries = 400

// SunCalc handles caching and calculation of sun event times
type SunCalc struct {
	cache    map[string]cacheEntry   // Cache of sun event times for dates
	lock     sync.RWMutex            // Lock for cache access
	observer astral.Observer         // Observer for sun event calculations
	location *time.Location          // Timezone derived from observer coordinates
	metrics  *metrics.SunCalcMetrics // Metrics for observability
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

// GetSunEventTimes returns the sun event times for a given date, using cache if available
func (sc *SunCalc) GetSunEventTimes(date time.Time) (SunEventTimes, error) {
	start := time.Now()

	// Normalize date to observer timezone before generating cache key.
	// This ensures requests for the same local date hit the same cache entry,
	// even if the input time has a different timezone (e.g., UTC).
	localDate := date.In(sc.location)
	dateKey := localDate.Format(time.DateOnly)

	// Acquire a read lock and check if the date is in the cache
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	// Update cache size metric while holding the lock to avoid race condition
	if sc.metrics != nil {
		sc.metrics.UpdateCacheSize(float64(len(sc.cache)))
	}
	sc.lock.RUnlock()

	// If the date exists in the cache, return the cached times
	if exists {
		if sc.metrics != nil {
			sc.metrics.RecordSunCalcCacheHit("get_sun_events")
			sc.metrics.RecordSunCalcOperation("get_sun_events", "success")
			sc.metrics.RecordSunCalcDuration("get_sun_events", time.Since(start).Seconds())
		}
		return entry.times, nil
	}

	// Double-check under write lock: another goroutine may have populated
	// the cache between the RLock check and now. This reduces (but does
	// not fully eliminate) redundant calculations under high concurrency,
	// which is acceptable since calculateSunEventTimes is pure math.
	sc.lock.Lock()
	if entry, ok := sc.cache[dateKey]; ok {
		sc.lock.Unlock()
		if sc.metrics != nil {
			sc.metrics.RecordSunCalcCacheHit("get_sun_events")
			sc.metrics.RecordSunCalcOperation("get_sun_events", "success")
			sc.metrics.RecordSunCalcDuration("get_sun_events", time.Since(start).Seconds())
		}
		return entry.times, nil
	}
	sc.lock.Unlock()

	// Record cache miss only after the double-check confirms it
	if sc.metrics != nil {
		sc.metrics.RecordSunCalcCacheMiss("get_sun_events")
	}

	// Calculate outside the lock to avoid blocking readers.
	times, err := sc.calculateSunEventTimes(localDate)
	if err != nil {
		if sc.metrics != nil {
			sc.metrics.RecordSunCalcOperation("get_sun_events", "error")
			sc.metrics.RecordSunCalcError("get_sun_events", "calculation_error")
		}
		return SunEventTimes{}, err
	}

	// Store result and enforce cache size limit.
	sc.lock.Lock()
	if len(sc.cache) >= maxCacheEntries {
		clear(sc.cache)
	}
	sc.cache[dateKey] = cacheEntry{times: times}
	if sc.metrics != nil {
		sc.metrics.UpdateCacheSize(float64(len(sc.cache)))
	}
	sc.lock.Unlock()

	// Record successful operation and update sun time gauges
	if sc.metrics != nil {
		sc.metrics.RecordSunCalcOperation("get_sun_events", "success")
		sc.metrics.RecordSunCalcDuration("get_sun_events", time.Since(start).Seconds())

		// Update sun time gauges for current day
		if dateKey == time.Now().In(sc.location).Format(time.DateOnly) {
			sc.metrics.UpdateSunTimes(
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
func (sc *SunCalc) calculateSunEventTimes(date time.Time) (SunEventTimes, error) {
	// Calculate sunrise
	sunrise, err := astral.Sunrise(sc.observer, date)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "calculate_sunrise").
			Build()
	}

	// Calculate sunset
	sunset, err := astral.Sunset(sc.observer, date)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryGeneric).
			Context("operation", "calculate_sunset").
			Build()
	}

	// Convert sunrise and sunset from UTC to observer's local timezone
	localSunrise := sunrise.In(sc.location)
	localSunset := sunset.In(sc.location)

	// Try to calculate civil dawn, but fall back to sunrise if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDawn, err := astral.Dawn(sc.observer, date, astral.DepressionCivil)
	var localCivilDawn time.Time
	if err != nil {
		localCivilDawn = localSunrise
	} else {
		localCivilDawn = civilDawn.In(sc.location)
	}

	// Try to calculate civil dusk, but fall back to sunset if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDusk, err := astral.Dusk(sc.observer, date, astral.DepressionCivil)
	var localCivilDusk time.Time
	if err != nil {
		localCivilDusk = localSunset
	} else {
		localCivilDusk = civilDusk.In(sc.location)
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
	return sc.location.String()
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
