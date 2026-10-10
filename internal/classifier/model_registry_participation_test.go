package classifier

import (
	"strconv"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

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

// TestParticipatesInRangeFilter_MatchesLegacyGate pins the capability switch for
// every REGISTRY entry: ParticipatesInRangeFilter must return exactly what the
// legacy display-name gate returned for a known model. A divergence here is a
// silent behavior change in the per-detection range-filter gate. Unknown/custom IDs
// deliberately diverge from the legacy gate now; that is pinned separately in
// TestParticipatesInRangeFilter_UnknownNotFiltered.
func TestParticipatesInRangeFilter_MatchesLegacyGate(t *testing.T) {
	t.Parallel()

	require.NotEmpty(t, ModelRegistry, "ModelRegistry must be non-empty or this differential test passes vacuously")
	for id := range ModelRegistry {
		t.Run("registry/"+id, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, legacyShouldRangeFilter(id), ParticipatesInRangeFilter(id),
				"capability gate must match the legacy display-name gate for registry ID %q", id)
		})
	}
}

// TestParticipatesInRangeFilter_UnknownNotFiltered pins the decision that a custom
// or otherwise unknown-ID classifier is NOT range-filtered: its label space is
// arbitrary, so gating it against a known model's inclusion list would silently drop
// labels. This intentionally diverges from the legacy display-name gate, which
// resolved unknown IDs to the default BirdNET name and filtered them. The branch is
// unreachable in production (LoadModel rejects unregistered IDs), so this pins the
// semantics rather than changing observable behavior.
func TestParticipatesInRangeFilter_UnknownNotFiltered(t *testing.T) {
	t.Parallel()

	// None of these are keys in ModelRegistry; "perch-v2" is the catalog ID, distinct
	// from the RegistryIDPerchV2 registry key.
	unknownIDs := []string{"", "__not_a_model__", "/path/to/custom/model.tflite", "perch-v2"}
	for i, id := range unknownIDs {
		// Index the subtest name: some fixture IDs contain "/" or are empty, which would
		// produce nested or trailing-slash subtest names. The ID under test stays in the
		// assertion messages.
		t.Run(strconv.Itoa(i), func(t *testing.T) {
			t.Parallel()
			_, known := ModelRegistry[id]
			require.False(t, known, "test ID %q must be absent from the registry", id)
			assert.True(t, legacyShouldRangeFilter(id),
				"context: the legacy gate filtered unknown ID %q; the new behavior diverges from it", id)
			assert.False(t, ParticipatesInRangeFilter(id),
				"unknown/custom ID %q must not be range-filtered", id)
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
