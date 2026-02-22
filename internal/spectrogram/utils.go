// Package spectrogram provides spectrogram generation utilities.
// This file contains shared utilities used by both the pre-renderer and API endpoints.
package spectrogram

import (
	"context"
	"fmt"
	"maps"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Spectrogram size constants define pixel widths for different display contexts.
// Heights are computed as 2^n + 1 to ensure sox uses fast FFT (O(n log n)) instead of
// brute-force DFT (O(n²)). Sox DFT size = 2*(height-1), so height must be 2^n + 1
// for the DFT size to be a power of 2. Widths are 2× height to maintain ~2:1 aspect ratio.
const (
	// sizeMediumPx is the width for standard detail view (height=257, DFT=512)
	sizeMediumPx = 514

	// sizeLargePx is the width for large display / default render size (height=513, DFT=1024)
	sizeLargePx = 1026

	// sizeExtraLargePx is the width for maximum quality for expert review (height=1025, DFT=2048)
	sizeExtraLargePx = 2050

	// signalKilledMessage is the error message pattern for process termination
	signalKilledMessage = "signal: killed"

	// exitCodeSIGKILL is the exit code when a process is terminated by SIGKILL (128 + 9)
	exitCodeSIGKILL = 137

	// exitCodeSIGTERM is the exit code when a process is terminated by SIGTERM (128 + 15)
	exitCodeSIGTERM = 143
)

// validSizes maps size strings to pixel widths (single source of truth).
// All sizes use FFT-friendly dimensions (width = 2 × height, height = 2^n + 1):
// - md (514px): Standard detail view (DFT=512)
// - lg (1026px): Default render size for all contexts (DFT=1024)
// - xl (2050px): Maximum quality for expert review (DFT=2048)
var validSizes = map[string]int{
	"md": sizeMediumPx,     // Medium - 514px (height=257, DFT=512)
	"lg": sizeLargePx,      // Large - 1026px (height=513, DFT=1024)
	"xl": sizeExtraLargePx, // Extra Large - 2050px (height=1025, DFT=2048)
}

// SizeToPixels converts a size string to pixel width.
// Returns an error if the size string is not valid.
//
// Valid sizes: md (514px), lg (1026px), xl (2050px)
func SizeToPixels(size string) (int, error) {
	width, ok := validSizes[size]
	if !ok {
		return 0, errors.Newf("invalid size (valid sizes: md, lg, xl)").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "size_to_pixels").
			Context("size", size).
			Build()
	}
	return width, nil
}

// PixelsToSize converts a pixel width to a size string.
// Returns an error if the width doesn't match any valid size.
func PixelsToSize(width int) (string, error) {
	for size, w := range validSizes {
		if w == width {
			return size, nil
		}
	}
	return "", errors.Newf("invalid width: no matching size").
		Component("spectrogram").
		Category(errors.CategoryValidation).
		Context("operation", "pixels_to_size").
		Context("width", width).
		Build()
}

// GetValidSizes returns a sorted list of valid size strings.
// Useful for runtime validation in web UI.
// Returns sizes in deterministic order for consistent UI/testing.
func GetValidSizes() []string {
	sizes := slices.Collect(maps.Keys(validSizes))
	// Sort for deterministic output
	slices.Sort(sizes)
	return sizes
}

// BuildSpectrogramPath constructs the spectrogram file path from the audio clip path.
// It replaces the audio file extension with .png.
//
// Example:
//
//	"clips/2024-01-15/Bird_species/Bird_species.2024-01-15T10:00:00.wav"
//	-> "clips/2024-01-15/Bird_species/Bird_species.2024-01-15T10:00:00.png"
func BuildSpectrogramPath(clipPath string) (string, error) {
	ext := filepath.Ext(clipPath)
	if ext == "" {
		return "", errors.Newf("clip path has no extension").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "build_spectrogram_path").
			Context("clip_path", clipPath).
			Build()
	}

	spectrogramPath := strings.TrimSuffix(clipPath, ext) + ".png"
	return spectrogramPath, nil
}

// IsOperationalError checks if an error is an expected operational event rather than
// a genuine failure. Operational errors include context cancellation, deadline exceeded,
// and process kills (e.g. context-triggered SIGKILL/SIGTERM).
//
// The function checks for operational errors in this order:
// 1. Context errors (Canceled, DeadlineExceeded)
// 2. Process exit codes (SIGKILL=137, SIGTERM=143) - more reliable than string matching
// 3. String matching for "signal: killed" - fallback for compatibility
func IsOperationalError(err error) bool {
	if err == nil {
		return false
	}

	// Check standard context errors
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return true
	}

	// Check for process termination via exit codes (more reliable than string matching)
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		code := exitErr.ExitCode()
		if code == exitCodeSIGKILL || code == exitCodeSIGTERM {
			return true
		}
	}

	// Fallback to string matching for compatibility with other error types
	return strings.Contains(err.Error(), signalKilledMessage)
}

// BuildSpectrogramPathWithParams builds a spectrogram path with size/raw encoded in filename.
// Used by API when different sizes/raw settings are requested than the default.
//
// The filename format is: basename.{size}[.raw].png
// Examples:
//
//	"file.wav" with width=514, raw=false  -> "file.md.png"
//	"file.wav" with width=1026, raw=true  -> "file.lg.raw.png"
//	"file.wav" with width=2050, raw=false -> "file.xl.png"
func BuildSpectrogramPathWithParams(audioPath string, width int, raw bool) (string, error) {
	// Find the size string for this width
	sizeStr, err := PixelsToSize(width)
	if err != nil {
		return "", err
	}

	// Build filename with parameters
	ext := filepath.Ext(audioPath)
	if ext == "" {
		return "", errors.Newf("audio path has no extension").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "build_spectrogram_path_with_params").
			Context("audio_path", audioPath).
			Build()
	}
	baseName := strings.TrimSuffix(audioPath, ext)

	suffix := fmt.Sprintf(".%s", sizeStr)
	if raw {
		suffix += ".raw"
	}
	suffix += ".png"

	return baseName + suffix, nil
}
