package cmd

import (
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestSetupFlags_KeepsNormalizedLocale guards the ordering bug that made every
// command run with an unnormalized locale. Config load normalizes birdnet.locale
// (conf.ValidateSettings rewrites e.g. "en" to "en-uk") in the settings struct
// while viper keeps the raw config value, and pflag writes a flag's default into
// its target at registration time. Taking the default from viper therefore
// clobbered the normalized locale before Execute ever parsed an argument, which
// left classifier.NewBirdNET to deal with a locale that config validation had
// already resolved.
func TestSetupFlags_KeepsNormalizedLocale(t *testing.T) {
	// setupFlags calls viper.BindPFlags, so this test leaves flag bindings as well
	// as values in the global registry. Reset the whole thing rather than restoring
	// the one key, so nothing added to this package later inherits the state.
	t.Cleanup(viper.Reset)
	viper.Set("birdnet.locale", "en") // raw config value, before normalization

	settings := &conf.Settings{}
	settings.BirdNET.Locale = conf.DefaultFallbackLocale // what validation produced

	rootCmd := &cobra.Command{Use: "birdnet"}
	require.NoError(t, setupFlags(rootCmd, settings))

	assert.Equal(t, conf.DefaultFallbackLocale, settings.BirdNET.Locale,
		"flag registration must not overwrite the validated locale")
	assert.Equal(t, conf.DefaultFallbackLocale, rootCmd.PersistentFlags().Lookup("locale").DefValue,
		"--help must advertise the locale the app actually runs with")

	// An explicit override still wins over the validated value.
	require.NoError(t, rootCmd.PersistentFlags().Parse([]string{"--locale", "de"}))
	assert.Equal(t, "de", settings.BirdNET.Locale, "--locale must still override")
}
