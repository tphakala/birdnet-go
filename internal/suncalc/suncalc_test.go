package suncalc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewSunCalc(t *testing.T) {
	sc := newTestSunCalc()
	require.NotNil(t, sc, "NewSunCalc returned nil")

	assert.InDelta(t, testLatitude, sc.observer.Latitude, 0.0001, "expected latitude to match")
	assert.InDelta(t, testLongitude, sc.observer.Longitude, 0.0001, "expected longitude to match")
}

func TestGetSunEventTimes(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	// First call to calculate and cache
	times1, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "failed to get sun event times")

	// Verify times are not zero
	assert.False(t, times1.Sunrise.IsZero(), "sunrise time is zero")
	assert.False(t, times1.Sunset.IsZero(), "sunset time is zero")
	assert.False(t, times1.CivilDawn.IsZero(), "civil dawn time is zero")
	assert.False(t, times1.CivilDusk.IsZero(), "civil dusk time is zero")

	// Second call to test cache
	times2, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "failed to get cached sun event times")

	// Verify cached times match original times
	assert.True(t, times1.Sunrise.Equal(times2.Sunrise), "cached sunrise time doesn't match original")
	assert.True(t, times1.Sunset.Equal(times2.Sunset), "cached sunset time doesn't match original")
}

func TestGetSunriseTime(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	sunrise, err := sc.GetSunriseTime(date)
	require.NoError(t, err, "failed to get sunrise time")

	assert.False(t, sunrise.IsZero(), "sunrise time is zero")
}

func TestGetSunsetTime(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	sunset, err := sc.GetSunsetTime(date)
	require.NoError(t, err, "failed to get sunset time")

	assert.False(t, sunset.IsZero(), "sunset time is zero")
}

func TestLocationName(t *testing.T) {
	sc := newTestSunCalc()

	name := sc.LocationName()
	assert.Equal(t, "Europe/Helsinki", name, "expected IANA timezone for Helsinki coordinates")

	// Sydney coordinates
	scSydney := NewSunCalc(-33.8688, 151.2093)
	assert.Equal(t, "Australia/Sydney", scSydney.LocationName(), "expected IANA timezone for Sydney coordinates")
}

func TestCacheConsistency(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	// Get times twice
	times1, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "failed to get initial sun event times")

	// Verify cache entry exists using the normalized local date key
	dateKey := date.In(sc.location).Format(time.DateOnly)
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	sc.lock.RUnlock()

	assert.True(t, exists, "cache entry not found after calculation")

	assert.True(t, entry.times.Sunrise.Equal(times1.Sunrise), "cached sunrise time doesn't match calculated time")
}

func TestCacheEviction(t *testing.T) {
	sc := newTestSunCalc()

	// Fill cache beyond maxCacheEntries
	baseDate := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range maxCacheEntries {
		d := baseDate.AddDate(0, 0, i)
		_, err := sc.GetSunEventTimes(d)
		require.NoError(t, err)
	}

	sc.lock.RLock()
	assert.Len(t, sc.cache, maxCacheEntries, "cache should be at capacity")
	sc.lock.RUnlock()

	// One more entry should trigger eviction (clear + re-add)
	nextDate := baseDate.AddDate(0, 0, maxCacheEntries)
	_, err := sc.GetSunEventTimes(nextDate)
	require.NoError(t, err)

	sc.lock.RLock()
	assert.Len(t, sc.cache, 1, "cache should contain only the new entry after eviction")
	sc.lock.RUnlock()
}

func TestConcurrentAccess(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	// Run many goroutines requesting the same date concurrently.
	// This exercises the double-check pattern and verifies no races.
	const goroutines = 50
	results := make(chan SunEventTimes, goroutines)
	errs := make(chan error, goroutines)

	for range goroutines {
		go func() {
			times, err := sc.GetSunEventTimes(date)
			if err != nil {
				errs <- err
				return
			}
			results <- times
		}()
	}

	var first SunEventTimes
	for i := range goroutines {
		select {
		case err := <-errs:
			t.Fatalf("goroutine %d returned error: %v", i, err)
		case times := <-results:
			if i == 0 {
				first = times
			} else {
				assert.True(t, first.Sunrise.Equal(times.Sunrise),
					"goroutine %d returned different sunrise", i)
			}
		}
	}
}

// TestGetSunEventTimesForDateUsesTheStationCalendarDate covers the date-based
// entry point. GetSunEventTimes takes an instant and re-derives the calendar day
// in the observer's own zone, so callers holding a date parsed somewhere else
// (a UTC-parsed API parameter, say) would silently get the neighbouring day.
// This entry point reads only the year/month/day and resolves them at the
// station.
func TestGetSunEventTimesForDateUsesTheStationCalendarDate(t *testing.T) {
	t.Parallel()

	// Honolulu, UTC-10: midnight UTC on any date is still the afternoon before
	// there, which is the case the instant-based call gets wrong.
	const (
		honoluluLatitude  = 21.3069
		honoluluLongitude = -157.8583
	)

	sc := NewSunCalc(honoluluLatitude, honoluluLongitude)
	stationZone, err := time.LoadLocation(sc.LocationName())
	require.NoError(t, err)

	const dateStr = "2025-03-20"
	midnightUTC, err := time.ParseInLocation(time.DateOnly, dateStr, time.UTC)
	require.NoError(t, err)

	times, err := sc.GetSunEventTimesForDate(midnightUTC)
	require.NoError(t, err)

	assert.Equal(t, dateStr, times.Sunrise.In(stationZone).Format(time.DateOnly),
		"sunrise must fall on the requested station date")
	assert.Equal(t, dateStr, times.Sunset.In(stationZone).Format(time.DateOnly),
		"sunset must fall on the requested station date")

	// The instant-based call on the same value is off by a day here; that gap is
	// the whole reason this entry point exists.
	viaInstant, err := sc.GetSunEventTimes(midnightUTC)
	require.NoError(t, err)
	assert.Equal(t, "2025-03-19", viaInstant.Sunrise.In(stationZone).Format(time.DateOnly),
		"the instant-based call is expected to resolve the previous station date here")
}

// TestGetSunEventTimesForDateIgnoresTheInputClockAndZone documents that only the
// calendar fields of the argument are read, so callers need not normalize it.
func TestGetSunEventTimesForDateIgnoresTheInputClockAndZone(t *testing.T) {
	t.Parallel()

	sc := newTestSunCalc()

	fromMidnightUTC, err := sc.GetSunEventTimesForDate(
		time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC))
	require.NoError(t, err)

	fromLateEveningElsewhere, err := sc.GetSunEventTimesForDate(
		time.Date(2024, 6, 21, 23, 45, 0, 0, time.FixedZone("elsewhere", -9*3600)))
	require.NoError(t, err)

	assert.True(t, fromMidnightUTC.Sunrise.Equal(fromLateEveningElsewhere.Sunrise))
	assert.True(t, fromMidnightUTC.Sunset.Equal(fromLateEveningElsewhere.Sunset))
}
