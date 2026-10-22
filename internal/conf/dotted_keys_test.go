package conf

import (
	"testing"

	"github.com/go-viper/mapstructure/v2"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Regression tests for #4523: viper treats "." as its key delimiter, so flat
// map keys like "analysis.processor" are split into nested maps before
// mapstructure sees them. Before the fix, logging.modules silently dropped
// every setting for dotted module names (decoding them as a zero-value
// module whose name lost the suffix), and logging.module_levels with any
// dotted key made viper.Unmarshal - and therefore Load - fail outright.
// taxonomySynonyms is the other scalar-valued map the hook protects.

// dottedKeyLoadIterations repeats a Load whose outcome depends on viper's
// random map iteration order. Without the repair a single load fails about
// 70% of the time, so 20 loads leave a negligible chance of a false pass.
const dottedKeyLoadIterations = 20

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
// instead of being dropped and replaced by the logger's defaultModuleOutputs entry.
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

// TestLoad_PartialDottedModuleEntryKeepsDefaults pins the second half of the
// dotted-key contract: a partial dotted entry must honor the fields the user
// set while keeping the built-in defaults for every field they did not.
// Before this was fixed, "analysis.processor: {console_also: false}" decoded
// over a zero-value base and came out Enabled=false with an empty FilePath,
// which silently stopped the module's log file from being written - and
// because the key now existed, applyConfigDefaults could not repair it.
func TestLoad_PartialDottedModuleEntryKeepsDefaults(t *testing.T) {
	cfg := `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    analysis.processor:
      console_also: false
`
	settings, _ := loadConfigFromYAML(t, cfg)

	out := settings.Logging.ModuleOutputs
	require.Contains(t, out, "analysis.processor",
		"the dotted module name must survive config loading intact")
	// The field the user set must be honored.
	assert.False(t, out["analysis.processor"].ConsoleAlso,
		"console_also: false must be honored, not replaced by the module default true")
	// The fields the user did not set must keep the module's defaults.
	assert.True(t, out["analysis.processor"].Enabled,
		"a partial entry must not disable the module's log file")
	assert.Equal(t, "logs/actions.log", out["analysis.processor"].FilePath,
		"a partial entry must keep the module's default log file path")

	// Same contract for a second dotted module whose default lives in a
	// different file (audio.ffmpeg defaults to logs/audio.log), again set
	// through logging.modules with only `enabled`, so the file path must come
	// from the module default.
	cfg2 := `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    audio.ffmpeg:
      enabled: true
`
	settings2, _ := loadConfigFromYAML(t, cfg2)
	out2 := settings2.Logging.ModuleOutputs
	require.Contains(t, out2, "audio.ffmpeg")
	assert.True(t, out2["audio.ffmpeg"].Enabled)
	assert.Equal(t, "logs/audio.log", out2["audio.ffmpeg"].FilePath,
		"a partial entry must keep the audio module's default log file path")
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

// TestLoad_DottedTaxonomySynonymKeysAreKept pins that the decode hook is wired
// into Load() for a scalar-valued map that has no re-decode behind it.
// taxonomySynonyms keys are user-chosen and legitimately contain dots
// ("larus sp.", "T. migratorius"). Viper splits keys of scalar-valued maps it
// reaches by key path, which today means logging.module_levels and
// taxonomySynonyms. Without the hook a single load fails about 70% of the
// time, depending on viper's random map order, so the load repeats. Keys come
// back lowercased by viper, which is fine because buildSynonymIndexes
// lowercases them anyway.
func TestLoad_DottedTaxonomySynonymKeysAreKept(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
taxonomySynonyms:
  larus sp.: larus argentatus
  T. migratorius: turdus migratorius
`
	want := map[string]string{
		"larus sp.":      "larus argentatus",
		"t. migratorius": "turdus migratorius",
	}
	for i := range dottedKeyLoadIterations {
		settings, _ := loadConfigFromYAML(t, cfg)
		require.Equal(t, want, settings.TaxonomySynonyms, "iteration %d", i)
	}
}

// TestLoad_DottedChildAndParentModuleKeysAreBothKept pins the case the decode
// hook cannot recover: a module and its dotted child set side by side. Viper's
// AllSettings keeps only one of "audio" and "audio.ffmpeg" (one path needs a
// string where the other needs a map), so both maps are rebuilt from the raw
// subtree. Parent and child differ in every pair so a swapped or lost entry
// shows. The outcome depends on viper's random map order, so the load repeats.
func TestLoad_DottedChildAndParentModuleKeysAreBothKept(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    audio:
      level: info
    audio.ffmpeg:
      level: warn
    spectrogram:
      level: error
    spectrogram.prerenderer:
      level: info
  module_levels:
    audio: info
    audio.ffmpeg: warn
    spectrogram: error
    spectrogram.prerenderer: warn
`
	wantLevels := map[string]string{
		"audio":                   "info",
		"audio.ffmpeg":            "warn",
		"spectrogram":             "error",
		"spectrogram.prerenderer": "warn",
	}
	wantOutputLevels := map[string]string{
		"audio":                   "info",
		"audio.ffmpeg":            "warn",
		"spectrogram":             "error",
		"spectrogram.prerenderer": "info",
	}
	for i := range dottedKeyLoadIterations {
		settings, _ := loadConfigFromYAML(t, cfg)
		require.Equal(t, wantLevels, settings.Logging.ModuleLevels, "iteration %d", i)
		for name, level := range wantOutputLevels {
			require.Contains(t, settings.Logging.ModuleOutputs, name, "iteration %d", i)
			assert.Equal(t, level, settings.Logging.ModuleOutputs[name].Level,
				"iteration %d, module %s", i, name)
		}
	}
}

// TestLoad_ScalarDottedModuleEntryIsIgnored pins that a scalar under a dotted
// logging.modules name does not fail Load. Main ignores it (viper splits the
// name and Unmarshal drops the unknown field), so the re-decode must skip
// values that are not mappings instead of failing startup.
func TestLoad_ScalarDottedModuleEntryIsIgnored(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    analysis.processor: true
`
	settings, _ := loadConfigFromYAML(t, cfg)

	out := settings.Logging.ModuleOutputs
	assert.NotContains(t, out, "analysis.processor",
		"a scalar entry is ignored as on main; the logger defaults fill the module")
	assert.True(t, out["analysis"].Enabled,
		"the standalone analysis module must not be damaged")
}

// TestDottedKeyDecodeHook_ScalarMapsOnly pins the hook's core behavior and
// blast radius through the hook itself: it flattens split keys for
// scalar-valued maps, and leaves map[string]any and struct-valued targets
// alone, where nested maps are legitimate data the hook cannot distinguish
// from a split key by shape.
func TestDottedKeyDecodeHook_ScalarMapsOnly(t *testing.T) {
	t.Parallel()

	type moduleStub struct {
		Level string `mapstructure:"level"`
	}

	t.Run("scalar map is flattened", func(t *testing.T) {
		t.Parallel()
		var got map[string]string
		decodeThroughHook(t, map[string]any{
			"analysis": map[string]any{"processor": "warn"},
			"a":        map[string]any{"b": map[string]any{"c": "v"}},
			"ebird":    "error",
		}, &got)
		assert.Equal(t, map[string]string{
			"analysis.processor": "warn",
			"a.b.c":              "v",
			"ebird":              "error",
		}, got)
	})

	t.Run("any map keeps its nesting", func(t *testing.T) {
		t.Parallel()
		input := map[string]any{"range": map[string]any{"model": "birdnet"}}
		var got map[string]any
		decodeThroughHook(t, input, &got)
		assert.Equal(t, input, got)
	})

	t.Run("struct map keeps its keys", func(t *testing.T) {
		t.Parallel()
		var got map[string]moduleStub
		decodeThroughHook(t, map[string]any{
			"birdnet": map[string]any{"level": "debug"},
		}, &got)
		assert.Equal(t, map[string]moduleStub{"birdnet": {Level: "debug"}}, got)
	})
}

// decodeThroughHook decodes input into result with only DottedKeyDecodeHook
// installed, so a test sees what the hook does and nothing else.
func decodeThroughHook(t *testing.T, input, result any) {
	t.Helper()
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		Result:     result,
		DecodeHook: DottedKeyDecodeHook(),
	})
	require.NoError(t, err)
	require.NoError(t, decoder.Decode(input))
}
