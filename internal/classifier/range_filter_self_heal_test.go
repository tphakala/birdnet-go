package classifier

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// writeSharedGeomodel creates the shared geomodel ONNX and labels files under
// <modelsDir>/shared/ using the runtime's expected local filenames.
func writeSharedGeomodel(t *testing.T, modelsDir string) (onnxPath, labelsPath string) {
	t.Helper()
	sharedDir := filepath.Join(modelsDir, "shared")
	require.NoError(t, os.MkdirAll(sharedDir, 0o755))
	onnxPath = filepath.Join(sharedDir, geomodelONNXLocalName)
	labelsPath = filepath.Join(sharedDir, geomodelLabelsLocalName)
	require.NoError(t, os.WriteFile(onnxPath, []byte("onnx"), 0o644))
	require.NoError(t, os.WriteFile(labelsPath, []byte("labels"), 0o644))
	return onnxPath, labelsPath
}

// sharedGeomodelExpectedPaths returns the gallery-managed shared paths under
// modelsDir without creating the files.
func sharedGeomodelExpectedPaths(modelsDir string) (onnxPath, labelsPath string) {
	sharedDir := filepath.Join(modelsDir, "shared")
	return filepath.Join(sharedDir, geomodelONNXLocalName), filepath.Join(sharedDir, geomodelLabelsLocalName)
}

// redirectConfigFile points conf persistence at a throwaway file so the orphan
// self-heal's conf.SaveSettings call cannot touch the real user config during
// tests. conf.ConfigPath is checked first by FindConfigFile, so setting it
// fully redirects the write. The original value is restored on cleanup.
func redirectConfigFile(t *testing.T) {
	t.Helper()
	cfg := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(cfg, []byte("{}\n"), 0o600))
	orig := conf.ConfigPath
	conf.ConfigPath = cfg
	t.Cleanup(func() { conf.ConfigPath = orig })
}

func TestDecideGeomodelOrphanAction(t *testing.T) {
	t.Parallel()

	const gallerySharedDirPrefix = "/models/shared/"

	var (
		modelPath  = gallerySharedDirPrefix + geomodelONNXLocalName
		labelsPath = gallerySharedDirPrefix + geomodelLabelsLocalName
	)

	tests := []struct {
		name         string
		rf           conf.RangeFilterSettings
		filesPresent bool
		want         geomodelOrphanAction
	}{
		{
			name:         "gallery paths, files present, model empty -> promote",
			rf:           conf.RangeFilterSettings{Model: "", ModelPath: modelPath, LabelsPath: labelsPath},
			filesPresent: true,
			want:         geomodelOrphanPromote,
		},
		{
			name:         "gallery paths, files present, already v3 -> none",
			rf:           conf.RangeFilterSettings{Model: "v3", ModelPath: modelPath, LabelsPath: labelsPath},
			filesPresent: true,
			want:         geomodelOrphanNone,
		},
		{
			name:         "gallery paths, files absent, dead refs -> clear",
			rf:           conf.RangeFilterSettings{Model: "v3", ModelPath: modelPath, LabelsPath: labelsPath},
			filesPresent: false,
			want:         geomodelOrphanClear,
		},
		{
			name:         "gallery paths, files absent, model empty but paths still set -> clear",
			rf:           conf.RangeFilterSettings{Model: "", ModelPath: modelPath, LabelsPath: labelsPath},
			filesPresent: false,
			want:         geomodelOrphanClear,
		},
		{
			name:         "custom model path -> none",
			rf:           conf.RangeFilterSettings{Model: "v3", ModelPath: "/custom/geo.onnx", LabelsPath: labelsPath},
			filesPresent: true,
			want:         geomodelOrphanNone,
		},
		{
			name:         "custom labels path -> none",
			rf:           conf.RangeFilterSettings{Model: "v3", ModelPath: modelPath, LabelsPath: "/custom/labels.txt"},
			filesPresent: false,
			want:         geomodelOrphanNone,
		},
		{
			name:         "empty paths -> none",
			rf:           conf.RangeFilterSettings{Model: "", ModelPath: "", LabelsPath: ""},
			filesPresent: false,
			want:         geomodelOrphanNone,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			rf := tt.rf
			got := decideGeomodelOrphanAction(&rf, modelPath, labelsPath, tt.filesPresent)
			assert.Equal(t, tt.want, got)
		})
	}
}

// newSelfHealManager builds a ModelManager wired to a minimal orchestrator
// (no primary, so ReloadRangeFilter is a safe no-op) and to the global test
// settings snapshot. installedIDs are returned for passing to ensureGeomodelConfig.
func newSelfHealManager(t *testing.T, modelsDir string) *ModelManager {
	t.Helper()
	orch := &Orchestrator{models: make(map[string]*modelEntry)}
	settings := conf.GetSettings()
	return NewModelManager(modelsDir, orch, settings)
}

