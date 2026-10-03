package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// Synthetic geomodel file names used by the range-filter guard tests. They differ
// from the shipped catalog's names so a test passes only when the guard reads the
// names from the active catalog rather than from conf constants.
const (
	guardGeomodelName        = "guard_geomodel.onnx"
	guardGeomodelLabelsName  = "guard_geomodel_labels.txt"
	guardVariantGeomodelName = "guard_variant_geomodel.onnx"
	guardRenamedGeomodelName = "guard_geomodel_renamed.onnx"
	guardCustomModelPath     = "/mnt/nas/range/my_filter.onnx"
	guardCustomLabelsPath    = "/mnt/nas/range/my_filter_labels.txt"
	guardCustomModelVersion  = "custom-range-filter"
)

// guardGeomodelEntry returns a flat catalog entry carrying a full geomodel tuple
// under the given model file name.
func guardGeomodelEntry(id, modelName string) CatalogEntry {
	return CatalogEntry{
		ID:              id,
		RegistryID:      RegistryIDPerchV2,
		Category:        CategoryWildlife,
		GeomodelVersion: geomodelRangeFilterVersion,
		Files: []CatalogFile{
			{RemotePath: modelName, LocalName: modelName, Role: RoleGeomodelModel},
			{RemotePath: guardGeomodelLabelsName, LocalName: guardGeomodelLabelsName, Role: RoleGeomodelLabels},
		},
	}
}

// guardVariantEntry returns an entry whose geomodel file appears only in a
// non-default variant, so the guard must look past the resolved entry files.
func guardVariantEntry() CatalogEntry {
	return CatalogEntry{
		ID:         "guard-variant",
		RegistryID: RegistryIDBirdNETV3,
		Category:   CategoryWildlife,
		Variants: []CatalogVariant{
			{ID: "plain", Default: true, Files: []CatalogFile{
				{RemotePath: "plain.onnx", LocalName: "plain.onnx", Role: RoleModel},
			}},
			{ID: "with-geo", Files: []CatalogFile{
				{RemotePath: "with_geo.onnx", LocalName: "with_geo.onnx", Role: RoleModel},
				{RemotePath: guardVariantGeomodelName, LocalName: guardVariantGeomodelName, Role: RoleGeomodelModel},
			}},
		},
	}
}

// useGuardCatalog installs a synthetic active catalog for the test and restores
// the embedded catalog on cleanup. Callers must not run in parallel.
func useGuardCatalog(t *testing.T, entries ...CatalogEntry) {
	t.Helper()
	resetActiveCatalog(t)
	setActiveCatalog(entries)
}

// guardSharedPath returns the gallery-managed shared path of name under modelsDir.
func guardSharedPath(modelsDir, name string) string {
	return filepath.Join(modelsDir, sharedDirName, name)
}

// setCustomRangeFilter points rf at a custom range filter outside the gallery.
func setCustomRangeFilter(rf *conf.RangeFilterSettings) {
	rf.Model = guardCustomModelVersion
	rf.ModelPath = guardCustomModelPath
	rf.LabelsPath = guardCustomLabelsPath
}

// assertCustomRangeFilter asserts rf still holds the custom range filter that
// setCustomRangeFilter wrote.
func assertCustomRangeFilter(t *testing.T, rf *conf.RangeFilterSettings) {
	t.Helper()
	assert.Equal(t, guardCustomModelVersion, rf.Model, "a custom range filter keeps its model version")
	assert.Equal(t, guardCustomModelPath, rf.ModelPath, "a custom model path is never rewritten")
	assert.Equal(t, guardCustomLabelsPath, rf.LabelsPath, "a custom labels path is never rewritten")
}

// guardModelsDir returns a models directory whose base name is "models", the
// shape a real install has, under a fresh temporary root.
func guardModelsDir(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "models")
}

func TestRangeFilterGalleryManaged(t *testing.T) {
	// Not parallel: mutates the global active catalog via setActiveCatalog.
	useGuardCatalog(t, guardGeomodelEntry("guard-geo", guardGeomodelName), guardVariantEntry())

	modelsDir := guardModelsDir(t)
	mm := NewModelManager(modelsDir, nil, &conf.Settings{})

	tests := []struct {
		name      string
		modelPath string
		want      bool
	}{
		{"empty path", "", true},
		{"current gallery path", guardSharedPath(modelsDir, guardGeomodelName), true},
		{"gallery path of a variant-only geomodel file", guardSharedPath(modelsDir, guardVariantGeomodelName), true},
		{"gallery labels file name", guardSharedPath(modelsDir, guardGeomodelLabelsName), true},
		{"moved models directory", filepath.FromSlash("/other/models/shared/" + guardGeomodelName), true},
		{"user file in the shared directory", guardSharedPath(modelsDir, "my_filter.onnx"), false},
		{"look-alike layout under another root", filepath.FromSlash("/mnt/nas/shared/" + guardGeomodelName), false},
		{"catalog name outside a shared directory", filepath.FromSlash("/mnt/nas/" + guardGeomodelName), false},
		{"catalog name in a model subdirectory", filepath.Join(modelsDir, "perch-v2", guardGeomodelName), false},
		{"non-geomodel catalog file in the shared directory", guardSharedPath(modelsDir, "with_geo.onnx"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			rf := &conf.RangeFilterSettings{ModelPath: tt.modelPath}
			assert.Equal(t, tt.want, mm.rangeFilterGalleryManaged(rf))
		})
	}
}

