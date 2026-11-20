package classifier

import (
	"math"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// newTestBirdNETV3 builds a BirdNETV3 instance around a fake classifier, with the
// model metadata sourced from the registry (as NewBirdNETV3 does at runtime).
func newTestBirdNETV3(classifier *predTelemetryClassifier, labels []string) *BirdNETV3 {
	info := ModelRegistry[RegistryIDBirdNETV3]
	info.NumSpecies = len(labels)
	return &BirdNETV3{classifier: classifier, labels: labels, info: info}
}

// TestBirdNETV3Predict_NoActivation is the core correctness guard: the v3.0
// predictions output is already a per-class sigmoid (in-graph), so BirdNETV3.Predict
// must return those probabilities verbatim, applying neither sigmoid (BirdNET v2.4)
// nor softmax (Perch). A re-applied sigmoid would map 0.9 -> ~0.711 and a softmax
// would renormalize all scores, so an exact match proves no activation was applied.
func TestBirdNETV3Predict_NoActivation(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	SetMetrics(newTestMetrics(t))

	labels := []string{"Aaa aaa_Alpha", "Bbb bbb_Beta", "Ccc ccc_Gamma"}
	probs := []float32{0.9, 0.1, 0.3}
	b := newTestBirdNETV3(&predTelemetryClassifier{logits: probs}, labels)

	results, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
	require.NoError(t, err)
	require.Len(t, results, len(labels))

	// Results are sorted by confidence descending; the top must be the 0.9 class
	// with its probability unchanged.
	assert.Equal(t, "Aaa aaa_Alpha", results[0].Species)
	assert.InDelta(t, 0.9, results[0].Confidence, 1e-6,
		"v3.0 must return the in-graph sigmoid probability verbatim, not re-activated")

	// Guard explicitly against a double sigmoid: sigmoid(0.9) ~= 0.7109.
	assert.Greater(t, math.Abs(float64(results[0].Confidence)-0.7109), 0.1,
		"confidence must not be a re-applied sigmoid of the input probability")

	// Every input probability must be preserved on its label.
	byLabel := map[string]float32{}
	for _, r := range results {
		byLabel[r.Species] = r.Confidence
	}
	assert.InDelta(t, 0.1, byLabel["Bbb bbb_Beta"], 1e-6)
	assert.InDelta(t, 0.3, byLabel["Ccc ccc_Gamma"], 1e-6)
}

// TestBirdNETV3_Metadata verifies the ModelInstance metadata surface.
func TestBirdNETV3_Metadata(t *testing.T) {
	t.Parallel()

	labels := []string{"Aaa aaa_Alpha", "Bbb bbb_Beta"}
	b := newTestBirdNETV3(&predTelemetryClassifier{logits: []float32{0.5, 0.5}}, labels)

	assert.Equal(t, RegistryIDBirdNETV3, b.ModelID())
	assert.Equal(t, ModelNameBirdNETv30, b.ModelName())
	assert.Equal(t, "V3.0", b.ModelVersion())
	assert.Equal(t, 2, b.NumSpecies())
	assert.Equal(t, 32000, b.Spec().SampleRate)
	assert.Equal(t, 5*time.Second, b.Spec().ClipLength)

	// Labels returns a copy: mutating it must not affect internal state.
	got := b.Labels()
	require.Equal(t, labels, got)
	got[0] = "mutated"
	assert.Equal(t, "Aaa aaa_Alpha", b.Labels()[0])
}

// TestBirdNETV3Predict_EmptySample verifies the pre-inference empty-sample guard.
func TestBirdNETV3Predict_EmptySample(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	b := newTestBirdNETV3(&predTelemetryClassifier{logits: []float32{0.5}}, []string{"Aaa aaa_Alpha"})

	_, err := b.Predict(t.Context(), nil)
	require.Error(t, err)

	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBirdNETV3, "success"), 0,
		"an empty-sample rejection must not record a success")
}

