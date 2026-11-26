package classifier

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
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
	// Cleanups run last in, first out: release the builder, then join the pass goroutines
	// before the seams and settings are restored.
	t.Cleanup(env.settle)
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

func TestGPUNotice_FailureRecordDoesNotSurviveAnExplicitRoundTrip(t *testing.T) {
	a := &recoveryBuilder{id: testSecondaryID, err: errors.NewStd("build failed")}
	env := newGPURecoveryEnv(t, "auto", "auto", map[string]*recoveryBuilder{testSecondaryID: a}, []string{testSecondaryID})
	env.faulted.Store(true)
	env.o.onOpenVINOGPUFault()
	env.settle()
	require.Equal(t, notification.MsgOpenVINOGPUMovedPartialMessage, gpuNoticesNow(t, env.svc)[0].MessageKey)

	setSelection := func(device string) {
		s := conftest.GetTestSettings()
		s.BirdNET.Backend, s.BirdNET.OpenVINODevice = "auto", device
		conftest.SetTestSettings(s)
	}
	setSelection("gpu")
	env.o.ReconcileGPUFaultAfterSettingsReload()
	env.settle()
	setSelection("auto")

	// A sync between the settings swap and the reconcile: the model was never tried
	// under this selection, so it must not be named as one that could not be moved.
	env.o.syncInferenceHealth()
	assert.Empty(t, gpuNoticesNow(t, env.svc))
}

func TestGPUNotice_StoppedRecoveryKeepsFailedMoves(t *testing.T) {
	env := newGPURecoveryEnv(t, "auto", "auto", nil, []string{testSecondaryID})
	env.faulted.Store(true)
	env.o.setMoveFailed(testSecondaryID, true)
	env.o.syncInferenceHealth()
	before := gpuNoticesNow(t, env.svc)
	require.Len(t, before, 1)
	require.Equal(t, notification.MsgOpenVINOGPUMovedPartialMessage, before[0].MessageKey)

	// Delete stops recovery before it clears the models; a settings reload in that
	// window requests a pass that never runs, so the record the notice shows stays.
	env.o.stopGPURecovery()
	env.o.ReconcileGPUFaultAfterSettingsReload()
	env.settle()

	assert.True(t, env.o.moveFailed(testSecondaryID), "no pass runs once recovery is stopped")
	after := gpuNoticesNow(t, env.svc)
	require.Len(t, after, 1)
	assert.Equal(t, before[0].ID, after[0].ID, "the raised partial notice stays")
}
