package conf

import (
	"strings"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// unmarshalLikeLoad decodes dottedModuleConfigYAML exactly the way
// storage.Load does: the tolerant decode hook, then the raw-tree overlay.
func unmarshalLikeLoad(t *testing.T) Settings {
	t.Helper()

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(dottedModuleConfigYAML)))

	var settings Settings
	require.NoError(t, v.Unmarshal(&settings, viper.DecodeHook(tolerantDecodeHook())),
		"a dotted module_levels key must not abort the unmarshal (#4523)")
	restoreDottedLoggingKeys(v, &settings)
	return settings
}

// TestViperUnmarshalDottedLoggingModuleNames pins the two underlying
// primitives at the viper layer: the tolerant hook keeps the unmarshal from
// failing on a dotted module_levels key, and restoreDottedLoggingKeys
// restores both logging maps with dotted names intact.
func TestViperUnmarshalDottedLoggingModuleNames(t *testing.T) {
	t.Parallel()

	cfg := unmarshalLikeLoad(t).Logging

	require.NotNil(t, cfg.ModuleOutputs)
	require.Contains(t, cfg.ModuleOutputs, "analysis.processor")
	assert.NotContains(t, cfg.ModuleOutputs, "analysis",
		"the dotted name must not be split into a phantom 'analysis' module")

	proc := cfg.ModuleOutputs["analysis.processor"]
	assert.True(t, proc.Enabled)
	assert.Equal(t, "logs/processor.log", proc.FilePath)
	assert.Equal(t, "warn", proc.Level)
	assert.False(t, proc.ConsoleAlso)

	require.NotNil(t, cfg.ModuleLevels)
	assert.Equal(t, "warn", cfg.ModuleLevels["analysis.processor"])
	assert.Equal(t, "error", cfg.ModuleLevels["audio"])
}

// TestViperUnmarshalDottedModuleLevelsNoLongerFails isolates the hard-fail
// half of #4523: on main, `module_levels: {analysis.processor: warn}` made the
// whole viper.Unmarshal call return an error, so a hand-edited config stopped
// the app from booting. The tolerant hook must let the unmarshal succeed.
func TestViperUnmarshalDottedModuleLevelsNoLongerFails(t *testing.T) {
	t.Parallel()

	v := viper.New()
	v.SetConfigType("yaml")
	require.NoError(t, v.ReadConfig(strings.NewReader(dottedModuleConfigYAML)))

	var settings Settings
	require.NoError(t, v.Unmarshal(&settings, viper.DecodeHook(tolerantDecodeHook())),
		"dotted module_levels keys must not make the config unmarshal fail")
}
