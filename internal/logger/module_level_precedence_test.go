package logger

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestCentralLogger_ModuleLevelsBeatModuleOutputLevel pins the documented
// precedence: an explicit module_levels entry wins over the level of the
// module's own output, which in turn wins over default_level. The config layer
// supplies a non-empty output level through the viper defaults of every module
// that has them, so a module output level that overrode module_levels would
// make module_levels useless for those modules.
func TestCentralLogger_ModuleLevelsBeatModuleOutputLevel(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name         string
		moduleLevels map[string]string
		outputLevel  string
		wantDebug    bool
		wantInfo     bool
	}{
		{"module_levels debug over output info", map[string]string{"probe": "debug"}, "info", true, true},
		{"module_levels warn over output debug", map[string]string{"probe": "warn"}, "debug", false, false},
		{"no module_levels uses the output level", nil, "info", false, true},
		{"output level debug without module_levels", nil, "debug", true, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			path := filepath.Join(t.TempDir(), "probe.log")
			// Start from the disabled built-in outputs so the logger does not seed
			// and open the default log files relative to the working directory.
			outputs := DisabledModuleOutputs()
			outputs["probe"] = ModuleOutput{Enabled: true, FilePath: path, Level: tc.outputLevel}
			cl, err := NewCentralLogger(&LoggingConfig{
				Console:       &ConsoleOutput{Enabled: false},
				FileOutput:    &FileOutput{Enabled: false},
				DefaultLevel:  "error",
				ModuleLevels:  tc.moduleLevels,
				ModuleOutputs: outputs,
			})
			require.NoError(t, err)

			log := cl.Module("probe")
			log.Debug("debug-line")
			log.Info("info-line")
			require.NoError(t, cl.Flush())
			require.NoError(t, cl.Close())

			data, err := os.ReadFile(path)
			require.NoError(t, err)
			assert.Equal(t, tc.wantDebug, strings.Contains(string(data), "debug-line"), "debug line written")
			assert.Equal(t, tc.wantInfo, strings.Contains(string(data), "info-line"), "info line written")
		})
	}
}
