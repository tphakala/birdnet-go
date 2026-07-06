package conf

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestStreamTypeForURL exercises the strict stream classifier used by
// ReconcileMisplacedAudioSources. Unlike inferStreamType it must never promote
// an unknown scheme or a sound-card device name to a stream type.
func TestStreamTypeForURL(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		device     string
		wantType   string
		wantStream bool
	}{
		// Stream schemes.
		{"rtsp", "rtsp://192.168.1.10/stream", StreamTypeRTSP, true},
		{"rtsps", "rtsps://secure.cam/stream", StreamTypeRTSP, true},
		{"rtsp uppercase", "RTSP://192.168.1.10/stream", StreamTypeRTSP, true},
		{"rtmp", "rtmp://192.168.1.60/live", StreamTypeRTMP, true},
		{"rtmps", "rtmps://secure.rtmp/live", StreamTypeRTMP, true},
		{"udp", "udp://239.0.0.1:1234", StreamTypeUDP, true},
		{"rtp", "rtp://239.0.0.1:5004", StreamTypeUDP, true},
		{"hls http", "http://server/live/playlist.m3u8", StreamTypeHLS, true},
		{"hls https", "https://cdn.example.com/stream.m3u8?token=abc", StreamTypeHLS, true},
		{"http", "http://192.168.1.50:8000/audio", StreamTypeHTTP, true},
		{"https", "https://secure.server/stream", StreamTypeHTTP, true},
		{"whitespace trimmed", "  rtsp://192.168.1.10/stream  ", StreamTypeRTSP, true},

		// Local sound cards and files must NOT classify as streams.
		{"hw device", "hw:0,0", "", false},
		{"plughw device", "plughw:1,0", "", false},
		{"sysdefault", "sysdefault", "", false},
		{"default", "default", "", false},
		{"dsnoop", "dsnoop", "", false},
		{"loopback", "Loopback", "", false},
		{"bare name", "USB Audio Device", "", false},
		{"file path wav", "/tmp/recording.wav", "", false},
		{"empty", "", "", false},

		// Unknown schemes must NOT default to RTSP (regression guard).
		{"ftp scheme", "ftp://server/file", "", false},
		{"unknown scheme", "unknown://something", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			gotType, gotStream := streamTypeForURL(tt.device)
			assert.Equal(t, tt.wantStream, gotStream, "isStream mismatch for %q", tt.device)
			assert.Equal(t, tt.wantType, gotType, "streamType mismatch for %q", tt.device)
		})
	}
}

// TestSettings_ReconcileMisplacedAudioSources_MovesStreamURL verifies that a
// full stream URL (with credentials) misconfigured under audio.sources is moved
// into rtsp.streams with every field carried over and credentials preserved.
func TestSettings_ReconcileMisplacedAudioSources_MovesStreamURL(t *testing.T) {
	t.Parallel()

	eq := &EqualizerSettings{Enabled: true}
	qh := QuietHoursConfig{Enabled: true, Mode: "fixed", StartTime: "22:00", EndTime: "06:00"}

	settings := Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{
			Name:       "Backyard Cam",
			Device:     "rtsp://admin:secret@192.168.1.100:554/h264/main",
			Models:     []string{"birdnet", "perch_v2"},
			Equalizer:  eq,
			QuietHours: qh,
		},
	}

	changed := settings.ReconcileMisplacedAudioSources()
	require.True(t, changed, "reconcile should report a change")

	assert.Empty(t, settings.Realtime.Audio.Sources, "stream source should be removed from audio.sources")
	require.Len(t, settings.Realtime.RTSP.Streams, 1, "stream should be added to rtsp.streams")

	stream := settings.Realtime.RTSP.Streams[0]
	assert.Equal(t, "Backyard Cam", stream.Name)
	assert.Equal(t, "rtsp://admin:secret@192.168.1.100:554/h264/main", stream.URL, "credentials must be preserved verbatim")
	assert.Equal(t, StreamTypeRTSP, stream.Type)
	assert.Equal(t, DefaultTransport, stream.Transport)
	assert.True(t, stream.Enabled)
	assert.Equal(t, []string{"birdnet", "perch_v2"}, stream.Models)
	assert.Same(t, eq, stream.Equalizer)
	assert.Equal(t, qh, stream.QuietHours)

	assert.NotEmpty(t, settings.ValidationWarnings, "a warning should be recorded for the move")
	// Credentials must not leak into the warning text.
	for _, w := range settings.ValidationWarnings {
		assert.NotContains(t, w, "secret", "warning must use a sanitized URL")
	}
}

