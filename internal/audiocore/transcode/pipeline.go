package transcode

import (
	"context"
	"math"
	"os"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/audioformat"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// processingTempDirName is the directory under the clip base that holds the
// request-scoped temporary files.
const processingTempDirName = ".tmp-processing"

// processingTempDirPerm is the mode of the temporary directory.
const processingTempDirPerm = 0o700

// ClipOptions describes a clip extraction.
type ClipOptions struct {
	// InputPath is the source audio file.
	InputPath string
	// Start and End bound the clip in seconds; 0 <= Start < End.
	Start, End float64
	// Format is the output format, one of the audioformat constants.
	Format string
	// Filters are the optional processing steps.
	Filters Filters
	// TempDir is the parent of the encoder's private temporary directory.
	TempDir string
}

// ClipResult is an extracted clip.
type ClipResult struct {
	// Data is the encoded file.
	Data []byte
	// Encoder is the encoder identity for logs (see EncoderTag).
	Encoder string
}

// ExtractClip decodes [Start, End) of the source, applies the filters and
// encodes the result. The timeout is twice the clip duration, clamped to
// [30 s, 10 min].
func ExtractClip(ctx context.Context, opts *ClipOptions) (*ClipResult, error) {
	if opts == nil {
		return nil, validationError(ErrInvalidRequest, "extract_clip", "options cannot be nil")
	}
	if err := validateClipOptions(opts); err != nil {
		return nil, err
	}
	duration := opts.End - opts.Start

	ctx, cancel := context.WithTimeout(ctx, clipTimeout(duration))
	defer cancel()

	pcm, err := Decode(ctx, opts.InputPath, &Window{Start: opts.Start, Duration: duration}, MaxDecodedPCMBytes)
	if err != nil {
		return nil, err
	}
	if err := ApplyFilters(ctx, pcm, opts.Filters); err != nil {
		return nil, err
	}
	data, err := Encode(ctx, pcm, opts.Format, opts.TempDir)
	if err != nil {
		return nil, err
	}
	return &ClipResult{Data: data, Encoder: EncoderTag(opts.Format)}, nil
}

// validateClipOptions checks the values ExtractClip depends on.
func validateClipOptions(opts *ClipOptions) error {
	switch {
	case math.IsNaN(opts.Start) || math.IsInf(opts.Start, 0) || opts.Start < 0:
		return validationError(ErrInvalidRequest, "extract_clip", "start time must be finite and non-negative, got %v", opts.Start)
	case math.IsNaN(opts.End) || math.IsInf(opts.End, 0) || opts.End <= opts.Start:
		return validationError(ErrInvalidRequest, "extract_clip", "end time (%v) must be finite and greater than start time (%v)", opts.End, opts.Start)
	case opts.End-opts.Start > MaxClipDurationSec:
		return validationError(ErrInvalidRequest, "extract_clip", "clip duration (%.1fs) exceeds maximum (%ds)", opts.End-opts.Start, MaxClipDurationSec)
	case !IsSupportedClipFormat(opts.Format):
		return validationError(ErrInvalidRequest, "extract_clip", "unsupported clip format %q", opts.Format)
	case !IsValidDenoisePreset(opts.Filters.Denoise):
		return validationError(ErrInvalidRequest, "extract_clip", "unknown denoise preset %q", opts.Filters.Denoise)
	case !IsValidGainDB(opts.Filters.GainDB):
		return validationError(ErrInvalidRequest, "extract_clip", "gain %v dB is out of range", opts.Filters.GainDB)
	}
	return nil
}

// clipTimeout scales with the requested duration and is clamped to the bounds.
func clipTimeout(durationSec float64) time.Duration {
	timeout := time.Duration(durationSec*clipTimeoutFactor) * time.Second
	return min(max(timeout, clipMinTimeout), clipMaxTimeout)
}

// ProcessToWAV decodes the whole source, applies the filters and returns a 16-bit
// WAV at the source sample rate. The timeout is 60 s.
func ProcessToWAV(ctx context.Context, inputPath string, f Filters) ([]byte, error) {
	if err := validateFilters(f); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, processingTimeout)
	defer cancel()

	pcm, err := Decode(ctx, inputPath, nil, MaxDecodedPCMBytes)
	if err != nil {
		return nil, err
	}
	if err := ApplyFilters(ctx, pcm, f); err != nil {
		return nil, err
	}
	return Encode(ctx, pcm, audioformat.WAV, "")
}

// AudibleBats decodes the whole source, time-expands it by factor to 48 kHz, then
// applies normalization and gain (f.Denoise must be empty) and returns a 16-bit
// WAV. The timeout is 120 s for the whole pipeline.
func AudibleBats(ctx context.Context, inputPath string, factor int, f Filters) ([]byte, error) {
	if f.Denoise != "" {
		return nil, validationError(ErrInvalidRequest, "audible_bats", "denoise is not offered for audible bats")
	}
	if err := validateFilters(f); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, audibleBatsTimeout)
	defer cancel()

	pcm, err := Decode(ctx, inputPath, nil, MaxDecodedPCMBytes)
	if err != nil {
		return nil, err
	}
	expanded, err := TimeExpand(ctx, pcm, factor, MaxDecodedPCMBytes)
	if err != nil {
		return nil, err
	}
	if err := ApplyFilters(ctx, expanded, f); err != nil {
		return nil, err
	}
	return Encode(ctx, expanded, audioformat.WAV, "")
}

// validateFilters checks the filter values.
func validateFilters(f Filters) error {
	switch {
	case !IsValidDenoisePreset(f.Denoise):
		return validationError(ErrInvalidRequest, "validate_filters", "unknown denoise preset %q", f.Denoise)
	case !IsValidGainDB(f.GainDB):
		return validationError(ErrInvalidRequest, "validate_filters", "gain %v dB is out of range", f.GainDB)
	}
	return nil
}

// ProcessingTempDir returns the request-scoped temporary directory under base,
// creating it when missing.
func ProcessingTempDir(base string) (string, error) {
	dir := filepath.Join(base, processingTempDirName)
	if err := os.MkdirAll(dir, processingTempDirPerm); err != nil {
		return "", errors.New(err).
			Component(componentName).
			Category(errors.CategoryFileIO).
			Context("operation", "processing_temp_dir").
			Build()
	}
	return dir, nil
}
