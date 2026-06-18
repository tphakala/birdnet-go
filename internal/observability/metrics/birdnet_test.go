package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestInferenceGauges(t *testing.T) {
	t.Parallel()
	reg := prometheus.NewRegistry()
	m, err := NewBirdNETMetrics(reg)
	require.NoError(t, err, "NewBirdNETMetrics")

	m.SetInferenceRTF("BirdNET_V2.4", 0.016)
	m.SetModelRSSBytes("BirdNET_V2.4", 125_000_000)

	mfs, err := reg.Gather()
	require.NoError(t, err, "Gather")

	byName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		byName[mf.GetName()] = mf
	}

	// Verify birdnet_inference_rtf is present and has the expected sample.
	require.Contains(t, byName, "birdnet_inference_rtf", "birdnet_inference_rtf not registered/exposed")
	rtfFound := findGaugeValue(byName["birdnet_inference_rtf"], "model", "BirdNET_V2.4")
	require.NotNil(t, rtfFound, "expected birdnet_inference_rtf sample with label model=BirdNET_V2.4")
	assert.InDelta(t, 0.016, *rtfFound, 1e-9, "birdnet_inference_rtf value for BirdNET_V2.4")

	// Verify birdnet_model_rss_bytes is present and has the expected sample.
	require.Contains(t, byName, "birdnet_model_rss_bytes", "birdnet_model_rss_bytes not registered/exposed")
	rssFound := findGaugeValue(byName["birdnet_model_rss_bytes"], "model", "BirdNET_V2.4")
	require.NotNil(t, rssFound, "expected birdnet_model_rss_bytes sample with label model=BirdNET_V2.4")
	assert.InDelta(t, 1.25e8, *rssFound, 1.0, "birdnet_model_rss_bytes value for BirdNET_V2.4")
}

// findGaugeValue searches a MetricFamily for a metric with the given label name/value
// and returns its gauge value, or nil if not found.
func findGaugeValue(mf *dto.MetricFamily, labelName, labelValue string) *float64 {
	if mf == nil {
		return nil
	}
	for _, metric := range mf.GetMetric() {
		for _, lp := range metric.GetLabel() {
			if lp.GetName() == labelName && lp.GetValue() == labelValue {
				v := metric.GetGauge().GetValue()
				return &v
			}
		}
	}
	return nil
}

// SetInferenceRTF / SetModelRSSBytes must be nil-safe.
func TestInferenceGauges_NilSafe(t *testing.T) {
	t.Parallel()
	var m *BirdNETMetrics
	m.SetInferenceRTF("x", 1) // must not panic
	m.SetModelRSSBytes("x", 1)
}
