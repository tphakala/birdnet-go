package classifier

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// testSecondaryID and testSecondaryID2 are synthetic registry IDs used to
// register fake OV-capable secondary builders for the ReloadSecondaryModels tests.
const (
	testSecondaryID  = "TestSecondary_OV"
	testSecondaryID2 = "TestSecondary2_OV"
)

// fakeModelVersion is the version string reported by the test fake.
const fakeModelVersion = "1.0"

// reloadFakeModel is a ModelInstance that records Close calls so tests can assert
// the old instance is torn down after a swap.
type reloadFakeModel struct {
	id      string
	closes  atomic.Int32
	onClose func()
}

func (m *reloadFakeModel) Predict(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
	return []datastore.Results{{Species: "Turdus merula", Confidence: 0.9}}, nil
}
func (m *reloadFakeModel) Spec() ModelSpec      { return ModelSpec{} }
func (m *reloadFakeModel) ModelID() string      { return m.id }
func (m *reloadFakeModel) ModelName() string    { return "reload-fake-" + m.id }
func (m *reloadFakeModel) ModelVersion() string { return fakeModelVersion }
func (m *reloadFakeModel) NumSpecies() int      { return 1 }
func (m *reloadFakeModel) Labels() []string     { return []string{"Turdus merula_Common Blackbird"} }
func (m *reloadFakeModel) Close() error {
	m.closes.Add(1)
	if m.onClose != nil {
		m.onClose()
	}
	return nil
}

// registerTestSecondaryBuilder adds a builder under id for the duration of the
// test, restoring the global map on cleanup. The map is a package global, so the
// tests that use it must not run in parallel.
func registerTestSecondaryBuilder(t *testing.T, id string, build secondaryModelBuilder) {
	t.Helper()
	_, existed := openvinoCapableSecondaryBuilders[id]
	require.False(t, existed, "test builder %s already registered", id)
	openvinoCapableSecondaryBuilders[id] = build
	t.Cleanup(func() { delete(openvinoCapableSecondaryBuilders, id) })
}

// setGlobalBackend publishes test settings with the given backend/device so
// o.currentSettings() (which prefers the global snapshot) returns them.
func setGlobalBackend(t *testing.T, backend, ovDevice, ovPath string) {
	t.Helper()
	s := conftest.GetTestSettings()
	s.BirdNET.Backend = backend
	s.BirdNET.OpenVINODevice = ovDevice
	s.BirdNET.OpenVINOPath = ovPath
	conftest.SetTestSettings(s)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })
}

func TestReloadSecondaryModels_SwapsAndClosesOld(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Loaded on a different backend so the per-entry gate fires.
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: "onnx"}}

	var built atomic.Int32
	newInst := &reloadFakeModel{id: testSecondaryID}
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, settings *conf.Settings, _ int) (ModelInstance, error) {
		built.Add(1)
		// Builder must receive the fresh (gated) settings snapshot.
		assert.Equal(t, "openvino", settings.BirdNET.Backend)
		assert.Equal(t, "gpu", settings.BirdNET.OpenVINODevice)
		return newInst, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Equal(t, int32(1), built.Load(), "builder should run once")
	assert.Same(t, ModelInstance(newInst), o.models[testSecondaryID].instance, "new instance should be swapped in")
	assert.Equal(t, int32(1), old.closes.Load(), "old instance should be closed once")
	assert.Equal(t, int32(0), newInst.closes.Load(), "new instance must not be closed")
	assert.Equal(t, secondaryBackendKey{backend: "openvino", ovDevice: "gpu", ovPath: "/opt/ov"}, o.models[testSecondaryID].backend,
		"entry triplet should advance to the new backend")
}

func TestReloadSecondaryModels_NoOpWhenTripletUnchanged(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Already on the current triplet: reload must be a no-op.
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: "openvino", ovDevice: "gpu", ovPath: "/opt/ov"}}

	var built atomic.Int32
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		built.Add(1)
		return &reloadFakeModel{id: testSecondaryID}, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Equal(t, int32(0), built.Load(), "builder must not run when triplet is unchanged")
	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance, "instance must be untouched")
	assert.Equal(t, int32(0), old.closes.Load(), "old instance must not be closed")
}

