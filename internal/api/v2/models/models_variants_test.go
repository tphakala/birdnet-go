package models

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
)

// TestGetModelCatalog_EmitsVariants verifies the catalog response exposes the
// nested variants[] for a multi-variant entry, with per-variant sizes and a single
// default variant. Regional tile generation is asserted in the classifier package.
func TestGetModelCatalog_EmitsVariants(t *testing.T) {
	h := recommendHandler(t, nil, amd64ONNXProfile)

	rec := findEntry(catalogRequest(t, h), apiRecID)
	require.NotNil(t, rec)
	require.Len(t, rec.Variants, 2)

	sizes := make(map[string]int64, len(rec.Variants))
	for _, v := range rec.Variants {
		sizes[v.ID] = v.SizeBytes
		assert.Equal(t, v.ID == apiVarFP32, v.Default, "only fp32 is the default variant (%s)", v.ID)
	}
	assert.Equal(t, map[string]int64{apiVarFP32: apiSizeFP32Model, apiVarInt8ARM: apiSizeInt8Model}, sizes)
}

// Synthetic ids for the regional variant projection test.
const (
	apiRegionalID      = "api-regional"
	apiRegionalSlug    = "nordic"
	apiVarGlobal       = "fp32"
	apiVarRegional     = "fp32@nordic"
	apiSizeRegionalMdl = 400
)

// TestGetModelCatalog_EmitsRegionalVariant verifies a regional variant reaches the
// response with its region and never as the default, next to a global default
// that carries no region.
func TestGetModelCatalog_EmitsRegionalVariant(t *testing.T) {
	useCatalog(t, []classifier.CatalogEntry{{
		ID: apiRegionalID, Name: "Regional", Version: "1.0", Category: classifier.CategoryBird,
		HuggingFaceRepo: "t/regional",
		Variants: []classifier.CatalogVariant{
			{ID: apiVarGlobal, Precision: "fp32", Default: true, Files: []classifier.CatalogFile{apiFile(classifier.RoleModel, "global.onnx", apiSizeFP32Model)}},
			{ID: apiVarRegional, Precision: "fp32", Region: apiRegionalSlug, Files: []classifier.CatalogFile{apiFile(classifier.RoleModel, "regional.onnx", apiSizeRegionalMdl)}},
		},
	}})
	h := dependencyHandler(t, classifier.NewModelManager(t.TempDir(), nil, nil))

	entry := findEntry(catalogRequest(t, h), apiRegionalID)
	require.NotNil(t, entry)

	global := findVariant(entry, apiVarGlobal)
	require.NotNil(t, global)
	assert.Empty(t, global.Region, "the global variant carries no region")
	assert.True(t, global.Default)

	regional := findVariant(entry, apiVarRegional)
	require.NotNil(t, regional, "the regional variant must reach the catalog API")
	assert.Equal(t, apiRegionalSlug, regional.Region)
	assert.False(t, regional.Default, "a regional variant is never the default")
	assert.Equal(t, int64(apiSizeRegionalMdl), regional.SizeBytes)
}

// TestInstallModel_RejectsUnknownVariant verifies the install handler rejects a
// variant the entry does not offer with 400, synchronously, before any download.
func TestInstallModel_RejectsUnknownVariant(t *testing.T) {
	core := apitest.NewCore(t)
	core.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	h := New(core, nil)
	e := echo.New()

	body := strings.NewReader(`{"variantId":"does-not-exist"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/perch-v2", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("perch-v2")

	require.NoError(t, h.InstallModel(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "unknown variant")
}

// TestInstallModel_RejectsMalformedBody verifies a malformed JSON body is
// rejected with 400 (an empty body, by contrast, is tolerated and installs the
// default variant).
func TestInstallModel_RejectsMalformedBody(t *testing.T) {
	core := apitest.NewCore(t)
	core.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	h := New(core, nil)
	e := echo.New()

	body := strings.NewReader(`{"variantId":`) // truncated JSON
	req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/perch-v2", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("perch-v2")

	require.NoError(t, h.InstallModel(ctx))
	assert.Equal(t, http.StatusBadRequest, rec.Code)
	assert.Contains(t, rec.Body.String(), "invalid request body")
}

// TestBuildVariantResponses_LegacyHiddenUnlessInstalled verifies a superseded
// (Legacy) variant is hidden from the gallery unless it is the installed one, and
// that the Installed flag is set for the on-disk variant.
func TestBuildVariantResponses_LegacyHiddenUnlessInstalled(t *testing.T) {
	entry := &classifier.CatalogEntry{
		ID: "x",
		Variants: []classifier.CatalogVariant{
			{ID: "cur", Default: true, Files: []classifier.CatalogFile{{LocalName: "cur.onnx", Role: classifier.RoleModel, SizeBytes: 10}}},
			{ID: "old", Legacy: true, Files: []classifier.CatalogFile{{LocalName: "old.onnx", Role: classifier.RoleModel, SizeBytes: 5}}},
		},
	}

	ids := func(vs []CatalogVariantResponse) []string {
		out := make([]string, 0, len(vs))
		for _, v := range vs {
			out = append(out, v.ID)
		}
		return out
	}

	// Not installed: the legacy variant is hidden, none is marked installed.
	notInstalled := buildVariantResponses(entry, false, "", nil, "")
	assert.Contains(t, ids(notInstalled), "cur")
	assert.NotContains(t, ids(notInstalled), "old", "a legacy variant is hidden when not installed")
	for _, v := range notInstalled {
		assert.False(t, v.Installed)
		assert.NotEmpty(t, v.HardwareClass, "buildVariantResponses must populate a hardware-class token on every variant")
	}

	// The legacy variant is the installed one: it stays visible and is flagged.
	withLegacy := buildVariantResponses(entry, true, "old", nil, "")
	assert.Contains(t, ids(withLegacy), "old", "a legacy variant stays visible when it is installed")
	for _, v := range withLegacy {
		switch v.ID {
		case "old":
			assert.True(t, v.Installed, "the installed legacy variant must be flagged installed")
		case "cur":
			assert.False(t, v.Installed)
		}
	}
}
