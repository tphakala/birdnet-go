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

// TestBuildSourceConfigsWithModels_RTSPStreamCarriesGain verifies per-stream
// gain flows from the config into the built RTSP SourceConfig.
func TestBuildSourceConfigsWithModels_RTSPStreamCarriesGain(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "gain-stream",
			URL:     "rtsp://cam-gain",
			Enabled: true,
			Type:    conf.StreamTypeRTSP,
			Gain:    7.5,
			Models:  []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	require.Len(t, configs, 1)
	cfg := findConfigByConnection(configs, "rtsp://cam-gain")
	require.NotNil(t, cfg, "rtsp stream config should be present")
	assert.Equal(t, audiocore.SourceTypeRTSP, cfg.Type)
	assert.InDelta(t, 7.5, cfg.Gain, 1e-9, "per-stream gain should flow to the pipeline")
}

// TestBuildSourceConfigsWithModels_RTSPStreamCarriesTransport verifies the
// per-stream transport flows from the config into the built RTSP SourceConfig,
// so the initial stream start honors it rather than only the engine-wide
// default (issue #4240).
func TestBuildSourceConfigsWithModels_RTSPStreamCarriesTransport(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:      "udp-stream",
			URL:       "rtsp://cam-udp",
			Enabled:   true,
			Type:      conf.StreamTypeRTSP,
			Transport: transportUDP,
			Models:    []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	require.Len(t, configs, 1)
	cfg := findConfigByConnection(configs, "rtsp://cam-udp")
	require.NotNil(t, cfg, "rtsp stream config should be present")
	assert.Equal(t, transportUDP, cfg.Transport, "per-stream transport should flow to the pipeline")
}

// TestBuildSourceConfigsWithModels_RTSPStreamResolvesGlobalTransport verifies a
// stream with no per-stream transport resolves to the global default in the built
// config, so the pipeline diff and the engine agree on one concrete value even
// when the global is not the built-in "tcp" default (issue #4240).
func TestBuildSourceConfigsWithModels_RTSPStreamResolvesGlobalTransport(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.RTSP.Transport = transportUDP // global default
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "no-transport-stream",
			URL:     "rtsp://cam-global",
			Enabled: true,
			Type:    conf.StreamTypeRTSP,
			// Transport intentionally empty: must resolve to the global "udp".
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	require.Len(t, configs, 1)
	cfg := findConfigByConnection(configs, "rtsp://cam-global")
	require.NotNil(t, cfg, "rtsp stream config should be present")
	assert.Equal(t, transportUDP, cfg.Transport, "unset per-stream transport must resolve to the global default")
}

// TestBuildSourceConfigsWithModels_StreamURLInAudioSources verifies that a
// stream URL misplaced under audio.sources is emitted as a stream-typed
// SourceConfig (not an ALSA audio card) with its gain carried.
func TestBuildSourceConfigsWithModels_StreamURLInAudioSources(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	settings := &conf.Settings{}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name:   "misplaced-stream",
			Device: "rtsp://cam-in-sources",
			Gain:   4.25,
			Models: []string{"birdnet"},
		},
		{
			Name:   "real-mic",
			Device: "hw:0,0",
			Gain:   2.0,
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	require.Len(t, configs, 2)

	streamCfg := findConfigByConnection(configs, "rtsp://cam-in-sources")
	require.NotNil(t, streamCfg, "misplaced stream should still be emitted")
	assert.Equal(t, audiocore.SourceTypeRTSP, streamCfg.Type, "stream URL must be typed as a stream, not an audio card")
	assert.InDelta(t, 4.25, streamCfg.Gain, 1e-9)

	micCfg := findConfigByConnection(configs, "hw:0,0")
	require.NotNil(t, micCfg, "local mic should be emitted")
	assert.Equal(t, audiocore.SourceTypeAudioCard, micCfg.Type, "genuine device must remain an audio card")
	assert.InDelta(t, 2.0, micCfg.Gain, 1e-9)
}

