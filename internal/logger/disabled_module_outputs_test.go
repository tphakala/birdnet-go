package logger

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestDisabledModuleOutputs_CoversEveryBuiltInModule guards against a module
// being added to defaultModuleOutputs without DisabledModuleOutputs turning it
// off, which would make tests quietly write log files again.
func TestDisabledModuleOutputs_CoversEveryBuiltInModule(t *testing.T) {
	t.Parallel()

	outputs := DisabledModuleOutputs()
	require.Len(t, outputs, len(defaultModuleOutputs))
	for module, out := range outputs {
		assert.False(t, out.Enabled, "module %s must be disabled", module)
	}
}

// TestNewCentralLogger_DisabledOutputsCreateNoFiles proves a logger built with
// file output off and DisabledModuleOutputs leaves the working directory empty,
// while the same config without it still seeds the built-in log files.
func TestNewCentralLogger_DisabledOutputsCreateNoFiles(t *testing.T) {
	// Not parallel: t.Chdir changes the process working directory.
	cases := []struct {
		name      string
		modules   map[string]ModuleOutput
		wantFiles bool
	}{
		{"disabled module outputs", DisabledModuleOutputs(), false},
		{"seeded module outputs", nil, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			t.Chdir(dir)

			cl, err := NewCentralLogger(&LoggingConfig{
				Console:       &ConsoleOutput{Enabled: false},
				FileOutput:    &FileOutput{Enabled: false},
				ModuleOutputs: tc.modules,
				DefaultLevel:  "debug",
			})
			require.NoError(t, err)
			t.Cleanup(func() { _ = cl.Close() })

			entries, err := os.ReadDir(dir)
			require.NoError(t, err)
			if tc.wantFiles {
				assert.NotEmpty(t, entries, "built-in module outputs are expected to be seeded")
			} else {
				assert.Empty(t, entries, "no log file or directory may be created")
			}
		})
	}
}
