// Package myaudio provides audio processing utilities including FFmpeg integration.
package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// Clip extraction timeout bounds. The actual timeout scales with requested
// duration (2x) but is clamped to these limits.
const (
	clipExtractionMinTimeout = 30 * time.Second
	clipExtractionMaxTimeout = 10 * time.Minute
)

// MaxClipDurationSec is the maximum allowed clip duration in seconds.
// Prevents memory exhaustion from very long extraction requests.
const MaxClipDurationSec = 300 // 5 minutes

// clipDefaultBitrates defines the default bitrates for lossy clip extraction formats.
// These are independent of the audio export bitrate setting — clips are short previews
// so lower bitrates keep file sizes small while preserving sufficient quality.
var clipDefaultBitrates = map[string]string{
	FormatMP3:  "128k",
	FormatOpus: "64k",
	FormatAAC:  "96k",
}

// supportedClipFormats lists the formats supported by ExtractAudioClip.
var supportedClipFormats = map[string]bool{
	"wav":      true,
	FormatMP3:  true,
	FormatFLAC: true,
	FormatOpus: true,
	FormatAAC:  true,
	FormatALAC: true,
}

// requiresSeekableOutput lists formats whose muxers cannot write to a pipe.
// MP4-based containers (AAC → mp4, ALAC → ipod) need seekable output to
// write the moov atom, so we route them through a temporary file.
var requiresSeekableOutput = map[string]bool{
	FormatAAC:  true,
	FormatALAC: true,
}

// IsSupportedClipFormat returns true if the format is supported for clip extraction.
func IsSupportedClipFormat(format string) bool {
	return supportedClipFormats[format]
}

// ExtractAudioClip extracts a time range from an audio file and re-encodes it
// to the specified format. The result is returned as an in-memory buffer.
func ExtractAudioClip(ctx context.Context, inputPath string, start, end float64, format string, settings *conf.AudioSettings, filters *AudioFilters) (*bytes.Buffer, error) {
	if settings == nil {
		return nil, fmt.Errorf("audio settings cannot be nil")
	}

	// Validate parameters
	if start < 0 {
		return nil, fmt.Errorf("start time must be non-negative, got %f", start)
	}
	if end <= start {
		return nil, fmt.Errorf("end time (%f) must be greater than start time (%f)", end, start)
	}
	if end-start > MaxClipDurationSec {
		return nil, fmt.Errorf("clip duration (%.1fs) exceeds maximum (%ds)", end-start, MaxClipDurationSec)
	}
	if !supportedClipFormats[format] {
		return nil, fmt.Errorf("unsupported clip format: %q", format)
	}

	// Validate FFmpeg path
	if err := ValidateFFmpegPath(settings.FfmpegPath); err != nil {
		return nil, fmt.Errorf("invalid FFmpeg path: %w", err)
	}

	// Calculate duration
	duration := end - start

	// Create context with adaptive timeout (2x duration, clamped to bounds)
	// Placed before analysis so both loudness analysis and extraction are governed
	timeout := max(time.Duration(duration*2)*time.Second, clipExtractionMinTimeout)
	timeout = min(timeout, clipExtractionMaxTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Handle normalize two-pass if filters include normalization
	if filters != nil && filters.Normalize && filters.LoudnessStats == nil {
		seekRange := &SeekRange{Start: start, Duration: duration}
		stats, err := AnalyzeFileLoudness(ctx, inputPath, settings.FfmpegPath,
			AudioFilters{Denoise: filters.Denoise, Normalize: true}, seekRange)
		if err != nil {
			return nil, fmt.Errorf("loudness analysis for clip failed: %w", err)
		}
		filters.LoudnessStats = stats
	}

	// MP4-based formats (AAC, ALAC) require seekable output — use a temp file
	if requiresSeekableOutput[format] {
		return extractClipViaTempFile(ctx, settings.FfmpegPath, inputPath, start, duration, format, filters)
	}

	return extractClipViaPipe(ctx, settings.FfmpegPath, inputPath, start, duration, format, filters)
}

// extractClipViaPipe runs FFmpeg with output piped to stdout.
func extractClipViaPipe(ctx context.Context, ffmpegPath, inputPath string, start, duration float64, format string, filters *AudioFilters) (*bytes.Buffer, error) {
	args := buildClipFFmpegArgs(inputPath, start, duration, format, "pipe:1", filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath is from validated settings, args built internally

	var stdout bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("clip extraction timed out or cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("FFmpeg clip extraction failed: %w, stderr: %s", err, stderr.String())
	}

	if stdout.Len() == 0 {
		return nil, fmt.Errorf("FFmpeg produced empty output for %s (start=%.2f, duration=%.2f)", filepath.Base(inputPath), start, duration)
	}

	return &stdout, nil
}

// extractClipViaTempFile writes FFmpeg output to a temporary file, then reads
// it into memory. Required for MP4-based muxers that need seekable output.
func extractClipViaTempFile(ctx context.Context, ffmpegPath, inputPath string, start, duration float64, format string, filters *AudioFilters) (*bytes.Buffer, error) {
	ext := GetFileExtension(format)
	tmpFile, err := os.CreateTemp("", "birdnet-clip-*."+ext)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for clip extraction: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	args := buildClipFFmpegArgs(inputPath, start, duration, format, tmpPath, filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath is from validated settings, args built internally

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("clip extraction timed out or cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("FFmpeg clip extraction failed: %w, stderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(tmpPath)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp clip file: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("FFmpeg produced empty output for %s (start=%.2f, duration=%.2f)", filepath.Base(inputPath), start, duration)
	}

	return bytes.NewBuffer(data), nil
}

// buildClipFFmpegArgs constructs the FFmpeg command arguments for clip extraction.
// Uses -ss before -i for fast input seeking and -t for duration (not -to, which
// has inconsistent behavior across FFmpeg versions when combined with input seeking).
// Always re-encodes to ensure frame-accurate cuts (no -c copy).
// The outputTarget is either "pipe:1" for stdout or a file path.
func buildClipFFmpegArgs(inputPath string, start, duration float64, format, outputTarget string, filters *AudioFilters) []string {
	// WAV is not in the standard export format constants since it's a raw container,
	// so we handle it explicitly here rather than modifying the shared helpers.
	var outputEncoder, outputFormat string
	if format == "wav" {
		outputEncoder = "pcm_s16le"
		outputFormat = "wav"
	} else {
		outputEncoder = getEncoder(format)
		outputFormat = getOutputFormat(format)
	}

	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-ss", fmt.Sprintf("%.6f", start),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.6f", duration),
		"-c:a", outputEncoder,
	}

	// Add bitrate for lossy formats using clip-specific defaults
	// (independent of the audio export bitrate setting)
	if bitrate, ok := clipDefaultBitrates[format]; ok {
		args = append(args, "-b:a", bitrate)
	}

	// Add processing filters if provided
	if filters != nil {
		filterChain := BuildProcessingFilterChain(*filters)
		if filterChain != "" {
			args = append(args, "-af", filterChain)
		}
	}

	args = append(args,
		"-f", outputFormat,
		"-y", // overwrite temp file without prompting
		outputTarget,
	)

	return args
}
