package classifier

import (
	"context"
	"os"
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
	"github.com/tphakala/birdnet-go/internal/inference"
)

// gpuFaultTestDevice is the OpenVINO device name the GPU-reporting fake claims.
const gpuFaultTestDevice = "GPU.0"

// gpuFaultTestRSS is a previously recorded RSS figure the reload must restore.
const gpuFaultTestRSS int64 = 123456

// Spec values for the GPU fake; any non-empty clip makes the warm-up and probe run.
const (
	gpuFaultTestWait       = 2 * time.Second
	gpuFaultTestPoll       = 5 * time.Millisecond
	gpuFaultTestSampleRate = 48000
	gpuFaultTestClipLength = 3 * time.Second
)

// gpuFaultFakeModel reports the OpenVINO GPU and trips the fault latch seam on
// its first Predict, like a GPU that dies during the reload's warm-up.
type gpuFaultFakeModel struct {
	reloadFakeModel
	trip      func()
	onSpec    func()
	predicted atomic.Int32
}

func (m *gpuFaultFakeModel) Predict(ctx context.Context, in [][]float32) ([]datastore.Results, error) {
	if m.predicted.Add(1) == 1 && m.trip != nil {
		m.trip()
	}
	return m.reloadFakeModel.Predict(ctx, in)
}

func (m *gpuFaultFakeModel) RuntimeInfo() (device, backend, precision string) {
	return gpuFaultTestDevice, BackendOpenVINO, ""
}

// newGPUFaultFake returns a GPU fake that flips faulted on its first Predict.
func newGPUFaultFake(id string, faulted *atomic.Bool) *gpuFaultFakeModel {
	m := &gpuFaultFakeModel{trip: func() { faulted.Store(true) }}
	m.id = id
	return m
}

// Spec gives the fake a model spec with samples so the warm-up and
// the latency probe actually run.
func (m *gpuFaultFakeModel) Spec() ModelSpec {
	if m.onSpec != nil {
		m.onSpec()
	}
	return ModelSpec{SampleRate: gpuFaultTestSampleRate, ClipLength: gpuFaultTestClipLength}
}

// setGPUFaultSeam replaces the ovGPUFault seam for the test and returns the flag
// that flips it to faulted. Not parallel: the seam is a package global.
func setGPUFaultSeam(t *testing.T) *atomic.Bool {
	t.Helper()
	orig := ovGPUFault
	t.Cleanup(func() { ovGPUFault = orig })
	var faulted atomic.Bool
	ovGPUFault = func() (bool, error) { return faulted.Load(), nil }
	return &faulted
}

func TestBuiltOnFailedOpenVINOGPU(t *testing.T) {
	faulted := setGPUFaultSeam(t)
	gpu := &gpuFaultFakeModel{}
	cpu := &reloadFakeModel{}

	assert.False(t, builtOnFailedOpenVINOGPU(gpu), "no fault, no refusal")
	faulted.Store(true)
	assert.True(t, builtOnFailedOpenVINOGPU(gpu), "OpenVINO GPU instance under a fault")
	assert.False(t, builtOnFailedOpenVINOGPU(cpu), "an ONNX CPU instance is unaffected by the GPU fault")
}

// TestReloadEntry_GPUFaultDuringWarmupKeepsOldInstance pins that a reload whose
// warm-up trips the GPU fault latch never publishes the dead instance.
func TestReloadEntry_GPUFaultDuringWarmupKeepsOldInstance(t *testing.T) {
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")
	faulted := setGPUFaultSeam(t)

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: "onnx"}}
	o.modelRSS[testSecondaryID] = gpuFaultTestRSS

	next := newGPUFaultFake(testSecondaryID, faulted)
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return next, nil
	})

	require.Error(t, o.ReloadSecondaryModels())

	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance, "the faulted instance must not be published")
	assert.Equal(t, int32(0), old.closes.Load(), "the serving instance must stay open")
	assert.Equal(t, int32(1), next.closes.Load(), "the faulted instance must be closed")
	assert.Equal(t, int32(1), next.predicted.Load(), "the latency probe must be skipped after the fault")
	assert.Equal(t, gpuFaultTestRSS, o.modelRSS[testSecondaryID], "the previous RSS figure must be restored")
}

// TestReloadEntry_GPUFaultReturnsSentinel checks the error reloadEntry returns
// directly, and that a missing earlier RSS figure is deleted rather than kept.
func TestReloadEntry_GPUFaultReturnsSentinel(t *testing.T) {
	faulted := setGPUFaultSeam(t)

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: old, backend: secondaryBackendKey{backend: "onnx"}}
	next := newGPUFaultFake(testSecondaryID, faulted)

	swapped, err := o.reloadEntry(testSecondaryID, func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
		return next, nil
	}, reloadOpts{settings: conftest.GetTestSettings(), backend: &secondaryBackendKey{backend: "openvino"}})

	require.ErrorIs(t, err, inference.ErrOpenVINOGPUFault)
	assert.False(t, swapped)
	assert.Same(t, ModelInstance(old), o.models[testSecondaryID].instance)
	assert.Equal(t, secondaryBackendKey{backend: "onnx"}, o.models[testSecondaryID].backend, "the entry triplet must not advance with a refused swap")
	_, hasRSS := o.modelRSS[testSecondaryID]
	assert.False(t, hasRSS, "with no earlier figure, the warm-up's RSS entry must be dropped")
}

