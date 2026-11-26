package classifier

import (
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// stubModelLoader replaces the loader for registryID for the test and restores it
// on cleanup. The stub counts calls and either registers a mock instance or fails.
func stubModelLoader(t *testing.T, registryID string, calls *atomic.Int32, fail *atomic.Bool) {
	t.Helper()
	orig, had := modelLoaders[registryID]
	modelLoaders[registryID] = func(o *Orchestrator, _ int) error {
		calls.Add(1)
		if fail.Load() {
			return errors.NewStd("simulated load failure")
		}
		o.models[registryID] = &modelEntry{instance: &mockModelInstance{id: registryID}}
		return nil
	}
	t.Cleanup(func() {
		if had {
			modelLoaders[registryID] = orig
		} else {
			delete(modelLoaders, registryID)
		}
	})
}

// TestRetryFailedLoadsForBackendChange pins that a model whose load failed is
// retried when the inference settings differ from the ones it failed under, and
// only then.
func TestRetryFailedLoadsForBackendChange(t *testing.T) {
	// Not parallel: swaps modelLoaders and the global settings snapshot.
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{}
	settings.BirdNET.Backend = conf.BackendPrefAuto
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(o.Delete)

	var perchCalls, v3Calls atomic.Int32
	var perchFail, v3Fail atomic.Bool
	stubModelLoader(t, RegistryIDPerchV2, &perchCalls, &perchFail)
	stubModelLoader(t, RegistryIDBirdNETPlusV3, &v3Calls, &v3Fail)

	withBackend := func(backend string, ids ...string) {
		s := conftest.GetTestSettings()
		s.BirdNET.Backend = backend
		s.Models.Enabled = ids
		conftest.SetTestSettings(s)
	}

	// Perch fails under backend auto, BirdNET+ v3.0 under onnx.
	withBackend(conf.BackendPrefAuto, conf.ModelIDPerchV2, conf.ModelIDBirdNETPlusV3)
	o.recordLoadFailure(RegistryIDPerchV2, errors.NewStd("boom"))
	withBackend(conf.BackendPrefONNX, conf.ModelIDPerchV2, conf.ModelIDBirdNETPlusV3)
	o.recordLoadFailure(RegistryIDBirdNETPlusV3, errors.NewStd("boom"))

	loaded := o.RetryFailedLoadsForBackendChange()
	assert.Equal(t, []string{RegistryIDPerchV2}, loaded, "only the model that failed under other settings is retried")
	assert.True(t, o.IsModelLoaded(RegistryIDPerchV2))
	assert.False(t, o.IsModelLoaded(RegistryIDBirdNETPlusV3))
	assert.EqualValues(t, 1, perchCalls.Load())
	assert.Zero(t, v3Calls.Load())
	assert.NotContains(t, o.LoadErrors(), RegistryIDPerchV2, "a successful retry clears the record")

	assert.Empty(t, o.RetryFailedLoadsForBackendChange(), "a second retry under unchanged settings does nothing")
	assert.Zero(t, v3Calls.Load())

	// A disabled model is not retried even when its key differs.
	withBackend(conf.BackendPrefOpenVINO, conf.ModelIDPerchV2)
	assert.Empty(t, o.RetryFailedLoadsForBackendChange())
	assert.Zero(t, v3Calls.Load(), "a disabled model is not retried")

	// A retry that fails again records the new key, so the next retry does nothing.
	v3Fail.Store(true)
	withBackend(conf.BackendPrefOpenVINO, conf.ModelIDPerchV2, conf.ModelIDBirdNETPlusV3)
	assert.Empty(t, o.RetryFailedLoadsForBackendChange())
	assert.EqualValues(t, 1, v3Calls.Load())
	assert.Empty(t, o.RetryFailedLoadsForBackendChange())
	assert.EqualValues(t, 1, v3Calls.Load(), "the failed retry recorded the current key")
}

// TestRecordLoadFailure_KeyIsTakenBeforeTheLoad pins that a failure is stamped with the
// settings the load started under, not the ones current when it failed. A settings change
// that lands while a load is still running must leave that failure retryable.
func TestRecordLoadFailure_KeyIsTakenBeforeTheLoad(t *testing.T) {
	// Not parallel: swaps modelLoaders and the global settings snapshot.
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{conf.ModelIDPerchV2}
	settings.BirdNET.Backend = conf.BackendPrefAuto
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(o.Delete)

	var calls atomic.Int32
	orig, had := modelLoaders[RegistryIDPerchV2]
	modelLoaders[RegistryIDPerchV2] = func(orc *Orchestrator, _ int) error {
		if calls.Add(1) == 1 {
			// The user saves a new backend while this load is still running, then it fails.
			changed := conftest.GetTestSettings()
			changed.Models.Enabled = []string{conf.ModelIDPerchV2}
			changed.BirdNET.Backend = conf.BackendPrefONNX
			conftest.SetTestSettings(changed)
			return errors.NewStd("simulated load failure")
		}
		orc.models[RegistryIDPerchV2] = &modelEntry{instance: &mockModelInstance{id: RegistryIDPerchV2}}
		return nil
	}
	t.Cleanup(func() {
		if had {
			modelLoaders[RegistryIDPerchV2] = orig
		} else {
			delete(modelLoaders, RegistryIDPerchV2)
		}
	})

	require.Error(t, o.LoadModel(RegistryIDPerchV2))
	require.EqualValues(t, 1, calls.Load())

	assert.Equal(t, []string{RegistryIDPerchV2}, o.RetryFailedLoadsForBackendChange(),
		"the load never ran under the new backend, so the settings change must retry it")
}
