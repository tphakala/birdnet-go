package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"time"
)

// Audio format constants for export and clip operations.
const (
	FormatAAC  = "aac"
	FormatFLAC = "flac"
	FormatALAC = "alac"
	FormatOpus = "opus"
	FormatMP3  = "mp3"
	FormatWAV  = "wav"
)

// Clip extraction timeout bounds. The actual timeout scales with requested
// duration (2x) but is clamped to these limits.
const (
	clipExtractionMinTimeout = 30 * time.Second
	clipExtractionMaxTimeout = 10 * time.Minute
)

// MaxClipDurationSec is the maximum allowed clip duration in seconds.
// Prevents memory exhaustion from very long extraction requests.
const MaxClipDurationSec = 300 // 5 minutes

// clipDefaultBitrates defines the default bitrates for lossy clip extraction formats.
// These are independent of the audio export bitrate setting — clips are short previews
// so lower bitrates keep file sizes small while preserving sufficient quality.
var clipDefaultBitrates = map[string]string{
	FormatMP3:  "128k",
	FormatOpus: "64k",
	FormatAAC:  "96k",
}

// supportedClipFormats lists the formats supported by ExtractClip.
var supportedClipFormats = map[string]bool{
	FormatWAV:  true,
	FormatMP3:  true,
	FormatFLAC: true,
	FormatOpus: true,
	FormatAAC:  true,
	FormatALAC: true,
}

// requiresSeekableOutput lists formats whose muxers cannot write to a pipe.
// MP4-based containers (AAC → mp4, ALAC → ipod) need seekable output to
// write the moov atom. FLAC needs seekable output to finalize the STREAMINFO
// header (total sample count, min/max frame sizes, MD5 checksum). Without it,
// these fields are zeroed and players may reject or misinterpret the file.
var requiresSeekableOutput = map[string]bool{
	FormatAAC:  true,
	FormatALAC: true,
	FormatFLAC: true,
}

// ClipOptions contains all parameters for extracting an audio clip.
type ClipOptions struct {
	// InputPath is the path to the source audio file.
	InputPath string
	// OutputPath is the destination file path when using file output mode.
	// Ignored when extracting to a buffer (ExtractClip returns a buffer).
	OutputPath string
	// Start is the start time in seconds (must be >= 0).
	Start float64
	// End is the end time in seconds (must be > Start).
	End float64
	// Format is the output audio format (e.g., FormatWAV, FormatMP3).
	Format string
	// Filters contains optional audio processing filters.
	Filters *AudioFilters
	// FFmpegPath is the absolute path to the FFmpeg binary.
	FFmpegPath string
}

// IsSupportedClipFormat returns true if the format is supported for clip extraction.
func IsSupportedClipFormat(format string) bool {
	return supportedClipFormats[format]
}

// getFileExtension returns the appropriate file extension for a format.
// AAC audio uses the M4A container (MPEG-4 Part 14 audio-only profile)
// rather than raw .aac, because M4A supports seeking and metadata that
// raw AAC streams lack.
func getFileExtension(format string) string {
	if format == FormatAAC {
		return "m4a"
	}
	return format
}

// ExtractClip extracts a time range from an audio file and re-encodes it
// to the specified format. The result is returned as an in-memory buffer.
func ExtractClip(ctx context.Context, opts *ClipOptions) (*bytes.Buffer, error) {
	if opts == nil {
		return nil, fmt.Errorf("clip options cannot be nil")
	}
	// Validate parameters.
	if opts.Start < 0 {
		return nil, fmt.Errorf("start time must be non-negative, got %f", opts.Start)
	}
	if opts.End <= opts.Start {
		return nil, fmt.Errorf("end time (%f) must be greater than start time (%f)", opts.End, opts.Start)
	}
	if opts.End-opts.Start > MaxClipDurationSec {
		return nil, fmt.Errorf("clip duration (%.1fs) exceeds maximum (%ds)", opts.End-opts.Start, MaxClipDurationSec)
	}
	if !supportedClipFormats[opts.Format] {
		return nil, fmt.Errorf("unsupported clip format: %q", opts.Format)
	}

	// Validate FFmpeg path.
	if err := ValidateFFmpegPath(opts.FFmpegPath); err != nil {
		return nil, fmt.Errorf("invalid FFmpeg path: %w", err)
	}

	duration := opts.End - opts.Start

	// Create context with adaptive timeout (2x duration, clamped to bounds).
	// Placed before analysis so both loudness analysis and extraction are governed.
	timeout := max(time.Duration(duration*2)*time.Second, clipExtractionMinTimeout)
	timeout = min(timeout, clipExtractionMaxTimeout)
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	// Handle normalize two-pass if filters include normalization.
	filters := opts.Filters
	if filters != nil && filters.Normalize && filters.LoudnessStats == nil {
		seekRange := &SeekRange{Start: opts.Start, Duration: duration}
		stats, err := AnalyzeFileLoudness(ctx, opts.InputPath, opts.FFmpegPath,
			AudioFilters{Denoise: filters.Denoise, Normalize: true}, seekRange)
		if err != nil {
			return nil, fmt.Errorf("loudness analysis for clip failed: %w", err)
		}
		// Copy filters so we don't mutate the caller's struct.
		filtersCopy := *filters
		filtersCopy.LoudnessStats = stats
		filters = &filtersCopy
	}

	// MP4-based formats (AAC, ALAC) and FLAC require seekable output — use a temp file.
	if requiresSeekableOutput[opts.Format] {
		return extractClipViaTempFile(ctx, opts.FFmpegPath, opts.InputPath, opts.Start, duration, opts.Format, filters)
	}

	return extractClipViaPipe(ctx, opts.FFmpegPath, opts.InputPath, opts.Start, duration, opts.Format, filters)
}

