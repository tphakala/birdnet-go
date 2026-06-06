package conf_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// writeSharedGeomodelFiles creates the shared geomodel ONNX and labels files
// under <modelsDir>/shared/ so resolution and os.Stat checks succeed.
func writeSharedGeomodelFiles(t *testing.T, modelsDir string) {
	t.Helper()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, conf.GeomodelONNXLocalName), []byte("onnx"), 0o644))
	require.NoError(t, os.WriteFile(filepath.Join(sharedDir, conf.GeomodelLabelsLocalName), []byte("labels"), 0o644))
}

// sharedGeomodelPaths returns the gallery-managed shared ONNX and labels paths
// under modelsDir.
func sharedGeomodelPaths(modelsDir string) (onnxPath, labelsPath string) {
	sharedDir := filepath.Join(modelsDir, "shared")
	return filepath.Join(sharedDir, conf.GeomodelONNXLocalName), filepath.Join(sharedDir, conf.GeomodelLabelsLocalName)
}

func TestMigrateOrphanGeomodelRangeFilter(t *testing.T) {
	// Not parallel: mutates settings fields and relies on Models.Directory
	// for deterministic resolution.

	t.Run("orphan shared paths with both files present promotes to v3", func(t *testing.T) {
		modelsDir := t.TempDir()
		writeSharedGeomodelFiles(t, modelsDir)
		onnxPath, labelsPath := sharedGeomodelPaths(modelsDir)

		s := conftest.GetTestSettings()
		s.Models.Directory = modelsDir
		s.BirdNET.RangeFilter.Model = ""
		s.BirdNET.RangeFilter.ModelPath = onnxPath
		s.BirdNET.RangeFilter.LabelsPath = labelsPath

		changed := s.MigrateOrphanGeomodelRangeFilter()

		assert.True(t, changed, "promote must report a change")
		assert.Equal(t, "v3", s.BirdNET.RangeFilter.Model)
		assert.Equal(t, onnxPath, s.BirdNET.RangeFilter.ModelPath, "model path must be left intact")
		assert.Equal(t, labelsPath, s.BirdNET.RangeFilter.LabelsPath, "labels path must be left intact")
	})

	t.Run("orphan shared paths with files absent clears config", func(t *testing.T) {
		modelsDir := t.TempDir()
		// Intentionally do NOT create the shared files.
		onnxPath, labelsPath := sharedGeomodelPaths(modelsDir)

		s := conftest.GetTestSettings()
		s.Models.Directory = modelsDir
		s.BirdNET.RangeFilter.Model = "v3"
		s.BirdNET.RangeFilter.ModelPath = onnxPath
		s.BirdNET.RangeFilter.LabelsPath = labelsPath
		s.BirdNET.RangeFilter.PassUnmappedSpecies = true

		changed := s.MigrateOrphanGeomodelRangeFilter()

		assert.True(t, changed, "clear must report a change")
		assert.Empty(t, s.BirdNET.RangeFilter.Model)
		assert.Empty(t, s.BirdNET.RangeFilter.ModelPath)
		assert.Empty(t, s.BirdNET.RangeFilter.LabelsPath)
		assert.False(t, s.BirdNET.RangeFilter.PassUnmappedSpecies)
	})

	t.Run("custom paths are never touched", func(t *testing.T) {
		modelsDir := t.TempDir()
		writeSharedGeomodelFiles(t, modelsDir)

		const customModel = "/custom/geomodel.onnx"
		const customLabels = "/custom/geomodel_labels.txt"

		s := conftest.GetTestSettings()
		s.Models.Directory = modelsDir
		s.BirdNET.RangeFilter.Model = "v3"
		s.BirdNET.RangeFilter.ModelPath = customModel
		s.BirdNET.RangeFilter.LabelsPath = customLabels
		s.BirdNET.RangeFilter.PassUnmappedSpecies = true

		changed := s.MigrateOrphanGeomodelRangeFilter()

		assert.False(t, changed, "custom paths must not be migrated")
		assert.Equal(t, "v3", s.BirdNET.RangeFilter.Model)
		assert.Equal(t, customModel, s.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, customLabels, s.BirdNET.RangeFilter.LabelsPath)
		assert.True(t, s.BirdNET.RangeFilter.PassUnmappedSpecies)
	})

	t.Run("already v3 with shared paths and files present is a no-op", func(t *testing.T) {
		modelsDir := t.TempDir()
		writeSharedGeomodelFiles(t, modelsDir)
		onnxPath, labelsPath := sharedGeomodelPaths(modelsDir)

		s := conftest.GetTestSettings()
		s.Models.Directory = modelsDir
		s.BirdNET.RangeFilter.Model = "v3"
		s.BirdNET.RangeFilter.ModelPath = onnxPath
		s.BirdNET.RangeFilter.LabelsPath = labelsPath

		changed := s.MigrateOrphanGeomodelRangeFilter()

		assert.False(t, changed, "matching config must be a no-op")
		assert.Equal(t, "v3", s.BirdNET.RangeFilter.Model)
		assert.Equal(t, onnxPath, s.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, labelsPath, s.BirdNET.RangeFilter.LabelsPath)
	})

	t.Run("already cleared with shared paths and files absent is a no-op", func(t *testing.T) {
		modelsDir := t.TempDir()
		// No shared files, config already empty for all geomodel fields.
		s := conftest.GetTestSettings()
		s.Models.Directory = modelsDir
		s.BirdNET.RangeFilter.Model = ""
		s.BirdNET.RangeFilter.ModelPath = ""
		s.BirdNET.RangeFilter.LabelsPath = ""

		changed := s.MigrateOrphanGeomodelRangeFilter()

		assert.False(t, changed, "non-gallery (empty) paths must not be migrated")
		assert.Empty(t, s.BirdNET.RangeFilter.Model)
		assert.Empty(t, s.BirdNET.RangeFilter.ModelPath)
		assert.Empty(t, s.BirdNET.RangeFilter.LabelsPath)
	})
}
