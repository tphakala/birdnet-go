package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// overrideV24Loader forces the BirdNET v2.4 loader to fail with the given error for the
// duration of the test, restoring the original loader on cleanup. It lets a test drive
// the N=0-after-failure path deterministically on any build (the embedded model would
// otherwise load on the default build).
func overrideV24Loader(t *testing.T, loadErr error) {
	t.Helper()
	orig, had := modelLoaders[RegistryIDBirdNETV24]
	modelLoaders[RegistryIDBirdNETV24] = func(_ *Orchestrator, _ int) error { return loadErr }
	t.Cleanup(func() {
		if had {
			modelLoaders[RegistryIDBirdNETV24] = orig
		} else {
			delete(modelLoaders, RegistryIDBirdNETV24)
		}
	})
}

// TestNewOrchestrator_V24LoadFailureIsNotFatal pins that a BirdNET v2.4 load failure no
// longer aborts construction (model de-privilege epic, Phase 4). Before B1 the loader
// error was fatal and NewOrchestrator returned it; now models.enabled is authoritative
// and N=0 is a supported runtime state, so construction succeeds degraded: v2.4 is not
// loaded, the failure is on record, and the range filter is present but fails open.
func TestNewOrchestrator_V24LoadFailureIsNotFatal(t *testing.T) {
	// Not parallel: overrides the global v2.4 loader and publishes the global settings snapshot.
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	enableBirdNETV24(settings) // v2.4 is enabled, but its loader is forced to fail below
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	loaderErr := errors.NewStd("simulated v2.4 load failure")
	overrideV24Loader(t, loaderErr)

	o, err := NewOrchestrator(settings)
	require.NoError(t, err, "a v2.4 load failure must not be fatal; construction degrades to N=0")
	t.Cleanup(func() { o.Delete() })

	assert.False(t, o.IsModelLoaded(RegistryIDBirdNETV24), "v2.4 did not load")
	assert.Empty(t, o.ModelInfos(), "nothing loaded after the only enabled model failed")

	// The failure is a live fault (v2.4 is enabled), not a fresh-install state.
	assert.Equal(t, AcousticModelsLoadFailed, o.AcousticModelsState())
	assert.Equal(t, loaderErr.Error(), o.LoadErrors()[RegistryIDBirdNETV24])

	// PR A range-filter contract at N=0: the service is present but fails open (no backend,
	// no participants) and BuildRangeFilter is a no-op returning nil, so BirdNETAnalyzer.Start
	// does not treat N=0 as a fatal startup error.
	require.NotNil(t, o.rangeFilter, "the range-filter service is always constructed")
	status := o.RangeFilterStatus()
	assert.False(t, status.ParticipantsLoaded, "no participating classifier at N=0")
	assert.Equal(t, string(rfKindNone), status.Backend, "no backend at N=0")
	assert.NotNil(t, status.Classifiers, "the classifier list is an empty array, never null")
	assert.Empty(t, status.Classifiers)
	require.NoError(t, BuildRangeFilter(o), "BuildRangeFilter is a no-op at N=0")
}

// TestNewOrchestrator_ZeroModels builds a real orchestrator with an empty models.enabled and
// asserts it starts and stays at N=0: no models, no default targets, an empty range filter,
// working on-demand name resolution, and a clean shutdown. This is the primary evidence that
// N=0 is a supported runtime state now that models.enabled is authoritative (model
// de-privilege epic, Phase 4). It runs on every build (empty enabled means N=0 with no
// implicit v2.4), so it does not use requireV24Loaded.
func TestNewOrchestrator_ZeroModels(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // explicit N=0
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o, err := NewOrchestrator(settings)
	require.NoError(t, err, "N=0 is a valid state; construction must succeed")
	t.Cleanup(func() { o.Delete() })

	assert.Empty(t, o.ModelInfos(), "no models are loaded")
	assert.Nil(t, o.DefaultTargets(), "no default target at N=0")
	assert.Empty(t, o.AllLabels(), "no labels at N=0")
	assert.Equal(t, AcousticModelsNoneInstalled, o.AcousticModelsState())

	rf := o.RangeFilterStatus()
	assert.False(t, rf.Active, "no backend loaded at N=0")
	assert.False(t, rf.ParticipantsLoaded, "no participating classifier at N=0")
	assert.Equal(t, string(rfKindNone), rf.Backend)
	assert.NotNil(t, rf.Classifiers, "the classifier list is an empty array, never null")
	assert.Empty(t, rf.Classifiers)

	require.NoError(t, BuildRangeFilter(o), "BuildRangeFilter is a no-op at N=0")

	// Name resolution never panics at N=0 (the resolver chain is just the OpenFauna resolver,
	// with no v2.4 label resolver appended).
	assert.NotPanics(t, func() { _ = o.ResolveName("Turdus merula", "en-uk") })
}

