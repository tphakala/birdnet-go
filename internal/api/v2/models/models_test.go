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
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestModelsRouteRegistration verifies the models handler registers exactly the
// model endpoints, with the same methods and paths the monolithic facade used
// before the domain was extracted.
func TestModelsRouteRegistration(t *testing.T) {
	e := echo.New()
	core := apitest.NewCore(t, apitest.WithEcho(e))
	h := New(core, nil)

	h.RegisterRoutes(core.Group)

	expectedRoutes := []string{
		"GET /api/v2/models",
		"GET /api/v2/models/catalog",
		"GET /api/v2/models/regions",
		"GET /api/v2/models/regions/:slug/map",
		"GET /api/v2/models/installed",
		"POST /api/v2/models/install/:id",
		"POST /api/v2/models/reinstall/:id",
		"DELETE /api/v2/models/installed/:id",
		"GET /api/v2/models/install/:id/progress",
	}
	apitest.AssertRoutesRegistered(t, e, expectedRoutes)
}

// TestListModels_IncludesRegistryID verifies that ListModels returns, for every
// enabled model, both the config alias (id) and the classifier registry ID
// (registryId). The frontend joins registryId against the registry-ID
// defaultTargets served by GET /api/v2/system/inference to map default analysis
// targets back to the config aliases the source editors use (model de-privilege
// epic, Phase 4).
func TestListModels_IncludesRegistryID(t *testing.T) {
	// PublishTestSettings mutates the process-global settings snapshot, so this
	// test must not run in parallel. Publish AFTER NewCore, which installs its own
	// default snapshot, so ListModels (which reads conf.GetSettings()) observes the
	// enabled model list below.
	core := apitest.NewCore(t)
	h := New(core, nil)

	settings := &conf.Settings{}
	settings.Models.Enabled = []string{conf.ModelIDBirdNET}
	apitest.PublishTestSettings(t, settings)

	e := echo.New()
	req := httptest.NewRequest(http.MethodGet, "/api/v2/models", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, h.ListModels(ctx))
	require.Equal(t, http.StatusOK, rec.Code)

	// Pin the on-the-wire key name: decoding into ModelListItem alone would
	// tolerate a JSON-tag typo symmetrically, so assert the literal key the
	// frontend join reads is present in the raw body.
	assert.Contains(t, rec.Body.String(), `"registryId"`)

	var items []ModelListItem
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
	require.NotEmpty(t, items, "enabling birdnet must list at least one model")

	var birdnet *ModelListItem
	for i := range items {
		assert.NotEmpty(t, items[i].RegistryID, "every listed model must carry a registryId join key (id=%q)", items[i].ID)
		if items[i].ID == conf.ModelIDBirdNET {
			birdnet = &items[i]
		}
	}
	require.NotNil(t, birdnet, "the enabled birdnet alias must be listed")
	assert.Equal(t, classifier.RegistryIDBirdNETV24, birdnet.RegistryID,
		"birdnet alias must map to the BirdNET v2.4 registry ID")
}

// TestListModels_LabelsFromLiveRuntime verifies that the backend and precision
// come from the loaded instance (the runtime seam), not the static registry, that
// the name carries no backend suffix, that a model the seam does not list gets no
// runtime fields, and that an anonymous request on an auth-enabled instance does
// not receive the host-derived fields.
func TestListModels_LabelsFromLiveRuntime(t *testing.T) {
	// Mutates the process-global settings snapshot: not parallel.
	onnxInt8 := map[string]listedModelRuntime{classifier.RegistryIDBirdNETV24: {backend: "ONNX", precision: "INT8"}}
	tests := []struct {
		name          string
		authenticated bool
		withAuth      bool
		want          map[string]listedModelRuntime // by config alias
	}{
		{
			name: "loaded model reports the live backend and precision, an unlisted one none",
			want: map[string]listedModelRuntime{
				conf.ModelIDBirdNET: {backend: "ONNX", precision: "INT8"},
				conf.ModelIDPerchV2: {},
			},
		},
		{
			name:     "anonymous request on an auth-enabled instance gets no runtime fields",
			withAuth: true,
			want: map[string]listedModelRuntime{
				conf.ModelIDBirdNET: {},
				conf.ModelIDPerchV2: {},
			},
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			core := apitest.NewCore(t)
			var authSvc auth.Service
			if tt.withAuth {
				authSvc = &stubAuth{authenticated: tt.authenticated}
			}
			h := New(core, authSvc)
			h.modelRuntime = func() map[string]listedModelRuntime { return onnxInt8 }

			settings := &conf.Settings{}
			settings.Models.Enabled = []string{conf.ModelIDBirdNET, conf.ModelIDPerchV2}
			apitest.PublishTestSettings(t, settings)

			e := echo.New()
			req := httptest.NewRequest(http.MethodGet, "/api/v2/models", http.NoBody)
			rec := httptest.NewRecorder()
			require.NoError(t, h.ListModels(e.NewContext(req, rec)))

			var items []ModelListItem
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &items))
			byID := make(map[string]ModelListItem, len(items))
			for i := range items {
				byID[items[i].ID] = items[i]
			}
			for id, want := range tt.want {
				item, ok := byID[id]
				require.True(t, ok, "model %q must be listed", id)
				assert.Equal(t, want, listedModelRuntime{backend: item.Backend, precision: item.Precision}, "runtime of %q", id)
				assert.NotContains(t, item.Name, "(", "name of %q must carry no backend suffix", id)
			}
		})
	}
}

