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
