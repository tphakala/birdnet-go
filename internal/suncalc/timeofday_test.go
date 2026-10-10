package suncalc

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// sunEventsAt builds a SunEventTimes with sunrise and sunset at the given
// wall-clock times on a fixed base date in loc. Only Sunrise and Sunset are set;
// ClassifyTimeOfDay does not read the other fields.
func sunEventsAt(loc *time.Location, sunrise, sunset time.Duration) *SunEventTimes {
	base := time.Date(2025, 7, 15, 0, 0, 0, 0, loc)
	return &SunEventTimes{
		Sunrise: base.Add(sunrise),
		Sunset:  base.Add(sunset),
	}
}

// at builds a detection timestamp at the given wall-clock offset from midnight on
// the fixed base date in loc.
func at(loc *time.Location, offset time.Duration) time.Time {
	return time.Date(2025, 7, 15, 0, 0, 0, 0, loc).Add(offset)
}

const (
	hh = time.Hour
	mm = time.Minute
)

func TestClassifyTimeOfDay(t *testing.T) {
	t.Parallel()
	loc := time.UTC

	tests := []struct {
		name    string
		sunrise time.Duration
		sunset  time.Duration
		det     time.Duration
		want    string
	}{
		// Normal mid-latitude day: sunrise 06:00, sunset 21:00.
		{"midday is day", 6 * hh, 21 * hh, 12 * hh, "day"},
		{"exact sunrise", 6 * hh, 21 * hh, 6 * hh, "sunrise"},
		{"15m after sunrise is sunrise", 6 * hh, 21 * hh, 6*hh + 15*mm, "sunrise"},
		{"sunrise window end (+30m) is sunrise", 6 * hh, 21 * hh, 6*hh + 30*mm, "sunrise"},
		{"just past sunrise window (+31m) is day", 6 * hh, 21 * hh, 6*hh + 31*mm, "day"},
		{"sunrise window start (-30m) is sunrise", 6 * hh, 21 * hh, 5*hh + 30*mm, "sunrise"},
		{"just before sunrise window (-31m) is night", 6 * hh, 21 * hh, 5*hh + 29*mm, "night"},
		{"exact sunset", 6 * hh, 21 * hh, 21 * hh, "sunset"},
		{"sunset window start (-30m) is sunset", 6 * hh, 21 * hh, 20*hh + 30*mm, "sunset"},
		{"just before sunset window (-31m) is day", 6 * hh, 21 * hh, 20*hh + 29*mm, "day"},
		{"25m before sunset is sunset", 6 * hh, 21 * hh, 20*hh + 35*mm, "sunset"},
		{"sunset window end (+30m) is sunset", 6 * hh, 21 * hh, 21*hh + 30*mm, "sunset"},
		{"just past sunset window (+31m) is night", 6 * hh, 21 * hh, 21*hh + 31*mm, "night"},
		{"deep night before sunrise", 6 * hh, 21 * hh, 3 * hh, "night"},
		{"deep night after sunset", 6 * hh, 21 * hh, 23 * hh, "night"},

		// High-latitude summer: sunset 23:50 -> window [23:20, 00:20] crosses
		// midnight. This is the case the old lexical string comparison got wrong.
		{"midnight-crossing sunset, 5m before (23:55)", 3 * hh, 23*hh + 50*mm, 23*hh + 55*mm, "sunset"},
		{"midnight-crossing sunset, 20m after into next day (00:10)", 3 * hh, 23*hh + 50*mm, 10 * mm, "sunset"},
		{"midnight-crossing sunset, edge +30m (00:20)", 3 * hh, 23*hh + 50*mm, 20 * mm, "sunset"},
		{"midnight-crossing sunset, +31m is night (00:21)", 3 * hh, 23*hh + 50*mm, 21 * mm, "night"},
		{"midnight-crossing sunset, midday still day", 3 * hh, 23*hh + 50*mm, 12 * hh, "day"},
		{"midnight-crossing sunset, 25m before sunrise", 3 * hh, 23*hh + 50*mm, 2*hh + 35*mm, "sunrise"},

		// High-latitude: sunrise 00:15 -> window [23:45, 00:45] crosses midnight
		// backward.
		{"midnight-crossing sunrise, 5m after (00:20)", 15 * mm, 22 * hh, 20 * mm, "sunrise"},
		{"midnight-crossing sunrise, 25m before into prev evening (23:50)", 15 * mm, 22 * hh, 23*hh + 50*mm, "sunrise"},
		{"midnight-crossing sunrise, edge +30m (00:45)", 15 * mm, 22 * hh, 45 * mm, "sunrise"},
		{"midnight-crossing sunrise, +31m is day (00:46)", 15 * mm, 22 * hh, 46 * mm, "day"},

		// Summer inversion: local sunset falls after midnight (sunrise 02:02, sunset
		// 00:01), so sunset's wall-clock precedes sunrise's and the daytime arc wraps
		// midnight (Reykjavik/Oslo near the solstice).
		{"inversion, midday is day", 2*hh + 2*mm, 1 * mm, 12 * hh, "day"},
		{"inversion, late evening is day", 2*hh + 2*mm, 1 * mm, 23 * hh, "day"},
		{"inversion, 00:00 is sunset", 2*hh + 2*mm, 1 * mm, 0, "sunset"},
		{"inversion, exact sunrise", 2*hh + 2*mm, 1 * mm, 2*hh + 2*mm, "sunrise"},
		{"inversion, 01:00 is night", 2*hh + 2*mm, 1 * mm, 1 * hh, "night"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := ClassifyTimeOfDay(at(loc, tt.det), sunEventsAt(loc, tt.sunrise, tt.sunset))
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestClassifyTimeOfDay_PrioritySunriseOverDay verifies that when a timestamp
// falls in both the sunrise window and the daytime span, the sunrise window
// wins (transition windows are checked before the day span).
func TestClassifyTimeOfDay_PrioritySunriseOverDay(t *testing.T) {
	t.Parallel()
	loc := time.UTC
	sun := sunEventsAt(loc, 6*hh, 21*hh)
	// 06:20 is after sunrise (so inside [sunrise, sunset)) and within +30m window.
	assert.Equal(t, "sunrise", ClassifyTimeOfDay(at(loc, 6*hh+20*mm), sun))
}

// TestClassifyTimeOfDay_NilSunEvents documents the nil-guard: an unclassifiable
// timestamp yields the empty string rather than panicking.
func TestClassifyTimeOfDay_NilSunEvents(t *testing.T) {
	t.Parallel()
	assert.Empty(t, ClassifyTimeOfDay(time.Now(), nil))
}

// TestWithinWindow exercises the circular-distance helper directly, including the
// midnight wraparound that is the core of the fix.
func TestWithinWindow(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name   string
		tod    time.Duration
		center time.Duration
		radius time.Duration
		want   bool
	}{
		{"exact center", 6 * hh, 6 * hh, 30 * mm, true},
		{"within radius", 6*hh + 10*mm, 6 * hh, 30 * mm, true},
		{"at radius edge", 6*hh + 30*mm, 6 * hh, 30 * mm, true},
		{"just outside radius", 6*hh + 31*mm, 6 * hh, 30 * mm, false},
		{"far away not wrapped", 18 * hh, 6 * hh, 30 * mm, false},
		{"wraps forward across midnight", 10 * mm, 23*hh + 50*mm, 30 * mm, true},
		{"wraps backward across midnight", 23*hh + 50*mm, 15 * mm, 30 * mm, true},
		{"wrap edge exactly at radius", 20 * mm, 23*hh + 50*mm, 30 * mm, true},
		{"wrap just outside radius", 21 * mm, 23*hh + 50*mm, 30 * mm, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tt.want, withinWindow(tt.tod, tt.center, tt.radius))
		})
	}
}
