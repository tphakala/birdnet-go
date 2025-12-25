package suncalc

import "time"

// Helsinki coordinates for testing
const (
	testLatitude  = 60.1699
	testLongitude = 24.9384
)

// newTestSunCalc creates a SunCalc instance with Helsinki coordinates.
func newTestSunCalc() *SunCalc {
	return NewSunCalc(testLatitude, testLongitude)
}

// midsummerDate returns June 21, 2024 UTC - a date with predictable sun events.
func midsummerDate() time.Time {
	return time.Date(2024, 6, 21, 0, 0, 0, 0, time.UTC)
}
