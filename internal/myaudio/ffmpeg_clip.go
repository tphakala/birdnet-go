// Package myaudio provides audio processing utilities including FFmpeg integration.
package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// clipExtractionTimeout is the maximum time allowed for a clip extraction.
const clipExtractionTimeout = 30 * time.Second

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

// IsSupportedClipFormat returns true if the format is supported for clip extraction.
func IsSupportedClipFormat(format string) bool {
	return supportedClipFormats[format]
}

// ExtractAudioClip extracts a time range from an audio file and re-encodes it
// to the specified format. The result is returned as an in-memory buffer.
func ExtractAudioClip(ctx context.Context, inputPath string, start, end float64, format string, settings *conf.AudioSettings) (*bytes.Buffer, error) {
	// Validate parameters
	if start < 0 {
		return nil, fmt.Errorf("start time must be non-negative, got %f", start)
	}
	if end <= start {
		return nil, fmt.Errorf("end time (%f) must be greater than start time (%f)", end, start)
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

	// Build FFmpeg arguments
	args := buildClipFFmpegArgs(inputPath, start, duration, format)

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, clipExtractionTimeout)
	defer cancel()

	// Execute FFmpeg
	cmd := exec.CommandContext(ctx, settings.FfmpegPath, args...) //nolint:gosec // G204: ffmpegPath is from validated settings, args built internally

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
		return nil, fmt.Errorf("FFmpeg produced empty output for %s (start=%.2f, end=%.2f)", filepath.Base(inputPath), start, end)
	}

	return &stdout, nil
}

// buildClipFFmpegArgs constructs the FFmpeg command arguments for clip extraction.
// Uses -ss before -i for fast input seeking and -t for duration (not -to, which
// has inconsistent behavior across FFmpeg versions when combined with input seeking).
// Always re-encodes to ensure frame-accurate cuts (no -c copy).
func buildClipFFmpegArgs(inputPath string, start, duration float64, format string) []string {
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

	args = append(args,
		"-f", outputFormat,
		"pipe:1",
	)

	return args
}
