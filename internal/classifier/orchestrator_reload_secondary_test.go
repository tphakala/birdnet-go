package classifier

import (
	"context"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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
	id           string
	closes       atomic.Int32
	onClose      func()
	resolvedPath string
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
func (m *reloadFakeModel) RuntimeInfo() (device, backend, precision string) {
	return deviceCPU, BackendONNX, ""
}
func (m *reloadFakeModel) ResolvedModelPath() string { return m.resolvedPath }

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

// TestReloadSecondaryModels_ThreadCountChangeForcesRebuild verifies that a runtime
// change to the CPU thread budget (birdnet.threads) rebuilds an OV-capable secondary
// even when the backend, device, and OpenVINO path are unchanged, so a secondary
// re-applies the new thread count live exactly as the primary model does. Before the
// fix the per-entry gate keyed only on backend/device/path, so a pure thread-count
// change was silently skipped and secondaries kept their old thread count until a
// restart. Mutation check: this fails if `threads` is dropped from secondaryBackendKey.
func TestReloadSecondaryModels_ThreadCountChangeForcesRebuild(t *testing.T) {
	// CPU device so INFERENCE_NUM_THREADS is meaningful; only Threads differs from
	// the loaded entry below.
	s := conftest.GetTestSettings()
	s.BirdNET.Backend = "openvino"
	s.BirdNET.OpenVINODevice = "cpu"
	s.BirdNET.OpenVINOPath = "/opt/ov"
	s.BirdNET.Threads = 1
	conftest.SetTestSettings(s)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Loaded with the SAME backend/device/path but a different thread count (4).
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{
		backend: "openvino", ovDevice: "cpu", ovPath: "/opt/ov", threads: 4,
	}}

	var built atomic.Int32
	var gotThreads atomic.Int32
	newInst := &reloadFakeModel{id: testSecondaryID}
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, settings *conf.Settings, threads int) (ModelInstance, error) {
		built.Add(1)
		gotThreads.Store(int32(threads)) //nolint:gosec // small test thread count
		assert.Equal(t, 1, settings.BirdNET.Threads, "builder must see the new thread count")
		return newInst, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Equal(t, int32(1), built.Load(), "a thread-count change must rebuild the secondary")
	assert.Same(t, ModelInstance(newInst), o.models[testSecondaryID].instance, "new instance should be swapped in")
	assert.Equal(t, int32(1), old.closes.Load(), "old instance should be closed once")
	assert.Equal(t, int32(1), gotThreads.Load(), "builder should receive the new per-model thread budget")
	assert.Equal(t, secondaryBackendKey{backend: "openvino", ovDevice: "cpu", ovPath: "/opt/ov", threads: 1},
		o.models[testSecondaryID].backend, "entry key should advance to the new thread count")
}

// TestReloadSecondaryModels_NoOpWhenThreadsUnchangedNonZero verifies the skip
// direction of the thread-aware gate at a NON-ZERO thread count: when the entry
// was already built with the same backend/device/path AND the same thread count,
// an unrelated reload_birdnet trigger must NOT rebuild it. The sibling
// TestReloadSecondaryModels_NoOpWhenTripletUnchanged only exercises the zero-value
// (threads:0) match; this brackets the new `threads` field at a real value so a
// regression that made every reload rebuild (e.g. comparing against a zero-value
// key) is caught.
func TestReloadSecondaryModels_NoOpWhenThreadsUnchangedNonZero(t *testing.T) {
	s := conftest.GetTestSettings()
	s.BirdNET.Backend = "openvino"
	s.BirdNET.OpenVINODevice = "cpu"
	s.BirdNET.OpenVINOPath = "/opt/ov"
	s.BirdNET.Threads = 4
	conftest.SetTestSettings(s)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Already built with the exact current key, including threads:4.
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{
		backend: "openvino", ovDevice: "cpu", ovPath: "/opt/ov", threads: 4,
	}}

	var built atomic.Int32
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		built.Add(1)
		return &reloadFakeModel{id: testSecondaryID}, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Equal(t, int32(0), built.Load(), "unchanged threads (and backend/device/path) must NOT rebuild")
	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance, "instance must be left in place")
	assert.Equal(t, int32(0), old.closes.Load(), "old instance must not be closed")
}

