package classifier

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// Mock model IDs for the model-species tests. They are deliberately not registry
// IDs: the walk must not branch on a model's identity.
const (
	modelSpeciesIDA = "ModelSpecies_A"
	modelSpeciesIDB = "ModelSpecies_B"

	modelSpeciesBlackbird = "Turdus merula_Common Blackbird"
	modelSpeciesGreatTit  = "Parus major"
	modelSpeciesDove      = "Streptopelia senegalensis_African Collared Dove"
	modelSpeciesDoveAlias = "Spilopelia senegalensis"
	modelSpeciesBlueTit   = "Cyanistes caeruleus_Eurasian Blue Tit"
	modelSpeciesSoundCls  = "Power_tool"
)

func TestModelSpecies_PerModelCanonicalKeys(t *testing.T) {
	t.Parallel()

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird, modelSpeciesGreatTit}},
		&mockModelInstance{id: modelSpeciesIDB, labels: []string{modelSpeciesSoundCls, modelSpeciesDove}},
	)

	ms := o.ModelSpecies()
	require.NotNil(t, ms)

	blackbird := speciesindex.CanonicalKey(modelSpeciesBlackbird)
	greatTit := speciesindex.CanonicalKey(modelSpeciesGreatTit)
	sound := speciesindex.CanonicalKey(modelSpeciesSoundCls)
	dove := speciesindex.CanonicalKey(modelSpeciesDove)
	require.NotEmpty(t, blackbird)
	require.NotEmpty(t, greatTit)
	require.NotEmpty(t, sound)
	require.NotEmpty(t, dove)

	assert.True(t, ms.HasModel(modelSpeciesIDA))
	assert.True(t, ms.HasModel(modelSpeciesIDB))
	assert.False(t, ms.HasModel("ModelSpecies_Unloaded"), "an unloaded ID has no set")

	assert.True(t, ms.Knows(modelSpeciesIDA, blackbird))
	assert.True(t, ms.Knows(modelSpeciesIDA, greatTit))
	assert.False(t, ms.Knows(modelSpeciesIDA, sound), "model A must not know B's species")
	assert.False(t, ms.Knows(modelSpeciesIDA, dove), "model A must not know B's species")

	assert.True(t, ms.Knows(modelSpeciesIDB, sound))
	assert.True(t, ms.Knows(modelSpeciesIDB, dove))
	assert.False(t, ms.Knows(modelSpeciesIDB, blackbird), "model B must not know A's species")
	assert.False(t, ms.Knows(modelSpeciesIDB, greatTit), "model B must not know A's species")

	// The legacy label is found under the current accepted name, the key the
	// pending-detection map uses, and case does not matter.
	assert.True(t, ms.Knows(modelSpeciesIDB, speciesindex.CanonicalKey(modelSpeciesDoveAlias)))
	assert.True(t, ms.Knows(modelSpeciesIDA, speciesindex.CanonicalKey("TURDUS MERULA")))

	assert.False(t, ms.Knows("ModelSpecies_Unloaded", blackbird))
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
	assert.True(t, ms.Knows(RegistryIDBirdNETV24, speciesindex.CanonicalKey(modelSpeciesBlackbird)))
	assert.False(t, ms.Knows(RegistryIDBirdNETV24, speciesindex.CanonicalKey(modelSpeciesGreatTit)))
	assert.True(t, ms.Knows(modelSpeciesIDA, speciesindex.CanonicalKey(modelSpeciesGreatTit)))
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
	ModelRegistry[registryID] = ModelInfo{ID: registryID}
	t.Cleanup(func() { delete(ModelRegistry, registryID) })

	modelLoaders[registryID] = func(orc *Orchestrator, _ int) error {
		orc.models[registryID] = &modelEntry{instance: &mockModelInstance{id: registryID, labels: []string{modelSpeciesGreatTit}}}
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, registryID) })

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

func TestModelSpecies_ConcurrentWithTopologyChanges(t *testing.T) {
	// Mutates package-global ModelRegistry/modelLoaders, so not parallel.
	const registryID = "ModelSpecies_Concurrent"
	ModelRegistry[registryID] = ModelInfo{ID: registryID}
	t.Cleanup(func() { delete(ModelRegistry, registryID) })
	modelLoaders[registryID] = func(orc *Orchestrator, _ int) error {
		orc.models[registryID] = &modelEntry{instance: &mockModelInstance{id: registryID, labels: []string{modelSpeciesGreatTit}}}
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, registryID) })

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: modelSpeciesIDA, labels: []string{modelSpeciesBlackbird}})
	blackbird := speciesindex.CanonicalKey(modelSpeciesBlackbird)

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					ms := o.ModelSpecies()
					_ = ms.Generation()
					_ = ms.HasModel(registryID)
					assert.True(t, ms.Knows(modelSpeciesIDA, blackbird), "the permanent model is always known")
				}
			}
		})
	}

	for range loadUnloadCycles {
		require.NoError(t, o.LoadModel(registryID))
		require.NoError(t, o.UnloadModel(registryID))
	}
	require.NoError(t, o.LoadModel(registryID))
	close(stop)
	wg.Wait()

	final := o.ModelSpecies()
	assert.True(t, final.HasModel(registryID), "the final snapshot matches the final topology")
	assert.True(t, final.Knows(registryID, speciesindex.CanonicalKey(modelSpeciesGreatTit)))
	assert.True(t, final.Knows(modelSpeciesIDA, blackbird))
}
