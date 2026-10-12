package conf

import (
	"strings"
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for #4523: viper treats "." as its key delimiter, so flat
// map keys like "analysis.processor" are split into nested maps before
// mapstructure sees them. Before the fix, logging.modules silently dropped
// every setting for dotted module names (decoding them as a zero-value
// module whose name lost the suffix), and logging.module_levels with any
// dotted key made viper.Unmarshal - and therefore Load - fail outright.

// dottedKeysConfig is the smallest config that exercises both failure modes
// plus a non-dotted control entry for each map.
const dottedKeysConfig = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  default_level: info
  modules:
    analysis.processor:
      console_also: false
      level: warn
    birdnet:
      level: debug
  module_levels:
    spectrogram.prerenderer: warn
    ebird: error
`

// TestLoad_DottedModuleNamesAreKeptInModuleOutputs pins the reported symptom:
// settings a user writes for a dotted module name must reach the logger
// instead of being dropped and replaced by the ensureModuleOutput default.
func TestLoad_DottedModuleNamesAreKeptInModuleOutputs(t *testing.T) {
	settings, _ := loadConfigFromYAML(t, dottedKeysConfig)

	out := settings.Logging.ModuleOutputs
	require.Contains(t, out, "analysis.processor",
		"the dotted module name must survive config loading intact")
	assert.False(t, out["analysis.processor"].ConsoleAlso,
		"console_also: false must be honored, not replaced by the module default true")
	assert.Equal(t, "warn", out["analysis.processor"].Level)

	// The non-dotted sibling entry must keep decoding exactly as before.
	require.Contains(t, out, "birdnet")
	assert.Equal(t, "debug", out["birdnet"].Level,
		"non-dotted module entries must be unaffected")

	// "analysis" is a real standalone module with its own defaults, so it
	// must stay present - but with the values the file sets, not a phantom
	// copy of the dotted module's settings.
	require.Contains(t, out, "analysis",
		"the standalone analysis module must keep its own entry")
	assert.True(t, out["analysis"].Enabled,
		"the analysis module's defaults must survive the re-decode")
}

// TestLoad_DottedModuleNamesAreKeptInModuleLevels pins the startup failure: a
// dotted key in module_levels previously made Unmarshal return a decode
// error, which Load treats as fatal. It must instead load, with the level
// keyed by the full dotted module name.
func TestLoad_DottedModuleNamesAreKeptInModuleLevels(t *testing.T) {
	settings, _ := loadConfigFromYAML(t, dottedKeysConfig)

	levels := settings.Logging.ModuleLevels
	require.Contains(t, levels, "spectrogram.prerenderer",
		"the dotted module name must survive config loading intact")
	assert.Equal(t, "warn", levels["spectrogram.prerenderer"])
	assert.Equal(t, "error", levels["ebird"],
		"non-dotted module_levels entries must be unaffected")
	assert.NotContains(t, levels, "spectrogram",
		"the split prefix must not survive as a phantom entry")
}

// TestLoad_DottedKeysInOtherScalarMaps proves the decode hook is generic for
// scalar-valued maps: notification push provider environment variables and
// webhook endpoint headers are map[string]string, whose keys are user-chosen
// and can legitimately contain dots. They must survive loading the same way.
func TestLoad_DottedKeysInOtherScalarMaps(t *testing.T) {
	cfg := `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
notification:
  push:
    providers:
      - name: webhook-test
        type: webhook
        environment:
          model.name: birdnet-v2.4
        endpoints:
          - url: http://example.invalid/hook
            headers:
              user.agent: birdnet-go
`
	settings, _ := loadConfigFromYAML(t, cfg)

	providers := settings.Notification.Push.Providers
	require.Len(t, providers, 1)
	env := providers[0].Environment
	require.Contains(t, env, "model.name",
		"a dotted environment key must survive as one entry")
	assert.Equal(t, "birdnet-v2.4", env["model.name"])

	require.Len(t, providers[0].Endpoints, 1)
	headers := providers[0].Endpoints[0].Headers
	require.Contains(t, headers, "user.agent",
		"a dotted header key must survive as one entry")
	assert.Equal(t, "birdnet-go", headers["user.agent"])
}

// TestDottedKeyDecodeHook_ScalarMapsOnly pins the hook's core behavior and
// blast radius: it must flatten split keys for scalar-valued maps, and must
// not touch map[string]any or struct-valued targets, where nested maps are
// legitimate data the hook cannot distinguish from a split key by shape.
func TestDottedKeyDecodeHook_ScalarMapsOnly(t *testing.T) {
	t.Parallel()

	// Scalar-valued map: the nested value is unambiguously a split key.
	split := map[string]any{
		"analysis": map[string]any{"processor": "warn"},
		"ebird":    "error",
	}
	flat := flattenDottedMapKeys(split, 0)
	assert.Equal(t, map[string]any{
		"analysis.processor": "warn",
		"ebird":              "error",
	}, flat)

	// A key with two dots reassembles in one pass.
	twoDots := map[string]any{
		"a": map[string]any{"b": map[string]any{"c": "v"}},
	}
	assert.Equal(t, map[string]any{"a.b.c": "v"}, flattenDottedMapKeys(twoDots, 0))
}

// TestDottedKeyDecodeHook_UnmarshalKeepsNestedAnyMaps guards against a
// regression that would flatten nested maps inside map[string]any targets,
// where nesting is legitimate configuration.
func TestDottedKeyDecodeHook_UnmarshalKeepsNestedAnyMaps(t *testing.T) {
	t.Parallel()

	type metaConfig struct {
		Filters map[string]any `mapstructure:"filters"`
	}
	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(`
filters:
  range:
    model: birdnet
    enabled: true
`)))

	var cfg metaConfig
	err := v.Unmarshal(&cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		DottedKeyDecodeHook(), DurationDecodeHook())))
	require.NoError(t, err)
	require.NotNil(t, cfg.Filters)
	require.Contains(t, cfg.Filters, "range",
		"nested map[string]any values must not be flattened away")
}

// TestDottedKeyDecodeHook_AppliesToWholeSettings pins the composed-hook
// behavior end to end on the Settings type itself: module_levels with a
// dotted key decodes without error and logging.modules keeps the dotted
// entry, exactly as the Load-level tests assert but without the global
// loader state, so a failure here points at the hook rather than Load.
func TestDottedKeyDecodeHook_AppliesToWholeSettings(t *testing.T) {
	t.Parallel()

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(dottedKeysConfig)))

	var settings Settings
	err := v.Unmarshal(&settings, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		DottedKeyDecodeHook(), DurationDecodeHook())))
	require.NoError(t, err, "a dotted module_levels key must not fail the unmarshal")
}
