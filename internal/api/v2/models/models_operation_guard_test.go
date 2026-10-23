package models

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/hwprofile"
	"github.com/tphakala/birdnet-go/internal/inference"
)

// guardWait bounds how long a guard test waits for a held download request.
const guardWait = 10 * time.Second

// streamHoldWait is how long a stream test watches for an early complete event.
const streamHoldWait = 300 * time.Millisecond

// guardVariantDFT is the DFT variant id of the synthetic primary entry.
const guardVariantDFT = "fp32-dfttrunc"

// guardCatalog is apiDependencyCatalog with the primary's DFT variant declared
// runnable on TFLite, so the ONNX Runtime gate does not fire in a test host.
func guardCatalog() []classifier.CatalogEntry {
	catalog := apiDependencyCatalog()
	for i := range catalog {
		if catalog[i].ID != apiDepP {
			continue
		}
		for j := range catalog[i].Variants {
			if catalog[i].Variants[j].ID == guardVariantDFT {
				catalog[i].Variants[j].Backends = map[string]classifier.BackendSupport{"tflite": {Supported: true}}
			}
		}
	}
	return catalog
}

// guardInstalledManager returns a manager with A, G and T recorded from files on disk.
func guardInstalledManager(t *testing.T) (mm *classifier.ModelManager, modelsDir string) {
	t.Helper()
	modelsDir = t.TempDir()
	writeFile(t, filepath.Join(modelsDir, apiDepA, "a.onnx"))
	writeFile(t, filepath.Join(modelsDir, apiDepA, "a.txt"))
	writeFile(t, filepath.Join(modelsDir, "shared", "geo.onnx"))
	writeFile(t, filepath.Join(modelsDir, "shared", "geo.txt"))
	writeFile(t, filepath.Join(modelsDir, "shared", "taxonomy.csv"))
	mm = classifier.NewModelManager(modelsDir, nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(apiDepA))
	return mm, modelsDir
}

// holdSlot takes the manager's operation slot for an uninstall of G until cleanup.
func holdSlot(t *testing.T, mm *classifier.ModelManager) {
	t.Helper()
	lease, err := mm.BeginOperation(classifier.OperationUninstall, apiDepG)
	require.NoError(t, err)
	t.Cleanup(lease.Release)
}

// guardRequest runs handler for method and id with an optional JSON body.
func guardRequest(t *testing.T, handler echo.HandlerFunc, method, id, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(method, "/api/v2/models/"+id, strings.NewReader(body))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	ctx := echo.New().NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues(id)
	require.NoError(t, handler(ctx))
	return rec
}

// requireOperationInProgress asserts rec is the 409 for a busy slot held by an
// operation of running (display name runningName).
func requireOperationInProgress(t *testing.T, rec *httptest.ResponseRecorder, running, runningName, operation string) {
	t.Helper()
	require.Equal(t, http.StatusConflict, rec.Code, rec.Body.String())
	var body struct {
		Code        int    `json:"code"`
		ErrorKey    string `json:"error_key"`
		ErrorParams struct {
			Name      string                `json:"name"`
			Operation string                `json:"operation"`
			Running   classifier.CatalogRef `json:"running"`
		} `json:"error_params"`
	}
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
	assert.Equal(t, http.StatusConflict, body.Code)
	assert.Equal(t, operationInProgressKey, body.ErrorKey)
	assert.Equal(t, runningName, body.ErrorParams.Name)
	assert.Equal(t, operation, body.ErrorParams.Operation)
	assert.Equal(t, running, body.ErrorParams.Running.ID)
}

func TestInstallModel_Returns409WhileAnotherOperationRuns(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	h := dependencyHandler(t, mm)
	holdSlot(t, mm)

	rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepA, "")

	requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
	h.Wait()
	assert.Nil(t, mm.GetDownloadState(apiDepA), "a refused install starts no background work")
}

func TestInstallModel_SwapReturns409WhileAnotherOperationRuns(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	mm.ScanInstalled()
	require.True(t, mm.IsInstalled(apiDepP), "the permanent entry is always installed")
	h := dependencyHandler(t, mm)
	holdSlot(t, mm)

	rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepP, `{"variantId":"`+guardVariantDFT+`","allowIncompatible":true}`)

	requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
	h.Wait()
	assert.Nil(t, mm.GetDownloadState(apiDepP), "a refused swap starts no background work")
}

func TestReinstallModel_Returns409WhileAnotherOperationRuns(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm, modelsDir := guardInstalledManager(t)
	h := dependencyHandler(t, mm)
	holdSlot(t, mm)

	rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, "")

	requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
	h.Wait()
	assert.Nil(t, mm.GetDownloadState(apiDepA))
	assert.FileExists(t, filepath.Join(modelsDir, apiDepA, "a.onnx"), "a refused reinstall touches no file")
}

