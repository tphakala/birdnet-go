package myaudio

import (
	"bytes"
	"context"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// validateFFmpegPath checks if FFmpeg is available
func validateFFmpegPath(ffmpegPath string) error {
	if ffmpegPath == "" {
		return fmt.Errorf("FFmpeg is not available")
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

// GetAudioDuration uses ffprobe to get the duration of an audio file in seconds.
// This supports all formats that ffprobe can handle (AAC, MP3, M4A, OGG, FLAC, WAV, etc.)
// Returns the duration in seconds as a float64, or an error if ffprobe fails.
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

	// Get the proper ffprobe binary name based on OS
	ffprobeBinary := conf.GetFfprobeBinaryName()

	// Build ffprobe command with context for cancellation support
	// -v error: suppress all output except errors
	// -show_entries format=duration: only show duration from format section
	// -of default=noprint_wrappers=1:nokey=1: output just the value, no formatting
	cmd := exec.CommandContext(ctx, ffprobeBinary,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath)

	var out bytes.Buffer
	var stderr bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &stderr

	// Execute ffprobe with context support
	if err := cmd.Run(); err != nil {
		// Check if context was canceled or timed out
		if ctx.Err() != nil {
			if ctx.Err() == context.DeadlineExceeded {
				// Use the actual timeout duration in the error message
				if timeoutDuration > 0 {
					return 0, fmt.Errorf("ffprobe timed out after %v for file %s: %w", timeoutDuration, audioPath, ctx.Err())
				}
				return 0, fmt.Errorf("ffprobe timed out for file %s: %w", audioPath, ctx.Err())
			}
			return 0, fmt.Errorf("ffprobe canceled: %w", ctx.Err())
		}
		// Include stderr in error message for debugging
		errMsg := stderr.String()
		if errMsg == "" {
			errMsg = err.Error()
		}
		return 0, fmt.Errorf("ffprobe failed: %s", errMsg)
	}

	// Parse the duration output
	durationStr := strings.TrimSpace(out.String())
	if durationStr == "" || durationStr == "N/A" {
		return 0, fmt.Errorf("ffprobe could not determine duration for file: %s", audioPath)
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
