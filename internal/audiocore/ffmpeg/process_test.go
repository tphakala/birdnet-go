package ffmpeg

import (
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

	// Default timeout must be present when no user timeout is supplied.
	timeoutIdx := slices.Index(args, "-timeout")
	require.NotEqual(t, -1, timeoutIdx, "expected -timeout flag")

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

	// Default timeout should still be present.
	assert.NotEqual(t, -1, slices.Index(args, "-timeout"), "expected -timeout flag")

	// Input URL must be present.
	iIdx := slices.Index(args, "-i")
	require.NotEqual(t, -1, iIdx, "expected -i flag")
	require.Less(t, iIdx+1, len(args), "-i must have a value")
	assert.Equal(t, cfg.URL, args[iIdx+1])

	// Output must go to stdout pipe.
	assert.Equal(t, "pipe:1", args[len(args)-1])
}

// TestBuildFFmpegArgs_CustomTimeout verifies that a user-provided valid timeout
// replaces the default and is included in the output args.
func TestBuildFFmpegArgs_CustomTimeout(t *testing.T) {
	t.Parallel()

	cfg := &StreamConfig{
		URL:       "rtsp://camera.example.com/live",
		Type:      "rtsp",
		Transport: "tcp",
	}

	// 5 seconds in microseconds — above the 1-second minimum.
	customParams := []string{"-timeout", "5000000"}
	args := BuildFFmpegArgs(cfg, customParams)

	timeoutIdx := slices.Index(args, "-timeout")
	require.NotEqual(t, -1, timeoutIdx)
	require.Less(t, timeoutIdx+1, len(args), "-timeout must have a value")
	assert.Equal(t, "5000000", args[timeoutIdx+1])

	// Must appear only once.
	count := 0
	for _, a := range args {
		if a == ffmpegTimeoutFlag {
			count++
		}
	}
	assert.Equal(t, 1, count, "timeout flag must appear exactly once")
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