// TestMergeModelRuntime verifies that the live instance's backend and precision
// replace the loaded model's own info only when non-empty, and that a missing
// instance source leaves the info values in place.
func TestMergeModelRuntime(t *testing.T) {
	t.Parallel()
	infos := []classifier.ModelInfo{
		{ID: "a", Backend: "TFLite", Quantization: classifier.QuantizationFP32},
		{ID: "b", Backend: "ONNX", Quantization: classifier.QuantizationFP16},
	}
	live := func(id string) (backend, precision string) {
		if id == "a" {
			return "OpenVINO", ""
		}
		return "", ""
	}

	withLive := mergeModelRuntime(infos, live)
	assert.Equal(t, listedModelRuntime{backend: "OpenVINO", precision: "FP32"}, withLive["a"],
		"a live backend wins and an empty live precision keeps the info value")
	assert.Equal(t, listedModelRuntime{backend: "ONNX", precision: "FP16"}, withLive["b"],
		"an instance reporting nothing keeps the info values")

	withoutLive := mergeModelRuntime(infos, nil)
	assert.Equal(t, listedModelRuntime{backend: "TFLite", precision: "FP32"}, withoutLive["a"])
}

// TestLiveModelRuntime_NothingLoaded verifies that no model is reported as loaded
// without a model manager, or with a manager that has no orchestrator.
func TestLiveModelRuntime_NothingLoaded(t *testing.T) {
	core := apitest.NewCore(t)
	core.ModelManager = nil
	assert.Empty(t, New(core, nil).liveModelRuntime())

	core.ModelManager = classifier.NewModelManager(t.TempDir(), nil, nil)
	assert.Empty(t, New(core, nil).liveModelRuntime())
}

// TestInstallModel_RejectsHiddenEntries verifies that hidden, foundation-only
// catalog entries (e.g. bsg-finland) cannot be installed or reinstalled by ID.
func TestInstallModel_RejectsHiddenEntries(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil)
	e := echo.New()

	hiddenIDs := []string{"bsg-finland"}
	for _, id := range hiddenIDs {
		t.Run(id, func(t *testing.T) {
			// Install must be rejected with 404 before touching the model manager.
			req := httptest.NewRequest(http.MethodPost, "/api/v2/models/install/"+id, http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("id")
			ctx.SetParamValues(id)
			require.NoError(t, h.InstallModel(ctx))
			assert.Equal(t, http.StatusNotFound, rec.Code, "install of hidden entry %q must be rejected", id)
			assert.Contains(t, rec.Body.String(), "not available for installation")

			// Reinstall must be rejected the same way.
			req = httptest.NewRequest(http.MethodPost, "/api/v2/models/reinstall/"+id, http.NoBody)
			rec = httptest.NewRecorder()
			ctx = e.NewContext(req, rec)
			ctx.SetParamNames("id")
			ctx.SetParamValues(id)
			require.NoError(t, h.ReinstallModel(ctx))
			assert.Equal(t, http.StatusNotFound, rec.Code, "reinstall of hidden entry %q must be rejected", id)
			assert.Contains(t, rec.Body.String(), "not available for installation")
		})
	}
}

// TestReinstallModel_RejectsPermanentEntry verifies that the permanent, now-visible
// BirdNET v2.4 entry cannot be reinstalled: its BuiltIn baseline has no downloadable
// files, and a DFT variant is acquired by swapping to it via install, not reinstall.
func TestReinstallModel_RejectsPermanentEntry(t *testing.T) {
	core := apitest.NewCore(t)
	h := New(core, nil)
	e := echo.New()

	req := httptest.NewRequest(http.MethodPost, "/api/v2/models/reinstall/birdnet-v2.4", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("birdnet-v2.4")
	require.NoError(t, h.ReinstallModel(ctx))
	assert.Equal(t, http.StatusConflict, rec.Code, "reinstall of the permanent v2.4 entry must be refused")
	assert.Contains(t, rec.Body.String(), "cannot be reinstalled")
}
