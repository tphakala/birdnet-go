package classifier

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// fakeModelInstance is a minimal ModelInstance for testing orchestrator logic
// without loading real models.
type fakeModelInstance struct {
	id           string
	name         string
	labels       []string
	resolvedPath string
}

func (f *fakeModelInstance) Predict(_ context.Context, _ [][]float32) ([]datastore.Results, error) {
	return nil, nil
}
func (f *fakeModelInstance) Spec() ModelSpec      { return ModelSpec{} }
func (f *fakeModelInstance) ModelID() string      { return f.id }
func (f *fakeModelInstance) ModelName() string    { return f.name }
func (f *fakeModelInstance) ModelVersion() string { return "" }
func (f *fakeModelInstance) NumSpecies() int      { return len(f.labels) }
func (f *fakeModelInstance) Labels() []string     { return f.labels }
func (f *fakeModelInstance) Close() error         { return nil }
func (f *fakeModelInstance) RuntimeInfo() (device, backend, precision string) {
	return deviceCPU, BackendONNX, ""
}
func (f *fakeModelInstance) ResolvedModelPath() string { return f.resolvedPath }

func TestShouldAutoSelectV3Geomodel(t *testing.T) {
	t.Parallel()

	// Create a temp directory with the expected shared geomodel files.
	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, conf.GeomodelONNXLocalName), []byte("fake-onnx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, conf.GeomodelLabelsLocalName), []byte("fake-labels"), 0o644))

	// A separate temp dir that has no geomodel files.
	emptyModelsDir := t.TempDir()
	require.NoError(t, os.MkdirAll(filepath.Join(emptyModelsDir, "shared"), 0o755))

	tests := []struct {
		name      string
		modelID   string
		modelsDir string
		want      bool
	}{
		{
			name:      "PerchV2 with files present",
			modelID:   RegistryIDPerchV2,
			modelsDir: modelsDir,
			want:      true,
		},
		{
			name:      "BirdNET V3.0 with files present",
			modelID:   RegistryIDBirdNETV3,
			modelsDir: modelsDir,
			want:      true,
		},
		{
			name:      "BirdNET V2.4 is not eligible",
			modelID:   "BirdNET_V2.4",
			modelsDir: modelsDir,
			want:      false,
		},
		{
			name:      "Bat is not eligible",
			modelID:   RegistryIDBat,
			modelsDir: modelsDir,
			want:      false,
		},
		{
			name:      "BSG is not eligible",
			modelID:   RegistryIDBSG,
			modelsDir: modelsDir,
			want:      false,
		},
		{
			name:      "empty modelsDir returns false",
			modelID:   RegistryIDPerchV2,
			modelsDir: "",
			want:      false,
		},
		{
			name:      "missing geomodel files returns false",
			modelID:   RegistryIDPerchV2,
			modelsDir: emptyModelsDir,
			want:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := shouldAutoSelectV3Geomodel(tt.modelID, tt.modelsDir)
			assert.Equal(t, tt.want, got)
		})
	}
}

// TestIsStockMDataRangeFilterPath pins that a stock MData model is recognized by its
// base name alone, in any directory and in any letter case, and that nothing else is.
func TestIsStockMDataRangeFilterPath(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		modelPath string
		want      bool
	}{
		{"empty path", "", false},
		{"ONNX MData under the model dir", filepath.Join(DefaultModelDirectory, DefaultRangeFilterV2ONNXModelName), true},
		{"V1 TFLite MData, absolute", filepath.Join(t.TempDir(), DefaultRangeFilterV1ModelName), true},
		{"V2 TFLite MData, bare name", DefaultRangeFilterV2ModelName, true},
		{"different letter case", filepath.Join(DefaultModelDirectory, strings.ToLower(DefaultRangeFilterV2ONNXModelName)), true},
		{"custom file", filepath.FromSlash("/mnt/nas/range/my_filter.onnx"), false},
		{"shared geomodel file", filepath.Join("models", sharedDirName, conf.GeomodelONNXLocalName), false},
		{"stock name as a directory", filepath.Join(DefaultRangeFilterV2ONNXModelName, "my_filter.onnx"), false},
		{"stock name with a suffix", DefaultRangeFilterV2ONNXModelName + ".bak", false},
		// Literal names that existing user configs hold; they must stay stock even if
		// the Default* constants are renamed for a new release.
		{"literal ONNX MData name", "model/BirdNET_MData_V2.onnx", true},
		{"literal V1 TFLite MData name", "model/BirdNET_GLOBAL_6K_V2.4_MData_Model_FP16.tflite", true},
		{"literal V2 TFLite MData name", "model/BirdNET_GLOBAL_6K_V2.4_MData_Model_V2_FP16.tflite", true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isStockMDataRangeFilterPath(tt.modelPath))
		})
	}
}