// extractClipViaPipe runs FFmpeg with output piped to stdout.
func extractClipViaPipe(ctx context.Context, ffmpegPath, inputPath string, start, duration float64, format string, filters *AudioFilters) (*bytes.Buffer, error) {
	args := buildClipFFmpegArgs(inputPath, start, duration, format, "pipe:1", filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by ValidateFFmpegPath, args built internally

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
		return nil, fmt.Errorf("FFmpeg produced empty output for %s (start=%.2f, duration=%.2f)", filepath.Base(inputPath), start, duration)
	}

	return &stdout, nil
}

// extractClipViaTempFile writes FFmpeg output to a temporary file, then reads
// it into memory. Required for MP4-based muxers and FLAC that need seekable output.
func extractClipViaTempFile(ctx context.Context, ffmpegPath, inputPath string, start, duration float64, format string, filters *AudioFilters) (*bytes.Buffer, error) {
	ext := getFileExtension(format)
	tmpFile, err := os.CreateTemp("", "birdnet-clip-*."+ext)
	if err != nil {
		return nil, fmt.Errorf("failed to create temp file for clip extraction: %w", err)
	}
	tmpPath := tmpFile.Name()
	_ = tmpFile.Close()
	defer func() { _ = os.Remove(tmpPath) }()

	args := buildClipFFmpegArgs(inputPath, start, duration, format, tmpPath, filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by ValidateFFmpegPath, args built internally

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("clip extraction timed out or cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("FFmpeg clip extraction failed: %w, stderr: %s", err, stderr.String())
	}

	data, err := os.ReadFile(tmpPath) //nolint:gosec // G304: tmpPath is generated by os.CreateTemp
	if err != nil {
		return nil, fmt.Errorf("failed to read temp clip file: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("FFmpeg produced empty output for %s (start=%.2f, duration=%.2f)", filepath.Base(inputPath), start, duration)
	}

	return bytes.NewBuffer(data), nil
}

// buildClipFFmpegArgs constructs the FFmpeg command arguments for clip extraction.
// Uses -ss before -i for fast input seeking and -t for duration (not -to, which
// has inconsistent behavior across FFmpeg versions when combined with input seeking).
// Always re-encodes to ensure frame-accurate cuts (no -c copy).
// outputTarget is either "pipe:1" for stdout or a file path.
func buildClipFFmpegArgs(inputPath string, start, duration float64, format, outputTarget string, filters *AudioFilters) []string {
	var outputEncoder, outputFormat string
	if format == FormatWAV {
		outputEncoder = "pcm_s16le"
		outputFormat = FormatWAV
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
	// (independent of the audio export bitrate setting).
	if bitrate, ok := clipDefaultBitrates[format]; ok {
		args = append(args, "-b:a", bitrate)
	}

	// Add processing filters if provided.
	if filters != nil {
		filterChain := BuildProcessingFilterChain(*filters)
		if filterChain != "" {
			args = append(args, "-af", filterChain)
		}
	}

	args = append(args,
		"-f", outputFormat,
		"-y", // overwrite temp file without prompting
		outputTarget,
	)

	return args
}

// getEncoder returns the FFmpeg codec name for a given format.
func getEncoder(format string) string {
	switch format {
	case FormatFLAC:
		return FormatFLAC
	case FormatALAC:
		return FormatALAC
	case FormatOpus:
		return "libopus"
	case FormatAAC:
		return FormatAAC
	case FormatMP3:
		return "libmp3lame"
	default:
		return format
	}
}

// getOutputFormat returns the FFmpeg output container format for a given format.
func getOutputFormat(format string) string {
	switch format {
	case FormatFLAC:
		return FormatFLAC
	case FormatALAC:
		return "ipod" // ALAC uses the iPod container format.
	case FormatOpus:
		return FormatOpus
	case FormatAAC:
		// AAC is muxed into MP4 (produces .m4a files — the audio-only profile of MP4).
		// Raw .aac lacks seeking and metadata support that MP4 provides.
		return "mp4"
	case FormatMP3:
		return FormatMP3
	default:
		return format
	}
}
