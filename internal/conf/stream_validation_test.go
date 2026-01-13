package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestStreamConfig_Validate(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		stream  StreamConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid RTSP stream",
			stream: StreamConfig{
				Name:      "Front Yard",
				URL:       "rtsp://192.168.1.10/stream",
				Type:      StreamTypeRTSP,
				Transport: "tcp",
			},
			wantErr: false,
		},
		{
			name: "valid RTSPS stream",
			stream: StreamConfig{
				Name:      "Secure Camera",
				URL:       "rtsps://192.168.1.10/stream",
				Type:      StreamTypeRTSP,
				Transport: "tcp",
			},
			wantErr: false,
		},
		{
			name: "valid HTTP stream",
			stream: StreamConfig{
				Name: "Icecast Feed",
				URL:  "http://192.168.1.50:8000/audio",
				Type: StreamTypeHTTP,
			},
			wantErr: false,
		},
		{
			name: "valid HLS stream",
			stream: StreamConfig{
				Name: "Webcam HLS",
				URL:  "https://camera.local/live/playlist.m3u8",
				Type: StreamTypeHLS,
			},
			wantErr: false,
		},
		{
			name: "valid RTMP stream",
			stream: StreamConfig{
				Name:      "OBS Stream",
				URL:       "rtmp://192.168.1.50/live/birdcam",
				Type:      StreamTypeRTMP,
				Transport: "tcp",
			},
			wantErr: false,
		},
		{
			name: "valid UDP stream",
			stream: StreamConfig{
				Name: "ESP32 Feed",
				URL:  "udp://192.168.1.5:1234",
				Type: StreamTypeUDP,
			},
			wantErr: false,
		},
		{
			name: "valid RTP stream",
			stream: StreamConfig{
				Name: "RTP Feed",
				URL:  "rtp://192.168.1.5:5004",
				Type: StreamTypeUDP,
			},
			wantErr: false,
		},
		{
			name: "missing name",
			stream: StreamConfig{
				Name: "",
				URL:  "rtsp://192.168.1.10/stream",
				Type: StreamTypeRTSP,
			},
			wantErr: true,
			errMsg:  "stream name is required",
		},
		{
			name: "whitespace-only name",
			stream: StreamConfig{
				Name: "   ",
				URL:  "rtsp://192.168.1.10/stream",
				Type: StreamTypeRTSP,
			},
			wantErr: true,
			errMsg:  "stream name is required",
		},
		{
			name: "name too long",
			stream: StreamConfig{
				Name: "This is a very long stream name that exceeds the maximum allowed length of 64 characters",
				URL:  "rtsp://192.168.1.10/stream",
				Type: StreamTypeRTSP,
			},
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
		{
			name: "missing URL",
			stream: StreamConfig{
				Name: "Test Stream",
				URL:  "",
				Type: StreamTypeRTSP,
			},
			wantErr: true,
			errMsg:  "stream URL is required",
		},
		{
			name: "invalid stream type",
			stream: StreamConfig{
				Name: "Test Stream",
				URL:  "rtsp://192.168.1.10/stream",
				Type: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid stream type",
		},
		{
			name: "invalid transport",
			stream: StreamConfig{
				Name:      "Test Stream",
				URL:       "rtsp://192.168.1.10/stream",
				Type:      StreamTypeRTSP,
				Transport: "invalid",
			},
			wantErr: true,
			errMsg:  "invalid transport",
		},
		{
			name: "URL scheme mismatch - RTSP type with HTTP URL",
			stream: StreamConfig{
				Name: "Test Stream",
				URL:  "http://192.168.1.10/stream",
				Type: StreamTypeRTSP,
			},
			wantErr: true,
			errMsg:  "RTSP type requires rtsp://",
		},
		{
			name: "URL scheme mismatch - HTTP type with RTSP URL",
			stream: StreamConfig{
				Name: "Test Stream",
				URL:  "rtsp://192.168.1.10/stream",
				Type: StreamTypeHTTP,
			},
			wantErr: true,
			errMsg:  "HTTP type requires http://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.stream.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func TestRTSPSettings_ValidateStreams(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		rtsp    RTSPSettings
		wantErr bool
		errMsg  string
	}{
		{
			name: "valid streams",
			rtsp: RTSPSettings{
				Streams: []StreamConfig{
					{Name: "Front Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP, Transport: "tcp"},
					{Name: "Back Yard", URL: "rtsp://192.168.1.20/stream", Type: StreamTypeRTSP, Transport: "tcp"},
				},
			},
			wantErr: false,
		},
		{
			name: "empty streams list",
			rtsp: RTSPSettings{
				Streams: []StreamConfig{},
			},
			wantErr: false,
		},
		{
			name: "duplicate names",
			rtsp: RTSPSettings{
				Streams: []StreamConfig{
					{Name: "Front Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
					{Name: "Front Yard", URL: "rtsp://192.168.1.20/stream", Type: StreamTypeRTSP},
				},
			},
			wantErr: true,
			errMsg:  "duplicate stream name",
		},
		{
			name: "duplicate names case insensitive",
			rtsp: RTSPSettings{
				Streams: []StreamConfig{
					{Name: "Front Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
					{Name: "FRONT YARD", URL: "rtsp://192.168.1.20/stream", Type: StreamTypeRTSP},
				},
			},
			wantErr: true,
			errMsg:  "duplicate stream name",
		},
		{
			name: "duplicate URLs",
			rtsp: RTSPSettings{
				Streams: []StreamConfig{
					{Name: "Front Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
					{Name: "Back Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
				},
			},
			wantErr: true,
			errMsg:  "has a duplicate URL",
		},
		{
			name: "invalid stream in list",
			rtsp: RTSPSettings{
				Streams: []StreamConfig{
					{Name: "Front Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
					{Name: "", URL: "rtsp://192.168.1.20/stream", Type: StreamTypeRTSP}, // Missing name
				},
			},
			wantErr: true,
			errMsg:  "stream name is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.rtsp.ValidateStreams()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Additional edge case tests for stream validation
func TestStreamConfig_Validate_EdgeCases(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name    string
		stream  StreamConfig
		wantErr bool
		errMsg  string
	}{
		{
			name: "name at exactly 64 characters",
			stream: StreamConfig{
				Name: "This is exactly sixty-four characters long name for testing now!", // 64 chars
				URL:  "rtsp://192.168.1.10/stream",
				Type: StreamTypeRTSP,
			},
			wantErr: false,
		},
		{
			name: "name at 65 characters",
			stream: StreamConfig{
				Name: "This is exactly sixty-five characters long name for testing now!!", // 65 chars
				URL:  "rtsp://192.168.1.10/stream",
				Type: StreamTypeRTSP,
			},
			wantErr: true,
			errMsg:  "exceeds maximum length",
		},
		{
			name: "empty transport defaults to valid",
			stream: StreamConfig{
				Name:      "Test Stream",
				URL:       "rtsp://192.168.1.10/stream",
				Type:      StreamTypeRTSP,
				Transport: "",
			},
			wantErr: false,
		},
		{
			name: "valid udp transport",
			stream: StreamConfig{
				Name:      "Test Stream",
				URL:       "rtsp://192.168.1.10/stream",
				Type:      StreamTypeRTSP,
				Transport: "udp",
			},
			wantErr: false,
		},
		{
			name: "HLS with https URL",
			stream: StreamConfig{
				Name: "HLS Stream",
				URL:  "https://example.com/playlist.m3u8",
				Type: StreamTypeHLS,
			},
			wantErr: false,
		},
		{
			name: "RTMP with rtmps URL",
			stream: StreamConfig{
				Name: "Secure RTMP",
				URL:  "rtmps://example.com/live/stream",
				Type: StreamTypeRTMP,
			},
			wantErr: false,
		},
		{
			name: "RTMP type with HTTP URL - mismatch",
			stream: StreamConfig{
				Name: "Mismatched RTMP",
				URL:  "http://example.com/stream",
				Type: StreamTypeRTMP,
			},
			wantErr: true,
			errMsg:  "RTMP type requires rtmp://",
		},
		{
			name: "UDP type with HTTP URL - mismatch",
			stream: StreamConfig{
				Name: "Mismatched UDP",
				URL:  "http://192.168.1.5:1234",
				Type: StreamTypeUDP,
			},
			wantErr: true,
			errMsg:  "UDP type requires udp://",
		},
		{
			name: "HLS type with RTSP URL - mismatch",
			stream: StreamConfig{
				Name: "Mismatched HLS",
				URL:  "rtsp://example.com/stream",
				Type: StreamTypeHLS,
			},
			wantErr: true,
			errMsg:  "HLS type requires http://",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			err := tt.stream.Validate()
			if tt.wantErr {
				require.Error(t, err)
				assert.Contains(t, err.Error(), tt.errMsg)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

// Benchmark for stream validation
func BenchmarkStreamConfig_Validate(b *testing.B) {
	stream := &StreamConfig{
		Name:      "Front Yard",
		URL:       "rtsp://192.168.1.10/stream",
		Type:      StreamTypeRTSP,
		Transport: "tcp",
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = stream.Validate()
	}
}

func BenchmarkRTSPSettings_ValidateStreams(b *testing.B) {
	rtsp := &RTSPSettings{
		Streams: []StreamConfig{
			{Name: "Front Yard", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP, Transport: "tcp"},
			{Name: "Back Yard", URL: "rtsp://192.168.1.20/stream", Type: StreamTypeRTSP, Transport: "tcp"},
			{Name: "Side Yard", URL: "rtsp://192.168.1.30/stream", Type: StreamTypeRTSP, Transport: "tcp"},
		},
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = rtsp.ValidateStreams()
	}
}