// TestRangeFilterYieldsToGeomodel pins which range filter configs give way to an
// installed geomodel: every model value except v3, with no path or a stock MData path.
// A custom path never yields.
func TestRangeFilterYieldsToGeomodel(t *testing.T) {
	t.Parallel()

	stockPath := filepath.Join(DefaultModelDirectory, DefaultRangeFilterV2ONNXModelName)
	tests := []struct {
		name string
		rf   conf.RangeFilterSettings
		want bool
	}{
		{"default config", conf.RangeFilterSettings{}, true},
		{"latest, no path", conf.RangeFilterSettings{Model: conf.RangeFilterModelLatest}, true},
		{"legacy pin, no path", conf.RangeFilterSettings{Model: conf.RangeFilterModelLegacy}, true},
		{"latest, stock MData path", conf.RangeFilterSettings{Model: conf.RangeFilterModelLatest, ModelPath: stockPath}, true},
		{"legacy pin, stock MData path", conf.RangeFilterSettings{Model: conf.RangeFilterModelLegacy, ModelPath: stockPath}, true},
		{"latest, custom path", conf.RangeFilterSettings{Model: conf.RangeFilterModelLatest, ModelPath: "/m/custom.onnx"}, false},
		{"v3 already selects the geomodel", conf.RangeFilterSettings{Model: conf.RangeFilterModelV3}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, rangeFilterYieldsToGeomodel(&tt.rf))
		})
	}
}

func TestApplyAutoSelectedGeomodelPaths(t *testing.T) {
	t.Parallel()

	t.Run("sets paths when not configured", func(t *testing.T) {
		t.Parallel()

		modelsDir := t.TempDir()
		sharedDir := filepath.Join(modelsDir, "shared")
		require.NoError(t, os.MkdirAll(sharedDir, 0o755))

		settings := &conf.Settings{}
		// RangeFilter.Model is empty (default), so auto-selection should apply.
		applyAutoSelectedGeomodelPaths(settings, modelsDir)

		assert.Equal(t, "v3", settings.BirdNET.RangeFilter.Model)
		assert.Equal(t, filepath.Join(sharedDir, conf.GeomodelONNXLocalName), settings.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, filepath.Join(sharedDir, conf.GeomodelLabelsLocalName), settings.BirdNET.RangeFilter.LabelsPath)
	})

	t.Run("does not override existing valid v3 config", func(t *testing.T) {
		t.Parallel()

		modelsDir := t.TempDir()
		sharedDir := filepath.Join(modelsDir, "shared")
		require.NoError(t, os.MkdirAll(sharedDir, 0o755))

		// Create real files at custom paths to simulate an existing valid config.
		customONNX := filepath.Join(t.TempDir(), "custom_geomodel.onnx")
		customLabels := filepath.Join(t.TempDir(), "custom_labels.txt")
		require.NoError(t, os.WriteFile(customONNX, []byte("custom"), 0o644))
		require.NoError(t, os.WriteFile(customLabels, []byte("custom"), 0o644))

		settings := &conf.Settings{}
		settings.BirdNET.RangeFilter.Model = "v3"
		settings.BirdNET.RangeFilter.ModelPath = customONNX
		settings.BirdNET.RangeFilter.LabelsPath = customLabels

		applyAutoSelectedGeomodelPaths(settings, modelsDir)

		// Paths should remain unchanged because both files exist.
		assert.Equal(t, "v3", settings.BirdNET.RangeFilter.Model)
		assert.Equal(t, customONNX, settings.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, customLabels, settings.BirdNET.RangeFilter.LabelsPath)
	})

	t.Run("overrides v3 config when files are missing", func(t *testing.T) {
		t.Parallel()

		modelsDir := t.TempDir()
		sharedDir := filepath.Join(modelsDir, "shared")
		require.NoError(t, os.MkdirAll(sharedDir, 0o755))

		settings := &conf.Settings{}
		settings.BirdNET.RangeFilter.Model = "v3"
		settings.BirdNET.RangeFilter.ModelPath = "/nonexistent/geomodel.onnx"
		settings.BirdNET.RangeFilter.LabelsPath = "/nonexistent/labels.txt"

		applyAutoSelectedGeomodelPaths(settings, modelsDir)

		// Paths should be overridden because the configured files don't exist.
		assert.Equal(t, "v3", settings.BirdNET.RangeFilter.Model)
		assert.Equal(t, filepath.Join(sharedDir, conf.GeomodelONNXLocalName), settings.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, filepath.Join(sharedDir, conf.GeomodelLabelsLocalName), settings.BirdNET.RangeFilter.LabelsPath)
	})
}

