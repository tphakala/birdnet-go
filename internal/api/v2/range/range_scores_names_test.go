// range_scores_names_test.go: tests for the names=false fast path of the
// species-scores endpoint, which skips localized common-name resolution.

package rangeapi

import (
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
