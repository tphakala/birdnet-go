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

// setTestGlobalSettings publishes a default settings snapshot so o.currentSettings() (which
// reloadEntry clones in step 2) returns non-nil, and restores the global on cleanup. Tests
// that mutate it must not run in parallel.
func setTestGlobalSettings(t *testing.T) {
	t.Helper()
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })
}

// v24 identity/path helpers for the check tests. The check reads only ModelInfo and
// primaryPath (via identitySnapshot), so a bare *BirdNET with those two fields set is a
// faithful stand-in for a serving or freshly built v2.4 instance.
func servingV24(customPath string) *BirdNET {
	bn := &BirdNET{ModelInfo: customBirdNETV24ModelInfo(customPath)}
	bn.primaryPath = pathResolution{resolved: modelFileSet{model: customPath}}
	return bn
}

// builtinV24 models the stock embedded classifier: empty CustomPath, empty resolved path.
func builtinV24() *BirdNET {
	bn := &BirdNET{ModelInfo: ModelInfo{ID: RegistryIDBirdNETV24}}
	bn.primaryPath = pathResolution{}
	return bn
}

// TestV24SettingsReloadCheck reproduces the reachable settings-reload refusals of the
// former reloadModelInternal(false), which the build-then-swap reloadEntry moved into
// v24SettingsReloadCheck. Each case is a former reloadModelInternal test from
// primary_model_path_test.go, re-expressed against the pure check. The switch keys on the
// RESOLVED model path (primaryPath.resolved.model), which is load-bearing: a cleared or
// vanished file leaves next resolved onto the embedded baseline with an empty CustomPath,
// and a bare CustomPath!=CustomPath comparison would misreport it as "birdnet model file
// changed" instead of "no longer usable".
func TestV24SettingsReloadCheck(t *testing.T) {
	t.Run("recovered path with stale config is not refused", func(t *testing.T) {
		// A start recovered a stale configured path onto the installed file; the reload
		// re-resolves to the SAME recovered file, so the check must accept it (a veto here
		// would fail every settings save for exactly the users the recovery rescued).
		recovered := "/config/models/birdnet-v2.4/primary_dft.onnx"
		old := servingV24(recovered)
		next := servingV24(recovered)
		require.NoError(t, v24SettingsReloadCheck(old, next),
			"re-resolving the recovered path must not be refused")
	})

	t.Run("genuine edit to a different file is refused", func(t *testing.T) {
		old := servingV24("/srv/models/the_old_model.tflite")
		next := servingV24("/srv/models/a_different_model.tflite")
		err := v24SettingsReloadCheck(old, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires orchestrator restart",
			"changing the primary model file is a model-identity change")
		assert.Contains(t, err.Error(), "birdnet model file changed")
	})

	t.Run("clearing the path while a custom model runs is refused", func(t *testing.T) {
		// The user cleared birdnet.modelpath while a custom model was serving: next resolves
		// to the embedded baseline. Refusing (rather than silently swapping onto the baseline
		// under the custom identity) is the MAJOR-3 silent-corruption fix.
		old := servingV24("/srv/models/my_custom_primary.tflite")
		next := builtinV24()
		next.Settings = &conf.Settings{}
		next.settingsAtomic.Store(next.Settings)
		err := v24SettingsReloadCheck(old, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires orchestrator restart",
			"clearing the model path while a custom model runs must be refused")
		assert.Contains(t, err.Error(), "no longer usable")
	})

	t.Run("builtin steady state reloads cleanly", func(t *testing.T) {
		// After a successful recovery onto the baseline, config keeps the stale path, so EVERY
		// reload re-resolves to the same empty result. The check must not refuse it, or every
		// settings save fails forever. Keyed on the resolution having CHANGED from a real file
		// to nothing: here it was already empty, so no refusal.
		require.NoError(t, v24SettingsReloadCheck(builtinV24(), builtinV24()),
			"a steady-state baseline reload must not be refused")
	})

	t.Run("vanished running model is refused and names the reloaded path", func(t *testing.T) {
		const (
			oldPath = "/data/previous_v24.tflite"
			newPath = "/data/just_saved_v24.tflite"
		)
		old := servingV24(oldPath)
		next := builtinV24() // the new resolution finds nothing
		next.Settings = &conf.Settings{}
		next.Settings.BirdNET.ModelPath = newPath
		next.settingsAtomic.Store(next.Settings)

		err := v24SettingsReloadCheck(old, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "requires orchestrator restart")
		assert.Contains(t, err.Error(), newPath, "the message must name the path being reloaded")
		assert.NotContains(t, err.Error(), oldPath,
			"the message must name the reloaded path, not the previous file")
	})

	t.Run("identity change is refused", func(t *testing.T) {
		// Unreachable in Phase 3 (a fresh NewBirdNET only yields BirdNET_V2.4 or fails to
		// build), but the check retains the identity-change branch for Phase 4; pin its text.
		old := &BirdNET{ModelInfo: ModelInfo{ID: RegistryIDBirdNETV24}}
		next := &BirdNET{ModelInfo: ModelInfo{ID: RegistryIDBirdNETV3}}
		err := v24SettingsReloadCheck(old, next)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "model identity changed")
		assert.Contains(t, err.Error(), "requires orchestrator restart")
	})
}

