package classifier

import (
	"math"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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
}

// TestBirdNETV3_CatalogEntry guards the catalog entry shape: correct repo, hidden
// during preview, and the expected model/labels/geomodel/taxonomy companion files.
func TestBirdNETV3_CatalogEntry(t *testing.T) {
	t.Parallel()

	entry, ok := GetCatalogEntry("birdnet-v3.0")
	require.True(t, ok, "birdnet-v3.0 catalog entry must exist")

	assert.Equal(t, RegistryIDBirdNETV3, entry.RegistryID)
	assert.Equal(t, "tphakala/BirdNET-v3.0-Models", entry.HuggingFaceRepo)
	assert.True(t, entry.Hidden, "v3.0 stays hidden until GA (private HF repo, no final checksums)")
	assert.True(t, entry.RequiresONNX)

	roles := map[string]int{}
	for _, f := range entry.Files {
		roles[f.Role]++
	}
	assert.Equal(t, 1, roles[RoleModel], "one model file")
	assert.Equal(t, 1, roles[RoleLabels], "one labels file")
	assert.True(t, HasGeomodelFiles(&entry), "v3.0 pairs with the v3 geomodel range filter")
	assert.True(t, HasTaxonomyFiles(&entry), "v3.0 ships the shared taxonomy companion")
}
