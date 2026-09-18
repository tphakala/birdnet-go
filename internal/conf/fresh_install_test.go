package conf

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFreshInstall_DefaultsToBirdNET pins that a brand-new install boots at N >= 1 with
// BirdNET v2.4 enabled, not N = 0. It replicates the fresh-install read path: the embedded
// default template (which now carries an explicit models.enabled: [birdnet] block) is stamped
// with the current configversion so the one-shot migrations skip it, then read and
// unmarshalled exactly as Load does.
//
// The fresh default now comes from the template block, backstopped by the models.enabled
// viper default (defaults.go). This test guards that coupling: a fresh install must not
// silently boot inert (model de-privilege epic, Phase 4).
func TestFreshInstall_DefaultsToBirdNET(t *testing.T) {
	t.Parallel()

	template, err := getDefaultConfig()
	require.NoError(t, err)
	require.Contains(t, template, "\nmodels:",
		"the embedded template must carry an explicit models block so a fresh install is authoritative")

	stamped := stampConfigVersion(template)

	v := viper.New()
	v.SetConfigType("yaml")
	// Mirror setDefaultConfig's models.enabled default (defaults.go) as the backstop.
	v.SetDefault("models.enabled", []string{ModelIDBirdNET})
	require.NoError(t, v.ReadConfig(strings.NewReader(stamped)))

	var s Settings
	require.NoError(t, v.Unmarshal(&s, viper.DecodeHook(DurationDecodeHook())))

	assert.Equal(t, currentConfigVersion, s.ConfigVersion, "a fresh config is stamped at the current version")
	assert.Equal(t, []string{ModelIDBirdNET}, s.Models.Enabled,
		"a fresh install must default to BirdNET v2.4 enabled (N >= 1), not N = 0")

	// The stamp makes both one-shot migrations skip, so the fresh default is never rewritten.
	assert.False(t, s.MigrateSourceTargetDefaults(), "M1 must not run on a freshly stamped config")
	assert.False(t, s.MigrateModelsEnabledAuthoritative(), "M2 must not run on a freshly stamped config")
	assert.Equal(t, []string{ModelIDBirdNET}, s.Models.Enabled, "the fresh default survives the skipped migrations")
}
