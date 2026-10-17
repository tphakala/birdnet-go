package conf

import (
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

func TestImportElevationDefaultsOn(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()
	require.True(t, viper.GetBool("import.allowInAppElevation"),
		"in-app elevation must default to enabled")
}

func TestEqualizerDefaultsShipBypassed(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()
	require.False(t, viper.GetBool("realtime.audio.equalizer.enabled"))

	var filters []EqualizerFilter
	require.NoError(t, viper.UnmarshalKey("realtime.audio.equalizer.filters", &filters))
	require.Len(t, filters, 2)
	for _, f := range filters {
		require.Equal(t, 0, f.Passes, "%s ships with passes 0, which turns the filter off", f.Type)
	}
}

// TestEqualizerTemplateShipsBypassed pins the equalizer block of the embedded
// config.yaml that new installs receive, separately from the viper defaults
// above. Passes is a pointer so a deleted key fails instead of reading as 0,
// which is also the value that turns a filter off.
func TestEqualizerTemplateShipsBypassed(t *testing.T) {
	t.Parallel()

	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)

	var probe struct {
		Realtime struct {
			Audio struct {
				Equalizer struct {
					Enabled bool `yaml:"enabled"`
					Filters []struct {
						Type   string `yaml:"type"`
						Passes *int   `yaml:"passes"`
					} `yaml:"filters"`
				} `yaml:"equalizer"`
			} `yaml:"audio"`
		} `yaml:"realtime"`
	}
	require.NoError(t, yaml.Unmarshal(template, &probe))

	eq := probe.Realtime.Audio.Equalizer
	require.False(t, eq.Enabled)
	require.Len(t, eq.Filters, 2)
	for i, wantType := range []string{"HighPass", "LowPass"} {
		require.Equal(t, wantType, eq.Filters[i].Type)
		require.NotNil(t, eq.Filters[i].Passes, "%s must set passes in config.yaml", wantType)
		require.Equal(t, 0, *eq.Filters[i].Passes, "%s ships with passes 0 in config.yaml, which turns the filter off", wantType)
	}
}
