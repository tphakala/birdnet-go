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
	apiDepN = "api-dep-none"

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
// shared geomodel), T (a hidden taxonomy component) and N (flat, no dependency and no
// geomodel).
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
			GeomodelVersion: "v3", HuggingFaceRepo: "t/g", Channel: classifier.ChannelPreview,
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
		{
			ID: apiDepN, Name: "No geomodel", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/n",
			Files: []classifier.CatalogFile{
				apiFile(classifier.RoleModel, "n.onnx", apiSizeModel),
				apiFile(classifier.RoleLabels, "n.txt", apiSizeLabels),
			},
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
	_, ok := classifier.GetCatalogEntry(entries[0].ID)
	require.True(t, ok, "the synthetic catalog must be active")
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
	// The permanent entry is always reported installed on its baseline variant, which
	// depends on the geomodel like any other variant.
	assert.Equal(t, "Dependent A, Primary", body.ErrorParams.Models)
	require.Len(t, body.ErrorParams.Dependents, 2)
	assert.Equal(t, apiDepA, body.ErrorParams.Dependents[0].ID)
	assert.Equal(t, apiDepP, body.ErrorParams.Dependents[1].ID)
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
	assert.Equal(t, classifier.ChannelStable, a.Channel, "an entry without a channel is reported as stable")
	assert.Equal(t, []string{apiDepG, apiDepT}, a.DependsOn)
	assert.Nil(t, a.Variants, "flat entries carry no variants")
	assert.Empty(t, a.RecommendedVariantID, "flat entries carry no recommendation")
	assert.Equal(t, int64(apiSizeModel+apiSizeLabels+apiSizeGeo+apiSizeGeoLbl+apiSizeTax), a.TotalSizeBytes,
		"the size includes the dependency files")

	p := findEntry(catalog, apiDepP)
	require.NotNil(t, p)
	assert.True(t, p.HasGeomodel, "the default variant brings the geomodel through the dependency")
	builtin := findVariant(p, "builtin")
	require.NotNil(t, builtin)
	assert.Equal(t, int64(apiSizeGeo+apiSizeGeoLbl), builtin.SizeBytes, "the baseline counts only its dependency files")
	dft := findVariant(p, "fp32-dfttrunc")
	require.NotNil(t, dft)
	assert.Equal(t, int64(apiSizeDFT+apiSizeGeo+apiSizeGeoLbl), dft.SizeBytes)

	g := findEntry(catalog, apiDepG)
	require.NotNil(t, g)
	assert.Empty(t, g.DependsOn)
	assert.True(t, g.HasGeomodel, "the geomodel entry provides its own geomodel files")
	assert.Equal(t, classifier.ChannelPreview, g.Channel, "a declared channel is passed through")
	assert.Nil(t, findEntry(catalog, apiDepT), "a hidden component is not listed")

	n := findEntry(catalog, apiDepN)
	require.NotNil(t, n)
	assert.False(t, n.HasGeomodel, "an entry with no geomodel file and no dependency has no geomodel")
}

