package ffmpeg

import (
	"slices"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestIsLossyCodec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		codec  string
		expect bool
	}{
		{name: "aac", codec: "aac", expect: true},
		{name: "mp3", codec: "mp3", expect: true},
		{name: "opus", codec: "opus", expect: true},
		{name: "vorbis", codec: "vorbis", expect: true},
		{name: "ac3", codec: "ac3", expect: true},
		{name: "eac3", codec: "eac3", expect: true},
		{name: "wmav2", codec: "wmav2", expect: true},
		{name: "AAC uppercase", codec: "AAC", expect: true},
		{name: "MP3 uppercase", codec: "MP3", expect: true},
		{name: "pcm_s16le", codec: "pcm_s16le", expect: false},
		{name: "pcm_s32le", codec: "pcm_s32le", expect: false},
		{name: "flac", codec: "flac", expect: false},
		{name: "pcm_f32le", codec: "pcm_f32le", expect: false},
		{name: "empty string", codec: "", expect: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expect, IsLossyCodec(tt.codec))
		})
	}
}

func TestParseProbeOutput(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		input     string
		expected  *StreamInfo
		expectErr bool
	}{
		{
			name:  "PCM 192kHz stereo",
			input: `{"streams":[{"codec_name":"pcm_s16le","codec_type":"audio","sample_rate":"192000","channels":2,"bits_per_sample":16}]}`,
			expected: &StreamInfo{
				SampleRate: 192000,
				Channels:   2,
				Codec:      "pcm_s16le",
				BitDepth:   16,
			},
		},
		{
			name:  "AAC 48kHz mono with zero bit depth",
			input: `{"streams":[{"codec_name":"aac","codec_type":"audio","sample_rate":"48000","channels":1,"bits_per_sample":0}]}`,
			expected: &StreamInfo{
				SampleRate: 48000,
				Channels:   1,
				Codec:      "aac",
				BitDepth:   0,
			},
		},
		{
			name:  "FLAC 384kHz",
			input: `{"streams":[{"codec_name":"flac","codec_type":"audio","sample_rate":"384000","channels":1,"bits_per_sample":24}]}`,
			expected: &StreamInfo{
				SampleRate: 384000,
				Channels:   1,
				Codec:      "flac",
				BitDepth:   24,
			},
		},
		{
			name:      "no audio streams",
			input:     `{"streams":[]}`,
			expectErr: true,
		},
		{
			name:      "invalid JSON",
			input:     `not json`,
			expectErr: true,
		},
		{
			name:  "fractional sample rate",
			input: `{"streams":[{"codec_name":"pcm_s16le","codec_type":"audio","sample_rate":"44100/1","channels":1,"bits_per_sample":16}]}`,
			expected: &StreamInfo{
				SampleRate: 44100,
				Channels:   1,
				Codec:      "pcm_s16le",
				BitDepth:   16,
			},
		},
		{
			name:      "zero sample rate",
			input:     `{"streams":[{"codec_name":"pcm_s16le","codec_type":"audio","sample_rate":"0","channels":1,"bits_per_sample":16}]}`,
			expectErr: true,
		},
		{
			name:      "negative sample rate",
			input:     `{"streams":[{"codec_name":"pcm_s16le","codec_type":"audio","sample_rate":"-1","channels":1,"bits_per_sample":16}]}`,
			expectErr: true,
		},
		{
			name:      "invalid sample rate",
			input:     `{"streams":[{"codec_name":"pcm_s16le","codec_type":"audio","sample_rate":"not_a_number","channels":1,"bits_per_sample":16}]}`,
			expectErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result, err := parseProbeOutput([]byte(tt.input))
			if tt.expectErr {
				require.Error(t, err)
				assert.Nil(t, result)
				return
			}

			require.NoError(t, err)
			require.NotNil(t, result)
			assert.Equal(t, tt.expected.SampleRate, result.SampleRate)
			assert.Equal(t, tt.expected.Channels, result.Channels)
			assert.Equal(t, tt.expected.Codec, result.Codec)
			assert.Equal(t, tt.expected.BitDepth, result.BitDepth)
		})
	}
}

func TestBuildProbeArgs(t *testing.T) {
	t.Parallel()

	t.Run("RTSP URL includes tcp transport", func(t *testing.T) {
		t.Parallel()

		args := buildProbeArgs("rtsp://192.168.1.10:554/stream1")

		assert.Contains(t, args, "-rtsp_transport")
		assert.Contains(t, args, "tcp")
		// Only audio tracks should be SETUP during the RTSP handshake (issue #3798).
		allowedIdx := slices.Index(args, "-allowed_media_types")
		require.NotEqual(t, -1, allowedIdx, "expected -allowed_media_types flag for RTSP")
		require.Less(t, allowedIdx+1, len(args), "-allowed_media_types must have a value")
		assert.Equal(t, "audio", args[allowedIdx+1])
		assert.Equal(t, "rtsp://192.168.1.10:554/stream1", args[len(args)-1])
		assert.Contains(t, args, "-v")
		assert.Contains(t, args, "quiet")
		assert.Contains(t, args, "-print_format")
		assert.Contains(t, args, "json")
	})

	t.Run("RTSPS URL includes tcp transport", func(t *testing.T) {
		t.Parallel()

		args := buildProbeArgs("rtsps://camera.example.com/live")

		assert.Contains(t, args, "-rtsp_transport")
		assert.Contains(t, args, "tcp")
		assert.Equal(t, "rtsps://camera.example.com/live", args[len(args)-1])
		assert.Contains(t, args, "-v")
		assert.Contains(t, args, "quiet")
		assert.Contains(t, args, "-print_format")
		assert.Contains(t, args, "json")
	})

	t.Run("HTTP URL does not include rtsp transport", func(t *testing.T) {
		t.Parallel()

		args := buildProbeArgs("http://example.com/stream.m3u8")

		assert.NotContains(t, args, "-rtsp_transport")
		assert.NotContains(t, args, "-allowed_media_types")
		assert.Equal(t, "http://example.com/stream.m3u8", args[len(args)-1])
		assert.Contains(t, args, "-v")
		assert.Contains(t, args, "quiet")
		assert.Contains(t, args, "-print_format")
		assert.Contains(t, args, "json")
	})
}