// TestRangeFilterGalleryManaged_FollowsCatalogNames pins that the managed names come
// from the active catalog: when a catalog update renames the geomodel file, a config
// still pointing at the old shared file is no longer gallery-managed.
func TestRangeFilterGalleryManaged_FollowsCatalogNames(t *testing.T) {
	// Not parallel: mutates the global active catalog via setActiveCatalog.
	modelsDir := guardModelsDir(t)
	mm := NewModelManager(modelsDir, nil, &conf.Settings{})
	oldPath := &conf.RangeFilterSettings{ModelPath: guardSharedPath(modelsDir, guardGeomodelName)}
	newPath := &conf.RangeFilterSettings{ModelPath: guardSharedPath(modelsDir, guardRenamedGeomodelName)}

	useGuardCatalog(t, guardGeomodelEntry("guard-geo", guardGeomodelName))
	assert.True(t, mm.rangeFilterGalleryManaged(oldPath), "the current catalog name is managed")
	assert.False(t, mm.rangeFilterGalleryManaged(newPath), "a name the catalog does not carry is custom")

	setActiveCatalog([]CatalogEntry{guardGeomodelEntry("guard-geo", guardRenamedGeomodelName)})
	assert.False(t, mm.rangeFilterGalleryManaged(oldPath), "a name dropped from the catalog becomes custom")
	assert.True(t, mm.rangeFilterGalleryManaged(newPath), "the renamed catalog file is managed")
}

func TestApplyRangeFilterConfigForInstall_KeepsCustomPath(t *testing.T) {
	// Not parallel: mutates the global active catalog via setActiveCatalog.
	entry := guardGeomodelEntry("guard-geo", guardGeomodelName)
	useGuardCatalog(t, entry)

	modelsDir := guardModelsDir(t)
	mm := NewModelManager(modelsDir, nil, &conf.Settings{})

	t.Run("custom path is kept", func(t *testing.T) {
		updated := &conf.Settings{}
		rf := updated.RangeFilterConfig()
		setCustomRangeFilter(rf)

		mm.applyRangeFilterConfigForInstall(updated, &entry)

		assertCustomRangeFilter(t, rf)
	})

	t.Run("gallery-managed path is written", func(t *testing.T) {
		updated := &conf.Settings{}
		mm.applyRangeFilterConfigForInstall(updated, &entry)

		rf := updated.RangeFilterConfig()
		assert.Equal(t, geomodelRangeFilterVersion, rf.Model)
		assert.Equal(t, guardSharedPath(modelsDir, guardGeomodelName), rf.ModelPath)
		assert.Equal(t, guardSharedPath(modelsDir, guardGeomodelLabelsName), rf.LabelsPath)
	})
}

func TestApplyInstalledGeomodelConfig_KeepsCustomPath(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings and the
	// active catalog via setActiveCatalog.
	redirectConfigFile(t)
	entry := guardGeomodelEntry("guard-geo", guardGeomodelName)
	useGuardCatalog(t, entry)
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	t.Run("custom path is kept", func(t *testing.T) {
		modelsDir := guardModelsDir(t)
		settings := conftest.GetTestSettings()
		rf := settings.RangeFilterConfig()
		setCustomRangeFilter(rf)
		conf.StoreSettings(settings)

		mm := newSelfHealManager(t, modelsDir)
		before := conf.GetSettings()
		mm.applyInstalledGeomodelConfig(GetLogger(), &entry, entry.ID)
		after := conf.GetSettings()

		assert.Same(t, before, after, "a custom range filter must not publish a new settings snapshot")
		assertCustomRangeFilter(t, after.RangeFilterConfig())
	})

	t.Run("gallery-managed path is promoted", func(t *testing.T) {
		modelsDir := guardModelsDir(t)
		settings := conftest.GetTestSettings()
		rf := settings.RangeFilterConfig()
		rf.Model = ""
		rf.ModelPath = ""
		rf.LabelsPath = ""
		conf.StoreSettings(settings)

		mm := newSelfHealManager(t, modelsDir)
		mm.applyInstalledGeomodelConfig(GetLogger(), &entry, entry.ID)

		current := conf.GetSettings().RangeFilterConfig()
		assert.Equal(t, geomodelRangeFilterVersion, current.Model)
		assert.Equal(t, guardSharedPath(modelsDir, guardGeomodelName), current.ModelPath)
		assert.Equal(t, guardSharedPath(modelsDir, guardGeomodelLabelsName), current.LabelsPath)
	})
}

func TestApplyRangeFilterConfigForUninstall_KeepsCustomPathWhenNoSurvivor(t *testing.T) {
	// Not parallel: mutates the global active catalog via setActiveCatalog.
	removed := guardGeomodelEntry("guard-geo", guardGeomodelName)
	useGuardCatalog(t, removed)

	modelsDir := guardModelsDir(t)
	mm := NewModelManager(modelsDir, nil, &conf.Settings{})

	t.Run("custom path is kept", func(t *testing.T) {
		updated := &conf.Settings{}
		rf := updated.RangeFilterConfig()
		setCustomRangeFilter(rf)
		rf.PassUnmappedSpecies = true

		mm.applyRangeFilterConfigForUninstall(updated, &removed)

		assertCustomRangeFilter(t, rf)
		assert.True(t, rf.PassUnmappedSpecies, "a custom range filter keeps PassUnmappedSpecies")
	})

	t.Run("gallery-managed path is cleared", func(t *testing.T) {
		updated := &conf.Settings{}
		rf := updated.RangeFilterConfig()
		rf.Model = geomodelRangeFilterVersion
		rf.ModelPath = guardSharedPath(modelsDir, guardGeomodelName)
		rf.LabelsPath = guardSharedPath(modelsDir, guardGeomodelLabelsName)
		rf.PassUnmappedSpecies = true

		mm.applyRangeFilterConfigForUninstall(updated, &removed)

		assert.Empty(t, rf.Model)
		assert.Empty(t, rf.ModelPath)
		assert.Empty(t, rf.LabelsPath)
		assert.False(t, rf.PassUnmappedSpecies)
	})
}
