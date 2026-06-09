package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBirdNETMetrics_RecordEmbeddingExtraction(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m, err := NewBirdNETMetrics(reg)
	require.NoError(t, err)

	m.RecordEmbeddingExtraction("test-model", "success")
	m.RecordEmbeddingExtraction("test-model", "success")
	m.RecordEmbeddingExtraction("test-model", "unavailable")

	assert.InDelta(t, float64(2), testutil.ToFloat64(m.EmbeddingExtractionTotal.WithLabelValues("test-model", "success")), 0.0001)
	assert.InDelta(t, float64(1), testutil.ToFloat64(m.EmbeddingExtractionTotal.WithLabelValues("test-model", "unavailable")), 0.0001)

	m.SetEmbeddingDim("test-model", 1024)
	assert.InDelta(t, float64(1024), testutil.ToFloat64(m.EmbeddingDimGauge.WithLabelValues("test-model")), 0.0001)
}

func TestBirdNETMetrics_ClearEmbeddingDim(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	m, err := NewBirdNETMetrics(reg)
	require.NoError(t, err)

	m.SetEmbeddingDim("test-model", 1024)
	assert.InDelta(t, float64(1024), testutil.ToFloat64(m.EmbeddingDimGauge.WithLabelValues("test-model")), 0.0001)

	m.ClearEmbeddingDim("test-model")
	// After deletion the series no longer exists, so the gauge has zero metrics for that label set.
	assert.Equal(t, 0, testutil.CollectAndCount(m.EmbeddingDimGauge))
}

func TestBirdNETMetrics_RecordEmbeddingCapture(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m, err := NewBirdNETMetrics(reg)
	require.NoError(t, err)

	m.RecordEmbeddingCapture("persisted")
	m.RecordEmbeddingCapture("persisted")
	m.RecordEmbeddingCapture("dropped_queue_full")
	m.RecordEmbeddingPrune(5)
	m.RecordEmbeddingPrune(0) // no-op

	assert.InDelta(t, 2.0, testutil.ToFloat64(m.EmbeddingCaptureTotal.WithLabelValues("persisted")), 0.0001)
	assert.InDelta(t, 1.0, testutil.ToFloat64(m.EmbeddingCaptureTotal.WithLabelValues("dropped_queue_full")), 0.0001)
	assert.InDelta(t, 5.0, testutil.ToFloat64(m.EmbeddingPruneTotal), 0.0001)
}
