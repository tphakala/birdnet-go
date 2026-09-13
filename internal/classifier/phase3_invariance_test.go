package classifier

// Phase 3 invariance harness. It pins the observable orchestrator surface for a set
// of fixed fixtures so the Phase 3 refactor (de-privileging the primary model: v2.4
// becomes an ordinary o.models entry, o.primary and the Primary* API are removed, one
// transactional reload path replaces the primary-only reload) can be proven
// behavior-identical. The goldens are recorded here against the CURRENT code
// (pre-PR-2), so every later PR is pinned to today's behavior.
//
// Every field the snapshot serializes is deterministic for a fixed fixture: labels are
// read from a fixed embedded locale, threads are pinned so thread allocation does not
// depend on the host CPU count, and RangeFilterStatusResponse.LastUpdated (the one
// wall-clock field) is zeroed before serialization. Goldens live under
// testdata/invariance/phase3_*.golden.json; regenerate them with
// UPDATE_PHASE3_GOLDEN=1 go test ./internal/classifier/ -run Phase3Invariance.
//
// The goldens are recorded on the default build (embedded TFLite v2.4). ModelInfos
// reports the live Backend/Quantization, which differs by platform and build tag, so
// the golden is default-build specific; the phase's tag matrix is go vet (compile
// only), and go test runs on the default build, so the golden is never exercised under
// a build that would report a different backend. Skipped when the embedded model is
// unavailable (noembed), like the other real-orchestrator tests.

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// phase3FixedThreads pins BirdNET.Threads so computeThreadAllocation does not fall
// back to runtime.NumCPU() (which would make ThreadAllocation host-specific).
const phase3FixedThreads = 4

// phase3FixedLocale pins the label locale so AllLabels and the species index are
// byte-stable across runs.
const phase3FixedLocale = "en-us"

// phase3Snapshot is the byte-comparable observable orchestrator surface pinned across
// the Phase 3 PRs of the model de-privilege epic.
type phase3Snapshot struct {
	LoadedIDs          []string       // sorted
	ModelInfos         []ModelInfo    // sorted by ID; full struct incl. Backend, Quantization, NumSpecies, Overlap
	DefaultTargetIDs   []string       // recorder uses PrimaryModelInfo().ID before PR 1's DefaultTargets() lands
	EngineDims         [3]int         // clipBytes, overlapBytes, readSize of the default target
	ThreadAllocation   map[string]int // per-model thread budget
	AllLabelsCount     int
	AllLabelsHead      []string // first 50 labels in order
	AllLabelsSHA256    string   // over the full ordered label list
	PublishedLabelsLen int      // len(published settings.BirdNET.Labels) after construction
	RangeFilterKind    string   // runtime state (active/fellBack) plus whether a universal geomodel view is loaded
	RangeFilterStatus  RangeFilterStatusResponse
	RarityFilterActive bool
	SpeciesIndexSHA256 string // over the published species-index snapshot
	ResolverChainLen   int
}

// phase3SnapshotDate is the fixed date used for range-filter and rarity reads so the
// snapshot never depends on the wall clock.
var phase3SnapshotDate = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

