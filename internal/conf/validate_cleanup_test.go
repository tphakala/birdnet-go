package conf

import (
	"os/exec"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore/nativeenc"
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
// a format with no native encoder available is forced to WAV, but only while
// export is enabled, and a format that can be encoded natively is never
// downgraded. For AAC and Opus "natively" depends on the runtime gate, so those
// are covered separately in TestApplyFfmpegFormatFallback_NativeGate.
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

// An install without FFmpeg that opts into a native lossy encoder must keep the
// format it asked for. Getting this wrong is silent and total: validation
// rewrites the type before export ever runs, so the encoder is never consulted
// and the operator just sees WAV files, on precisely the FFmpeg-less systems the
// native encoders exist to serve. Every developer machine has FFmpeg, so nothing
// else in the suite would catch a regression here.
func TestApplyFfmpegFormatFallback_NativeGate(t *testing.T) {
	// Not parallel: t.Setenv.
	tests := []struct {
		name     string
		envVar   string
		envValue string
		input    string
		want     string
	}{
		{
			name:  "aac without the gate falls back to WAV",
			input: AudioExportTypeAAC, want: AudioExportTypeWAV,
		},
		{
			name: "aac with the gate keeps aac", envVar: nativeenc.EnvAACEncoder, envValue: "native",
			input: AudioExportTypeAAC, want: AudioExportTypeAAC,
		},
		{
			name:  "opus without the gate falls back to WAV",
			input: AudioExportTypeOPUS, want: AudioExportTypeWAV,
		},
		{
			name: "opus with the gate keeps opus", envVar: nativeenc.EnvOpusEncoder, envValue: "native",
			input: AudioExportTypeOPUS, want: AudioExportTypeOPUS,
		},
		{
			// The gates are per codec: the AAC gate must not rescue Opus.
			name: "the aac gate does not keep opus", envVar: nativeenc.EnvAACEncoder, envValue: "native",
			input: AudioExportTypeOPUS, want: AudioExportTypeWAV,
		},
		{
			// MP3 has no native encoder, so it is downgraded whatever is set.
			name: "mp3 falls back even with both gates set", envVar: nativeenc.EnvAACEncoder, envValue: "native",
			input: AudioExportTypeMP3, want: AudioExportTypeWAV,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Clear both gates, then set only the one under test, so a leaked
			// value from the environment cannot make a case pass.
			t.Setenv(nativeenc.EnvAACEncoder, "")
			t.Setenv(nativeenc.EnvOpusEncoder, "")
			if tt.envVar != "" {
				t.Setenv(tt.envVar, tt.envValue)
			}

			settings := &AudioSettings{
				FfmpegPath: "", // FFmpeg unavailable
				Export:     ExportSettings{Enabled: true, Type: tt.input},
			}
			settings.applyFfmpegFormatFallback()

			assert.Equal(t, tt.want, settings.Export.Type)
		})
	}
}
