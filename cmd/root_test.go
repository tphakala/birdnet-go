package cmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
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

// TestSilenceUsageOnRunErrors pins where cobra prints usage: errors found before
// RunE (unknown flag, missing required flag) keep the usage text, an error that
// RunE itself returns does not. Cobra still prints the error line in every case.
func TestSilenceUsageOnRunErrors(t *testing.T) {
	t.Parallel()

	errRuntime := errors.NewStd("runtime failure")

	newRoot := func() *cobra.Command {
		root := &cobra.Command{Use: "app"}
		group := &cobra.Command{Use: "group"}
		group.AddCommand(&cobra.Command{
			Use:  "fail",
			RunE: func(*cobra.Command, []string) error { return errRuntime },
		})
		needsFlag := &cobra.Command{
			Use:  "needs-flag",
			RunE: func(*cobra.Command, []string) error { return nil },
		}
		needsFlag.Flags().String("src", "", "source")
		require.NoError(t, needsFlag.MarkFlagRequired("src"))
		root.AddCommand(group, needsFlag)
		silenceUsageOnRunErrors(root)
		return root
	}

	tests := []struct {
		name      string
		args      []string
		wantErr   error
		wantUsage bool
	}{
		{name: "nested runtime error prints no usage", args: []string{"group", "fail"}, wantErr: errRuntime},
		{name: "unknown flag prints usage", args: []string{"group", "fail", "--nosuchflag"}, wantUsage: true},
		{name: "missing required flag prints usage", args: []string{"needs-flag"}, wantUsage: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			root := newRoot()
			var out bytes.Buffer
			root.SetOut(&out)
			root.SetErr(&out)
			root.SetArgs(tt.args)

			err := root.Execute()
			require.Error(t, err)
			if tt.wantErr != nil {
				require.ErrorIs(t, err, tt.wantErr)
			}
			assert.Equal(t, tt.wantUsage, strings.Contains(out.String(), "Usage:"), "output: %s", out.String())
			assert.Contains(t, out.String(), "Error:", "cobra must still print the error line")
		})
	}
}
