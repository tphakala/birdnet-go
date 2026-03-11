package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// validateFFmpegPath checks if the FFmpeg path is valid for execution.
// It rejects empty paths and paths that appear to be HTTP/proxy URL prefixes
// rather than filesystem paths (e.g., ingress path contamination).
func validateFFmpegPath(ffmpegPath string) error {
	if ffmpegPath == "" {
		return fmt.Errorf("FFmpeg is not available")
	}
	// Reject paths contaminated by HTTP proxy/ingress prefixes.
	// A valid FFmpeg binary path should be a clean filesystem path,
	// not contain URL-like segments such as "/api/" or "/ingress/".
	if !filepath.IsAbs(ffmpegPath) {
		return fmt.Errorf("FFmpeg path must be absolute, got: %s", ffmpegPath)
	}
	if strings.Contains(ffmpegPath, "/api/") || strings.Contains(ffmpegPath, "/ingress/") {
		return fmt.Errorf("FFmpeg path appears contaminated by proxy/ingress prefix: %s", ffmpegPath)
	}
	return nil
}

// getFFmpegCompatibleValues returns FFmpeg-compatible values for sample rate, channels, and bit depth
func getFFmpegFormat(sampleRate, numChannels, bitDepth int) (sampleRateStr, channelsStr, formatStr string) {
	// FFmpeg supports most common sample rates, so we can use it as-is
	sampleRateStr = fmt.Sprintf("%d", sampleRate)

	// FFmpeg supports any number of channels, so we can use it as-is
	channelsStr = fmt.Sprintf("%d", numChannels)

	// Map bit depth to FFmpeg-compatible format
	switch bitDepth {
	case 16:
		formatStr = "s16le"
	case 24:
		formatStr = "s24le"
	case 32:
		formatStr = "s32le"
	default:
		formatStr = "s16le" // Default to 16-bit if unsupported
	}

	return
}

// GetAudioDuration uses sox --info -D to get the duration of an audio file in seconds.
// This is ~30x faster than ffprobe for duration queries.
// Returns the duration in seconds as a float64, or an error if sox fails.
// The context allows for cancellation and timeout to prevent hanging.
func GetAudioDuration(ctx context.Context, audioPath string) (float64, error) {
	// Validate input path
	if audioPath == "" {
		return 0, fmt.Errorf("audio path cannot be empty")
	}

	// Track the actual timeout/deadline for accurate error messages
	var timeoutDuration time.Duration

	// Create a context with timeout if none exists
	if ctx == nil {
		var cancel context.CancelFunc
		timeoutDuration = 5 * time.Second
		ctx, cancel = context.WithTimeout(context.Background(), timeoutDuration)
		defer cancel()
	} else if deadline, ok := ctx.Deadline(); ok {
		// Calculate remaining time if context has a deadline
		timeoutDuration = time.Until(deadline)
	}

	// Get the proper sox binary name based on OS
	soxBinary := conf.GetSoxBinaryName()

	// Build sox --info command with context for cancellation support
	// --info -D: output only duration in seconds
	cmd := exec.CommandContext(ctx, soxBinary, "--info", "-D", audioPath) //nolint:gosec // G204: soxBinary from conf.GetSoxBinaryName(), args are fixed

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	// Execute sox --info with context support
	if err := cmd.Run(); err != nil {
		// Check if context was canceled or timed out
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				// Use the actual timeout duration in the error message
				if timeoutDuration > 0 {
					return 0, fmt.Errorf("sox --info timed out after %v for file %s: %w", timeoutDuration, audioPath, ctx.Err())
				}
				return 0, fmt.Errorf("sox --info timed out for file %s: %w", audioPath, ctx.Err())
			}
			return 0, fmt.Errorf("sox --info canceled: %w", ctx.Err())
		}
		// Include stderr in error message for debugging
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return 0, fmt.Errorf("sox --info failed: %s", errMsg)
	}

	// Parse the duration output
	durationStr := strings.TrimSpace(out.String())
	if durationStr == "" {
		return 0, fmt.Errorf("sox --info could not determine duration for file: %s", audioPath)
	}

	duration, err := strconv.ParseFloat(durationStr, 64)
	if err != nil {
		return 0, fmt.Errorf("failed to parse duration '%s': %w", durationStr, err)
	}

	if duration <= 0 {
		return 0, fmt.Errorf("invalid duration %f for file: %s", duration, audioPath)
	}

	return duration, nil
}