func TestUninstallModel_Returns409WhileAnotherOperationRuns(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm, modelsDir := guardInstalledManager(t)
	h := dependencyHandler(t, mm)
	lease, err := mm.BeginOperation(classifier.OperationInstall, apiDepP)
	require.NoError(t, err)
	t.Cleanup(lease.Release)

	rec := guardRequest(t, h.UninstallModel, http.MethodDelete, apiDepA, "")

	requireOperationInProgress(t, rec, apiDepP, "Primary", "install")
	assert.True(t, mm.IsInstalled(apiDepA), "a refused uninstall keeps the model")
	assert.FileExists(t, filepath.Join(modelsDir, apiDepA, "a.onnx"))
}

// gatedHub is a download server that parks the first request until released and
// answers every request with 404.
type gatedHub struct {
	*httptest.Server
	reached chan struct{}
	release chan struct{}
	once    sync.Once
}

func newGatedHub(t *testing.T) *gatedHub {
	t.Helper()
	g := &gatedHub{reached: make(chan struct{}), release: make(chan struct{})}
	g.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		first := false
		g.once.Do(func() { first = true })
		if first {
			close(g.reached)
			select {
			case <-g.release:
			case <-r.Context().Done():
			}
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(g.Close)
	return g
}

func TestInstallModel_HoldsSlotUntilBackgroundInstallEnds(t *testing.T) {
	useCatalog(t, guardCatalog())
	hub := newGatedHub(t)
	core := apitest.NewCore(t, apitest.WithSettingsFunc(func(s *conf.Settings) {
		s.BirdNET.HuggingFaceEndpoint = hub.URL
	}))
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	core.ModelManager = mm
	h := New(core, nil)
	h.hardwareProfile = func(inference.ORTStatus) hwprofile.Profile {
		return hwprofile.Profile{Arch: "amd64", TotalRAMBytes: 16 << 30}
	}

	rec := guardRequest(t, h.InstallModel, http.MethodPost, apiDepA, "")
	require.Equal(t, http.StatusAccepted, rec.Code, rec.Body.String())
	select {
	case <-hub.reached:
	case <-time.After(guardWait):
		require.FailNow(t, "the background install never requested a file")
	}

	rec = guardRequest(t, h.InstallModel, http.MethodPost, apiDepG, "")
	requireOperationInProgress(t, rec, apiDepA, "Dependent A", "install")
	rec = guardRequest(t, h.UninstallModel, http.MethodDelete, apiDepG, "")
	requireOperationInProgress(t, rec, apiDepA, "Dependent A", "install")

	close(hub.release)
	h.Wait()

	lease, err := mm.BeginOperation(classifier.OperationInstall, apiDepA)
	require.NoError(t, err, "the slot is released after the background install fails")
	lease.Release()
}

// streamProgress runs StreamInstallProgress for id until it returns or ctx ends.
func streamProgress(t *testing.T, h *Handler, ctx context.Context, id string) string {
	t.Helper()
	req := httptest.NewRequestWithContext(ctx, http.MethodGet, "/api/v2/models/install/"+id+"/progress", http.NoBody)
	rec := httptest.NewRecorder()
	ectx := echo.New().NewContext(req, rec)
	ectx.SetParamNames("id")
	ectx.SetParamValues(id)
	require.NoError(t, h.StreamInstallProgress(ectx))
	return rec.Body.String()
}

func TestStreamInstallProgress_CompleteWaitsForOperationSlot(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm, _ := guardInstalledManager(t)
	h := dependencyHandler(t, mm)
	lease, err := mm.BeginOperation(classifier.OperationReinstall, apiDepA)
	require.NoError(t, err)
	t.Cleanup(lease.Release)

	// The entry is installed but its operation still runs (the post-install hot-load,
	// or a reinstall that has not registered its download yet): no complete event.
	ctx, cancel := context.WithTimeout(t.Context(), streamHoldWait)
	defer cancel()
	body := streamProgress(t, h, ctx, apiDepA)
	assert.NotContains(t, body, string(classifier.StatusComplete), "complete is sent while the operation holds the slot")

	lease.Release()
	body = streamProgress(t, h, t.Context(), apiDepA)
	assert.Contains(t, body, string(classifier.StatusComplete), "complete is sent once the slot is released")
}

func TestReinstallModel_ReservesSlotBeforeInstalledCheck(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	h := dependencyHandler(t, mm)
	holdSlot(t, mm)

	// The installed check runs under the slot, so an uninstall cannot complete
	// between it and the 202: a busy slot answers first.
	rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, "")
	requireOperationInProgress(t, rec, apiDepG, "Geomodel", "uninstall")
}

func TestReinstallModel_ReleasesSlotOnValidationFailure(t *testing.T) {
	useCatalog(t, guardCatalog())
	mm := classifier.NewModelManager(t.TempDir(), nil, nil)
	h := dependencyHandler(t, mm)

	rec := guardRequest(t, h.ReinstallModel, http.MethodPost, apiDepA, "")
	require.Equal(t, http.StatusBadRequest, rec.Code, rec.Body.String())
	lease, err := mm.BeginOperation(classifier.OperationInstall, apiDepA)
	require.NoError(t, err, "a refused reinstall frees the slot")
	lease.Release()
}
