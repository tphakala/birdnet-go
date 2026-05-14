package classifier

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// fakeModelInstance is a minimal ModelInstance for testing orchestrator logic
// without loading real models.
type fakeModelInstance struct {
	id     string
	name   string
	labels []string
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

func TestShouldAutoSelectV3Geomodel(t *testing.T) {
	t.Parallel()

	// Create a temp directory with the expected shared geomodel files.
	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, geomodelONNXLocalName), []byte("fake-onnx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, geomodelLabelsLocalName), []byte("fake-labels"), 0o644))

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
		assert.Equal(t, filepath.Join(sharedDir, geomodelONNXLocalName), settings.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, filepath.Join(sharedDir, geomodelLabelsLocalName), settings.BirdNET.RangeFilter.LabelsPath)
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
		assert.Equal(t, filepath.Join(sharedDir, geomodelONNXLocalName), settings.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, filepath.Join(sharedDir, geomodelLabelsLocalName), settings.BirdNET.RangeFilter.LabelsPath)
	})
}

func TestBirdNET_SetModelsDir(t *testing.T) {
	t.Parallel()

	bn := &BirdNET{}
	assert.Empty(t, bn.modelsDir)

	bn.SetModelsDir("/some/path")
	assert.Equal(t, "/some/path", bn.modelsDir)
}

func TestPrimaryRangeFilterCoverage_NoFilter(t *testing.T) {
	t.Parallel()

	settings := &conf.Settings{}
	settings.BirdNET.Labels = []string{
		"Turdus merula_Common Blackbird",
		"Parus major_Great Tit",
	}
	settings.BirdNET.RangeFilter.Model = ""
	settings.BirdNET.RangeFilter.Threshold = 0.05

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}

	geomodel, primary, geoLabels, autoSelected := bn.PrimaryRangeFilterCoverage()

	assert.Nil(t, geomodel, "geomodel should be nil when no mapped filter is active")
	assert.Equal(t, "BirdNET_V2.4", primary.ID)
	assert.Equal(t, "BirdNET v2.4", primary.Name)
	assert.Equal(t, 2, primary.TotalSpecies)
	assert.Zero(t, primary.WithRangeData)
	assert.Zero(t, primary.WithoutRangeData)
	assert.Empty(t, geoLabels)
	assert.False(t, autoSelected)
}

func TestPrimaryRangeFilterCoverage_WithMappedFilter(t *testing.T) {
	t.Parallel()

	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))

	expectedONNX := filepath.Join(sharedDir, geomodelONNXLocalName)
	expectedLabels := filepath.Join(sharedDir, geomodelLabelsLocalName)

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

	inner := &fakeRangeFilter{
		scores: make([]float32, len(geomodelLabels)),
	}
	mapped := newMappedRangeFilter(inner, classifierLabels, geomodelLabels, 1.0)

	bn := &BirdNET{
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: RegistryIDBirdNETV3, Name: ModelNameBirdNETv30},
		modelsDir:    modelsDir,
		rangeFilter:  mapped,
	}

	geomodel, primary, geoLabels, autoSelected := bn.PrimaryRangeFilterCoverage()

	require.NotNil(t, geomodel)
	assert.Equal(t, "v3.0", geomodel.Version)
	assert.Equal(t, len(geomodelLabels), geomodel.TotalSpecies)
	assert.True(t, geomodel.AutoSelected)
	assert.True(t, autoSelected)

	assert.Equal(t, RegistryIDBirdNETV3, primary.ID)
	assert.Equal(t, len(classifierLabels), primary.TotalSpecies)
	assert.Equal(t, 3, primary.WithRangeData)
	assert.Equal(t, 1, primary.WithoutRangeData)

	assert.Equal(t, geomodelLabels, geoLabels)
}

func TestRangeFilterStatus_PerClassifierCoverage(t *testing.T) {
	t.Parallel()

	modelsDir := t.TempDir()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))

	expectedONNX := filepath.Join(sharedDir, geomodelONNXLocalName)
	expectedLabels := filepath.Join(sharedDir, geomodelLabelsLocalName)

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
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
		modelsDir:    modelsDir,
		rangeFilter:  mapped,
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
		Settings:  settings,
		ModelInfo: primary.ModelInfo,
		primary:   primary,
		models: map[string]*modelEntry{
			"BirdNET_V2.4":    {instance: primary},
			RegistryIDPerchV2: {instance: perchInstance},
		},
		modelsDir: modelsDir,
	}

	resp := orch.RangeFilterStatus()

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

	perch := classifierByID[RegistryIDPerchV2]
	assert.Equal(t, ModelNamePerchV2, perch.Name)
	assert.Equal(t, 3, perch.TotalSpecies)
	assert.Equal(t, 2, perch.WithRangeData)
	assert.Equal(t, 1, perch.WithoutRangeData)
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
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
		rangeFilter:  mapped,
	}

	batInstance := &fakeModelInstance{
		id:     RegistryIDBat,
		name:   "Bat Classifier",
		labels: []string{"Myotis daubentonii", "Pipistrellus pipistrellus"},
	}

	orch := &Orchestrator{
		Settings:  settings,
		ModelInfo: primary.ModelInfo,
		primary:   primary,
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: primary},
			RegistryIDBat:  {instance: batInstance},
		},
	}

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
		Settings:     settings,
		speciesCache: make(map[string]*speciesCacheEntry),
		ModelInfo:    ModelInfo{ID: "BirdNET_V2.4", Name: "BirdNET v2.4"},
	}

	orch := &Orchestrator{
		Settings:  settings,
		ModelInfo: primary.ModelInfo,
		primary:   primary,
		models: map[string]*modelEntry{
			"BirdNET_V2.4": {instance: primary},
		},
	}

	resp := orch.RangeFilterStatus()

	assert.Nil(t, resp.Geomodel)
	require.Len(t, resp.Classifiers, 1)
	assert.Equal(t, "BirdNET_V2.4", resp.Classifiers[0].ID)
	assert.Equal(t, 2, resp.Classifiers[0].TotalSpecies)
	assert.Zero(t, resp.Classifiers[0].WithRangeData)
	assert.Zero(t, resp.Classifiers[0].WithoutRangeData)
	assert.InDelta(t, 0.05, resp.Threshold, 0.001)
}
