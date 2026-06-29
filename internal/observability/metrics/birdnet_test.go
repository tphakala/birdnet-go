package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// gatherMetricsByName creates a fresh BirdNETMetrics with a private registry,
// applies setup, gathers all metric families, and returns them indexed by name.
// It is a test helper to reduce boilerplate in gauge tests.
func gatherMetricsByName(t *testing.T, setup func(m *BirdNETMetrics)) map[string]*dto.MetricFamily {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewBirdNETMetrics(reg)
	require.NoError(t, err, "NewBirdNETMetrics")
	setup(m)
	mfs, err := reg.Gather()
	require.NoError(t, err, "Gather")
	byName := make(map[string]*dto.MetricFamily, len(mfs))
	for _, mf := range mfs {
		byName[mf.GetName()] = mf
	}
	return byName
}

func TestInferenceGauges(t *testing.T) {
	t.Parallel()

	byName := gatherMetricsByName(t, func(m *BirdNETMetrics) {
		m.SetInferenceRTF("BirdNET_V2.4", 0.016)
		m.SetModelRSSBytes("BirdNET_V2.4", 125_000_000)
	})

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

// TestAudioGauges verifies that birdnet_audio_queue_depth and
// birdnet_audio_dropped_chunks are registered, exposed, and set correctly.
func TestAudioGauges(t *testing.T) {
	t.Parallel()

	byName := gatherMetricsByName(t, func(m *BirdNETMetrics) {
		m.SetAudioQueueDepth("rtsp_source_1", 8.0)
		m.SetAudioDroppedChunks("rtsp_source_1", 42.0)
	})

	require.Contains(t, byName, "birdnet_audio_queue_depth", "birdnet_audio_queue_depth not registered/exposed")
	depthFound := findGaugeValue(byName["birdnet_audio_queue_depth"], "source", "rtsp_source_1")
	require.NotNil(t, depthFound, "expected birdnet_audio_queue_depth sample with label source=rtsp_source_1")
	assert.InDelta(t, 8.0, *depthFound, 1e-9, "birdnet_audio_queue_depth value")

	require.Contains(t, byName, "birdnet_audio_dropped_chunks", "birdnet_audio_dropped_chunks not registered/exposed")
	dropsFound := findGaugeValue(byName["birdnet_audio_dropped_chunks"], "source", "rtsp_source_1")
	require.NotNil(t, dropsFound, "expected birdnet_audio_dropped_chunks sample with label source=rtsp_source_1")
	assert.InDelta(t, 42.0, *dropsFound, 1e-9, "birdnet_audio_dropped_chunks value")
}

// TestAudioGauges_NilSafe verifies that SetAudioQueueDepth and SetAudioDroppedChunks
// are nil-safe (no panic on nil receiver).
func TestAudioGauges_NilSafe(t *testing.T) {
	t.Parallel()
	var m *BirdNETMetrics
	m.SetAudioQueueDepth("src", 1)    // must not panic
	m.SetAudioDroppedChunks("src", 1) // must not panic
}