func TestOrchestrator_SetModelsDir(t *testing.T) {
	t.Parallel()

	o := &Orchestrator{}
	o.mu.RLock()
	assert.Empty(t, o.modelsDir)
	o.mu.RUnlock()

	o.SetModelsDir("/some/path")
	o.mu.RLock()
	assert.Equal(t, "/some/path", o.modelsDir)
	o.mu.RUnlock()
}

func TestPrimaryRangeFilterCoverage_NoFilter(t *testing.T) {
	// Not parallel: RangeFilterStatus reads the globally published settings snapshot
	// via primary.currentSettings() (Phase 2b moved coverage assembly onto the
	// orchestrator), so this publishes settings globally rather than relying on a
	// struct field a concurrent test could shadow.
	settings := &conf.Settings{}
	settings.BirdNET.Labels = []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
	}
	settings.BirdNET.RangeFilter.Model = ""
	settings.BirdNET.RangeFilter.Threshold = 0.05
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	bn := &BirdNET{
		Settings:  settings,
		ModelInfo: ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}
	bn.settingsAtomic.Store(settings)
	o := &Orchestrator{Settings: settings,
		models: map[string]*modelEntry{RegistryIDBirdNETV24: {instance: bn}}}
	o.settingsAtomic.Store(settings)
	o.rangeFilter = newTestRangeFilterService(nil)

	resp := o.RangeFilterStatus()

	assert.Nil(t, resp.Geomodel, "geomodel should be nil when no mapped filter is active")
	require.NotEmpty(t, resp.Classifiers)
	primary := resp.Classifiers[0]
	assert.Equal(t, "BirdNET_V2.4", primary.ID)
	assert.Equal(t, "BirdNET v2.4", primary.Name)
	assert.Equal(t, 2, primary.TotalSpecies)
	assert.Zero(t, primary.WithRangeData)
	assert.Zero(t, primary.WithoutRangeData)
}

