// Package export provides audio export functionality for the audiocore system.
// It supports multiple audio formats including WAV (native Go) and advanced
// formats through FFmpeg integration.
package export

import (
	"context"
	"io"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
)

// Format represents the audio export format
type Format string

const (
	// FormatWAV represents WAV audio format (native Go implementation)
	FormatWAV Format = "wav"
	// FormatMP3 represents MP3 audio format (requires FFmpeg)
	FormatMP3 Format = "mp3"
	// FormatFLAC represents FLAC audio format (requires FFmpeg)
	FormatFLAC Format = "flac"
	// FormatAAC represents AAC audio format (requires FFmpeg)
	FormatAAC Format = "aac"
	// FormatOpus represents Opus audio format (requires FFmpeg)
	FormatOpus Format = "opus"
)

// Config contains configuration for audio export
type Config struct {
	// Format specifies the output audio format
	Format Format

	// OutputPath is the directory where exported files will be saved
	OutputPath string

	// FileNameTemplate is a template for generating file names
	// Supports: {source}, {date}, {time}, {timestamp}
	FileNameTemplate string

	// Bitrate for lossy formats (e.g., "128k", "192k")
	// Only used for MP3, AAC, Opus
	Bitrate string

	// FFmpegPath is the path to the FFmpeg executable
	// Required for non-WAV formats
	FFmpegPath string

	// EnableDebug enables debug logging
	EnableDebug bool

	// Timeout for export operations
	Timeout time.Duration
}

// Exporter defines the interface for audio export functionality
type Exporter interface {
	// ExportToFile exports audio data to a file
	ExportToFile(ctx context.Context, audioData *audiocore.AudioData, config *Config) (string, error)

	// ExportToWriter exports audio data to an io.Writer
	ExportToWriter(ctx context.Context, audioData *audiocore.AudioData, writer io.Writer, config *Config) error

	// ExportClip exports a clip with specific timing
	ExportClip(ctx context.Context, audioData *audiocore.AudioData, startTime, endTime time.Time, config *Config) (string, error)

	// ValidateConfig checks if the export configuration is valid
	ValidateConfig(config *Config) error

	// SupportedFormats returns the list of supported export formats
	SupportedFormats() []Format
}

// Factory creates exporters for different formats
type Factory interface {
	// CreateExporter creates an exporter for the specified format
	CreateExporter(format Format) (Exporter, error)

	// IsFormatSupported checks if a format is supported
	IsFormatSupported(format Format) bool
}

// Metadata contains metadata for exported audio files
type Metadata struct {
	// SourceID identifies the audio source
	SourceID string

	// Timestamp when the audio was captured
	Timestamp time.Time

	// Duration of the audio clip
	Duration time.Duration

	// Format of the exported audio
	Format Format

	// FilePath where the audio was exported
	FilePath string

	// FileSize in bytes
	FileSize int64

	// Additional metadata (e.g., detection info)
	Extra map[string]interface{}
}

// ExportResult contains the result of an export operation
type ExportResult struct {
	// Success indicates if the export was successful
	Success bool

	// FilePath is the path to the exported file
	FilePath string

	// Metadata contains information about the export
	Metadata *Metadata

	// Error if the export failed
	Error error

	// Duration is how long the export took
	Duration time.Duration
}

// Result is an alias for ExportResult for backward compatibility
type Result = ExportResult
