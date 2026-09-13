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
