package classifier

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// Mock model IDs for the model-species tests. They are deliberately not registry
// IDs: any entry with a live instance takes part in the snapshot whatever its ID;
// only the v2.4 anchor slot is read through the primary pointer.
const (
	modelSpeciesIDA = "ModelSpecies_A"
	modelSpeciesIDB = "ModelSpecies_B"

	modelSpeciesBlackbird = "Turdus merula_Common Blackbird"
	modelSpeciesGreatTit  = "Parus major"
	modelSpeciesDove      = "Streptopelia senegalensis_African Collared Dove"
	modelSpeciesDoveAlias = "Spilopelia senegalensis"
	modelSpeciesBlueTit   = "Cyanistes caeruleus_Eurasian Blue Tit"
	modelSpeciesSoundCls  = "Power_tool"

	// modelSpeciesReaders is the number of goroutines reading ModelSpecies while the
	// topology changes in the concurrency test.
	modelSpeciesReaders = 8
)

// labelsHookModel is a mock model that runs onLabels each time Labels is called, so
// a test can change the topology while ModelSpecies is walking the models.
type labelsHookModel struct {
	*mockModelInstance
	onLabels func()
}

func (m *labelsHookModel) Labels() []string {
	if m.onLabels != nil {
		m.onLabels()
	}
	return m.mockModelInstance.Labels()
}

// knows reports whether the snapshot says the model can classify the species named
// by label, keyed the way the pending-detection map keys it.
func knows(ms *ModelSpecies, modelID, label string) bool {
	return ms.Knows(modelID, speciesindex.CanonicalKey(label))
}

func TestModelSpecies_PerModelCanonicalKeys(t *testing.T) {
	t.Parallel()

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird, modelSpeciesGreatTit}},
		&mockModelInstance{id: modelSpeciesIDB, labels: []string{modelSpeciesSoundCls, modelSpeciesDove}},
	)

	ms := o.ModelSpecies()
	require.NotNil(t, ms)

	assert.True(t, ms.HasModel(modelSpeciesIDA))
	assert.True(t, ms.HasModel(modelSpeciesIDB))
	assert.False(t, ms.HasModel("ModelSpecies_Unloaded"), "an unloaded ID has no set")

	assert.True(t, knows(ms, modelSpeciesIDA, modelSpeciesBlackbird))
	assert.True(t, knows(ms, modelSpeciesIDA, modelSpeciesGreatTit))
	assert.False(t, knows(ms, modelSpeciesIDA, modelSpeciesSoundCls), "model A must not know B's species")
	assert.False(t, knows(ms, modelSpeciesIDA, modelSpeciesDove), "model A must not know B's species")

	assert.True(t, knows(ms, modelSpeciesIDB, modelSpeciesSoundCls))
	assert.True(t, knows(ms, modelSpeciesIDB, modelSpeciesDove))
	assert.False(t, knows(ms, modelSpeciesIDB, modelSpeciesBlackbird), "model B must not know A's species")
	assert.False(t, knows(ms, modelSpeciesIDB, modelSpeciesGreatTit), "model B must not know A's species")

	// The legacy label is found under the current accepted name, the key the
	// pending-detection map uses, and case does not matter.
	assert.True(t, knows(ms, modelSpeciesIDB, modelSpeciesDoveAlias))
	assert.True(t, knows(ms, modelSpeciesIDA, "TURDUS MERULA"))

	assert.False(t, knows(ms, "ModelSpecies_Unloaded", modelSpeciesBlackbird))
	assert.False(t, ms.Knows(modelSpeciesIDA, ""), "the empty key is never known")
}

