package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// dottedModuleConfigYAML is a config with per-module logging settings for a
// module whose name contains a dot ("analysis.processor"), plus one ordinary
// module as a control. Regression fixture for #4523: viper's key delimiter
// made the dotted module's settings silently disappear while the plain
// module's settings survived, and a dotted module_levels key made the whole
// config load fail outright.
const dottedModuleConfigYAML = `
logging:
  default_level: info
  console:
    enabled: false
  modules:
    analysis.processor:
      enabled: true
      file_path: logs/processor.log
      level: warn
      console_also: false
    audio:
      enabled: true
      file_path: logs/audio.log
      level: trace
      console_also: false
  module_levels:
    analysis.processor: warn
    audio: error
`

// TestLoad_DottedLoggingModuleNames is the end-to-end regression for #4523:
// a config file that carries per-module logging settings for a module name
// with a dot (e.g. "analysis.processor") must load, must not fabricate a
// phantom entry for the first path segment, and must apply the configured
// values instead of silently falling back to defaults. On unfixed main this
// test fails: Load returns an error because the dotted module_levels key
// makes viper.Unmarshal abort.
func TestLoad_DottedLoggingModuleNames(t *testing.T) {
	settings, _ := loadConfigFromYAML(t, dottedModuleConfigYAML)

	cfg := settings.Logging

	require.NotNil(t, cfg.ModuleOutputs, "ModuleOutputs must be unmarshaled at all")
	require.Contains(t, cfg.ModuleOutputs, "analysis.processor",
		"the dotted module name must survive as a single map key (#4523)")
	require.Contains(t, cfg.ModuleOutputs, "audio",
		"the plain module name must keep working (control)")
	assert.NotContains(t, cfg.ModuleOutputs, "analysis",
		"a dotted module name must not be split into a phantom 'analysis' entry")

	proc := cfg.ModuleOutputs["analysis.processor"]
	assert.True(t, proc.Enabled, "dotted module Enabled must come from the config, not defaults")
	assert.Equal(t, "logs/processor.log", proc.FilePath)
	assert.Equal(t, "warn", proc.Level, "dotted module Level must come from the config, not defaults")
	assert.False(t, proc.ConsoleAlso,
		"dotted module ConsoleAlso=false must survive; before the fix the default (true) silently won")

	audio := cfg.ModuleOutputs["audio"]
	assert.True(t, audio.Enabled)
	assert.Equal(t, "logs/audio.log", audio.FilePath)
	assert.Equal(t, "trace", audio.Level)

	require.NotNil(t, cfg.ModuleLevels)
	assert.Equal(t, "warn", cfg.ModuleLevels["analysis.processor"],
		"module_levels must keep dotted module names as single keys")
	assert.Equal(t, "error", cfg.ModuleLevels["audio"])

	// The rest of the config must keep decoding normally alongside the dotted keys.
	assert.Equal(t, "info", cfg.DefaultLevel)
	require.NotNil(t, cfg.Console)
	assert.False(t, cfg.Console.Enabled)
}

// TestLoad_DottedLoggingModuleNamesPreserveDefaults pins the merge contract
// of restoreDottedLoggingKeys: re-decoding from the raw tree must not drop
// the module entries the main pass produced from viper defaults (SaveSettings
// writes whatever Load produced, so losing defaults would rewrite a user's
// config file), and the phantom first-segment entry fabricated by the
// delimiter confusion must be gone.
func TestLoad_DottedLoggingModuleNamesPreserveDefaults(t *testing.T) {
	settings, _ := loadConfigFromYAML(t, dottedModuleConfigYAML)

	outs := settings.Logging.ModuleOutputs
	require.NotNil(t, outs)

	// The default module set must survive alongside the file-configured ones.
	for _, name := range []string{"audio", "datastore", "api", "auth", "security", "diskmanager"} {
		require.Contains(t, outs, name,
			"viper-default module entries must survive the raw-tree overlay")
	}

	// No phantom first-segment entry for the dotted module.
	assert.NotContains(t, outs, "analysis",
		"the phantom 'analysis' entry fabricated for 'analysis.processor' must be removed")
}