// TestGetModelCatalog_RegistryIDWireKey pins the registryId JSON key that clients
// join on, and its omission for an entry that is not a classifier. It decodes the
// raw body into maps so a renamed tag or a dropped omitempty fails.
func TestGetModelCatalog_RegistryIDWireKey(t *testing.T) {
	useCatalog(t, apiDependencyCatalog())
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/models/catalog", http.NoBody)
	rec := httptest.NewRecorder()
	require.NoError(t, h.GetModelCatalog(e.NewContext(req, rec)))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Catalog []map[string]any `json:"catalog"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	byID := make(map[string]map[string]any, len(resp.Catalog))
	for _, entry := range resp.Catalog {
		id, _ := entry["id"].(string)
		byID[id] = entry
	}

	require.Contains(t, byID, apiDepP)
	assert.Equal(t, classifier.RegistryIDBirdNETV24, byID[apiDepP]["registryId"], "a classifier entry reports its registry ID")
	require.Contains(t, byID, apiDepG)
	assert.NotContains(t, byID[apiDepG], "registryId", "an entry that is not a classifier omits the key")
}

func TestGetModelCatalog_SupersededBy(t *testing.T) {
	catalog := apiDependencyCatalog()
	// A is superseded by N (visible); N is superseded by T (a hidden component).
	catalog[0].SupersededBy = apiDepN
	catalog[4].SupersededBy = apiDepT
	useCatalog(t, catalog)
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	listed := catalogRequest(t, h)

	a := findEntry(listed, apiDepA)
	require.NotNil(t, a)
	require.NotNil(t, a.SupersededBy, "a visible successor is reported")
	assert.Equal(t, apiDepN, a.SupersededBy.ID)
	assert.Equal(t, "No geomodel", a.SupersededBy.Name)

	n := findEntry(listed, apiDepN)
	require.NotNil(t, n)
	assert.Nil(t, n.SupersededBy, "a hidden successor is not reported, so the gallery never points at a model it does not show")

	p := findEntry(listed, apiDepP)
	require.NotNil(t, p)
	assert.Nil(t, p.SupersededBy)
}

// TestGetModelCatalog_Documents pins the wiring of the document registry: an
// entry reports the documents of its own repo only, with the agreement flag, and
// depending on a gated entry adds none.
func TestGetModelCatalog_Documents(t *testing.T) {
	const gatedID, dependentID = "gated", "dependent"
	useCatalog(t, []classifier.CatalogEntry{
		{
			ID: gatedID, Name: "Gated geomodel", Version: "3.0", Category: classifier.CategoryGeomodel,
			GeomodelVersion: "v3", HuggingFaceRepo: "tphakala/BirdNET-Geomodel",
			Files: []classifier.CatalogFile{
				apiFile(classifier.RoleGeomodelModel, "geo.onnx", apiSizeGeo),
				apiFile(classifier.RoleGeomodelLabels, "geo.txt", apiSizeGeoLbl),
			},
		},
		{
			ID: dependentID, Name: "Dependent", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/d", DependsOn: []string{gatedID},
			Files: []classifier.CatalogFile{apiFile(classifier.RoleModel, "d.onnx", apiSizeModel)},
		},
	})
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	catalog := catalogRequest(t, h)

	gated := findEntry(catalog, gatedID)
	require.NotNil(t, gated)
	require.Len(t, gated.Documents, 2)
	var agreementKinds []string
	for _, d := range gated.Documents {
		assert.Equal(t, "tphakala/BirdNET-Geomodel", d.Repo)
		assert.NotEmpty(t, d.SHA256, "a hosted document carries its digest")
		if d.RequiresAgreement {
			agreementKinds = append(agreementKinds, d.Kind)
		}
	}
	assert.Equal(t, []string{classifier.DocumentKindAcceptableUse}, agreementKinds)

	dependent := findEntry(catalog, dependentID)
	require.NotNil(t, dependent)
	assert.Empty(t, dependent.Documents, "a dependency's documents are not the entry's own")
}

// TestGetModelCatalog_DocumentsWireShape pins the JSON form: an external document
// carries url and no sha256, and an entry without documents has no documents key.
func TestGetModelCatalog_DocumentsWireShape(t *testing.T) {
	const externalID, hostedID, plainID = "external", "hosted", "plain"
	useCatalog(t, []classifier.CatalogEntry{
		{
			ID: externalID, Name: "External licence", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "tphakala/Perch-v2-Models",
			Files:           []classifier.CatalogFile{apiFile(classifier.RoleModel, "e.onnx", apiSizeModel)},
		},
		{
			ID: hostedID, Name: "Hosted policy", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "tphakala/BirdNET-Plus-V3.0",
			Files:           []classifier.CatalogFile{apiFile(classifier.RoleModel, "h.onnx", apiSizeModel)},
		},
		{
			ID: plainID, Name: "Plain", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/p",
			Files:           []classifier.CatalogFile{apiFile(classifier.RoleModel, "p.onnx", apiSizeModel)},
		},
	})
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	e := echo.New()
	rec := httptest.NewRecorder()
	ctx := e.NewContext(httptest.NewRequest(http.MethodGet, "/api/v2/models/catalog", http.NoBody), rec)
	require.NoError(t, h.GetModelCatalog(ctx))
	var resp struct {
		Catalog []map[string]any `json:"catalog"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	byID := make(map[string]map[string]any, len(resp.Catalog))
	for _, entry := range resp.Catalog {
		id, isString := entry["id"].(string)
		require.True(t, isString, "catalog entry id is a string")
		byID[id] = entry
	}
	require.Contains(t, byID, externalID)
	require.Contains(t, byID, hostedID)
	require.Contains(t, byID, plainID)

	docs, ok := byID[externalID]["documents"].([]any)
	require.True(t, ok, "an entry with a licence reports documents")
	require.Len(t, docs, 1)
	doc, isObject := docs[0].(map[string]any)
	require.True(t, isObject, "a document is a JSON object")
	assert.Equal(t, "tphakala/Perch-v2-Models", doc["repo"])
	assert.Equal(t, "license", doc["kind"])
	assert.Equal(t, false, doc["requiresAgreement"])
	assert.NotEmpty(t, doc["url"], "an external document carries its url")
	assert.NotContains(t, doc, "sha256", "an external document has no digest")

	hostedDocs, ok := byID[hostedID]["documents"].([]any)
	require.True(t, ok, "a hosted entry reports documents")
	require.NotEmpty(t, hostedDocs)
	for _, raw := range hostedDocs {
		hosted, isHostedObject := raw.(map[string]any)
		require.True(t, isHostedObject, "a document is a JSON object")
		assert.NotEmpty(t, hosted["sha256"], "a hosted document carries its digest")
		assert.NotContains(t, hosted, "url", "a hosted document has no external url")
		assert.NotContains(t, hosted, "path", "the repo-relative path is not exposed")
		assert.NotContains(t, hosted, "revision", "the pinned revision is not exposed")
	}

	assert.NotContains(t, byID[plainID], "documents", "an entry without documents omits the key")
}
