package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/detection"
)

// legacyShouldRangeFilter reproduces the pre-capability gate that
// processor.shouldApplyRangeFilter ran before this change: resolve the model ID
// through DetectionModelInfoForID and match the default BirdNET name or Perch. It
// exists only so the equivalence test below can prove ParticipatesInRangeFilter is
// byte-for-byte behavior-identical to what it replaced.
func legacyShouldRangeFilter(modelID string) bool {
	mInfo := DetectionModelInfoForID(modelID)
	return mInfo.Name == detection.DefaultModelName || mInfo.Name == DetectionNamePerch
}

// TestParticipatesInRangeFilter_MatchesLegacyGate pins the capability switch: for
// every registry entry AND for unknown/custom IDs, ParticipatesInRangeFilter must
// return exactly what the legacy display-name gate returned. A divergence here is a
// silent behavior change in the per-detection range-filter gate.
func TestParticipatesInRangeFilter_MatchesLegacyGate(t *testing.T) {
	t.Parallel()

	for id := range ModelRegistry {
		t.Run("registry/"+id, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, legacyShouldRangeFilter(id), ParticipatesInRangeFilter(id),
				"capability gate must match the legacy display-name gate for registry ID %q", id)
		})
	}

	// Unknown/custom IDs resolve to the default BirdNET model info under the legacy
	// gate and so were filtered; the capability path must preserve that.
	unknownIDs := []string{"", "__not_a_model__", "/path/to/custom/model.tflite", "perch-v2"}
	for _, id := range unknownIDs {
		t.Run("unknown/"+id, func(t *testing.T) {
			t.Parallel()
			assert.True(t, legacyShouldRangeFilter(id), "sanity: legacy gate filters unknown ID %q", id)
			assert.True(t, ParticipatesInRangeFilter(id), "unknown ID %q must still participate (behavior preserved)", id)
		})
	}
}

// TestParticipatesInRangeFilter_ExpectedPerModel documents the intended truth table
// so the equivalence above cannot pass by both sides being wrong in the same way.
func TestParticipatesInRangeFilter_ExpectedPerModel(t *testing.T) {
	t.Parallel()

	want := map[string]bool{
		"BirdNET_V2.4":      true, // MData range filter over the v2.4 label set
		RegistryIDBirdNETV3: true, // mapped geomodel v3
		RegistryIDPerchV2:   true, // mapped geomodel v3, scientific-name labels
		RegistryIDBat:       false,
		RegistryIDBSG:       false,
	}
	for id, expected := range want {
		t.Run(id, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, expected, ParticipatesInRangeFilter(id),
				"unexpected range-filter participation for %q", id)
		})
	}
	// Every registry entry must have an explicit expectation in the table above, so a
	// newly added or swapped-in model forces a participation decision here instead of
	// silently defaulting. A count check alone is swap-blind, so assert membership.
	for id := range ModelRegistry {
		_, ok := want[id]
		assert.True(t, ok, "want-table must include an explicit expectation for registry ID %q", id)
	}
}
