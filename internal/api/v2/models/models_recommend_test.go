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
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/classifier/recommend"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
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

// availableORT is an ortProbe seam that reports a usable ONNX Runtime whatever
// the host has installed.
func availableORT(string) inference.ORTStatus { return inference.ORTStatus{Available: true} }

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

// Synthetic catalog ids, variant ids and sizes for the recommendation tests.
const (
	apiRecID      = "api-rec"
	apiFlatID     = "api-flat"
	apiVarFP32    = "fp32"
	apiVarInt8ARM = "int8-arm"

	apiSizeFP32Model = 900
	apiSizeInt8Model = 250
)

// apiRecommendCatalog is a synthetic catalog: api-rec has two variants (fp32 default,
// runnable on ONNX Runtime CPU; int8-arm, which needs aarch64) and api-flat is a
// variant-less entry. api-rec is listed first so a sort that does not move it fails.
func apiRecommendCatalog() []classifier.CatalogEntry {
	onnxBackends := map[string]classifier.BackendSupport{
		hwprofile.CapONNXRuntimeCPU: {Supported: true, Recommended: true},
	}
	onnxRequirement := []string{hwprofile.CapONNXRuntimeCPU}
	return []classifier.CatalogEntry{
		{
			ID: apiRecID, Name: "Recommendable", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/rec",
			Variants: []classifier.CatalogVariant{
				{
					ID: apiVarFP32, Precision: "fp32", Default: true,
					Requirements: classifier.VariantRequirements{Backends: onnxRequirement},
					Backends:     onnxBackends,
					Files:        []classifier.CatalogFile{apiFile(classifier.RoleModel, "rec-fp32.onnx", apiSizeFP32Model)},
				},
				{
					ID: apiVarInt8ARM, Precision: "int8",
					Requirements: classifier.VariantRequirements{Arch: []string{hwprofile.CapAArch64}, Backends: onnxRequirement},
					Backends:     onnxBackends,
					Files:        []classifier.CatalogFile{apiFile(classifier.RoleModel, "rec-int8.onnx", apiSizeInt8Model)},
				},
			},
		},
		{
			ID: apiFlatID, Name: "Flat", Version: "1.0", Category: classifier.CategoryBird,
			HuggingFaceRepo: "t/flat",
			Files: []classifier.CatalogFile{
				apiFile(classifier.RoleModel, "flat.onnx", apiSizeModel),
				apiFile(classifier.RoleLabels, "flat.txt", apiSizeLabels),
			},
		},
	}
}

// recommendHandler activates the synthetic recommendation catalog and returns a
// handler whose host profile (from profile) and ONNX Runtime probe are fixed. The
// catalog is global, so callers are serial.
func recommendHandler(t *testing.T, authSvc auth.Service, profile func() hwprofile.Profile) *Handler {
	t.Helper()
	useCatalog(t, apiRecommendCatalog())
	h := New(apitest.NewCore(t), authSvc)
	h.ortProbe = availableORT
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile() }
	return h
}

func TestGetModelCatalog_RecommendsAndBlocksVariants(t *testing.T) {
	h := recommendHandler(t, nil, amd64ONNXProfile) // nil authService -> enrichment allowed

	rec := findEntry(catalogRequest(t, h), apiRecID)
	require.NotNil(t, rec)
	assert.Equal(t, apiVarFP32, rec.RecommendedVariantID, "fp32 is the recommended variant on amd64 with ONNX only")

	fp32 := findVariant(rec, apiVarFP32)
	require.NotNil(t, fp32)
	assert.True(t, fp32.Recommended)
	assert.True(t, fp32.Compatible)
	require.NotEmpty(t, fp32.Reasons, "the recommended variant carries a why-line reason")
	assert.Equal(t, recommend.ReasonBackendRecommended, fp32.Reasons[0].Code, "fp32 runs on its recommended ONNX backend")

	// Exactly one recommended variant per entry.
	recommended := 0
	for _, v := range rec.Variants {
		if v.Recommended {
			recommended++
		}
	}
	assert.Equal(t, 1, recommended)

	int8Variant := findVariant(rec, apiVarInt8ARM)
	require.NotNil(t, int8Variant, "the aarch64-only variant is still listed, just blocked")
	assert.False(t, int8Variant.Compatible, "int8-arm requires aarch64, unavailable on amd64")
	assert.False(t, int8Variant.Recommended)
	require.NotEmpty(t, int8Variant.Blockers)
	assert.Equal(t, recommend.BlockerArchUnsupported, int8Variant.Blockers[0].Code)
}

