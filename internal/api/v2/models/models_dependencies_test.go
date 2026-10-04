package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// Synthetic catalog IDs and sizes for the dependency API tests.
const (
	apiDepA = "api-dep-a"
	apiDepP = "api-dep-primary"
	apiDepG = "api-dep-geo"
	apiDepT = "api-dep-tax"

	apiSizeModel  = 1000
	apiSizeLabels = 20
	apiSizeGeo    = 300
	apiSizeGeoLbl = 4
	apiSizeTax    = 5
	apiSizeDFT    = 700
)

func apiFile(role, name string, size int64) classifier.CatalogFile {
	return classifier.CatalogFile{RemotePath: name, LocalName: name, Role: role, SizeBytes: size}
}

// apiDependencyCatalog is a synthetic catalog: A (flat, depends on G and T), P (a
// permanent-shaped entry: builtin baseline plus a DFT variant, depends on G), G (the
// shared geomodel) and T (a hidden taxonomy component).
func apiDependencyCatalog() []classifier.CatalogEntry {
	return []classifier.CatalogEntry{
		{
			ID: apiDepA, Name: "Dependent A", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/a", DependsOn: []string{apiDepG, apiDepT},
			Files: []classifier.CatalogFile{
				apiFile(classifier.RoleModel, "a.onnx", apiSizeModel),
				apiFile(classifier.RoleLabels, "a.txt", apiSizeLabels),
			},
		},
		{
			ID: apiDepP, Name: "Primary", Version: "2.4", Category: classifier.CategoryBird,
			RegistryID: classifier.RegistryIDBirdNETV24, HuggingFaceRepo: "t/p", DependsOn: []string{apiDepG},
			Variants: []classifier.CatalogVariant{
				{ID: "builtin", BuiltIn: true, Default: true},
				{ID: "fp32-dfttrunc", Precision: "fp32", Files: []classifier.CatalogFile{apiFile(classifier.RoleModel, "p-dft.onnx", apiSizeDFT)}},
			},
		},
		{
			ID: apiDepG, Name: "Geomodel", Version: "3.0", Category: classifier.CategoryGeomodel,
			GeomodelVersion: "v3", HuggingFaceRepo: "t/g",
			Files: []classifier.CatalogFile{
				apiFile(classifier.RoleGeomodelModel, "geo.onnx", apiSizeGeo),
				apiFile(classifier.RoleGeomodelLabels, "geo.txt", apiSizeGeoLbl),
			},
		},
		{
			ID: apiDepT, Name: "Taxonomy", Version: "1.0", Category: classifier.CategoryBird,
			Hidden: true, Component: true, HuggingFaceRepo: "t/t",
			Files: []classifier.CatalogFile{apiFile(classifier.RoleTaxonomy, "taxonomy.csv", apiSizeTax)},
		},
	}
}

// useCatalog makes entries the active catalog through the exported loader (an empty
// catalog_checksum is treated as a hand-edited file and kept), and restores the
// embedded catalog afterwards. Tests using it are serial: the catalog is global.
func useCatalog(t *testing.T, entries []classifier.CatalogEntry) {
	t.Helper()
	dir := t.TempDir()
	data, err := json.Marshal(map[string]any{"schema_version": 1, "catalog_checksum": "", "entries": entries})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(dir, "model-catalog.json"), data, 0o600))
	require.NoError(t, classifier.LoadCatalog(dir))
	t.Cleanup(func() { require.NoError(t, classifier.LoadCatalog(t.TempDir())) })
	_, ok := classifier.GetCatalogEntry(apiDepG)
	require.Equal(t, entries[0].ID == apiDepA, ok, "the synthetic catalog must be active")
}

func dependencyHandler(t *testing.T, mm *classifier.ModelManager) *Handler {
	t.Helper()
	core := apitest.NewCore(t)
	core.ModelManager = mm
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		return hwprofile.Profile{
			Arch:          "amd64",
			TotalRAMBytes: 16 * 1024 * 1024 * 1024,
			Backends:      hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
		}
	}
	return h
}

func writeFile(t *testing.T, path string) {
	t.Helper()
	require.NoError(t, os.MkdirAll(filepath.Dir(path), 0o750))
	require.NoError(t, os.WriteFile(path, []byte("x"), 0o600))
}

