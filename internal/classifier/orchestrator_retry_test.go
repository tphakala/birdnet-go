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

	enable := func(s *conf.Settings, ids ...string) {
		s.Models.Enabled = ids
	}
	withBackend := func(backend string, ids ...string) {
		s := conftest.GetTestSettings()
		s.BirdNET.Backend = backend
		enable(s, ids...)
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
