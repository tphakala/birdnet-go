// internal/suncalc/suncalc.go

package suncalc

import (
	"fmt"
	"sync"
	"time"

	"github.com/sj14/astral/pkg/astral"
	"github.com/tphakala/birdnet-go/internal/conf"
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
	cache    map[string]cacheEntry // Cache of sun event times for dates
	lock     sync.RWMutex          // Lock for cache access
	observer astral.Observer       // Observer for sun event calculations
}

// NewSunCalc creates a new SunCalc instance
func NewSunCalc(latitude, longitude float64) *SunCalc {
	return &SunCalc{
		cache:    make(map[string]cacheEntry),
		observer: astral.Observer{Latitude: latitude, Longitude: longitude},
	}
}

// GetSunEventTimes returns the sun event times for a given date, using cache if available
func (sc *SunCalc) GetSunEventTimes(date time.Time) (SunEventTimes, error) {
	// Format the date as a string key for the cache
	dateKey := date.Format("2006-01-02")

	// Acquire a read lock and check if the date is in the cache
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	sc.lock.RUnlock()

	// If the date exists in the cache and matches the requested date, return the cached times
	if exists && entry.date.Equal(date) {
		return entry.times, nil
	}

	// If not in cache, calculate the sun event times
	times, err := sc.calculateSunEventTimes(date)
	if err != nil {
		return SunEventTimes{}, err
	}

	// Acquire a write lock and update the cache with the new times
	sc.lock.Lock()
	sc.cache[dateKey] = cacheEntry{times: times, date: date}
	sc.lock.Unlock()

	// Return the calculated times
	return times, nil
}

// calculateSunEventTimes calculates the sun event times for a given date
func (sc *SunCalc) calculateSunEventTimes(date time.Time) (SunEventTimes, error) {
	// Calculate civil dawn
	civilDawn, err := astral.Dawn(sc.observer, date, astral.DepressionCivil)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate civil dawn: %w", err)
	}

	// Calculate sunrise
	sunrise, err := astral.Sunrise(sc.observer, date)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate sunrise: %w", err)
	}

	// Calculate sunset
	sunset, err := astral.Sunset(sc.observer, date)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate sunset: %w", err)
	}

	// Calculate civil dusk
	civilDusk, err := astral.Dusk(sc.observer, date, astral.DepressionCivil)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate civil dusk: %w", err)
	}

	// Convert civil dawn UTC to local time
	localCivilDawn, err := conf.ConvertUTCToLocal(civilDawn)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert civil dawn to local time: %w", err)
	}

	// Convert sunrise UTC to local time
	localSunrise, err := conf.ConvertUTCToLocal(sunrise)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert sunrise to local time: %w", err)
	}

	// Convert sunset UTC to local time
	localSunset, err := conf.ConvertUTCToLocal(sunset)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert sunset to local time: %w", err)
	}

	// Convert civil dusk UTC to local time
	localCivilDusk, err := conf.ConvertUTCToLocal(civilDusk)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert civil dusk to local time: %w", err)
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
		return time.Time{}, fmt.Errorf("failed to get sun event times: %w", err)
	}
	return sunEventTimes.Sunrise, nil
}

// GetSunsetTime returns the sunset time for a given date
func (sc *SunCalc) GetSunsetTime(date time.Time) (time.Time, error) {
	sunEventTimes, err := sc.GetSunEventTimes(date)
	if err != nil {
		return time.Time{}, fmt.Errorf("failed to get sun event times: %w", err)
	}
	return sunEventTimes.Sunset, nil
}