func TestUninstallModel_DependentsReturns409WithErrorKey(t *testing.T) {
	useCatalog(t, apiDependencyCatalog())
	modelsDir := t.TempDir()
	writeFile(t, filepath.Join(modelsDir, apiDepA, "a.onnx"))
	writeFile(t, filepath.Join(modelsDir, apiDepA, "a.txt"))
	writeFile(t, filepath.Join(modelsDir, "shared", "geo.onnx"))
	writeFile(t, filepath.Join(modelsDir, "shared", "geo.txt"))
	writeFile(t, filepath.Join(modelsDir, "shared", "taxonomy.csv"))
	mm := classifier.NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(apiDepA))
	require.True(t, mm.IsInstalled(apiDepG))
	h := dependencyHandler(t, mm)

	e := echo.New()
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/models/installed/"+apiDepG, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues(apiDepG)
	require.NoError(t, h.UninstallModel(ctx))

	require.Equal(t, http.StatusConflict, rec.Code)
	var body struct {
		Code        int    `json:"code"`
		ErrorKey    string `json:"error_key"`
		ErrorParams struct {
			Name       string                  `json:"name"`
			Models     string                  `json:"models"`
			Dependents []classifier.CatalogRef `json:"dependents"`
		} `json:"error_params"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, http.StatusConflict, body.Code)
	assert.Equal(t, removeHasDependentsKey, body.ErrorKey)
	assert.Equal(t, "Geomodel", body.ErrorParams.Name)
	assert.Equal(t, "Dependent A", body.ErrorParams.Models)
	require.Len(t, body.ErrorParams.Dependents, 1)
	assert.Equal(t, apiDepA, body.ErrorParams.Dependents[0].ID)
	assert.True(t, mm.IsInstalled(apiDepG), "a refused uninstall keeps the model")
	assert.FileExists(t, filepath.Join(modelsDir, "shared", "geo.onnx"))
}

func TestGetModelCatalog_DependencySizesAndFields(t *testing.T) {
	useCatalog(t, apiDependencyCatalog())
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	catalog := catalogRequest(t, h)

	a := findEntry(catalog, apiDepA)
	require.NotNil(t, a)
	assert.True(t, a.HasGeomodel, "the geomodel arrives through a dependency")
	assert.Equal(t, []string{apiDepG, apiDepT}, a.DependsOn)
	assert.Equal(t, int64(apiSizeModel+apiSizeLabels+apiSizeGeo+apiSizeGeoLbl+apiSizeTax), a.TotalSizeBytes,
		"the size includes the dependency files")

	p := findEntry(catalog, apiDepP)
	require.NotNil(t, p)
	assert.True(t, p.HasGeomodel, "a downloaded variant brings the geomodel though the default is the baseline")
	builtin := findVariant(p, "builtin")
	require.NotNil(t, builtin)
	assert.Zero(t, builtin.SizeBytes, "the baseline downloads nothing")
	dft := findVariant(p, "fp32-dfttrunc")
	require.NotNil(t, dft)
	assert.Equal(t, int64(apiSizeDFT+apiSizeGeo+apiSizeGeoLbl), dft.SizeBytes)

	g := findEntry(catalog, apiDepG)
	require.NotNil(t, g)
	assert.Empty(t, g.DependsOn)
	assert.Nil(t, findEntry(catalog, apiDepT), "a hidden component is not listed")
}

func TestGetModelCatalog_EmbeddedHasGeomodelAndSizesUnchanged(t *testing.T) {
	require.NoError(t, classifier.LoadCatalog(t.TempDir()))
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	catalog := catalogRequest(t, h)
	require.NotEmpty(t, catalog)
	for i := range catalog {
		got := &catalog[i]
		entry, ok := classifier.GetCatalogEntry(got.ID)
		require.True(t, ok, got.ID)
		var total int64
		for _, f := range entry.Files {
			total += f.SizeBytes
		}
		assert.Equal(t, classifier.HasGeomodelFiles(&entry), got.HasGeomodel, "%s hasGeomodel", got.ID)
		assert.Equal(t, total, got.TotalSizeBytes, "%s totalSizeBytes", got.ID)
		assert.Empty(t, got.DependsOn, "%s dependsOn", got.ID)
		for _, v := range entry.Variants {
			var size int64
			for _, f := range v.Files {
				size += f.SizeBytes
			}
			if rv := findVariant(got, v.ID); rv != nil {
				assert.Equal(t, size, rv.SizeBytes, "%s/%s sizeBytes", got.ID, v.ID)
			}
		}
	}
}