// TestModelSpecies_IncludesBirdNETAnchor pins that the v2.4 model, which the walk
// reads through the orderedEntryRefs primary pointer rather than its map entry, is
// still present in the snapshot under its registry ID.
func TestModelSpecies_IncludesBirdNETAnchor(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.BirdNET.Labels = []string{modelSpeciesBlackbird}
	o := newTestOrchestrator(t, &mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesGreatTit}})
	o.models[RegistryIDBirdNETV24] = &modelEntry{instance: &BirdNET{Settings: settings}}

	ms := o.ModelSpecies()
	assert.True(t, ms.HasModel(RegistryIDBirdNETV24))
	assert.True(t, knows(ms, RegistryIDBirdNETV24, modelSpeciesBlackbird))
	assert.False(t, knows(ms, RegistryIDBirdNETV24, modelSpeciesGreatTit))
	assert.True(t, knows(ms, modelSpeciesIDA, modelSpeciesGreatTit))
}

func TestModelSpecies_NilSafe(t *testing.T) {
	t.Parallel()

	var ms *ModelSpecies
	assert.Equal(t, uint64(0), ms.Generation())
	assert.False(t, ms.HasModel(modelSpeciesIDA))
	assert.False(t, ms.Knows(modelSpeciesIDA, "turdus merula"))

	var nilOrch *Orchestrator
	assert.Nil(t, nilOrch.ModelSpecies())
}

func TestModelSpecies_TracksTopologyTransitions(t *testing.T) {
	// Mutates package-global ModelRegistry/modelLoaders, so not parallel.
	const registryID = "ModelSpecies_Transitions"
	setTestGlobalSettings(t)
	registerPhase3Secondary(t, phase3Secondary{id: registryID, labels: []string{modelSpeciesGreatTit}})

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird}})
	blackbird := speciesindex.CanonicalKey(modelSpeciesBlackbird)
	greatTit := speciesindex.CanonicalKey(modelSpeciesGreatTit)
	blueTit := speciesindex.CanonicalKey(modelSpeciesBlueTit)

	first := o.ModelSpecies()
	assert.True(t, first.HasModel(modelSpeciesIDA))
	assert.False(t, first.HasModel(registryID))
	assert.Same(t, first, o.ModelSpecies(), "no topology change must hit the cache")

	// Load.
	require.NoError(t, o.LoadModel(registryID))
	afterLoad := o.ModelSpecies()
	assert.NotSame(t, first, afterLoad)
	assert.Greater(t, afterLoad.Generation(), first.Generation())
	assert.True(t, afterLoad.HasModel(modelSpeciesIDA))
	assert.True(t, afterLoad.Knows(modelSpeciesIDA, blackbird))
	assert.True(t, afterLoad.HasModel(registryID))
	assert.True(t, afterLoad.Knows(registryID, greatTit))
	assert.Same(t, afterLoad, o.ModelSpecies())

	// Reload with a different label set, through reloadEntry.
	swapped, err := o.reloadEntry(registryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return &mockModelInstance{id: registryID, labels: []string{modelSpeciesBlueTit}}, nil
	}, reloadOpts{threads: 1})
	require.NoError(t, err)
	require.True(t, swapped)
	afterReload := o.ModelSpecies()
	assert.Greater(t, afterReload.Generation(), afterLoad.Generation())
	assert.True(t, afterReload.Knows(registryID, blueTit))
	assert.False(t, afterReload.Knows(registryID, greatTit), "the reloaded model's old labels are gone")

	// Unload.
	require.NoError(t, o.UnloadModel(registryID))
	afterUnload := o.ModelSpecies()
	assert.Greater(t, afterUnload.Generation(), afterReload.Generation())
	assert.False(t, afterUnload.HasModel(registryID))
	assert.False(t, afterUnload.Knows(registryID, blueTit))
	assert.True(t, afterUnload.Knows(modelSpeciesIDA, blackbird))
}

