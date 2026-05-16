package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

func TestGetAudioHealth_NoWatchdog(t *testing.T) {
	e := echo.New()
	c := getTestController(t, e)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/health/audio", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := c.GetAudioHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp AudioHealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.Sources)
}

func TestGetAudioHealth_WithWatchdog(t *testing.T) {
	e := echo.New()
	c := getTestController(t, e)

	router := audiocore.NewAudioRouter(audiocore.GetLogger(), nil)
	defer router.Close()

	cfg := audiocore.DefaultLivenessConfig()
	callbacks := audiocore.LivenessCallbacks{}
	watchdog := audiocore.NewLivenessWatchdog(cfg, router, callbacks)
	c.SetAudioWatchdog(watchdog)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/health/audio", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	err := c.GetAudioHealth(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var resp AudioHealthResponse
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
	assert.Empty(t, resp.Sources, "no sources tracked without active routes")
}