// buildPhase3Snapshot captures the observable orchestrator surface for o. It reads
// only pre-Phase-3 accessors so it can be recorded against pristine main; slices
// sourced from map-order-randomized producers (ModelInfos, LoadedIDs) are sorted here
// so the golden does not flap between runs.
func buildPhase3Snapshot(t *testing.T, o *Orchestrator) phase3Snapshot {
	t.Helper()

	infos := o.ModelInfos()
	slices.SortFunc(infos, func(a, b ModelInfo) int { return strings.Compare(a.ID, b.ID) })

	loadedIDs := make([]string, 0, len(infos))
	for i := range infos {
		loadedIDs = append(loadedIDs, infos[i].ID)
	}
	slices.Sort(loadedIDs)

	// Default target: the primary's live ModelInfo (PrimaryModelInfo already stamps the
	// effective overlap). PR 1 swaps this recorder to DefaultTargets() (identical value).
	primaryInfo := o.PrimaryModelInfo()
	var defaultTargetIDs []string
	var engineDims [3]int
	if primaryInfo.ID != "" {
		defaultTargetIDs = []string{primaryInfo.ID}
		clip, ov, read := primaryInfo.Spec.BufferDimensions(primaryInfo.Overlap)
		engineDims = [3]int{clip, ov, read}
	}

	labels := o.AllLabels()
	head := labels
	if len(head) > 50 {
		head = head[:50]
	}
	head = slices.Clone(head)

	status := o.RangeFilterStatus()
	status.LastUpdated = time.Time{} // wall-clock; zero it for a stable golden

	rc, err := o.GetRarityContext(phase3SnapshotDate)
	require.NoError(t, err)

	return phase3Snapshot{
		LoadedIDs:          loadedIDs,
		ModelInfos:         infos,
		DefaultTargetIDs:   defaultTargetIDs,
		EngineDims:         engineDims,
		ThreadAllocation:   o.computeThreadAllocation(o.CurrentSettings(), primaryInfo.ID),
		AllLabelsCount:     len(labels),
		AllLabelsHead:      head,
		AllLabelsSHA256:    sha256Strings(labels),
		PublishedLabelsLen: len(conf.GetSettings().BirdNET.Labels),
		RangeFilterKind:    o.phase3RangeFilterKind(),
		RangeFilterStatus:  status,
		RarityFilterActive: rc.FilterActive,
		SpeciesIndexSHA256: phase3SpeciesIndexSHA256(o),
		ResolverChainLen:   o.phase3ResolverChainLen(),
	}
}

// phase3RangeFilterKind composes a stable string from the range-filter runtime state
// and whether a universal geomodel view is loaded. The universal-view flag is what
// distinguishes an MData/legacy backend (false) from a geomodel backend (true), which
// is exactly the distinction scenario (e) pins ("no auto-select for the v2.4 anchor").
func (o *Orchestrator) phase3RangeFilterKind() string {
	o.mu.RLock()
	rfs := o.rangeFilter
	o.mu.RUnlock()
	if rfs == nil {
		return "active=false;fellBack=false;universal=false"
	}
	active, fellBack := rfs.runtimeState()
	_, universal := rfs.mappedView()
	return phase3KindString(active, fellBack, universal)
}

func phase3KindString(active, fellBack, universal bool) string {
	return "active=" + boolStr(active) + ";fellBack=" + boolStr(fellBack) + ";universal=" + boolStr(universal)
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}

// phase3ResolverChainLen returns the length of the name-resolver chain under o.mu,
// mirroring how ResolveName snapshots it.
func (o *Orchestrator) phase3ResolverChainLen() int {
	o.mu.RLock()
	defer o.mu.RUnlock()
	return len(o.nameResolvers)
}

// phase3SpeciesIndexSHA256 hashes the published species-index snapshot. encoding/json
// sorts map keys, and the slice fields (Labels and the per-canonical label groups) are
// produced in the deterministic AllLabels union order, so the hash is stable.
func phase3SpeciesIndexSHA256(o *Orchestrator) string {
	snap := o.SpeciesSnapshot()
	if snap == nil {
		return ""
	}
	data, err := json.Marshal(struct {
		SciToCommon       map[string]string
		SciToCommonFolded map[string]string
		CommonToSci       map[string]string
		CanonicalByLabel  map[string]string
		LabelsByCanonical map[string][]string
		LabelBySci        map[string]string
		Labels            []string
	}{
		SciToCommon:       snap.SciToCommon,
		SciToCommonFolded: snap.SciToCommonFolded,
		CommonToSci:       snap.CommonToSci,
		CanonicalByLabel:  snap.CanonicalByLabel,
		LabelsByCanonical: snap.LabelsByCanonical,
		LabelBySci:        snap.LabelBySci,
		Labels:            snap.Labels,
	})
	if err != nil {
		return "marshal-error:" + err.Error()
	}
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func sha256Strings(ss []string) string {
	h := sha256.New()
	for _, s := range ss {
		h.Write([]byte(s))
		h.Write([]byte{0}) // NUL delimiter so ["ab","c"] and ["a","bc"] differ
	}
	return hex.EncodeToString(h.Sum(nil))
}

// phase3BaseSettings returns a settings snapshot with the label locale and thread
// count pinned, published as the process-global snapshot (so construction writes the
// v2.4 labels into the same object conf.GetSettings() returns) and torn down on
// cleanup.
func phase3BaseSettings(t *testing.T) *conf.Settings {
	t.Helper()
	settings := conftest.GetTestSettings()
	settings.BirdNET.Locale = phase3FixedLocale
	settings.BirdNET.Threads = phase3FixedThreads
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })
	return settings
}