// TestReplaceVariant_GPUFaultDuringWarmupRollsBackKeepingOldFiles pins that a
// variant swap whose new instance faults the GPU rolls back and keeps the old
// variant's files.
func TestReplaceVariant_GPUFaultDuringWarmupRollsBackKeepingOldFiles(t *testing.T) {
	faulted := setGPUFaultSeam(t)

	old := &reloadFakeModel{id: gaplessSecondaryID}
	o, mm, entry, modelsDir, srvURL := seedLoadedSecondary(t, old)

	next := newGPUFaultFake(gaplessSecondaryID, faulted)
	registerTestSecondaryBuilder(t, gaplessSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return next, nil
	})

	fp32Path := filepath.Join(modelsDir, entry.ID, "model.onnx")
	require.NoError(t, os.MkdirAll(filepath.Dir(fp32Path), 0o755))
	require.NoError(t, os.WriteFile(fp32Path, []byte("fp32-model"), 0o644))

	old0 := &InstalledModel{CatalogID: entry.ID, VariantID: "fp32", ModelPath: fp32Path}
	err := mm.replaceVariant(t.Context(), &entry, old0, "int8-arm", srvURL, nil)

	require.Error(t, err)
	require.ErrorIs(t, err, inference.ErrOpenVINOGPUFault)
	assert.Same(t, ModelInstance(old), o.models[gaplessSecondaryID].instance, "the old variant keeps serving")
	assert.Equal(t, int32(0), old.closes.Load())
	assert.Equal(t, int32(1), next.closes.Load())
	assert.FileExists(t, fp32Path, "the old variant's file must be kept")
	assert.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID)
}

// TestReloadEntry_GPUFaultRSSRestoreDoesNotResurrectUnloadedModel pins that the
// refused swap restores the RSS figure before the model's lock is released, so an
// unload that clears the figure while the refused instance is being closed is not
// undone.
func TestReloadEntry_GPUFaultRSSRestoreDoesNotResurrectUnloadedModel(t *testing.T) {
	faulted := setGPUFaultSeam(t)

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: old}
	o.modelRSS[testSecondaryID] = gpuFaultTestRSS

	next := newGPUFaultFake(testSecondaryID, faulted)
	// An unload clears the figure under rssMu; running it from the close hook puts it
	// after the entry lock is released.
	next.onClose = func() {
		o.rssMu.Lock()
		delete(o.modelRSS, testSecondaryID)
		o.rssMu.Unlock()
	}

	_, err := o.reloadEntry(testSecondaryID, func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
		return next, nil
	}, reloadOpts{settings: conftest.GetTestSettings()})

	require.ErrorIs(t, err, inference.ErrOpenVINOGPUFault)
	_, hasRSS := o.modelRSS[testSecondaryID]
	assert.False(t, hasRSS, "a figure cleared by an unload must stay cleared")
}

// TestReloadEntry_RSSSnapshotTakenUnderInferenceMu pins that the previous RSS
// figure is read while inferenceMu is held, the lock the warm-up writers hold, so a warm-up that finishes just before the reload's own cannot be missed.
func TestReloadEntry_RSSSnapshotTakenUnderInferenceMu(t *testing.T) {
	faulted := setGPUFaultSeam(t)

	old := &reloadFakeModel{id: testSecondaryID}
	o := newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[testSecondaryID] = &modelEntry{instance: old}
	next := newGPUFaultFake(testSecondaryID, faulted)
	reachedWarmup := make(chan struct{})
	proceed := make(chan struct{})
	var once sync.Once
	next.onSpec = func() {
		once.Do(func() {
			close(reachedWarmup)
			<-proceed
		})
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		_, _ = o.reloadEntry(testSecondaryID, func(*Orchestrator, *conf.Settings, int) (ModelInstance, error) {
			return next, nil
		}, reloadOpts{settings: conftest.GetTestSettings()})
	}()
	<-reachedWarmup
	o.rssMu.Lock()
	close(proceed)

	// With rssMu held, a reload that snapshots under inferenceMu takes it and then
	// waits on rssMu; one that snapshots first never takes it.
	holdsInference := assert.Eventually(t, func() bool {
		if o.inferenceMu.TryLock() {
			o.inferenceMu.Unlock()
			return false
		}
		return true
	}, gpuFaultTestWait, gpuFaultTestPoll, "the RSS snapshot must wait on rssMu while holding inferenceMu")

	o.rssMu.Unlock()
	<-done
	require.True(t, holdsInference)
}
