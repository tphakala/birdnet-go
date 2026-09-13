package datastore

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Two stations in different hemispheres and timezones, so a move between them
// changes both the sun events and the zone they are reported in.
const (
	helsinkiLatitude  = 60.1699
	helsinkiLongitude = 24.9384
	sydneyLatitude    = -33.8688
	sydneyLongitude   = 151.2093
)

// TestReconfigureSunCalcInvalidatesCachedSunTimes covers the hot-reload path for
// the station location: the datastore builds its SunCalc once at startup and
// memoizes each date's events, so without an explicit invalidation a location
// edited in the UI would leave time-of-day classification answering from the old
// observer until the process restarted.
func TestReconfigureSunCalcInvalidatesCachedSunTimes(t *testing.T) {
	t.Parallel()

	const dateStr = "2024-06-21"

	ds := &DataStore{SunCalc: suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)}

	before, err := ds.getSunEventsForDate(dateStr)
	require.NoError(t, err)
	_, cached := ds.getCachedSunTimes(dateStr)
	require.True(t, cached, "test setup must leave a warm cache entry to invalidate")

	ds.ReconfigureSunCalc(sydneyLatitude, sydneyLongitude)

	_, stillCached := ds.getCachedSunTimes(dateStr)
	assert.False(t, stillCached, "a coordinate change must drop the memoized sun times")

	after, err := ds.getSunEventsForDate(dateStr)
	require.NoError(t, err)
	assert.False(t, before.Sunrise.Equal(after.Sunrise),
		"sun events must be recomputed for the new station location")
}

// TestReconfigureSunCalcKeepsCacheForUnchangedCoordinates documents that the
// reconfigure path is safe to call from a broader "settings changed" signal:
// re-passing the current coordinates must not throw away a warm cache.
func TestReconfigureSunCalcKeepsCacheForUnchangedCoordinates(t *testing.T) {
	t.Parallel()

	const dateStr = "2024-06-21"

	ds := &DataStore{SunCalc: suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)}

	before, err := ds.getSunEventsForDate(dateStr)
	require.NoError(t, err)

	ds.ReconfigureSunCalc(helsinkiLatitude, helsinkiLongitude)

	after, cached := ds.getCachedSunTimes(dateStr)
	assert.True(t, cached, "unchanged coordinates must leave the cache intact")
	assert.True(t, before.Sunrise.Equal(after.Sunrise))
}

// TestReconfigureSunCalcWithoutSunCalc guards the nil case: a datastore built
// without a SunCalc (no location configured) must ignore the signal rather than
// panic on the reconfigure path.
func TestReconfigureSunCalcWithoutSunCalc(t *testing.T) {
	t.Parallel()

	ds := &DataStore{}
	assert.NotPanics(t, func() {
		ds.ReconfigureSunCalc(sydneyLatitude, sydneyLongitude)
	})
}

// TestGetSunEventsForDateRejectsMalformedDate covers the parse guard added when
// the lookup stopped taking a row timestamp: a date string the datastore cannot
// parse must surface an error instead of silently classifying against some other
// day's events.
func TestGetSunEventsForDateRejectsMalformedDate(t *testing.T) {
	t.Parallel()

	ds := &DataStore{SunCalc: suncalc.NewSunCalc(helsinkiLatitude, helsinkiLongitude)}

	_, err := ds.getSunEventsForDate("not-a-date")
	require.Error(t, err)
}

// TestGetSunEventsForDateIsIndependentOfRowOrder is the unit-level counterpart to
// TestTimeOfDayFilterAgreesWithClassifiedLabel: the memoized events for a date
// used to be seeded from whichever row was scanned first, so two queries over the
// same date could classify against events a day apart. The lookup now depends on
// the date alone.
func TestGetSunEventsForDateIsIndependentOfRowOrder(t *testing.T) {
	t.Parallel()

	const dateStr = "2024-06-21"

	// A station far from any plausible server timezone, so the server-local date
	// and the station-local date do not trivially coincide.
	first := &DataStore{SunCalc: suncalc.NewSunCalc(sydneyLatitude, sydneyLongitude)}
	second := &DataStore{SunCalc: suncalc.NewSunCalc(sydneyLatitude, sydneyLongitude)}

	fromFirst, err := first.getSunEventsForDate(dateStr)
	require.NoError(t, err)
	fromSecond, err := second.getSunEventsForDate(dateStr)
	require.NoError(t, err)

	assert.True(t, fromFirst.Sunrise.Equal(fromSecond.Sunrise))
	assert.True(t, fromFirst.Sunset.Equal(fromSecond.Sunset))

	// And they are the same events the SQL filter bounds that date with.
	localDate, err := time.ParseInLocation(time.DateOnly, dateStr, time.Local)
	require.NoError(t, err)
	expected, err := first.SunCalc.GetSunEventTimes(sunEventAnchor(localDate))
	require.NoError(t, err)
	assert.True(t, expected.Sunrise.Equal(fromFirst.Sunrise))
	assert.True(t, expected.Sunset.Equal(fromFirst.Sunset))
}
