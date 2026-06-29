package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSpeciesGuideConfig_IsShowDefaults(t *testing.T) {
	t.Parallel()

	t.Run("nil pointers default to true", func(t *testing.T) {
		t.Parallel()
		c := &SpeciesGuideConfig{}
		assert.True(t, c.IsShowNotes())
		assert.True(t, c.IsShowEnrichments())
		assert.True(t, c.IsShowSimilarSpecies())
	})

	t.Run("explicit false is respected", func(t *testing.T) {
		t.Parallel()
		f := false
		c := &SpeciesGuideConfig{
			ShowNotes:          &f,
			ShowEnrichments:    &f,
			ShowSimilarSpecies: &f,
		}
		assert.False(t, c.IsShowNotes())
		assert.False(t, c.IsShowEnrichments())
		assert.False(t, c.IsShowSimilarSpecies())
	})

	t.Run("explicit true is respected", func(t *testing.T) {
		t.Parallel()
		tr := true
		c := &SpeciesGuideConfig{ShowNotes: &tr}
		assert.True(t, c.IsShowNotes())
	})
}

func TestValidateSpeciesGuideSettings_WarmTopNClamp(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		in   int
		want int
	}{
		{name: "negative floored to zero", in: -5, want: 0},
		{name: "in range preserved", in: 50, want: 50},
		{name: "at max preserved", in: SpeciesGuideMaxWarmTopN, want: SpeciesGuideMaxWarmTopN},
		{name: "above max clamped", in: SpeciesGuideMaxWarmTopN + 1, want: SpeciesGuideMaxWarmTopN},
		{name: "absurd value clamped", in: 1_000_000_000, want: SpeciesGuideMaxWarmTopN},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := SpeciesGuideConfig{Enabled: true, WarmTopN: tt.in}
			validateSpeciesGuideSettings(&c)
			assert.Equal(t, tt.want, c.WarmTopN)
		})
	}

	t.Run("disabled leaves WarmTopN untouched", func(t *testing.T) {
		t.Parallel()
		c := SpeciesGuideConfig{Enabled: false, WarmTopN: 1_000_000_000}
		validateSpeciesGuideSettings(&c)
		assert.Equal(t, 1_000_000_000, c.WarmTopN)
	})
}

func TestCloneSettings_SpeciesGuideShowFlagsIndependence(t *testing.T) {
	t.Parallel()

	src := &Settings{}
	tr := true
	src.Realtime.Dashboard.SpeciesGuide.ShowNotes = &tr
	src.Realtime.Dashboard.SpeciesGuide.ShowEnrichments = &tr
	src.Realtime.Dashboard.SpeciesGuide.ShowSimilarSpecies = &tr

	dst := CloneSettings(src)
	require.NotNil(t, dst)
	dstGuide := &dst.Realtime.Dashboard.SpeciesGuide
	srcGuide := &src.Realtime.Dashboard.SpeciesGuide
	require.NotNil(t, dstGuide.ShowNotes)
	require.NotNil(t, dstGuide.ShowEnrichments)
	require.NotNil(t, dstGuide.ShowSimilarSpecies)

	// None of the three Show* pointers may be aliased with the source.
	assert.NotSame(t, srcGuide.ShowNotes, dstGuide.ShowNotes)
	assert.NotSame(t, srcGuide.ShowEnrichments, dstGuide.ShowEnrichments)
	assert.NotSame(t, srcGuide.ShowSimilarSpecies, dstGuide.ShowSimilarSpecies)

	// Mutating the clone must not affect the source, for every flag.
	*dstGuide.ShowNotes = false
	*dstGuide.ShowEnrichments = false
	*dstGuide.ShowSimilarSpecies = false
	assert.True(t, *srcGuide.ShowNotes)
	assert.True(t, *srcGuide.ShowEnrichments)
	assert.True(t, *srcGuide.ShowSimilarSpecies)
}
