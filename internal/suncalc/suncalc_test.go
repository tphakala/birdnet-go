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
