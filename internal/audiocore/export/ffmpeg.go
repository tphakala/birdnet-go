package export

import (
	"bytes"
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// FFmpegExporter implements audio export using FFmpeg for advanced formats
type FFmpegExporter struct {
	format     Format
	ffmpegPath string
}

// NewFFmpegExporter creates a new FFmpeg-based exporter
func NewFFmpegExporter(format Format) *FFmpegExporter {
	return &FFmpegExporter{
		format: format,
	}
}

// ExportToFile exports audio data to a file using FFmpeg
func (f *FFmpegExporter) ExportToFile(ctx context.Context, audioData *audiocore.AudioData, config *Config) (string, error) {
	if err := f.ValidateConfig(config); err != nil {
		return "", err
	}

	// Set FFmpeg path from config
	f.ffmpegPath = config.FFmpegPath

	// Generate file path
	fileName := GenerateFileName(config.FileNameTemplate, audioData.SourceID, audioData.Timestamp, config.Format)
	filePath := filepath.Join(config.OutputPath, fileName)

	// Ensure output directory exists
	if err := os.MkdirAll(config.OutputPath, 0o755); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "create_export_directory").
			Context("path", config.OutputPath).
			Build()
	}

	// Create temporary file for atomic write
	tempPath := filePath + ".tmp"

	// Build FFmpeg command
	args := f.buildFFmpegArgs(audioData.Format, config, tempPath)

	// Create context with timeout
	exportCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// Create FFmpeg command
	cmd := exec.CommandContext(exportCtx, f.ffmpegPath, args...)

	// Create stdin pipe for PCM data
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "create_ffmpeg_stdin").
			Build()
	}

	// Capture stderr for error reporting
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start FFmpeg
	if err := cmd.Start(); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "start_ffmpeg").
			Context("stderr", stderr.String()).
			Build()
	}

	// Write PCM data to FFmpeg stdin
	writeErr := make(chan error, 1)
	go func() {
		defer func() {
			_ = stdin.Close()
		}()
		_, err := stdin.Write(audioData.Buffer)
		writeErr <- err
	}()

	// Wait for write to complete
	select {
	case err := <-writeErr:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return "", errors.New(err).
				Component("audiocore").
				Category(errors.CategorySystem).
				Context("operation", "write_pcm_to_ffmpeg").
				Build()
		}
	case <-exportCtx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return "", errors.New(exportCtx.Err()).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "ffmpeg_export_timeout").
			Build()
	}

	// Wait for FFmpeg to complete
	if err := cmd.Wait(); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "ffmpeg_export_failed").
			Context("stderr", stderr.String()).
			Build()
	}

	// Atomic rename
	if err := os.Rename(tempPath, filePath); err != nil {
		return "", errors.New(err).
			Component("audiocore").
			Category(errors.CategoryFileIO).
			Context("operation", "rename_export_file").
			Context("from", tempPath).
			Context("to", filePath).
			Build()
	}

	return filePath, nil
}

// ExportToWriter exports audio data to an io.Writer using FFmpeg
func (f *FFmpegExporter) ExportToWriter(ctx context.Context, audioData *audiocore.AudioData, writer io.Writer, config *Config) error {
	if err := f.ValidateConfig(config); err != nil {
		return err
	}

	// Set FFmpeg path from config
	f.ffmpegPath = config.FFmpegPath

	// Build FFmpeg command for pipe output
	args := f.buildFFmpegArgsForPipe(audioData.Format, config)

	// Create context with timeout
	exportCtx, cancel := context.WithTimeout(ctx, config.Timeout)
	defer cancel()

	// Create FFmpeg command
	cmd := exec.CommandContext(exportCtx, f.ffmpegPath, args...)

	// Create pipes
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "create_ffmpeg_stdin").
			Build()
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "create_ffmpeg_stdout").
			Build()
	}

	// Capture stderr
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start FFmpeg
	if err := cmd.Start(); err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "start_ffmpeg").
			Build()
	}

	// Write PCM data
	writeErr := make(chan error, 1)
	go func() {
		defer func() {
			_ = stdin.Close()
		}()
		_, err := stdin.Write(audioData.Buffer)
		writeErr <- err
	}()

	// Read output data
	readErr := make(chan error, 1)
	go func() {
		_, err := io.Copy(writer, stdout)
		readErr <- err
	}()

	// Wait for completion
	select {
	case err := <-writeErr:
		if err != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return errors.New(err).
				Component("audiocore").
				Category(errors.CategorySystem).
				Context("operation", "write_pcm_to_ffmpeg").
				Build()
		}
	case <-exportCtx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return errors.New(exportCtx.Err()).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "ffmpeg_export_timeout").
			Build()
	}

	// Wait for read to complete
	if err := <-readErr; err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "read_ffmpeg_output").
			Build()
	}

	// Wait for FFmpeg to complete
	if err := cmd.Wait(); err != nil {
		return errors.New(err).
			Component("audiocore").
			Category(errors.CategorySystem).
			Context("operation", "ffmpeg_export_failed").
			Context("stderr", stderr.String()).
			Build()
	}

	return nil
}

