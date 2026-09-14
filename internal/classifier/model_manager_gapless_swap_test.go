package classifier

import (
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// gaplessSecondaryID is a synthetic OV-capable registry id for the gapless
// variant-swap tests. Distinct from testSecondaryID so registration never collides
// with the ReloadSecondaryModels tests.
const gaplessSecondaryID = "TestGaplessSecondary_OV"

// seedLoadedSecondary wires a fake OV-capable secondary as LOADED in a test
// orchestrator and returns the orchestrator, the model manager, the two-variant
// catalog entry (keyed to gaplessSecondaryID), the models dir, and the download
// server URL. The old instance is the one currently serving; the caller registers
// the builder that produces the new instance. mm has no settings (config sync off),
// so the swap exercises the reload path without touching global config.
func seedLoadedSecondary(t *testing.T, old *reloadFakeModel) (o *Orchestrator, mm *ModelManager, entry CatalogEntry, modelsDir, srvURL string) {
	t.Helper()
	// Publish test settings so o.currentSettings() (which prefers the global snapshot,
	// used for the thread-budget lookup) has one; restore on cleanup.
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	entry, modelsDir, srvURL = twoVariantServerEntry(t)
	entry.RegistryID = gaplessSecondaryID

	o = newTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
	o.models[gaplessSecondaryID] = &modelEntry{instance: old}
	o.SetModelsDir(modelsDir)

	mm = NewModelManager(modelsDir, o, nil)
	mm.mu.Lock()
	mm.downloading[entry.ID] = &DownloadState{CatalogID: entry.ID, Status: StatusDownloading}
	mm.mu.Unlock()
	return o, mm, entry, modelsDir, srvURL
}

// TestReplaceVariant_LoadedSecondaryGaplessSwap verifies that swapping a LOADED
// secondary's variant builds the new instance and swaps it in through reloadEntry
// WITHOUT ever unloading the old one (gapless): the old instance keeps serving during
// the build, the new one is swapped in and the old closed, the record advances, the new
// file is on disk, and the superseded old file is removed. A fatal unloadFn proves the
// unload-then-load window is gone.
func TestReplaceVariant_LoadedSecondaryGaplessSwap(t *testing.T) {
	// Not parallel: registers a global builder and mutates global settings.
	old := &reloadFakeModel{id: gaplessSecondaryID}
	o, mm, entry, modelsDir, srvURL := seedLoadedSecondary(t, old)
	mm.unloadFn = func(string) error {
		t.Error("a gapless secondary variant swap must not unload the model")
		return nil
	}

	var built atomic.Int32
	newInst := &reloadFakeModel{id: gaplessSecondaryID}
	registerTestSecondaryBuilder(t, gaplessSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		built.Add(1)
		return newInst, nil
	})

	// The old fp32 file on disk must be removed as superseded after the switch.
	fp32Path := filepath.Join(modelsDir, entry.ID, "model.onnx")
	require.NoError(t, os.MkdirAll(filepath.Dir(fp32Path), 0o755))
	require.NoError(t, os.WriteFile(fp32Path, []byte("fp32-model"), 0o644))

	old0 := &InstalledModel{CatalogID: entry.ID, VariantID: "fp32", ModelPath: fp32Path}
	require.NoError(t, mm.replaceVariant(t.Context(), &entry, old0, "int8-arm", srvURL, nil))

	assert.Equal(t, int32(1), built.Load(), "the new variant must be built once")
	assert.Same(t, ModelInstance(newInst), o.models[gaplessSecondaryID].instance, "the new instance must be swapped in")
	assert.Equal(t, int32(1), old.closes.Load(), "the old instance must be closed once, after the swap")
	assert.Equal(t, int32(0), newInst.closes.Load(), "the new instance must not be closed")
	assert.Equal(t, "int8-arm", installedByID(t, mm, entry.ID).VariantID, "the switch must record the new variant")
	assert.FileExists(t, filepath.Join(modelsDir, entry.ID, "model_int8.onnx"), "the new variant's file must be on disk")
	_, statErr := os.Stat(fp32Path)
	assert.True(t, os.IsNotExist(statErr), "the superseded old file must be removed after the switch")
	assert.Nil(t, mm.GetDownloadState(entry.ID), "no download state may linger after a completed switch")
}

