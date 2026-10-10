package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// overrideModelLoader replaces the loader for registryID for the duration of the test,
// restoring the original on cleanup. It lets a reconcile test drive load/unload without real
// model files (generalizes overrideV24Loader to any registry ID).
func overrideModelLoader(t *testing.T, registryID string, fn func(*Orchestrator, int) error) {
	t.Helper()
	orig, had := modelLoaders[registryID]
	modelLoaders[registryID] = fn
	t.Cleanup(func() {
		if had {
			modelLoaders[registryID] = orig
		} else {
			delete(modelLoaders, registryID)
		}
	})
}

// TestReconcileEnabledModels drives ReconcileEnabledModels through load, no-op and unload:
// enabling a model loads it, a re-run with the same set is a no-op, and disabling it unloads
// it. Uses a real N=0 orchestrator with the Perch loader overridden to register a mock.
func TestReconcileEnabledModels(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot and the test
	// overrides the global modelLoaders map.
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // start at N=0
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	perchLabels := []string{"Cyanistes caeruleus_Eurasian Blue Tit"}
	overrideModelLoader(t, RegistryIDPerchV2, func(o *Orchestrator, _ int) error {
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &mockModelInstance{id: RegistryIDPerchV2, labels: perchLabels, numSpecies: len(perchLabels)}}
		return nil
	})

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(func() { o.Delete() })

	// No diff at N=0: nothing to do.
	loaded, unloaded, rerr := o.ReconcileEnabledModels()
	require.NoError(t, rerr)
	assert.Empty(t, loaded)
	assert.Empty(t, unloaded)

	// Enable Perch -> it loads.
	settings.Models.Enabled = []string{"perch_v2"}
	conftest.SetTestSettings(settings)
	loaded, unloaded, rerr = o.ReconcileEnabledModels()
	require.NoError(t, rerr)
	assert.Equal(t, []string{RegistryIDPerchV2}, loaded)
	assert.Empty(t, unloaded)
	assert.True(t, o.IsModelLoaded(RegistryIDPerchV2))

	// Re-run with the same enabled set: idempotent no-op.
	loaded, unloaded, rerr = o.ReconcileEnabledModels()
	require.NoError(t, rerr)
	assert.Empty(t, loaded)
	assert.Empty(t, unloaded)

	// Disable Perch -> it unloads.
	settings.Models.Enabled = []string{}
	conftest.SetTestSettings(settings)
	loaded, unloaded, rerr = o.ReconcileEnabledModels()
	require.NoError(t, rerr)
	assert.Empty(t, loaded)
	assert.Equal(t, []string{RegistryIDPerchV2}, unloaded)
	assert.False(t, o.IsModelLoaded(RegistryIDPerchV2))
}

// TestReconcileEnabledModels_FailingLoaderRecordsError verifies a loader failure is surfaced
// and recorded (so AcousticModelsState/LoadErrors reflect it) rather than panicking or
// silently succeeding.
func TestReconcileEnabledModels_FailingLoaderRecordsError(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{"perch_v2"}
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	loaderErr := errors.NewStd("simulated perch load failure")
	overrideModelLoader(t, RegistryIDPerchV2, func(_ *Orchestrator, _ int) error { return loaderErr })

	o, err := NewOrchestrator(settings) // perch enabled, but its loader fails at construction
	require.NoError(t, err)
	t.Cleanup(func() { o.Delete() })
	require.False(t, o.IsModelLoaded(RegistryIDPerchV2))

	loaded, _, rerr := o.ReconcileEnabledModels()
	require.Error(t, rerr, "a failing loader must surface an error")
	assert.Empty(t, loaded)
	assert.Contains(t, o.LoadErrors(), RegistryIDPerchV2, "the load failure is recorded")
}
