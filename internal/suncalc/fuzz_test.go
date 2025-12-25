package suncalc

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// FuzzNewSunCalc tests NewSunCalc with arbitrary latitude/longitude values.
func FuzzNewSunCalc(f *testing.F) {
	// Seed with valid coordinates
	f.Add(60.1699, 24.9384)  // Helsinki
	f.Add(0.0, 0.0)          // Null Island
	f.Add(90.0, 0.0)         // North Pole
	f.Add(-90.0, 0.0)        // South Pole
	f.Add(0.0, 180.0)        // Dateline
	f.Add(0.0, -180.0)       // Dateline
	f.Add(89.999, 179.999)   // Near extremes
	f.Add(-89.999, -179.999) // Near extremes
	// Invalid coordinates
	f.Add(91.0, 0.0)         // Invalid latitude
	f.Add(0.0, 181.0)        // Invalid longitude
	f.Add(-91.0, -181.0)     // Both invalid

	f.Fuzz(func(t *testing.T, lat, lon float64) {
		// Skip NaN and Inf - these cause undefined behavior
		if math.IsNaN(lat) || math.IsNaN(lon) || math.IsInf(lat, 0) || math.IsInf(lon, 0) {
			return
		}

		sc := NewSunCalc(lat, lon)
		require.NotNil(t, sc, "NewSunCalc returned nil")

		// Verify coordinates are stored correctly
		assert.InDelta(t, lat, sc.observer.Latitude, 0.0001, "latitude not stored correctly")
		assert.InDelta(t, lon, sc.observer.Longitude, 0.0001, "longitude not stored correctly")

		// For invalid coordinates, GetSunEventTimes should either error or return reasonable values
		date := midsummerDate()
		times, err := sc.GetSunEventTimes(date)

		isValidLat := lat >= -90 && lat <= 90
		isValidLon := lon >= -180 && lon <= 180

		if isValidLat && isValidLon {
			// Valid coordinates should not error (polar regions may have zero times though)
			if err != nil {
				// Some errors are acceptable (polar night/day)
				t.Logf("valid coordinates (%v, %v) returned error: %v", lat, lon, err)
			}
		} else {
			// Invalid coordinates - we accept either error or graceful handling
			// Just verify no panic occurred
			_ = times
			_ = err
		}
	})
}

// FuzzGetSunEventTimes tests GetSunEventTimes with arbitrary dates.
func FuzzGetSunEventTimes(f *testing.F) {
	// Seed with various dates
	f.Add(int64(0))                    // Unix epoch
	f.Add(int64(1719014400))           // 2024-06-21 (midsummer)
	f.Add(int64(1703203200))           // 2023-12-21 (winter solstice)
	f.Add(int64(-62135596800))         // Year 1
	f.Add(int64(253402300799))         // Year 9999
	f.Add(int64(1000000000))           // 2001-09-09
	f.Add(int64(-1000000000))          // 1938-04-24

	f.Fuzz(func(t *testing.T, unixSec int64) {
		// Skip dates that would overflow time.Time
		if unixSec < -62135596800 || unixSec > 253402300799 {
			return
		}

		sc := newTestSunCalc()
		date := time.Unix(unixSec, 0).UTC()

		// GetSunEventTimes should not panic
		// It may return errors for extreme dates, which is acceptable
		times, err := sc.GetSunEventTimes(date)

		// If no error, verify we got a result (times struct is populated)
		// At polar regions, sunrise/sunset may be zero (polar night/day)
		// so we just verify no panic occurred
		_ = err
		_ = times
	})
}

// FuzzSunCalcCoordinatesAndDate tests the combination of coordinates and dates.
func FuzzSunCalcCoordinatesAndDate(f *testing.F) {
	// Seed with combinations
	f.Add(60.1699, 24.9384, int64(1719014400))  // Helsinki, midsummer
	f.Add(71.0, 25.0, int64(1719014400))        // Arctic, midsummer (midnight sun)
	f.Add(-71.0, 0.0, int64(1719014400))        // Antarctic, midsummer (polar night)
	f.Add(0.0, 0.0, int64(1719014400))          // Equator
	f.Add(90.0, 0.0, int64(1719014400))         // North Pole

	f.Fuzz(func(t *testing.T, lat, lon float64, unixSec int64) {
		// Skip dates that would overflow
		if unixSec < -62135596800 || unixSec > 253402300799 {
			return
		}

		sc := NewSunCalc(lat, lon)
		require.NotNil(t, sc, "NewSunCalc returned nil")

		date := time.Unix(unixSec, 0).UTC()

		// Should not panic
		_, _ = sc.GetSunEventTimes(date)
		_, _ = sc.GetSunriseTime(date)
		_, _ = sc.GetSunsetTime(date)
	})
}
