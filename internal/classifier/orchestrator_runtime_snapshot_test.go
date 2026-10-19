package classifier

import (
	"context"
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

// runtimeSnapFake is a ModelInstance whose runtime facts a test controls, so the
// snapshot a load or reload publishes can be told apart by instance.
type runtimeSnapFake struct {
	id      string
	device  string
	backend string
	path    string
	// spec is returned by Spec; a non-zero spec makes the reload run its warm-up.
	spec ModelSpec
	// onPredict, when set, runs once per Predict call.
	onPredict func()
}

func (m *runtimeSnapFake) Predict(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
	if m.onPredict != nil {
		m.onPredict()
	}
	return nil, nil
}
func (m *runtimeSnapFake) Spec() ModelSpec      { return m.spec }
func (m *runtimeSnapFake) ModelID() string      { return m.id }
func (m *runtimeSnapFake) ModelName() string    { return "snap-fake-" + m.id }
func (m *runtimeSnapFake) ModelVersion() string { return "1.0" }
func (m *runtimeSnapFake) NumSpecies() int      { return 7 }
func (m *runtimeSnapFake) Labels() []string     { return []string{"Turdus merula_Common Blackbird"} }
func (m *runtimeSnapFake) Close() error         { return nil }
func (m *runtimeSnapFake) RuntimeInfo() (device, backend, precision string) {
	return m.device, m.backend, ""
}
func (m *runtimeSnapFake) ResolvedModelPath() string { return m.path }

// snapshotByID indexes ModelRuntimeSnapshots by registry ID.
func snapshotByID(o *Orchestrator) map[string]ModelRuntimeSnapshot {
	out := map[string]ModelRuntimeSnapshot{}
	snaps := o.ModelRuntimeSnapshots()
	for i := range snaps {
		out[snaps[i].RegistryID] = snaps[i]
	}
	return out
}

// newSnapshotTestOrchestrator builds an N=0 orchestrator whose loaders for the
// given IDs register runtimeSnapFake instances. The loader for failID fails.
func newSnapshotTestOrchestrator(t *testing.T, threads int, loadIDs []string, failID string) *Orchestrator {
	t.Helper()
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{}
	settings.BirdNET.Threads = threads
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	for _, id := range loadIDs {
		overrideModelLoader(t, id, func(o *Orchestrator, _ int) error {
			o.models[id] = &modelEntry{instance: &runtimeSnapFake{id: id, device: deviceCPU, backend: BackendONNX, path: "/models/" + id + ".onnx"}}
			return nil
		})
	}
	if failID != "" {
		overrideModelLoader(t, failID, func(_ *Orchestrator, _ int) error {
			return errors.NewStd("simulated load failure")
		})
	}

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(func() { o.Delete() })
	return o
}

// TestModelRuntimeSnapshots_FollowTopology pins that the published set equals the
// serving set across load, reload, failed reload, failed load, unload and delete.
func TestModelRuntimeSnapshots_FollowTopology(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	const budget = 3
	o := newSnapshotTestOrchestrator(t, budget, []string{RegistryIDPerchV2, RegistryIDBirdNETV3}, RegistryIDBat)

	// 1. Load A and B.
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))
	require.NoError(t, o.LoadModel(RegistryIDBirdNETV3))
	snaps := snapshotByID(o)
	require.Len(t, snaps, 2)
	assert.Equal(t, budget, snaps[RegistryIDPerchV2].ConfiguredThreads)
	assert.Equal(t, BackendONNX, snaps[RegistryIDPerchV2].Backend)
	assert.Equal(t, 7, snaps[RegistryIDPerchV2].NumSpecies)
	assert.Equal(t, "/models/"+RegistryIDPerchV2+".onnx", snaps[RegistryIDPerchV2].ModelPath)
	assert.Equal(t, uint64(0), snaps[RegistryIDPerchV2].Generation)
	assert.False(t, snaps[RegistryIDPerchV2].LoadedAt.IsZero())

	// 2. Reload A onto another backend and device.
	swapped, err := o.reloadEntry(RegistryIDPerchV2, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return &runtimeSnapFake{id: RegistryIDPerchV2, device: "GPU", backend: BackendOpenVINO}, nil
	}, reloadOpts{threads: 5})
	require.NoError(t, err)
	require.True(t, swapped)
	snaps = snapshotByID(o)
	require.Len(t, snaps, 2)
	assert.Equal(t, BackendOpenVINO, snaps[RegistryIDPerchV2].Backend)
	assert.Equal(t, "GPU", snaps[RegistryIDPerchV2].Device)
	assert.Equal(t, uint64(1), snaps[RegistryIDPerchV2].Generation)
	assert.Equal(t, 5, snaps[RegistryIDPerchV2].ConfiguredThreads)

	// 3. A failing reload keeps the record.
	_, err = o.reloadEntry(RegistryIDPerchV2, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return nil, errors.NewStd("simulated build failure")
	}, reloadOpts{threads: 9})
	require.Error(t, err)
	assert.Equal(t, snaps[RegistryIDPerchV2], snapshotByID(o)[RegistryIDPerchV2])

	// 4. A failing load publishes nothing.
	require.Error(t, o.LoadModel(RegistryIDBat))
	assert.NotContains(t, snapshotByID(o), RegistryIDBat)

	// 5. Unload B.
	require.NoError(t, o.UnloadModel(RegistryIDBirdNETV3))
	snaps = snapshotByID(o)
	require.Len(t, snaps, 1)
	assert.Contains(t, snaps, RegistryIDPerchV2)

	// 6. Delete.
	o.Delete()
	assert.Empty(t, o.ModelRuntimeSnapshots())
}

