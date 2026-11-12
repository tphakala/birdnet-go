package noisebank

import (
	"bufio"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/logger"
)

// TestService_LogsThroughTheNoiseBankModule pins that the service writes through
// the same log module as the processor's gate decisions, so the whole trail of the
// feature is read in one file.
func TestService_LogsThroughTheNoiseBankModule(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "noisebank.log")
	outputs := logger.DisabledModuleOutputs()
	outputs[LogModule] = logger.ModuleOutput{Enabled: true, FilePath: logPath}
	cl, err := logger.NewCentralLogger(&logger.LoggingConfig{
		DefaultLevel:  "info",
		Console:       &logger.ConsoleOutput{Enabled: false},
		FileOutput:    &logger.FileOutput{Enabled: false},
		ModuleOutputs: outputs,
	})
	require.NoError(t, err)
	prev := logger.Global()
	logger.SetGlobal(cl)
	t.Cleanup(func() {
		logger.SetGlobal(prev)
		_ = cl.Close()
	})

	newTestEnv(t)
	require.NoError(t, cl.Flush())

	f, err := os.Open(filepath.Clean(logPath))
	require.NoError(t, err)
	defer func() { _ = f.Close() }()
	var found bool
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "noise examples loaded") {
			found = true
		}
	}
	require.NoError(t, sc.Err())
	assert.True(t, found, "the service's lines land in the module file")
}
