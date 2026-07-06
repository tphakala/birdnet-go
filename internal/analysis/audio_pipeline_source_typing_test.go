package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
)

// findConfigByConnection returns the built config whose ConnectionString
// matches conn, or nil when none match.
func findConfigByConnection(configs []sourceConfigWithModels, conn string) *audiocore.SourceConfig {
	for i := range configs {
		if configs[i].config.ConnectionString == conn {
			return configs[i].config
		}
	}
	return nil
}

// TestBuildSourceConfigsWithModels_StreamURLInAudioSources verifies that a
// stream URL misplaced under audio.sources is emitted as a stream-typed
// SourceConfig (not an ALSA audio card).
func TestBuildSourceConfigsWithModels_StreamURLInAudioSources(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name:   "misplaced-stream",
			Device: "rtsp://cam-in-sources",
			Models: []string{"birdnet"},
		},
		{
			Name:   "real-mic",
			Device: "hw:0,0",
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels()

	require.Len(t, configs, 2)

	streamCfg := findConfigByConnection(configs, "rtsp://cam-in-sources")
	require.NotNil(t, streamCfg, "misplaced stream should still be emitted")
	assert.Equal(t, audiocore.SourceTypeRTSP, streamCfg.Type, "stream URL must be typed as a stream, not an audio card")

	micCfg := findConfigByConnection(configs, "hw:0,0")
	require.NotNil(t, micCfg, "local mic should be emitted")
	assert.Equal(t, audiocore.SourceTypeAudioCard, micCfg.Type, "genuine device must remain an audio card")
}

// TestBuildSourceConfigsWithModels_DuplicateURLPrefersRTSPStream guards the
// priority-inversion bug: when the same URL appears in both rtsp.streams and
// audio.sources, exactly one config is produced and it comes from the
// rtsp.streams entry, so the audio.sources duplicate cannot overwrite the
// proper stream config via the connection-keyed dedup.
func TestBuildSourceConfigsWithModels_DuplicateURLPrefersRTSPStream(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	const dupURL = "rtsp://shared-cam"

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "proper-stream",
			URL:     dupURL,
			Enabled: true,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
	}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name:   "duplicate-in-sources",
			Device: dupURL,
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels()

	require.Len(t, configs, 1, "duplicate URL must yield exactly one desired config")
	cfg := configs[0].config
	assert.Equal(t, dupURL, cfg.ConnectionString)
	assert.Equal(t, audiocore.SourceTypeRTSP, cfg.Type)
}

// TestBuildSourceConfigsWithModels_DisabledStreamSuppressesAudioSourceDuplicate
// guards the "disabled stream re-enabled via misplaced audio.sources entry"
// regression: when a DISABLED rtsp.streams entry shares its URL with an
// audio.sources entry, the disabled stream is not built (it is off) and the
// audio.sources duplicate is skipped by the dedup guard, which keys off ALL
// configured streams (enabled AND disabled). The net result is zero configs for
// that URL, so a stream the user turned off is not silently re-enabled through
// audio.sources.
//
// This test is hermetic: a disabled stream never reaches probeAllStreams (which
// only receives EnabledStreams), so no ffprobe process is spawned.
func TestBuildSourceConfigsWithModels_DisabledStreamSuppressesAudioSourceDuplicate(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	const dupURL = "rtsp://dup.local/stream"

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "disabled-stream",
			URL:     dupURL,
			Enabled: false, // disabled: not built, but still shadows the audio.sources duplicate
			Type:    conf.StreamTypeRTSP,
			Models:  []string{"birdnet"},
		},
	}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name:   "duplicate-in-sources",
			Device: dupURL,
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels()

	// No config may reference the shared URL: the disabled stream is not built,
	// and the audio.sources duplicate is suppressed by the dedup guard.
	assert.Nil(t, findConfigByConnection(configs, dupURL),
		"disabled stream duplicated under audio.sources must not be promoted to an active stream")

	// Make the guarantee explicit: exactly zero configs for that URL.
	var count int
	for i := range configs {
		if configs[i].config.ConnectionString == dupURL {
			count++
		}
	}
	assert.Equal(t, 0, count, "expected zero configs for the disabled/duplicated URL")

	// Nothing else is configured, so the whole result set is empty.
	assert.Empty(t, configs, "no sources should be built from a lone disabled stream plus its duplicate")
}
