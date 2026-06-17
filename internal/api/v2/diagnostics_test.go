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
	assert.Equal(t, float64(0), byID["Ghost_Model"].WindowMS)

	// Mapped model uses DisplayName ("Google Perch v2 (TFLite)").
	assert.Equal(t, "Google Perch v2 (TFLite)", byID["Perch_V2"].ModelName)

	// Avg and p99 are computed correctly for the mapped model.
	assert.InDelta(t, 1.0, byID["Perch_V2"].AvgMS, 0.001, "avg = 4000us / 4 / 1000 = 1ms")
	assert.InDelta(t, 1.5, byID["Perch_V2"].P99MS, 0.001, "p99 = 1500us / 1000 = 1.5ms")

	// Avg and p99 computed for unmapped model too.
	assert.InDelta(t, 1.0, byID["Ghost_Model"].AvgMS, 0.001, "avg = 2000us / 2 / 1000 = 1ms")
	assert.InDelta(t, 1.2, byID["Ghost_Model"].P99MS, 0.001, "p99 = 1200us / 1000 = 1.2ms")
}
