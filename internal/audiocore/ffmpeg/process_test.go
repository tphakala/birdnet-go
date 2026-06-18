package ffmpeg

import (
	"slices"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// TestBuildFFmpegArgs_RTSP verifies that BuildFFmpegArgs produces the correct
// argument sequence for an RTSP stream source.
func TestBuildFFmpegArgs_RTSP(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:        "rtsp://camera.example.com/live",
		Type:       "rtsp",
		SampleRate: 48000,
		BitDepth:   16,
		Channels:   1,
		Transport:  "tcp",
		LogLevel:   "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	// RTSP transport flag must be present.
	rtspIdx := slices.Index(args, "-rtsp_transport")
	require.NotEqual(t, -1, rtspIdx, "expected -rtsp_transport flag")
	require.Less(t, rtspIdx+1, len(args), "-rtsp_transport must have a value")
	assert.Equal(t, "tcp", args[rtspIdx+1])

	// Empty FFmpegPath yields an unknown version, which safely falls back to -timeout.
	timeoutIdx := slices.Index(args, "-timeout")
	require.NotEqual(t, -1, timeoutIdx, "expected -timeout flag for RTSP")
	assert.Equal(t, -1, slices.Index(args, "-stimeout"), "RTSP must not use legacy -stimeout")

	// Input URL must be present.
	iIdx := slices.Index(args, "-i")
	require.NotEqual(t, -1, iIdx, "expected -i flag")
	require.Less(t, iIdx+1, len(args), "-i must have a value")
	assert.Equal(t, cfg.URL, args[iIdx+1])

	// Output must go to stdout pipe.
	assert.Equal(t, "pipe:1", args[len(args)-1])

	// Audio codec and format flags must be present.
	assert.Contains(t, args, "-ar")
	assert.Contains(t, args, "-ac")
	assert.Contains(t, args, "-f")
	assert.Contains(t, args, "-vn")
}

// TestBuildFFmpegArgs_HTTP verifies that BuildFFmpegArgs produces the correct
// argument sequence for an HTTP stream source and does NOT include RTSP flags.
func TestBuildFFmpegArgs_HTTP(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:        "http://radio.example.com/stream.mp3",
		Type:       "http",
		SampleRate: 44100,
		BitDepth:   16,
		Channels:   2,
		Transport:  "tcp",
		LogLevel:   "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	// RTSP transport flag must NOT be present for HTTP sources.
	assert.Equal(t, -1, slices.Index(args, "-rtsp_transport"), "HTTP source must not include -rtsp_transport")

	// HTTP must use -timeout, not -stimeout.
	assert.NotEqual(t, -1, slices.Index(args, "-timeout"), "expected -timeout flag for HTTP")
	assert.Equal(t, -1, slices.Index(args, "-stimeout"), "HTTP must not use -stimeout")

	// Input URL must be present.
	iIdx := slices.Index(args, "-i")
	require.NotEqual(t, -1, iIdx, "expected -i flag")
	require.Less(t, iIdx+1, len(args), "-i must have a value")
	assert.Equal(t, cfg.URL, args[iIdx+1])

	// Output must go to stdout pipe.
	assert.Equal(t, "pipe:1", args[len(args)-1])
}

// TestBuildFFmpegArgs_CustomTimeout verifies that a user-provided valid timeout
// is converted to the correct flag for the source type.
func TestBuildFFmpegArgs_CustomTimeout(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:       "rtsp://camera.example.com/live",
		Type:      "rtsp",
		Transport: "tcp",
	}

	// Empty FFmpegPath yields an unknown version, so RTSP uses -timeout.
	customParams := []string{"-timeout", "5000000"}
	args := BuildFFmpegArgs(cfg, customParams)

	// The user's value must be honoured on the -timeout flag.
	timeoutIdx := slices.Index(args, "-timeout")
	require.NotEqual(t, -1, timeoutIdx, "expected -timeout for RTSP")
	require.Less(t, timeoutIdx+1, len(args), "-timeout must have a value")
	assert.Equal(t, "5000000", args[timeoutIdx+1])

	// Legacy -stimeout must not appear.
	assert.Equal(t, -1, slices.Index(args, "-stimeout"), "RTSP must not contain legacy -stimeout")

	// -timeout must appear only once.
	count := 0
	for _, a := range args {
		if a == "-timeout" {
			count++
		}
	}
	assert.Equal(t, 1, count, "-timeout must appear exactly once")
}

