package suncalc

import (
	"math"
	"runtime"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Sydney coordinates: a second location in a different timezone and hemisphere, so its sunrise
// differs from the Helsinki test location on the same date.
const (
	sydneyLatitude  = -33.8688
	sydneyLongitude = 151.2093
)

// New York coordinates: a third location, for tests that need two successive changes.
const (
	newYorkLatitude  = 40.7128
	newYorkLongitude = -74.006
)

// testCoordinates is a concurrency-safe coordinate source for tests.
type testCoordinates struct {
	mu        sync.Mutex
	latitude  float64
	longitude float64
	calls     atomic.Int64
}

func newTestCoordinates(latitude, longitude float64) *testCoordinates {
	return &testCoordinates{latitude: latitude, longitude: longitude}
}

func (c *testCoordinates) set(latitude, longitude float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.latitude, c.longitude = latitude, longitude
}

func (c *testCoordinates) source() (latitude, longitude float64) {
	c.calls.Add(1)
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.latitude, c.longitude
}

func TestNewSunCalcWithSource_FollowsLocationChange(t *testing.T) {
	coords := newTestCoordinates(testLatitude, testLongitude)
	sc := NewSunCalcWithSource(coords.source)

	date := midsummerDate()
	helsinki, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	assert.Equal(t, "Europe/Helsinki", sc.LocationName())

	coords.set(sydneyLatitude, sydneyLongitude)

	assert.Equal(t, "Australia/Sydney", sc.LocationName(), "timezone must follow the new location")
	sydney, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	assert.False(t, helsinki.Sunrise.Equal(sydney.Sunrise),
		"sunrise must be recalculated for the new location, not served from the old cache")

	want, err := NewSunCalc(sydneyLatitude, sydneyLongitude).GetSunEventTimes(date)
	require.NoError(t, err)
	assert.True(t, want.Sunrise.Equal(sydney.Sunrise), "sunrise must match a fixed Sydney calculator")
	assert.True(t, want.CivilDawn.Equal(sydney.CivilDawn), "civil dawn must match a fixed Sydney calculator")

	st := sc.current()
	assert.InDelta(t, sydneyLatitude, st.observer.Latitude, 0.0001)
	assert.InDelta(t, sydneyLongitude, st.observer.Longitude, 0.0001)
	st.lock.RLock()
	assert.Len(t, st.cache, 1, "the new location must start from a fresh cache")
	st.lock.RUnlock()
}

func TestNewSunCalcWithSource_UnchangedLocationKeepsState(t *testing.T) {
	coords := newTestCoordinates(testLatitude, testLongitude)
	sc := NewSunCalcWithSource(coords.source)

	first := sc.current()
	_, err := sc.GetSunEventTimes(midsummerDate())
	require.NoError(t, err)

	second := sc.current()
	assert.Same(t, first, second, "unchanged coordinates must not rebuild the state")
	second.lock.RLock()
	assert.Len(t, second.cache, 1, "the cache must survive calls with unchanged coordinates")
	second.lock.RUnlock()
}

// TestNewSunCalcWithSource_NoStaleOverwrite pins that a caller holding coordinates it read
// before a concurrent swap cannot publish them over the newer state. The scripted source makes
// the outer call read B and, from inside that same read, drives a nested call that swaps to the
// newest location C. When the outer call then takes the swap lock it must see that the source
// now reports C and keep C, not overwrite it with its stale B.
func TestNewSunCalcWithSource_NoStaleOverwrite(t *testing.T) {
	var sc *SunCalc
	var newest *sunState
	calls := 0
	source := func() (float64, float64) {
		calls++
		switch calls {
		case 1: // construction: Helsinki (A)
			return testLatitude, testLongitude
		case 2: // outer pre-lock read: Sydney (B), then a newer location lands first
			newest = sc.current()
			return sydneyLatitude, sydneyLongitude
		default: // every later read, including the nested swap: New York (C)
			return newYorkLatitude, newYorkLongitude
		}
	}
	sc = NewSunCalcWithSource(source)

	_ = sc.current()

	// Inspect the published state directly: calling current() again would consult the source
	// and repair a stale overwrite, hiding it.
	st := sc.state.Load()
	assert.InDelta(t, newYorkLatitude, st.observer.Latitude, 0.0001,
		"a stale pre-lock read must not overwrite the newer location")
	assert.Equal(t, "America/New_York", st.location.String())
	assert.Same(t, newest, st, "the state the nested call published must be kept, not rebuilt")
}

// TestNewSunCalcWithSource_NonFiniteInLockKeepsState pins that when the source reports a new,
// finite location before the swap lock but a non-finite one once the lock is held, the swap is
// abandoned and the previous state stays published.
func TestNewSunCalcWithSource_NonFiniteInLockKeepsState(t *testing.T) {
	calls := 0
	source := func() (float64, float64) {
		calls++
		switch calls {
		case 1: // construction: Helsinki
			return testLatitude, testLongitude
		case 2: // pre-lock read: Sydney
			return sydneyLatitude, sydneyLongitude
		default: // in-lock read: not a number
			return math.NaN(), math.NaN()
		}
	}
	sc := NewSunCalcWithSource(source)
	before := sc.state.Load()

	_ = sc.current()

	assert.Same(t, before, sc.state.Load(), "a non-finite in-lock read must keep the previous state")
}

func TestNewSunCalcWithSource_IgnoresNonFiniteCoordinates(t *testing.T) {
	coords := newTestCoordinates(testLatitude, testLongitude)
	sc := NewSunCalcWithSource(coords.source)
	before := sc.current()

	for _, bad := range [][2]float64{
		{math.NaN(), testLongitude},
		{testLatitude, math.Inf(1)},
		{math.Inf(-1), math.NaN()},
	} {
		coords.set(bad[0], bad[1])
		assert.Same(t, before, sc.current(), "non-finite coordinates must keep the previous state")
	}
	assert.Equal(t, "Europe/Helsinki", sc.LocationName())
}

func TestNewSunCalcWithSource_NonFiniteInitialCoordinates(t *testing.T) {
	sc := NewSunCalcWithSource(func() (float64, float64) { return math.NaN(), math.NaN() })
	st := sc.current()
	assert.Zero(t, st.observer.Latitude, "non-finite initial coordinates fall back to (0, 0)")
	assert.Zero(t, st.observer.Longitude, "non-finite initial coordinates fall back to (0, 0)")
}

func TestNewSunCalcWithSource_NilSourceIsFixed(t *testing.T) {
	sc := NewSunCalcWithSource(nil)
	require.NotNil(t, sc)
	_, err := sc.GetSunEventTimes(midsummerDate())
	require.NoError(t, err)
	st := sc.current()
	assert.Zero(t, st.observer.Latitude)
	assert.Zero(t, st.observer.Longitude)
}

func TestNewSunCalc_DoesNotConsultSource(t *testing.T) {
	sc := NewSunCalc(testLatitude, testLongitude)
	assert.Nil(t, sc.source, "a fixed-location SunCalc has no coordinate source")
	first := sc.current()
	_, err := sc.GetSunEventTimes(midsummerDate())
	require.NoError(t, err)
	assert.Same(t, first, sc.current())
}

// TestLiveLocationRaceUnderConcurrency flips the location while many goroutines read sun data
// and another flips the metrics instance. Run with -race: every read must see one consistent
// state and never race with a swap.
func TestLiveLocationRaceUnderConcurrency(t *testing.T) {
	coords := newTestCoordinates(testLatitude, testLongitude)
	sc := NewSunCalcWithSource(coords.source)
	m := newTestMetrics(t)

	const (
		readers  = 8
		duration = 200 * time.Millisecond
	)
	baseDate := midsummerDate()
	start := make(chan struct{})
	var wg sync.WaitGroup
	var reads, failures atomic.Int64

	for r := range readers {
		seed := r
		wg.Go(func() {
			<-start
			deadline := time.Now().Add(duration)
			i := seed
			for time.Now().Before(deadline) {
				d := baseDate.AddDate(0, 0, i%30)
				// require cannot stop the test from a spawned goroutine, so count
				// failures here and assert on the total after the goroutines finish.
				if times, err := sc.GetSunEventTimes(d); err != nil || times.Sunrise.IsZero() {
					failures.Add(1)
				}
				_, _ = sc.GetCivilDawn(d)
				_ = sc.LocationName()
				reads.Add(1)
				i++
			}
		})
	}
	wg.Go(func() {
		<-start
		deadline := time.Now().Add(duration)
		for i := 0; time.Now().Before(deadline); i++ {
			if i%2 == 0 {
				coords.set(sydneyLatitude, sydneyLongitude)
			} else {
				coords.set(testLatitude, testLongitude)
			}
			runtime.Gosched()
		}
	})
	wg.Go(func() {
		<-start
		deadline := time.Now().Add(duration)
		for i := 0; time.Now().Before(deadline); i++ {
			if i%2 == 0 {
				sc.SetMetrics(m)
			} else {
				sc.SetMetrics(nil)
			}
		}
	})

	close(start)
	wg.Wait()
	assert.Positive(t, reads.Load(), "readers must have run")
	assert.Zero(t, failures.Load(), "every concurrent read must return valid sun event times")
}
