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

func TestCacheConsistency(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	// Get times twice
	times1, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "failed to get initial sun event times")

	// Verify cache entry exists
	dateKey := date.Format(time.DateOnly)
	sc.lock.RLock()
	entry, exists := sc.cache[dateKey]
	sc.lock.RUnlock()

	assert.True(t, exists, "cache entry not found after calculation")

	assert.True(t, entry.date.Equal(date), "cached date doesn't match requested date")

	assert.True(t, entry.times.Sunrise.Equal(times1.Sunrise), "cached sunrise time doesn't match calculated time")
}
