package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpeciesGuideConfig_ShowDefaults verifies the three Show* sub-section toggles
// default ON via viper defaults (so an unset config shows all sections when the guide
// is enabled), replacing the former *bool nil-means-true convention.
func TestSpeciesGuideConfig_ShowDefaults(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()

	assert.True(t, viper.GetBool("realtime.dashboard.speciesguide.shownotes"),
		"notes section must default to shown")
	assert.True(t, viper.GetBool("realtime.dashboard.speciesguide.showenrichments"),
		"enrichments must default to shown")
	assert.True(t, viper.GetBool("realtime.dashboard.speciesguide.showsimilarspecies"),
		"similar-species panel must default to shown")

	// An explicitly stored false must win over the default (opt-out is respected).
	viper.Set("realtime.dashboard.speciesguide.shownotes", false)
	assert.False(t, viper.GetBool("realtime.dashboard.speciesguide.shownotes"),
		"an explicit false opt-out must be respected")
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
	src.Realtime.Dashboard.SpeciesGuide.ShowNotes = true
	src.Realtime.Dashboard.SpeciesGuide.ShowEnrichments = true
	src.Realtime.Dashboard.SpeciesGuide.ShowSimilarSpecies = true

	dst := CloneSettings(src)
	require.NotNil(t, dst)
	dstGuide := &dst.Realtime.Dashboard.SpeciesGuide
	srcGuide := &src.Realtime.Dashboard.SpeciesGuide

	// The Show* flags are plain bool value types; mutating the clone must not affect
	// the source (they ride the shallow struct copy with no shared pointer).
	dstGuide.ShowNotes = false
	dstGuide.ShowEnrichments = false
	dstGuide.ShowSimilarSpecies = false
	assert.True(t, srcGuide.ShowNotes)
	assert.True(t, srcGuide.ShowEnrichments)
	assert.True(t, srcGuide.ShowSimilarSpecies)
}
