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

	// Sun times first: this call alone must notice the change (LocationName would swap first).
	sydney, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)
	assert.Equal(t, "Australia/Sydney", sc.LocationName(), "timezone must follow the new location")
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

	coords.set(sydneyLatitude, sydneyLongitude)
	assert.Equal(t, "Australia/Sydney", sc.LocationName(),
		"a valid location after non-finite ones must still be picked up")
}

// TestGetSunEventTimes_SingleSnapshotPerCall pins that one GetSunEventTimes call takes one
// state snapshot and computes from it. The scripted source reports Sydney for the first
// operation's reads (the read before the swap lock and the re-read under it) and Helsinki on
// every later read, so a call that took a second snapshot would switch back and return
// Helsinki times.
func TestGetSunEventTimes_SingleSnapshotPerCall(t *testing.T) {
	const firstOperationReads = 2 // pre-lock read plus the re-read under the swap lock
	calls := 0
	source := func() (float64, float64) {
		calls++
		if calls > 1 && calls <= 1+firstOperationReads {
			return sydneyLatitude, sydneyLongitude
		}
		return testLatitude, testLongitude
	}
	sc := NewSunCalcWithSource(source)
	require.Equal(t, 1, calls, "construction reads the source once")

	date := midsummerDate()
	got, err := sc.GetSunEventTimes(date)
	require.NoError(t, err)

	assert.Equal(t, 1+firstOperationReads, calls,
		"one GetSunEventTimes call must read the source only for its single swap (pre-lock read plus in-lock re-read)")
	want, err := NewSunCalc(sydneyLatitude, sydneyLongitude).GetSunEventTimes(date)
	require.NoError(t, err)
	assert.True(t, want.Sunrise.Equal(got.Sunrise), "sunrise must come from the state the call read")
	assert.True(t, want.CivilDusk.Equal(got.CivilDusk), "civil dusk must come from the state the call read")
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

func TestNewSunCalc_KeepsState(t *testing.T) {
	sc := NewSunCalc(testLatitude, testLongitude)
	assert.Nil(t, sc.source, "a fixed-location SunCalc has no coordinate source")
	first := sc.current()
	_, err := sc.GetSunEventTimes(midsummerDate())
	require.NoError(t, err)
	assert.Same(t, first, sc.current())
}

// TestLiveLocationRaceUnderConcurrency flips the location while many goroutines read sun data
// and another flips the metrics instance. Run with -race, it checks that reads never race with
// a swap, that every result equals what a fixed-location calculator returns for one of the two
// locations (a result mixing the two fails), and that readers actually saw both locations.
func TestLiveLocationRaceUnderConcurrency(t *testing.T) {
	const (
		readers     = 8
		duration    = 200 * time.Millisecond
		maxDuration = 10 * time.Second
		days        = 30
	)
	baseDate := midsummerDate()

	// Precompute each location's results. This also resolves both timezones up front, so the
	// first swap does not spend the whole window loading timezone data.
	helsinki := NewSunCalc(testLatitude, testLongitude)
	sydney := NewSunCalc(sydneyLatitude, sydneyLongitude)
	var wantHelsinki, wantSydney [days]SunEventTimes
	for i := range days {
		d := baseDate.AddDate(0, 0, i)
		var err error
		wantHelsinki[i], err = helsinki.GetSunEventTimes(d)
		require.NoError(t, err)
		wantSydney[i], err = sydney.GetSunEventTimes(d)
		require.NoError(t, err)
	}
	// time.Equal ignores the zone, so compare the zone too: a result re-zoned into the other
	// location's timezone must not match.
	same := func(got, want time.Time) bool {
		return got.Equal(want) && got.Location().String() == want.Location().String()
	}
	matches := func(got, want *SunEventTimes) bool {
		return same(got.Sunrise, want.Sunrise) && same(got.Sunset, want.Sunset) &&
			same(got.CivilDawn, want.CivilDawn) && same(got.CivilDusk, want.CivilDusk)
	}

	coords := newTestCoordinates(testLatitude, testLongitude)
	sc := NewSunCalcWithSource(coords.source)
	m := newTestMetrics(t)

	start := make(chan struct{})
	stop := make(chan struct{}) // closed once every reader is done; ends the writers
	var readersWG, writersWG sync.WaitGroup
	var reads, failures, sawHelsinki, sawSydney atomic.Int64

	// Readers run for at least duration and keep going (up to maxDuration) until both
	// locations have been observed, so a slow or loaded machine cannot end the window before
	// any swap was seen.
	keepReading := func(deadline, hardDeadline time.Time) bool {
		now := time.Now()
		if now.Before(deadline) {
			return true
		}
		return now.Before(hardDeadline) && (sawHelsinki.Load() == 0 || sawSydney.Load() == 0)
	}

	for r := range readers {
		seed := r
		readersWG.Go(func() {
			<-start
			deadline := time.Now().Add(duration)
			hardDeadline := time.Now().Add(maxDuration)
			i := seed
			for keepReading(deadline, hardDeadline) {
				day := i % days
				d := baseDate.AddDate(0, 0, day)
				// require cannot stop the test from a spawned goroutine, so count
				// failures here and assert on the total after the goroutines finish.
				times, err := sc.GetSunEventTimes(d)
				if err != nil || (!matches(&times, &wantHelsinki[day]) && !matches(&times, &wantSydney[day])) {
					failures.Add(1)
				}
				_, _ = sc.GetCivilDawn(d)
				switch sc.LocationName() {
				case "Europe/Helsinki":
					sawHelsinki.Add(1)
				case "Australia/Sydney":
					sawSydney.Add(1)
				default:
					failures.Add(1)
				}
				reads.Add(1)
				i++
			}
		})
	}
	writersWG.Go(func() {
		<-start
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				coords.set(sydneyLatitude, sydneyLongitude)
			} else {
				coords.set(testLatitude, testLongitude)
			}
			runtime.Gosched()
		}
	})
	writersWG.Go(func() {
		<-start
		for i := 0; ; i++ {
			select {
			case <-stop:
				return
			default:
			}
			if i%2 == 0 {
				sc.SetMetrics(m)
			} else {
				sc.SetMetrics(nil)
			}
			runtime.Gosched()
		}
	})

	close(start)
	readersWG.Wait()
	close(stop)
	writersWG.Wait()
	assert.Positive(t, reads.Load(), "readers must have run")
	assert.Zero(t, failures.Load(), "every concurrent read must match one location's sun event times")
	assert.Positive(t, sawHelsinki.Load(), "readers must have seen the Helsinki state")
	assert.Positive(t, sawSydney.Load(), "readers must have seen the Sydney state")
}
