// internal/api/v2/system/diagnostics_test.go
package system

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
		"Perch_V2":    {InvokeCount: 4, InvokeTotalUs: 4000, RecentP95Us: 1500},
		"Ghost_Model": {InvokeCount: 2, InvokeTotalUs: 2000, RecentP95Us: 1200},
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

	// Avg and p95 are computed correctly for the mapped model.
	assert.InDelta(t, 1.0, byID["Perch_V2"].AvgMS, 0.001, "avg = 4000us / 4 / 1000 = 1ms")
	assert.InDelta(t, 1.5, byID["Perch_V2"].P95MS, 0.001, "p95 = 1500us / 1000 = 1.5ms")

	// Avg and p95 computed for unmapped model too.
	assert.InDelta(t, 1.0, byID["Ghost_Model"].AvgMS, 0.001, "avg = 2000us / 2 / 1000 = 1ms")
	assert.InDelta(t, 1.2, byID["Ghost_Model"].P95MS, 0.001, "p95 = 1200us / 1000 = 1.2ms")
}

// TestMapInferenceSnapshotsUsesP95NotLifetime guards the latency health-check
// path against a regression where a one-time warm-up spike permanently latches
// the check into Warning/Critical. mapInferenceSnapshots must derive P95MS from
// the rolling-window p95 (RecentP95Us), NOT from the never-reset lifetime max
// (InvokeMaxUsLifetime). The model card uses the lifetime max; the health check
// must not.
func TestMapInferenceSnapshotsUsesP95NotLifetime(t *testing.T) {
	t.Parallel()
	// A model whose all-time peak was a slow 2s warm-up, but whose rolling p95
	// is a healthy 250ms.
	snaps := map[string]inferencestats.PeekSnapshot{
		"Perch_V2": {
			InvokeCount:         10,
			InvokeTotalUs:       2_500_000,
			RecentP95Us:         250_000,   // rolling-window p95: recent steady state
			InvokeMaxUsLifetime: 2_000_000, // lifetime: one-time warm-up spike
		},
	}

	out := mapInferenceSnapshots(snaps, map[string]*classifier.ModelInfo{})
	require.Len(t, out, 1)

	// P95MS must reflect the rolling p95 (250ms), not the lifetime spike (2000ms),
	// so the latency health check clears once the warm-up falls out of the window.
	assert.InDelta(t, 250.0, out[0].P95MS, 0.001,
		"health check must use the rolling p95, not the lifetime warm-up spike")
}

// TestMapInferenceSnapshotsDeterministicOrder verifies the output is sorted by
// ModelID regardless of map iteration order, so the derived health results and
// logs are stable rather than reordering randomly between runs.
func TestMapInferenceSnapshotsDeterministicOrder(t *testing.T) {
	t.Parallel()
	snaps := map[string]inferencestats.PeekSnapshot{
		"Zebra_Model": {InvokeCount: 1, InvokeTotalUs: 1000, RecentP95Us: 1000},
		"Alpha_Model": {InvokeCount: 1, InvokeTotalUs: 1000, RecentP95Us: 1000},
		"Mango_Model": {InvokeCount: 1, InvokeTotalUs: 1000, RecentP95Us: 1000},
	}

	out := mapInferenceSnapshots(snaps, map[string]*classifier.ModelInfo{})
	ids := make([]string, len(out))
	for i, r := range out {
		ids[i] = r.ModelID
	}
	assert.Equal(t, []string{"Alpha_Model", "Mango_Model", "Zebra_Model"}, ids,
		"models must be returned in sorted ModelID order")
}