// failingBuilder is an entryBuilder that always fails, to drive reloadEntry's build-failure
// path (equivalent to a NewBirdNET failure at labels/init under the real v2.4 builder).
func failingBuilder(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
	return nil, errors.Newf("simulated build failure").Component("classifier.orchestrator").Category(errors.CategoryModelInit).Build()
}

// TestReloadEntry_BuildFailureLeavesOldServing proves the transactional guarantee the former
// in-place rollback provided: a failed build never swaps, so the previously serving instance
// stays in the entry, is not closed, and the generation does not advance.
func TestReloadEntry_BuildFailureLeavesOldServing(t *testing.T) {
	setTestGlobalSettings(t)
	old := &reloadFakeModel{id: RegistryIDBirdNETV24}
	o := newTestOrchestrator(t)
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: old}

	swapped, err := o.reloadEntry(RegistryIDBirdNETV24, failingBuilder, reloadOpts{check: v24SettingsReloadCheck})

	require.Error(t, err)
	assert.False(t, swapped, "a failed build must not report a swap")
	assert.Same(t, ModelInstance(old), o.models[RegistryIDBirdNETV24].instance, "old instance must still be serving")
	assert.Equal(t, int32(0), old.closes.Load(), "old instance must not be closed by a failed reload")
	assert.Equal(t, uint64(0), o.models[RegistryIDBirdNETV24].generation, "generation must not advance on failure")
}

// TestReloadEntry_CheckRefusalClosesNewKeepsOld proves a post-build refusal (the family
// reloadCheck vetoing, e.g. a v2.4 model-file change) closes the freshly built instance and
// leaves the old one serving. The refusal texts are covered by TestV24SettingsReloadCheck;
// this isolates reloadEntry's mechanics with a fake instance and a stub check.
func TestReloadEntry_CheckRefusalClosesNewKeepsOld(t *testing.T) {
	setTestGlobalSettings(t)
	old := &reloadFakeModel{id: RegistryIDBirdNETV24}
	newInst := &reloadFakeModel{id: RegistryIDBirdNETV24}
	o := newTestOrchestrator(t)
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: old}

	refuseErr := errors.Newf("refused: requires orchestrator restart").
		Component("birdnet").Category(errors.CategoryModelInit).Build()
	swapped, err := o.reloadEntry(RegistryIDBirdNETV24,
		func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) { return newInst, nil },
		reloadOpts{check: func(_, _ ModelInstance) error { return refuseErr }})

	require.Error(t, err)
	assert.False(t, swapped)
	assert.Same(t, ModelInstance(old), o.models[RegistryIDBirdNETV24].instance, "old must still be serving after a refusal")
	assert.Equal(t, int32(0), old.closes.Load(), "old must not be closed")
	assert.Equal(t, int32(1), newInst.closes.Load(), "the refused, freshly built instance must be closed once")
}

