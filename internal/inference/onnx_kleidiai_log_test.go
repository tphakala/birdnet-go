package inference

import (
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestLogKleidiAIWorkaround_LogsOnlyWhenWorkaroundApplies pins the Info line
// that tells support the KleidiAI kernels are off: it names the ONNX Runtime
// version for a release with the leak and stays silent for one without it.
// Not parallel: it replaces the global logger.
func TestLogKleidiAIWorkaround_LogsOnlyWhenWorkaroundApplies(t *testing.T) {
	buf := logtest.CaptureBufferAt(t, slog.LevelInfo)

	logKleidiAIWorkaround("1.25.1")
	out := buf.String()
	assert.Contains(t, out, "level=INFO")
	assert.Contains(t, out, "KleidiAI")
	assert.Contains(t, out, "ort_version=1.25.1")
	assert.Contains(t, out, "fixed_in=1.27")

	buf.Reset()
	logKleidiAIWorkaround("1.31.0")
	assert.Empty(t, buf.String(), "a release with the fix must not log the workaround line")
}
