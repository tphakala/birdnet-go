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
	"github.com/tphakala/birdnet-go/internal/conf"
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
	date  time.Time     // Date for which the sun event times are cached
}

// SunCalc handles caching and calculation of sun event times
type SunCalc struct {
	cache    map[string]cacheEntry   // Cache of sun event times for dates
	lock     sync.RWMutex            // Lock for cache access
	observer astral.Observer         // Observer for sun event calculations
	metrics  *metrics.SunCalcMetrics // Metrics for observability
}

// NewSunCalc creates a new SunCalc instance
func NewSunCalc(latitude, longitude float64) *SunCalc {
	return &SunCalc{
		cache:    make(map[string]cacheEntry),
		observer: astral.Observer{Latitude: latitude, Longitude: longitude},
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

	// Format the date as a string key for the cache
	dateKey := date.Format(time.DateOnly)

	// Acquire a read lock and check if the date is in the cache
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	// Update cache size metric while holding the lock to avoid race condition
	if sc.metrics != nil {
		sc.metrics.UpdateCacheSize(float64(len(sc.cache)))
	}
	sc.lock.RUnlock()

	// If the date exists in the cache and matches the requested date, return the cached times
	if exists && entry.date.Equal(date) {
		if sc.metrics != nil {
			sc.metrics.RecordSunCalcCacheHit("get_sun_events")
			sc.metrics.RecordSunCalcOperation("get_sun_events", "success")
			sc.metrics.RecordSunCalcDuration("get_sun_events", time.Since(start).Seconds())
		}
		return entry.times, nil
	}

	// Record cache miss
	if sc.metrics != nil {
		sc.metrics.RecordSunCalcCacheMiss("get_sun_events")
	}

	// If not in cache, calculate the sun event times
	times, err := sc.calculateSunEventTimes(date)
	if err != nil {
		if sc.metrics != nil {
			sc.metrics.RecordSunCalcOperation("get_sun_events", "error")
			sc.metrics.RecordSunCalcError("get_sun_events", "calculation_error")
		}
		return SunEventTimes{}, err
	}

	// Acquire a write lock and update the cache with the new times
	sc.lock.Lock()
	sc.cache[dateKey] = cacheEntry{times: times, date: date}
	sc.lock.Unlock()

	// Record successful operation and update sun time gauges
	if sc.metrics != nil {
		sc.metrics.RecordSunCalcOperation("get_sun_events", "success")
		sc.metrics.RecordSunCalcDuration("get_sun_events", time.Since(start).Seconds())

		// Update sun time gauges for current day
		// Compare dates in the same location to handle time zone correctly
		now := time.Now()
		if date.Year() == now.Year() && date.YearDay() == now.YearDay() {
			sc.metrics.UpdateSunTimes(
				float64(times.Sunrise.Unix()),
				float64(times.Sunset.Unix()),
				float64(times.CivilDawn.Unix()),
				float64(times.CivilDusk.Unix()),
			)
		}
	}

	// Return the calculated times
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

	// Convert sunrise UTC to local time
	localSunrise, err := conf.ConvertUTCToLocal(sunrise)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryConfiguration).
			Context("operation", "convert_sunrise_to_local").
			Build()
	}

	// Convert sunset UTC to local time
	localSunset, err := conf.ConvertUTCToLocal(sunset)
	if err != nil {
		return SunEventTimes{}, errors.New(err).
			Component("suncalc").
			Category(errors.CategoryConfiguration).
			Context("operation", "convert_sunset_to_local").
			Build()
	}

	// Try to calculate civil dawn, but fall back to sunrise if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDawn, err := astral.Dawn(sc.observer, date, astral.DepressionCivil)
	var localCivilDawn time.Time
	if err != nil {
		// Civil dawn calculation failed (likely due to polar day conditions)
		// Fall back to using sunrise time
		localCivilDawn = localSunrise
	} else {
		// Convert civil dawn UTC to local time
		localCivilDawn, err = conf.ConvertUTCToLocal(civilDawn)
		if err != nil {
			// If conversion fails, fall back to sunrise
			localCivilDawn = localSunrise
		}
	}

	// Try to calculate civil dusk, but fall back to sunset if it fails
	// (this handles polar day conditions like midsummer in high latitudes)
	civilDusk, err := astral.Dusk(sc.observer, date, astral.DepressionCivil)
	var localCivilDusk time.Time
	if err != nil {
		// Civil dusk calculation failed (likely due to polar day conditions)
		// Fall back to using sunset time
		localCivilDusk = localSunset
	} else {
		// Convert civil dusk UTC to local time
		localCivilDusk, err = conf.ConvertUTCToLocal(civilDusk)
		if err != nil {
			// If conversion fails, fall back to sunset
			localCivilDusk = localSunset
		}
	}

	// Return the calculated sun event times
	return SunEventTimes{
		CivilDawn: localCivilDawn,
		Sunrise:   localSunrise,
		Sunset:    localSunset,
		CivilDusk: localCivilDusk,
	}, nil
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
