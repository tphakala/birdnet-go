// range_dedup_test.go: tests for display-boundary de-duplication of the
// range-filter species lists (collapsing force-include override copies and
// localized taxonomic synonyms into a single displayed row).

package rangeapi

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/dto"
)

func TestDedupeSpeciesForDisplay(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   []dto.RangeFilterSpecies
		want []dto.RangeFilterSpecies
	}{
		{
			name: "nil input",
			in:   nil,
			want: nil,
		},
		{
			name: "single entry unchanged",
			in:   []dto.RangeFilterSpecies{{ScientificName: "Corvus cornix", CommonName: "varis", Score: new(0.71)}},
			want: []dto.RangeFilterSpecies{{ScientificName: "Corvus cornix", CommonName: "varis", Score: new(0.71)}},
		},
		{
			// R1: a geomodel-scored species and its force-include override copy
			// carry different label strings but the same resolved common name.
			// They collapse to one row at the always-active 1.0 score.
			name: "override copy collapses, 1.0 wins",
			in: []dto.RangeFilterSpecies{
				{Label: "Corvus cornix_varis", ScientificName: "Corvus cornix", CommonName: "varis", Score: new(1.0)},
				{Label: "Corvus cornix_Hooded Crow", ScientificName: "Corvus cornix", CommonName: "varis", Score: new(0.71)},
			},
			want: []dto.RangeFilterSpecies{
				{Label: "Corvus cornix_varis", ScientificName: "Corvus cornix", CommonName: "varis", Score: new(1.0)},
			},
		},
		{
			// R4: two taxonomic synonyms that localize to the same common name
			// collapse to a single displayed row (max score wins).
			name: "synonyms with same common name collapse",
			in: []dto.RangeFilterSpecies{
				{Label: "Eptesicus nilssonii", ScientificName: "Eptesicus nilssonii", CommonName: "pohjanlepakko", Score: new(1.0)},
				{Label: "Cnephaeus nilssonii_Northern Bat", ScientificName: "Cnephaeus nilssonii", CommonName: "pohjanlepakko", Score: new(0.01)},
			},
			want: []dto.RangeFilterSpecies{
				{Label: "Eptesicus nilssonii", ScientificName: "Eptesicus nilssonii", CommonName: "pohjanlepakko", Score: new(1.0)},
			},
		},
		{
			name: "distinct species are kept",
			in: []dto.RangeFilterSpecies{
				{ScientificName: "Corvus cornix", CommonName: "varis", Score: new(0.71)},
				{ScientificName: "Parus major", CommonName: "talitiainen", Score: new(0.73)},
			},
			want: []dto.RangeFilterSpecies{
				{ScientificName: "Corvus cornix", CommonName: "varis", Score: new(0.71)},
				{ScientificName: "Parus major", CommonName: "talitiainen", Score: new(0.73)},
			},
		},
		{
			name: "case insensitive common name collapse",
			in: []dto.RangeFilterSpecies{
				{ScientificName: "Eptesicus nilssonii", CommonName: "Pohjanlepakko", Score: new(0.5)},
				{ScientificName: "Cnephaeus nilssonii", CommonName: "pohjanlepakko", Score: new(0.5)},
			},
			want: []dto.RangeFilterSpecies{
				{ScientificName: "Eptesicus nilssonii", CommonName: "Pohjanlepakko", Score: new(0.5)},
			},
		},
		{
			// Genuine NFC vs NFD: composed "ö" (U+00F6) vs decomposed "o" + U+0308.
			// normalizeForLookup recomposes via norm.NFC, so both key identically.
			// This pins the NFC half of the key; ToLower alone would not collapse them.
			name: "NFC and NFD decomposed common name collapse",
			in: []dto.RangeFilterSpecies{
				{ScientificName: "Strix aluco", CommonName: "Lehtopöllö", Score: new(0.6)},
				{ScientificName: "Syrnium aluco", CommonName: "Lehtopöllö", Score: new(0.4)},
			},
			want: []dto.RangeFilterSpecies{
				{ScientificName: "Strix aluco", CommonName: "Lehtopöllö", Score: new(0.6)},
			},
		},
		{
			// Without a common name, fall back to the scientific name so unrelated
			// unresolved rows are not all merged into one bucket.
			name: "empty common name falls back to scientific name",
			in: []dto.RangeFilterSpecies{
				{Label: "Foobarus_x", ScientificName: "Foobarus x"},
				{Label: "Foobarus x", ScientificName: "Foobarus x"},
				{Label: "Bazquxus y", ScientificName: "Bazquxus y"},
			},
			want: []dto.RangeFilterSpecies{
				{Label: "Foobarus_x", ScientificName: "Foobarus x"},
				{Label: "Bazquxus y", ScientificName: "Bazquxus y"},
			},
		},
		{
			// Rows with neither common nor scientific name have no identity to key
			// on and must not collapse into a single bucket.
			name: "identity-less rows are all kept",
			in: []dto.RangeFilterSpecies{
				{Label: "a"},
				{Label: "b"},
			},
			want: []dto.RangeFilterSpecies{
				{Label: "a"},
				{Label: "b"},
			},
		},
		{
			// Defensive: even when the higher score is not first, the survivor
			// surfaces the higher score while keeping the first position.
			name: "higher score wins regardless of order",
			in: []dto.RangeFilterSpecies{
				{Label: "Corvus cornix_Hooded Crow", ScientificName: "Corvus cornix", CommonName: "varis", Score: new(0.71)},
				{Label: "Corvus cornix_varis", ScientificName: "Corvus cornix", CommonName: "varis", Score: new(1.0)},
			},
			want: []dto.RangeFilterSpecies{
				{Label: "Corvus cornix_varis", ScientificName: "Corvus cornix", CommonName: "varis", Score: new(1.0)},
			},
		},
		{
			// A scored entry beats an unscored (label-only) entry for the same species.
			name: "scored entry wins over unscored",
			in: []dto.RangeFilterSpecies{
				{ScientificName: "Parus major", CommonName: "talitiainen"},
				{ScientificName: "Parus major", CommonName: "talitiainen", Score: new(0.42)},
			},
			want: []dto.RangeFilterSpecies{
				{ScientificName: "Parus major", CommonName: "talitiainen", Score: new(0.42)},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := dedupeSpeciesForDisplay(tt.in)
			require.Len(t, got, len(tt.want))
			for i := range tt.want {
				assert.Equal(t, tt.want[i].ScientificName, got[i].ScientificName, "row %d scientific name", i)
				assert.Equal(t, tt.want[i].CommonName, got[i].CommonName, "row %d common name", i)
				assert.Equal(t, tt.want[i].Label, got[i].Label, "row %d label", i)
				if tt.want[i].Score == nil {
					assert.Nil(t, got[i].Score, "row %d score", i)
				} else {
					require.NotNil(t, got[i].Score, "row %d score", i)
					assert.InDelta(t, *tt.want[i].Score, *got[i].Score, 1e-9, "row %d score value", i)
				}
			}
		})
	}
}

func TestSpeciesScoreHigher(t *testing.T) {
	t.Parallel()
	assert.True(t, speciesScoreHigher(dto.RangeFilterSpecies{Score: new(1.0)}, dto.RangeFilterSpecies{Score: new(0.5)}))
	assert.False(t, speciesScoreHigher(dto.RangeFilterSpecies{Score: new(0.5)}, dto.RangeFilterSpecies{Score: new(1.0)}))
	assert.False(t, speciesScoreHigher(dto.RangeFilterSpecies{Score: new(0.5)}, dto.RangeFilterSpecies{Score: new(0.5)}))
	// nil score sorts below any real score.
	assert.False(t, speciesScoreHigher(dto.RangeFilterSpecies{}, dto.RangeFilterSpecies{Score: new(0.0)}))
	assert.True(t, speciesScoreHigher(dto.RangeFilterSpecies{Score: new(0.0)}, dto.RangeFilterSpecies{}))
	assert.False(t, speciesScoreHigher(dto.RangeFilterSpecies{}, dto.RangeFilterSpecies{}))
}