func TestReloadSecondaryModels_KeepsOldOnBuildFailure(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: "onnx"}}

	buildErr := errors.Newf("simulated build failure").Build()
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return nil, buildErr
	})

	err := o.ReloadSecondaryModels()
	require.Error(t, err, "build failure should be returned to the caller")

	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance, "old instance must keep serving on build failure")
	assert.Equal(t, int32(0), old.closes.Load(), "old instance must not be closed when its rebuild fails")
	// Advance-always: the entry's triplet moves to the new triplet so an unrelated
	// reload does not retry the failed build.
	assert.Equal(t, secondaryBackendKey{backend: "openvino", ovDevice: "gpu"}, o.models[testSecondaryID].backend,
		"entry triplet should still advance after a build failure")
}

func TestReloadSecondaryModels_SkipsNonOVCapableSecondary(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	// A non-OV-capable secondary (no builder registered, e.g. ORT-only Bat) must
	// not be touched by the reload.
	batLike := &reloadFakeModel{id: RegistryIDBat}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[RegistryIDBat] = &modelEntry{instance: batLike}

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Same(t, ModelInstance(batLike), o.models[RegistryIDBat].instance, "non-OV secondary must be untouched")
	assert.Equal(t, int32(0), batLike.closes.Load(), "non-OV secondary must not be closed")
}

func TestReloadSecondaryModels_OrphanedEntrySkipsSwapAndClosesNew(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// The entry has a live instance and a stale triplet, so the per-entry gate
	// fires and the build runs. The builder simulates a concurrent Delete/Unload
	// tearing the entry down (instance == nil) WHILE the slow build is in flight;
	// the post-build orphan guard must then close the freshly built instance and
	// must not resurrect the detached entry. (The already-orphaned-before-build
	// case is covered by TestReloadSecondaryModels_AlreadyOrphanedSkipsBuild.)
	o.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}, backend: secondaryBackendKey{backend: "onnx"}}

	built := &reloadFakeModel{id: testSecondaryID}
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		// Tear the entry down mid-build to race the swap.
		e := o.models[testSecondaryID]
		e.mu.Lock()
		e.instance = nil
		e.mu.Unlock()
		return built, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Nil(t, o.models[testSecondaryID].instance, "orphaned entry must not be resurrected")
	assert.Equal(t, int32(1), built.closes.Load(), "freshly built instance must be closed when the entry was orphaned during the build")
}

// TestReloadSecondaryModels_AlreadyOrphanedSkipsBuild verifies the up-front gate
// skip: an entry whose instance was already torn down before the reload starts
// (instance == nil) must be skipped WITHOUT running the (slow, JIT-compiling)
// builder, since any instance built for a detached entry would only be discarded.
func TestReloadSecondaryModels_AlreadyOrphanedSkipsBuild(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Already orphaned, with a stale triplet that would otherwise fire the gate.
	o.models[testSecondaryID] = &modelEntry{instance: nil, backend: secondaryBackendKey{backend: "onnx"}}

	var built atomic.Int32
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		built.Add(1)
		return &reloadFakeModel{id: testSecondaryID}, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Equal(t, int32(0), built.Load(), "builder must not run for an already-orphaned entry")
	assert.Nil(t, o.models[testSecondaryID].instance, "orphaned entry must stay nil")
}

func TestReloadSecondaryModels_PartialFailureAmongMultiple(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	oldOK := &reloadFakeModel{id: testSecondaryID}
	oldFail := &reloadFakeModel{id: testSecondaryID2}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Both entries are on a different backend so the per-entry gate fires for each.
	o.models[testSecondaryID] = &modelEntry{instance: oldOK, backend: secondaryBackendKey{backend: "onnx"}}
	o.models[testSecondaryID2] = &modelEntry{instance: oldFail, backend: secondaryBackendKey{backend: "onnx"}}

	newOK := &reloadFakeModel{id: testSecondaryID}
	buildErr := errors.Newf("simulated build failure for second secondary").Build()
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return newOK, nil
	})
	registerTestSecondaryBuilder(t, testSecondaryID2, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return nil, buildErr
	})

	err := o.ReloadSecondaryModels()
	require.Error(t, err, "a build failure among multiple secondaries must be returned")

	// One failure must not abort the others: the model that built successfully is
	// swapped and its old instance closed.
	assert.Same(t, ModelInstance(newOK), o.models[testSecondaryID].instance, "successful secondary should be swapped in")
	assert.Equal(t, int32(1), oldOK.closes.Load(), "old instance of the successful secondary should be closed")
	// The model whose build failed keeps its old instance, not closed.
	assert.Same(t, ModelInstance(oldFail), o.models[testSecondaryID2].instance, "failed secondary should keep serving its old instance")
	assert.Equal(t, int32(0), oldFail.closes.Load(), "old instance of the failed secondary must not be closed")
}

