package classifier

import (
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/openfauna"
	"github.com/tphakala/birdnet-go/internal/speciesindex"
)

// newSpeciesIndexTestOrchestrator builds an Orchestrator wired with the
// orchestrator-owned species-name index and its OpenFauna resolver, seeded from
// the given mock models, without any real model files. It performs the initial
// rebuild so SpeciesSnapshot() reflects the mocks' labels immediately.
func newSpeciesIndexTestOrchestrator(t *testing.T, mocks ...*mockModelInstance) *Orchestrator {
	t.Helper()
	models := make(map[string]*modelEntry, len(mocks))
	for _, m := range mocks {
		models[m.id] = &modelEntry{instance: m}
	}
	of := openfauna.NewResolver()
	o := &Orchestrator{
		models:    models,
		modelRSS:  make(map[string]int64),
		openfauna: of,
		names:     speciesindex.New(of),
	}
	s := &conf.Settings{}
	s.BirdNET.Locale = "en"
	o.updateSettings(s)
	o.rebuildSpeciesIndex()
	return o
}

func TestSpeciesSnapshot_NeverNil(t *testing.T) {
	t.Parallel()

	var nilOrch *Orchestrator
	assert.NotNil(t, nilOrch.SpeciesSnapshot(), "nil receiver returns Empty()")
	assert.Nil(t, nilOrch.SpeciesIndex(), "nil receiver has no service")

	bare := &Orchestrator{}
	assert.NotNil(t, bare.SpeciesSnapshot(), "bare struct returns Empty()")
	assert.Nil(t, bare.SpeciesIndex(), "bare struct has no service")

	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: "M", labels: []string{"Turdus merula_Common Blackbird"}})
	require.NotNil(t, o.SpeciesIndex(), "constructed orchestrator owns a service")
	assert.NotNil(t, o.SpeciesSnapshot())
}

func TestSpeciesIndex_RebuiltOnLoadModel(t *testing.T) {
	// Mutates package-global ModelRegistry/modelLoaders, so not parallel.
	const testID = "SpeciesIndex_LoadTrigger"
	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID, labels: []string{"Cyanistes caeruleus_Eurasian Blue Tit"}})

	// Precondition: the loaded model's species is not yet indexed.
	_, present := o.SpeciesSnapshot().LabelBySci["Turdus merula"]
	require.False(t, present, "species must be absent before the model is loaded")

	ModelRegistry[testID] = ModelInfo{ID: testID}
	t.Cleanup(func() { delete(ModelRegistry, testID) })
	modelLoaders[testID] = func(orc *Orchestrator, _ int) error {
		orc.models[testID] = &modelEntry{instance: &mockModelInstance{id: testID, labels: []string{"Turdus merula_Common Blackbird"}}}
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, testID) })

	require.NoError(t, o.LoadModel(testID))

	snap := o.SpeciesSnapshot()
	assert.Equal(t, "Turdus merula_Common Blackbird", snap.LabelBySci["Turdus merula"], "loaded model's species must be indexed after LoadModel")
	assert.Equal(t, o.AllLabels(), snap.Labels, "snapshot Labels must equal the label union")
}

func TestSpeciesIndex_RebuiltOnUnloadModel(t *testing.T) {
	// Mutates package-global ModelRegistry, so not parallel.
	const secondary = "SpeciesIndex_UnloadTrigger"
	ModelRegistry[secondary] = ModelInfo{ID: secondary}
	t.Cleanup(func() { delete(ModelRegistry, secondary) })

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: permanentRegistryID, labels: []string{"Cyanistes caeruleus_Eurasian Blue Tit"}},
		&mockModelInstance{id: secondary, labels: []string{"Turdus merula_Common Blackbird"}},
	)

	require.Equal(t, "Turdus merula_Common Blackbird", o.SpeciesSnapshot().LabelBySci["Turdus merula"], "secondary species indexed before unload")

	require.NoError(t, o.UnloadModel(secondary))

	snap := o.SpeciesSnapshot()
	_, present := snap.LabelBySci["Turdus merula"]
	assert.False(t, present, "unloaded model's species must be gone from the index")
	assert.Equal(t, "Cyanistes caeruleus_Eurasian Blue Tit", snap.LabelBySci["Cyanistes caeruleus"], "surviving model's species stays indexed")
	assert.Equal(t, o.AllLabels(), snap.Labels, "snapshot Labels must equal the reduced label union")
}

