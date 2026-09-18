package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/notification"
)

// TestAcousticModelsState covers the coarse "is anything loaded" verdict that drives the
// acoustic_models health check and GET /api/v2/system/inference (model de-privilege epic,
// Phase 4).
func TestAcousticModelsState(t *testing.T) {
	t.Parallel()

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

	t.Run("no models with a recorded load error is load_failed", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDBirdNETV24, errBoom)
		assert.Equal(t, AcousticModelsLoadFailed, o.AcousticModelsState())
	})

	t.Run("a model loaded after a failure recovers to ok", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2})
		o.recordLoadFailure(RegistryIDPerchV2, errBoom)
		// The error is on record, but a model is loaded, so the loaded set wins.
		assert.Equal(t, AcousticModelsOK, o.AcousticModelsState())
	})

	t.Run("a deleted orchestrator is none_installed", func(t *testing.T) {
		t.Parallel()
		o := newTestOrchestrator(t)
		o.recordLoadFailure(RegistryIDBirdNETV24, errBoom)
		o.models = nil // mimic Delete clearing the map
		assert.Equal(t, AcousticModelsNoneInstalled, o.AcousticModelsState())
	})
}

// TestBuildRangeFilter_NoAnchorIsNoop pins that BuildRangeFilter is a no-op returning nil
// (not an error) when the v2.4 anchor is not loaded, so BirdNETAnalyzer.Start does not
// treat N = 0 as a fatal startup error (model de-privilege epic, Phase 4).
func TestBuildRangeFilter_NoAnchorIsNoop(t *testing.T) {
	isolateTestConfig(t)
	// No v2.4 anchor is loaded, so rangeFilterReady returns !ok and BuildRangeFilter
	// returns before touching o.rangeFilter; no service needs to be wired.
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2})

	require.NoError(t, BuildRangeFilter(o), "no anchor loaded must be a no-op, not an error")
}

// TestRangeFilterStatus_NoAnchor pins the honest no-anchor status: an empty (never null)
// classifier list, CoverageApplicable false, Active false, and the configured settings
// reflected back (model de-privilege epic, Phase 4).
func TestRangeFilterStatus_NoAnchor(t *testing.T) {
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.Threshold = 0.02
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2})

	resp := o.RangeFilterStatus()
	assert.NotNil(t, resp.Classifiers, "classifiers must be an empty array, never null")
	assert.Empty(t, resp.Classifiers)
	assert.False(t, resp.CoverageApplicable)
	assert.False(t, resp.Active)
	assert.True(t, resp.LocationConfigured)
	assert.InDelta(t, 0.02, resp.Threshold, 1e-6)
}

// TestNewOrchestrator_ZeroModels builds a real orchestrator with an empty models.enabled
// and asserts it starts and stays at N = 0: no models, no default targets, an empty range
// filter, working on-demand name resolution, and a clean shutdown. This is the primary
// evidence that N = 0 is a supported runtime state (model de-privilege epic, Phase 4).
func TestNewOrchestrator_ZeroModels(t *testing.T) {
	// Not parallel: NewOrchestrator publishes into the global settings snapshot.
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{} // explicit N = 0

	o, err := NewOrchestrator(settings)
	require.NoError(t, err, "N = 0 is a valid state; construction must succeed")
	t.Cleanup(func() { o.Delete() })

	assert.Empty(t, o.ModelInfos(), "no models are loaded")
	assert.Nil(t, o.DefaultTargets(), "no default target at N = 0")
	assert.Empty(t, o.AllLabels(), "no labels at N = 0")
	assert.Equal(t, AcousticModelsNoneInstalled, o.AcousticModelsState())

	rf := o.RangeFilterStatus()
	assert.False(t, rf.Active)
	assert.False(t, rf.CoverageApplicable)
	assert.NotNil(t, rf.Classifiers)
	assert.Empty(t, rf.Classifiers)

	require.NoError(t, BuildRangeFilter(o), "BuildRangeFilter is a no-op at N = 0")

	// Name resolution never panics at N = 0 (the resolver chain is just the OpenFauna
	// resolver, with no v2.4 label resolver appended).
	assert.NotPanics(t, func() { _ = o.ResolveName("Turdus merula", "en-uk") })
}