// TestSettings_ReconcileMisplacedAudioSources_AllStreamSchemes ensures every
// recognized stream scheme is classified and moved with the correct type and
// transport assignment.
func TestSettings_ReconcileMisplacedAudioSources_AllStreamSchemes(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		device        string
		wantType      string
		wantTransport string
	}{
		{"rtsp", "rtsp://cam/stream", StreamTypeRTSP, DefaultTransport},
		{"rtsps", "rtsps://cam/stream", StreamTypeRTSP, DefaultTransport},
		{"rtmp", "rtmp://live/stream", StreamTypeRTMP, DefaultTransport},
		{"rtmps", "rtmps://live/stream", StreamTypeRTMP, DefaultTransport},
		{"udp", "udp://239.0.0.1:1234", StreamTypeUDP, ""},
		{"rtp", "rtp://239.0.0.1:5004", StreamTypeUDP, ""},
		{"hls", "https://cdn/live.m3u8", StreamTypeHLS, ""},
		{"http", "http://icecast:8000/audio", StreamTypeHTTP, ""},
		{"https", "https://server/audio", StreamTypeHTTP, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := Settings{}
			settings.Realtime.Audio.Sources = []AudioSourceConfig{
				{Name: "Src", Device: tt.device},
			}

			changed := settings.ReconcileMisplacedAudioSources()
			require.True(t, changed)
			assert.Empty(t, settings.Realtime.Audio.Sources)
			require.Len(t, settings.Realtime.RTSP.Streams, 1)

			stream := settings.Realtime.RTSP.Streams[0]
			assert.Equal(t, tt.wantType, stream.Type)
			assert.Equal(t, tt.wantTransport, stream.Transport)
			assert.Equal(t, tt.device, stream.URL)
			assert.True(t, stream.Enabled)
		})
	}
}

// TestSettings_ReconcileMisplacedAudioSources_LocalDevicesStay ensures that
// genuine local audio devices are never promoted to streams.
func TestSettings_ReconcileMisplacedAudioSources_LocalDevicesStay(t *testing.T) {
	t.Parallel()

	devices := []string{"hw:0,0", "sysdefault", "Loopback", "USB Audio", "/tmp/x.wav", ""}

	settings := Settings{}
	for _, d := range devices {
		settings.Realtime.Audio.Sources = append(settings.Realtime.Audio.Sources,
			AudioSourceConfig{Name: "local", Device: d})
	}

	changed := settings.ReconcileMisplacedAudioSources()
	assert.False(t, changed, "no local device should be moved")
	assert.Len(t, settings.Realtime.Audio.Sources, len(devices), "all local sources must be retained")
	assert.Empty(t, settings.Realtime.RTSP.Streams, "no streams should be created")
	assert.Empty(t, settings.ValidationWarnings)
}

// TestSettings_ReconcileMisplacedAudioSources_FallbackName verifies the
// generated stream name when the source has no name.
func TestSettings_ReconcileMisplacedAudioSources_FallbackName(t *testing.T) {
	t.Parallel()

	settings := Settings{}
	settings.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "Existing", URL: "rtsp://cam0/stream", Type: StreamTypeRTSP, Enabled: true},
	}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "", Device: "rtsp://cam1/stream"},
	}

	changed := settings.ReconcileMisplacedAudioSources()
	require.True(t, changed)
	require.Len(t, settings.Realtime.RTSP.Streams, 2)
	// New stream is appended after the existing one; N = len(streams)+1 = 2.
	assert.Equal(t, "Stream 2", settings.Realtime.RTSP.Streams[1].Name)
}

