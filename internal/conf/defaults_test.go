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
// consensus filter: off, with the default minimum. Reading the viper keys
// directly matters because Load normalizes an unset minimum, which would hide a
// missing or misspelled default.
func TestModelConsensusDefaultsShipOff(t *testing.T) {
	t.Cleanup(viper.Reset)
	viper.Reset()
	setDefaultConfig()

	require.False(t, viper.GetBool("realtime.modelconsensus.allspecies"))
	require.False(t, viper.GetBool("realtime.modelconsensus.listedspecies"))
	require.False(t, viper.GetBool("realtime.modelconsensus.alleligible"))
	require.False(t, viper.GetBool("realtime.modelconsensus.debug"))
	require.Empty(t, viper.GetStringSlice("realtime.modelconsensus.species"))
	require.True(t, viper.IsSet("realtime.modelconsensus.minmodels"), "minmodels must have a viper default")
	require.Equal(t, DefaultModelConsensusMinModels, viper.GetInt("realtime.modelconsensus.minmodels"))
}

// TestModelConsensusTemplateShipsOff pins the model consensus block of the
// embedded config.yaml that new installs receive. The scalar fields are pointers
// so a deleted or renamed key fails instead of reading as the zero value; the
// species list is a plain slice, because a missing list and an empty one behave
// the same.
func TestModelConsensusTemplateShipsOff(t *testing.T) {
	t.Parallel()

	template, err := configFiles.ReadFile("config.yaml")
	require.NoError(t, err)

	var probe struct {
		Realtime struct {
			ModelConsensus struct {
				AllSpecies    *bool    `yaml:"allspecies"`
				ListedSpecies *bool    `yaml:"listedspecies"`
				Species       []string `yaml:"species"`
				MinModels     *int     `yaml:"minmodels"`
				AllEligible   *bool    `yaml:"alleligible"`
				Debug         *bool    `yaml:"debug"`
			} `yaml:"modelconsensus"`
		} `yaml:"realtime"`
	}
	require.NoError(t, yaml.Unmarshal(template, &probe))

	mc := probe.Realtime.ModelConsensus
	require.NotNil(t, mc.AllSpecies, "allspecies must be present in the template")
	require.NotNil(t, mc.ListedSpecies, "listedspecies must be present in the template")
	require.NotNil(t, mc.MinModels, "minmodels must be present in the template")
	require.NotNil(t, mc.AllEligible, "alleligible must be present in the template")
	require.NotNil(t, mc.Debug, "debug must be present in the template")
	require.False(t, *mc.AllSpecies)
	require.False(t, *mc.ListedSpecies)
	require.False(t, *mc.AllEligible)
	require.False(t, *mc.Debug)
	require.Empty(t, mc.Species)
	require.Equal(t, DefaultModelConsensusMinModels, *mc.MinModels)
}

// TestModelConsensusYAMLKeys pins the key names written when the settings are
// saved. Load matches keys to field names and never reads the yaml tags, so only
// a marshal can show that a saved file reads back under the same names.
func TestModelConsensusYAMLKeys(t *testing.T) {
	t.Parallel()

	out, err := yaml.Marshal(ModelConsensusSettings{
		AllSpecies: true, ListedSpecies: true, Species: []string{"Parus major"},
		MinModels: DefaultModelConsensusMinModels, AllEligible: true, Debug: true,
	})
	require.NoError(t, err)

	var keys map[string]any
	require.NoError(t, yaml.Unmarshal(out, &keys))
	require.ElementsMatch(t,
		[]string{"allspecies", "listedspecies", "species", "minmodels", "alleligible", "debug"},
		slices.Collect(maps.Keys(keys)))
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