func TestPrimaryRangeFilterCoverage_WithMappedFilter(t *testing.T) {
	// Not parallel: see TestPrimaryRangeFilterCoverage_NoFilter; RangeFilterStatus
	// reads the globally published settings snapshot.
	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))

	expectedONNX := filepath.Join(sharedDir, conf.GeomodelONNXLocalName)
	expectedLabels := filepath.Join(sharedDir, conf.GeomodelLabelsLocalName)

	settings := &conf.Settings{}
	settings.BirdNET.RangeFilter.Model = "v3"
	settings.BirdNET.RangeFilter.ModelPath = expectedONNX
	settings.BirdNET.RangeFilter.LabelsPath = expectedLabels
	settings.BirdNET.RangeFilter.Threshold = 0.03
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
	settings.BirdNET.LocationConfigured = true

	classifierLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Erithacus rubecula_European Robin",
		"Ficedula hypoleuca_Pied Flycatcher",
	}
	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
		"Erithacus rubecula_Robin",
	}

	// NumSpecies() reads from settings.BirdNET.Labels, so populate it.
	settings.BirdNET.Labels = classifierLabels
	conftest.SetTestSettings(settings)
	t.Cleanup(func() { conftest.SetTestSettings(nil) })

	inner := &fakeRangeFilter{
		scores: make([]float32, len(geomodelLabels)),
	}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 1.0)

	bn := &BirdNET{
		Settings:  settings,
		ModelInfo: ModelInfo{ID: RegistryIDBirdNETV3, Name: ModelNameBirdNETv30},
	}
	bn.settingsAtomic.Store(settings)
	o := &Orchestrator{Settings: settings, modelsDir: modelsDir,
		models: map[string]*modelEntry{RegistryIDBirdNETV3: {instance: bn}}}
	o.settingsAtomic.Store(settings)
	o.rangeFilter = newTestRangeFilterService(mapped)

	resp := o.RangeFilterStatus()

	require.NotNil(t, resp.Geomodel)
	assert.Equal(t, "v3.0", resp.Geomodel.Version)
	assert.Equal(t, len(geomodelLabels), resp.Geomodel.TotalSpecies)
	assert.True(t, resp.Geomodel.AutoSelected)

	require.NotEmpty(t, resp.Classifiers)
	primary := resp.Classifiers[0]
	assert.Equal(t, RegistryIDBirdNETV3, primary.ID)
	assert.Equal(t, len(classifierLabels), primary.TotalSpecies)
	assert.Equal(t, 3, primary.WithRangeData)
	assert.Equal(t, 1, primary.WithoutRangeData)

	// geoLabels are internal to the service now; assert them via the mapped view.
	mrf, ok := o.rangeFilter.mappedView()
	require.True(t, ok)
	assert.Equal(t, geomodelLabels, mrf.geomodelLabels)
}

