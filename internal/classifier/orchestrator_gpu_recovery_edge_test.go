package classifier

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// releaseOnce unblocks a blocking test builder; safe to call twice.
func releaseOnce(b *recoveryBuilder) {
	b.releaseOnce.Do(func() { close(b.release) })
}

func TestSetGPURecoveryCallback_NoReplayAfterDelete(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.o.SetGPURecoveryCallback(nil)
	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	env.o.Delete()

	var calls atomic.Int32
	env.o.SetGPURecoveryCallback(func() { calls.Add(1) })
	assert.Equal(t, int32(0), calls.Load(), "a deleted orchestrator replays no move")
}

func TestGPURecovery_StopsWhenSelectionBecomesExplicitMidPass(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID, entered: make(chan struct{}), release: make(chan struct{})}
	b := &recoveryBuilder{id: gpuFaultHealthySecondaryID}
	env := newGPURecoveryEnv(t, "auto", "auto",
		map[string]*recoveryBuilder{testSecondaryID: a, gpuFaultHealthySecondaryID: b},
		[]string{testSecondaryID, gpuFaultHealthySecondaryID})
	t.Cleanup(func() { releaseOnce(a) })
	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	select {
	case <-a.entered:
	case <-time.After(gpuRecoveryWait):
		require.FailNow(t, "the recovery pass did not reach the builder")
	}

	s := conftest.GetTestSettings()
	s.BirdNET.Backend, s.BirdNET.OpenVINODevice = "auto", "gpu"
	conftest.SetTestSettings(s)
	releaseOnce(a)
	env.settle()

	assert.Equal(t, int32(0), b.builds.Load(), "an explicit selection never rebuilds off the GPU, even mid-pass")
}

func TestRebuildFromCurrentSettings_RecordsTripletAndNeedsSettings(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.faulted.Store(true)

	swapped, err := env.o.rebuildFromCurrentSettings(testSecondaryID, true)
	require.NoError(t, err)
	require.True(t, swapped)
	assert.Equal(t, secondaryTripletFor(env.o.currentSettings()), env.o.models[testSecondaryID].backend,
		"the entry records the settings its rebuilt instance was built from")

	bare := newTestOrchestrator(t)
	bare.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}}
	conftest.SetTestSettings(nil)
	bare.settingsAtomic.Store(nil)
	assert.NotPanics(t, func() {
		_, err := bare.rebuildFromCurrentSettings(testSecondaryID, false)
		assert.Error(t, err, "no settings, nothing to rebuild from")
	})
}