func TestEnsureGeomodelConfig_OrphanSelfHeal(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings and the
	// process-global conf.ConfigPath.
	redirectConfigFile(t)

	t.Run("orphan files present promotes config to v3", func(t *testing.T) {
		origSettings := conf.GetSettings()
		t.Cleanup(func() { conf.StoreSettings(origSettings) })

		modelsDir := t.TempDir()
		onnxPath, labelsPath := writeSharedGeomodel(t, modelsDir)

		settings := conf.GetTestSettings()
		settings.Models.Directory = modelsDir
		settings.BirdNET.RangeFilter.Model = ""
		settings.BirdNET.RangeFilter.ModelPath = onnxPath
		settings.BirdNET.RangeFilter.LabelsPath = labelsPath
		conf.StoreSettings(settings)

		mm := newSelfHealManager(t, modelsDir)
		// No geomodel-capable model installed (orphan scenario).
		mm.ensureGeomodelConfig(GetLogger(), nil)

		current := conf.GetSettings()
		assert.Equal(t, "v3", current.BirdNET.RangeFilter.Model)
		assert.Equal(t, onnxPath, current.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, labelsPath, current.BirdNET.RangeFilter.LabelsPath)
	})

	t.Run("orphan files absent clears config", func(t *testing.T) {
		origSettings := conf.GetSettings()
		t.Cleanup(func() { conf.StoreSettings(origSettings) })

		modelsDir := t.TempDir()
		onnxPath, labelsPath := sharedGeomodelExpectedPaths(modelsDir)

		settings := conf.GetTestSettings()
		settings.Models.Directory = modelsDir
		settings.BirdNET.RangeFilter.Model = "v3"
		settings.BirdNET.RangeFilter.ModelPath = onnxPath
		settings.BirdNET.RangeFilter.LabelsPath = labelsPath
		settings.BirdNET.RangeFilter.PassUnmappedSpecies = true
		conf.StoreSettings(settings)

		mm := newSelfHealManager(t, modelsDir)
		mm.ensureGeomodelConfig(GetLogger(), nil)

		current := conf.GetSettings()
		assert.Empty(t, current.BirdNET.RangeFilter.Model)
		assert.Empty(t, current.BirdNET.RangeFilter.ModelPath)
		assert.Empty(t, current.BirdNET.RangeFilter.LabelsPath)
		assert.False(t, current.BirdNET.RangeFilter.PassUnmappedSpecies)
	})

	t.Run("custom paths are not touched", func(t *testing.T) {
		origSettings := conf.GetSettings()
		t.Cleanup(func() { conf.StoreSettings(origSettings) })

		modelsDir := t.TempDir()
		writeSharedGeomodel(t, modelsDir)

		const customModel = "/custom/geo.onnx"
		const customLabels = "/custom/labels.txt"

		settings := conf.GetTestSettings()
		settings.Models.Directory = modelsDir
		settings.BirdNET.RangeFilter.Model = "v3"
		settings.BirdNET.RangeFilter.ModelPath = customModel
		settings.BirdNET.RangeFilter.LabelsPath = customLabels
		conf.StoreSettings(settings)

		mm := newSelfHealManager(t, modelsDir)
		mm.ensureGeomodelConfig(GetLogger(), nil)

		current := conf.GetSettings()
		assert.Equal(t, "v3", current.BirdNET.RangeFilter.Model)
		assert.Equal(t, customModel, current.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, customLabels, current.BirdNET.RangeFilter.LabelsPath)
	})

	t.Run("already v3 with present files is a no-op", func(t *testing.T) {
		origSettings := conf.GetSettings()
		t.Cleanup(func() { conf.StoreSettings(origSettings) })

		modelsDir := t.TempDir()
		onnxPath, labelsPath := writeSharedGeomodel(t, modelsDir)

		settings := conf.GetTestSettings()
		settings.Models.Directory = modelsDir
		settings.BirdNET.RangeFilter.Model = "v3"
		settings.BirdNET.RangeFilter.ModelPath = onnxPath
		settings.BirdNET.RangeFilter.LabelsPath = labelsPath
		conf.StoreSettings(settings)

		// Capture the snapshot pointer so we can verify no new snapshot was published.
		before := conf.GetSettings()

		mm := newSelfHealManager(t, modelsDir)
		mm.ensureGeomodelConfig(GetLogger(), nil)

		after := conf.GetSettings()
		assert.Same(t, before, after, "no-op must not publish a new settings snapshot")
		assert.Equal(t, "v3", after.BirdNET.RangeFilter.Model)
	})

	t.Run("installed geomodel model preserves promote and skips orphan path", func(t *testing.T) {
		origSettings := conf.GetSettings()
		t.Cleanup(func() { conf.StoreSettings(origSettings) })

		modelsDir := t.TempDir()
		onnxPath, labelsPath := writeSharedGeomodel(t, modelsDir)

		// Config stale (model empty) but a geomodel-capable model IS installed:
		// existing promote behavior must set v3 via the loop, not the orphan path.
		settings := conf.GetTestSettings()
		settings.Models.Directory = modelsDir
		settings.BirdNET.RangeFilter.Model = ""
		settings.BirdNET.RangeFilter.ModelPath = onnxPath
		settings.BirdNET.RangeFilter.LabelsPath = labelsPath
		conf.StoreSettings(settings)

		// perch-v2 is a real catalog entry with geomodel files whose LocalNames
		// match the shared geomodel files created above.
		mm := newSelfHealManager(t, modelsDir)
		mm.ensureGeomodelConfig(GetLogger(), []string{"perch-v2"})

		current := conf.GetSettings()
		assert.Equal(t, "v3", current.BirdNET.RangeFilter.Model, "installed-model promote must still apply")
		assert.Equal(t, onnxPath, current.BirdNET.RangeFilter.ModelPath)
		assert.Equal(t, labelsPath, current.BirdNET.RangeFilter.LabelsPath)
	})
}
