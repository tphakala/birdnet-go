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

// TestRetryTemplateMatchesCodeDefaults pins the retry settings of the embedded
// config.yaml (what new installs receive) to the viper defaults (what a config
// without the keys receives), so the two cannot drift apart again. Not parallel:
// it resets the global viper instance.
func TestRetryTemplateMatchesCodeDefaults(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Pointer fields so a deleted template key fails instead of reading as zero.
	type retryProbe struct {
		Enabled           *bool    `yaml:"enabled"`
		MaxRetries        *int     `yaml:"maxretries"`
		InitialDelay      *int     `yaml:"initialdelay"`
		MaxDelay          *int     `yaml:"maxdelay"`
		BackoffMultiplier *float64 `yaml:"backoffmultiplier"`
	}
	var probe struct {
		Realtime struct {
			MQTT struct {
				RetrySettings retryProbe `yaml:"retrysettings"`
			} `yaml:"mqtt"`
			Birdweather struct {
				RetrySettings retryProbe `yaml:"retrysettings"`
			} `yaml:"birdweather"`
		} `yaml:"realtime"`
	}
	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(template, &probe))

	tests := []struct {
		prefix   string
		template retryProbe
	}{
		{"realtime.mqtt.retrysettings", probe.Realtime.MQTT.RetrySettings},
		{"realtime.birdweather.retrysettings", probe.Realtime.Birdweather.RetrySettings},
	}

	viper.Reset()
	setDefaultConfig()

	for _, tt := range tests {
		t.Run(tt.prefix, func(t *testing.T) {
			require.NotNil(t, tt.template.Enabled, "config.yaml must set enabled")
			require.NotNil(t, tt.template.MaxRetries, "config.yaml must set maxretries")
			require.NotNil(t, tt.template.InitialDelay, "config.yaml must set initialdelay")
			require.NotNil(t, tt.template.MaxDelay, "config.yaml must set maxdelay")
			require.NotNil(t, tt.template.BackoffMultiplier, "config.yaml must set backoffmultiplier")

			want := RetrySettings{
				Enabled:           viper.GetBool(tt.prefix + ".enabled"),
				MaxRetries:        viper.GetInt(tt.prefix + ".maxretries"),
				InitialDelay:      viper.GetInt(tt.prefix + ".initialdelay"),
				MaxDelay:          viper.GetInt(tt.prefix + ".maxdelay"),
				BackoffMultiplier: viper.GetFloat64(tt.prefix + ".backoffmultiplier"),
			}
			got := RetrySettings{
				Enabled:           *tt.template.Enabled,
				MaxRetries:        *tt.template.MaxRetries,
				InitialDelay:      *tt.template.InitialDelay,
				MaxDelay:          *tt.template.MaxDelay,
				BackoffMultiplier: *tt.template.BackoffMultiplier,
			}
			require.Equal(t, want, got, "config.yaml retry settings must equal the defaults in defaults.go")
		})
	}
}
