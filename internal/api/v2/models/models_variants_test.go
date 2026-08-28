package models

import (
	"encoding/json"
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
// nested variants[] for a multi-variant entry (Perch v2), with sizes and a single
// default variant.
func TestGetModelCatalog_EmitsVariants(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil)
	e := echo.New()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/models/catalog", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	require.NoError(t, h.GetModelCatalog(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	var resp struct {
		Catalog []CatalogEntryResponse `json:"catalog"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))

	var perch *CatalogEntryResponse
	for i := range resp.Catalog {
		if resp.Catalog[i].ID == "perch-v2" {
			perch = &resp.Catalog[i]
			break
		}
	}
	require.NotNil(t, perch, "perch-v2 must be present in the visible catalog")
	// perch-v2 exposes its 3 global variants plus the generated regional tiles.
	// The exact regional count is asserted in the classifier package; here we only
	// require the globals plus at least one regional variant reaching the API.
	require.GreaterOrEqual(t, len(perch.Variants), 3, "perch-v2 must expose at least its three global variants")

	byID := make(map[string]CatalogVariantResponse, len(perch.Variants))
	defaults := 0
	for _, v := range perch.Variants {
		byID[v.ID] = v
		if v.Default {
			defaults++
		}
		assert.Positive(t, v.SizeBytes, "variant %s must report a size", v.ID)
	}
	assert.Contains(t, byID, "fp32")
	assert.Contains(t, byID, "int8-arm")
	assert.Contains(t, byID, "int8-arm@nordic", "regional tiles must reach the catalog API")
	assert.False(t, byID["int8-arm@nordic"].Default, "regional tiles must never be the default")
	assert.True(t, byID["fp32"].Default, "fp32 must be the default variant")
	assert.Equal(t, 1, defaults, "exactly one variant may be the default")
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
