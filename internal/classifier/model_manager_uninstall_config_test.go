package classifier

import (
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// batEmbeddingsLocalName is the shared backbone file every bat catalog entry carries
// (batCatalogEntry hard-codes it). The re-point points Bat.EmbeddingModel at it under
// {modelsDir}/shared.
const batEmbeddingsLocalName = "birdnet-v24-embeddings.onnx"

// installBat records an installed bat model under mm.installed with synthetic model
// and labels paths derived from its catalog ID, mirroring what ScanInstalled would
// record without downloading real multi-MB model files.
func installBat(t *testing.T, mm *ModelManager, modelsDir, catalogID string) InstalledModel {
	t.Helper()
	im := InstalledModel{
		CatalogID:  catalogID,
		ModelPath:  filepath.Join(modelsDir, catalogID, catalogID+"-model.onnx"),
		LabelsPath: filepath.Join(modelsDir, catalogID, catalogID+"-labels.txt"),
	}
	mm.installed[catalogID] = im
	return im
}

// TestApplyConfigForUninstall_BatRepointsToRemainingBat verifies that uninstalling one
// bat while another remains re-points the whole Bat family (classifier, labels and the
// shared embeddings backbone) at the survivor and retains the bat config alias and
// every source model list.
func TestApplyConfigForUninstall_BatRepointsToRemainingBat(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	settings.Bat.ClassifierModel = filepath.Join(modelsDir, "battybirdnet-eu", "battybirdnet-eu-model.onnx")
	settings.Bat.LabelPath = filepath.Join(modelsDir, "battybirdnet-eu", "battybirdnet-eu-labels.txt")
	settings.Bat.EmbeddingModel = filepath.Join(modelsDir, sharedDirName, batEmbeddingsLocalName)
	settings.Models.Enabled = []string{conf.ModelIDBirdNET, conf.ModelIDBat}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{Name: "mic", Models: []string{conf.ModelIDBirdNET, conf.ModelIDBat}}}
	conf.StoreSettings(settings)

	mm := NewModelManager(modelsDir, nil, settings)
	// The eu entry is already "deleted" (never added), matching the post-delete
	// contract of applyConfigForUninstall; only the uk survivor is installed.
	uk := installBat(t, mm, modelsDir, "battybirdnet-uk")

	euEntry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok)
	mm.applyConfigForUninstall(&euEntry)

	current := conf.GetSettings()
	assert.Equal(t, uk.ModelPath, current.Bat.ClassifierModel, "bat classifier must re-point to the survivor")
	assert.Equal(t, uk.LabelsPath, current.Bat.LabelPath, "bat labels must re-point to the survivor")
	assert.Equal(t, filepath.Join(modelsDir, sharedDirName, batEmbeddingsLocalName), current.Bat.EmbeddingModel,
		"bat embeddings must point at the shared backbone of the survivor")
	assert.Contains(t, current.Models.Enabled, conf.ModelIDBat, "the bat alias must be retained on a re-point")
	require.Len(t, current.Realtime.Audio.Sources, 1)
	assert.Equal(t, []string{conf.ModelIDBirdNET, conf.ModelIDBat}, current.Realtime.Audio.Sources[0].Models,
		"a re-point must leave source model lists untouched")
}

// TestApplyConfigForUninstall_BatPicksLowestCatalogID verifies the deterministic pick:
// with two bats remaining, the re-point chooses the lowest catalog ID (sorted), not a
// random map-order entry.
func TestApplyConfigForUninstall_BatPicksLowestCatalogID(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	settings.Models.Enabled = []string{conf.ModelIDBat}
	conf.StoreSettings(settings)

	mm := NewModelManager(modelsDir, nil, settings)
	bavaria := installBat(t, mm, modelsDir, "battybirdnet-bavaria")
	installBat(t, mm, modelsDir, "battybirdnet-uk")

	euEntry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok)
	mm.applyConfigForUninstall(&euEntry)

	current := conf.GetSettings()
	assert.Equal(t, bavaria.ModelPath, current.Bat.ClassifierModel,
		"the re-point must pick the lowest catalog ID (battybirdnet-bavaria), not battybirdnet-uk")
}

