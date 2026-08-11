package models

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/auth"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
)

// stubAuth is a minimal auth.Service double that answers only IsAuthenticated.
// It embeds the interface so it satisfies auth.Service without spelling out the
// other methods; the catalog handler calls only IsAuthenticated, so the nil
// embedded interface is never dereferenced.
type stubAuth struct {
	auth.Service
	authenticated bool
	called        bool
}

func (s *stubAuth) IsAuthenticated(echo.Context) bool {
	s.called = true
	return s.authenticated
}

// amd64ONNXProfile is a synthetic host profile: 64-bit x86 with the ONNX Runtime
// backend available and ample RAM. Its Capabilities() are {x86-64, onnxruntime-cpu}.
func amd64ONNXProfile() hwprofile.Profile {
	return hwprofile.Profile{
		Arch:          "amd64",
		TotalRAMBytes: 16 * 1024 * 1024 * 1024,
		Backends:      hwprofile.Backends{ONNX: hwprofile.BackendStatus{Available: true}},
	}
}

// catalogRequest issues GET /models/catalog against the handler and returns the
// decoded catalog.
func catalogRequest(t *testing.T, h *Handler) []CatalogEntryResponse {
	t.Helper()
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
	return resp.Catalog
}

func findEntry(catalog []CatalogEntryResponse, id string) *CatalogEntryResponse {
	for i := range catalog {
		if catalog[i].ID == id {
			return &catalog[i]
		}
	}
	return nil
}

func findVariant(entry *CatalogEntryResponse, id string) *CatalogVariantResponse {
	for i := range entry.Variants {
		if entry.Variants[i].ID == id {
			return &entry.Variants[i]
		}
	}
	return nil
}

func TestGetModelCatalog_MarksRecommendedVariant(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil) // nil authService -> enrichment allowed
	h.hardwareProfile = func(string) hwprofile.Profile { return amd64ONNXProfile() }

	perch := findEntry(catalogRequest(t, h), "perch-v2")
	require.NotNil(t, perch)
	assert.Equal(t, "fp32", perch.RecommendedVariantID, "fp32 is the recommended variant on amd64 with ONNX only")

	fp32 := findVariant(perch, "fp32")
	require.NotNil(t, fp32)
	assert.True(t, fp32.Recommended)
	assert.True(t, fp32.Compatible)
	require.NotEmpty(t, fp32.Reasons, "the recommended variant carries a why-line reason")
	assert.Equal(t, recommend.ReasonBackendRecommended, fp32.Reasons[0].Code, "fp32 runs on its recommended ONNX backend")

	// Exactly one recommended variant per entry.
	recommended := 0
	for _, v := range perch.Variants {
		if v.Recommended {
			recommended++
		}
	}
	assert.Equal(t, 1, recommended)
}

func TestGetModelCatalog_BlockedVariantCarriesBlockers(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil)
	h.hardwareProfile = func(string) hwprofile.Profile { return amd64ONNXProfile() }

	perch := findEntry(catalogRequest(t, h), "perch-v2")
	require.NotNil(t, perch)

	int8Variant := findVariant(perch, "int8-arm")
	require.NotNil(t, int8Variant, "the aarch64-only variant is still listed, just blocked")
	assert.False(t, int8Variant.Compatible, "int8-arm requires aarch64, unavailable on amd64")
	assert.False(t, int8Variant.Recommended)
	require.NotEmpty(t, int8Variant.Blockers)
	assert.Equal(t, recommend.BlockerArchUnsupported, int8Variant.Blockers[0].Code)
}

func TestGetModelCatalog_FlatEntriesUnchanged(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil)
	h.hardwareProfile = func(string) hwprofile.Profile { return amd64ONNXProfile() }

	catalog := catalogRequest(t, h)
	// A geomodel is a flat (variant-less) entry.
	geomodel := findEntry(catalog, "birdnet-geomodel-v3")
	require.NotNil(t, geomodel, "the geomodel entry must be present")
	assert.Nil(t, geomodel.Variants, "flat entries carry no variants")
	assert.Empty(t, geomodel.RecommendedVariantID, "flat entries carry no recommendation")
}

func TestGetModelCatalog_AuthGatedForUnauthenticated(t *testing.T) {
	core := apitest.NewCore(t)
	stub := &stubAuth{authenticated: false}
	h := New(core, stub)
	h.hardwareProfile = func(string) hwprofile.Profile {
		t.Fatal("hardware profile must not be probed for an unauthenticated request")
		return hwprofile.Profile{}
	}

	perch := findEntry(catalogRequest(t, h), "perch-v2")
	require.NotNil(t, perch)
	assert.True(t, stub.called, "the auth gate must be consulted")
	assert.Empty(t, perch.RecommendedVariantID, "no recommendation leaks to an unauthenticated request")
	for _, v := range perch.Variants {
		assert.False(t, v.Recommended, "variant %s must not be marked recommended", v.ID)
		assert.True(t, v.Compatible, "variant %s reports the neutral compatible=true", v.ID)
		assert.Empty(t, v.Reasons, "variant %s leaks no reasons", v.ID)
		assert.Empty(t, v.Blockers, "variant %s leaks no blockers", v.ID)
	}
}

func TestGetModelCatalog_AuthenticatedGetsRecommendations(t *testing.T) {
	core := apitest.NewCore(t)
	stub := &stubAuth{authenticated: true}
	h := New(core, stub)
	h.hardwareProfile = func(string) hwprofile.Profile { return amd64ONNXProfile() }

	perch := findEntry(catalogRequest(t, h), "perch-v2")
	require.NotNil(t, perch)
	assert.True(t, stub.called)
	assert.Equal(t, "fp32", perch.RecommendedVariantID, "an authenticated request receives the recommendation")
}
