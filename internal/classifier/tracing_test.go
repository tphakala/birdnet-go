package classifier

import (
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// resetGlobalMetrics resets the package-level metrics state so each test
// starts with a clean slate. Only safe in tests (same package).
func resetGlobalMetrics(t *testing.T) {
	t.Helper()
	globalMetrics.Store(nil)
	metricsOnce = sync.Once{}
}

// newTestMetrics creates a BirdNETMetrics instance backed by a throw-away
// Prometheus registry, suitable for unit tests.
func newTestMetrics(t *testing.T) *metrics.BirdNETMetrics {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := metrics.NewBirdNETMetrics(reg)
	require.NoError(t, err, "creating test metrics must succeed")
	return m
}

func TestSetMetrics_StoresAndReturnsInstance(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })

	// Before SetMetrics, getMetrics should return nil.
	assert.Nil(t, getMetrics(), "getMetrics must return nil before SetMetrics is called")

	m := newTestMetrics(t)
	SetMetrics(m)

	got := getMetrics()
	require.NotNil(t, got, "getMetrics must return the stored instance after SetMetrics")
	assert.Same(t, m, got, "getMetrics must return the exact same pointer passed to SetMetrics")
}

func TestSetMetrics_IsIdempotent(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })

	first := newTestMetrics(t)
	second := newTestMetrics(t)

	SetMetrics(first)
	SetMetrics(second) // should be ignored

	got := getMetrics()
	assert.Same(t, first, got, "second call to SetMetrics must be ignored; first instance should persist")
}

func TestGetMetrics_IsSafeForConcurrentAccess(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })

	m := newTestMetrics(t)
	SetMetrics(m)

	// Spin up several goroutines that all read concurrently.
	const goroutines = 64
	results := make(chan *metrics.BirdNETMetrics, goroutines)

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			results <- getMetrics()
		})
	}
	wg.Wait()
	close(results)

	for got := range results {
		assert.Same(t, m, got, "concurrent getMetrics must always return the stored instance")
	}
}
