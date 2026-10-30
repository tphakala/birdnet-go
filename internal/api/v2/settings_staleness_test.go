package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// installedModelID is the model a gallery install adds to models.enabled in
// the staleness tests below.
const installedModelID = "birdnet_v3.0"

// stalenessBaseModels returns the models.enabled list before the simulated install.
func stalenessBaseModels() []string { return []string{"birdnet", "perch_v2"} }

// stalenessInstalledModels returns the models.enabled list after the simulated install.
func stalenessInstalledModels() []string {
	return append(stalenessBaseModels(), installedModelID)
}

// newGlobalOwnerController stores initial as the process-global settings and
// returns a controller that owns the global singleton, as in production. The
// previous global settings are restored when the test ends.
func newGlobalOwnerController(t *testing.T, e *echo.Echo, initial *conf.Settings) *Controller {
	t.Helper()
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })
	conf.StoreSettings(initial)

	c := getTestController(t, e)
	c.isGlobalOwner = true
	c.Settings.Store(initial)
	return c
}

// getJSON calls a settings GET handler and decodes the 200 response into T.
// A non-empty section is passed as the "section" path parameter.
func getJSON[T any](t *testing.T, e *echo.Echo, handler echo.HandlerFunc, path, section string) T {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	if section != "" {
		ctx.SetParamNames("section")
		ctx.SetParamValues(section)
	}
	require.NoError(t, handler(ctx))
	require.Equal(t, http.StatusOK, rec.Code, "GET %s must succeed; body: %s", path, rec.Body.String())

	var out T
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &out))
	return out
}

// TestSettingsReadEndpoints_ReflectOutOfBandUpdates reproduces issue #4764:
// When an out-of-band writer (such as ModelManager gallery install) updates
// global settings via conf.StoreSettings, the read handlers (GetAllSettings,
// GetSectionSettings, GetDashboardSettings, and GetSystemID) must return the
// updated settings rather than the stale startup snapshot when isGlobalOwner is true.
func TestSettingsReadEndpoints_ReflectOutOfBandUpdates(t *testing.T) {
	initial := getTestSettings(t)
	initial.Models.Enabled = stalenessBaseModels()
	initial.Realtime.Dashboard.Locale = "en"
	initial.SystemID = "sys-initial"

	e := echo.New()
	c := newGlobalOwnerController(t, e, initial)

	// Simulate out-of-band update (e.g. gallery install of birdnet_v3.0)
	updated := conf.CloneSettings(initial)
	updated.Models.Enabled = stalenessInstalledModels()
	updated.Realtime.Dashboard.Locale = "fi"
	updated.SystemID = "sys-updated"
	conf.StoreSettings(updated)

	all := getJSON[conf.Settings](t, e, c.GetAllSettings, "/api/v2/settings", "")
	assert.Equal(t, stalenessInstalledModels(), all.Models.Enabled,
		"GetAllSettings must reflect out-of-band models.enabled update")

	models := getJSON[conf.ModelsConfig](t, e, c.GetSectionSettings, "/api/v2/settings/models", "models")
	assert.Equal(t, stalenessInstalledModels(), models.Enabled,
		"GetSectionSettings must reflect out-of-band models.enabled update")

	dashboard := getJSON[conf.Dashboard](t, e, c.GetDashboardSettings, "/api/v2/settings/dashboard", "")
	assert.Equal(t, "fi", dashboard.Locale,
		"GetDashboardSettings must reflect out-of-band dashboard update")

	systemID := getJSON[map[string]string](t, e, c.GetSystemID, "/api/v2/settings/systemid", "")
	assert.Equal(t, "sys-updated", systemID["systemID"],
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

	systemID := getJSON[map[string]string](t, e, c.GetSystemID, "/api/v2/settings/systemid", "")
	assert.Equal(t, "local-test-id", systemID["systemID"],
		"Standalone controller must read isolated settings when isGlobalOwner is false")
}

// TestSettingsSave_AfterFreshGetKeepsOutOfBandModel covers the server side of
// issue #4764: a client that fetches settings after an out-of-band update via
// GetAllSettings, changes an unrelated setting and sends the result back via
// UpdateSettings must not revert the out-of-band model. It does not cover a
// client that fetched settings before the update and kept using that copy.
func TestSettingsSave_AfterFreshGetKeepsOutOfBandModel(t *testing.T) {
	initial := getTestSettings(t)
	initial.Models.Enabled = stalenessBaseModels()
	initial.Realtime.Interval = 15

	e := echo.New()
	c := newGlobalOwnerController(t, e, initial)
	c.SetTestContext(t.Context(), func() {})
	c.DisableSaveSettings = true // don't write to disk in unit test

	// Out-of-band install occurs
	installed := conf.CloneSettings(initial)
	installed.Models.Enabled = stalenessInstalledModels()
	conf.StoreSettings(installed)

	// Client fetches settings after the install: GET /api/v2/settings
	payload := getJSON[map[string]any](t, e, c.GetAllSettings, "/api/v2/settings", "")

	// Client changes an unrelated setting (realtime.interval: 15 -> 20) and sends PUT /api/v2/settings
	realtimeMap, ok := payload["realtime"].(map[string]any)
	require.True(t, ok)
	realtimeMap["interval"] = 20

	bodyBytes, err := json.Marshal(payload)
	require.NoError(t, err)

	putRawSettings(t, e, c, string(bodyBytes))

	// Verify that models.enabled was NOT reverted to the pre-install list
	currentGlobal := conf.GetSettings()
	assert.Contains(t, currentGlobal.Models.Enabled, installedModelID,
		"Subsequent settings save must not unload out-of-band installed model")
	assert.Equal(t, 20, currentGlobal.Realtime.Interval)
}

// TestGetSettingsOrFallback_OwnerWithoutGlobalUsesControllerSnapshot pins the
// source order of getSettingsOrFallback for a controller that owns the global
// singleton before any global settings are published: it must serve its own
// snapshot instead of falling through to conf.Setting().
func TestGetSettingsOrFallback_OwnerWithoutGlobalUsesControllerSnapshot(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.StoreSettings(prev) })
	conf.StoreSettings(nil)

	local := getTestSettings(t)
	c := getTestController(t, echo.New())
	c.isGlobalOwner = true
	c.Settings.Store(local)

	assert.Same(t, local, c.getSettingsOrFallback(),
		"a global owner without published global settings must serve its own snapshot")
}
