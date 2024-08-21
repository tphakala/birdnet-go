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
	CivilDawn time.Time
	Sunrise   time.Time
	Sunset    time.Time
	CivilDusk time.Time
}

type cacheEntry struct {
	times SunEventTimes
	date  time.Time
}

// SunCalc handles caching and calculation of sun event times
type SunCalc struct {
	cache    map[string]cacheEntry
	lock     sync.RWMutex
	observer astral.Observer
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
	dateKey := date.Format("2006-01-02")

	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	sc.lock.RUnlock()

	if exists && entry.date.Equal(date) {
		return entry.times, nil
	}

	times, err := sc.calculateSunEventTimes(date)
	if err != nil {
		return SunEventTimes{}, err
	}

	sc.lock.Lock()
	sc.cache[dateKey] = cacheEntry{times: times, date: date}
	sc.lock.Unlock()

	return times, nil
}

// calculateSunEventTimes calculates the sun event times for a given date
func (sc *SunCalc) calculateSunEventTimes(date time.Time) (SunEventTimes, error) {
	civilDawn, err := astral.Dawn(sc.observer, date, astral.DepressionCivil)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate civil dawn: %w", err)
	}

	sunrise, err := astral.Sunrise(sc.observer, date)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate sunrise: %w", err)
	}

	sunset, err := astral.Sunset(sc.observer, date)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate sunset: %w", err)
	}

	civilDusk, err := astral.Dusk(sc.observer, date, astral.DepressionCivil)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to calculate civil dusk: %w", err)
	}

	// Convert UTC times to local time
	localCivilDawn, err := conf.ConvertUTCToLocal(civilDawn)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert civil dawn to local time: %w", err)
	}

	localSunrise, err := conf.ConvertUTCToLocal(sunrise)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert sunrise to local time: %w", err)
	}

	localSunset, err := conf.ConvertUTCToLocal(sunset)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert sunset to local time: %w", err)
	}

	localCivilDusk, err := conf.ConvertUTCToLocal(civilDusk)
	if err != nil {
		return SunEventTimes{}, fmt.Errorf("failed to convert civil dusk to local time: %w", err)
	}

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
