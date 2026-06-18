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
	o.models[testSecondaryID] = &modelEntry{instance: old}
	// Loaded on a different backend so the gate fires.
	o.secondaryBackend = secondaryBackendKey{backend: "onnx"}

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
	assert.Equal(t, secondaryBackendKey{backend: "openvino", ovDevice: "gpu", ovPath: "/opt/ov"}, o.secondaryBackend,
		"gate should advance to the new triplet")
}

func TestReloadSecondaryModels_NoOpWhenTripletUnchanged(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[testSecondaryID] = &modelEntry{instance: old}
	// Already on the current triplet: reload must be a no-op.
	o.secondaryBackend = secondaryBackendKey{backend: "openvino", ovDevice: "gpu", ovPath: "/opt/ov"}

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
	o.models[testSecondaryID] = &modelEntry{instance: old}
	o.secondaryBackend = secondaryBackendKey{backend: "onnx"}

	buildErr := errors.Newf("simulated build failure").Build()
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return nil, buildErr
	})

	err := o.ReloadSecondaryModels()
	require.Error(t, err, "build failure should be returned to the caller")

	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance, "old instance must keep serving on build failure")
	assert.Equal(t, int32(0), old.closes.Load(), "old instance must not be closed when its rebuild fails")
	// Advance-always: the gate moves to the new triplet so an unrelated reload
	// does not retry the failed build.
	assert.Equal(t, secondaryBackendKey{backend: "openvino", ovDevice: "gpu"}, o.secondaryBackend,
		"gate should still advance after a build failure")
}

func TestReloadSecondaryModels_SkipsNonOVCapableSecondary(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	// A non-OV-capable secondary (no builder registered, e.g. ORT-only Bat) must
	// not be touched by the reload.
	batLike := &reloadFakeModel{id: RegistryIDBat}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[RegistryIDBat] = &modelEntry{instance: batLike}
	o.secondaryBackend = secondaryBackendKey{backend: "onnx"}

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Same(t, ModelInstance(batLike), o.models[RegistryIDBat].instance, "non-OV secondary must be untouched")
	assert.Equal(t, int32(0), batLike.closes.Load(), "non-OV secondary must not be closed")
}

func TestReloadSecondaryModels_OrphanedEntrySkipsSwapAndClosesNew(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Entry is present in the map but its instance was torn down by a concurrent
	// Delete/Unload (instance == nil). The reload must not resurrect a detached
	// entry; it must close the freshly built instance and leave the entry nil.
	o.models[testSecondaryID] = &modelEntry{instance: nil}
	o.secondaryBackend = secondaryBackendKey{backend: "onnx"}

	built := &reloadFakeModel{id: testSecondaryID}
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return built, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Nil(t, o.models[testSecondaryID].instance, "orphaned entry must not be resurrected")
	assert.Equal(t, int32(1), built.closes.Load(), "freshly built instance must be closed when the entry was orphaned")
}

func TestReloadSecondaryModels_PartialFailureAmongMultiple(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "")

	oldOK := &reloadFakeModel{id: testSecondaryID}
	oldFail := &reloadFakeModel{id: testSecondaryID2}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[testSecondaryID] = &modelEntry{instance: oldOK}
	o.models[testSecondaryID2] = &modelEntry{instance: oldFail}
	o.secondaryBackend = secondaryBackendKey{backend: "onnx"}

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
	o.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}}
	o.secondaryBackend = secondaryBackendKey{backend: "onnx"}

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

	// Force a rebuild on each iteration by alternating the device so the gate
	// keeps firing (the previous successful reload advanced it to the prior value).
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
