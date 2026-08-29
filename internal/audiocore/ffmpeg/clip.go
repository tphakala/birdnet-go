package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
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

// DefaultMaxTranscodeOutputBytes bounds the complete response retained for a
// whole-recording export. It accommodates the maximum 20-minute extended
// capture as 48 kHz, mono, 16-bit WAV while keeping concurrent exports bounded.
const DefaultMaxTranscodeOutputBytes int64 = 128 << 20

// ErrTranscodeOutputTooLarge identifies exports that exceed the in-memory
// response limit.
var ErrTranscodeOutputTooLarge = errors.NewStd("transcoded audio exceeds output size limit")

// clipDefaultBitrates defines the default bitrates for lossy clip extraction formats.
// These are independent of the audio export bitrate setting — clips are short previews
// so lower bitrates keep file sizes small while preserving sufficient quality.
var clipDefaultBitrates = map[string]string{
	FormatMP3:  "128k",
	FormatOpus: "64k",
	FormatAAC:  "96k",
}

// recordingExportDefaultBitrates preserve more detail than the preview-oriented
// clip defaults when a user explicitly downloads a whole recording.
var recordingExportDefaultBitrates = map[string]string{
	FormatMP3:  "192k",
	FormatOpus: "128k",
	FormatAAC:  "192k",
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

// TranscodeOptions contains all parameters for transcoding a whole audio file.
type TranscodeOptions struct {
	// InputPath is the path to the source audio file.
	InputPath string
	// Format is the output audio format (e.g., FormatWAV, FormatMP3).
	Format string
	// Filters contains optional audio processing filters.
	Filters *AudioFilters
	// FFmpegPath is the absolute path to the FFmpeg binary.
	FFmpegPath string
	// MaxOutputBytes is the maximum complete output retained in memory. Zero uses
	// DefaultMaxTranscodeOutputBytes; callers may provide a smaller positive limit.
	MaxOutputBytes int64
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
	if format == FormatAAC || format == FormatALAC {
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

// TranscodeAudio re-encodes a whole audio file to the specified format.
// The result is returned as an in-memory buffer.
func TranscodeAudio(ctx context.Context, opts *TranscodeOptions) (*bytes.Buffer, error) {
	if opts == nil {
		return nil, fmt.Errorf("transcode options cannot be nil")
	}
	if !supportedClipFormats[opts.Format] {
		return nil, fmt.Errorf("unsupported audio format: %q", opts.Format)
	}
	if err := ValidateFFmpegPath(opts.FFmpegPath); err != nil {
		return nil, fmt.Errorf("invalid FFmpeg path: %w", err)
	}
	if opts.MaxOutputBytes < 0 || opts.MaxOutputBytes > DefaultMaxTranscodeOutputBytes {
		return nil, fmt.Errorf("max output bytes must be between 1 and %d, or zero for the default", DefaultMaxTranscodeOutputBytes)
	}

	maxOutputBytes := opts.MaxOutputBytes
	if maxOutputBytes == 0 {
		maxOutputBytes = DefaultMaxTranscodeOutputBytes
	}

	ctx, cancel := context.WithTimeout(ctx, clipExtractionMaxTimeout)
	defer cancel()

	filters := opts.Filters
	if filters != nil && filters.Normalize && filters.LoudnessStats == nil {
		stats, err := AnalyzeFileLoudness(ctx, opts.InputPath, opts.FFmpegPath,
			AudioFilters{Denoise: filters.Denoise, Normalize: true}, nil)
		if err != nil {
			return nil, fmt.Errorf("loudness analysis for audio export failed: %w", err)
		}
		filtersCopy := *filters
		filtersCopy.LoudnessStats = stats
		filters = &filtersCopy
	}

	if requiresSeekableOutput[opts.Format] {
		return transcodeAudioViaTempFile(ctx, opts.FFmpegPath, opts.InputPath, opts.Format, filters, maxOutputBytes)
	}

	return transcodeAudioViaPipe(ctx, opts.FFmpegPath, opts.InputPath, opts.Format, filters, maxOutputBytes)
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
		return nil, errors.Newf("FFmpeg clip extraction failed: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "clip_extract_pipe").
			Context("error_detail", stderr.String()).
			Build()
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
	tmpPath, err := createTempOutput("birdnet-clip-*."+ext, "clip_extract")
	if err != nil {
		return nil, err
	}
	defer removeTempOutput(tmpPath, "clip_extract")

	args := buildClipFFmpegArgs(inputPath, start, duration, format, tmpPath, filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by ValidateFFmpegPath, args built internally

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("clip extraction timed out or cancelled: %w", ctx.Err())
		}
		return nil, errors.Newf("FFmpeg clip extraction failed: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "clip_extract_tempfile").
			Context("error_detail", stderr.String()).
			Build()
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

type limitedBuffer struct {
	buffer   bytes.Buffer
	maxBytes int64
	exceeded bool
}

func (b *limitedBuffer) Write(p []byte) (int, error) {
	remaining := b.maxBytes - int64(b.buffer.Len())
	if remaining <= 0 {
		b.exceeded = true
		return 0, ErrTranscodeOutputTooLarge
	}
	if int64(len(p)) > remaining {
		n, _ := b.buffer.Write(p[:int(remaining)])
		b.exceeded = true
		return n, ErrTranscodeOutputTooLarge
	}
	return b.buffer.Write(p)
}

func transcodeOutputLimitError(maxOutputBytes int64) error {
	return fmt.Errorf("%w: maximum is %d bytes", ErrTranscodeOutputTooLarge, maxOutputBytes)
}

func transcodeAudioViaPipe(ctx context.Context, ffmpegPath, inputPath, format string, filters *AudioFilters, maxOutputBytes int64) (*bytes.Buffer, error) {
	args := buildTranscodeFFmpegArgs(inputPath, format, "pipe:1", filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by ValidateFFmpegPath, args built internally

	stdout := limitedBuffer{maxBytes: maxOutputBytes}
	var stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if stdout.exceeded {
			return nil, transcodeOutputLimitError(maxOutputBytes)
		}
		if ctx.Err() != nil {
			return nil, fmt.Errorf("audio export timed out or cancelled: %w", ctx.Err())
		}
		return nil, errors.Newf("FFmpeg audio export failed: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "audio_export_pipe").
			Context("error_detail", stderr.String()).
			Build()
	}
	if stdout.exceeded {
		return nil, transcodeOutputLimitError(maxOutputBytes)
	}

	if stdout.buffer.Len() == 0 {
		return nil, fmt.Errorf("FFmpeg produced empty output for %s", filepath.Base(inputPath))
	}

	return &stdout.buffer, nil
}

func transcodeAudioViaTempFile(ctx context.Context, ffmpegPath, inputPath, format string, filters *AudioFilters, maxOutputBytes int64) (*bytes.Buffer, error) {
	ext := getFileExtension(format)
	tmpPath, err := createTempOutput("birdnet-audio-export-*."+ext, "audio_export")
	if err != nil {
		return nil, err
	}
	defer removeTempOutput(tmpPath, "audio_export")

	args := buildTranscodeFFmpegArgs(inputPath, format, tmpPath, filters)

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated by ValidateFFmpegPath, args built internally

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Run(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("audio export timed out or cancelled: %w", ctx.Err())
		}
		return nil, errors.Newf("FFmpeg audio export failed: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "audio_export_tempfile").
			Context("error_detail", stderr.String()).
			Build()
	}

	data, err := readTempOutputWithLimit(tmpPath, maxOutputBytes)
	if err != nil {
		return nil, fmt.Errorf("failed to read temp audio export file: %w", err)
	}

	if len(data) == 0 {
		return nil, fmt.Errorf("FFmpeg produced empty output for %s", filepath.Base(inputPath))
	}

	return bytes.NewBuffer(data), nil
}

func createTempOutput(pattern, operation string) (string, error) {
	tmpFile, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", fmt.Errorf("failed to create temp file for %s: %w", operation, err)
	}

	tmpPath := tmpFile.Name()
	if err := tmpFile.Close(); err != nil {
		removeTempOutput(tmpPath, operation)
		return "", fmt.Errorf("failed to close temp file for %s: %w", operation, err)
	}
	return tmpPath, nil
}

func removeTempOutput(path, operation string) {
	if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
		getStreamLogger().Warn("failed to remove temporary FFmpeg output",
			logger.String("operation", operation),
			logger.String("file", filepath.Base(path)),
			logger.Error(err))
	}
}

func readTempOutputWithLimit(path string, maxOutputBytes int64) (data []byte, resultErr error) {
	file, err := os.Open(path) //nolint:gosec // G304: path is generated by os.CreateTemp
	if err != nil {
		return nil, err
	}
	defer func() {
		if closeErr := file.Close(); closeErr != nil {
			resultErr = errors.Join(resultErr, fmt.Errorf("failed to close temp audio export file: %w", closeErr))
		}
	}()

	data, err = io.ReadAll(io.LimitReader(file, maxOutputBytes+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > maxOutputBytes {
		return nil, transcodeOutputLimitError(maxOutputBytes)
	}
	return data, nil
}

// buildClipFFmpegArgs constructs the FFmpeg command arguments for clip extraction.
// Uses -ss before -i for fast input seeking and -t for duration (not -to, which
// has inconsistent behavior across FFmpeg versions when combined with input seeking).
// Always re-encodes to ensure frame-accurate cuts (no -c copy).
// outputTarget is either "pipe:1" for stdout or a file path.
func buildClipFFmpegArgs(inputPath string, start, duration float64, format, outputTarget string, filters *AudioFilters) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-ss", fmt.Sprintf("%.6f", start),
		"-i", inputPath,
		"-t", fmt.Sprintf("%.6f", duration),
	}

	return appendAudioOutputArgs(args, format, outputTarget, filters, clipDefaultBitrates)
}

func buildTranscodeFFmpegArgs(inputPath, format, outputTarget string, filters *AudioFilters) []string {
	args := []string{
		"-hide_banner",
		"-loglevel", "error",
		"-i", inputPath,
	}

	return appendAudioOutputArgs(args, format, outputTarget, filters, recordingExportDefaultBitrates)
}

func appendAudioOutputArgs(args []string, format, outputTarget string, filters *AudioFilters, defaultBitrates map[string]string) []string {
	outputEncoder := getEncoder(format)
	outputFormat := getOutputFormat(format)
	if format == FormatWAV {
		outputEncoder = "pcm_s16le"
		outputFormat = FormatWAV
	}

	args = append(args, "-c:a", outputEncoder)

	if bitrate, ok := defaultBitrates[format]; ok {
		args = append(args, "-b:a", bitrate)
	}

	if filters != nil {
		if filterChain := BuildProcessingFilterChain(*filters); filterChain != "" {
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
