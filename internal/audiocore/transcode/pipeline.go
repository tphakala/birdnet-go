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

// job describes one decode, optional time expansion, filter and encode run.
type job struct {
	path    string
	win     *window
	timeout time.Duration
	// minRate rejects a source below this sample rate before it is decoded (0 off).
	minRate int
	// expansion time-expands the decoded audio by this factor (0 off).
	expansion int
	filters   Filters
	format    string
	tempDir   string
}

// run is the shared pipeline: decode, time-expand, filter, encode, all under the
// job's timeout.
func run(ctx context.Context, j *job) ([]byte, error) {
	ctx, cancel := context.WithTimeout(ctx, j.timeout)
	defer cancel()

	pcm, err := decodeAtLeast(ctx, j.path, j.win, maxDecodedPCMBytes, j.minRate)
	if err != nil {
		return nil, err
	}
	if j.expansion > 0 {
		if pcm, err = timeExpand(ctx, pcm, j.expansion, maxDecodedPCMBytes); err != nil {
			return nil, err
		}
	}
	if err := applyFilters(ctx, pcm, j.filters); err != nil {
		return nil, err
	}
	return encode(ctx, pcm, j.format, j.tempDir)
}

// ExtractClip decodes [Start, End) of the source, applies the filters and returns
// the clip encoded in opts.Format. The timeout is twice the clip duration, clamped
// to [30 s, 10 min].
func ExtractClip(ctx context.Context, opts *ClipOptions) ([]byte, error) {
	if opts == nil {
		return nil, validationError(ErrInvalidRequest, "extract_clip", "options cannot be nil")
	}
	if err := validateClipOptions(opts); err != nil {
		return nil, err
	}
	duration := opts.End - opts.Start
	return run(ctx, &job{
		path:    opts.InputPath,
		win:     &window{Start: opts.Start, Duration: duration},
		timeout: clipTimeout(duration),
		filters: opts.Filters,
		format:  opts.Format,
		tempDir: opts.TempDir,
	})
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
	}
	return validateFilters(opts.Filters)
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
	return run(ctx, &job{path: inputPath, timeout: processingTimeout, filters: f, format: audioformat.WAV})
}

// AudibleBats decodes the whole source, time-expands it by factor to 48 kHz, then
// applies normalization and gain (f.Denoise must be empty) and returns a 16-bit
// WAV. A source below audioformat.MinBatSampleRate is rejected from its header
// with ErrBelowMinimumRate. The timeout is 120 s for the whole pipeline.
func AudibleBats(ctx context.Context, inputPath string, factor int, f Filters) ([]byte, error) {
	if f.Denoise != "" {
		return nil, validationError(ErrInvalidRequest, "audible_bats", "denoise is not offered for audible bats")
	}
	if err := validateFilters(f); err != nil {
		return nil, err
	}
	if !IsValidBatExpansionFactor(factor) {
		return nil, validationError(ErrInvalidRequest, "audible_bats", "unsupported expansion factor %d", factor)
	}
	return run(ctx, &job{
		path:      inputPath,
		timeout:   audibleBatsTimeout,
		minRate:   audioformat.MinBatSampleRate,
		expansion: factor,
		filters:   f,
		format:    audioformat.WAV,
	})
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
		return "", wrapError(err, errors.CategoryFileIO, "processing_temp_dir")
	}
	return dir, nil
}
