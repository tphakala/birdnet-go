package suncalc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestResolveTimezone_Helsinki(t *testing.T) {
	loc := resolveTimezone(testLatitude, testLongitude)
	require.NotNil(t, loc, "resolveTimezone returned nil")
	assert.Equal(t, "Europe/Helsinki", loc.String(), "expected Europe/Helsinki timezone for Helsinki coordinates")
}

func TestResolveTimezone_NewYork(t *testing.T) {
	loc := resolveTimezone(40.7128, -74.0060)
	require.NotNil(t, loc, "resolveTimezone returned nil")
	assert.Equal(t, "America/New_York", loc.String(), "expected America/New_York timezone")
}

func TestResolveTimezone_Tokyo(t *testing.T) {
	loc := resolveTimezone(35.6762, 139.6503)
	require.NotNil(t, loc, "resolveTimezone returned nil")
	assert.Equal(t, "Asia/Tokyo", loc.String(), "expected Asia/Tokyo timezone")
}

func TestResolveTimezone_FallbackOnZeroCoordinates(t *testing.T) {
	loc := resolveTimezone(0.0, 0.0)
	require.NotNil(t, loc, "resolveTimezone returned nil for (0,0)")
}

func TestNewSunCalc_StoresLocation(t *testing.T) {
	sc := NewSunCalc(testLatitude, testLongitude)
	require.NotNil(t, sc.location, "SunCalc.location is nil after construction")
	assert.Equal(t, "Europe/Helsinki", sc.location.String(), "SunCalc.location should be Europe/Helsinki")
}

func TestSunEventTimes_HelsinkiSummerTimezone(t *testing.T) {
	sc := newTestSunCalc()
	date := midsummerDate()

	times, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "GetSunEventTimes failed")

	_, offset := times.Sunrise.Zone()
	assert.Equal(t, 3*3600, offset, "sunrise should be in EEST (UTC+3) for Helsinki midsummer, got offset %d", offset)

	_, offset = times.Sunset.Zone()
	assert.Equal(t, 3*3600, offset, "sunset should be in EEST (UTC+3) for Helsinki midsummer, got offset %d", offset)

	_, offset = times.CivilDawn.Zone()
	assert.Equal(t, 3*3600, offset, "civil dawn should be in EEST (UTC+3) for Helsinki midsummer, got offset %d", offset)

	_, offset = times.CivilDusk.Zone()
	assert.Equal(t, 3*3600, offset, "civil dusk should be in EEST (UTC+3) for Helsinki midsummer, got offset %d", offset)
}

func TestSunEventTimes_HelsinkiWinterTimezone(t *testing.T) {
	sc := newTestSunCalc()
	date := time.Date(2024, 12, 21, 0, 0, 0, 0, time.UTC)

	times, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "GetSunEventTimes failed")

	_, offset := times.Sunrise.Zone()
	assert.Equal(t, 2*3600, offset, "sunrise should be in EET (UTC+2) for Helsinki winter, got offset %d", offset)
}

func TestSunEventTimes_IndependentOfSystemTZ(t *testing.T) {
	sc := NewSunCalc(testLatitude, testLongitude)
	date := midsummerDate()

	times, err := sc.GetSunEventTimes(date)
	require.NoError(t, err, "GetSunEventTimes failed")

	_, offset := times.Sunrise.Zone()
	assert.NotEqual(t, 0, offset, "sunrise offset must not be 0 (UTC) for Helsinki coordinates")
}

func TestCacheKey_NormalizedToObserverTimezone(t *testing.T) {
	sc := newTestSunCalc()
	dateUTC := time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)
	dateLater := time.Date(2024, 6, 21, 5, 0, 0, 0, time.UTC)

	times1, err := sc.GetSunEventTimes(dateUTC)
	require.NoError(t, err)

	times2, err := sc.GetSunEventTimes(dateLater)
	require.NoError(t, err)

	assert.True(t, times1.Sunrise.Equal(times2.Sunrise),
		"same local date should return same sunrise from cache")
}

func TestCacheKey_DateBoundaryNearMidnightUTC(t *testing.T) {
	sc := newTestSunCalc()
	lateUTC := time.Date(2024, 6, 20, 23, 0, 0, 0, time.UTC)

	times, err := sc.GetSunEventTimes(lateUTC)
	require.NoError(t, err)

	localSunrise := times.Sunrise
	assert.Equal(t, 21, localSunrise.Day(),
		"sunrise day should be 21 (local date) not 20 (UTC date)")
}
