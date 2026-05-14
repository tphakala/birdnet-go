package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestExtractScientificName(t *testing.T) {
	t.Parallel()
	tests := []struct {
		label    string
		expected string
	}{
		{"Turdus merula_Common Blackbird", "Turdus merula"},
		{"Parus major_Great Tit", "Parus major"},
		{"NoUnderscore", "NoUnderscore"},
		{"Sci_Common_Extra", "Sci"},
		{"", ""},
	}
	for _, tt := range tests {
		t.Run(tt.label, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, extractScientificName(tt.label))
		})
	}
}

func TestBuildSpeciesMapping(t *testing.T) {
	t.Parallel()

	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Erithacus rubecula_European Robin",
		"Ficedula hypoleuca_European Pied Flycatcher",
	}

	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
		"Corvus corax_Northern Raven",
		"Erithacus rubecula_Robin",
	}

	mapping := buildSpeciesMapping(classifierLabels, geomodelLabels)

	require.Len(t, mapping, 4)
	assert.Equal(t, 1, mapping[0], "Turdus merula should map to geomodel index 1")
	assert.Equal(t, 0, mapping[1], "Parus major should map to geomodel index 0")
	assert.Equal(t, 3, mapping[2], "Erithacus rubecula should map to geomodel index 3")
	assert.Equal(t, -1, mapping[3], "Ficedula hypoleuca has no match in geomodel")
}

func TestBuildSpeciesMapping_CaseInsensitive(t *testing.T) {
	t.Parallel()

	classifierLabels := []string{
		"turdus merula_Common Blackbird",
	}
	geomodelLabels := []string{
		"Turdus Merula_Amsel",
	}

	mapping := buildSpeciesMapping(classifierLabels, geomodelLabels)

	require.Len(t, mapping, 1)
	assert.Equal(t, 0, mapping[0], "case-insensitive match should succeed")
}

func TestBuildSpeciesMapping_EmptyInputs(t *testing.T) {
	t.Parallel()

	t.Run("empty classifier labels", func(t *testing.T) {
		t.Parallel()
		mapping := buildSpeciesMapping(nil, []string{"A_B"})
		assert.Empty(t, mapping)
	})

	t.Run("empty geomodel labels", func(t *testing.T) {
		t.Parallel()
		mapping := buildSpeciesMapping([]string{"A_B"}, nil)
		require.Len(t, mapping, 1)
		assert.Equal(t, -1, mapping[0])
	})
}

// fakeRangeFilter is a test double that returns preconfigured scores.
type fakeRangeFilter struct {
	scores []float32
	err    error
	closed bool
}

func (f *fakeRangeFilter) Predict(_, _, _ float32) ([]float32, error) {
	if f.err != nil {
		return nil, f.err
	}
	out := make([]float32, len(f.scores))
	copy(out, f.scores)
	return out, nil
}

func (f *fakeRangeFilter) NumSpecies() int { return len(f.scores) }
func (f *fakeRangeFilter) Close()          { f.closed = true }

func TestMappedRangeFilter_Predict(t *testing.T) {
	t.Parallel()

	classifierLabels := []string{
		"Turdus merula_Common Blackbird",     // maps to geo index 1
		"Parus major_Great Tit",              // maps to geo index 0
		"Ficedula hypoleuca_Pied Flycatcher", // no match -> unmappedScore
	}
	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
		"Corvus corax_Northern Raven",
	}

	inner := &fakeRangeFilter{
		scores: []float32{0.8, 0.9, 0.3},
	}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 0.0)

	scores, err := mapped.Predict(60.0, 25.0, 20.0)
	require.NoError(t, err)
	require.Len(t, scores, 3)

	assert.InDelta(t, 0.9, scores[0], 0.001, "Turdus merula -> geomodel[1]=0.9")
	assert.InDelta(t, 0.8, scores[1], 0.001, "Parus major -> geomodel[0]=0.8")
	assert.InDelta(t, 0.0, scores[2], 0.001, "Ficedula hypoleuca -> unmapped=0.0")
}

