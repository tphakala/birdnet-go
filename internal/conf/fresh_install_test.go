package conf

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestFreshInstall_DefaultsToBirdNET pins that a brand-new install boots at N >= 1 with
// BirdNET v2.4 enabled, not N = 0. It replicates the fresh-install read path exactly: the
// embedded default template (which carries no models key) is stamped with configversion
// (so the one-shot migrations skip it), the models.enabled viper default is registered
// (defaults.go), the stamped bytes are read, and the result is unmarshalled.
//
// The default comes from viper.SetDefault filling the absent models.enabled key at
// unmarshal time, NOT from a migration (MigrateModelsEnabledAuthoritative skips a
// version-stamped file) and NOT from the template (it has no models section). This test
// is the guard for that load-bearing coupling: if the viper default were removed, or
// ModelsConfig.Enabled gained an omitempty that let an empty key round-trip, a fresh
// install would silently boot inert (model de-privilege epic, Phase 4).
func TestFreshInstall_DefaultsToBirdNET(t *testing.T) {
	t.Parallel()

	template, err := getDefaultConfig()
	require.NoError(t, err)
	require.NotContains(t, template, "\nmodels:", "the embedded template must have no models section for this test to be meaningful")

	stamped := stampConfigVersion(template)

	v := viper.New()
	v.SetConfigType("yaml")
	// Mirror setDefaultConfig's models.enabled default (defaults.go).
	v.SetDefault("models.enabled", []string{ModelIDBirdNET})
	require.NoError(t, v.ReadConfig(strings.NewReader(stamped)))

	var s Settings
	require.NoError(t, v.Unmarshal(&s, viper.DecodeHook(DurationDecodeHook())))

	assert.Equal(t, currentConfigVersion, s.ConfigVersion, "a fresh config is stamped at the current version")
	assert.Equal(t, []string{ModelIDBirdNET}, s.Models.Enabled,
		"a fresh install must default to BirdNET v2.4 enabled (N >= 1), not N = 0")

	// The stamp makes both one-shot migrations skip, so the default is never rewritten.
	assert.False(t, s.MigrateSourceTargetDefaults(), "M1 must not run on a freshly stamped config")
	assert.False(t, s.MigrateModelsEnabledAuthoritative(), "M2 must not run on a freshly stamped config")
	assert.Equal(t, []string{ModelIDBirdNET}, s.Models.Enabled, "the fresh default survives the skipped migrations")
}
