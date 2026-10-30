package conf

import (
	"maps"
	"os"
	"slices"
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

// TestLoad_DottedModuleNamesLeavePhantomPrefixOut pins that a dotted
// logging.modules name does not leave the zero-value entry Unmarshal creates
// under its first segment, unless something really sets that segment: a
// sibling entry in the file, or a built-in default (audio, analysis,
// spectrogram), which viper.IsSet reports for the exact path.
func TestLoad_DottedModuleNamesLeavePhantomPrefixOut(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    foo.bar:
      level: warn
      file_path: logs/foo.log
    audiocore.x:
      level: error
    baz:
      level: info
    baz.qux:
      level: warn
    quux.scalar: true
    audio.ffmpeg:
      level: warn
`
	for i := range dottedKeyLoadIterations {
		settings, _ := loadConfigFromYAML(t, cfg)
		out := settings.Logging.ModuleOutputs

		require.Contains(t, out, "foo.bar", "iteration %d", i)
		assert.Equal(t, "warn", out["foo.bar"].Level, "iteration %d", i)
		assert.Equal(t, "logs/foo.log", out["foo.bar"].FilePath, "iteration %d", i)
		require.Contains(t, out, "audiocore.x", "iteration %d", i)

		for _, phantom := range []string{"foo", "audiocore", "quux"} {
			assert.NotContains(t, out, phantom,
				"iteration %d: no phantom entry for the prefix of a dotted module name", i)
		}

		// A real parent next to a dotted child keeps both.
		require.Contains(t, out, "baz", "iteration %d", i)
		assert.Equal(t, "info", out["baz"].Level, "iteration %d", i)
		require.Contains(t, out, "baz.qux", "iteration %d", i)
		assert.Equal(t, "warn", out["baz.qux"].Level, "iteration %d", i)

		// A built-in dotted module must not remove its built-in parent.
		require.Contains(t, out, "audio", "iteration %d", i)
		assert.True(t, out["audio"].Enabled, "iteration %d", i)
		assert.Contains(t, out, "audio.ffmpeg", "iteration %d", i)
	}
}

// TestLoad_DottedModuleNamesAreNotPersistedWithPhantomPrefix pins the visible
// consequence: the file a migration rewrites during Load must not carry the
// phantom prefix entry.
func TestLoad_DottedModuleNamesAreNotPersistedWithPhantomPrefix(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    foo.bar:
      level: warn
`
	for range dottedKeyLoadIterations {
		_, configPath := loadConfigFromYAML(t, cfg)

		data, err := os.ReadFile(configPath) //nolint:gosec // test-controlled path
		require.NoError(t, err)
		require.NotEqual(t, cfg, string(data),
			"the test config must trigger a migration save, otherwise the disk check proves nothing")

		persisted := readPersistedSettings(t, configPath).Logging.ModuleOutputs
		assert.Contains(t, persisted, "foo.bar")
		assert.NotContains(t, persisted, "foo",
			"the migration save must not write the phantom prefix entry")
	}
}

// TestLoad_BadDottedModuleEntryNamesTheModule pins that a mapping entry with an
// invalid field value fails Load with an error that names the module. The
// decoder reports field paths relative to the entry, so without the module in
// the message a dotted entry would only report "max_size".
func TestLoad_BadDottedModuleEntryNamesTheModule(t *testing.T) {
	const cfg = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
logging:
  modules:
    analysis.processor:
      max_size: not-a-number
`
	prepareConfigFromYAML(t, cfg)

	_, err := Load()
	require.Error(t, err)
	assert.Contains(t, err.Error(), "logging.modules[analysis.processor]",
		"the error must name the module the decoder rejected")
	assert.Contains(t, err.Error(), "max_size")
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

// structMapConfigPrefix is the config every struct-map test shares: a valid
// session secret so Load passes validation.
const structMapConfigPrefix = `
security:
  sessionsecret: "0123456789abcdef0123456789abcdef"
`

// dottedSpeciesConfig carries species names with dots: an abbreviation with an
// inner dot, an apostrophe, a trailing dot, a parent key ("St") that coexists
// with a dotted child, and a non-dotted control. Loading this minimal config
// already triggers migration saves, which the test asserts before it relies on
// them.
const dottedSpeciesConfig = structMapConfigPrefix + `
realtime:
  species:
    config:
      "St. Helena Plover":
        threshold: 0.8
        interval: 30
        actions:
          - type: ExecuteCommand
            command: /bin/true
            parameters: [CommonName, Confidence]
      "Mrs. Gould's Sunbird":
        threshold: "0.9"
      "Larus sp.":
        threshold: 0.6
      "St":
        threshold: 0.5
      "American Robin":
        threshold: 0.7
`

var dottedSpeciesKeys = []string{
	"st. helena plover", "mrs. gould's sunbird", "larus sp.", "st", "american robin",
}

// TestLoad_DottedSpeciesConfigKeysAreKept pins that species names containing a
// dot reach Settings.Realtime.Species.Config intact, both in memory and in the
// file a migration rewrites during Load, with no zero-value prefix entry.
func TestLoad_DottedSpeciesConfigKeysAreKept(t *testing.T) {
	for range dottedKeyLoadIterations {
		settings, configPath := loadConfigFromYAML(t, dottedSpeciesConfig)

		cfg := settings.Realtime.Species.Config
		require.ElementsMatch(t, dottedSpeciesKeys, mapKeys(cfg),
			"dotted names must be kept, with no phantom prefix entry (mrs, larus sp)")

		helena := cfg["st. helena plover"]
		assert.InDelta(t, 0.8, helena.Threshold, 1e-9)
		assert.Equal(t, 30, helena.Interval)
		require.Len(t, helena.Actions, 1)
		assert.Equal(t, "ExecuteCommand", helena.Actions[0].Type)
		assert.Equal(t, []string{"CommonName", "Confidence"}, helena.Actions[0].Parameters)

		assert.InDelta(t, 0.9, cfg["mrs. gould's sunbird"].Threshold, 1e-9,
			"a string threshold must convert with the same weak typing Unmarshal uses")
		assert.InDelta(t, 0.6, cfg["larus sp."].Threshold, 1e-9)
		assert.InDelta(t, 0.5, cfg["st"].Threshold, 1e-9,
			"the real parent key must keep its own value")
		assert.InDelta(t, 0.7, cfg["american robin"].Threshold, 1e-9)

		// A migration rewrote the file during Load; it must hold the repaired keys.
		data, err := os.ReadFile(configPath) //nolint:gosec // test-controlled path
		require.NoError(t, err)
		require.NotEqual(t, dottedSpeciesConfig, string(data),
			"the test config must trigger a migration save, otherwise the disk check proves nothing")
		assert.ElementsMatch(t, dottedSpeciesKeys, mapKeys(readPersistedSettings(t, configPath).Realtime.Species.Config),
			"the migration save must not write a phantom entry or drop a dotted one")
	}
}

// TestLoad_DottedSeasonNamesKeepDefaultSeasons pins that dotted season names
// load (the phantom "early" season has zero dates and used to fail validation)
// and that a default season sharing a prefix with a dotted one is kept.
func TestLoad_DottedSeasonNamesKeepDefaultSeasons(t *testing.T) {
	const cfg = structMapConfigPrefix + `
realtime:
  speciestracking:
    seasonaltracking:
      seasons:
        early.spring:
          startmonth: 2
          startday: 5
        spring.late:
          startmonth: 4
          startday: 1
`
	for range dottedKeyLoadIterations {
		settings, _ := loadConfigFromYAML(t, cfg)

		seasons := settings.Realtime.SpeciesTracking.SeasonalTracking.Seasons
		require.ElementsMatch(t,
			[]string{"early.spring", "spring.late", "spring", "summer", "fall", "winter"},
			mapKeys(seasons))
		assert.Equal(t, Season{StartMonth: 2, StartDay: 5}, seasons["early.spring"])
		assert.Equal(t, Season{StartMonth: 4, StartDay: 1}, seasons["spring.late"])
		assert.Equal(t, Season{StartMonth: 3, StartDay: 20}, seasons["spring"],
			"the default season sharing a prefix with a dotted name must stay")
	}
}

// TestLoad_DottedStructMapKeysSurviveSaveAndReload pins that the keys the save
// path writes load back to the same maps, repeatedly.
func TestLoad_DottedStructMapKeysSurviveSaveAndReload(t *testing.T) {
	const cfg = structMapConfigPrefix + `
realtime:
  species:
    config:
      "St. Helena Plover":
        threshold: 0.8
      "Larus sp.":
        threshold: 0.6
      "American Robin":
        threshold: 0.7
  speciestracking:
    seasonaltracking:
      seasons:
        early.spring:
          startmonth: 2
          startday: 5
`
	first, configPath := loadConfigFromYAML(t, cfg)
	require.NoError(t, SaveYAMLConfig(configPath, first))

	// The save writes "actions: []" for entries without actions, so the first
	// reload (empty slice) is the baseline for the fixed-point comparison, not
	// the first load (nil slice); the key sets are compared against the first.
	var prev *Settings
	for range 3 {
		data, err := os.ReadFile(configPath) //nolint:gosec // test-controlled path
		require.NoError(t, err)
		next, nextPath := loadConfigFromYAML(t, string(data))
		assert.ElementsMatch(t, mapKeys(first.Realtime.Species.Config), mapKeys(next.Realtime.Species.Config))
		assert.ElementsMatch(t, mapKeys(first.Realtime.SpeciesTracking.SeasonalTracking.Seasons),
			mapKeys(next.Realtime.SpeciesTracking.SeasonalTracking.Seasons))
		if prev != nil {
			assert.Equal(t, prev.Realtime.Species.Config, next.Realtime.Species.Config)
			assert.Equal(t, prev.Realtime.SpeciesTracking.SeasonalTracking.Seasons,
				next.Realtime.SpeciesTracking.SeasonalTracking.Seasons)
		}
		require.NoError(t, SaveYAMLConfig(nextPath, next))
		configPath, prev = nextPath, next
	}
}

// TestLoad_BadDottedStructMapEntryNamesTheKey pins that an invalid value under
// a dotted key fails Load and names the path and key, as it already does under
// a non-dotted key.
func TestLoad_BadDottedStructMapEntryNamesTheKey(t *testing.T) {
	tests := []struct {
		name, yaml, wantPath string
	}{
		{"species", `
realtime:
  species:
    config:
      "St. Helena Plover":
        threshold: not-a-number
`, "realtime.species.config[st. helena plover]"},
		{"season", `
realtime:
  speciestracking:
    seasonaltracking:
      seasons:
        early.spring:
          startmonth: not-a-number
`, "realtime.speciestracking.seasonaltracking.seasons[early.spring]"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prepareConfigFromYAML(t, structMapConfigPrefix+tt.yaml)

			_, err := Load()
			require.Error(t, err)
			assert.Contains(t, err.Error(), tt.wantPath)
		})
	}
}

// TestLoad_ScalarDottedSpeciesEntryIsIgnored pins that a dotted species key
// whose value is not a mapping is skipped, as Unmarshal already skipped it,
// instead of failing the load or creating an entry.
func TestLoad_ScalarDottedSpeciesEntryIsIgnored(t *testing.T) {
	settings, _ := loadConfigFromYAML(t, structMapConfigPrefix+`
realtime:
  species:
    config:
      "Larus sp.": 5
      "American Robin":
        threshold: 0.7
`)
	assert.NotContains(t, settings.Realtime.Species.Config, "larus sp.")
	assert.NotContains(t, settings.Realtime.Species.Config, "larus sp",
		"the zero-value entry viper's split leaves for the scalar must be dropped too")
	assert.Contains(t, settings.Realtime.Species.Config, "american robin")
}

// mapKeys returns the keys of m in unspecified order.
func mapKeys[V any](m map[string]V) []string {
	return slices.Collect(maps.Keys(m))
}
