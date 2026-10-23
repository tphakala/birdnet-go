package inference

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestORTRuntimeSnapshot_TracksInit pins that the snapshot is stored after a
// successful init with the library handed to the binding. It does not destroy
// the runtime afterwards: the official binding cannot initialize again after a
// shutdown, which would break every later test in the process. It needs a real
// ONNX Runtime library, so it is opt-in through OV_TEST_ORT_LIB like the other
// library-backed tests.
func TestORTRuntimeSnapshot_TracksInit(t *testing.T) {
	lib := os.Getenv("OV_TEST_ORT_LIB")
	if lib == "" {
		t.Skip("set OV_TEST_ORT_LIB to a libonnxruntime path to run")
	}
	require.Equal(t, ORTRuntime{}, ORTRuntimeSnapshot(), "another test left the runtime initialized")

	require.NoError(t, InitONNXRuntime(lib))
	snap := ORTRuntimeSnapshot()
	assert.True(t, snap.Initialized)
	assert.NotEmpty(t, snap.Version)
	assert.Equal(t, lib, snap.LibraryPath)
	assert.True(t, snap.LibraryConfigured)
}

// TestProbeStatusName pins the mapping from the probe cache state to the status
// the support dump reports, in the default build where no probe can run.
func TestProbeStatusName(t *testing.T) {
	t.Parallel()
	assert.Equal(t, OVProbeStatusNotRun, probeStatusName(ovProbeNotRun))
	assert.Equal(t, OVProbeStatusInFlight, probeStatusName(ovProbeInFlight))
	assert.Equal(t, OVProbeStatusCached, probeStatusName(ovProbeCached))
}
