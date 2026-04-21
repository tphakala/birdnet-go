package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	enginepkg "github.com/tphakala/birdnet-go/internal/audiocore/engine"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBuildSourceConfigsWithModels_SkipsDisabledStreams(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

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
	conf.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels()

	require.Len(t, configs, 1, "disabled streams should be excluded from active source configs")
	assert.Equal(t, "rtsp://cam1", configs[0].config.ConnectionString)
}

func TestReconfigureChangedSources_RemovesDisabledRunningStream(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

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
	conf.SetTestSettings(settings)

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
