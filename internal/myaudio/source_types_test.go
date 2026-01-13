// source_types_test.go - Unit tests for source types and type conversion
package myaudio

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestSourceTypeConstants verifies all expected source type constants are defined
func TestSourceTypeConstants(t *testing.T) {
	tests := []struct {
		name     string
		constant SourceType
		expected string
	}{
		{"RTSP type", SourceTypeRTSP, "rtsp"},
		{"HTTP type", SourceTypeHTTP, "http"},
		{"HLS type", SourceTypeHLS, "hls"},
		{"RTMP type", SourceTypeRTMP, "rtmp"},
		{"UDP type", SourceTypeUDP, "udp"},
		{"AudioCard type", SourceTypeAudioCard, "audio_card"},
		{"File type", SourceTypeFile, "file"},
		{"Unknown type", SourceTypeUnknown, "unknown"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			assert.Equal(t, tt.expected, string(tt.constant))
		})
	}
}

// TestStreamTypeToSourceType tests the conversion from config stream type strings to SourceType
func TestStreamTypeToSourceType(t *testing.T) {
	tests := []struct {
		name       string
		streamType string
		expected   SourceType
	}{
		// Valid conversions
		{"rtsp to RTSP", "rtsp", SourceTypeRTSP},
		{"http to HTTP", "http", SourceTypeHTTP},
		{"hls to HLS", "hls", SourceTypeHLS},
		{"rtmp to RTMP", "rtmp", SourceTypeRTMP},
		{"udp to UDP", "udp", SourceTypeUDP},

		// Edge cases - should return Unknown
		{"empty string", "", SourceTypeUnknown},
		{"uppercase RTSP", "RTSP", SourceTypeUnknown},
		{"mixed case Rtsp", "Rtsp", SourceTypeUnknown},
		{"invalid type", "invalid", SourceTypeUnknown},
		{"audio_card (not valid stream type)", "audio_card", SourceTypeUnknown},
		{"file (not valid stream type)", "file", SourceTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := StreamTypeToSourceType(tt.streamType)
			assert.Equal(t, tt.expected, result, "StreamTypeToSourceType(%q)", tt.streamType)
		})
	}
}

// TestDetectSourceTypeFromString tests auto-detection of source type from connection strings
func TestDetectSourceTypeFromString(t *testing.T) {
	tests := []struct {
		name             string
		connectionString string
		expected         SourceType
	}{
		// RTSP streams
		{"RTSP basic", "rtsp://192.168.1.100/stream", SourceTypeRTSP},
		{"RTSP with credentials", "rtsp://user:pass@192.168.1.100/stream", SourceTypeRTSP},
		{"RTSP with port", "rtsp://192.168.1.100:554/stream", SourceTypeRTSP},
		{"RTSPS secure", "rtsps://192.168.1.100/stream", SourceTypeRTSP},
		{"Test URL (treated as RTSP)", "test://example/stream", SourceTypeRTSP},

		// RTMP streams
		{"RTMP basic", "rtmp://live.example.com/app/stream", SourceTypeRTMP},
		{"RTMPS secure", "rtmps://live.example.com/app/stream", SourceTypeRTMP},
		{"RTMP with credentials", "rtmp://user:pass@live.example.com/app/stream", SourceTypeRTMP},

		// HLS streams (m3u8)
		{"HLS m3u8 HTTP", "http://cdn.example.com/playlist.m3u8", SourceTypeHLS},
		{"HLS m3u8 HTTPS", "https://cdn.example.com/playlist.m3u8", SourceTypeHLS},
		{"HLS with query params", "https://cdn.example.com/playlist.m3u8?token=abc", SourceTypeHLS},
		{"HLS with query and ampersand", "https://cdn.example.com/playlist.m3u8?token=abc&expires=123", SourceTypeHLS},

		// HTTP streams (non-HLS)
		{"HTTP audio stream", "http://stream.example.com/live", SourceTypeHTTP},
		{"HTTPS audio stream", "https://stream.example.com/live", SourceTypeHTTP},
		{"HTTP with path", "http://example.com/audio/stream.mp3", SourceTypeHTTP},

		// UDP/RTP streams
		{"UDP multicast", "udp://239.0.0.1:1234", SourceTypeUDP},
		{"UDP with params", "udp://@239.0.0.1:1234?pkt_size=1316", SourceTypeUDP},
		{"RTP stream", "rtp://239.0.0.1:5004", SourceTypeUDP},

		// Audio devices
		{"ALSA hw device", "hw:1,0", SourceTypeAudioCard},
		{"ALSA plughw device", "plughw:1,0", SourceTypeAudioCard},
		{"PulseAudio", "pulse:input", SourceTypeAudioCard},
		{"ALSA default", "sysdefault:CARD=Device", SourceTypeAudioCard},
		{"ALSA dsnoop", "dsnoop:1,0", SourceTypeAudioCard},

		// Audio files
		{"WAV file", "/path/to/audio.wav", SourceTypeFile},
		{"MP3 file", "/path/to/audio.mp3", SourceTypeFile},
		{"FLAC file", "/path/to/audio.flac", SourceTypeFile},
		{"M4A file", "/path/to/audio.m4a", SourceTypeFile},
		{"OGG file", "/path/to/audio.ogg", SourceTypeFile},

		// Unknown/ambiguous
		{"Empty string", "", SourceTypeUnknown},
		{"Plain text", "some random string", SourceTypeUnknown},
		{"FTP (unsupported)", "ftp://server/file", SourceTypeUnknown},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := detectSourceTypeFromString(tt.connectionString)
			assert.Equal(t, tt.expected, result, "detectSourceTypeFromString(%q)", tt.connectionString)
		})
	}
}