func TestRangeFilterStatus_PerClassifierCoverage(t *testing.T) {
	t.Parallel()

	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))

	expectedONNX := filepath.Join(sharedDir, conf.GeomodelONNXLocalName)
	expectedLabels := filepath.Join(sharedDir, conf.GeomodelLabelsLocalName)

	settings := &conf.Settings{}
	settings.BirdNET.RangeFilter.Model = "v3"
	settings.BirdNET.RangeFilter.ModelPath = expectedONNX
	settings.BirdNET.RangeFilter.LabelsPath = expectedLabels
	settings.BirdNET.RangeFilter.Threshold = 0.01
	settings.BirdNET.RangeFilter.PassUnmappedSpecies = false
	settings.BirdNET.LocationConfigured = true
	settings.BirdNET.RangeFilter.LastUpdated = time.Date(2026, 5, 14, 12, 0, 0, 0, time.UTC)

	primaryLabels := []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
		"Erithacus rubecula_European Robin",
		"Ficedula hypoleuca_Pied Flycatcher",
	}
	// NumSpecies() reads from settings.BirdNET.Labels, so populate it.
	settings.BirdNET.Labels = primaryLabels

	geomodelLabels := []string{
		"Parus major_Great Tit",
		"Turdus merula_Amsel",
		"Erithacus rubecula_Robin",
		"Corvus corax_Northern Raven",
		"Sturnus vulgaris_European Starling",
	}

	inner := &fakeRangeFilter{scores: make([]float32, len(geomodelLabels))}
	mapped := newMappedRangeFilter(inner, primaryLabels, geomodelLabels, 0.0)

	primary := &BirdNET{
		Settings:  settings,
		ModelInfo: ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}

	perchLabels := []string{
		"Turdus merula",
		"Corvus corax",
		"Bubo bubo",
	}
	perchInstance := &fakeModelInstance{
		id:     RegistryIDPerchV2,
		name:   ModelNamePerchV2,
		labels: perchLabels,
	}

	orch := &Orchestrator{
		Settings: settings,
		models: map[string]*modelEntry{
			"BirdNET_V2.4":    {instance: primary},
			RegistryIDPerchV2: {instance: perchInstance},
		},
		modelsDir: modelsDir,
	}
	// Publish the state a real geomodel reload would: kind geomodel_v3, coveredLabels the
	// v2.4 label set (v2.4 is loaded), and the participant snapshot the reload builds from
	// the loaded models. Per-participant coverage is decided by the built-over participant
	// set and the backend kind (state.hasParticipant + kind), not by the live model map.
	orch.rangeFilter = newRangeFilterService(nil)
	orch.rangeFilter.state.Store(&rangeFilterState{
		backend:       mapped,
		kind:          rfKindGeomodelV3,
		coveredLabels: primaryLabels,
		participants:  []participantLabels{{id: "BirdNET_V2.4", labels: primaryLabels}, {id: RegistryIDPerchV2, labels: perchLabels}},
		anchoredOnV24: true,
		generation:    1,
	})

	resp := orch.RangeFilterStatus()

	// Backend must report a valid kind, never the empty string (JSON contract).
	assert.Equal(t, string(rfKindGeomodelV3), resp.Backend)

	require.NotNil(t, resp.Geomodel)
	assert.Equal(t, "v3.0", resp.Geomodel.Version)
	assert.Equal(t, len(geomodelLabels), resp.Geomodel.TotalSpecies)
	assert.True(t, resp.Geomodel.AutoSelected)

	assert.False(t, resp.PassUnmappedSpecies)
	assert.InDelta(t, 0.01, resp.Threshold, 0.001)
	assert.True(t, resp.LocationConfigured)

	require.Len(t, resp.Classifiers, 2)

	classifierByID := make(map[string]ClassifierCoverage)
	for _, c := range resp.Classifiers {
		classifierByID[c.ID] = c
	}

	birdnet := classifierByID["BirdNET_V2.4"]
	assert.Equal(t, "BirdNET v2.4", birdnet.Name)
	assert.Equal(t, 4, birdnet.TotalSpecies)
	assert.Equal(t, 3, birdnet.WithRangeData)
	assert.Equal(t, 1, birdnet.WithoutRangeData)
	// v2.4 is loaded, so coveredLabels is the v2.4 label space and the geomodel scores it:
	// v2.4 is honestly covered.
	assert.True(t, birdnet.CoveredByBackend, "v2.4's label space is what the geomodel maps onto when v2.4 is loaded")

	perch := classifierByID[RegistryIDPerchV2]
	assert.Equal(t, ModelNamePerchV2, perch.Name)
	assert.Equal(t, 3, perch.TotalSpecies)
	assert.Equal(t, 2, perch.WithRangeData)
	assert.Equal(t, 1, perch.WithoutRangeData)
	// Perch is covered: the universal geomodel scores every loaded participant by canonical
	// scientific name, and Perch's geomodel-unknown species are reconciled into both the gate
	// list and the Settings preview (governed by the "allow species without range data"
	// toggle), so the status honestly reports coverage for every participant under a geomodel.
	assert.True(t, perch.CoveredByBackend, "the geomodel maps every loaded participant by canonical name")
}

func TestRangeFilterStatus_BatExcluded(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.BirdNET.RangeFilter.Model = "v3"

	geomodelLabels := []string{"Parus major_Great Tit"}
	primaryLabels := []string{"Parus major_Great Tit"}
	settings.BirdNET.Labels = primaryLabels

	inner := &fakeRangeFilter{scores: make([]float32, len(geomodelLabels))}
	mapped := newMappedRangeFilter(inner, primaryLabels, geomodelLabels, 0.0)

	primary := &BirdNET{
		Settings:  settings,
		ModelInfo: ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}

	batInstance := &fakeModelInstance{
		id:     RegistryIDBat,
		name:   "Bat Classifier",
		labels: []string{"Myotis daubentonii", "Pipistrellus pipistrellus"},
	}

	orch := &Orchestrator{
		Settings: settings,
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: primary},
			RegistryIDBat:  {instance: batInstance},
		},
	}
	orch.rangeFilter = newTestRangeFilterService(mapped)

	resp := orch.RangeFilterStatus()

	require.Len(t, resp.Classifiers, 1, "bat model should be excluded")
	assert.Equal(t, "BirdNET_V2.4", resp.Classifiers[0].ID)
}

