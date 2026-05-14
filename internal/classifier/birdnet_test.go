package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

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
	assert.Equal(t, "v3", geomodel.Version)
	assert.Equal(t, len(geomodelLabels), geomodel.TotalSpecies)
	assert.True(t, geomodel.AutoSelected)
	assert.True(t, autoSelected)

	assert.Equal(t, RegistryIDBirdNETV3, primary.ID)
	assert.Equal(t, len(classifierLabels), primary.TotalSpecies)
	assert.Equal(t, 3, primary.WithRangeData)
	assert.Equal(t, 1, primary.WithoutRangeData)

	assert.Equal(t, geomodelLabels, geoLabels)
}