// phase3NewOrchestrator builds a real orchestrator on the embedded v2.4 model, or
// skips when the model is unavailable (noembed / missing model in the environment).
func phase3NewOrchestrator(t *testing.T, settings *conf.Settings) *Orchestrator {
	t.Helper()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: embedded model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })
	return o
}

// assertPhase3Golden compares the snapshot against the recorded golden, regenerating
// it when UPDATE_PHASE3_GOLDEN is set.
func assertPhase3Golden(t *testing.T, name string, snap *phase3Snapshot) {
	t.Helper()
	data, err := json.MarshalIndent(snap, "", "  ")
	require.NoError(t, err)
	data = append(data, '\n')

	path := filepath.Join("testdata", "invariance", "phase3_"+name+".golden.json")
	if os.Getenv("UPDATE_PHASE3_GOLDEN") != "" {
		require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
		require.NoError(t, os.WriteFile(path, data, 0o600))
		return
	}
	want, err := os.ReadFile(path) //nolint:gosec // fixed testdata path, not user input
	require.NoError(t, err, "golden missing for %q; regenerate with UPDATE_PHASE3_GOLDEN=1", name)
	assert.Equal(t, string(want), string(data), "phase 3 invariance golden drift for %q", name)
}

// TestPhase3NeutralAccessors_EquivalentToPrimary pins the three PR 1 equivalence
// claims on a real orchestrator: DefaultTargets()[0] == PrimaryModelInfo() and
// ResolvedModelPathForID(v24) == PrimaryResolvedModelPath(), plus the not-loaded
// zero-value cases. It exists only to prove the neutral accessors match what they
// shadow; PR 2 removes it together with the Primary* accessors it compares against.
func TestPhase3NeutralAccessors_EquivalentToPrimary(t *testing.T) {
	t.Parallel()

	settings := conftest.GetTestSettings()
	o, err := NewOrchestrator(settings)
	if err != nil {
		t.Skipf("Skipping: embedded model not available in test environment: %v", err)
	}
	t.Cleanup(func() { o.Delete() })

	dt := o.DefaultTargets()
	require.Len(t, dt, 1, "a loaded v2.4 yields exactly one default target")
	assert.Equal(t, o.PrimaryModelInfo(), dt[0], "DefaultTargets()[0] must equal PrimaryModelInfo()")

	assert.Equal(t, o.PrimaryResolvedModelPath(), o.ResolvedModelPathForID(RegistryIDBirdNETV24),
		"ResolvedModelPathForID(v24) must equal PrimaryResolvedModelPath()")

	// Not-loaded / zero-value cases.
	bare := &Orchestrator{}
	assert.Nil(t, bare.DefaultTargets(), "no primary yields no default targets")
	assert.Empty(t, bare.ResolvedModelPathForID(RegistryIDBirdNETV24), "not loaded resolves to empty")
	assert.Empty(t, o.ResolvedModelPathForID("nonexistent-id"), "unknown ID resolves to empty")
}

// TestDefaultTargets covers the neutral default-target accessor without the embedded
// model: not loaded yields nil, loaded yields the single PrimaryModelInfo().
func TestDefaultTargets(t *testing.T) {
	t.Parallel()

	bare := &Orchestrator{}
	assert.Nil(t, bare.DefaultTargets(), "no primary yields nil")

	want := ModelInfo{ID: RegistryIDBirdNETV24, Name: "BirdNET v2.4", Spec: ModelSpec{SampleRate: 48000}}
	o := &Orchestrator{ModelInfo: want}
	dt := o.DefaultTargets()
	require.Len(t, dt, 1)
	assert.Equal(t, o.PrimaryModelInfo(), dt[0], "the single default target is PrimaryModelInfo(), overlap stamped")
}

