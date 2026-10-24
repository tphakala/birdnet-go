// Package spectrogram provides spectrogram generation utilities.
// This file contains shared utilities used by both the pre-renderer and API endpoints.
package spectrogram

import (
	"fmt"
	"maps"
	"path/filepath"
	"slices"
	"strings"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// Spectrogram size constants define pixel widths for different display contexts.
// Heights are computed as 2^n + 1 so the analysis frame, 2*(height-1), is a power of 2
// and the FFT takes its fast path (O(n log n)) instead of brute-force DFT (O(n²)).
// Widths are 2× height to maintain ~2:1 aspect ratio.
const (
	// sizeSmallPx is the width for compact display in lists and dashboards (height=129, DFT=256)
	sizeSmallPx = 258

	// sizeMediumPx is the width for standard detail view (height=257, DFT=512)
	sizeMediumPx = 514

	// sizeLargePx is the width for large display / default render size (height=513, DFT=1024)
	sizeLargePx = 1026

	// sizeExtraLargePx is the width for maximum quality for expert review (height=1025, DFT=2048)
	sizeExtraLargePx = 2050
)

// validSizes maps size strings to pixel widths (single source of truth).
// All sizes use FFT-friendly dimensions (width = 2 × height, height = 2^n + 1):
// - sm (258px): Compact display in lists and dashboards (DFT=256)
// - md (514px): Standard detail view (DFT=512)
// - lg (1026px): Default render size for all contexts (DFT=1024)
// - xl (2050px): Maximum quality for expert review (DFT=2048)
var validSizes = map[string]int{
	"sm": sizeSmallPx,      // Small - 258px (height=129, DFT=256)
	"md": sizeMediumPx,     // Medium - 514px (height=257, DFT=512)
	"lg": sizeLargePx,      // Large - 1026px (height=513, DFT=1024)
	"xl": sizeExtraLargePx, // Extra Large - 2050px (height=1025, DFT=2048)
}

// SizeToPixels converts a size string to pixel width.
// Returns an error if the size string is not valid.
//
// Valid sizes: sm (258px), md (514px), lg (1026px), xl (2050px)
func SizeToPixels(size string) (int, error) {
	width, ok := validSizes[size]
	if !ok {
		return 0, fmt.Errorf("invalid spectrogram size %q (valid sizes: sm, md, lg, xl)", size)
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
	return "", fmt.Errorf("invalid spectrogram width %d: no matching size", width)
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
		// Only the basename goes into the context. Callers pass a path joined
		// against the export directory, and this error is reported to Sentry,
		// where the directory prefix would leak the account name and directory
		// layout of the reporting machine. The diagnostic here is which file
		// lacks an extension, which the basename answers on its own.
		//
		// Keyed clip_basename rather than clip_name because clip_name is already
		// taken: elsewhere it carries the DB column value, which keeps its
		// year/month directories. Reusing it for a bare filename would make one
		// key mean two shapes.
		return "", errors.Newf("clip path has no extension").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "build_spectrogram_path").
			Context("clip_basename", filepath.Base(clipPath)).
			Build()
	}

	spectrogramPath := strings.TrimSuffix(clipPath, ext) + ".png"
	return spectrogramPath, nil
}

// IsOperationalError checks if an error is an expected operational event rather than
// a genuine failure, such as a context cancellation, an expired deadline, or a render
// interrupted by either.
//
// The function checks for operational errors in this order:
//  1. Context errors (Canceled, DeadlineExceeded) anywhere in the error chain
//  2. An operational classification already attached upstream: a CategorySystem
//     EnhancedError tagged PriorityLow (set by nativeError for context interruptions)
func IsOperationalError(err error) bool {
	if err == nil {
		return false
	}

	if errors.IsContextCancellation(err) {
		return true
	}

	// Honor an operational classification already attached upstream. nativeError
	// tags context-driven interruptions as a CategorySystem error with PriorityLow
	// using the governing context. IsSuppressibleOperationalError is the shared
	// definition the Sentry filter uses, so an unrelated PriorityLow error in
	// another category is not misread as operational and downstream consumers
	// (prerenderer, API media handlers) get the same answer.
	enhanced, ok := errors.AsType[*errors.EnhancedError](err)
	return ok && errors.IsSuppressibleOperationalError(enhanced)
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
			// Basename only, for the reason spelled out on the sibling above:
			// this error is reported to Sentry and the directory prefix leaks
			// the account name and layout of the reporting machine.
			Context("operation", "build_spectrogram_path_with_params").
			Context("clip_basename", filepath.Base(audioPath)).
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
