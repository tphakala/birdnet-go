package export

import (
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// DefaultConfig returns a default export configuration
func DefaultConfig() *Config {
	return &Config{
		Format:           FormatWAV,
		OutputPath:       "clips/",
		FileNameTemplate: "{source}_{timestamp}",
		Bitrate:          "128k",
		Timeout:          30 * time.Second,
	}
}

// ValidateConfig validates an export configuration
func ValidateConfig(config *Config) error {
	if config == nil {
		return errors.Newf("export config is nil").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate format
	if !IsValidFormat(config.Format) {
		return errors.Newf("invalid export format: %s", config.Format).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("format", string(config.Format)).
			Build()
	}

	// Validate output path
	if config.OutputPath == "" {
		return errors.Newf("export output path is empty").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate file name template
	if config.FileNameTemplate == "" {
		return errors.Newf("export file name template is empty").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	// Validate bitrate for lossy formats
	if IsLossyFormat(config.Format) && config.Bitrate != "" {
		if !IsValidBitrate(config.Bitrate) {
			return errors.Newf("invalid bitrate: %s", config.Bitrate).
				Component("audiocore").
				Category(errors.CategoryValidation).
				Context("bitrate", config.Bitrate).
				Build()
		}
	}

	// Validate FFmpeg path for non-WAV formats
	if config.Format != FormatWAV && config.FFmpegPath == "" {
		return errors.Newf("FFmpeg path required for format: %s", config.Format).
			Component("audiocore").
			Category(errors.CategoryConfiguration).
			Context("format", string(config.Format)).
			Build()
	}

	// Validate timeout
	if config.Timeout <= 0 {
		return errors.Newf("invalid export timeout: %v", config.Timeout).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("timeout", config.Timeout.String()).
			Build()
	}

	return nil
}

// IsValidFormat checks if a format is valid
func IsValidFormat(format Format) bool {
	switch format {
	case FormatWAV, FormatMP3, FormatFLAC, FormatAAC, FormatOpus:
		return true
	default:
		return false
	}
}

// IsLossyFormat checks if a format is lossy
func IsLossyFormat(format Format) bool {
	switch format {
	case FormatMP3, FormatAAC, FormatOpus:
		return true
	default:
		return false
	}
}

// IsValidBitrate checks if a bitrate string is valid
func IsValidBitrate(bitrate string) bool {
	// Must end with 'k' and have a numeric value
	if !strings.HasSuffix(bitrate, "k") {
		return false
	}

	// Extract numeric part
	numStr := strings.TrimSuffix(bitrate, "k")
	if numStr == "" {
		return false
	}

	// Try to parse as integer
	var rate int
	if _, err := parseIntFromString(numStr); err != nil {
		return false
	}

	// Check valid range (32k to 320k)
	if rate < 32 || rate > 320 {
		return false
	}

	return true
}

// parseIntFromString is a helper to parse integer from string
func parseIntFromString(s string) (int, error) {
	var val int
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0, errors.Newf("invalid numeric string").
				Component("audiocore").
				Category(errors.CategoryValidation).
				Build()
		}
		val = val*10 + int(r-'0')
	}
	return val, nil
}

// GenerateFileName generates a file name based on the template
func GenerateFileName(template, sourceID string, timestamp time.Time, format Format) string {
	fileName := template

	// Replace placeholders
	fileName = strings.ReplaceAll(fileName, "{source}", sourceID)
	fileName = strings.ReplaceAll(fileName, "{date}", timestamp.Format("2006-01-02"))
	fileName = strings.ReplaceAll(fileName, "{time}", timestamp.Format("15-04-05"))
	fileName = strings.ReplaceAll(fileName, "{timestamp}", timestamp.Format("20060102_150405"))

	// Add extension
	fileName = fileName + "." + string(format)

	// Clean the path
	return filepath.Clean(fileName)
}

// GetFFmpegFormat returns the FFmpeg format name for a Format
func GetFFmpegFormat(format Format) string {
	switch format {
	case FormatMP3:
		return "mp3"
	case FormatFLAC:
		return "flac"
	case FormatAAC:
		return "mp4" // AAC typically uses MP4 container
	case FormatOpus:
		return "opus"
	default:
		return string(format)
	}
}

// GetFFmpegCodec returns the FFmpeg codec name for a Format
func GetFFmpegCodec(format Format) string {
	switch format {
	case FormatMP3:
		return "libmp3lame"
	case FormatFLAC:
		return "flac"
	case FormatAAC:
		return "aac"
	case FormatOpus:
		return "libopus"
	default:
		return string(format)
	}
}
