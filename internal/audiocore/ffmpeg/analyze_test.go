package ffmpeg

import (
	"math"
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestDeinterleave(t *testing.T) {
	t.Parallel()

	// Stereo s16le: L=1000, R=2000, L=1000, R=2000
	pcm := []byte{
		0xe8, 0x03, // 1000 LE
		0xd0, 0x07, // 2000 LE
		0xe8, 0x03, // 1000 LE
		0xd0, 0x07, // 2000 LE
	}

	left, right := deinterleave(pcm)
	require.Len(t, left, 2)
	require.Len(t, right, 2)
	assert.Equal(t, int16(1000), left[0])
	assert.Equal(t, int16(1000), left[1])
	assert.Equal(t, int16(2000), right[0])
	assert.Equal(t, int16(2000), right[1])
}

func TestComputeRmsDbfs(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		samples   []int16
		wantAbout float64
		tolerance float64
	}{
		{
			name:      "full scale sine approximation",
			samples:   []int16{32767, 0, -32767, 0},
			wantAbout: -3.0,
			tolerance: 0.1,
		},
		{
			name:      "silence",
			samples:   []int16{0, 0, 0, 0},
			wantAbout: -96.0,
			tolerance: 1.0,
		},
		{
			name:      "constant low signal",
			samples:   []int16{100, 100, 100, 100},
			wantAbout: -50.3,
			tolerance: 0.5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := computeRmsDbfs(tt.samples)
			assert.InDelta(t, tt.wantAbout, got, tt.tolerance,
				"RMS dBFS mismatch: got %.2f, want ~%.2f", got, tt.wantAbout)
		})
	}
}

func TestRecommendChannel(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		leftDbfs  float64
		rightDbfs float64
		want      string
	}{
		{"left louder by >6dB", -12.0, -25.0, "left"},
		{"right louder by >6dB", -30.0, -15.0, "right"},
		{"similar energy", -12.0, -14.0, "downmix"},
		{"exactly 6dB difference", -12.0, -18.0, "downmix"},
		{"left just over threshold", -12.0, -18.1, "left"},
		{"both silent", -96.0, -96.0, "downmix"},
		{"both quiet but diff >6dB", -65.0, -75.0, "downmix"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			got := recommendChannel(tt.leftDbfs, tt.rightDbfs)
			assert.Equal(t, tt.want, got)
		})
	}
}

func TestComputeRmsDbfs_EmptySamples(t *testing.T) {
	t.Parallel()
	got := computeRmsDbfs(nil)
	assert.InDelta(t, silenceFloorDbfs, got, 0.01)
}

func TestComputeRmsDbfs_KnownValue(t *testing.T) {
	t.Parallel()
	samples := make([]int16, 1000)
	for i := range samples {
		samples[i] = 100
	}
	got := computeRmsDbfs(samples)
	expected := 20 * math.Log10(100.0/32768.0)
	assert.InDelta(t, expected, got, 0.01)
}

func TestBuildAnalysisArgs_TimeoutFlag(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		url           string
		ffmpegMajor   int
		wantTransport bool
		wantFlag      string
		forbiddenFlag string
	}{
		{"rtsp_ffmpeg4_uses_stimeout", "rtsp://cam.example.com/live", 4, true, "-stimeout", "-timeout"},
		{"rtsps_ffmpeg4_uses_stimeout", "rtsps://cam.example.com/live", 4, true, "-stimeout", "-timeout"},
		{"rtsp_ffmpeg7_uses_timeout", "rtsp://cam.example.com/live", 7, true, "-timeout", "-stimeout"},
		{"rtsp_unknown_uses_timeout", "rtsp://cam.example.com/live", 0, true, "-timeout", "-stimeout"},
		{"http_ffmpeg4_uses_timeout", "http://host.example.com/stream", 4, false, "-timeout", "-stimeout"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			args := buildAnalysisArgs(tt.url, tt.ffmpegMajor, true)

			assert.Equal(t, tt.wantTransport, slices.Contains(args, "-rtsp_transport"), "rtsp_transport presence mismatch")
			assert.Contains(t, args, tt.wantFlag, "expected timeout flag missing")
			assert.NotContains(t, args, tt.forbiddenFlag, "forbidden timeout flag present")
			// Only audio tracks should be SETUP during the RTSP handshake (issue #3798);
			// non-RTSP sources must not carry the flag.
			if tt.wantTransport {
				allowedIdx := slices.Index(args, "-allowed_media_types")
				require.NotEqual(t, -1, allowedIdx, "expected -allowed_media_types flag for RTSP")
				require.Less(t, allowedIdx+1, len(args), "-allowed_media_types must have a value")
				assert.Equal(t, "audio", args[allowedIdx+1])
			} else {
				assert.NotContains(t, args, "-allowed_media_types")
			}
		})
	}
}

func TestBuildAnalysisArgs_AudioOnlyFallback(t *testing.T) {
	t.Parallel()

	// audioOnly=false is the full-stream fallback for cameras that cannot SETUP
	// the audio track alone (issue #3902): keep the RTSP transport but drop the
	// -allowed_media_types restriction. -vn still extracts audio only after decode.
	args := buildAnalysisArgs("rtsp://cam.example.com/live", 7, false)

	assert.Contains(t, args, "-rtsp_transport")
	assert.Contains(t, args, "tcp")
	assert.NotContains(t, args, "-allowed_media_types")
	assert.Contains(t, args, "-vn")
}
