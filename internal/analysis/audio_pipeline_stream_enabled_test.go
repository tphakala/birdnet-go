package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/tphakala/birdnet-go/internal/conf"
)

func TestBuildSourceConfigsWithModels_SkipsDisabledStreams(t *testing.T) {
	prev := conf.GetSettings()
	t.Cleanup(func() { conf.SetTestSettings(prev) })

	enabled := true
	disabled := false
	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "enabled-stream",
			URL:     "rtsp://cam1",
			Enabled: &enabled,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
		{
			Name:    "disabled-stream",
			URL:     "rtsp://cam2",
			Enabled: &disabled,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
		{
			Name:   "legacy-stream",
			URL:    "rtsp://cam3",
			Type:   conf.StreamTypeRTSP,
			Models: []string{"birdnet"},
		},
	}
	conf.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels()

	assert.Len(t, configs, 2, "disabled streams should be excluded from active source configs")
	assert.Equal(t, "rtsp://cam1", configs[0].config.ConnectionString)
	assert.Equal(t, "rtsp://cam3", configs[1].config.ConnectionString, "legacy nil enabled should default to enabled")
}