// TestReloadEntry_SuccessSwapsBumpsGenerationClosesOld proves the happy path: the new
// instance is swapped in, the old one is closed exactly once, and the generation advances.
func TestReloadEntry_SuccessSwapsBumpsGenerationClosesOld(t *testing.T) {
	setTestGlobalSettings(t)
	old := &reloadFakeModel{id: RegistryIDBirdNETV24}
	newInst := &reloadFakeModel{id: RegistryIDBirdNETV24}
	o := newTestOrchestrator(t)
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: old}

	// A nil check accepts the build (variant-swap semantics), and the fake instance is not a
	// *BirdNET, so the v2.4 family follow-ups are skipped; this isolates the swap mechanics.
	swapped, err := o.reloadEntry(RegistryIDBirdNETV24, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return newInst, nil
	}, reloadOpts{})

	require.NoError(t, err)
	assert.True(t, swapped)
	assert.Same(t, ModelInstance(newInst), o.models[RegistryIDBirdNETV24].instance, "new instance must be swapped in")
	assert.Equal(t, int32(1), old.closes.Load(), "old instance must be closed once")
	assert.Equal(t, int32(0), newInst.closes.Load(), "new instance must not be closed")
	assert.Equal(t, uint64(1), o.models[RegistryIDBirdNETV24].generation, "generation must advance on a successful swap")

	// A second reload advances the generation again.
	_, err = o.reloadEntry(RegistryIDBirdNETV24, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return &reloadFakeModel{id: RegistryIDBirdNETV24}, nil
	}, reloadOpts{})
	require.NoError(t, err)
	assert.Equal(t, uint64(2), o.models[RegistryIDBirdNETV24].generation)
}

// TestReloadEntry_NotLoadedAndDeleted covers the two entry-snapshot refusals.
func TestReloadEntry_NotLoadedAndDeleted(t *testing.T) {
	t.Run("not loaded", func(t *testing.T) {
		o := newTestOrchestrator(t)
		swapped, err := o.reloadEntry("Nope_ID", failingBuilder, reloadOpts{})
		require.Error(t, err)
		assert.False(t, swapped)
		assert.Contains(t, err.Error(), "not loaded")
	})
	t.Run("orchestrator deleted", func(t *testing.T) {
		o := newTestOrchestrator(t)
		o.models = nil
		swapped, err := o.reloadEntry(RegistryIDBirdNETV24, failingBuilder, reloadOpts{})
		require.Error(t, err)
		assert.False(t, swapped)
		assert.Contains(t, err.Error(), "has been deleted")
	})
}

// TestReloadEntry_OrphanedEntryClosesNewInstance proves an entry torn down before the swap
// (a concurrent Delete/Unload) closes the freshly built instance rather than resurrecting a
// detached entry, and reports no swap and no error.
func TestReloadEntry_OrphanedEntryClosesNewInstance(t *testing.T) {
	setTestGlobalSettings(t)
	o := newTestOrchestrator(t)
	// An entry whose instance was already nilled by a Delete/Unload.
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: nil}
	newInst := &reloadFakeModel{id: RegistryIDBirdNETV24}

	swapped, err := o.reloadEntry(RegistryIDBirdNETV24, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return newInst, nil
	}, reloadOpts{})

	require.NoError(t, err)
	assert.False(t, swapped, "an orphaned entry must not be resurrected")
	assert.Equal(t, int32(1), newInst.closes.Load(), "the unpublished instance must be closed")
	assert.Nil(t, o.models[RegistryIDBirdNETV24].instance, "the entry must stay detached")
}

// TestReloadEntry_ConcurrentWithPredict_NoRace runs reloadEntry against live PredictModel on
// the same model under the race detector, proving the build-then-swap (entry.mu) never races
// an in-flight inference reading entry.instance.
func TestReloadEntry_ConcurrentWithPredict_NoRace(t *testing.T) {
	conftest.SetTestSettings(conftest.GetTestSettings())
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	o := newTestOrchestrator(t)
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: &reloadFakeModel{id: RegistryIDBirdNETV24}}

	const iterations = 50
	start := make(chan struct{})
	ctx := t.Context()
	var wg sync.WaitGroup
	var swaps atomic.Int64

	wg.Go(func() {
		<-start
		for range iterations {
			sw, _ := o.reloadEntry(RegistryIDBirdNETV24, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
				return &reloadFakeModel{id: RegistryIDBirdNETV24}, nil
			}, reloadOpts{})
			if sw {
				swaps.Add(1)
			}
		}
	})

	wg.Go(func() {
		<-start
		samples := [][]float32{make([]float32, 144000)}
		for range iterations {
			_, _ = o.PredictModel(ctx, RegistryIDBirdNETV24, samples)
		}
	})

	close(start)
	wg.Wait()
	assert.Positive(t, swaps.Load(), "reloadEntry must complete at least one swap, proving the contended swap path actually ran")
}

