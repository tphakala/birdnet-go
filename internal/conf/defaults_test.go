package conf

import (
	"maps"
	"slices"
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

func TestBatDiscardUnlikelyDefaultsOff(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()
	require.False(t, viper.GetBool("bat.ultrasonicfilter.discardunlikely"),
		"discarding unlikely bat detections must be opt-in")
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

// TestAlternateMinConfidenceDefault pins the default of
// birdnet.alternateminconfidence in the viper defaults and in the embedded
// config.yaml, and that a saved 0 stays in the YAML so the default cannot come
// back on the next start. Not parallel: it resets the global viper instance.
func TestAlternateMinConfidenceDefault(t *testing.T) {
	t.Cleanup(viper.Reset)

	viper.Reset()
	setDefaultConfig()
	require.InDelta(t, DefaultAlternateMinConfidence, viper.GetFloat64("birdnet.alternateminconfidence"), 1e-9)

	// Pointer field so a deleted template key fails instead of reading as zero.
	var probe struct {
		BirdNET struct {
			AlternateMinConfidence *float64 `yaml:"alternateminconfidence"`
		} `yaml:"birdnet"`
	}
	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(template, &probe))
	require.NotNil(t, probe.BirdNET.AlternateMinConfidence, "config.yaml must set alternateminconfidence")
	require.InDelta(t, DefaultAlternateMinConfidence, *probe.BirdNET.AlternateMinConfidence, 1e-9)

	out, err := yaml.Marshal(BirdNETConfig{AlternateMinConfidence: 0})
	require.NoError(t, err)
	require.Contains(t, string(out), "alternateminconfidence: 0", "a saved 0 must be written, not omitted")
}

// TestModelConsensusDefaultsShipOff pins the viper defaults of the model
// consensus filter: off, with no default for the removed keys. A default for one of
// them would be read by the version 4 migration as a value the user chose.
func TestModelConsensusDefaultsShipOff(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()

	require.False(t, viper.GetBool("realtime.modelconsensus.debug"))
	require.Empty(t, viper.Get("realtime.modelconsensus.rules"))
	for _, legacy := range []string{"allspecies", "listedspecies", "species", "minmodels", "alleligible"} {
		require.False(t, viper.IsSet("realtime.modelconsensus."+legacy),
			"%s must have no viper default, the migration decodes it as a user value", legacy)
	}
}

// TestModelConsensusTemplateShipsOff pins the model consensus block of the
// embedded config.yaml that new installs receive. The debug field is a pointer so
// a deleted or renamed key fails instead of reading as the zero value, and rules
// is decoded to a map to tell a present empty list from a missing key.
func TestModelConsensusTemplateShipsOff(t *testing.T) {
	t.Parallel()

	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)

	var probe struct {
		Realtime struct {
			ModelConsensus map[string]any `yaml:"modelconsensus"`
		} `yaml:"realtime"`
	}
	require.NoError(t, yaml.Unmarshal(template, &probe))

	mc := probe.Realtime.ModelConsensus
	require.Contains(t, mc, "debug", "debug must be present in the template")
	require.Contains(t, mc, "rules", "rules must be present in the template")
	require.Equal(t, false, mc["debug"])
	require.Empty(t, mc["rules"])
	for _, legacy := range []string{"allspecies", "listedspecies", "species", "minmodels", "alleligible"} {
		require.NotContains(t, mc, legacy)
	}
}

// TestModelConsensusYAMLKeys pins the key names written when the settings are
// saved. Load matches keys to field names and never reads the yaml tags, so only
// a marshal can show that a saved file reads back under the same names.
func TestModelConsensusYAMLKeys(t *testing.T) {
	t.Parallel()

	out, err := yaml.Marshal(ModelConsensusSettings{
		Rules: []ModelConsensusRule{{
			Species: []string{"Parus major"}, Models: []string{"perch_v2"},
			MinModels: DefaultModelConsensusMinModels, AllEligible: true,
		}},
		Debug: true,
	})
	require.NoError(t, err)

	var keys struct {
		Debug bool             `yaml:"debug"`
		Rules []map[string]any `yaml:"rules"`
	}
	require.NoError(t, yaml.Unmarshal(out, &keys))
	require.True(t, keys.Debug)
	require.Len(t, keys.Rules, 1)
	require.ElementsMatch(t,
		[]string{"species", "models", "minmodels", "alleligible"},
		slices.Collect(maps.Keys(keys.Rules[0])))

	var top map[string]any
	require.NoError(t, yaml.Unmarshal(out, &top))
	require.ElementsMatch(t, []string{"debug", "rules"}, slices.Collect(maps.Keys(top)))
}

// TestModuleLogLevelTemplateMatchesCodeDefaults pins the module log levels of the
// embedded config.yaml (what new installs receive) to the viper defaults (what a
// module entry without a level receives), so the two cannot drift apart again. Not
// parallel: it resets the global viper instance.
func TestModuleLogLevelTemplateMatchesCodeDefaults(t *testing.T) {
	t.Cleanup(viper.Reset)

	// Pointer field so a deleted template level fails instead of reading as empty.
	var probe struct {
		Logging struct {
			Modules map[string]struct {
				Level *string `yaml:"level"`
			} `yaml:"modules"`
		} `yaml:"logging"`
	}
	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)
	require.NoError(t, yaml.Unmarshal(template, &probe))

	viper.Reset()
	setDefaultConfig()

	require.Equal(t,
		slices.Sorted(maps.Keys(probe.Logging.Modules)),
		slices.Sorted(maps.Keys(viper.GetStringMap("logging.modules"))),
		"config.yaml and setDefaultConfig must declare the same log modules")

	for module, tmpl := range probe.Logging.Modules {
		t.Run(module, func(t *testing.T) {
			require.NotNil(t, tmpl.Level, "config.yaml must set the level of module %s", module)
			got := viper.GetString("logging.modules." + module + ".level")
			require.Equal(t, *tmpl.Level, got, "config.yaml level must equal the default in defaults.go")
			require.Equal(t, "info", got, "modules log at info by default")
		})
	}
}