func TestSpeciesIndex_RebuiltOnReloadSecondaryModels(t *testing.T) {
	// Mutates package-global builder map and settings, so not parallel.
	setGlobalBackend(t, "openvino", "gpu", "/opt/ov")

	of := openfauna.NewResolver()
	o := &Orchestrator{
		models:    map[string]*modelEntry{permanentRegistryID: {instance: &mockModelInstance{id: permanentRegistryID}}},
		modelRSS:  make(map[string]int64),
		openfauna: of,
		names:     speciesindex.New(of),
	}
	o.ModelInfo.ID = permanentRegistryID
	// Loaded on a different backend so the per-entry gate fires and the swap runs.
	o.models[testSecondaryID] = &modelEntry{instance: &reloadFakeModel{id: testSecondaryID}, backend: secondaryBackendKey{backend: "onnx"}}
	registerTestSecondaryBuilder(t, testSecondaryID, func(_ *Orchestrator, _ *conf.Settings, _ int) (ModelInstance, error) {
		return &reloadFakeModel{id: testSecondaryID}, nil
	})
	o.rebuildSpeciesIndex()

	before := o.SpeciesSnapshot()
	require.NoError(t, o.ReloadSecondaryModels())
	after := o.SpeciesSnapshot()
	assert.NotSame(t, before, after, "a secondary swap must republish the species-name index")
}

func TestSpeciesIndex_RebuiltOnRebuildNameResolver(t *testing.T) {
	t.Parallel()

	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID, labels: []string{"Turdus merula_Common Blackbird"}})
	before := o.SpeciesSnapshot()
	require.NoError(t, o.RebuildNameResolver(nil))
	after := o.SpeciesSnapshot()
	assert.NotSame(t, before, after, "RebuildNameResolver must republish the species-name index")
	assert.Equal(t, "Turdus merula_Common Blackbird", after.LabelBySci["Turdus merula"])
}

// TestSpeciesIndex_EqualsLegacySeed is the I1 pin for units B and C: the
// orchestrator's published snapshot must equal a snapshot built the legacy way,
// from AllLabels() with the same resolver and locale.
func TestSpeciesIndex_EqualsLegacySeed(t *testing.T) {
	t.Parallel()

	o := newSpeciesIndexTestOrchestrator(t,
		&mockModelInstance{id: permanentRegistryID, labels: []string{"Turdus merula_Common Blackbird"}},
		&mockModelInstance{id: "Perch_like", labels: []string{"Cyanistes caeruleus_Eurasian Blue Tit", "Spilopelia senegalensis_Laughing Dove"}},
	)

	locale := o.CurrentSettings().BirdNET.Locale
	legacy := speciesindex.New(o.openfauna)
	legacy.Rebuild(o.AllLabels(), locale)

	want := legacy.Snapshot()
	got := o.SpeciesSnapshot()
	assert.Equal(t, want.Labels, got.Labels)
	assert.Equal(t, want.SciToCommon, got.SciToCommon)
	assert.Equal(t, want.SciToCommonFolded, got.SciToCommonFolded)
	assert.Equal(t, want.CommonToSci, got.CommonToSci)
	assert.Equal(t, want.CanonicalByLabel, got.CanonicalByLabel)
	assert.Equal(t, want.LabelsByCanonical, got.LabelsByCanonical)
	assert.Equal(t, want.LabelBySci, got.LabelBySci)
	assert.Equal(t, want.Locale, got.Locale)
}

// TestRebuildNameResolver_UnionSeedsSecondaryLabels verifies a secondary model's
// species is pre-indexed in the OpenFauna resolver after RebuildNameResolver, so
// it no longer falls to the on-demand Lookup path.
func TestRebuildNameResolver_UnionSeedsSecondaryLabels(t *testing.T) {
	t.Parallel()

	// A real species carried only by a "secondary" model (no primary here).
	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: "Secondary", labels: []string{"Turdus merula_Common Blackbird"}})
	require.NoError(t, o.RebuildNameResolver(nil))

	_, ok := o.openfauna.ResolveLocal("Turdus merula")
	assert.True(t, ok, "a loaded model's species must be pre-indexed in the resolver working set")
}

// TestRebuildNameResolver_InclusionListIsIncluded verifies an inclusion-list
// species absent from every model is still pre-indexed.
func TestRebuildNameResolver_InclusionListIsIncluded(t *testing.T) {
	t.Parallel()

	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: "M", labels: []string{"Cyanistes caeruleus_Eurasian Blue Tit"}})
	require.NoError(t, o.RebuildNameResolver([]string{"Turdus merula_Common Blackbird"}))

	_, ok := o.openfauna.ResolveLocal("Turdus merula")
	assert.True(t, ok, "an inclusion-list species must be pre-indexed even when no model carries it")
	_, ok2 := o.openfauna.ResolveLocal("Cyanistes caeruleus")
	assert.True(t, ok2, "the model's own species stays pre-indexed alongside the inclusion list")
}