// TestApplyConfigForUninstall_LastBatClearsFamilyAndAlias verifies that uninstalling
// the only bat clears all three Bat fields, removes the alias from Models.Enabled, and
// resets a source whose only model was bat back to the birdnet fallback.
func TestApplyConfigForUninstall_LastBatClearsFamilyAndAlias(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	modelsDir := t.TempDir()
	settings := conftest.GetTestSettings()
	settings.Bat.ClassifierModel = filepath.Join(modelsDir, "battybirdnet-eu", "battybirdnet-eu-model.onnx")
	settings.Bat.LabelPath = filepath.Join(modelsDir, "battybirdnet-eu", "battybirdnet-eu-labels.txt")
	settings.Bat.EmbeddingModel = filepath.Join(modelsDir, sharedDirName, batEmbeddingsLocalName)
	settings.Models.Enabled = []string{conf.ModelIDBirdNET, conf.ModelIDBat}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{{Name: "mic", Models: []string{conf.ModelIDBat}}}
	conf.StoreSettings(settings)

	mm := NewModelManager(modelsDir, nil, settings) // no bat remains installed

	euEntry, ok := GetCatalogEntry("battybirdnet-eu")
	require.True(t, ok)
	mm.applyConfigForUninstall(&euEntry)

	current := conf.GetSettings()
	assert.Empty(t, current.Bat.ClassifierModel, "the last bat uninstall must clear the classifier path")
	assert.Empty(t, current.Bat.LabelPath, "the last bat uninstall must clear the labels path")
	assert.Empty(t, current.Bat.EmbeddingModel, "the last bat uninstall must clear the embeddings path")
	assert.NotContains(t, current.Models.Enabled, conf.ModelIDBat, "the bat alias must be removed")
	require.Len(t, current.Realtime.Audio.Sources, 1)
	assert.Equal(t, []string{conf.ModelIDBirdNET}, current.Realtime.Audio.Sources[0].Models,
		"a source left with no models must fall back to the birdnet default")
}

// TestApplyConfigForUninstall_SameCategoryDifferentFamilyIsNotARepoint is the guard
// against a category-keyed rule: perch-v2 and birdnet-v3.0 are both CategoryWildlife
// but write different settings families, so uninstalling perch must clear the Perch
// family and never touch the still-installed BirdNET v3.0 family.
func TestApplyConfigForUninstall_SameCategoryDifferentFamilyIsNotARepoint(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	modelsDir := t.TempDir()
	perchAlias := ConfigAliasForRegistry(RegistryIDPerchV2)
	v3Alias := ConfigAliasForRegistry(RegistryIDBirdNETV3)

	settings := conftest.GetTestSettings()
	settings.Perch.ModelPath = filepath.Join(modelsDir, "perch-v2", "perch.onnx")
	settings.Perch.LabelPath = filepath.Join(modelsDir, "perch-v2", "perch-labels.txt")
	settings.BirdNETV3.ModelPath = filepath.Join(modelsDir, "birdnet-v3.0", "v3.onnx")
	settings.BirdNETV3.LabelPath = filepath.Join(modelsDir, "birdnet-v3.0", "v3-labels.txt")
	settings.Models.Enabled = []string{conf.ModelIDBirdNET, perchAlias, v3Alias}
	conf.StoreSettings(settings)

	mm := NewModelManager(modelsDir, nil, settings)
	// birdnet-v3.0 remains installed; perch-v2 is being uninstalled (not in mm.installed).
	mm.installed["birdnet-v3.0"] = InstalledModel{
		CatalogID:  "birdnet-v3.0",
		ModelPath:  settings.BirdNETV3.ModelPath,
		LabelsPath: settings.BirdNETV3.LabelPath,
	}

	perchEntry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	mm.applyConfigForUninstall(&perchEntry)

	current := conf.GetSettings()
	assert.Empty(t, current.Perch.ModelPath, "the uninstalled perch family must be cleared")
	assert.Empty(t, current.Perch.LabelPath, "the uninstalled perch family must be cleared")
	assert.Equal(t, settings.BirdNETV3.ModelPath, current.BirdNETV3.ModelPath, "birdnet v3.0 must be untouched")
	assert.Equal(t, settings.BirdNETV3.LabelPath, current.BirdNETV3.LabelPath, "birdnet v3.0 must be untouched")
	assert.NotContains(t, current.Models.Enabled, perchAlias, "the perch alias must be removed")
	assert.Contains(t, current.Models.Enabled, v3Alias, "the birdnet v3.0 alias must be retained")
}