// TestBirdNETV3Predict_NilClassifier verifies the nil-classifier guard fires
// (e.g. after Close()).
func TestBirdNETV3Predict_NilClassifier(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	SetMetrics(newTestMetrics(t))

	b := &BirdNETV3{labels: []string{"Aaa aaa_Alpha"}} // classifier is a nil interface

	_, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)
}

// TestBirdNETV3_LoaderRegistered guards the orchestrator wiring: without a
// modelLoaders entry, models.enabled=[birdnet_v3.0] would be silently skipped.
func TestBirdNETV3_LoaderRegistered(t *testing.T) {
	t.Parallel()

	_, ok := modelLoaders[RegistryIDBirdNETV3]
	assert.True(t, ok, "BirdNET v3.0 must have a modelLoaders entry")

	_, ok = openvinoCapableSecondaryBuilders[RegistryIDBirdNETV3]
	assert.True(t, ok, "BirdNET v3.0 must have an OpenVINO-capable secondary builder")

	_, ok = modelLoaders[RegistryIDBirdNETPlusV3]
	assert.True(t, ok, "BirdNET+ v3.0 must have a modelLoaders entry")

	_, ok = openvinoCapableSecondaryBuilders[RegistryIDBirdNETPlusV3]
	assert.True(t, ok, "BirdNET+ v3.0 must have an OpenVINO-capable secondary builder")
}

// birdNETV3FamilySettings returns settings in which the BirdNET v3.0 and BirdNET+ v3.0
// sections name distinct, existing, empty files, plus the label path of each family
// keyed by registry ID. BirdNET.ONNXRuntimePath names an existing stub file.
func birdNETV3FamilySettings(t *testing.T) (settings *conf.Settings, labelPaths map[string]string) {
	t.Helper()
	dir := t.TempDir()
	touch := func(name string) string {
		p := filepath.Join(dir, name)
		require.NoError(t, os.WriteFile(p, nil, 0o600))
		return p
	}
	settings = &conf.Settings{}
	settings.BirdNETV3.ModelPath = touch("preview.onnx")
	settings.BirdNETV3.LabelPath = touch("preview-labels.txt")
	settings.BirdNETPlus.ModelPath = touch("plus.onnx")
	settings.BirdNETPlus.LabelPath = touch("plus-labels.txt")
	settings.BirdNET.ONNXRuntimePath = touch("ort-stub")
	return settings, map[string]string{
		RegistryIDBirdNETV3:     settings.BirdNETV3.LabelPath,
		RegistryIDBirdNETPlusV3: settings.BirdNETPlus.LabelPath,
	}
}

// birdNETV3FamilyEntryPoints lists the two map entries production uses to build a
// BirdNET v3.0 architecture family: modelLoaders for the startup and runtime load,
// openvinoCapableSecondaryBuilders for the hot reload.
var birdNETV3FamilyEntryPoints = []string{"loader", "reload_builder"}

// buildBirdNETV3Family builds the family registryID through the named entry point
// against settings s and returns the build error. A failed build must register no
// model (loader) and return no instance (reload builder). The loader reads the
// global settings snapshot (orchestrator.go:280-291), so it publishes s there and
// the caller must not be parallel. o.Settings stays nil on purpose, so a loader that
// stopped reading the snapshot fails with the unknown-ID error (familyFields returns
// false for nil settings, model_manager.go:571-572) instead of passing.
func buildBirdNETV3Family(t *testing.T, entry, registryID string, s *conf.Settings) error {
	t.Helper()
	o := newTestOrchestrator(t)
	switch entry {
	case "loader":
		conftest.SetTestSettings(s)
		t.Cleanup(func() { conftest.SetTestSettings(nil) })
		require.Contains(t, modelLoaders, registryID)
		o.mu.Lock()
		err := modelLoaders[registryID](o, 1)
		o.mu.Unlock()
		assert.Empty(t, o.models, "a failed load must not register a model")
		return err
	case "reload_builder":
		require.Contains(t, openvinoCapableSecondaryBuilders, registryID)
		m, err := openvinoCapableSecondaryBuilders[registryID](o, s, 1)
		assert.Nil(t, m, "a failed build must return no instance")
		return err
	default:
		require.Failf(t, "unknown entry point", "%q", entry)
		return nil
	}
}