// TestModelRuntimeSnapshots_UnloadThenReloadKeepsNewRecord pins the identity-checked
// drop: the old entry's teardown, which waits behind in-flight inference, must not
// delete the record of the instance that replaced it.
func TestModelRuntimeSnapshots_UnloadThenReloadKeepsNewRecord(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	o := newSnapshotTestOrchestrator(t, 2, []string{RegistryIDPerchV2}, "")
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))

	o.mu.RLock()
	oldEntry := o.models[RegistryIDPerchV2]
	o.mu.RUnlock()
	require.NotNil(t, oldEntry)

	// Simulate an in-flight inference holding the old entry. The lock is released
	// on every exit, so a failed assertion reports instead of hanging the suite.
	oldEntry.mu.Lock()
	releaseOld := sync.OnceFunc(oldEntry.mu.Unlock)
	t.Cleanup(releaseOld)
	unloaded := make(chan error, 1)
	go func() { unloaded <- o.UnloadModel(RegistryIDPerchV2) }()
	require.Eventually(t, func() bool { return !o.IsModelLoaded(RegistryIDPerchV2) }, 5*time.Second, 5*time.Millisecond)

	overrideModelLoader(t, RegistryIDPerchV2, func(o *Orchestrator, _ int) error {
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &runtimeSnapFake{id: RegistryIDPerchV2, device: "GPU", backend: BackendOpenVINO}}
		return nil
	})
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))

	releaseOld()
	require.NoError(t, <-unloaded)

	snap, ok := snapshotByID(o)[RegistryIDPerchV2]
	require.True(t, ok, "the new instance's record was dropped by the old entry's teardown")
	assert.Equal(t, BackendOpenVINO, snap.Backend)
}

// TestModelRuntimeSnapshots_DoNotWaitForInferenceOrLoad pins that the dump-path
// readers never wait on inference, a model's lock, or a model build.
func TestModelRuntimeSnapshots_DoNotWaitForInferenceOrLoad(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	o := newSnapshotTestOrchestrator(t, 2, []string{RegistryIDPerchV2}, "")
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))

	o.mu.RLock()
	entry := o.models[RegistryIDPerchV2]
	o.mu.RUnlock()

	o.inferenceMu.Lock()
	entry.mu.Lock()
	o.mu.Lock()
	t.Cleanup(func() {
		o.mu.Unlock()
		entry.mu.Unlock()
		o.inferenceMu.Unlock()
	})

	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = o.ModelRuntimeSnapshots()
		_ = o.RangeFilterRuntimeSummary()
		_ = o.ModelsDirSnapshot()
	}()
	require.Eventually(t, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, 5*time.Second, 5*time.Millisecond, "a snapshot reader waited on an orchestrator lock")
}

// TestConfiguredThreadsFor pins that v2.4 reports the thread setting it really
// uses, while every other model reports the budget it was handed.
func TestConfiguredThreadsFor(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.BirdNET.Threads = 3

	assert.Equal(t, 3, configuredThreadsFor(RegistryIDBirdNETV24, settings, 12))
	assert.Equal(t, 12, configuredThreadsFor(RegistryIDPerchV2, settings, 12))
}