func TestTimeoutParamForSource(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sourceType  audiocore.SourceType
		ffmpegMajor int
		want        string
	}{
		{"rtsp_ffmpeg4_uses_stimeout", audiocore.SourceTypeRTSP, 4, "-stimeout"},
		{"rtsp_ffmpeg5_uses_timeout", audiocore.SourceTypeRTSP, 5, "-timeout"},
		{"rtsp_ffmpeg6_uses_timeout", audiocore.SourceTypeRTSP, 6, "-timeout"},
		{"rtsp_ffmpeg7_uses_timeout", audiocore.SourceTypeRTSP, 7, "-timeout"},
		{"rtsp_unknown_uses_timeout", audiocore.SourceTypeRTSP, 0, "-timeout"},
		{"http_ffmpeg4_uses_timeout", audiocore.SourceTypeHTTP, 4, "-timeout"},
		{"http_ffmpeg7_uses_timeout", audiocore.SourceTypeHTTP, 7, "-timeout"},
		{"hls_ffmpeg4_uses_timeout", audiocore.SourceTypeHLS, 4, "-timeout"},
		{"hls_ffmpeg7_uses_timeout", audiocore.SourceTypeHLS, 7, "-timeout"},
		{"rtmp_ffmpeg4_uses_timeout", audiocore.SourceTypeRTMP, 4, "-timeout"},
		{"rtmp_ffmpeg7_uses_timeout", audiocore.SourceTypeRTMP, 7, "-timeout"},
		{"udp_ffmpeg4_uses_timeout", audiocore.SourceTypeUDP, 4, "-timeout"},
		{"udp_ffmpeg7_uses_timeout", audiocore.SourceTypeUDP, 7, "-timeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tt.want, timeoutParamForSource(tt.sourceType, tt.ffmpegMajor))
		})
	}
}

func TestBuildFFmpegArgs_RTSP_LegacyFFmpeg4(t *testing.T) {
	tests := []struct {
		name          string
		ffmpegPath    string
		ffmpegMajor   int
		wantFlag      string
		forbiddenFlag string
	}{
		{
			name:          "ffmpeg4_uses_stimeout",
			ffmpegPath:    "/fake/ffmpeg4",
			ffmpegMajor:   4,
			wantFlag:      "-stimeout",
			forbiddenFlag: "-timeout",
		},
		{
			name:          "ffmpeg7_uses_timeout",
			ffmpegPath:    "/fake/ffmpeg7",
			ffmpegMajor:   7,
			wantFlag:      "-timeout",
			forbiddenFlag: "-stimeout",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ffmpegMajorCache.Store(tt.ffmpegPath, tt.ffmpegMajor)
			t.Cleanup(func() {
				ffmpegMajorCache.Delete(tt.ffmpegPath)
			})

			cfg := &StreamConfig{
				URL:        "rtsp://camera.example.com/live",
				Type:       "rtsp",
				Transport:  "tcp",
				FFmpegPath: tt.ffmpegPath,
			}

			args := BuildFFmpegArgs(cfg, nil)

			assertFlagValue(t, args, tt.wantFlag, strconv.FormatInt(defaultTimeoutMicroseconds, 10))
			assert.NotContains(t, args, tt.forbiddenFlag)
		})
	}
}

func TestStripTimeoutParams(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		input  []string
		expect []string
	}{
		{"empty", []string{}, []string{}},
		{"no_timeout", []string{"-loglevel", "debug"}, []string{"-loglevel", "debug"}},
		{"strip_timeout", []string{"-timeout", "5000000", "-loglevel", "debug"}, []string{"-loglevel", "debug"}},
		{"strip_stimeout", []string{"-stimeout", "5000000", "-loglevel", "debug"}, []string{"-loglevel", "debug"}},
		{"strip_both", []string{"-timeout", "3000000", "-stimeout", "5000000"}, []string{}},
		{"timeout_at_end", []string{"-loglevel", "debug", "-timeout", "5000000"}, []string{"-loglevel", "debug"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := stripTimeoutParams(tt.input)
			assert.Equal(t, tt.expect, result)
		})
	}
}

// TestBuildFFmpegArgs_ChannelModeLeft verifies that a stereo source with
// channel mode "left" produces a pan filter extracting the left channel.
func TestBuildFFmpegArgs_ChannelModeLeft(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:            "rtsp://camera.example.com/live",
		Type:           "rtsp",
		SampleRate:     48000,
		BitDepth:       16,
		Channels:       1,
		SourceChannels: 2,
		ChannelMode:    "left",
		Transport:      "tcp",
		LogLevel:       "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	afIdx := slices.Index(args, "-af")
	require.NotEqual(t, -1, afIdx, "expected -af flag for left channel extraction")
	require.Less(t, afIdx+1, len(args), "-af must have a value")
	assert.Equal(t, "pan=mono|c0=c0", args[afIdx+1])
	assertFlagValue(t, args, "-ac", "1")
}