// TestBirdNETV3Family_EachFamilyReadsItsOwnSection drives both entry points for each
// BirdNET v3.0 architecture family and proves each builds from its own label path,
// not the sibling's. TestBirdNETV3Family_EachFamilyReadsItsOwnModelPath pins the
// model path.
//
// A real instance needs ONNX Runtime and a model file, so the observable here is the
// build error. Both sections name distinct existing files and the label files are
// empty, so NewBirdNETV3 fails at "no labels parsed from <path>"
// (birdnet_v3_onnx.go:113-117) and the error carries the label path it was handed.
// That check runs before the runtime is loaded (birdnet_v3_onnx.go:126), and
// BirdNET.ONNXRuntimePath names an existing stub file so the availability probe
// passes (internal/inference/ort_status.go:72-95) whether or not ONNX Runtime is
// installed on the machine. The result is the same on every machine and the native
// runtime is never loaded. That same early return is why the model path is not
// observable here: NewBirdNETV3 returns before it opens ModelPath.
func TestBirdNETV3Family_EachFamilyReadsItsOwnSection(t *testing.T) {
	for _, id := range []string{RegistryIDBirdNETV3, RegistryIDBirdNETPlusV3} {
		for _, entry := range birdNETV3FamilyEntryPoints {
			t.Run(entry+"/"+id, func(t *testing.T) {
				s, labels := birdNETV3FamilySettings(t)

				err := buildBirdNETV3Family(t, entry, id, s)

				require.Error(t, err)
				require.ErrorContains(t, err, labels[id], "the build must read this family's own label path")
				for other, otherPath := range labels {
					if other != id {
						assert.NotContains(t, err.Error(), otherPath,
							"the build must not read the %s section", other)
					}
				}
			})
		}
	}
}

// TestBirdNETV3Family_EachFamilyReadsItsOwnModelPath proves each family builds from
// its own model path, not the sibling's. No hermetic step after the label check
// names the model path: NewBirdNETV3 goes on to load ONNX Runtime
// (birdnet_v3_onnx.go:126) and open the model file. Instead the family under test has
// an empty ModelPath while its sibling's ModelPath names an existing file. The
// orchestrator has no models directory, so nothing can fill the empty path
// (model_path_resolution.go:193-226 keeps it empty when the sibling lookup finds no
// model, :294-296, and the installed-path lookup returns nothing, orchestrator.go:699-700).
// The build must therefore stop at "model files not installed or configured" for this
// family, before the label file is read. A build that took the sibling's model path
// would have a model path, get past that check and fail at the empty label file
// instead.
func TestBirdNETV3Family_EachFamilyReadsItsOwnModelPath(t *testing.T) {
	for _, id := range []string{RegistryIDBirdNETV3, RegistryIDBirdNETPlusV3} {
		for _, entry := range birdNETV3FamilyEntryPoints {
			t.Run(entry+"/"+id, func(t *testing.T) {
				s, labels := birdNETV3FamilySettings(t)
				switch id {
				case RegistryIDBirdNETV3:
					s.BirdNETV3.ModelPath = ""
				case RegistryIDBirdNETPlusV3:
					s.BirdNETPlus.ModelPath = ""
				}

				err := buildBirdNETV3Family(t, entry, id, s)

				require.Error(t, err)
				require.ErrorContains(t, err, ModelRegistry[id].Name+" model files not installed or configured")
				for _, labelPath := range labels {
					assert.NotContains(t, err.Error(), labelPath, "the build must stop before the label file is read")
				}
			})
		}
	}
}

