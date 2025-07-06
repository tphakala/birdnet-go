package export

import (
	"testing"
	"time"
)

func TestDefaultConfig(t *testing.T) {
	config := DefaultConfig()

	if config.Format != FormatWAV {
		t.Errorf("expected default format WAV, got %s", config.Format)
	}

	if config.OutputPath != "clips/" {
		t.Errorf("expected default output path 'clips/', got %s", config.OutputPath)
	}

	if config.Timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", config.Timeout)
	}
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name      string
		config    *Config
		wantError bool
		errorMsg  string
	}{
		{
			name:      "nil config",
			config:    nil,
			wantError: true,
			errorMsg:  "export config is nil",
		},
		{
			name: "invalid format",
			config: &Config{
				Format:           "invalid",
				OutputPath:       "clips/",
				FileNameTemplate: "test",
				Timeout:          30 * time.Second,
			},
			wantError: true,
			errorMsg:  "invalid export format",
		},
		{
			name: "empty output path",
			config: &Config{
				Format:           FormatWAV,
				OutputPath:       "",
				FileNameTemplate: "test",
				Timeout:          30 * time.Second,
			},
			wantError: true,
			errorMsg:  "export output path is empty",
		},
		{
			name: "empty file name template",
			config: &Config{
				Format:           FormatWAV,
				OutputPath:       "clips/",
				FileNameTemplate: "",
				Timeout:          30 * time.Second,
			},
			wantError: true,
			errorMsg:  "export file name template is empty",
		},
		{
			name: "invalid bitrate",
			config: &Config{
				Format:           FormatMP3,
				OutputPath:       "clips/",
				FileNameTemplate: "test",
				Bitrate:          "invalid",
				FFmpegPath:       "/usr/bin/ffmpeg",
				Timeout:          30 * time.Second,
			},
			wantError: true,
			errorMsg:  "invalid bitrate",
		},
		{
			name: "missing ffmpeg for non-WAV",
			config: &Config{
				Format:           FormatMP3,
				OutputPath:       "clips/",
				FileNameTemplate: "test",
				Bitrate:          "128k",
				FFmpegPath:       "",
				Timeout:          30 * time.Second,
			},
			wantError: true,
			errorMsg:  "FFmpeg path required",
		},
		{
			name: "invalid timeout",
			config: &Config{
				Format:           FormatWAV,
				OutputPath:       "clips/",
				FileNameTemplate: "test",
				Timeout:          0,
			},
			wantError: true,
			errorMsg:  "invalid export timeout",
		},
		{
			name: "valid WAV config",
			config: &Config{
				Format:           FormatWAV,
				OutputPath:       "clips/",
				FileNameTemplate: "{source}_{timestamp}",
				Timeout:          30 * time.Second,
			},
			wantError: false,
		},
		{
			name: "valid MP3 config",
			config: &Config{
				Format:           FormatMP3,
				OutputPath:       "clips/",
				FileNameTemplate: "{source}_{timestamp}",
				Bitrate:          "192k",
				FFmpegPath:       "/usr/bin/ffmpeg",
				Timeout:          30 * time.Second,
			},
			wantError: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := ValidateConfig(tt.config)
			if (err != nil) != tt.wantError {
				t.Errorf("ValidateConfig() error = %v, wantError %v", err, tt.wantError)
			}
			if err != nil && tt.errorMsg != "" {
				if !contains(err.Error(), tt.errorMsg) {
					t.Errorf("expected error containing '%s', got '%s'", tt.errorMsg, err.Error())
				}
			}
		})
	}
}

func TestIsValidFormat(t *testing.T) {
	tests := []struct {
		format Format
		valid  bool
	}{
		{FormatWAV, true},
		{FormatMP3, true},
		{FormatFLAC, true},
		{FormatAAC, true},
		{FormatOpus, true},
		{"invalid", false},
		{"", false},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			if got := IsValidFormat(tt.format); got != tt.valid {
				t.Errorf("IsValidFormat(%s) = %v, want %v", tt.format, got, tt.valid)
			}
		})
	}
}

