// Package ffmpeg provides utilities for working with the FFmpeg binary,
// including path validation, audio format helpers, and duration queries.
package ffmpeg

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// rePathContamination matches URL-like path segments that indicate the FFmpeg
// path has been contaminated by a reverse proxy or ingress prefix (e.g.,
// "/api/", "/ingress/", "/proxy/", "/hassio/"). A valid FFmpeg binary path
// should never contain these segments.
var rePathContamination = regexp.MustCompile(`(?i)/(?:api|ingress|proxy|hassio)/`)

// ValidateFFmpegPath checks if the FFmpeg path is valid for execution.
// It rejects empty paths, relative paths, and paths that appear to be
// HTTP/proxy URL prefixes rather than filesystem paths (e.g., ingress path contamination).
func ValidateFFmpegPath(ffmpegPath string) error {
	if ffmpegPath == "" {
		return errors.Newf("FFmpeg is not available").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_ffmpeg_path").
			Build()
	}

	if !filepath.IsAbs(ffmpegPath) {
		return errors.Newf("FFmpeg path must be absolute, got: %s", ffmpegPath).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_ffmpeg_path").
			Context("path", ffmpegPath).
			Build()
	}

	// Reject paths contaminated by HTTP proxy/ingress prefixes.
	// A valid FFmpeg binary path should be a clean filesystem path,
	// not contain URL-like segments such as "/api/", "/ingress/", "/proxy/",
	// or "/hassio/". Uses a case-insensitive regex for broader detection.
	if rePathContamination.MatchString(ffmpegPath) {
		return errors.Newf("FFmpeg path appears contaminated by proxy/ingress prefix: %s", ffmpegPath).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_ffmpeg_path").
			Context("path", ffmpegPath).
			Build()
	}

	return nil
}

// GetFFmpegFormat converts audio configuration values to FFmpeg-compatible format strings.
// Returns sampleRateStr (e.g. "48000"), channelsStr (e.g. "1"), and formatStr (e.g. "s16le").
// Supported bit depths: 16 → "s16le", 24 → "s24le", 32 → "s32le". Defaults to "s16le".
func GetFFmpegFormat(sampleRate, numChannels, bitDepth int) (sampleRateStr, channelsStr, formatStr string) {
	sampleRateStr = strconv.Itoa(sampleRate)
	channelsStr = strconv.Itoa(numChannels)

	switch bitDepth {
	case 16:
		formatStr = "s16le"
	case 24:
		formatStr = "s24le"
	case 32:
		formatStr = "s32le"
	default:
		formatStr = "s16le"
	}

	return
}

// getSoxBinaryName returns the platform-appropriate sox binary name.
func getSoxBinaryName() string {
	if runtime.GOOS == "windows" {
		return "sox.exe"
	}

	return "sox"
}

// GetAudioDuration uses sox --info -D to get the duration of an audio file in seconds.
// This is approximately 30x faster than ffprobe for duration queries.
// Returns the duration in seconds as a float64, or an error if sox fails.
// The context allows for cancellation and timeout to prevent hanging.
// If ctx is nil, a 5-second timeout is applied automatically.
func GetAudioDuration(ctx context.Context, audioPath string) (float64, error) {
	if audioPath == "" {
		return 0, errors.Newf("audio path cannot be empty").
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "get_audio_duration").
			Build()
	}

	// Track the actual timeout/deadline for accurate error messages.
	var timeoutDuration time.Duration

	if ctx == nil {
		var cancel context.CancelFunc
		timeoutDuration = 5 * time.Second
		ctx, cancel = context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
	} else if deadline, ok := ctx.Deadline(); ok {
		timeoutDuration = time.Until(deadline)
	}

	soxBinary := getSoxBinaryName()

	// --info -D: output only duration in seconds.
	cmd := exec.CommandContext(ctx, soxBinary, "--info", "-D", audioPath) //nolint:gosec // G204: soxBinary is a fixed platform constant, audioPath validated by caller

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			if errors.Is(ctx.Err(), context.DeadlineExceeded) {
				if timeoutDuration > 0 {
					return 0, errors.Newf("sox --info timed out after %v for file %s: %w", timeoutDuration, audioPath, ctx.Err()).
						Component("audiocore").
						Category(errors.CategoryAudio).
						Context("operation", "get_audio_duration").
						Context("path", audioPath).
						Build()
				}

				return 0, errors.Newf("sox --info timed out for file %s: %w", audioPath, ctx.Err()).
					Component("audiocore").
					Category(errors.CategoryAudio).
					Context("operation", "get_audio_duration").
					Context("path", audioPath).
					Build()
			}

			return 0, errors.Newf("sox --info canceled: %w", ctx.Err()).
				Component("audiocore").
				Category(errors.CategoryAudio).
				Context("operation", "get_audio_duration").
				Context("path", audioPath).
				Build()
		}

		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}

		return 0, errors.Newf("sox --info failed: %s", errMsg).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "get_audio_duration").
			Context("path", audioPath).
			Build()
	}

	durationStr := strings.TrimSpace(out.String())
	if durationStr == "" {
		return 0, errors.Newf("sox --info could not determine duration for file: %s", audioPath).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "get_audio_duration").
			Context("path", audioPath).
			Build()
	}

	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return 0, errors.Newf("failed to parse duration '%s': %w", durationStr, err).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "get_audio_duration").
			Context("path", audioPath).
			Build()
	}

	if duration <= 0 {
		return 0, errors.Newf("invalid duration %f for file: %s", duration, audioPath).
			Component("audiocore").
			Category(errors.CategoryAudio).
			Context("operation", "get_audio_duration").
			Context("path", audioPath).
			Build()
	}

	return duration, nil
}