// TestAudioSourceGetConnectionString tests the GetConnectionString method
func TestAudioSourceGetConnectionString(t *testing.T) {
	t.Run("returns connection string when set", func(t *testing.T) {
		source := &AudioSource{
			ID:               "test_001",
			DisplayName:      "Test Source",
			Type:             SourceTypeRTSP,
			connectionString: "rtsp://192.168.1.100/stream",
			SafeString:       "rtsp://192.168.1.100/stream",
		}

		connStr, err := source.GetConnectionString()
		require.NoError(t, err)
		assert.Equal(t, "rtsp://192.168.1.100/stream", connStr)
	})

	t.Run("returns error when connection string is empty", func(t *testing.T) {
		source := &AudioSource{
			ID:               "test_002",
			DisplayName:      "Empty Source",
			Type:             SourceTypeRTSP,
			connectionString: "",
			SafeString:       "",
		}

		connStr, err := source.GetConnectionString()
		assert.Error(t, err)
		assert.Empty(t, connStr)
		assert.Contains(t, err.Error(), "connection string is empty")
	})

	t.Run("preserves credentials in connection string", func(t *testing.T) {
		source := &AudioSource{
			ID:               "test_003",
			DisplayName:      "Credential Source",
			Type:             SourceTypeRTSP,
			connectionString: "rtsp://admin:secret123@192.168.1.100/stream",
			SafeString:       "rtsp://192.168.1.100/stream", // Sanitized
		}

		connStr, err := source.GetConnectionString()
		require.NoError(t, err)
		assert.Contains(t, connStr, "admin:secret123", "Connection string should preserve credentials")
	})
}

// TestAudioSourceStringInterface tests the Stringer interface implementation
func TestAudioSourceStringInterface(t *testing.T) {
	t.Run("String returns SafeString", func(t *testing.T) {
		source := &AudioSource{
			ID:               "test_001",
			DisplayName:      "Test Source",
			Type:             SourceTypeRTSP,
			connectionString: "rtsp://admin:secret@192.168.1.100/stream",
			SafeString:       "rtsp://192.168.1.100/stream",
		}

		// Stringer interface should return SafeString (sanitized)
		str := source.String()
		assert.Equal(t, "rtsp://192.168.1.100/stream", str)
		assert.NotContains(t, str, "secret", "String() should not expose credentials")
	})

	t.Run("Empty SafeString returns empty", func(t *testing.T) {
		source := &AudioSource{
			ID:         "test_002",
			SafeString: "",
		}

		assert.Empty(t, source.String())
	})
}

// TestAudioSourceFields tests that AudioSource fields are set correctly
func TestAudioSourceFields(t *testing.T) {
	now := time.Now()

	source := &AudioSource{
		ID:               "rtsp_001",
		DisplayName:      "Front Camera",
		Type:             SourceTypeRTSP,
		connectionString: "rtsp://192.168.1.100/stream",
		SafeString:       "rtsp://192.168.1.100/stream",
		RegisteredAt:     now,
		IsActive:         true,
		LastSeen:         now,
		TotalBytes:       1024,
		ErrorCount:       2,
	}

	assert.Equal(t, "rtsp_001", source.ID)
	assert.Equal(t, "Front Camera", source.DisplayName)
	assert.Equal(t, SourceTypeRTSP, source.Type)
	assert.True(t, source.IsActive)
	assert.Equal(t, int64(1024), source.TotalBytes)
	assert.Equal(t, 2, source.ErrorCount)
}

// TestSourceConfigUsage tests the SourceConfig struct
func TestSourceConfigUsage(t *testing.T) {
	config := SourceConfig{
		ID:          "custom_id",
		DisplayName: "Custom Display Name",
		Type:        SourceTypeHTTP,
	}

	assert.Equal(t, "custom_id", config.ID)
	assert.Equal(t, "Custom Display Name", config.DisplayName)
	assert.Equal(t, SourceTypeHTTP, config.Type)
}

// TestSourceTypeStringConversion verifies that SourceType can be converted to string
func TestSourceTypeStringConversion(t *testing.T) {
	tests := []SourceType{
		SourceTypeRTSP,
		SourceTypeHTTP,
		SourceTypeHLS,
		SourceTypeRTMP,
		SourceTypeUDP,
		SourceTypeAudioCard,
		SourceTypeFile,
		SourceTypeUnknown,
	}

	for _, sourceType := range tests {
		t.Run(string(sourceType), func(t *testing.T) {
			// Verify string conversion works
			str := string(sourceType)
			assert.NotEmpty(t, str, "SourceType should convert to non-empty string")

			// Verify round-trip (string -> SourceType)
			converted := SourceType(str)
			assert.Equal(t, sourceType, converted)
		})
	}
}