// TestReloadSecondaryModels_WarmupHoldsInferenceMu verifies that the hot-reload
// warm-up of the freshly built secondary serializes via inferenceMu, so it cannot
// run a second model session concurrently with live inference. Mutation check:
// this fails if the warm-up runs without inferenceMu (the pre-fix behavior).
func TestReloadSecondaryModels_WarmupHoldsInferenceMu(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	// Loaded on a different backend so the per-entry gate fires and the rebuild runs.
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: "onnx"}}

	started := make(chan struct{})
	release := make(chan struct{})
	// A non-empty Spec makes the warm-up actually run Predict (sized from the spec);
	// the blocking Predict lets us observe inferenceMu while the warm-up is in flight.
	newInst := &mockModelInstance{
		id:   testSecondaryID,
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
		predict: func(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
			close(started)
			<-release
			return nil, nil
		},
	}
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return newInst, nil
	})

	done := make(chan struct{})
	go func() { defer close(done); _ = o.ReloadSecondaryModels() }()

	select {
	case <-started:
	case <-time.After(2 * time.Second):
		t.Fatal("reload warm-up Predict did not start")
	}

	// While the warm-up inference runs, inferenceMu must be held so a live
	// PredictModel cannot run a second model session concurrently. Capture the
	// result and release if TryLock unexpectedly succeeds, so a regression does
	// not leak the lock past the failing assertion.
	locked := !o.inferenceMu.TryLock()
	if !locked {
		o.inferenceMu.Unlock()
	}
	assert.True(t, locked, "reload warm-up must hold inferenceMu during Predict")

	close(release)
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("ReloadSecondaryModels did not finish")
	}
	assert.Same(t, ModelInstance(newInst), o.models[testSecondaryID].instance, "new instance should be swapped in")
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

	// A secondary with no registered OV builder must not be touched by the reload.
	// (Bat used to be the example here, but it is now OV-capable, so use a synthetic
	// ID that is genuinely absent from openvinoCapableSecondaryBuilders.)
	const ortOnlyID = "ORTOnlySecondary"
	ortOnly := &reloadFakeModel{id: ortOnlyID}
	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[ortOnlyID] = &modelEntry{instance: ortOnly}

	require.NoError(t, o.ReloadSecondaryModels())

	assert.Same(t, ModelInstance(ortOnly), o.models[ortOnlyID].instance, "non-OV secondary must be untouched")
	assert.Equal(t, int32(0), ortOnly.closes.Load(), "non-OV secondary must not be closed")
}

// TestBatRegisteredAsOVCapableSecondary pins that the bat model is wired as an
// OpenVINO-capable secondary, so ReloadSecondaryModels rebuilds it on a backend or
// device change (only its heavy embedding extractor honors the preference; the bat
// head stays on ORT). The rebuild/swap/triplet mechanics are covered by the
// synthetic-ID reload tests above.
func TestBatRegisteredAsOVCapableSecondary(t *testing.T) {
	t.Parallel()
	_, ok := openvinoCapableSecondaryBuilders[RegistryIDBat]
	assert.True(t, ok, "bat must be registered as an OpenVINO-capable secondary")
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

// TestReloadSecondaryModels_DiscardsPathResolution pins that the hot-reload path
// queues no configuration repair.
//
// Scope, stated precisely because an earlier version of this test overclaimed:
// it pins ReloadSecondaryModels ITSELF, not the bare `_` inside the three real
// closures in openvinoCapableSecondaryBuilders. Those closures discard the
// resolution on their SUCCESS path, which needs a real ONNX model and a real
// ONNX Runtime, so no unit test can reach it. The builder-ran assertion closes
// the "no rebuild happened" vacuity mode; it does not close that one.
// TestBuildPerch_DoesNotQueuePathCorrection below covers the adjacent half that
// IS reachable: that resolving is separated from queueing, so only a loader can
// queue.
func TestReloadSecondaryModels_DiscardsPathResolution(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")

	o := newTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID})
	o.ModelInfo.ID = permanentRegistryID
	o.models[testSecondaryID] = &modelEntry{
		instance: &reloadFakeModel{id: testSecondaryID},
		backend:  secondaryBackendKey{backend: "onnx"},
	}

	var built atomic.Int32
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		built.Add(1)
		return &reloadFakeModel{id: testSecondaryID}, nil
	})

	require.NoError(t, o.ReloadSecondaryModels())

	require.Equal(t, int32(1), built.Load(),
		"the rebuild must actually run, or an empty queue proves nothing")
	assert.Empty(t, o.pendingPathCorrections,
		"a hot reload must never queue a config repair: a backend or device swap is not a stale path")
}

// TestBuildPerch_DoesNotQueuePathCorrection pins the separation the reload path
// depends on: build* RESOLVES but never QUEUES, so only a loader can turn a
// resolution into a config rewrite. Moving queuePathCorrection into buildPerch
// (the shape that would make ReloadSecondaryModels start rewriting the user's
// paths on a backend swap) fails here.
//
// The build itself is expected to fail: there is no real ONNX model or runtime
// here. The resolution runs first, which is the part under test.
func TestBuildPerch_DoesNotQueuePathCorrection(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)

	modelsDir := filepath.Join(t.TempDir(), "models")
	installedModel := writeVariantModelFile(t, modelsDir, &entry, "fp32")
	installedLabels := writeVariantLabelsFile(t, modelsDir, &entry, "fp32")

	o := &Orchestrator{}
	o.SetModelsDir(modelsDir)

	// A stale gallery-shaped configured set: the resolution substitutes and is
	// repairable, so anything that queued would queue here.
	staleDir := filepath.Join(t.TempDir(), "models", entry.ID)
	settings := &conf.Settings{}
	settings.Perch.ModelPath = filepath.Join(staleDir, filepath.Base(installedModel))
	settings.Perch.LabelPath = filepath.Join(staleDir, filepath.Base(installedLabels))

	res := o.resolveFamilyPaths(RegistryIDPerchV2, modelFileSet{
		model:  settings.Perch.ModelPath,
		labels: settings.Perch.LabelPath,
	}, false)
	require.True(t, res.substituted, "fixture must produce a substituting resolution, or the test proves nothing")
	require.True(t, res.repairable)

	_, _, err := o.buildPerch(settings, 1)
	require.Error(t, err, "no real ONNX runtime here; the resolution before the failure is the subject")

	assert.Empty(t, o.pendingPathCorrections,
		"build* must never queue: only a loader may turn a resolution into a config rewrite")
}
