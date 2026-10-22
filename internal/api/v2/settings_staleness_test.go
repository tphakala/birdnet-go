package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestSettingsReadEndpoints_ReflectOutOfBandUpdates reproduces issue #4764:
// When an out-of-band writer (such as ModelManager gallery install) updates
// global settings via conf.StoreSettings, the read handlers (GetAllSettings,
// GetSectionSettings, GetDashboardSettings, and GetSystemID) must return the
// updated settings rather than the stale startup snapshot when isGlobalOwner is true.
func TestSettingsReadEndpoints_ReflectOutOfBandUpdates(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	initial := getTestSettings(t)
	initial.Models.Enabled = []string{"birdnet", "perch_v2"}
	initial.Realtime.Dashboard.Locale = "en"
	initial.SystemID = "sys-initial"
	conf.StoreSettings(initial)

	e := echo.New()
	c := getTestController(t, e)
	c.isGlobalOwner = true
	c.Settings.Store(initial)

	// Simulate out-of-band update (e.g. gallery install of birdnet_v3.0)
	updated := conf.CloneSettings(initial)
	updated.Models.Enabled = []string{"birdnet", "perch_v2", "birdnet_v3.0"}
	updated.Realtime.Dashboard.Locale = "fi"
	updated.SystemID = "sys-updated"
	conf.StoreSettings(updated)

	// 1. GET /api/v2/settings (GetAllSettings)
	reqAll := httptest.NewRequest(http.MethodGet, "/api/v2/settings", http.NoBody)
	recAll := httptest.NewRecorder()
	require.NoError(t, c.GetAllSettings(e.NewContext(reqAll, recAll)))
	require.Equal(t, http.StatusOK, recAll.Code)
	var respAll conf.Settings
	require.NoError(t, json.Unmarshal(recAll.Body.Bytes(), &respAll))
	assert.Equal(t, []string{"birdnet", "perch_v2", "birdnet_v3.0"}, respAll.Models.Enabled,
		"GetAllSettings must reflect out-of-band models.enabled update")

	// 2. GET /api/v2/settings/models (GetSectionSettings)
	reqSec := httptest.NewRequest(http.MethodGet, "/api/v2/settings/models", http.NoBody)
	recSec := httptest.NewRecorder()
	ctxSec := e.NewContext(reqSec, recSec)
	ctxSec.SetParamNames("section")
	ctxSec.SetParamValues("models")
	require.NoError(t, c.GetSectionSettings(ctxSec))
	require.Equal(t, http.StatusOK, recSec.Code)
	var respSec conf.ModelsConfig
	require.NoError(t, json.Unmarshal(recSec.Body.Bytes(), &respSec))
	assert.Equal(t, []string{"birdnet", "perch_v2", "birdnet_v3.0"}, respSec.Enabled,
		"GetSectionSettings must reflect out-of-band models.enabled update")

	// 3. GET /api/v2/settings/dashboard (GetDashboardSettings)
	reqDash := httptest.NewRequest(http.MethodGet, "/api/v2/settings/dashboard", http.NoBody)
	recDash := httptest.NewRecorder()
	require.NoError(t, c.GetDashboardSettings(e.NewContext(reqDash, recDash)))
	require.Equal(t, http.StatusOK, recDash.Code)
	var respDash conf.Dashboard
	require.NoError(t, json.Unmarshal(recDash.Body.Bytes(), &respDash))
	assert.Equal(t, "fi", respDash.Locale,
		"GetDashboardSettings must reflect out-of-band dashboard update")

	// 4. GET /api/v2/settings/systemid (GetSystemID)
	reqSys := httptest.NewRequest(http.MethodGet, "/api/v2/settings/systemid", http.NoBody)
	recSys := httptest.NewRecorder()
	require.NoError(t, c.GetSystemID(e.NewContext(reqSys, recSys)))
	require.Equal(t, http.StatusOK, recSys.Code)
	var respSys map[string]string
	require.NoError(t, json.Unmarshal(recSys.Body.Bytes(), &respSys))
	assert.Equal(t, "sys-updated", respSys["systemID"],
		"GetSystemID must reflect out-of-band system ID update")
}