// TestReloadSecondaryModels_RaceWithPredict exercises the entry.mu swap against
// concurrent PredictModel calls. Run with -race to prove the swap is race-free.
func TestReloadSecondaryModels_RaceWithPredict(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}, backend: secondaryBackendKey{backend: "onnx"}}

	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return &reloadFakeModel{id: testSecondaryID}, nil
	})

	var stop atomic.Bool
	var wg sync.WaitGroup
	ctx := t.Context()
	sample := [][]float32{make([]float32, 16)} // arbitrary non-empty frame; the fake model ignores the shape

	for range 4 {
		wg.Go(func() {
			for !stop.Load() {
				// The swap happens entirely under entry.mu and PredictModel reads
				// entry.instance under the same lock, so a predict never sees a
				// half-closed instance. This loop's job is to give the race
				// detector concurrent readers against the swapping writer.
				_, _ = o.PredictModel(ctx, testSecondaryID, sample)
			}
		})
	}

	// Force a rebuild on each iteration by alternating the device so the per-entry
	// gate keeps firing (the previous successful reload advanced it to the prior value).
	// Publish directly (not via setGlobalBackend) to avoid stacking 40 cleanups;
	// the initial setGlobalBackend already registered the reset-to-nil cleanup.
	devices := []string{"cpu", "gpu"}
	for i := range 40 {
		s := conftest.GetTestSettings()
		s.BirdNET.Backend = "openvino"
		s.BirdNET.OpenVINODevice = devices[i%2]
		conftest.SetTestSettings(s)
		require.NoError(t, o.ReloadSecondaryModels())
	}

	stop.Store(true)
	wg.Wait()
}

// TestReloadSecondaryModels_PerEntryTripletRebuildsOnlyStale is the core
// Forgejo #1119 behavior: with per-entry triplet tracking, a reload rebuilds only
// the secondaries whose own recorded triplet differs from the current settings.
// One secondary is already on the current triplet (e.g. installed out-of-band by
// LoadModel after the backend change, which records the entry's triplet at load);
// the other is stale. A single orchestrator-wide gate could not represent this
// mixed state and would rebuild both or neither.
func TestReloadSecondaryModels_PerEntryTripletRebuildsOnlyStale(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")
	currentTriplet := secondaryBackendKey{backend: "openvino", ovDevice: "gpu"}

	upToDate := &reloadFakeModel{id: testSecondaryID}
	stale := &reloadFakeModel{id: testSecondaryID2}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// testSecondaryID is already on the current triplet; testSecondaryID2 is stale.
	o.models[testSecondaryID] = &modelEntry{instance: upToDate, backend: currentTriplet}
	o.models[testSecondaryID2] = &modelEntry{instance: stale, backend: secondaryBackendKey{backend: "onnx"}}

	var builtUpToDate, builtStale atomic.Int32
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		builtUpToDate.Add(1)
		return &reloadFakeModel{id: testSecondaryID}, nil
	})
	newStale := &reloadFakeModel{id: testSecondaryID2}
	registerTestSecondaryBuilder(t, testSecondaryID2, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		builtStale.Add(1)
		return newStale, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	// The up-to-date secondary must be left completely untouched.
	assert.Equal(t, int32(0), builtUpToDate.Load(), "up-to-date secondary must not be rebuilt")
	assert.Same(t, ModelInstance(upToDate), o.models[testSecondaryID].instance, "up-to-date instance must be unchanged")
	assert.Equal(t, int32(0), upToDate.closes.Load(), "up-to-date instance must not be closed")

	// The stale secondary must be rebuilt and its triplet advanced.
	assert.Equal(t, int32(1), builtStale.Load(), "stale secondary must be rebuilt once")
	assert.Same(t, ModelInstance(newStale), o.models[testSecondaryID2].instance, "stale instance must be swapped in")
	assert.Equal(t, int32(1), stale.closes.Load(), "stale old instance must be closed")
	assert.Equal(t, currentTriplet, o.models[testSecondaryID2].backend, "stale entry triplet must advance to current")
}