// TestGetModelCatalog_BirdNETv24RecommendedVariant covers the permanent BirdNET
// v2.4 entry now that it is visible and carries a BuiltIn baseline plus two
// DFT-truncated builds. The recommended variant per host class drives whether the
// gallery offers an in-place optimize swap: when the host's recommended variant is
// the installed one there is no offer, and the BuiltIn baseline must not win the
// backend/size tie-break on an ORT-capable host (its ONNX backend is not marked
// recommended precisely so a real DFT build beats it there).
func TestGetModelCatalog_BirdNETv24RecommendedVariant(t *testing.T) {
	// aarch64 host with the ONNX Runtime and low RAM (1 GB): passes the 250 MB
	// variant floor but is tagged low-ram, and the arch-specific INT8 build applies.
	aarch64LowRAMONNX := hwprofile.Profile{
		Arch:          "arm64",
		TotalRAMBytes: 1 * 1024 * 1024 * 1024,
		Backends:      hwprofile.Backends{ONNX: hwprofile.BackendStatus{Available: true}},
	}
	// amd64 host with only TFLite linked (no ONNX Runtime): the DFT builds are ONNX
	// and blocked, so the embedded BuiltIn baseline is the only runnable variant.
	amd64TFLiteOnly := hwprofile.Profile{
		Arch:          "amd64",
		TotalRAMBytes: 16 * 1024 * 1024 * 1024,
		Backends:      hwprofile.Backends{TFLite: hwprofile.BackendStatus{Available: true}},
	}
	// amd64 host with OpenVINO including a GPU device: the FP32 DFT build runs on
	// OpenVINO, the INT8 build is aarch64-only, and the baseline has no OpenVINO path.
	amd64OpenVINOGPU := hwprofile.Profile{
		Arch:          "amd64",
		TotalRAMBytes: 16 * 1024 * 1024 * 1024,
		Backends: hwprofile.Backends{
			OpenVINO: hwprofile.OpenVINOStatus{Supported: true, Devices: []string{"GPU"}},
		},
	}

	cases := []struct {
		name    string
		profile hwprofile.Profile
		want    string
	}{
		{"amd64+ONNX -> fp32-dfttrunc", amd64ONNXProfile(), "fp32-dfttrunc"},
		{"tflite-only -> builtin baseline", amd64TFLiteOnly, "builtin"},
		{"aarch64 low-RAM ONNX -> int8-arm-dfttrunc", aarch64LowRAMONNX, "int8-arm-dfttrunc"},
		{"amd64 openvino-gpu -> fp32-dfttrunc", amd64OpenVINOGPU, "fp32-dfttrunc"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			core := apitest.NewCore(t)
			h := New(core, nil)
			profile := tc.profile
			h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return profile }

			v24 := findEntry(catalogRequest(t, h), "birdnet-v2.4")
			require.NotNil(t, v24, "birdnet-v2.4 must be visible in the catalog")
			assert.True(t, v24.Permanent, "birdnet-v2.4 must be marked permanent")
			assert.Equal(t, tc.want, v24.RecommendedVariantID, "recommended v2.4 variant for %s", tc.name)

			// Exactly one recommended variant, and it is the one named.
			recommended := 0
			for _, v := range v24.Variants {
				if v.Recommended {
					recommended++
					assert.Equal(t, tc.want, v.ID)
				}
			}
			assert.Equal(t, 1, recommended, "exactly one recommended v2.4 variant")

			// The built-in baseline variant is always present and flagged BuiltIn.
			builtin := findVariant(v24, "builtin")
			require.NotNil(t, builtin, "the built-in baseline variant must always be listed")
			assert.True(t, builtin.BuiltIn, "baseline variant must carry the BuiltIn flag")
			assert.Zero(t, builtin.SizeBytes, "the built-in baseline has no downloadable size")
		})
	}
}

func TestGetModelCatalog_AuthGatedForUnauthenticated(t *testing.T) {
	stub := &stubAuth{authenticated: false}
	h := recommendHandler(t, stub, func() hwprofile.Profile {
		t.Fatal("hardware profile must not be probed for an unauthenticated request")
		return hwprofile.Profile{}
	})

	rec := findEntry(catalogRequest(t, h), apiRecID)
	require.NotNil(t, rec)
	assert.True(t, stub.called, "the auth gate must be consulted")
	assert.Empty(t, rec.RecommendedVariantID, "no recommendation leaks to an unauthenticated request")
	require.Len(t, rec.Variants, 2)
	for _, v := range rec.Variants {
		assert.False(t, v.Recommended, "variant %s must not be marked recommended", v.ID)
		assert.True(t, v.Compatible, "variant %s reports the neutral compatible=true", v.ID)
		assert.Empty(t, v.Reasons, "variant %s leaks no reasons", v.ID)
		assert.Empty(t, v.Blockers, "variant %s leaks no blockers", v.ID)
	}
}

func TestGetModelCatalog_AuthenticatedGetsRecommendations(t *testing.T) {
	stub := &stubAuth{authenticated: true}
	h := recommendHandler(t, stub, amd64ONNXProfile)

	rec := findEntry(catalogRequest(t, h), apiRecID)
	require.NotNil(t, rec)
	assert.True(t, stub.called)
	assert.Equal(t, apiVarFP32, rec.RecommendedVariantID, "an authenticated request receives the recommendation")
}

func TestGetModelCatalog_RecommendedFirstOrdering(t *testing.T) {
	// A host with no usable inference backend: api-rec has all variants blocked and
	// is therefore not recommendable, while the flat entry (no variants) stays
	// recommendable. The fixture lists api-rec first, so the response order proves
	// the sort moved the recommendable entry ahead of it.
	h := recommendHandler(t, nil, func() hwprofile.Profile {
		return hwprofile.Profile{Arch: "amd64", TotalRAMBytes: 16 * 1024 * 1024 * 1024}
	})

	catalog := catalogRequest(t, h)
	require.Len(t, catalog, 2)
	assert.Equal(t, apiFlatID, catalog[0].ID, "the recommendable entry sorts ahead of the blocked one")
	assert.Equal(t, apiRecID, catalog[1].ID)
	assert.Empty(t, catalog[1].RecommendedVariantID, "api-rec has no recommendable variant on this host")
}
