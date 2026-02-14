// Package spectrogram provides spectrogram generation utilities.
// This file contains shared utilities used by both the pre-renderer and API endpoints.
package spectrogram

import (
	"context"
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Spectrogram size constants define pixel widths for different display contexts
const (
	// sizeSmallPx is the width for compact display in lists and dashboards (default)
	sizeSmallPx = 400

	// sizeMediumPx is the width for standard detail view and review modals
	sizeMediumPx = 800

	// sizeLargePx is the width for large display for detailed analysis
	sizeLargePx = 1000

	// sizeExtraLargePx is the width for maximum quality for expert review
	sizeExtraLargePx = 1200
)

// validSizes maps size strings to pixel widths (single source of truth).
// These sizes are optimized for different UI contexts:
// - sm (400px): Compact display in lists and dashboards (default)
// - md (800px): Standard detail view and review modals
// - lg (1000px): Large display for detailed analysis
// - xl (1200px): Maximum quality for expert review
var validSizes = map[string]int{
	"sm": sizeSmallPx,      // Small - 400px
	"md": sizeMediumPx,     // Medium - 800px
	"lg": sizeLargePx,      // Large - 1000px
	"xl": sizeExtraLargePx, // Extra Large - 1200px
}

// SizeToPixels converts a size string to pixel width.
// Returns an error if the size string is not valid.
//
// Valid sizes: sm (400px), md (800px), lg (1000px), xl (1200px)
func SizeToPixels(size string) (int, error) {
	width, ok := validSizes[size]
	if !ok {
		return 0, errors.Newf("invalid size (valid sizes: sm, md, lg, xl)").
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
// and process kills (e.g. context-triggered SIGKILL).
func IsOperationalError(err error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, context.Canceled) ||
		errors.Is(err, context.DeadlineExceeded) ||
		strings.Contains(err.Error(), "signal: killed")
}

// BuildSpectrogramPathWithParams builds a spectrogram path with size/raw encoded in filename.
// Used by API when different sizes/raw settings are requested than the default.
//
// The filename format is: basename.{size}[.raw].png
// Examples:
//
//	"file.wav" with width=400, raw=false  -> "file.sm.png"
//	"file.wav" with width=400, raw=true   -> "file.sm.raw.png"
//	"file.wav" with width=800, raw=false  -> "file.md.png"
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