// TestBuildFFmpegArgs_ChannelModeRight verifies that a stereo source with
// channel mode "right" produces a pan filter extracting the right channel.
func TestBuildFFmpegArgs_ChannelModeRight(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:            "rtsp://camera.example.com/live",
		Type:           "rtsp",
		SampleRate:     48000,
		BitDepth:       16,
		Channels:       1,
		SourceChannels: 2,
		ChannelMode:    "right",
		Transport:      "tcp",
		LogLevel:       "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	afIdx := slices.Index(args, "-af")
	require.NotEqual(t, -1, afIdx, "expected -af flag for right channel extraction")
	require.Less(t, afIdx+1, len(args), "-af must have a value")
	assert.Equal(t, "pan=mono|c0=c1", args[afIdx+1])
	assertFlagValue(t, args, "-ac", "1")
}

// TestBuildFFmpegArgs_ChannelModeDownmix verifies that "downmix" mode uses
// simple -ac 1 (mono downmix) without a pan filter.
func TestBuildFFmpegArgs_ChannelModeDownmix(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:            "rtsp://camera.example.com/live",
		Type:           "rtsp",
		SampleRate:     48000,
		BitDepth:       16,
		Channels:       1,
		SourceChannels: 2,
		ChannelMode:    "downmix",
		Transport:      "tcp",
		LogLevel:       "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	assert.NotContains(t, args, "-af")
	assertFlagValue(t, args, "-ac", "1")
}

// TestBuildFFmpegArgs_ChannelModeEmpty verifies that an unset channel mode (the
// default for existing streams that predate the feature) downmixes a stereo
// source to mono with -ac 1 and no pan filter, matching pre-feature behavior.
func TestBuildFFmpegArgs_ChannelModeEmpty(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:            "rtsp://camera.example.com/live",
		Type:           "rtsp",
		SampleRate:     48000,
		BitDepth:       16,
		Channels:       1,
		SourceChannels: 2,
		ChannelMode:    "",
		Transport:      "tcp",
		LogLevel:       "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	assert.NotContains(t, args, "-af")
	assertFlagValue(t, args, "-ac", "1")
}

// TestBuildFFmpegArgs_OutputResampling verifies that -ar is emitted only when
// the source sample rate is unknown or differs from the target, and omitted
// when the probed source rate already matches the target. -ac must always be
// present so multi-channel sources still downmix to mono regardless of -ar.
func TestBuildFFmpegArgs_OutputResampling(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name             string
		sourceSampleRate int
		wantAr           bool
	}{
		{"unknown_source_rate_resamples", 0, true},
		{"differing_rate_resamples", 44100, true},
		{"matching_rate_skips_ar", 48000, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			cfg := &StreamConfig{
				URL:              "rtsp://camera.example.com/live",
				Type:             "rtsp",
				SampleRate:       48000,
				SourceSampleRate: tt.sourceSampleRate,
				BitDepth:         16,
				Channels:         1,
				Transport:        "tcp",
				LogLevel:         "error",
			}

			args := BuildFFmpegArgs(cfg, nil)

			if tt.wantAr {
				assertFlagValue(t, args, "-ar", "48000")
			} else {
				assert.NotContains(t, args, "-ar", "expected no -ar when source rate matches target")
			}
			// -ac must always be present so stereo sources downmix to mono.
			assertFlagValue(t, args, "-ac", "1")
		})
	}
}

// TestBuildFFmpegArgs_ChannelModeLeftOnMonoSource verifies the safety guard:
// a mono source never gets a pan filter, even when channel mode is "left".
func TestBuildFFmpegArgs_ChannelModeLeftOnMonoSource(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:            "rtsp://camera.example.com/live",
		Type:           "rtsp",
		SampleRate:     48000,
		BitDepth:       16,
		Channels:       1,
		SourceChannels: 1,
		ChannelMode:    "left",
		Transport:      "tcp",
		LogLevel:       "error",
	}

	args := BuildFFmpegArgs(cfg, nil)

	// Safety guard: mono source should NOT get pan filter, and must downmix to mono.
	assert.NotContains(t, args, "-af")
	assertFlagValue(t, args, "-ac", "1")
}

