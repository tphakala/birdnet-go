package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	enginepkg "github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

func TestBuildSourceConfigsWithModels_SkipsDisabledStreams(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "enabled-stream",
			URL:     "rtsp://cam1",
			Enabled: true,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
		{
			Name:    "disabled-stream",
			URL:     "rtsp://cam2",
			Enabled: false,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	require.Len(t, configs, 1, "disabled streams should be excluded from active source configs")
	assert.Equal(t, "rtsp://cam1", configs[0].config.ConnectionString)
}

func TestReconfigureChangedSources_RemovesDisabledRunningStream(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	const connection = "rtsp://cam1"

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "test-stream",
			URL:     connection,
			Enabled: false,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	engine := enginepkg.New(t.Context(), &enginepkg.Config{}, nil)
	_, err := engine.Registry().Register(&audiocore.SourceConfig{
		DisplayName:      "test-stream",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: connection,
		SampleRate:       conf.SampleRate,
		BitDepth:         conf.BitDepth,
		Channels:         1,
	})
	require.NoError(t, err)

	_, exists := engine.Registry().GetByConnection(connection)
	require.True(t, exists, "test setup should start with the stream registered")

	// bufferMgr is intentionally nil: reconfigureChangedSources guards the
	// UpdateMonitors call with a nil check, so this exercises the disable
	// path without needing a real buffer manager.
	p := &AudioPipelineService{engine: engine}
	p.reconfigureChangedSources(make(chan audiocore.AudioLevelData))

	_, exists = engine.Registry().GetByConnection(connection)
	assert.False(t, exists, "disabled configured streams must be removed from the running registry")
}

// TestBuildSourceConfigsWithModels_RegistryFallbackOnProbeFailure verifies the
// nil-map path used by reconfigure/startup: when the live probe fails and no
// caller fallback is supplied, the last known parameters are recovered from the
// running source in the registry so a bat stream keeps its high rate and channel
// count instead of collapsing to the 48 kHz target (#4350).
func TestBuildSourceConfigsWithModels_RegistryFallbackOnProbeFailure(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	const connection = "rtsp://cam-bat-registry.invalid/stream"
	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:        "bat-registry",
			URL:         connection,
			Enabled:     true,
			Type:        conf.StreamTypeRTSP,
			ChannelMode: conf.ChannelModeRight,
			Models:      []string{conf.ModelIDBat},
		},
	}
	conftest.SetTestSettings(settings)

	engine := enginepkg.New(t.Context(), &enginepkg.Config{}, nil)
	// A source already running at its true 192 kHz stereo rate, as a healthy
	// probe would have recorded it.
	_, err := engine.Registry().Register(&audiocore.SourceConfig{
		DisplayName:      "bat-registry",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: connection,
		SampleRate:       192000,
		SourceSampleRate: 192000,
		BitDepth:         conf.BitDepth,
		Channels:         1,
		SourceChannels:   2,
	})
	require.NoError(t, err)

	p := &AudioPipelineService{engine: engine}
	// nil fallback map: the probe of the bogus URL fails, so the values must come
	// from the live registry consult.
	configs := p.buildSourceConfigsWithModels(nil)

	cfg := findConfigByConnection(configs, connection)
	require.NotNil(t, cfg, "rtsp stream config should be present")
	assert.Equal(t, 192000, cfg.SourceSampleRate, "source rate should be recovered from the registry on probe failure")
	assert.Equal(t, 192000, cfg.SampleRate, "bat output rate should track the recovered source rate")
	assert.Equal(t, 2, cfg.SourceChannels, "channel count should be recovered from the registry")
	assert.True(t, cfg.SourceSampleRateEstimated, "a registry-recovered rate is an estimate and must force resampling")
}

// TestCaptureStreamFallback verifies the RestartSource capture-before-remove
// snapshot: it reads the running source's true rate and channel count from the
// registry so a transient probe failure during the restart can fall back to
// them (#4350). Regressions in the captured fields or the emptiness guard would
// otherwise stay green because the downstream consumer is tested with a
// hand-built map.
func TestCaptureStreamFallback(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	conftest.SetTestSettings(&conf.Settings{})

	engine := enginepkg.New(t.Context(), &enginepkg.Config{}, nil)
	const known = "rtsp://cam-known.invalid/stream"
	_, err := engine.Registry().Register(&audiocore.SourceConfig{
		DisplayName:      "known",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: known,
		SampleRate:       250000,
		SourceSampleRate: 250000,
		BitDepth:         conf.BitDepth,
		Channels:         1,
		SourceChannels:   2,
	})
	require.NoError(t, err)

	// A source whose rate/channels were never discovered (probe failed at cold
	// start); the emptiness guard must reject it so nothing bogus is captured.
	const unknownRate = "rtsp://cam-norate.invalid/stream"
	_, err = engine.Registry().Register(&audiocore.SourceConfig{
		DisplayName:      "norate",
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: unknownRate,
		SampleRate:       conf.SampleRate,
		BitDepth:         conf.BitDepth,
		Channels:         1,
	})
	require.NoError(t, err)

	p := &AudioPipelineService{engine: engine}

	got := p.captureStreamFallback(known)
	require.Contains(t, got, known, "a source with known params must be captured")
	assert.Equal(t, 250000, got[known].sampleRate, "captured sample rate must match the registry")
	assert.Equal(t, 2, got[known].channels, "captured channel count must match the registry")

	assert.Empty(t, p.captureStreamFallback(unknownRate), "a source with no known params must not be captured")
	assert.Empty(t, p.captureStreamFallback("rtsp://cam-absent.invalid/stream"), "an unregistered connection must yield an empty map")
}

// TestCaptureStreamFallback_NilEngine confirms the capture is nil-safe for a
// minimally constructed service (no engine wired).
func TestCaptureStreamFallback_NilEngine(t *testing.T) {
	t.Parallel()
	p := &AudioPipelineService{}
	assert.Empty(t, p.captureStreamFallback("rtsp://cam.invalid/stream"), "nil engine must yield an empty map, not panic")
}