// TestSettings_ReconcileMisplacedAudioSources_UniqueNameOnCollision verifies
// the migration never produces a duplicate stream name, which the validator
// would otherwise reject with a fatal error at load time. It covers both a
// non-empty source name colliding (case-insensitively) with an existing stream
// and the "Stream N" fallback colliding with a manually named stream.
func TestSettings_ReconcileMisplacedAudioSources_UniqueNameOnCollision(t *testing.T) {
	t.Parallel()

	t.Run("non-empty name collides case-insensitively", func(t *testing.T) {
		t.Parallel()

		settings := Settings{}
		settings.Realtime.RTSP.Streams = []StreamConfig{
			{Name: "Backyard", URL: "rtsp://cam0/stream", Type: StreamTypeRTSP, Enabled: true},
		}
		settings.Realtime.Audio.Sources = []AudioSourceConfig{
			{Name: "backyard", Device: "rtsp://cam2/stream"}, // collides with "Backyard"
		}

		require.True(t, settings.ReconcileMisplacedAudioSources())
		require.Len(t, settings.Realtime.RTSP.Streams, 2)
		assert.Equal(t, "backyard (2)", settings.Realtime.RTSP.Streams[1].Name)
		// The reconciled config must pass the same validator that runs at load
		// time; asserting it directly (rather than re-implementing the rule)
		// covers the duplicate-name and length checks and stays drift-proof.
		require.NoError(t, settings.Realtime.RTSP.ValidateStreams())
	})

	t.Run("empty-name fallback collides with manual name", func(t *testing.T) {
		t.Parallel()

		settings := Settings{}
		// One existing stream, so the empty-name fallback base is "Stream 2"
		// (len+1), which is already taken by the manually named stream below.
		settings.Realtime.RTSP.Streams = []StreamConfig{
			{Name: "Stream 2", URL: "rtsp://cam0/stream", Type: StreamTypeRTSP, Enabled: true},
		}
		settings.Realtime.Audio.Sources = []AudioSourceConfig{
			{Name: "", Device: "rtsp://cam3/stream"},
		}

		require.True(t, settings.ReconcileMisplacedAudioSources())
		require.Len(t, settings.Realtime.RTSP.Streams, 2)
		assert.Equal(t, "Stream 2 (2)", settings.Realtime.RTSP.Streams[1].Name)
		require.NoError(t, settings.Realtime.RTSP.ValidateStreams())
	})

	t.Run("repeated collisions increment the suffix", func(t *testing.T) {
		t.Parallel()

		// "Cam" and "Cam (2)" are both taken, so the moved source must become
		// "Cam (3)" - exercises the increment loop past the first suffix.
		settings := Settings{}
		settings.Realtime.RTSP.Streams = []StreamConfig{
			{Name: "Cam", URL: "rtsp://cam0/stream", Type: StreamTypeRTSP, Enabled: true},
			{Name: "Cam (2)", URL: "rtsp://cam1/stream", Type: StreamTypeRTSP, Enabled: true},
		}
		settings.Realtime.Audio.Sources = []AudioSourceConfig{
			{Name: "Cam", Device: "rtsp://cam2/stream"},
		}

		require.True(t, settings.ReconcileMisplacedAudioSources())
		require.Len(t, settings.Realtime.RTSP.Streams, 3)
		assert.Equal(t, "Cam (3)", settings.Realtime.RTSP.Streams[2].Name)
		require.NoError(t, settings.Realtime.RTSP.ValidateStreams())
	})

	t.Run("over-length source name is truncated to a valid name", func(t *testing.T) {
		t.Parallel()

		// A name at MaxAudioSourceNameLength is valid for a source but exceeds
		// MaxStreamNameLength, so the migration must truncate it rather than emit
		// a name the length validator would reject with a fatal error at load.
		longName := strings.Repeat("a", MaxAudioSourceNameLength)
		settings := Settings{}
		settings.Realtime.Audio.Sources = []AudioSourceConfig{
			{Name: longName, Device: "rtsp://cam0/stream"},
		}

		require.True(t, settings.ReconcileMisplacedAudioSources())
		require.Len(t, settings.Realtime.RTSP.Streams, 1)
		assert.LessOrEqual(t, len(settings.Realtime.RTSP.Streams[0].Name), MaxStreamNameLength)
		require.NoError(t, settings.Realtime.RTSP.ValidateStreams())
	})
}

// TestSettings_ReconcileMisplacedAudioSources_DedupMergeDefaultStream verifies
// that when the same URL exists in both places and the stream side is default,
// the source's non-default fields fill the stream and the source is removed.
func TestSettings_ReconcileMisplacedAudioSources_DedupMergeDefaultStream(t *testing.T) {
	t.Parallel()

	eq := &EqualizerSettings{Enabled: true}
	qh := QuietHoursConfig{Enabled: true, Mode: "fixed", StartTime: "22:00", EndTime: "06:00"}

	settings := Settings{}
	settings.Realtime.RTSP.Streams = []StreamConfig{
		{Name: "Cam", URL: "rtsp://cam/stream", Type: StreamTypeRTSP, Transport: DefaultTransport, Enabled: true},
	}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{
			Name:       "DupSrc",
			Device:     "rtsp://cam/stream",
			Models:     []string{"perch_v2"},
			Equalizer:  eq,
			QuietHours: qh,
		},
	}

	changed := settings.ReconcileMisplacedAudioSources()
	require.True(t, changed)
	assert.Empty(t, settings.Realtime.Audio.Sources, "duplicate source must be removed")
	require.Len(t, settings.Realtime.RTSP.Streams, 1, "no new stream should be created for a duplicate URL")

	stream := settings.Realtime.RTSP.Streams[0]
	assert.Equal(t, []string{"perch_v2"}, stream.Models, "models should be filled from source")
	assert.Same(t, eq, stream.Equalizer, "equalizer should be filled from source")
	assert.Equal(t, qh, stream.QuietHours, "quiet hours should be filled from source")
	assert.NotEmpty(t, settings.ValidationWarnings)
}