// TestRuntimeSnapshotReaders_ZeroOrchestrator pins that the readers answer on an
// orchestrator that has published nothing, as tests and a torn-down one do.
func TestRuntimeSnapshotReaders_ZeroOrchestrator(t *testing.T) {
	t.Parallel()
	o := &Orchestrator{}

	assert.NotNil(t, o.ModelRuntimeSnapshots())
	assert.Empty(t, o.ModelRuntimeSnapshots())
	assert.Empty(t, o.ModelsDirSnapshot())
	assert.Equal(t, RangeFilterRuntimeSummary{}, o.RangeFilterRuntimeSummary())
}

// TestPublishModelRuntime_ReloadSkipsForeignEntry pins that a reload swap on an
// entry that was unloaded and replaced does not overwrite the replacement's
// record. The builder stands in for the load of the new entry, which publishes
// while the old entry's reload is still building.
func TestPublishModelRuntime_ReloadSkipsForeignEntry(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	o := newSnapshotTestOrchestrator(t, 2, []string{RegistryIDPerchV2}, "")
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))

	swapped, err := o.reloadEntry(RegistryIDPerchV2, func(o *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		o.publishModelRuntime(&modelEntry{}, &ModelRuntimeSnapshot{RegistryID: RegistryIDPerchV2, Backend: BackendOpenVINO})
		return &runtimeSnapFake{id: RegistryIDPerchV2, device: deviceCPU, backend: BackendONNX}, nil
	}, reloadOpts{threads: 2})
	require.NoError(t, err)
	require.True(t, swapped)

	assert.Equal(t, BackendOpenVINO, snapshotByID(o)[RegistryIDPerchV2].Backend,
		"a reload of a replaced entry overwrote the newer entry's record")
}

// TestModelRuntimeSnapshots_StartupLoad pins that a model loaded at construction
// from models.enabled is published with the thread budget it was built with.
func TestModelRuntimeSnapshots_StartupLoad(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	const threads = 3
	isolateTestConfig(t)
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{"perch_v2"}
	settings.BirdNET.Threads = threads
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	overrideModelLoader(t, RegistryIDPerchV2, func(o *Orchestrator, _ int) error {
		o.models[RegistryIDPerchV2] = &modelEntry{instance: &runtimeSnapFake{id: RegistryIDPerchV2, device: deviceCPU, backend: BackendONNX}}
		return nil
	})

	o, err := NewOrchestrator(settings)
	require.NoError(t, err)
	t.Cleanup(func() { o.Delete() })

	snap, ok := snapshotByID(o)[RegistryIDPerchV2]
	require.True(t, ok, "a model loaded at startup has no runtime record")
	assert.Equal(t, threads, snap.ConfiguredThreads)
	assert.Equal(t, BackendONNX, snap.Backend)
}

// TestModelRuntimeSnapshots_OrphanedReloadKeepsNoRecord pins that a reload whose
// entry was unloaded after the build, during the warm-up, publishes nothing: the
// swap finds the entry orphaned and the model is not serving.
func TestModelRuntimeSnapshots_OrphanedReloadKeepsNoRecord(t *testing.T) {
	// Not parallel: overrides the global loaders and settings snapshot.
	o := newSnapshotTestOrchestrator(t, 2, []string{RegistryIDPerchV2}, "")
	require.NoError(t, o.LoadModel(RegistryIDPerchV2))

	var unloaded atomic.Bool
	next := &runtimeSnapFake{
		id: RegistryIDPerchV2, device: deviceCPU, backend: BackendOpenVINO,
		spec: ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second},
	}
	next.onPredict = func() {
		if unloaded.CompareAndSwap(false, true) {
			assert.NoError(t, o.UnloadModel(RegistryIDPerchV2))
		}
	}

	swapped, err := o.reloadEntry(RegistryIDPerchV2, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return next, nil
	}, reloadOpts{threads: 2})
	require.NoError(t, err)
	require.True(t, unloaded.Load(), "the warm-up never ran, so the swap-time orphan check was not reached")
	assert.False(t, swapped)
	assert.NotContains(t, snapshotByID(o), RegistryIDPerchV2)
}