// TestResolvedModelPathForID covers the neutral resolved-path accessor with mock
// instances: a built-in (empty) path, a custom path, an unknown ID, and a bare
// orchestrator.
func TestResolvedModelPathForID(t *testing.T) {
	t.Parallel()

	o := newTestOrchestrator(t,
		&mockModelInstance{id: "builtin-model", resolvedPath: ""},
		&mockModelInstance{id: "custom-model", resolvedPath: "/models/custom.onnx"},
	)
	assert.Empty(t, o.ResolvedModelPathForID("builtin-model"), "built-in source resolves to empty")
	assert.Equal(t, "/models/custom.onnx", o.ResolvedModelPathForID("custom-model"))
	assert.Empty(t, o.ResolvedModelPathForID("not-loaded"), "an unloaded ID resolves to empty")

	bare := &Orchestrator{}
	assert.Empty(t, bare.ResolvedModelPathForID("anything"), "a bare orchestrator resolves to empty")
}

// TestPhase3Invariance_DefaultConfig pins scenario (a): the default configuration with
// only the embedded v2.4 model loaded.
func TestPhase3Invariance_DefaultConfig(t *testing.T) {
	// Not parallel: publishes the process-global settings snapshot.
	settings := phase3BaseSettings(t)
	o := phase3NewOrchestrator(t, settings)

	snap := buildPhase3Snapshot(t, o)
	assertPhase3Golden(t, "default", &snap)
}

// phase3Secondary describes a synthetic secondary model registered for the multi-model
// scenario: a registry template plus a stub loader that installs a labelled mock. Real
// Perch/v3.0/Bat loaders need ONNX files, so a stub keeps the golden hermetic while
// still driving the real construction and union/thread-allocation paths.
type phase3Secondary struct {
	id     string
	labels []string
}

// registerPhase3Secondary installs a synthetic secondary in the package-global
// ModelRegistry and modelLoaders maps for the duration of the test. The template is
// distinct from v2.4 (ONNX/FP32, 32 kHz/5 s) so ModelInfos pins a non-primary shape;
// the stub loader installs a mock with the given labels so AllLabels pins the union
// order across models. The config alias equals the ID, so settings.Models.Enabled can
// name it directly.
func registerPhase3Secondary(t *testing.T, sec phase3Secondary) {
	t.Helper()
	ModelRegistry[sec.id] = ModelInfo{
		ID:            sec.id,
		Name:          "mock-" + sec.id,
		Backend:       BackendONNX,
		Quantization:  "FP32",
		Spec:          ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		ConfigAliases: []string{sec.id},
		NumSpecies:    len(sec.labels),
	}
	t.Cleanup(func() { delete(ModelRegistry, sec.id) })

	labels := slices.Clone(sec.labels)
	id := sec.id
	modelLoaders[id] = func(orc *Orchestrator, _ int) error {
		orc.models[id] = &modelEntry{instance: &mockModelInstance{
			id:         id,
			labels:     labels,
			numSpecies: len(labels),
			spec:       ModelSpec{SampleRate: 32000, ClipLength: 5 * time.Second},
		}}
		return nil
	}
	t.Cleanup(func() { delete(modelLoaders, id) })
}

// TestPhase3Invariance_MultiModel pins scenario (b): v2.4 plus three enabled
// secondaries loaded through the modelLoaders injection seam, pinning the multi-model
// union order, thread allocation, and per-model ModelInfos (v2.4 live, secondaries
// from the registry template).
func TestPhase3Invariance_MultiModel(t *testing.T) {
	// Not parallel: publishes global settings and mutates the package-global registry.
	secondaries := []phase3Secondary{
		// Unique labels not present in the v2.4 set, so the union order among
		// secondaries (byte-sorted after the anchor) is observable in AllLabelsSHA256.
		{id: "Phase3SecA", labels: []string{"Testus alpha_Phase3 Test Alpha"}},
		{id: "Phase3SecB", labels: []string{"Testus beta_Phase3 Test Beta"}},
		{id: "Phase3SecC", labels: []string{"Testus gamma_Phase3 Test Gamma"}},
	}
	for _, sec := range secondaries {
		registerPhase3Secondary(t, sec)
	}

	settings := phase3BaseSettings(t)
	settings.Models.Enabled = []string{"birdnet", "Phase3SecA", "Phase3SecB", "Phase3SecC"}

	o := phase3NewOrchestrator(t, settings)

	snap := buildPhase3Snapshot(t, o)
	assertPhase3Golden(t, "multimodel", &snap)
}