// TestSpeciesIndex_LocaleChangeReflectsResolver verifies the published snapshot's
// localized names follow the resolver's locale after RebuildNameResolver. Uses
// the real vendored OpenFauna dataset, so it is skipped under -short.
func TestSpeciesIndex_LocaleChangeReflectsResolver(t *testing.T) {
	if testing.Short() {
		t.Skip("uses the real OpenFauna dataset")
	}
	t.Parallel()

	of := openfauna.NewResolver()
	o := &Orchestrator{
		models:    map[string]*modelEntry{"M": {instance: &mockModelInstance{id: "M", labels: []string{"Turdus merula_Common Blackbird"}}}},
		modelRSS:  make(map[string]int64),
		openfauna: of,
		names:     speciesindex.New(of),
	}
	en := &conf.Settings{}
	en.BirdNET.Locale = "en"
	o.updateSettings(en)
	require.NoError(t, o.RebuildNameResolver(nil))
	enName := o.SpeciesSnapshot().SciToCommon["Turdus merula"]
	require.NotEmpty(t, enName, "English common name must resolve")

	fi := &conf.Settings{}
	fi.BirdNET.Locale = "fi"
	o.updateSettings(fi)
	require.NoError(t, o.RebuildNameResolver(nil))
	fiName := o.SpeciesSnapshot().SciToCommon["Turdus merula"]
	require.NotEmpty(t, fiName, "Finnish common name must resolve")

	// Do not hard-code the exact string (the dataset is refreshed on main);
	// assert only that the localized name changed with the locale.
	assert.NotEqual(t, enName, fiName, "snapshot common name must follow the resolver's locale")
}

// TestSpeciesIndex_ConcurrentReadersDuringLoadUnload runs readers against the
// snapshot while a single writer loops LoadModel/UnloadModel. Run with -race.
func TestSpeciesIndex_ConcurrentReadersDuringLoadUnload(t *testing.T) {
	const testID = "SpeciesIndex_ConcurrentLoadUnload"
	ModelRegistry[testID] = ModelInfo{ID: testID}
	t.Cleanup(func() { delete(ModelRegistry, testID) })
	modelLoaders[testID] = func(orc *Orchestrator, _ int) error {
		orc.models[testID] = &modelEntry{instance: &mockModelInstance{id: testID, labels: []string{"Turdus merula_Common Blackbird"}}}
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, testID) })

	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID, labels: []string{"Cyanistes caeruleus_Eurasian Blue Tit"}})

	stop := make(chan struct{})
	var wg sync.WaitGroup
	for range 8 {
		wg.Go(func() {
			for {
				select {
				case <-stop:
					return
				default:
					snap := o.SpeciesSnapshot()
					_ = snap.LabelBySci["Turdus merula"]
					_ = snap.SciToCommon["Cyanistes caeruleus"]
					for range snap.Labels {
					}
				}
			}
		})
	}

	for range 200 {
		require.NoError(t, o.LoadModel(testID))
		require.NoError(t, o.UnloadModel(testID))
	}
	close(stop)
	wg.Wait()

	// The permanent model's species is always present regardless of the final race.
	assert.Equal(t, "Cyanistes caeruleus_Eurasian Blue Tit", o.SpeciesSnapshot().LabelBySci["Cyanistes caeruleus"])
}

// TestSpeciesIndex_ConcurrentTriggersPublishNewest pins the rebuildMu guarantee:
// with LoadModel(A) and RebuildNameResolver racing, the final published snapshot
// must contain A rather than a stale union that blocked behind a newer one.
func TestSpeciesIndex_ConcurrentTriggersPublishNewest(t *testing.T) {
	const testID = "SpeciesIndex_ConcurrentPublish"
	ModelRegistry[testID] = ModelInfo{ID: testID}
	t.Cleanup(func() { delete(ModelRegistry, testID) })
	modelLoaders[testID] = func(orc *Orchestrator, _ int) error {
		orc.models[testID] = &modelEntry{instance: &mockModelInstance{id: testID, labels: []string{"Turdus merula_Common Blackbird"}}}
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, testID) })

	o := newSpeciesIndexTestOrchestrator(t, &mockModelInstance{id: permanentRegistryID, labels: []string{"Cyanistes caeruleus_Eurasian Blue Tit"}})

	var wg sync.WaitGroup
	wg.Go(func() { require.NoError(t, o.LoadModel(testID)) })
	wg.Go(func() { require.NoError(t, o.RebuildNameResolver(nil)) })
	wg.Wait()

	assert.Equal(t, "Turdus merula_Common Blackbird", o.SpeciesSnapshot().LabelBySci["Turdus merula"],
		"the last published snapshot must reflect the loaded model")
}