// TestBuildSourceConfigsWithModels_DuplicateURLPrefersRTSPStream guards the
// priority-inversion bug: when the same URL appears in both rtsp.streams and
// audio.sources, exactly one config is produced and it comes from the
// rtsp.streams entry (whose gain wins), so the audio.sources duplicate cannot
// overwrite the proper stream config via the connection-keyed dedup.
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
			Gain:    10.0,
			Models:  []string{"birdnet"},
		},
	}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name:   "duplicate-in-sources",
			Device: dupURL,
			Gain:   1.0,
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	require.Len(t, configs, 1, "duplicate URL must yield exactly one desired config")
	cfg := configs[0].config
	assert.Equal(t, dupURL, cfg.ConnectionString)
	assert.Equal(t, audiocore.SourceTypeRTSP, cfg.Type)
	assert.InDelta(t, 10.0, cfg.Gain, 1e-9, "the rtsp.streams entry must win, not the audio.sources duplicate")
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
			Gain:    3.0,
			Models:  []string{"birdnet"},
		},
	}
	settings.Realtime.Audio.Sources = []conf.AudioSourceConfig{
		{
			Name:   "duplicate-in-sources",
			Device: dupURL,
			Gain:   1.0,
			Models: []string{"birdnet"},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

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

// TestBuildSourceConfigsWithModels_FallbackPreservesParamsOnProbeFailure verifies
// that when a stream's live probe fails (the bogus .invalid URL never resolves),
// the caller-supplied fallback (the RestartSource capture) is used so a bat
// source retains its true high sample rate and channel count instead of silently
// collapsing to the 48 kHz target and downmixing (#4350).
func TestBuildSourceConfigsWithModels_FallbackPreservesParamsOnProbeFailure(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	const url = "rtsp://cam-bat.invalid/stream"
	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:        "bat-stream",
			URL:         url,
			Enabled:     true,
			Type:        conf.StreamTypeRTSP,
			ChannelMode: conf.ChannelModeLeft,
			Models:      []string{conf.ModelIDBat},
		},
	}
	conftest.SetTestSettings(settings)

	p := &AudioPipelineService{}
	fallback := map[string]streamFallback{
		url: {sampleRate: 250000, channels: 2},
	}
	configs := p.buildSourceConfigsWithModels(fallback)

	cfg := findConfigByConnection(configs, url)
	require.NotNil(t, cfg, "rtsp stream config should be present")
	assert.Equal(t, 250000, cfg.SourceSampleRate, "source rate should be retained from the fallback on probe failure")
	assert.Equal(t, 250000, cfg.SampleRate, "bat output rate should be raised to the retained source rate, not left at 48 kHz")
	assert.Equal(t, 2, cfg.SourceChannels, "channel count should be retained so left/right selection still applies")
	assert.True(t, cfg.SourceSampleRateEstimated, "a retained rate must be flagged estimated so FFmpeg still force-resamples if the live rate changed")
}

// TestBuildSourceConfigsWithModels_NoFallbackCollapsesToTarget documents the
// unavoidable cold-start behaviour: with no probe result and no fallback, a bat
// stream collapses to the target rate with an unknown source rate. The escalation
// path logs an error in this case so the loss is not silent (#4350).
func TestBuildSourceConfigsWithModels_NoFallbackCollapsesToTarget(t *testing.T) {
	prev := conf.CloneSettings(conf.GetSettings())
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	const url = "rtsp://cam-bat-cold.invalid/stream"
	settings := &conf.Settings{}
	settings.Realtime.RTSP.Streams = []conf.StreamConfig{
		{
			Name:    "bat-stream-cold",
			URL:     url,
			Enabled: true,
			Type:    conf.StreamTypeRTSP,
			Models:  []string{conf.ModelIDBat},
		},
	}
	conftest.SetTestSettings(settings)

	// Nil engine: the registry consult is skipped, so with no fallback map the
	// probe failure has nothing to fall back on.
	p := &AudioPipelineService{}
	configs := p.buildSourceConfigsWithModels(nil)

	cfg := findConfigByConnection(configs, url)
	require.NotNil(t, cfg, "rtsp stream config should be present")
	assert.Equal(t, 0, cfg.SourceSampleRate, "unknown source rate stays 0 when nothing is known")
	assert.Equal(t, conf.SampleRate, cfg.SampleRate, "output rate falls back to the analysis target")
	assert.False(t, cfg.SourceSampleRateEstimated, "nothing was retained, so the rate is not flagged estimated (SourceSampleRate 0 already forces resampling)")
}
