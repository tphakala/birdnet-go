package api

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
)

func TestRestartAudioSource_NoEngine(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-source/test-src", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("test-src")

	err := c.RestartAudioSource(ctx)
	assertControllerError(t, err, rec, http.StatusInternalServerError, "Audio engine not available")
}

func TestRestartAudioSource_SourceNotFound(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()
	c.engine = eng

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-source/nonexistent", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("nonexistent")

	err := c.RestartAudioSource(ctx)
	assertControllerError(t, err, rec, http.StatusNotFound, "Audio source not found")
}

func TestRestartAudioSource_NoPipeline(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()
	c.engine = eng

	src := &audiocore.SourceConfig{
		ID:          "test-src",
		DisplayName: "Test Source",
		Type:        audiocore.SourceTypeAudioCard,
	}
	_, err := eng.Registry().Register(src)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-source/test-src", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("test-src")

	err = c.RestartAudioSource(ctx)
	assertControllerError(t, err, rec, http.StatusServiceUnavailable, "Audio pipeline not started")
}

func TestRestartAudioSource_Success(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()
	c.engine = eng

	src := &audiocore.SourceConfig{
		ID:          "test-src",
		DisplayName: "Test Source",
		Type:        audiocore.SourceTypeAudioCard,
	}
	_, err := eng.Registry().Register(src)
	require.NoError(t, err)

	called := false
	restarter := SourceRestarterFunc(func(sourceID string) error {
		called = true
		assert.Equal(t, "test-src", sourceID)
		return nil
	})
	c.sourceRestarter.Store(&restarter)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-source/test-src", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("test-src")

	err = c.RestartAudioSource(ctx)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)
	assert.True(t, called)

	var result ControlResult
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &result))
	assert.True(t, result.Success)
	assert.Equal(t, ActionRestartAudioSource, result.Action)
}

func TestRestartAudioSource_RestartError(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	eng := engine.New(t.Context(), &engine.Config{}, nil)
	defer eng.Stop()
	c.engine = eng

	src := &audiocore.SourceConfig{
		ID:          "test-src",
		DisplayName: "Test Source",
		Type:        audiocore.SourceTypeAudioCard,
	}
	_, err := eng.Registry().Register(src)
	require.NoError(t, err)

	restarter := SourceRestarterFunc(func(_ string) error {
		return fmt.Errorf("device disconnected")
	})
	c.sourceRestarter.Store(&restarter)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-source/test-src", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("test-src")

	err = c.RestartAudioSource(ctx)
	assertControllerError(t, err, rec, http.StatusInternalServerError, "Failed to restart audio source")
}

func TestRestartAudioSource_EmptyID(t *testing.T) {
	t.Parallel()
	e := echo.New()
	c := getTestController(t, e)

	req := httptest.NewRequest(http.MethodPost, "/api/v2/control/restart-source/", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("")

	err := c.RestartAudioSource(ctx)
	assertControllerError(t, err, rec, http.StatusBadRequest, "Source ID is required")
}
