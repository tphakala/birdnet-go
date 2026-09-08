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
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// TestBrowserLocationSettingsRoundTrip exercises the PUT and subsequent GET
// handlers used by the Settings UI. The browser feature sends the complete
// settings form, including the explicit locationConfigured provenance flag.
func TestBrowserLocationSettingsRoundTrip(t *testing.T) {
	initial := getTestSettings(t)
	desired := conf.CloneSettings(initial)
	// Null Island is intentional: the backend compatibility fallback only marks
	// non-zero coordinates as configured, so this proves the explicit frontend
	// provenance flag itself survives the API round trip.
	desired.BirdNET.Latitude = 0
	desired.BirdNET.Longitude = 0
	desired.BirdNET.LocationConfigured = true

	body, err := json.Marshal(desired)
	require.NoError(t, err)

	e := echo.New()
	controller := &Controller{
		Core:                &apicore.Core{Echo: e},
		controlChan:         make(chan string, testControlChanBuffer),
		DisableSaveSettings: true,
	}
	controller.Settings.Store(initial)

	putRequest := httptest.NewRequest(http.MethodPut, "/api/v2/settings", bytes.NewReader(body))
	putRequest.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	putRecorder := httptest.NewRecorder()
	require.NoError(t, controller.UpdateSettings(e.NewContext(putRequest, putRecorder)))
	assert.Equal(t, http.StatusOK, putRecorder.Code)

	getRequest := httptest.NewRequest(http.MethodGet, "/api/v2/settings", http.NoBody)
	getRecorder := httptest.NewRecorder()
	require.NoError(t, controller.GetAllSettings(e.NewContext(getRequest, getRecorder)))
	assert.Equal(t, http.StatusOK, getRecorder.Code)

	var reloaded conf.Settings
	require.NoError(t, json.Unmarshal(getRecorder.Body.Bytes(), &reloaded))
	assert.Zero(t, reloaded.BirdNET.Latitude)
	assert.Zero(t, reloaded.BirdNET.Longitude)
	assert.True(t, reloaded.BirdNET.LocationConfigured)
}
