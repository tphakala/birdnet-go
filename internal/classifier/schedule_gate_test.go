package classifier

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// TestModelRegistry_ScheduleGatedIsExactlyBat pins the schedule-gated set to exactly
// the bat model and verifies isScheduleGated, the single predicate behind
// DefaultTargets, IsModelActive and ModelScheduleStatus. An unknown ID is never
// gated (the zero-value ModelInfo has scheduleGated false), so a custom model still
// analyzes.
func TestModelRegistry_ScheduleGatedIsExactlyBat(t *testing.T) {
	t.Parallel()

	var gated []string
	for id, info := range ModelRegistry {
		if info.scheduleGated {
			gated = append(gated, id)
		}
	}
	assert.ElementsMatch(t, []string{RegistryIDBat}, gated,
		"exactly the bat model is schedule-gated in the registry")

	assert.True(t, isScheduleGated(RegistryIDBat), "bat is schedule-gated")
	assert.False(t, isScheduleGated(RegistryIDBirdNETV24), "v2.4 is not schedule-gated")
	assert.False(t, isScheduleGated(RegistryIDPerchV2), "a loaded secondary is not schedule-gated")
	assert.False(t, isScheduleGated("not-a-model"), "an unknown ID is never schedule-gated")
}

// registerTestGatedModel adds a schedule-gated bat-group entry to the
// package-global ModelRegistry for the duration of the test. Callers must not be
// parallel.
func registerTestGatedModel(t *testing.T, id string) {
	t.Helper()
	ModelRegistry[id] = ModelInfo{ID: id, scheduleGated: true, taxonGroup: TaxonGroupBat}
	t.Cleanup(func() { delete(ModelRegistry, id) })
}

// schedulerStopped reports whether the scheduler's stop channel is closed.
func schedulerStopped(s *nighttimeScheduler) bool {
	select {
	case <-s.stopChan:
		return true
	default:
		return false
	}
}

// TestUnloadModel_SchedulerStopsWithLastGatedModel pins that the nighttime
// scheduler keeps running while any schedule-gated model stays loaded and stops
// only when the last one unloads.
func TestUnloadModel_SchedulerStopsWithLastGatedModel(t *testing.T) {
	// Not parallel: mutates the package-global ModelRegistry.
	const (
		firstID  = "test-gated-first"
		secondID = "test-gated-second"
	)
	registerTestGatedModel(t, firstID)
	registerTestGatedModel(t, secondID)

	o := newTestOrchestrator(t, &mockModelInstance{id: firstID}, &mockModelInstance{id: secondID})
	s := newNighttimeScheduler(nil)
	o.scheduler.Store(s)

	require.NoError(t, o.UnloadModel(firstID))
	assert.False(t, schedulerStopped(s), "scheduler must keep running while another gated model is loaded")

	require.NoError(t, o.UnloadModel(secondID))
	assert.True(t, schedulerStopped(s), "scheduler must stop when the last gated model unloads")
}

// TestUnloadModel_UngatedUnloadKeepsScheduler pins that unloading a model that is
// not schedule-gated never stops the scheduler, including when no gated model is
// loaded at all.
func TestUnloadModel_UngatedUnloadKeepsScheduler(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2})
	s := newNighttimeScheduler(nil)
	o.scheduler.Store(s)

	require.NoError(t, o.UnloadModel(RegistryIDPerchV2))
	assert.False(t, schedulerStopped(s), "unloading an ungated model must not stop the scheduler")
}