// TestAcousticModelsState covers the coarse "is anything loaded" verdict that drives the
// acoustic_models health check and GET /api/v2/system/inference (model de-privilege epic,
// Phase 4). Nothing loaded is none_installed unless an ENABLED model has a recorded load
// error, in which case it is load_failed; a disabled model's stale error is not a fault.
func TestAcousticModelsState(t *testing.T) {
	errBoom := errors.NewStd("simulated load failure")

	t.Run("no models and no errors is none_installed", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t)
		assert.Equal(t, AcousticModelsNoneInstalled, o.AcousticModelsState())
	})

	t.Run("a loaded model is ok", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
		assert.Equal(t, AcousticModelsOK, o.AcousticModelsState())
	})

	t.Run("a model loaded after a failure recovers to ok", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2})
		o.recordLoadFailure(RegistryIDPerchV2, errBoom)
		// A model is loaded, so the loaded set wins regardless of any stored error.
		assert.Equal(t, AcousticModelsOK, o.AcousticModelsState())
	})

	t.Run("a deleted orchestrator is none_installed", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDBirdNETV24, errBoom)
		o.models = nil // mimic Delete clearing the map
		assert.Equal(t, AcousticModelsNoneInstalled, o.AcousticModelsState())
	})

	// The two settings-dependent cases publish the global snapshot and must not run in
	// parallel with each other.
	t.Run("no models with an enabled model's recorded error is load_failed", func(t *testing.T) {
		settings := conftest.GetTestSettings()
		settings.Models.Enabled = []string{conf.ModelIDBirdNET}
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDBirdNETV24, errBoom)
		assert.Equal(t, AcousticModelsLoadFailed, o.AcousticModelsState())
	})

	t.Run("ignores errors of disabled models", func(t *testing.T) {
		settings := conftest.GetTestSettings()
		settings.Models.Enabled = []string{conf.ModelIDBirdNET} // Perch is NOT enabled
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDPerchV2, errBoom) // error for a model that is not enabled
		assert.Equal(t, AcousticModelsNoneInstalled, o.AcousticModelsState(),
			"a load error for a disabled model is not a live fault")
	})
}

// TestLoadErrors covers the enabled-filtered live-error snapshot that feeds the health
// check and inference status, distinct from the cumulative LoadFailures counter.
func TestLoadErrors(t *testing.T) {
	errBoom := errors.NewStd("simulated load failure")

	t.Run("reports the last error for an enabled failed model", func(t *testing.T) {
		settings := conftest.GetTestSettings()
		settings.Models.Enabled = []string{conf.ModelIDBirdNET}
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDBirdNETV24, errBoom)
		assert.Equal(t, errBoom.Error(), o.LoadErrors()[RegistryIDBirdNETV24])
	})

	t.Run("omits errors of disabled models", func(t *testing.T) {
		settings := conftest.GetTestSettings()
		settings.Models.Enabled = []string{conf.ModelIDBirdNET} // Perch is NOT enabled
		conftest.SetTestSettings(settings)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })

		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDPerchV2, errBoom)
		assert.NotContains(t, o.LoadErrors(), RegistryIDPerchV2,
			"a disabled model's error is filtered out of the live snapshot")
	})
}