// TestBirdNETV3_IdentityComesFromRegistryInfo checks that an instance carrying the
// BirdNET+ v3.0 registry entry reports that ID and name and tags a failed prediction
// with it, not with the BirdNET v3.0 preview's ID. The instance is built by hand, so
// this pins the methods that read the instance's registry info, not NewBirdNETV3
// storing the entry that cfg.RegistryID selects.
func TestBirdNETV3_IdentityComesFromRegistryInfo(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	info := ModelRegistry[RegistryIDBirdNETPlusV3]
	boom := errors.Newf("backend inference failed").Category(errors.CategoryAudio).Build()
	b := &BirdNETV3{
		classifier: &predTelemetryClassifier{err: boom},
		labels:     []string{"Aaa aaa_Alpha", "Bbb bbb_Beta"},
		info:       info,
	}

	assert.Equal(t, RegistryIDBirdNETPlusV3, b.ModelID())
	assert.Equal(t, ModelNameBirdNETPlusV30, b.ModelName())

	_, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)
	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDBirdNETPlusV3, "error"), 0,
		"the failure must be tagged with the BirdNET+ v3.0 registry ID")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBirdNETV3, "error"), 0,
		"the failure must not be tagged with the BirdNET v3.0 preview ID")
}

// TestNewBirdNETV3_UnknownRegistryID verifies the constructor refuses a registry
// ID it has no entry for instead of building an instance with empty metadata.
func TestNewBirdNETV3_UnknownRegistryID(t *testing.T) {
	t.Parallel()

	_, err := NewBirdNETV3(&BirdNETV3Config{RegistryID: "No_Such_Model"})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "unknown model registry ID")
}

// TestNewBirdNETV3_RejectsOtherArchitecture verifies the constructor refuses a
// registry ID that exists but belongs to another model architecture, so a Perch or
// BirdNET v2.4 identity can never be attached to the BirdNET v3.0 loader.
func TestNewBirdNETV3_RejectsOtherArchitecture(t *testing.T) {
	t.Parallel()

	for _, id := range []string{RegistryIDPerchV2, RegistryIDBirdNETV24} {
		_, err := NewBirdNETV3(&BirdNETV3Config{RegistryID: id})
		require.Error(t, err, id)
		assert.Contains(t, err.Error(), "not a BirdNET v3.0 architecture model", id)
	}
}

// TestBirdNETV3_CatalogEntry guards the catalog entry shape: correct repo, visible
// in the gallery, and the expected model/labels/geomodel/taxonomy companion files.
func TestBirdNETV3_CatalogEntry(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok, "birdnet-v3.0 catalog entry must exist")

	assert.Equal(t, RegistryIDBirdNETV3, entry.RegistryID)
	assert.Equal(t, "tphakala/BirdNET-v3.0-Models", entry.HuggingFaceRepo)
	assert.False(t, entry.Hidden, "v3.0 is visible in the gallery (public HF repo, pinned checksums, RAM-floor gated)")
	assert.True(t, entry.RequiresONNX)

	// v3.0 ships as a developer preview: the gallery flags it as not the GA build
	// and surfaces the build tag (which otherwise lives only in the file paths).
	assert.Equal(t, ChannelPreview, entry.Channel, "v3.0 is a developer-preview release")
	assert.Equal(t, "preview3.1", entry.BuildLabel, "v3.0 surfaces its preview build tag")

	// A stable (GA) sibling must leave Channel/BuildLabel empty, so its on-disk JSON
	// stays byte-identical and adding these omitempty fields does not shift the
	// stable entry's contribution to catalogChecksum (the invariant model_catalog.go
	// documents). Guards against a stray Channel: "stable" being added to a GA entry.
	perch, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok, "perch-v2 catalog entry must exist")
	assert.Empty(t, perch.Channel, "a stable entry must not set Channel (omitempty byte-identity)")
	assert.Empty(t, perch.BuildLabel, "a stable entry must not set BuildLabel")

	roles := map[string]int{}
	for _, f := range entry.Files {
		roles[f.Role]++
	}
	assert.Equal(t, 1, roles[RoleModel], "one model file")
	assert.Equal(t, 1, roles[RoleLabels], "one labels file")
	assert.True(t, ProvidesGeomodel(&entry, ""), "v3.0 pairs with the v3 geomodel range filter")
	assert.True(t, HasTaxonomyFiles(&entry), "v3.0 ships the shared taxonomy companion")
}

