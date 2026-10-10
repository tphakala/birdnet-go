package inference

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestLogOVProbeFailure_RecordsPathOnce pins the once-per-path WARN guard: the
// first failure for a library path marks it, later failures and other paths do
// not disturb that record.
func TestLogOVProbeFailure_RecordsPathOnce(t *testing.T) {
	// Not parallel: touches the process-wide ovProbeFailureWarned map.
	const pathA = "/test/libopenvino_c_a.so"
	const pathB = "/test/libopenvino_c_b.so"
	t.Cleanup(func() {
		ovProbeFailureWarned.Delete(pathA)
		ovProbeFailureWarned.Delete(pathB)
	})
	probeErr := errors.NewStd("openvino probe: child timed out")

	_, seen := ovProbeFailureWarned.Load(pathA)
	assert.False(t, seen)

	logOVProbeFailure(pathA, probeErr)
	_, seen = ovProbeFailureWarned.Load(pathA)
	assert.True(t, seen, "the first failure for a path is recorded (logged at WARN)")

	logOVProbeFailure(pathA, probeErr)
	_, seen = ovProbeFailureWarned.Load(pathB)
	assert.False(t, seen, "another path has its own record")

	logOVProbeFailure(pathB, probeErr)
	_, seen = ovProbeFailureWarned.Load(pathB)
	assert.True(t, seen)
}
