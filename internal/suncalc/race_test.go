package suncalc

import (
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// newTestMetrics builds a SunCalcMetrics backed by a private registry so it
// never collides with other tests or the global registry.
func newTestMetrics(t *testing.T) *metrics.SunCalcMetrics {
	t.Helper()
	m, err := metrics.NewSunCalcMetrics(prometheus.NewRegistry())
	require.NoError(t, err, "failed to create suncalc metrics")
	return m
}

// TestMetricsRaceUnderConcurrency exercises the data race and nil-panic window
// on sc.metrics: many goroutines call GetSunEventTimes (which reads sc.metrics
// outside the lock) while another goroutine flips sc.metrics between a real
// instance and nil via SetMetrics (which writes under the write lock).
//
// Run with -race. Before the fix this both races (read-without-lock vs
// write-under-lock) and can nil-panic when SetMetrics(nil) interleaves between
// the "if sc.metrics != nil" check and the subsequent method call.
func TestMetricsRaceUnderConcurrency(t *testing.T) {
	sc := newTestSunCalc()
	m := newTestMetrics(t)

	const (
		readers  = 16
		flippers = 4
		duration = 200 * time.Millisecond
	)

	baseDate := time.Date(2024, 1, 1, 12, 0, 0, 0, time.UTC)
	deadline := time.Now().Add(duration)

	var wg sync.WaitGroup

	// Readers: hammer GetSunEventTimes across many dates (mix of hits and
	// misses) so every metrics access path is exercised.
	for r := range readers {
		seed := r
		wg.Go(func() {
			i := seed
			for time.Now().Before(deadline) {
				d := baseDate.AddDate(0, 0, i%500)
				_, _ = sc.GetSunEventTimes(d)
				i++
			}
		})
	}

	// Flippers: toggle sc.metrics between a live instance and nil to open the
	// nil-panic window and force the write side of the race.
	for f := range flippers {
		seed := f
		wg.Go(func() {
			i := seed
			for time.Now().Before(deadline) {
				if i%2 == 0 {
					sc.SetMetrics(m)
				} else {
					sc.SetMetrics(nil)
				}
				i++
			}
		})
	}

	wg.Wait()

	// Leave metrics set so a final sanity call works.
	sc.SetMetrics(m)
	_, err := sc.GetSunEventTimes(baseDate)
	require.NoError(t, err, "sanity GetSunEventTimes after race loop failed")
}

// TestCacheClearThunderingHerd drives many goroutines to insert the SAME new
// date concurrently while the cache sits one below capacity. This is the bug
// from the issue: several goroutines compute the same missing date, the first
// inserts it (bringing the cache to capacity), and without the store-path
// double-check a slightly later goroutine sees the cache full and calls
// clear(), wiping the entry the first just inserted (and returning a value that
// is no longer cached). The fix reuses the existing entry and skips clear()
// when the date is already present, so the new date must survive and the cache
// must stay at capacity. A start barrier releases the goroutines together to
// maximize store-path overlap; running under -count or -race stresses the
// window further.
func TestCacheClearThunderingHerd(t *testing.T) {
	sc := newTestSunCalc()

	// Prime the cache to one below capacity so the first concurrent insert
	// reaches the clear threshold.
	baseDate := time.Date(2020, 1, 1, 12, 0, 0, 0, time.UTC)
	for i := range maxCacheEntries - 1 {
		_, err := sc.GetSunEventTimes(baseDate.AddDate(0, 0, i))
		require.NoError(t, err)
	}

	const goroutines = 64
	var wg sync.WaitGroup
	results := make(chan SunEventTimes, goroutines)
	errs := make(chan error, goroutines)
	start := make(chan struct{})

	// All goroutines insert the same brand-new date concurrently.
	newDate := baseDate.AddDate(0, 0, maxCacheEntries+1000)
	for range goroutines {
		wg.Go(func() {
			<-start // release all goroutines at once
			times, err := sc.GetSunEventTimes(newDate)
			if err != nil {
				errs <- err
				return
			}
			results <- times
		})
	}

	close(start)
	wg.Wait()
	close(results)
	close(errs)

	for err := range errs {
		require.NoError(t, err, "concurrent GetSunEventTimes returned error")
	}

	// All returned values must agree.
	var first SunEventTimes
	got := false
	for times := range results {
		if !got {
			first = times
			got = true
			continue
		}
		require.True(t, first.Sunrise.Equal(times.Sunrise),
			"concurrent goroutines returned different sunrise")
	}
	require.True(t, got, "expected at least one result")

	// The new date must still be cached and the cache must remain exactly at
	// capacity: the first goroutine inserts newDate and the rest reuse it via
	// the store-path double-check, so no clear() ever runs. On the buggy code a
	// later goroutine clears the full cache and reinserts, collapsing it to a
	// single entry, so both assertions below fail. This makes the test a real
	// regression guard rather than a no-op.
	newKey := newDate.In(sc.location).Format(time.DateOnly)
	sc.lock.RLock()
	_, ok := sc.cache[newKey]
	size := len(sc.cache)
	sc.lock.RUnlock()
	require.True(t, ok, "new date %s was wiped from the cache by a thundering-herd clear()", newKey)
	require.Equal(t, maxCacheEntries, size, "cache was inappropriately cleared by the thundering herd")
}