// TestNewOrchestrator_V24LoadFailureIsNotFatal pins that a BirdNET v2.4 load failure is a
// recorded failure and a warning, not a fatal construction error: the orchestrator starts
// at N = 0 in the load_failed state (model de-privilege epic, Phase 4).
func TestNewOrchestrator_V24LoadFailureIsNotFatal(t *testing.T) {
	// Not parallel: overrides the global modelLoaders map and the settings snapshot.
	orig := modelLoaders[RegistryIDBirdNETV24]
	t.Cleanup(func() { modelLoaders[RegistryIDBirdNETV24] = orig })
	modelLoaders[RegistryIDBirdNETV24] = func(_ *Orchestrator, _ int) error {
		return errors.NewStd("simulated v2.4 load failure")
	}

	settings := conftest.GetTestSettings()
	enableBirdNETV24(settings)

	o, err := NewOrchestrator(settings)
	require.NoError(t, err, "a v2.4 load failure is no longer fatal")
	t.Cleanup(func() { o.Delete() })

	assert.False(t, o.IsModelLoaded(RegistryIDBirdNETV24), "v2.4 did not load")
	assert.Equal(t, AcousticModelsLoadFailed, o.AcousticModelsState())
	assert.Positive(t, o.LoadFailures()[RegistryIDBirdNETV24], "the failure is recorded, not swallowed")
	assert.Contains(t, o.LoadErrors(), RegistryIDBirdNETV24, "the last error text is on record")
}

// TestSyncAcousticModelsNotice covers the persistent "no acoustic model" bell latch:
// one notification raised at N = 0, no double-raise, cleared once a model loads (model
// de-privilege epic, Phase 4).
func TestSyncAcousticModelsNotice(t *testing.T) {
	// Not parallel: uses the process-global notification service.
	svc := setupTestNotification(t)
	list := func() []*notification.Notification {
		notes, err := svc.List(nil)
		require.NoError(t, err)
		return notes
	}

	o := newTestOrchestrator(t) // no models -> none_installed
	o.syncAcousticModelsNotice()
	notes := list()
	require.Len(t, notes, 1, "N = 0 raises exactly one bell notification")
	assert.Equal(t, "classifier", notes[0].Component)
	assert.Equal(t, "none_installed", notes[0].Metadata["acoustic_models_state"])
	assert.Contains(t, notes[0].Message, "install one")
	require.NotEmpty(t, o.acousticNotice.id, "the notification id is latched")

	o.syncAcousticModelsNotice()
	assert.Len(t, list(), 1, "a second sync must not double-raise")

	// A model loads -> ok -> the notice is cleared.
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: &mockModelInstance{id: RegistryIDBirdNETV24}}
	o.syncAcousticModelsNotice()
	assert.Empty(t, list(), "the notice is deleted once a model is loaded")
	assert.Empty(t, o.acousticNotice.id, "the latch is cleared")
}

// TestSyncAcousticModelsNotice_LoadFailedMessage pins that the load_failed state gets a
// distinct message pointing at the inference page, not "install one" (model de-privilege
// epic, Phase 4): a model IS installed but failed to load.
func TestSyncAcousticModelsNotice_LoadFailedMessage(t *testing.T) {
	// Not parallel: uses the process-global notification service.
	svc := setupTestNotification(t)

	o := newTestOrchestrator(t)
	o.recordLoadFailure(RegistryIDBirdNETV24, errors.NewStd("simulated load failure"))
	require.Equal(t, AcousticModelsLoadFailed, o.AcousticModelsState())

	o.syncAcousticModelsNotice()
	notes, err := svc.List(nil)
	require.NoError(t, err)
	require.Len(t, notes, 1)
	assert.Equal(t, "load_failed", notes[0].Metadata["acoustic_models_state"])
	assert.Contains(t, notes[0].Message, "failed to load")
	assert.Contains(t, notes[0].Message, "Inference page")
	assert.NotContains(t, notes[0].Message, "install one", "load_failed must not tell the user to install a model they already have")
}

// TestSyncAcousticModelsNotice_NilServiceNoPanic pins that a sync with no notification
// service (not yet initialized, or a bare test) is a safe no-op that latches nothing.
func TestSyncAcousticModelsNotice_NilServiceNoPanic(t *testing.T) {
	notification.ResetForTest()
	t.Cleanup(notification.ResetForTest)

	o := newTestOrchestrator(t)
	assert.NotPanics(t, func() { o.syncAcousticModelsNotice() })
	assert.Empty(t, o.acousticNotice.id)
}