// warmupHookModel is a ModelInstance whose non-zero Spec makes Orchestrator.warmup run a
// real warm-up inference; its Predict fires onWarmup, letting a test nil the entry DURING
// the warm-up window between reloadEntry's step-4 read and its step-6 re-check.
type warmupHookModel struct {
	reloadFakeModel
	onWarmup func()
}

func (m *warmupHookModel) Spec() ModelSpec {
	return ModelSpec{SampleRate: 48000, ClipLength: 3 * time.Second}
}

func (m *warmupHookModel) Predict(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
	if m.onWarmup != nil {
		m.onWarmup()
	}
	return []datastore.Results{{Species: "Turdus merula", Confidence: 0.5}}, nil
}

// TestReloadEntry_LateOrphanClosesNewAndCleansRSS covers the step-6 orphan branch: an entry
// torn down by a concurrent Delete/Unload DURING the warm-up (after the step-4 read) must
// close the freshly built instance, leave the entry detached, and clean up the modelRSS entry
// recorded during warm-up. Uses a secondary ID so warm-up + RSS recording actually run.
func TestReloadEntry_LateOrphanClosesNewAndCleansRSS(t *testing.T) {
	setTestGlobalSettings(t)
	o := newTestOrchestrator(t)
	entry := &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}}
	o.models[testSecondaryID] = entry
	// Seed an RSS entry so the step-6 cleanup has something to remove regardless of whether
	// the warm-up's own RSS measurement was available in this environment.
	o.rssMu.Lock()
	o.modelRSS[testSecondaryID] = 123
	o.rssMu.Unlock()

	next := &warmupHookModel{}
	next.id = testSecondaryID
	next.onWarmup = func() {
		// Simulate a Delete/Unload landing during warm-up (between step 4 and step 6).
		entry.mu.Lock()
		entry.instance = nil
		entry.mu.Unlock()
	}

	triplet := secondaryBackendKey{backend: "onnx"}
	sw, err := o.reloadEntry(testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return next, nil
	}, reloadOpts{backend: &triplet, skipSpeciesIndex: true, threads: 1})

	require.NoError(t, err)
	assert.False(t, sw, "a late orphan must not report a swap")
	assert.Equal(t, int32(1), next.closes.Load(), "the freshly built instance must be closed on the late-orphan path")

	entry.mu.Lock()
	instance := entry.instance
	entry.mu.Unlock()
	assert.Nil(t, instance, "the entry must stay detached")

	o.rssMu.Lock()
	_, hasRSS := o.modelRSS[testSecondaryID]
	o.rssMu.Unlock()
	assert.False(t, hasRSS, "the modelRSS entry must be cleaned up on the late-orphan path")
}

// TestReloadEntry_OrphanDuringDeleteReturnsError covers the distinction the former in-place
// reload drew: when the orphan is a full orchestrator Delete (o.models goes nil), the reload
// must surface the "orchestrator has been deleted" error rather than silently reporting
// success, so a caller relying on the error (a settings save) is not misled. A single model
// unloaded (o.models stays non-nil), covered above, stays a benign no-op.
func TestReloadEntry_OrphanDuringDeleteReturnsError(t *testing.T) {
	setTestGlobalSettings(t)
	o := newTestOrchestrator(t)
	entry := &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}}
	o.models[testSecondaryID] = entry

	next := &warmupHookModel{}
	next.id = testSecondaryID
	next.onWarmup = func() {
		// Simulate a full orchestrator Delete landing during warm-up: both o.models and the
		// entry instance go nil.
		o.mu.Lock()
		o.models = nil
		o.mu.Unlock()
		entry.mu.Lock()
		entry.instance = nil
		entry.mu.Unlock()
	}

	triplet := secondaryBackendKey{backend: "onnx"}
	sw, err := o.reloadEntry(testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return next, nil
	}, reloadOpts{backend: &triplet, skipSpeciesIndex: true, threads: 1})

	require.Error(t, err, "a reload racing a full orchestrator delete must not report success")
	assert.False(t, sw)
	assert.Contains(t, err.Error(), "has been deleted")
	assert.Equal(t, int32(1), next.closes.Load(), "the freshly built instance must still be closed")
}