// TestModelSpecies_GenerationReadBeforeWalk pins that a topology change landing
// during the walk leaves the snapshot stamped with the older generation, so the next
// call rebuilds instead of trusting a set that may predate the change.
func TestModelSpecies_GenerationReadBeforeWalk(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t)
	var once sync.Once
	hook := &labelsHookModel{
		mockModelInstance: &mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird}},
		onLabels:          func() { once.Do(func() { o.topologyGen.Add(1) }) },
	}
	o.models[modelSpeciesIDA] = &modelEntry{instance: hook}

	first := o.ModelSpecies()
	second := o.ModelSpecies()
	assert.NotSame(t, first, second, "a change during the walk must force a rebuild")
	assert.Greater(t, second.Generation(), first.Generation())
	assert.Same(t, second, o.ModelSpecies(), "a quiet topology hits the cache again")
}

// TestModelSpecies_GenerationBumps pins the bump sites that the transition test does
// not reach: a bare-struct orchestrator without a species index, the secondary reload
// trigger, and the name-resolver rebuild, which is not a topology change.
func TestModelSpecies_GenerationBumps(t *testing.T) {
	// Mutates package-global builder map and settings, so not parallel.
	t.Run("bare struct without an index", func(t *testing.T) {
		bare := &Orchestrator{}
		before := bare.topologyGen.Load()
		bare.rebuildSpeciesIndex()
		assert.Greater(t, bare.topologyGen.Load(), before)
	})

	t.Run("name-resolver rebuild is not a topology change", func(t *testing.T) {
		o := newSpeciesIndexTestOrchestrator(t,
			&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird}})
		before := o.ModelSpecies()
		require.NoError(t, o.RebuildNameResolver(nil))
		assert.Same(t, before, o.ModelSpecies())
	})

	t.Run("secondary reload", func(t *testing.T) {
		setGlobalBackend(t, "openvino", "gpu", "/opt/ov")
		o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: RegistryIDBirdNETV24})
		o.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}, backend: secondaryBackendKey{backend: "onnx"}}
		registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
			return &reloadFakeModel{id: testSecondaryID}, nil
		})
		before := o.ModelSpecies()
		require.NoError(t, o.ReloadSecondaryModels())
		assert.Greater(t, o.ModelSpecies().Generation(), before.Generation())
	})
}

func TestModelSpecies_InvalidatedByDelete(t *testing.T) {
	// Not parallel: it publishes global settings.
	setTestGlobalSettings(t)
	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird}})
	before := o.ModelSpecies()
	require.True(t, before.HasModel(modelSpeciesIDA))

	o.Delete()
	assert.Nil(t, o.modelSpecies.Load(), "Delete must drop the cached snapshot")

	after := o.ModelSpecies()
	assert.Greater(t, after.Generation(), before.Generation())
	assert.False(t, after.HasModel(modelSpeciesIDA), "a deleted orchestrator has no loaded models")
}

func TestModelSpecies_ConcurrentWithTopologyChanges(t *testing.T) {
	// Mutates package-global ModelRegistry/modelLoaders, so not parallel.
	const registryID = "ModelSpecies_Concurrent"
	registerPhase3Secondary(t, phase3Secondary{id: registryID, labels: []string{modelSpeciesGreatTit}})

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird}})
	blackbird := speciesindex.CanonicalKey(modelSpeciesBlackbird)

	var stop atomic.Bool
	var wg sync.WaitGroup
	for range modelSpeciesReaders {
		wg.Go(func() {
			for !stop.Load() {
				assert.True(t, o.ModelSpecies().Knows(modelSpeciesIDA, blackbird), "the permanent model is always known")
			}
		})
	}

	for range loadUnloadCycles {
		require.NoError(t, o.LoadModel(registryID))
		require.NoError(t, o.UnloadModel(registryID))
	}
	require.NoError(t, o.LoadModel(registryID))
	stop.Store(true)
	wg.Wait()

	final := o.ModelSpecies()
	assert.True(t, final.HasModel(registryID), "the final snapshot matches the final topology")
	assert.True(t, final.Knows(registryID, speciesindex.CanonicalKey(modelSpeciesGreatTit)))
	assert.True(t, final.Knows(modelSpeciesIDA, blackbird))
}
