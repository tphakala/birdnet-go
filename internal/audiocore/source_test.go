package audiocore

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSourceType_String(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name     string
		st       SourceType
		expected string
	}{
		{"RTSP", SourceTypeRTSP, "rtsp"},
		{"HTTP", SourceTypeHTTP, "http"},
		{"HLS", SourceTypeHLS, "hls"},
		{"RTMP", SourceTypeRTMP, "rtmp"},
		{"UDP", SourceTypeUDP, "udp"},
		{"AudioCard", SourceTypeAudioCard, "audio_card"},
		{"File", SourceTypeFile, "file"},
		{"Unknown", SourceTypeUnknown, "unknown"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.st.String())
		})
	}
}

func TestSourceState_String(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "inactive", SourceInactive.String())
	assert.Equal(t, "running", SourceRunning.String())
	assert.Equal(t, "error", SourceError.String())
}

func TestAudioSource_SafeString(t *testing.T) {
	t.Parallel()
	source := &AudioSource{
		ID:          "rtsp_001",
		DisplayName: "Backyard cam",
		Type:        SourceTypeRTSP,
		SafeString:  "rtsp://***@192.168.1.10/stream",
	}
	assert.Equal(t, "rtsp://***@192.168.1.10/stream", source.String())
}

func TestStreamTypeToSourceType(t *testing.T) {
	t.Parallel()
	tests := []struct {
		input    string
		expected SourceType
	}{
		{"rtsp", SourceTypeRTSP},
		{"RTSP", SourceTypeRTSP},
		{"http", SourceTypeHTTP},
		{"hls", SourceTypeHLS},
		{"rtmp", SourceTypeRTMP},
		{"udp", SourceTypeUDP},
		{"unknown", SourceTypeUnknown},
		{"", SourceTypeUnknown},
	}
	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, StreamTypeToSourceType(tt.input))
		})
	}
}
