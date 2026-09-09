// range_scores_names_test.go: tests for the names=false fast path of the
// species-scores endpoint, which skips localized common-name resolution.

package rangeapi

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/classifier"
)

// convertSpeciesScoresNoNames backs GET /api/v2/range/species/scores?names=false.
// It must preserve label/scientificName/score but skip common-name resolution,
// which is the expensive step when converting all geomodel species.
func TestConvertSpeciesScoresNoNames(t *testing.T) {
	t.Parallel()

	scores := []classifier.SpeciesScore{
		{Label: "Turdus merula_Eurasian Blackbird", Score: 0.85},
		{Label: "Parus major_Great Tit", Score: 0.02},
	}

	got := convertSpeciesScoresNoNames(scores)
	require.Len(t, got, 2)

	// Scientific name is parsed from the label; common name is intentionally empty.
	assert.Equal(t, "Turdus merula_Eurasian Blackbird", got[0].Label)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Empty(t, got[0].CommonName)
	require.NotNil(t, got[0].Score)
	assert.InDelta(t, 0.85, *got[0].Score, 1e-9)

	assert.Equal(t, "Parus major", got[1].ScientificName)
	assert.Empty(t, got[1].CommonName)
	require.NotNil(t, got[1].Score)
	assert.InDelta(t, 0.02, *got[1].Score, 1e-9)

	// Each entry must own a distinct score pointer (no loop-variable aliasing).
	assert.NotSame(t, got[0].Score, got[1].Score)
}

func TestConvertSpeciesScoresNoNames_Empty(t *testing.T) {
	t.Parallel()
	assert.Empty(t, convertSpeciesScoresNoNames(nil))
}

// buildRangeFilterSpecies invokes resolveName once per label (with the full label)
// and stores the result as the common name; the names/default path relies on this.
func TestBuildRangeFilterSpecies_WithResolver(t *testing.T) {
	t.Parallel()

	scores := []classifier.SpeciesScore{
		{Label: "Turdus merula_Eurasian Blackbird", Score: 0.85},
		{Label: "Parus major_Great Tit", Score: 0.02},
	}

	var seen []string
	got := buildRangeFilterSpecies(scores, func(label string) string {
		seen = append(seen, label)
		return "COMMON"
	})

	require.Len(t, got, 2)
	assert.Equal(t, "Turdus merula", got[0].ScientificName)
	assert.Equal(t, "COMMON", got[0].CommonName)
	assert.Equal(t, "COMMON", got[1].CommonName)
	assert.Equal(t, []string{"Turdus merula_Eurasian Blackbird", "Parus major_Great Tit"}, seen)
}

// TestBuildRangeFilterSpecies_ProvenanceFlags covers the wire encoding of the badge
// flags. They are absent-or-true: a species with no provenance must omit the fields
// entirely rather than send an explicit false, because the client uses "field absent"
// to decide whether to fall back to matching the displayed names against the settings.
func TestBuildRangeFilterSpecies_ProvenanceFlags(t *testing.T) {
	t.Parallel()

	scores := []classifier.SpeciesScore{
		{Label: "Parus major_Great Tit", Score: 1.0, HasCustomConfig: true},
		{Label: "Turdus merula_Eurasian Blackbird", Score: 1.0, IsManuallyIncluded: true},
		{Label: "Corvus corax_Common Raven", Score: 1.0, HasCustomConfig: true, IsManuallyIncluded: true},
		{Label: "Pica pica_Eurasian Magpie", Score: 0.5},
	}

	got := buildRangeFilterSpecies(scores, nil)
	require.Len(t, got, 4)

	require.NotNil(t, got[0].HasCustomConfig)
	assert.True(t, *got[0].HasCustomConfig)
	assert.Nil(t, got[0].IsManuallyIncluded, "an unset flag must be omitted, not sent as false")

	assert.Nil(t, got[1].HasCustomConfig, "an unset flag must be omitted, not sent as false")
	require.NotNil(t, got[1].IsManuallyIncluded)
	assert.True(t, *got[1].IsManuallyIncluded)

	require.NotNil(t, got[2].HasCustomConfig)
	require.NotNil(t, got[2].IsManuallyIncluded)
	assert.True(t, *got[2].HasCustomConfig)
	assert.True(t, *got[2].IsManuallyIncluded)

	assert.Nil(t, got[3].HasCustomConfig)
	assert.Nil(t, got[3].IsManuallyIncluded)
}

// TestBuildRangeFilterSpecies_ProvenanceFlagsOmittedFromJSON pins the omitempty
// behavior at the JSON layer, which is what the client actually observes.
func TestBuildRangeFilterSpecies_ProvenanceFlagsOmittedFromJSON(t *testing.T) {
	t.Parallel()

	got := buildRangeFilterSpecies([]classifier.SpeciesScore{
		{Label: "Pica pica_Eurasian Magpie", Score: 0.5},
	}, nil)
	require.Len(t, got, 1)

	encoded, err := json.Marshal(got[0])
	require.NoError(t, err)
	assert.NotContains(t, string(encoded), "hasCustomConfig")
	assert.NotContains(t, string(encoded), "isManuallyIncluded")
}