func TestMappedRangeFilter_UnmappedScorePassthrough(t *testing.T) {
	t.Parallel()

	classifierLabels := []string{"Unknown species_Foo"}
	geomodelLabels := []string{"Other species_Bar"}

	inner := &fakeRangeFilter{
		scores: []float32{0.5},
	}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 1.0)

	scores, err := mapped.Predict(60.0, 25.0, 20.0)
	require.NoError(t, err)
	require.Len(t, scores, 1)
	assert.InDelta(t, 1.0, scores[0], 0.001, "unmapped species should get unmappedScore=1.0")
}

func TestMappedRangeFilter_PropagatesError(t *testing.T) {
	t.Parallel()

	inner := &fakeRangeFilter{
		err: assert.AnError,
	}
	mapped := newMappedRangeFilter(inner, []string{"A_B"}, []string{"A_B"}, 0.0)

	_, err := mapped.Predict(60.0, 25.0, 20.0)
	assert.Error(t, err)
}

func TestMappedRangeFilter_NumSpecies(t *testing.T) {
	t.Parallel()

	inner := &fakeRangeFilter{scores: make([]float32, 5)}
	mapped := newMappedRangeFilter(inner, make([]string, 3), make([]string, 5), 0.0)

	assert.Equal(t, 3, mapped.NumSpecies(), "NumSpecies should return classifier label count")
}

func TestMappedRangeFilter_Close(t *testing.T) {
	t.Parallel()

	inner := &fakeRangeFilter{scores: make([]float32, 3)}
	mapped := newMappedRangeFilter(inner, make([]string, 2), make([]string, 3), 0.0)

	mapped.Close()
	assert.True(t, inner.closed, "Close should delegate to inner filter")
}

func TestMappedRangeFilter_GeomodelLabelsStored(t *testing.T) {
	t.Parallel()

	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
		"Corvus corax_Northern Raven",
		"Erithacus rubecula_Robin",
	}

	inner := &fakeRangeFilter{scores: make([]float32, len(geomodelLabels))}
	mapped := newMappedRangeFilter(inner, []string{"Parus major_Great Tit"}, geomodelLabels, 0.0)

	require.Len(t, mapped.geomodelLabels, len(geomodelLabels), "geomodelLabels field should be populated with all geomodel labels")
	assert.Equal(t, geomodelLabels, mapped.geomodelLabels)
}

func TestMappedRangeFilter_PredictIncludedSpecies(t *testing.T) {
	t.Parallel()

	// geomodel scores: species A=0.8, B=0.9, C=0.3, D=0.05
	// threshold=0.1, so A, B, C pass; D does not
	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
		"Erithacus rubecula_Robin",
		"Ficedula hypoleuca_Pied Flycatcher",
	}
	inner := &fakeRangeFilter{
		scores: []float32{0.8, 0.9, 0.3, 0.05},
	}

	// classifier labels can differ from geomodel labels
	classifierLabels := []string{"Parus major_Titmouse"}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 0.0)

	included, err := mapped.PredictIncludedSpecies(60.0, 25.0, 20.0, 0.1)
	require.NoError(t, err)

	// species D (score 0.05) is below threshold; A, B, C should be included
	require.Len(t, included, 3)
	assert.Equal(t, "Parus major_Great Tit", included[0])
	assert.Equal(t, "Turdus merula_Amsel", included[1])
	assert.Equal(t, "Erithacus rubecula_Robin", included[2])
}

func TestMappedRangeFilter_PredictIncludedSpecies_EmptyResult(t *testing.T) {
	t.Parallel()

	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
	}
	inner := &fakeRangeFilter{
		scores: []float32{0.05, 0.08},
	}

	mapped := newMappedRangeFilter(inner, []string{"Parus major_Titmouse"}, geomodelLabels, 0.0)

	// threshold of 0.5 means no species pass
	included, err := mapped.PredictIncludedSpecies(60.0, 25.0, 20.0, 0.5)
	require.NoError(t, err)
	assert.Empty(t, included, "no species should pass a high threshold")
}

func TestMappedRangeFilter_PredictIncludedSpecies_PropagatesError(t *testing.T) {
	t.Parallel()

	inner := &fakeRangeFilter{
		err: assert.AnError,
	}
	mapped := newMappedRangeFilter(inner, []string{"A_B"}, []string{"A_B"}, 0.0)

	_, err := mapped.PredictIncludedSpecies(60.0, 25.0, 20.0, 0.1)
	assert.Error(t, err)
}
