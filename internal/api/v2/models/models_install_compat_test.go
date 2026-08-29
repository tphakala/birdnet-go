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
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
	"github.com/tphakala/birdnet-go/internal/logger/logtest"
)

// TestRequestedVariantCompatibility verifies the install gate's compatibility
// computation reuses the recommender correctly across the cases the handler
// depends on: an incompatible variant (int8-arm on amd64), a compatible one,
// the empty-variant default resolution, and a flat entry that is never gated.
// amd64ONNXProfile lives in models_recommend_test.go (same package).
func TestRequestedVariantCompatibility(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return amd64ONNXProfile() }

	entry, ok := classifier.GetCatalogEntry("perch-v2")
	require.True(t, ok, "perch-v2 must exist in the embedded catalog")

	ort := inference.ORTStatus{Available: true}

	// int8-arm requires aarch64; on an amd64 host it is gated and incompatible,
	// with an arch blocker naming the requirement.
	compatible, gated, blockers, arch := h.requestedVariantCompatibility(&entry, "int8-arm", ort)
	assert.True(t, gated, "a variant-bearing entry is gated")
	assert.False(t, compatible, "int8-arm is incompatible on amd64")
	require.NotEmpty(t, blockers, "an incompatible variant must carry blockers")
	assert.Equal(t, "amd64", arch)

	// fp32 (the default) runs on amd64 with ONNX available.
	compatible, gated, _, _ = h.requestedVariantCompatibility(&entry, "fp32", ort)
	assert.True(t, gated)
	assert.True(t, compatible, "fp32 is compatible on amd64 ONNX")

	// An empty variant id resolves to the default variant (fp32), which is compatible.
	compatible, gated, _, _ = h.requestedVariantCompatibility(&entry, "", ort)
	assert.True(t, gated)
	assert.True(t, compatible, "the default variant resolves and is compatible")

	// A flat entry (no variants) is never gated: there is nothing to select.
	flat := classifier.CatalogEntry{ID: "flat-test"}
	_, gated, _, _ = h.requestedVariantCompatibility(&flat, "", ort)
	assert.False(t, gated, "a flat entry with no variants is not gated")
}

// TestInstallModel_RejectsIncompatibleVariant verifies the install handler
// rejects a hardware-incompatible variant with 409 by default, synchronously and
// before any download, naming the variant, the hardware, and the override flag.
func TestInstallModel_RejectsIncompatibleVariant(t *testing.T) {
	core := apitest.NewCore(t)
	core.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return amd64ONNXProfile() }
	e := echo.New()

	body := strings.NewReader(`{"variantId":"int8-arm"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/perch-v2", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("perch-v2")

	require.NoError(t, h.InstallModel(ctx))
	assert.Equal(t, http.StatusConflict, rec.Code)
	body409 := rec.Body.String()
	assert.Contains(t, body409, "not compatible")
	assert.Contains(t, body409, "int8-arm")
	assert.Contains(t, body409, "allowIncompatible")
}

// TestInstallModel_AllowIncompatibleOverride verifies the explicit override flag
// bypasses the compatibility gate, accepts the install (202), AND emits the WARN
// whose whole purpose is to make the forced choice visible in logs and support
// dumps. Asserting the WARN (not just the 202) is what isolates the override
// branch: a gate that ignored allowIncompatible would also reach 202 on this
// input, but only the override branch logs the operation code. The buffer is
// swapped in before apitest.NewCore so the Core's APILogger (built from
// logger.Global() at construction) writes into it. The async download is
// cancelled by the apitest core cleanup (Cancel/Wait). Not parallel: logtest
// swaps the process-global logger.
func TestInstallModel_AllowIncompatibleOverride(t *testing.T) {
	buf := logtest.CaptureBuffer(t)
	core := apitest.NewCore(t)
	core.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return amd64ONNXProfile() }
	e := echo.New()

	body := strings.NewReader(`{"variantId":"int8-arm","allowIncompatible":true}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/perch-v2", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("perch-v2")

	require.NoError(t, h.InstallModel(ctx))
	assert.Equal(t, http.StatusAccepted, rec.Code, "an explicit override must accept the install")
	assert.Contains(t, buf.String(), "model_install_incompatible_override",
		"the override branch must log a WARN so the forced choice is visible in support dumps")
}

// TestInstallModel_AcceptsCompatibleVariant verifies the gate does NOT block a
// variant that is compatible with the detected hardware: a normal fp32 install on
// an amd64 ONNX host proceeds to 202 with no override flag. This pins the
// happy-path wiring (compatible => skip the gate => 202); without it a mutation
// that rejected compatible variants would leave the reject/override tests green
// while breaking every normal install. The async download is cancelled by the
// apitest core cleanup.
func TestInstallModel_AcceptsCompatibleVariant(t *testing.T) {
	core := apitest.NewCore(t)
	core.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile { return amd64ONNXProfile() }
	e := echo.New()

	body := strings.NewReader(`{"variantId":"fp32"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/perch-v2", body)
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("perch-v2")

	require.NoError(t, h.InstallModel(ctx))
	assert.Equal(t, http.StatusAccepted, rec.Code, "a compatible variant must not be gated")
}
