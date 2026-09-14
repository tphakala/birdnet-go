package suncalc

import (
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sydney, roughly antipodal to the Helsinki coordinates the shared test helper
// uses: a different hemisphere and a different IANA zone, so a location change
// moves both the sun events and the timezone they are reported in.
const (
	sydneyLatitude  = -33.8688
	sydneyLongitude = 151.2093
)

// TestUpdateLocationRecomputesEventsAndTimezone covers the hot-reload path for a
// station location edited in the UI: holders keep their *SunCalc pointer, so the
// observer, the derived timezone and the cached events must all follow the new
// coordinates rather than answering from the old ones until a restart.
func TestUpdateLocationRecomputesEventsAndTimezone(t *testing.T) {
	t.Parallel()

	date := midsummerDate()

	sc := newTestSunCalc()
	before, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	beforeZone := sc.LocationName()

	require.True(t, sc.UpdateLocation(sydneyLatitude, sydneyLongitude),
		"moving to different coordinates must report a change")

	after, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)

	assert.NotEqual(t, beforeZone, sc.LocationName(),
		"the timezone must be re-derived from the new coordinates")
	assert.False(t, before.Sunrise.Equal(after.Sunrise),
		"cached sun events must be discarded, not served from the previous location")
	assert.Equal(t, sc.LocationName(), after.Sunrise.Location().String(),
		"events must be reported in the new location's timezone")
}

// TestUpdateLocationIsNoOpForUnchangedCoordinates documents the contract callers
// rely on: the reconfigure path fires on a broad "settings changed" signal, so
// re-passing the current coordinates must neither report a change nor throw away
// a warm cache.
func TestUpdateLocationIsNoOpForUnchangedCoordinates(t *testing.T) {
	t.Parallel()

	date := midsummerDate()

	sc := newTestSunCalc()
	before, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	zone := sc.LocationName()

	assert.False(t, sc.UpdateLocation(testLatitude, testLongitude),
		"re-passing the current coordinates must report no change")

	after, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	assert.Equal(t, zone, sc.LocationName())
	assert.True(t, before.Sunrise.Equal(after.Sunrise))
}

// TestUpdateLocationUnderConcurrentReads exercises the observer/location fields
// being mutable rather than fixed at construction. Run with -race: reads used to
// take both fields without the lock, which is only safe while nothing writes
// them.
func TestUpdateLocationUnderConcurrentReads(t *testing.T) {
	sc := newTestSunCalc()

	const (
		readers  = 16
		duration = 200 * time.Millisecond
	)

	baseDate := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)

	var wg sync.WaitGroup
	// Release everyone together and start each deadline only once the goroutine
	// is actually running, so a slow CI worker cannot make this pass vacuously.
	start := make(chan struct{})

	for r := range readers {
		seed := r
		wg.Go(func() {
			<-start
			deadline := time.Now().Add(duration)
			i := seed
			for time.Now().Before(deadline) {
				_, _ = sc.GetSunEventTimes(baseDate.AddDate(0, 0, i%500))
				_ = sc.LocationName()
				i++
			}
		})
	}

	wg.Go(func() {
		<-start
		deadline := time.Now().Add(duration)
		i := 0
		for time.Now().Before(deadline) {
			if i%2 == 0 {
				sc.UpdateLocation(sydneyLatitude, sydneyLongitude)
			} else {
				sc.UpdateLocation(testLatitude, testLongitude)
			}
			i++
		}
	})

	close(start)
	wg.Wait()

	_, err := sc.GetSunEventTimes(baseDate)
	require.NoError(t, err, "the calculator must still be usable after concurrent location changes")
}

// TestUpdateLocationDuringInFlightCalculation is a regression test for a stale
// result repopulating the cache that UpdateLocation had just cleared.
//
// GetSunEventTimes snapshots the location, computes outside the lock, then takes
// the write lock to insert. An UpdateLocation landing in that window clears the
// cache and moves the station, and the in-flight call then inserted events
// computed for the *old* station under the date key. Nothing re-cleared the
// cache afterwards, so that date kept classifying against the previous
// coordinates until eviction or the next location change. Entries now carry the
// generation they were computed under, so a superseded one is never read back.
func TestUpdateLocationDuringInFlightCalculation(t *testing.T) {
	t.Parallel()

	date := midsummerDate()

	sc := NewSunCalc(testLatitude, testLongitude)

	// The events each station produces for this date, computed in isolation so
	// the expectation does not depend on the instance under test.
	helsinki, err := NewSunCalc(testLatitude, testLongitude).GetSunEventTimes(date)
	require.NoError(t, err)
	sydney, err := NewSunCalc(sydneyLatitude, sydneyLongitude).GetSunEventTimes(date)
	require.NoError(t, err)
	require.False(t, helsinki.Sunrise.Equal(sydney.Sunrise), "test setup needs two distinct locations")

	// Reproduce the window deterministically: take a snapshot for Helsinki, let
	// the move to Sydney happen, then finish the Helsinki-era call. This is what
	// an in-flight GetSunEventTimes does, with the interleaving forced.
	snap := sc.snapshot()
	require.True(t, sc.UpdateLocation(sydneyLatitude, sydneyLongitude))
	stale, err := sc.sunEventTimes(date, snap)
	require.NoError(t, err)
	assert.True(t, helsinki.Sunrise.Equal(stale.Sunrise),
		"the in-flight call returns what it computed, for the station it asked about")

	// The cache must not now serve that stale value to anyone else.
	fresh, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	assert.True(t, sydney.Sunrise.Equal(fresh.Sunrise),
		"a later caller must get the new station's events, not the stale insert")
	assert.False(t, helsinki.Sunrise.Equal(fresh.Sunrise),
		"the superseded entry must never be read back as a cache hit")
}
