package conf

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Tests for issue #493: validation code quality improvements.

// Item 1: validateBirdNETSettings no longer takes unused *Settings param.
// Covered implicitly by compilation: the call site in ValidateSettings
// passes only one argument. No runtime test needed beyond the existing
// TestValidateSoundLevelSettings suite exercising the full path.

// Item 2 + A: StreamConfig.Validate now trims Name and URL in-place.
func TestStreamConfigValidate_TrimsFieldsInPlace(t *testing.T) {
	t.Parallel()

	s := &StreamConfig{
		Name:      "  my-stream  ",
		URL:       "  rtsp://example.com/stream  ",
		Type:      StreamTypeRTSP,
		Transport: "tcp",
	}
	err := s.Validate()
	require.NoError(t, err)
	assert.Equal(t, "my-stream", s.Name, "Name should be trimmed in-place")
	assert.Equal(t, "rtsp://example.com/stream", s.URL, "URL should be trimmed in-place")
}

// Item 3: Nil guards on exported pure validators.
func TestNilGuards(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		validate func() ValidationResult
	}{
		{"ValidateBirdNETSettings", func() ValidationResult { return ValidateBirdNETSettings(nil) }},
		{"ValidateBirdweatherSettings", func() ValidationResult { return ValidateBirdweatherSettings(nil) }},
		{"ValidateMQTTSettings", func() ValidationResult { return ValidateMQTTSettings(nil) }},
		{"ValidateWebServerSettings", func() ValidationResult { return ValidateWebServerSettings(nil) }},
		{"ValidateTelemetrySettings", func() ValidationResult { return ValidateTelemetrySettings(nil) }},
		{"ValidateWebhookProvider", func() ValidationResult { return ValidateWebhookProvider(nil) }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := tt.validate()
			assert.False(t, result.Valid, "nil input should fail validation")
			require.NotEmpty(t, result.Errors, "nil input should produce an error")
			assert.Contains(t, result.Errors[0], "nil")
		})
	}
}

// Item 4: Whitespace-only MQTT broker/topic must be rejected.
func TestValidateMQTTSettings_WhitespaceOnlyBrokerAndTopic(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		broker      string
		topic       string
		errContains string
	}{
		{
			name:        "whitespace-only broker",
			broker:      "   ",
			topic:       "birds/detections",
			errContains: "broker URL is required",
		},
		{
			name:        "whitespace-only topic",
			broker:      "tcp://localhost:1883",
			topic:       "   ",
			errContains: "topic is required",
		},
		{
			name:        "both whitespace-only",
			broker:      "  ",
			topic:       "\t",
			errContains: "broker URL is required",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			settings := &MQTTSettings{
				Enabled: true,
				Broker:  tt.broker,
				Topic:   tt.topic,
			}
			result := ValidateMQTTSettings(settings)
			assert.False(t, result.Valid, "whitespace-only values should fail")
			require.NotEmpty(t, result.Errors)
			assert.Contains(t, result.Errors[0], tt.errContains)
		})
	}
}

// Item 5: clearFfmpegMetadata resets all FFmpeg-related fields.
func TestClearFfmpegMetadata(t *testing.T) {
	t.Parallel()

	settings := &AudioSettings{
		FfmpegPath:    "/usr/bin/ffmpeg",
		FfmpegVersion: "6.1.2",
		FfmpegMajor:   6,
		FfmpegMinor:   1,
	}
	settings.clearFfmpegMetadata()

	assert.Empty(t, settings.FfmpegPath)
	assert.Empty(t, settings.FfmpegVersion)
	assert.Zero(t, settings.FfmpegMajor)
	assert.Zero(t, settings.FfmpegMinor)
}

// Integration check: validateAudioSettings clears metadata when FFmpeg is absent.
func TestValidateAudioSettings_ClearsFFmpegMetadataOnFailure(t *testing.T) {
	t.Parallel()
	if _, err := exec.LookPath(GetFfmpegBinaryName()); err == nil {
		t.Skip("ffmpeg found on PATH; integration test requires ffmpeg to be absent")
	}

	settings := &AudioSettings{
		FfmpegPath:    "/nonexistent/ffmpeg",
		FfmpegVersion: "6.1.2",
		FfmpegMajor:   6,
		FfmpegMinor:   1,
		Export: ExportSettings{
			Enabled: false,
			Type:    AudioExportTypeWAV,
		},
	}
	_ = validateAudioSettings(settings)

	assert.Empty(t, settings.FfmpegPath)
	assert.Empty(t, settings.FfmpegVersion)
	assert.Zero(t, settings.FfmpegMajor)
	assert.Zero(t, settings.FfmpegMinor)
}

// TestApplyFfmpegFormatFallback covers the FFmpeg-missing export-format fallback:
// the lossy FFmpeg-only formats (MP3/AAC/Opus) are forced to WAV, but only while
// export is enabled, and the native formats (WAV, FLAC) are never downgraded.
func TestApplyFfmpegFormatFallback(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		enabled bool
		input   string
		want    string
	}{
		// Disabled export is never rewritten. MP3 (FFmpeg-only) proves the disabled
		// guard is what keeps the value, not the native WAV/FLAC exemption.
		{"disabled MP3 kept", false, AudioExportTypeMP3, AudioExportTypeMP3},
		// Enabled + FFmpeg-only format + no FFmpeg -> forced to WAV.
		{"enabled MP3 forced to WAV", true, AudioExportTypeMP3, AudioExportTypeWAV},
		// Native formats are exempt even when enabled and FFmpeg is missing: WAV is
		// PCM and FLAC is encoded by the native go-flac encoder.
		{"enabled WAV kept", true, AudioExportTypeWAV, AudioExportTypeWAV},
		{"enabled FLAC kept (native, no FFmpeg)", true, AudioExportTypeFLAC, AudioExportTypeFLAC},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := &AudioSettings{
				FfmpegPath: "", // FFmpeg unavailable
				Export:     ExportSettings{Enabled: tt.enabled, Type: tt.input},
			}
			settings.applyFfmpegFormatFallback()

			assert.Equal(t, tt.want, settings.Export.Type)
		})
	}
}
