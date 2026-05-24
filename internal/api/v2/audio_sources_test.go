// audio_sources_test.go - Tests for audio source listing endpoints.
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
	"github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// setupAudioSourcesTestEnv creates a Controller with an AudioEngine whose
// registry contains the given sources. Sources are registered directly in the
// registry to avoid requiring real audio hardware or network streams.
// The engine is stopped automatically when the test completes.
func setupAudioSourcesTestEnv(t *testing.T, sources []*audiocore.SourceConfig) (*echo.Echo, *Controller) {
	t.Helper()

	ctx := t.Context()
	log := audiocore.GetLogger()
	eng := engine.New(ctx, &engine.Config{Logger: log}, nil)
	t.Cleanup(eng.Stop)

	// Register sources directly in the registry instead of using
	// engine.AddSource, which tries to start real hardware capture
	// for audio cards and FFmpeg for streams.
	registry := eng.Registry()
	for _, cfg := range sources {
		_, err := registry.Register(cfg)
		require.NoError(t, err)
	}

	e := echo.New()
	controller := &Controller{
		Echo:     e,
		Group:    e.Group("/api/v2"),
		Settings: &conf.Settings{},
	}
	controller.engine.Store(eng)
	return e, controller
}

func TestListAudioSources(t *testing.T) {
	t.Parallel()
	t.Attr("component", "system")
	t.Attr("type", "integration")
	t.Attr("feature", "audio-sources")

	t.Run("No engine returns empty list", func(t *testing.T) {
		e := echo.New()
		controller := &Controller{
			Echo:     e,
			Group:    e.Group("/api/v2"),
			Settings: &conf.Settings{},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/sources", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetPath("/api/v2/system/audio/sources")

		err := controller.ListAudioSources(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp AudioSourceListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Empty(t, resp.Sources)
	})

	t.Run("Returns RTSP and audio card sources", func(t *testing.T) {
		sources := []*audiocore.SourceConfig{
			{
				DisplayName:      "Backyard Mic",
				Type:             audiocore.SourceTypeAudioCard,
				ConnectionString: "hw:0,0",
				SampleRate:       48000,
				BitDepth:         16,
				Channels:         1,
			},
			{
				DisplayName:      "Front Camera",
				Type:             audiocore.SourceTypeRTSP,
				ConnectionString: "rtsp://192.168.1.10:554/audio",
				SampleRate:       48000,
				BitDepth:         16,
				Channels:         1,
			},
		}
		e, controller := setupAudioSourcesTestEnv(t, sources)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/system/audio/sources", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetPath("/api/v2/system/audio/sources")

		err := controller.ListAudioSources(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp AudioSourceListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Len(t, resp.Sources, 2)

		// Sorted by DisplayName
		assert.Equal(t, "Backyard Mic", resp.Sources[0].Name)
		assert.Equal(t, "audio_card", resp.Sources[0].Type)
		assert.Equal(t, "Front Camera", resp.Sources[1].Name)
		assert.Equal(t, "rtsp", resp.Sources[1].Type)
	})
}

func TestListStreamSources(t *testing.T) {
	t.Parallel()
	t.Attr("component", "streams")
	t.Attr("type", "integration")
	t.Attr("feature", "stream-sources")

	t.Run("No engine returns empty list", func(t *testing.T) {
		e := echo.New()
		controller := &Controller{
			Echo:     e,
			Group:    e.Group("/api/v2"),
			Settings: &conf.Settings{},
		}

		req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/sources", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetPath("/api/v2/streams/sources")

		err := controller.ListStreamSources(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp AudioSourceListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Empty(t, resp.Sources)
	})

	t.Run("Filters to stream types only", func(t *testing.T) {
		sources := []*audiocore.SourceConfig{
			{
				DisplayName:      "Backyard Mic",
				Type:             audiocore.SourceTypeAudioCard,
				ConnectionString: "hw:0,0",
				SampleRate:       48000,
				BitDepth:         16,
				Channels:         1,
			},
			{
				DisplayName:      "Front Camera",
				Type:             audiocore.SourceTypeRTSP,
				ConnectionString: "rtsp://192.168.1.10:554/audio",
				SampleRate:       48000,
				BitDepth:         16,
				Channels:         1,
			},
			{
				DisplayName:      "HTTP Stream",
				Type:             audiocore.SourceTypeHTTP,
				ConnectionString: "http://192.168.1.20:8000/stream",
				SampleRate:       48000,
				BitDepth:         16,
				Channels:         1,
			},
		}
		e, controller := setupAudioSourcesTestEnv(t, sources)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/streams/sources", http.NoBody)
		rec := httptest.NewRecorder()
		ctx := e.NewContext(req, rec)
		ctx.SetPath("/api/v2/streams/sources")

		err := controller.ListStreamSources(ctx)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var resp AudioSourceListResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Len(t, resp.Sources, 2, "Should only include stream sources, not audio cards")

		types := make([]string, len(resp.Sources))
		for i, s := range resp.Sources {
			types[i] = s.Type
		}
		assert.NotContains(t, types, "audio_card")
	})
}
