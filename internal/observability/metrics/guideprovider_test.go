package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newTestGuideMetrics builds a GuideProviderMetrics backed by a fresh registry so
// each test is isolated from the global default registry.
func newTestGuideMetrics(t *testing.T) *GuideProviderMetrics {
	t.Helper()
	m, err := NewGuideProviderMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	require.NotNil(t, m)
	return m
}

func TestNewGuideProviderMetrics_RegistersSuccessfully(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	// All metric vectors/collectors must be initialized.
	assert.NotNil(t, m.CacheHits)
	assert.NotNil(t, m.CacheMisses)
	assert.NotNil(t, m.Fetches)
	assert.NotNil(t, m.FetchDuration)
	assert.NotNil(t, m.DBErrors)
	assert.NotNil(t, m.NegativeEntries)
	assert.NotNil(t, m.CachePopulationRatio)
}

func TestNewGuideProviderMetrics_DuplicateRegistrationFails(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()

	_, err := NewGuideProviderMetrics(reg)
	require.NoError(t, err)

	// Registering a second collector on the same registry must fail.
	_, err = NewGuideProviderMetrics(reg)
	require.Error(t, err)
}

func TestGuideProviderMetrics_RecordCacheHit(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	m.RecordCacheHit("memory", "full")
	m.RecordCacheHit("memory", "full")
	m.RecordCacheHit("db", "stub")

	assert.InDelta(t, 2, testutil.ToFloat64(m.CacheHits.WithLabelValues("memory", "full")), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.CacheHits.WithLabelValues("db", "stub")), 0.0001)
}

func TestGuideProviderMetrics_RecordCacheMiss(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	m.RecordCacheMiss("memory")
	m.RecordCacheMiss("db")
	m.RecordCacheMiss("db")

	assert.InDelta(t, 1, testutil.ToFloat64(m.CacheMisses.WithLabelValues("memory")), 0.0001)
	assert.InDelta(t, 2, testutil.ToFloat64(m.CacheMisses.WithLabelValues("db")), 0.0001)
}

func TestGuideProviderMetrics_RecordFetch(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	m.RecordFetch("wikipedia", "success", 0.12)
	m.RecordFetch("wikipedia", "success", 0.34)
	m.RecordFetch("ebird", "not_found", 0.05)

	assert.InDelta(t, 2, testutil.ToFloat64(m.Fetches.WithLabelValues("wikipedia", "success")), 0.0001)
	assert.InDelta(t, 1, testutil.ToFloat64(m.Fetches.WithLabelValues("ebird", "not_found")), 0.0001)
	// FetchDuration histogram observed two samples for the success outcome.
	assert.Equal(t, 2, testutil.CollectAndCount(m.FetchDuration))
}

func TestGuideProviderMetrics_RecordDBError(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	m.RecordDBError("timeout", "get")

	assert.InDelta(t, 1, testutil.ToFloat64(m.DBErrors.WithLabelValues("timeout", "get")), 0.0001)
}

func TestGuideProviderMetrics_RecordNegativeEntry(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	m.RecordNegativeEntry()
	m.RecordNegativeEntry()
	m.RecordNegativeEntry()

	assert.InDelta(t, 3, testutil.ToFloat64(m.NegativeEntries), 0.0001)
}

func TestGuideProviderMetrics_UpdateCachePopulationRatio(t *testing.T) {
	t.Parallel()
	m := newTestGuideMetrics(t)

	m.UpdateCachePopulationRatio(0.5)
	assert.InDelta(t, 0.5, testutil.ToFloat64(m.CachePopulationRatio), 0.0001)

	// A gauge is set, not accumulated: a second update replaces the value.
	m.UpdateCachePopulationRatio(0.75)
	assert.InDelta(t, 0.75, testutil.ToFloat64(m.CachePopulationRatio), 0.0001)
}
