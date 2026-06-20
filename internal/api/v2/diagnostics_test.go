// internal/api/v2/diagnostics_test.go
package api

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/inferencestats"
	"github.com/tphakala/birdnet-go/internal/health/checks"
)

// TestMapInferenceSnapshotsKeepsUnmappedModel verifies that a counter whose
// modelID is not present in infoMap is still included in the output using
// the raw modelID as ModelName, and that a mapped counter uses its DisplayName.
func TestMapInferenceSnapshotsKeepsUnmappedModel(t *testing.T) {
	t.Parallel()
	snaps := map[string]inferencestats.PeekSnapshot{
		"Perch_V2":    {InvokeCount: 4, InvokeTotalUs: 4000, InvokeMaxUs: 1500},
		"Ghost_Model": {InvokeCount: 2, InvokeTotalUs: 2000, InvokeMaxUs: 1200},
	}
	infoMap := map[string]*classifier.ModelInfo{
		"Perch_V2": {
			ID:      "Perch_V2",
			Name:    "Google Perch v2",
			Backend: "TFLite",
		},
	}

	out := mapInferenceSnapshots(snaps, infoMap)

	byID := make(map[string]checks.ModelInferenceInfo, len(out))
	for _, r := range out {
		byID[r.ModelID] = r
	}

	require.Contains(t, byID, "Perch_V2", "mapped counter must appear")
	require.Contains(t, byID, "Ghost_Model", "unmapped counter must still surface")

	// Unmapped model uses raw modelID as name and has zero window.
	assert.Equal(t, "Ghost_Model", byID["Ghost_Model"].ModelName)
	assert.InDelta(t, float64(0), byID["Ghost_Model"].WindowMS, 0.0, "unmapped model has zero window")

	// Mapped model uses DisplayName ("Google Perch v2 (TFLite)").
	assert.Equal(t, "Google Perch v2 (TFLite)", byID["Perch_V2"].ModelName)

	// Avg and max are computed correctly for the mapped model.
	assert.InDelta(t, 1.0, byID["Perch_V2"].AvgMS, 0.001, "avg = 4000us / 4 / 1000 = 1ms")
	assert.InDelta(t, 1.5, byID["Perch_V2"].MaxMS, 0.001, "max = 1500us / 1000 = 1.5ms")

	// Avg and max computed for unmapped model too.
	assert.InDelta(t, 1.0, byID["Ghost_Model"].AvgMS, 0.001, "avg = 2000us / 2 / 1000 = 1ms")
	assert.InDelta(t, 1.2, byID["Ghost_Model"].MaxMS, 0.001, "max = 1200us / 1000 = 1.2ms")
}

// TestMapInferenceSnapshotsUsesWindowedMaxNotLifetime guards the latency
// health-check path against a regression where a one-time warm-up spike
// permanently latches the check into Warning/Critical. mapInferenceSnapshots
// must derive MaxMS from the windowed max (InvokeMaxUs, reset every collector
// tick), NOT from the never-reset lifetime max (InvokeMaxUsLifetime). The model
// card uses the lifetime max; the health check must not.
func TestMapInferenceSnapshotsUsesWindowedMaxNotLifetime(t *testing.T) {
	t.Parallel()
	// A model whose all-time peak was a slow 2s warm-up, but whose recent
	// (windowed) peak is a healthy 250ms.
	snaps := map[string]inferencestats.PeekSnapshot{
		"Perch_V2": {
			InvokeCount:         10,
			InvokeTotalUs:       2_500_000,
			InvokeMaxUs:         250_000,   // windowed: recent steady-state peak
			InvokeMaxUsLifetime: 2_000_000, // lifetime: one-time warm-up spike
		},
	}

	out := mapInferenceSnapshots(snaps, map[string]*classifier.ModelInfo{})
	require.Len(t, out, 1)

	// MaxMS must reflect the windowed max (250ms), not the lifetime spike (2000ms),
	// so the latency health check clears once the warm-up falls out of the window.
	assert.InDelta(t, 250.0, out[0].MaxMS, 0.001,
		"health check must use windowed max, not the lifetime warm-up spike")
}