func TestIsLossyFormat(t *testing.T) {
	tests := []struct {
		format Format
		lossy  bool
	}{
		{FormatWAV, false},
		{FormatFLAC, false},
		{FormatMP3, true},
		{FormatAAC, true},
		{FormatOpus, true},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			if got := IsLossyFormat(tt.format); got != tt.lossy {
				t.Errorf("IsLossyFormat(%s) = %v, want %v", tt.format, got, tt.lossy)
			}
		})
	}
}

func TestIsValidBitrate(t *testing.T) {
	tests := []struct {
		bitrate string
		valid   bool
	}{
		{"128k", true},
		{"192k", true},
		{"320k", true},
		{"32k", true},
		{"31k", false},     // Too low
		{"321k", false},    // Too high
		{"128", false},     // Missing 'k'
		{"k", false},       // Missing number
		{"128kb", false},   // Wrong suffix
		{"abc", false},     // Not a number
		{"", false},        // Empty
	}

	for _, tt := range tests {
		t.Run(tt.bitrate, func(t *testing.T) {
			if got := IsValidBitrate(tt.bitrate); got != tt.valid {
				t.Errorf("IsValidBitrate(%s) = %v, want %v", tt.bitrate, got, tt.valid)
			}
		})
	}
}

func TestGenerateFileName(t *testing.T) {
	timestamp := time.Date(2024, 1, 15, 14, 30, 45, 0, time.UTC)

	tests := []struct {
		name      string
		template  string
		sourceID  string
		timestamp time.Time
		format    Format
		want      string
	}{
		{
			name:      "all placeholders",
			template:  "{source}_{date}_{time}_{timestamp}",
			sourceID:  "mic1",
			timestamp: timestamp,
			format:    FormatWAV,
			want:      "mic1_2024-01-15_14-30-45_20240115_143045.wav",
		},
		{
			name:      "source only",
			template:  "{source}",
			sourceID:  "rtsp_cam",
			timestamp: timestamp,
			format:    FormatMP3,
			want:      "rtsp_cam.mp3",
		},
		{
			name:      "no placeholders",
			template:  "recording",
			sourceID:  "test",
			timestamp: timestamp,
			format:    FormatFLAC,
			want:      "recording.flac",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateFileName(tt.template, tt.sourceID, tt.timestamp, tt.format)
			if got != tt.want {
				t.Errorf("GenerateFileName() = %s, want %s", got, tt.want)
			}
		})
	}
}

func TestGetFFmpegFormat(t *testing.T) {
	tests := []struct {
		format Format
		want   string
	}{
		{FormatMP3, "mp3"},
		{FormatFLAC, "flac"},
		{FormatAAC, "mp4"},
		{FormatOpus, "opus"},
		{FormatWAV, "wav"},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			if got := GetFFmpegFormat(tt.format); got != tt.want {
				t.Errorf("GetFFmpegFormat(%s) = %s, want %s", tt.format, got, tt.want)
			}
		})
	}
}

func TestGetFFmpegCodec(t *testing.T) {
	tests := []struct {
		format Format
		want   string
	}{
		{FormatMP3, "libmp3lame"},
		{FormatFLAC, "flac"},
		{FormatAAC, "aac"},
		{FormatOpus, "libopus"},
		{FormatWAV, "wav"},
	}

	for _, tt := range tests {
		t.Run(string(tt.format), func(t *testing.T) {
			if got := GetFFmpegCodec(tt.format); got != tt.want {
				t.Errorf("GetFFmpegCodec(%s) = %s, want %s", tt.format, got, tt.want)
			}
		})
	}
}

// Helper function to check if a string contains a substring
func contains(s, substr string) bool {
	return substr != "" && len(s) >= len(substr) && s[:len(substr)] == substr || 
		len(s) > len(substr) && contains(s[1:], substr)
}