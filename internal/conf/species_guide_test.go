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

func TestGetSpeciesGuideValidProviders_DefensiveCopy(t *testing.T) {
	t.Parallel()

	providers := GetSpeciesGuideValidProviders()
	assert.ElementsMatch(t, []string{
		SpeciesGuideProviderWikipedia,
		SpeciesGuideProviderEBird,
		SpeciesGuideProviderAuto,
	}, providers)

	// Mutating the returned slice must not affect the package-level source.
	providers[0] = "tampered"
	assert.Contains(t, GetSpeciesGuideValidProviders(), SpeciesGuideProviderWikipedia)

	policies := GetSpeciesGuideValidFallbackPolicies()
	assert.ElementsMatch(t, []string{SpeciesGuideFallbackAll, SpeciesGuideFallbackNone}, policies)
	policies[0] = "tampered"
	assert.Contains(t, GetSpeciesGuideValidFallbackPolicies(), SpeciesGuideFallbackAll)
}

func TestValidateSpeciesGuideSettings(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		in           SpeciesGuideConfig
		wantProvider string
		wantFallback string
	}{
		{
			name:         "disabled leaves invalid values untouched",
			in:           SpeciesGuideConfig{Enabled: false, Provider: "bogus", FallbackPolicy: "bogus"},
			wantProvider: "bogus",
			wantFallback: "bogus",
		},
		{
			name:         "empty values fall back to defaults",
			in:           SpeciesGuideConfig{Enabled: true},
			wantProvider: SpeciesGuideProviderWikipedia,
			wantFallback: SpeciesGuideFallbackAll,
		},
		{
			name:         "invalid values fall back to defaults",
			in:           SpeciesGuideConfig{Enabled: true, Provider: "bogus", FallbackPolicy: "bogus"},
			wantProvider: SpeciesGuideProviderWikipedia,
			wantFallback: SpeciesGuideFallbackAll,
		},
		{
			name:         "valid values are preserved",
			in:           SpeciesGuideConfig{Enabled: true, Provider: SpeciesGuideProviderAuto, FallbackPolicy: SpeciesGuideFallbackNone},
			wantProvider: SpeciesGuideProviderAuto,
			wantFallback: SpeciesGuideFallbackNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			c := tt.in
			validateSpeciesGuideSettings(&c)
			assert.Equal(t, tt.wantProvider, c.Provider)
			assert.Equal(t, tt.wantFallback, c.FallbackPolicy)
		})
	}
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
	require.NotNil(t, dst.Realtime.Dashboard.SpeciesGuide.ShowNotes)

	// Pointers must not be aliased.
	assert.NotSame(t, src.Realtime.Dashboard.SpeciesGuide.ShowNotes, dst.Realtime.Dashboard.SpeciesGuide.ShowNotes)

	// Mutating the clone must not affect the source.
	*dst.Realtime.Dashboard.SpeciesGuide.ShowNotes = false
	assert.True(t, *src.Realtime.Dashboard.SpeciesGuide.ShowNotes)
}
