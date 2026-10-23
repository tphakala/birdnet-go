// Package ffmpeg provides utilities for working with the FFmpeg binary,
// including path validation and audio format helpers.
package ffmpeg

import (
	"path/filepath"
	"strconv"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// osWindows is runtime.GOOS on Windows, where the ffprobe binary carries a .exe
// suffix (see getFfprobeBinaryName).
const osWindows = "windows"

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

	// Redact the path in errors/telemetry: a contaminated tool path can embed an
	// ingress credential token and these errors reach Sentry and support dumps.
	safeFFmpegPath := conf.RedactToolPath(ffmpegPath)

	if !filepath.IsAbs(ffmpegPath) {
		return errors.Newf("FFmpeg path must be absolute, got: %s", safeFFmpegPath).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_ffmpeg_path").
			Context("path", safeFFmpegPath).
			Build()
	}

	// Reject paths contaminated by HTTP proxy/ingress prefixes.
	// A valid FFmpeg binary path should be a clean filesystem path,
	// not contain URL-like segments such as "/api/", "/ingress/", "/proxy/",
	// or "/hassio/". Shares the canonical check with config-time validation.
	if conf.IsContaminatedToolPath(ffmpegPath) {
		return errors.Newf("FFmpeg path appears contaminated by proxy/ingress prefix: %s", safeFFmpegPath).
			Component("audiocore").
			Category(errors.CategoryValidation).
			Context("operation", "validate_ffmpeg_path").
			Context("path", safeFFmpegPath).
			Build()
	}

	return nil
}

// pcmSampleBytes returns the size in bytes of one PCM sample in the output
// format GetFFmpegFormat selects for the given bit depth: 16 maps to s16le,
// 24 to s24le, 32 to s32le, and anything else falls back to s16le.
//
// This is the byte-size twin of GetFFmpegFormat's format switch. The two are
// kept in agreement by TestPCMSampleBytesMatchesFFmpegFormat, which derives the
// expected byte size from the format string itself, so adding a bit depth to
// one without the other fails the build's tests rather than silently making
// stream readers frame on the wrong unit.
func pcmSampleBytes(bitDepth int) int {
	switch bitDepth {
	case 16, 24, 32:
		return bitDepth / bitsPerByte
	default:
		return defaultPCMSampleBytes
	}
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