// TestSettings_ReconcileMisplacedAudioSources_DedupMergeNonDefaultStream
// verifies that a stream's existing non-default values are never overwritten by
// the duplicate source, and the warning notes what was kept.
func TestSettings_ReconcileMisplacedAudioSources_DedupMergeNonDefaultStream(t *testing.T) {
	t.Parallel()

	streamEq := &EqualizerSettings{Enabled: true}
	srcEq := &EqualizerSettings{Enabled: false}
	streamQH := QuietHoursConfig{Enabled: true, Mode: "fixed", StartTime: "01:00", EndTime: "02:00"}
	srcQH := QuietHoursConfig{Enabled: true, Mode: "fixed", StartTime: "22:00", EndTime: "06:00"}

	settings := Settings{}
	settings.Realtime.RTSP.Streams = []StreamConfig{
		{
			Name:       "Cam",
			URL:        "rtsp://cam/stream",
			Type:       StreamTypeRTSP,
			Transport:  DefaultTransport,
			Enabled:    true,
			Models:     []string{"birdnet"},
			Equalizer:  streamEq,
			QuietHours: streamQH,
		},
	}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{
			Name:       "DupSrc",
			Device:     "rtsp://cam/stream",
			Models:     []string{"perch_v2"},
			Equalizer:  srcEq,
			QuietHours: srcQH,
		},
	}

	changed := settings.ReconcileMisplacedAudioSources()
	require.True(t, changed)
	assert.Empty(t, settings.Realtime.Audio.Sources)
	require.Len(t, settings.Realtime.RTSP.Streams, 1)

	stream := settings.Realtime.RTSP.Streams[0]
	assert.Equal(t, []string{"birdnet"}, stream.Models, "existing stream models must be preserved")
	assert.Same(t, streamEq, stream.Equalizer, "existing stream equalizer must be preserved")
	assert.Equal(t, streamQH, stream.QuietHours, "existing stream quiet hours must be preserved")

	require.NotEmpty(t, settings.ValidationWarnings)
	joined := ""
	for _, w := range settings.ValidationWarnings {
		joined += w + "\n"
	}
	assert.Contains(t, joined, "models", "warning should mention models were kept")
	assert.Contains(t, joined, "equalizer", "warning should mention equalizer was kept")
	assert.Contains(t, joined, "quietHours", "warning should mention quietHours were kept")
}

// TestSettings_ReconcileMisplacedAudioSources_SampleRateWarning verifies that a
// non-zero SampleRate on a moved source emits a warning since stream sample
// rates are auto-detected.
func TestSettings_ReconcileMisplacedAudioSources_SampleRateWarning(t *testing.T) {
	t.Parallel()

	settings := Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Src", Device: "rtsp://cam/stream", SampleRate: 96000},
	}

	changed := settings.ReconcileMisplacedAudioSources()
	require.True(t, changed)
	require.Len(t, settings.Realtime.RTSP.Streams, 1)

	joined := ""
	for _, w := range settings.ValidationWarnings {
		joined += w + "\n"
	}
	assert.Contains(t, joined, "sample rate", "a sample-rate warning should be recorded")
}

// TestSettings_ReconcileMisplacedAudioSources_Idempotent ensures a second call
// is a no-op once every stream URL has been relocated.
func TestSettings_ReconcileMisplacedAudioSources_Idempotent(t *testing.T) {
	t.Parallel()

	settings := Settings{}
	settings.Realtime.Audio.Sources = []AudioSourceConfig{
		{Name: "Cam", Device: "rtsp://cam/stream"},
		{Name: "Mic", Device: "hw:0,0"},
	}

	changed1 := settings.ReconcileMisplacedAudioSources()
	require.True(t, changed1)
	require.Len(t, settings.Realtime.RTSP.Streams, 1)
	require.Len(t, settings.Realtime.Audio.Sources, 1, "only the local mic should remain")

	streamsAfterFirst := settings.Realtime.RTSP.Streams[0]
	sourcesLen := len(settings.Realtime.Audio.Sources)

	changed2 := settings.ReconcileMisplacedAudioSources()
	assert.False(t, changed2, "second call must be a no-op")
	require.Len(t, settings.Realtime.RTSP.Streams, 1)
	assert.Equal(t, streamsAfterFirst, settings.Realtime.RTSP.Streams[0], "streams must not change on second call")
	assert.Len(t, settings.Realtime.Audio.Sources, sourcesLen, "sources must not change on second call")
}

// TestSettings_ReconcileMisplacedAudioSources_EmptySources verifies the no-op
// path when there are no audio sources at all.
func TestSettings_ReconcileMisplacedAudioSources_EmptySources(t *testing.T) {
	t.Parallel()

	settings := Settings{}
	changed := settings.ReconcileMisplacedAudioSources()
	assert.False(t, changed)
}