// TestSettingsReadEndpoints_PreserveTestIsolationWhenNotGlobalOwner guards that
// standalone test controllers (isGlobalOwner == false) isolate their settings reads
// to their own atomic pointer, ignoring global conf.GetSettings().
func TestSettingsReadEndpoints_PreserveTestIsolationWhenNotGlobalOwner(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	globalSettings := getTestSettings(t)
	globalSettings.SystemID = "global-system-id"
	conf.StoreSettings(globalSettings)

	localSettings := getTestSettings(t)
	localSettings.SystemID = "local-test-id"

	e := echo.New()
	c := getTestController(t, e)
	c.isGlobalOwner = false
	c.Settings.Store(localSettings)

	reqSys := httptest.NewRequest(http.MethodGet, "/api/v2/settings/systemid", http.NoBody)
	recSys := httptest.NewRecorder()
	require.NoError(t, c.GetSystemID(e.NewContext(reqSys, recSys)))
	require.Equal(t, http.StatusOK, recSys.Code)
	var respSys map[string]string
	require.NoError(t, json.Unmarshal(recSys.Body.Bytes(), &respSys))
	assert.Equal(t, "local-test-id", respSys["systemID"],
		"Standalone controller must read isolated settings when isGlobalOwner is false")
}

// TestSettingsSave_DoesNotRevertOutOfBandModel reproduces the full end-to-end bug:
// UI fetches settings via GetAllSettings, modifies an unrelated setting, and saves
// via UpdateSettings. The out-of-band installed model must not be reverted.
func TestSettingsSave_DoesNotRevertOutOfBandModel(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })

	initial := getTestSettings(t)
	initial.Models.Enabled = []string{"birdnet", "perch_v2"}
	initial.Realtime.Interval = 15
	conf.StoreSettings(initial)

	e := echo.New()
	c := getTestController(t, e)
	c.SetTestContext(t.Context(), func() {})
	c.isGlobalOwner = true
	c.DisableSaveSettings = true // don't write to disk in unit test
	c.Settings.Store(initial)

	// Out-of-band install occurs
	installed := conf.CloneSettings(initial)
	installed.Models.Enabled = []string{"birdnet", "perch_v2", "birdnet_v3.0"}
	conf.StoreSettings(installed)

	// UI loads the settings page: GET /api/v2/settings
	reqGet := httptest.NewRequest(http.MethodGet, "/api/v2/settings", http.NoBody)
	recGet := httptest.NewRecorder()
	require.NoError(t, c.GetAllSettings(e.NewContext(reqGet, recGet)))
	require.Equal(t, http.StatusOK, recGet.Code)

	// UI changes an unrelated setting (realtime.interval: 15 -> 20) and sends PUT /api/v2/settings
	var uiPayload map[string]any
	require.NoError(t, json.Unmarshal(recGet.Body.Bytes(), &uiPayload))
	realtimeMap, ok := uiPayload["realtime"].(map[string]any)
	require.True(t, ok)
	realtimeMap["interval"] = 20

	bodyBytes, err := json.Marshal(uiPayload)
	require.NoError(t, err)

	reqPut := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(bodyBytes))
	reqPut.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	recPut := httptest.NewRecorder()
	require.NoError(t, c.UpdateSettings(e.NewContext(reqPut, recPut)))
	require.Equal(t, http.StatusOK, recPut.Code)

	// Verify that models.enabled was NOT reverted to ["birdnet", "perch_v2"]
	currentGlobal := conf.GetSettings()
	assert.Contains(t, currentGlobal.Models.Enabled, "birdnet_v3.0",
		"Subsequent UI settings save must not unload out-of-band installed model")
	assert.Equal(t, 20, currentGlobal.Realtime.Interval)
}