// assertFlagValue asserts that flag appears in args immediately followed by want.
func assertFlagValue(t *testing.T, args []string, flag, want string) {
	t.Helper()
	idx := slices.Index(args, flag)
	require.NotEqual(t, -1, idx, "expected %s flag", flag)
	require.Less(t, idx+1, len(args), "%s must have a value", flag)
	assert.Equal(t, want, args[idx+1], "unexpected value for %s", flag)
}

// TestBackoffCalculation verifies that CalculateBackoff implements exponential
// backoff with a jitter ceiling and respects the maximum backoff cap.
func TestBackoffCalculation(t *testing.T) {
	t.Parallel()

	base := 5 * time.Second
	maxDur := 2 * time.Minute

	tests := []struct {
		name         string
		restartCount int
		wantMin      time.Duration // base backoff (no jitter)
		wantMax      time.Duration // base backoff + max jitter (20%)
	}{
		{
			name:         "first restart",
			restartCount: 1,
			wantMin:      5 * time.Second,
			wantMax:      6 * time.Second, // 5s + 20%
		},
		{
			name:         "second restart",
			restartCount: 2,
			wantMin:      10 * time.Second,
			wantMax:      12 * time.Second, // 10s + 20%
		},
		{
			name:         "third restart",
			restartCount: 3,
			wantMin:      20 * time.Second,
			wantMax:      24 * time.Second, // 20s + 20%
		},
		{
			name:         "capped at maxBackoff",
			restartCount: 20,
			wantMin:      maxDur,
			wantMax:      maxDur + maxDur/5, // maxBackoff + 20%
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			got := CalculateBackoff(tt.restartCount, base, maxDur)
			assert.GreaterOrEqual(t, got, tt.wantMin,
				"backoff must be at least the base duration")
			assert.LessOrEqual(t, got, tt.wantMax,
				"backoff must not exceed base + 20% jitter ceiling")
		})
	}
}

// TestBackoffCalculation_ZeroRestarts verifies the edge case of zero restart count.
func TestBackoffCalculation_ZeroRestarts(t *testing.T) {
	t.Parallel()

	base := 5 * time.Second
	maxDur := 2 * time.Minute

	got := CalculateBackoff(0, base, maxDur)
	// restart count 0: exponent clamped to 0, so backoff = base * 2^0 = base.
	assert.GreaterOrEqual(t, got, base)
	assert.LessOrEqual(t, got, base+base/5)
}

// TestComputeBaseBackoff verifies the jitter-free exponential backoff: doubling
// per restart, exponent clamped at zero, and capped at maxBackoff.
func TestComputeBaseBackoff(t *testing.T) {
	t.Parallel()

	base := 5 * time.Second
	maxDur := 2 * time.Minute

	tests := []struct {
		name         string
		restartCount int
		want         time.Duration
	}{
		{"zero clamps to base", 0, 5 * time.Second},
		{"first restart", 1, 5 * time.Second},
		{"second restart", 2, 10 * time.Second},
		{"third restart", 3, 20 * time.Second},
		{"capped at maxBackoff", 20, maxDur},
		{"large count stays capped", 1000, maxDur},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, computeBaseBackoff(tt.restartCount, base, maxDur))
		})
	}
}

// TestExtremeFailurePenalty verifies the escalating delay applied once the
// restart count exceeds extremeFailureThreshold, including the cap.
func TestExtremeFailurePenalty(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		restartCount int
		want         time.Duration
	}{
		{"below threshold", extremeFailureThreshold - 1, 0},
		{"at threshold", extremeFailureThreshold, 0},
		{"one past threshold", extremeFailureThreshold + 1, extremeFailureDelayStep},
		{"ten past threshold", extremeFailureThreshold + 10, 10 * extremeFailureDelayStep},
		{"capped", extremeFailureThreshold + 1000, extremeFailureDelayCap},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, extremeFailurePenalty(tt.restartCount))
		})
	}
}

// TestApplyBackoffJitter verifies jitter stays within the [backoff, backoff+20%]
// band and that non-positive inputs pass through unchanged.
func TestApplyBackoffJitter(t *testing.T) {
	t.Parallel()

	const backoff = 10 * time.Second
	for range 100 {
		got := applyBackoffJitter(backoff)
		assert.GreaterOrEqual(t, got, backoff)
		assert.LessOrEqual(t, got, backoff+backoff/5)
	}

	assert.Equal(t, time.Duration(0), applyBackoffJitter(0))
	assert.Equal(t, -time.Second, applyBackoffJitter(-time.Second))
}
