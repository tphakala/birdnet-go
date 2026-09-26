package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSpeciesGuideConfig_ShowDefaults verifies the Show* sub-section toggles
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
	assert.True(t, viper.GetBool("realtime.dashboard.speciesguide.showtaxonomy"),
		"taxonomy section must default to shown")

	// An explicitly stored false must win over the default (opt-out is respected).
	viper.Set("realtime.dashboard.speciesguide.shownotes", false)
	assert.False(t, viper.GetBool("realtime.dashboard.speciesguide.shownotes"),
		"an explicit false opt-out must be respected")
}

// TestSpeciesGuideConfig_FeatureDefaults verifies the non-Show* defaults through
// viper, which is where they actually come from. Asserting them off a zero-value
// SpeciesGuideConfig would only re-state Go's zero values and would keep passing
// if a default in defaults.go were flipped.
func TestSpeciesGuideConfig_FeatureDefaults(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()

	// viper.GetBool returns false for an unregistered key too, so each
	// default-false key is first asserted to actually be registered — otherwise
	// deleting a SetDefault line would leave this test green.
	boolDefaults := map[string]bool{
		"realtime.dashboard.speciesguide.enabled":                  false, // the guide is opt-in
		"realtime.dashboard.speciesguide.enablewikipedia":          false, // online descriptions are opt-in; the guide works offline
		"realtime.dashboard.speciesguide.enablesupplementarylinks": false,
		"realtime.dashboard.speciesguide.prefetchenabled":          true,
	}
	for key, want := range boolDefaults {
		require.True(t, viper.IsSet(key), "no default registered for %s", key)
		assert.Equal(t, want, viper.GetBool(key), "default for %s", key)
	}

	require.True(t, viper.IsSet("realtime.dashboard.speciesguide.warmtopn"))
	assert.Equal(t, 50, viper.GetInt("realtime.dashboard.speciesguide.warmtopn"),
		"warm top-N default")

	// An explicitly stored value must win over the default (opt-in is respected).
	viper.Set("realtime.dashboard.speciesguide.enablesupplementarylinks", true)
	assert.True(t, viper.GetBool("realtime.dashboard.speciesguide.enablesupplementarylinks"),
		"an explicit opt-in must be respected")
}

// TestDefaultSpeciesGuideConfig_MatchesViperDefaults pins every viper key to the
// corresponding field of DefaultSpeciesGuideConfig. setDefaultConfig registers
// the keys from that struct, so this catches a key wired to the wrong field —
// the one mistake the shared source of truth cannot prevent by itself.
func TestDefaultSpeciesGuideConfig_MatchesViperDefaults(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()

	want := DefaultSpeciesGuideConfig()
	const prefix = "realtime.dashboard.speciesguide."
	assert.Equal(t, want.Enabled, viper.GetBool(prefix+"enabled"))
	assert.Equal(t, want.EnableWikipedia, viper.GetBool(prefix+"enablewikipedia"))
	assert.Equal(t, want.EnableSupplementaryLinks, viper.GetBool(prefix+"enablesupplementarylinks"))
	assert.Equal(t, want.PreFetchEnabled, viper.GetBool(prefix+"prefetchenabled"))
	assert.Equal(t, want.WarmTopN, viper.GetInt(prefix+"warmtopn"))
	assert.Equal(t, want.ShowNotes, viper.GetBool(prefix+"shownotes"))
	assert.Equal(t, want.ShowEnrichments, viper.GetBool(prefix+"showenrichments"))
	assert.Equal(t, want.ShowSimilarSpecies, viper.GetBool(prefix+"showsimilarspecies"))
	assert.Equal(t, want.ShowTaxonomy, viper.GetBool(prefix+"showtaxonomy"))

	// The default warm target must be within the clamp, or a fresh install would
	// have its own default rewritten by validateSpeciesGuideSettings.
	assert.LessOrEqual(t, want.WarmTopN, SpeciesGuideMaxWarmTopN)
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
	src.Realtime.Dashboard.SpeciesGuide.ShowTaxonomy = true

	dst := CloneSettings(src)
	require.NotNil(t, dst)
	dstGuide := &dst.Realtime.Dashboard.SpeciesGuide
	srcGuide := &src.Realtime.Dashboard.SpeciesGuide

	// The Show* flags are plain bool value types; mutating the clone must not affect
	// the source (they ride the shallow struct copy with no shared pointer).
	dstGuide.ShowNotes = false
	dstGuide.ShowEnrichments = false
	dstGuide.ShowSimilarSpecies = false
	dstGuide.ShowTaxonomy = false
	assert.True(t, srcGuide.ShowNotes)
	assert.True(t, srcGuide.ShowEnrichments)
	assert.True(t, srcGuide.ShowSimilarSpecies)
	assert.True(t, srcGuide.ShowTaxonomy)
}
