package metrics

import (
	"testing"

	"github.com/prometheus/client_golang/prometheus"
)

func TestInferenceGauges(t *testing.T) {
	t.Helper()
	reg := prometheus.NewRegistry()
	m, err := NewBirdNETMetrics(reg)
	if err != nil {
		t.Fatalf("NewBirdNETMetrics: %v", err)
	}
	m.SetInferenceRTF("BirdNET_V2.4", 0.016)
	m.SetModelRSSBytes("BirdNET_V2.4", 125_000_000)

	mfs, err := reg.Gather()
	if err != nil {
		t.Fatalf("Gather: %v", err)
	}
	names := map[string]bool{}
	for _, mf := range mfs {
		names[mf.GetName()] = true
	}
	if !names["birdnet_inference_rtf"] {
		t.Error("birdnet_inference_rtf not registered/exposed")
	}
	if !names["birdnet_model_rss_bytes"] {
		t.Error("birdnet_model_rss_bytes not registered/exposed")
	}
}

// SetInferenceRTF / SetModelRSSBytes must be nil-safe.
func TestInferenceGauges_NilSafe(t *testing.T) {
	var m *BirdNETMetrics
	m.SetInferenceRTF("x", 1) // must not panic
	m.SetModelRSSBytes("x", 1)
}
