package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNewPrivacyFilterMetrics(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	m, err := NewPrivacyFilterMetrics(registry)
	require.NoError(t, err)
	require.NotNil(t, m)
}

func TestPrivacyFilterMetrics_RecordDiscard(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	m, err := NewPrivacyFilterMetrics(registry)
	require.NoError(t, err)

	m.RecordDiscard(TriggerLabel)
	m.RecordDiscard(TriggerVAD)
	m.RecordDiscard(TriggerVAD)

	assert.InDelta(t, 1.0, testutil.ToFloat64(m.discardsTotal.WithLabelValues(TriggerLabel)), 0.0001)
	assert.InDelta(t, 2.0, testutil.ToFloat64(m.discardsTotal.WithLabelValues(TriggerVAD)), 0.0001)
}

func TestPrivacyFilterMetrics_VADRecorders(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	m, err := NewPrivacyFilterMetrics(registry)
	require.NoError(t, err)

	m.RecordVADSpeechHit()
	m.RecordVADSpeechHit()
	m.ObserveVADInference(0.004)
	m.SetVADLastSpeechProbability(0.73)

	assert.InDelta(t, 2.0, testutil.ToFloat64(m.vadSpeechHitsTotal), 0.0001)
	assert.InDelta(t, 0.73, testutil.ToFloat64(m.vadLastSpeechProbability), 0.0001)
	assert.Equal(t, 1, testutil.CollectAndCount(m.vadInferenceDuration), "one histogram series")
}

// TestPrivacyFilterMetrics_Collect proves the collector exposes every metric it
// owns, so a metric silently dropped from Describe/Collect (and thus from
// /metrics) is caught rather than passing by asserting fields directly.
func TestPrivacyFilterMetrics_Collect(t *testing.T) {
	t.Parallel()
	registry := prometheus.NewRegistry()
	m, err := NewPrivacyFilterMetrics(registry)
	require.NoError(t, err)

	// Give each metric a value so counters/histograms produce a series.
	m.RecordDiscard(TriggerLabel)
	m.RecordVADSpeechHit()
	m.ObserveVADInference(0.003)
	m.SetVADLastSpeechProbability(0.4)

	// discardsTotal(1) + vadSpeechHitsTotal(1) + vadInferenceDuration(1) +
	// vadLastSpeechProbability(1) = 4 series exposed through the registered collector.
	assert.Equal(t, 4, testutil.CollectAndCount(m), "all four metrics must be collected")
}

// TestPrivacyFilterMetrics_NilSafe proves the recorders no-op on a nil group so
// callers only guard on the group pointer, not each metric.
func TestPrivacyFilterMetrics_NilSafe(t *testing.T) {
	t.Parallel()
	var m *PrivacyFilterMetrics
	assert.NotPanics(t, func() {
		m.RecordDiscard(TriggerVAD)
		m.RecordVADSpeechHit()
		m.ObserveVADInference(0.01)
		m.SetVADLastSpeechProbability(0.5)
	})
}
