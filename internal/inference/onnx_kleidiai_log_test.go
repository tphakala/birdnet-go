package inference

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestLogKleidiAIWorkaround_LogsOnlyWhenWorkaroundApplies pins the Info line
// that tells support the KleidiAI kernels are off: it names the ONNX Runtime
// version for a release with the leak on arm64, and stays silent for a release
// without it and off arm64. Not parallel: it replaces the global logger.
func TestLogKleidiAIWorkaround_LogsOnlyWhenWorkaroundApplies(t *testing.T) {
	buf := logtest.CaptureBufferAt(t, slog.LevelInfo)

	logKleidiAIWorkaround("1.25.1", "arm64")
	out := buf.String()
	assert.Contains(t, out, "level=INFO")
	assert.Contains(t, out, "KleidiAI")
	assert.Contains(t, out, "ort_version=1.25.1")
	assert.Contains(t, out, "fixed_in=1.27")

	buf.Reset()
	logKleidiAIWorkaround("1.31.0", "arm64")
	assert.Empty(t, buf.String(), "a release with the fix must not log the workaround line")

	buf.Reset()
	logKleidiAIWorkaround("1.25.1", "amd64")
	assert.Empty(t, buf.String(), "off arm64 the option is a no-op, so nothing is logged")
}