// ExportClip exports a specific time range as a clip
func (f *FFmpegExporter) ExportClip(ctx context.Context, audioData *audiocore.AudioData, startTime, endTime time.Time, config *Config) (string, error) {
	// For FFmpeg export, we assume the audioData already contains the correct clip
	return f.ExportToFile(ctx, audioData, config)
}

// ValidateConfig validates the export configuration
func (f *FFmpegExporter) ValidateConfig(config *Config) error {
	if config == nil {
		return errors.Newf("export config is nil").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Build()
	}

	if config.Format != f.format {
		return errors.Newf("FFmpeg exporter format mismatch: expected %s, got %s", f.format, config.Format).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("expected_format", string(f.format)).
			Context("config_format", string(config.Format)).
			Build()
	}

	return ValidateConfig(config)
}

// SupportedFormats returns the formats supported by this exporter
func (f *FFmpegExporter) SupportedFormats() []Format {
	return []Format{f.format}
}

// buildFFmpegArgs builds FFmpeg command arguments for file output
func (f *FFmpegExporter) buildFFmpegArgs(audioFormat audiocore.AudioFormat, config *Config, outputPath string) []string {
	// Determine input format based on bit depth
	inputFormat := f.getFFmpegInputFormat(audioFormat.BitDepth)

	args := []string{
		"-f", inputFormat, // Input format
		"-ar", strconv.Itoa(audioFormat.SampleRate), // Sample rate
		"-ac", strconv.Itoa(audioFormat.Channels), // Number of channels
		"-i", "-", // Read from stdin
	}

	// Add codec and format-specific options
	codec := GetFFmpegCodec(config.Format)
	args = append(args, "-c:a", codec)

	// Add bitrate for lossy formats
	if IsLossyFormat(config.Format) && config.Bitrate != "" {
		args = append(args, "-b:a", config.Bitrate)
	}

	// Add format-specific options
	args = append(args, f.getFormatSpecificArgs(config.Format)...)

	// Output format and file
	args = append(args,
		"-f", GetFFmpegFormat(config.Format),
		"-y", // Overwrite output file
		outputPath,
	)

	return args
}

// buildFFmpegArgsForPipe builds FFmpeg command arguments for pipe output
func (f *FFmpegExporter) buildFFmpegArgsForPipe(audioFormat audiocore.AudioFormat, config *Config) []string {
	args := f.buildFFmpegArgs(audioFormat, config, "pipe:1")
	// Replace the output path with pipe
	args[len(args)-1] = "pipe:1"
	return args
}

// getFFmpegInputFormat returns the FFmpeg input format based on bit depth
func (f *FFmpegExporter) getFFmpegInputFormat(bitDepth int) string {
	switch bitDepth {
	case 16:
		return "s16le"
	case 24:
		return "s24le"
	case 32:
		return "s32le"
	default:
		return "s16le" // Default to 16-bit
	}
}

// getFormatSpecificArgs returns format-specific FFmpeg arguments
func (f *FFmpegExporter) getFormatSpecificArgs(format Format) []string {
	switch format {
	case FormatOpus:
		// Opus has a maximum bitrate of 256k
		return []string{"-strict", "-2"} // May be needed for some FFmpeg versions
	case FormatAAC:
		// AAC encoding options
		return []string{"-movflags", "+faststart"} // For better streaming
	case FormatWAV, FormatFLAC, FormatMP3:
		// These formats don't need special arguments
		return nil
	default:
		return nil
	}
}

// validateBitrateForFormat validates and adjusts bitrate for format limits
func (f *FFmpegExporter) validateBitrateForFormat(format Format, bitrate string) string {
	if !IsLossyFormat(format) || bitrate == "" {
		return bitrate
	}

	// Parse bitrate value
	numStr := strings.TrimSuffix(bitrate, "k")
	rate, err := strconv.Atoi(numStr)
	if err != nil {
		return bitrate // Return as-is if we can't parse
	}

	// Apply format-specific limits
	switch format {
	case FormatOpus:
		if rate > 256 {
			return "256k"
		}
	case FormatMP3:
		if rate > 320 {
			return "320k"
		}
	case FormatAAC:
		// AAC typically supports up to 320k
		if rate > 320 {
			return "320k"
		}
	case FormatWAV, FormatFLAC:
		// Lossless formats don't use bitrate
		return ""
	default:
		// Unknown format, return as-is
		return bitrate
	}

	return bitrate
}