// TestPhase3Invariance_LocaleReload pins scenario (c): a reload_birdnet-style locale
// change (ReloadModel, BuildRangeFilter, ReloadSecondaryModels). It asserts the labels
// re-localize (so the reload genuinely reloaded) while the published label slice length
// is unchanged, and records the post-reload surface.
func TestPhase3Invariance_LocaleReload(t *testing.T) {
	// Not parallel: publishes the process-global settings snapshot.
	settings := phase3BaseSettings(t)
	o := phase3NewOrchestrator(t, settings)

	before := buildPhase3Snapshot(t, o)

	// Change locale in place: the instance holds this exact pointer and it is the
	// published snapshot, so ReloadModel picks up the new locale (as the settings-save
	// path does in production before emitting reload_birdnet).
	settings.BirdNET.Locale = "fr"
	require.NoError(t, o.ReloadModel())
	require.NoError(t, BuildRangeFilter(o))
	require.NoError(t, o.ReloadSecondaryModels())

	after := buildPhase3Snapshot(t, o)
	assert.NotEqual(t, before.AllLabelsSHA256, after.AllLabelsSHA256, "labels must re-localize on a locale reload")
	assert.Equal(t, before.AllLabelsCount, after.AllLabelsCount, "the label count must not change across a locale reload")
	assert.Equal(t, before.PublishedLabelsLen, after.PublishedLabelsLen, "the published label slice length must not change")

	assertPhase3Golden(t, "locale_reload", &after)
}

// TestPhase3Invariance_RefusedReload pins scenario (d): a settings hot-reload that
// resolves to an unknown model identity is refused, the previously loaded instance is
// rolled back and keeps serving, and the observable surface is unchanged. The exact
// per-refusal error texts are pinned by PR 3's dedicated reload-path tests; here PR 1
// only pins that a refused reload keeps serving.
func TestPhase3Invariance_RefusedReload(t *testing.T) {
	// Not parallel: publishes the process-global settings snapshot.
	settings := phase3BaseSettings(t)
	o := phase3NewOrchestrator(t, settings)

	before := buildPhase3Snapshot(t, o)

	// An unknown birdnet.version on a plain settings reload is refused and rolled back.
	settings.BirdNET.Version = "9.9-nonexistent"
	require.Error(t, o.ReloadModel(), "an unknown model version on a settings reload must be refused")

	after := buildPhase3Snapshot(t, o)
	assert.Equal(t, before, after, "a refused reload must leave the observable surface unchanged")
}

// TestPhase3Invariance_ModelsDirNoAutoSelect pins scenario (e): with an empty
// range-filter model path, pointing the models dir at a directory does NOT auto-select
// the geomodel for the v2.4 anchor. The auto-select gate requires geomodel
// range-filter compatibility (birdnet.go), and v2.4 is MData-compatible, so file
// presence in the models dir is irrelevant for the v2.4 anchor; the range filter stays
// the embedded MData backend (universal=false).
func TestPhase3Invariance_ModelsDirNoAutoSelect(t *testing.T) {
	// Not parallel: publishes the process-global settings snapshot.
	settings := phase3BaseSettings(t)
	o := phase3NewOrchestrator(t, settings)

	before := buildPhase3Snapshot(t, o)

	o.SetModelsDir(t.TempDir())
	require.NoError(t, o.ReloadRangeFilter())

	after := buildPhase3Snapshot(t, o)
	assert.Equal(t, before.RangeFilterKind, after.RangeFilterKind, "the v2.4 anchor must not auto-select the geomodel from the models dir")
	assert.Contains(t, after.RangeFilterKind, "universal=false", "the v2.4 anchor stays on the MData backend")

	assertPhase3Golden(t, "modelsdir_noautoselect", &after)
}