// TestLoadModel_SchedulerRestartsOnlyForFirstGatedModel pins that LoadModel gives
// the first schedule-gated model a fresh running scheduler, and leaves it in place
// when an ungated model or a second gated model loads.
func TestLoadModel_SchedulerRestartsOnlyForFirstGatedModel(t *testing.T) {
	// Not parallel: mutates the package-global ModelRegistry, modelLoaders and settings.
	const (
		firstID   = "test-gated-load-first"
		secondID  = "test-gated-load-second"
		ungatedID = "test-ungated-load"
	)
	settings := conftest.GetTestSettings()
	// Night-only with no configured location makes a started scheduler's initial
	// refresh set locationWarn, which tells a started scheduler from a stored one.
	settings.Bat.NighttimeOnly = true
	settings.BirdNET.LocationConfigured = false
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	// An ungated model, registered the same way, to load before any gated one.
	ModelRegistry[ungatedID] = ModelInfo{ID: ungatedID}
	t.Cleanup(func() { delete(ModelRegistry, ungatedID) })
	for _, id := range []string{ungatedID, firstID, secondID} {
		if id != ungatedID {
			registerTestGatedModel(t, id)
		}
		modelLoaders[id] = func(orc *Orchestrator, _ int) error {
			orc.models[id] = &modelEntry{instance: &mockModelInstance{id: id}}
			return nil
		}
		t.Cleanup(func() { delete(modelLoaders, id) })
	}

	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.Settings = settings
	initial := newNighttimeScheduler(suncalc.NewSunCalc(60.17, 24.94))
	o.scheduler.Store(initial)
	t.Cleanup(func() { o.scheduler.Load().stop() })

	require.NoError(t, o.LoadModel(ungatedID))
	assert.Same(t, initial, o.scheduler.Load(), "an ungated model leaves the scheduler in place")
	assert.False(t, schedulerStopped(initial), "an ungated load does not stop the scheduler")

	require.NoError(t, o.LoadModel(firstID))
	started := o.scheduler.Load()
	assert.NotSame(t, initial, started, "the first gated model gets a fresh scheduler")
	assert.True(t, schedulerStopped(initial), "the previous scheduler is stopped")
	assert.False(t, schedulerStopped(started), "the fresh scheduler is not stopped")
	assert.True(t, started.locationWarn.Load(), "the fresh scheduler was started and ran its initial refresh")

	require.NoError(t, o.LoadModel(secondID))
	assert.Same(t, started, o.scheduler.Load(), "a second gated model keeps the running scheduler")
	assert.False(t, schedulerStopped(started), "the running scheduler is not stopped by a second gated load")
}

// TestHasScheduleGatedModelLocked pins the predicate behind scheduler start: it is
// true for any loaded gated model, not only the Bat ID.
func TestHasScheduleGatedModelLocked(t *testing.T) {
	// Not parallel: mutates the package-global ModelRegistry.
	const gatedID = "test-gated-predicate"
	registerTestGatedModel(t, gatedID)

	none := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2})
	assert.False(t, none.hasScheduleGatedModelLocked(""), "an ungated model alone does not count")

	gated := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDPerchV2}, &mockModelInstance{id: gatedID})
	assert.True(t, gated.hasScheduleGatedModelLocked(""), "a non-Bat gated model counts")
	assert.False(t, gated.hasScheduleGatedModelLocked(gatedID), "the excluded ID is ignored")
}

// TestSetSunCalc_StartsSchedulerForAnyGatedModel pins that the scheduler starts
// when any schedule-gated model is loaded, not only the Bat ID, and not when only
// ungated models are loaded. A started scheduler with night-only enabled and no
// suncalc instance records its one-time warning during its initial refresh.
func TestSetSunCalc_StartsSchedulerForAnyGatedModel(t *testing.T) {
	// Not parallel: mutates the package-global ModelRegistry and settings.
	const gatedID = "test-gated-sunscalc"
	registerTestGatedModel(t, gatedID)
	settings := conftest.GetTestSettings()
	settings.Bat.NighttimeOnly = true
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	started := func(mocks ...*mockModelInstance) bool {
		o := newTestOrchestrator(t, mocks...)
		o.SetSunCalc(nil)
		s := o.scheduler.Load()
		require.NotNil(t, s)
		t.Cleanup(s.stop)
		return s.warnOnce.Load()
	}

	assert.True(t, started(&mockModelInstance{id: gatedID}), "a non-Bat gated model starts the scheduler")
	assert.False(t, started(&mockModelInstance{id: RegistryIDPerchV2}), "ungated models do not start the scheduler")
}