func TestRangeFilterStatus_NoGeomodel(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.BirdNET.Labels = []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
	}
	settings.BirdNET.RangeFilter.Model = ""
	settings.BirdNET.RangeFilter.Threshold = 0.05

	primary := &BirdNET{
		Settings:  settings,
		ModelInfo: ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}

	orch := &Orchestrator{
		Settings: settings,
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: primary},
		},
	}
	orch.rangeFilter = newTestRangeFilterService(nil)

	resp := orch.RangeFilterStatus()

	assert.Nil(t, resp.Geomodel)
	require.Len(t, resp.Classifiers, 1)
	assert.Equal(t, "BirdNET_V2.4", resp.Classifiers[0].ID)
	assert.Equal(t, 2, resp.Classifiers[0].TotalSpecies)
	assert.Zero(t, resp.Classifiers[0].WithRangeData)
	assert.Zero(t, resp.Classifiers[0].WithoutRangeData)
	assert.InDelta(t, 0.05, resp.Threshold, 0.001)
}

// testV24TFLiteModelPath is the committed FP32 v2.4 model, relative to this
// package directory. Setting it as birdnet.modelpath keeps construction on the
// TFLite backend (a non-empty CustomPath suppresses the arm64 ONNX remap) and
// works under the CI `noembed` tag, where the embedded model is compiled out.
const testV24TFLiteModelPath = "data/BirdNET_GLOBAL_6K_V2.4_Model_FP32.tflite"

// TestNewBirdNET_LocaleNormalization covers locales that NormalizeLocale has to
// rewrite. An unsupported locale falls back rather than aborting construction:
// treating the fallback as fatal stopped `serve` and `benchmark` from starting at
// all on a config carrying e.g. `birdnet.locale: en`. A locale given by full name
// is rewritten to its code, and labels must be loaded for the rewritten locale,
// not the raw input.
func TestNewBirdNET_LocaleNormalization(t *testing.T) {
	t.Parallel()

	// This test constructs a TFLite v2.4 model to exercise locale normalization.
	// A notflite build has no TFLite backend, so construction errors instead of
	// running; skip so those builds stay green. See the notflite build-skip rationale.
	if !tfliteBackendAvailable {
		t.Skip("TFLite backend not linked (notflite build); locale normalization uses a TFLite v2.4 model")
	}

	tests := []struct {
		name       string
		input      string
		wantLocale string
	}{
		{
			name:       "unsupported locale falls back",
			input:      "en",
			wantLocale: conf.DefaultFallbackLocale,
		},
		{
			name:       "full name normalizes to code",
			input:      "German",
			wantLocale: "de",
		},
		{
			name:       "supported code passes through",
			input:      "fi",
			wantLocale: "fi",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &conf.Settings{}
			settings.BirdNET.Locale = tt.input
			settings.BirdNET.Version = "2.4"
			settings.BirdNET.ModelPath = testV24TFLiteModelPath

			// nil resolver: no orchestrator here, so the configured path is used
			// verbatim, which is exactly the pre-recovery behaviour this test asserts.
			bn, err := NewBirdNET(settings, nil, nil)
			if bn != nil {
				t.Cleanup(bn.Delete)
			}
			require.NoError(t, err, "locale %q must not fail construction", tt.input)
			require.NotNil(t, bn)

			assert.Equal(t, tt.wantLocale, settings.BirdNET.Locale,
				"locale %q should normalize to %q", tt.input, tt.wantLocale)

			// Labels must come from the normalized locale's label file. Comparing
			// against that file directly catches normalization running after the
			// labels are loaded, which would silently pair e.g. English labels with
			// a settings locale of "de".
			require.NotEmpty(t, settings.BirdNET.Labels, "labels should be loaded")
			want := GetLabelFileDataWithResult(bn.ModelInfo.ID, tt.wantLocale, nil)
			require.NoError(t, want.Error)
			require.False(t, want.FallbackOccurred,
				"test locale %q must have its own label file", tt.wantLocale)
			wantFirstLabel, _, _ := strings.Cut(string(want.Data), "\n")
			assert.Equal(t, strings.TrimSpace(wantFirstLabel), settings.BirdNET.Labels[0],
				"labels should be loaded from the %q label file", tt.wantLocale)
		})
	}
}