// TestReplaceVariant_LoadedSecondaryRollbackOnBuildFailure verifies the gapless
// rollback: when the new variant fails to build, reloadEntry never swaps, so the old
// instance is still serving. rollbackVariantSwap restores the record, removes the new
// file, and reports failure, and the old instance is neither swapped nor closed.
func TestReplaceVariant_LoadedSecondaryRollbackOnBuildFailure(t *testing.T) {
	// Not parallel: registers a global builder and mutates global settings.
	old := &reloadFakeModel{id: gaplessSecondaryID}
	o, mm, entry, modelsDir, srvURL := seedLoadedSecondary(t, old)

	buildErr := errors.Newf("synthetic build failure").Build()
	registerTestSecondaryBuilder(t, gaplessSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return nil, buildErr
	})

	old0 := &InstalledModel{CatalogID: entry.ID, VariantID: "fp32"}
	err := mm.replaceVariant(t.Context(), &entry, old0, "int8-arm", srvURL, nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to load", "the rollback must report the swap failure")

	assert.Same(t, ModelInstance(old), o.models[gaplessSecondaryID].instance, "a build failure must leave the old instance serving")
	assert.Equal(t, int32(0), old.closes.Load(), "the old instance must not be closed on a build failure")
	assert.Equal(t, "fp32", installedByID(t, mm, entry.ID).VariantID, "rollback must restore the old variant record")
	_, statErr := os.Stat(filepath.Join(modelsDir, entry.ID, "model_int8.onnx"))
	assert.True(t, os.IsNotExist(statErr), "rollback must remove the failed new variant's file")

	// The old instance is still able to serve inference.
	res, perr := o.models[gaplessSecondaryID].instance.Predict(t.Context(), nil)
	require.NoError(t, perr)
	assert.NotEmpty(t, res, "the previous instance must keep serving after a failed swap")
}

// TestReplaceVariant_LoadedSecondaryConcurrentPredictNoGap pins gaplessness under
// -race: a concurrent PredictModel on the same model must never see ErrModelNotLoaded
// (or a closed instance) while a variant swap is in flight. The builder blocks until
// released, so predictions demonstrably run against the old instance during the build
// window; because reloadEntry only ever sets entry.instance to the new instance (never
// nil), no prediction observes a gap.
func TestReplaceVariant_LoadedSecondaryConcurrentPredictNoGap(t *testing.T) {
	// Not parallel: registers a global builder and mutates global settings.
	old := &reloadFakeModel{id: gaplessSecondaryID}
	o, mm, entry, _, srvURL := seedLoadedSecondary(t, old)

	building := make(chan struct{})
	release := make(chan struct{})
	var announceOnce sync.Once
	newInst := &reloadFakeModel{id: gaplessSecondaryID}
	registerTestSecondaryBuilder(t, gaplessSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		announceOnce.Do(func() { close(building) })
		<-release
		return newInst, nil
	})

	var wg sync.WaitGroup
	stop := make(chan struct{})
	var predictErr atomic.Pointer[error]
	for range 4 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
				}
				if _, err := o.PredictModel(t.Context(), gaplessSecondaryID, [][]float32{{0}}); err != nil {
					predictErr.CompareAndSwap(nil, &err)
					return
				}
			}
		})
	}

	swapDone := make(chan error, 1)
	old0 := &InstalledModel{CatalogID: entry.ID, VariantID: "fp32"}
	go func() {
		swapDone <- mm.replaceVariant(t.Context(), &entry, old0, "int8-arm", srvURL, nil)
	}()

	<-building     // the new instance is mid-build; predictions are hitting the old one
	close(release) // let the build finish and the swap commit
	require.NoError(t, <-swapDone)
	close(stop)
	wg.Wait()

	if p := predictErr.Load(); p != nil {
		t.Fatalf("a concurrent prediction saw an error during the gapless swap: %v", *p)
	}
	assert.Same(t, ModelInstance(newInst), o.models[gaplessSecondaryID].instance, "the new instance must be swapped in")
}