// TestBirdNETV3Predict_InferenceError verifies an inference failure surfaces as an
// error and is recorded exactly once (and not as a success).
func TestBirdNETV3Predict_InferenceError(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	boom := errors.Newf("backend inference failed").Category(errors.CategoryAudio).Build()
	b := newTestBirdNETV3(&predTelemetryClassifier{err: boom}, []string{"Aaa aaa_Alpha", "Bbb bbb_Beta"})

	_, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)
	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDBirdNETV3, "error"), 0,
		"a failed inference must be recorded exactly once as an error")
	assert.InDelta(t, 0.0, predictionCount(t, m, RegistryIDBirdNETV3, "success"), 0,
		"a failed inference must not record a spurious success")
}

// TestBirdNETV3Predict_LabelMismatch verifies a label/score count mismatch (a
// failure after inference begins) records exactly one error.
func TestBirdNETV3Predict_LabelMismatch(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	m := newTestMetrics(t)
	SetMetrics(m)

	// Two labels but only one score: pairLabelsAndConfidence returns an error.
	b := newTestBirdNETV3(&predTelemetryClassifier{logits: []float32{0.5}}, []string{"Aaa aaa_Alpha", "Bbb bbb_Beta"})

	_, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2}})
	require.Error(t, err)
	assert.InDelta(t, 1.0, predictionCount(t, m, RegistryIDBirdNETV3, "error"), 0,
		"a label/score mismatch must be recorded exactly once as an error")
}

// TestBirdNETV3_Close verifies Close releases the classifier without error and that
// a subsequent Predict fails via the nil-classifier guard.
func TestBirdNETV3_Close(t *testing.T) {
	resetGlobalMetrics(t)
	t.Cleanup(func() { resetGlobalMetrics(t) })
	SetMetrics(newTestMetrics(t))

	b := newTestBirdNETV3(&predTelemetryClassifier{logits: []float32{0.5}}, []string{"Aaa aaa_Alpha"})
	require.NoError(t, b.Close())

	_, err := b.Predict(t.Context(), [][]float32{{0.1}})
	require.Error(t, err, "Predict after Close must fail via the nil-classifier guard")
}

// TestBirdNETV3Predict_RejectsNonFiniteScores verifies that a backend returning a
// NaN or Inf score fails the window instead of handing a non-finite confidence to
// the processor, where it would bypass the threshold filter. BirdNET v3.0 runs
// the OpenVINO f16 GPU path that produced all-NaN Perch logits on Intel Arc, so
// it needs the same guard.
func TestBirdNETV3Predict_RejectsNonFiniteScores(t *testing.T) {
	t.Parallel()
	labels := []string{"Aaa aaa_Alpha", "Bbb bbb_Beta", "Ccc ccc_Gamma"}
	for name, scores := range map[string][]float32{
		"NaN":  {0.9, float32(math.NaN()), 0.1},
		"+Inf": {float32(math.Inf(1)), 0.2, 0.1},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			b := newTestBirdNETV3(&predTelemetryClassifier{logits: scores}, labels)
			results, err := b.Predict(t.Context(), [][]float32{{0.1, 0.2, 0.3}})
			require.Error(t, err)
			assert.Nil(t, results)
			assert.Contains(t, err.Error(), "non-finite score")
		})
	}
}