// TestApplyConfigForUninstall_UnknownRegistryIsNoop and _NilSettingsIsNoop pin the two
// safe no-op paths: an unknown registry ID (familyFields ok=false) writes no family
// field, and a nil settings receiver returns before any store.
func TestApplyConfigForUninstall_UnknownRegistryIsNoop(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })
	isolateTestConfig(t)

	settings := conftest.GetTestSettings()
	settings.Perch.ModelPath = "/keep/perch.onnx"
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, settings)

	mm.applyConfigForUninstall(&CatalogEntry{ID: "mystery", RegistryID: "NoSuchModel"})
	current := conf.GetSettings()
	assert.Equal(t, "/keep/perch.onnx", current.Perch.ModelPath, "an unknown registry ID must write nothing")
}

func TestApplyConfigForUninstall_NilSettingsIsNoop(t *testing.T) {
	// Not parallel: mutates global settings via conf.StoreSettings.
	origSettings := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(origSettings) })

	settings := conftest.GetTestSettings()
	settings.Perch.ModelPath = "/keep/perch.onnx"
	conf.StoreSettings(settings)
	mm := NewModelManager(t.TempDir(), nil, nil) // nil settings receiver

	perchEntry, ok := GetCatalogEntry("perch-v2")
	require.True(t, ok)
	mm.applyConfigForUninstall(&perchEntry)
	current := conf.GetSettings()
	assert.Equal(t, "/keep/perch.onnx", current.Perch.ModelPath, "a nil settings receiver must be a no-op")
}

// TestApplyRangeFilterConfigForUninstall_RetainsWhenOtherGeomodelInstalled verifies the
// geomodel range filter config is kept when another installed entry still carries
// geomodel files; it is only cleared when the last geomodel-bearing model is removed
// (that clear path is covered by TestModelManager_Uninstall_GeomodelConfigClearing).
func TestApplyRangeFilterConfigForUninstall_RetainsWhenOtherGeomodelInstalled(t *testing.T) {
	// Not parallel: mutates the global active catalog via setActiveCatalog.
	geomodelFiles := []CatalogFile{
		{RemotePath: "geo.onnx", LocalName: "geomodel_v3.onnx", Role: RoleGeomodelModel},
		{RemotePath: "geo_labels.txt", LocalName: "geomodel_v3_labels.txt", Role: RoleGeomodelLabels},
	}
	removed := CatalogEntry{ID: "geo-removed", RegistryID: RegistryIDPerchV2, Category: CategoryWildlife, GeomodelVersion: "v3", Files: geomodelFiles}
	remaining := CatalogEntry{ID: "geo-remaining", RegistryID: RegistryIDBirdNETV3, Category: CategoryWildlife, GeomodelVersion: "v3", Files: geomodelFiles}

	withEntries := make([]CatalogEntry, len(EmbeddedCatalog), len(EmbeddedCatalog)+2)
	copy(withEntries, EmbeddedCatalog)
	withEntries = append(withEntries, removed, remaining)
	setActiveCatalog(withEntries)
	t.Cleanup(func() { setActiveCatalog(nil) })

	mm := NewModelManager(t.TempDir(), nil, &conf.Settings{})
	mm.installed["geo-remaining"] = InstalledModel{CatalogID: "geo-remaining"}

	updated := &conf.Settings{}
	updated.BirdNET.RangeFilter.Model = "v3"
	updated.BirdNET.RangeFilter.ModelPath = "/models/shared/geomodel_v3.onnx"
	updated.BirdNET.RangeFilter.PassUnmappedSpecies = true

	mm.applyRangeFilterConfigForUninstall(updated, &removed)

	assert.Equal(t, "v3", updated.BirdNET.RangeFilter.Model, "config must be retained while another geomodel remains")
	assert.Equal(t, "/models/shared/geomodel_v3.onnx", updated.BirdNET.RangeFilter.ModelPath, "model path must be retained")
	assert.True(t, updated.BirdNET.RangeFilter.PassUnmappedSpecies, "PassUnmappedSpecies must be retained")
}
