package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TempExt is the temporary file extension used when exporting audio with FFmpeg.
// Audio files are written with this suffix during encoding and renamed upon
// completion to ensure atomic file operations.
const TempExt = ".temp"

// exportTimeout is the maximum time allowed for a single PCM-to-file export operation.
const exportTimeout = 30 * time.Second

// Format-specific maximum bitrate limits (kbps). Requested bitrates above
// these values are clamped to prevent encoder errors or bloated output.
const (
	// maxBitrateOpusKbps is the maximum bitrate for Opus encoding.
	// Opus specification caps useful bitrate at 256 kbps for stereo.
	maxBitrateOpusKbps = 256

	// maxBitrateMP3Kbps is the maximum bitrate for MP3 encoding.
	// MPEG-1 Layer III maximum for 44.1/48 kHz stereo.
	maxBitrateMP3Kbps = 320
)

// ExportOptions contains all parameters for exporting PCM audio to a file.
type ExportOptions struct {
	// PCMData is the raw PCM audio data to encode.
	PCMData []byte
	// OutputPath is the destination file path (final path, without TempExt).
	OutputPath string
	// Format is the target audio format (e.g., FormatMP3, FormatFLAC).
	Format string
	// Bitrate is the target bitrate for lossy formats (e.g., "192k").
	Bitrate string
	// SampleRate is the PCM input sample rate in Hz (e.g., 48000).
	SampleRate int
	// Channels is the number of PCM input channels (e.g., 1 for mono).
	Channels int
	// BitDepth is the PCM input bit depth (16, 24, or 32).
	BitDepth int
	// Normalization controls EBU R128 loudness normalisation.
	Normalization ExportNormalization
	// GainDB is the volume adjustment in dB (0 = no change).
	GainDB float64
	// FFmpegPath is the absolute path to the FFmpeg binary.
	FFmpegPath string
}

// ExportNormalization holds the parameters for EBU R128 loudness normalisation
// applied during audio export.
type ExportNormalization struct {
	// Enabled activates the loudnorm FFmpeg filter.
	Enabled bool
	// TargetLUFS is the integrated loudness target in LUFS (e.g., -23.0).
	TargetLUFS float64
	// TruePeak is the true-peak ceiling in dBTP (e.g., -2.0).
	TruePeak float64
	// LoudnessRange is the loudness range target in LU (e.g., 7.0).
	LoudnessRange float64
}

// ExportAudio encodes PCM data to the specified format and writes the result
// to opts.OutputPath. The write is atomic: data is written to a temp file and
// renamed on success.
func ExportAudio(ctx context.Context, opts *ExportOptions) error {
	if opts == nil {
		return fmt.Errorf("export options cannot be nil")
	}
	// Validate inputs.
	if err := ValidateFFmpegPath(opts.FFmpegPath); err != nil {
		return fmt.Errorf("invalid FFmpeg path: %w", err)
	}

	if opts.OutputPath == "" {
		return fmt.Errorf("empty output path provided")
	}

	if len(opts.PCMData) == 0 {
		return fmt.Errorf("empty PCM data provided for export")
	}

	// Apply a per-call timeout so the operation cannot hang indefinitely.
	ctx, cancel := context.WithTimeout(ctx, exportTimeout)
	defer cancel()

	// Create the output directory if needed.
	outputDir := filepath.Dir(opts.OutputPath)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return fmt.Errorf("failed to create output directory %s: %w", outputDir, err)
	}

	// Write to a temp file first for atomic finalisation.
	tempPath := opts.OutputPath + TempExt
	defer func() {
		// Best-effort cleanup of the temp file if export failed.
		if _, statErr := os.Stat(tempPath); statErr == nil {
			_ = os.Remove(tempPath)
		}
	}()

	if err := runExportFFmpeg(ctx, opts, tempPath); err != nil {
		return fmt.Errorf("FFmpeg export failed: %w", err)
	}

	// Atomic rename to final path.
	if err := os.Rename(tempPath, opts.OutputPath); err != nil {
		return fmt.Errorf("failed to finalize export output: %w", err)
	}

	return nil
}

// runExportFFmpeg executes FFmpeg, writing PCM from stdin to the temp output file.
func runExportFFmpeg(ctx context.Context, opts *ExportOptions, tempPath string) error {
	args := buildExportFFmpegArgs(opts, tempPath)

	cmd := exec.CommandContext(ctx, opts.FFmpegPath, args...) //nolint:gosec // G204: FFmpegPath validated by ValidateFFmpegPath, args built internally

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("failed to start FFmpeg (context error): %w", ctx.Err())
		}
		return fmt.Errorf("failed to start FFmpeg: %w, stderr: %s", err, stderr.String())
	}

	// Write PCM data in a goroutine to avoid blocking the main goroutine.
	writeErrCh := make(chan error, 1)
	go func() {
		defer func() { _ = stdin.Close() }()

		select {
		case <-ctx.Done():
			writeErrCh <- ctx.Err()
			return
		default:
		}

		_, writeErr := stdin.Write(opts.PCMData)
		writeErrCh <- writeErr
	}()

	// Wait for write to complete or context cancellation.
	select {
	case writeErr := <-writeErrCh:
		if writeErr != nil {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return fmt.Errorf("failed to write PCM data to FFmpeg: %w, stderr: %s", writeErr, stderr.String())
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("export cancelled: %w", ctx.Err())
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("FFmpeg export timed out: %w", ctx.Err())
		}

		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}

		return fmt.Errorf("FFmpeg export failed (exit_code=%d): %w, stderr: %s", exitCode, err, stderr.String())
	}

	return nil
}

// losslessFormats lists formats that do not accept a bitrate setting.
var losslessFormats = map[string]bool{
	FormatFLAC: true,
	FormatALAC: true,
	FormatWAV:  true,
}

// buildExportFFmpegArgs constructs the FFmpeg argument list for PCM-to-file export.
func buildExportFFmpegArgs(opts *ExportOptions, tempPath string) []string {
	sampleRateStr, channelsStr, formatStr := GetFFmpegFormat(opts.SampleRate, opts.Channels, opts.BitDepth)

	outputEncoder := getEncoder(opts.Format)
	outputFormat := getOutputFormat(opts.Format)

	args := []string{
		"-hide_banner",
		"-f", formatStr,
		"-ar", sampleRateStr,
		"-ac", channelsStr,
		"-i", "-", // read from stdin
	}

	// Add audio filter if normalization or gain is requested.
	audioFilter := buildExportAudioFilter(opts)
	if audioFilter != "" {
		args = append(args, "-af", audioFilter)
	}

	args = append(args, "-c:a", outputEncoder)

	// Lossless formats do not accept a bitrate parameter.
	if !losslessFormats[opts.Format] && opts.Bitrate != "" {
		outputBitrate := getMaxBitrate(opts.Format, opts.Bitrate)
		args = append(args, "-b:a", outputBitrate)
	}

	args = append(args,
		"-f", outputFormat,
		"-y", // overwrite if exists
		tempPath,
	)

	return args
}

// buildExportAudioFilter constructs the FFmpeg -af filter string for PCM export.
// Normalization takes precedence over gain adjustment.
func buildExportAudioFilter(opts *ExportOptions) string {
	if opts.Normalization.Enabled {
		return fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f",
			opts.Normalization.TargetLUFS,
			opts.Normalization.TruePeak,
			opts.Normalization.LoudnessRange,
		)
	}

	if opts.GainDB != 0 {
		if opts.GainDB > 0 {
			return fmt.Sprintf("volume=+%.1fdB", opts.GainDB)
		}
		return fmt.Sprintf("volume=%.1fdB", opts.GainDB) // negative sign included
	}

	return ""
}

// parseBitrateKbps extracts the numeric portion of a bitrate string like "192k"
// and returns it as an integer (kbps). Returns 0 if the string cannot be parsed.
func parseBitrateKbps(bitrate string) int {
	s := strings.TrimSuffix(strings.ToLower(bitrate), "k")
	n, err := strconv.Atoi(s)
	if err != nil {
		return 0
	}
	return n
}

// getMaxBitrate limits the bitrate to the maximum allowed by the format.
// Bitrate strings are parsed numerically so that e.g. "64k" is correctly
// recognised as less than "256k".
func getMaxBitrate(format, requestedBitrate string) string {
	requested := parseBitrateKbps(requestedBitrate)
	switch format {
	case FormatOpus:
		if requested > maxBitrateOpusKbps {
			return strconv.Itoa(maxBitrateOpusKbps) + "k"
		}
	case FormatMP3:
		if requested > maxBitrateMP3Kbps {
			return strconv.Itoa(maxBitrateMP3Kbps) + "k"
		}
	}
	return requestedBitrate
}

// ExportAudioToBuffer encodes PCM data using custom FFmpeg arguments and returns
// the result as an in-memory buffer. Useful for streaming responses.
func ExportAudioToBuffer(ctx context.Context, pcmData []byte, ffmpegPath string, sampleRate, channels, bitDepth int, customArgs []string) (*bytes.Buffer, error) {
	if err := ValidateFFmpegPath(ffmpegPath); err != nil {
		return nil, fmt.Errorf("invalid FFmpeg path: %w", err)
	}

	if len(pcmData) == 0 {
		return nil, fmt.Errorf("empty PCM data provided")
	}

	if len(customArgs) == 0 {
		return nil, fmt.Errorf("empty custom FFmpeg arguments")
	}

	sampleRateStr, channelsStr, formatStr := GetFFmpegFormat(sampleRate, channels, bitDepth)

	args := make([]string, 0, 9+len(customArgs)+1)
	args = append(args,
		"-hide_banner",
		"-f", formatStr,
		"-ar", sampleRateStr,
		"-ac", channelsStr,
		"-i", "-",
	)
	args = append(args, customArgs...)
	args = append(args, "pipe:1")

	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath validated above, args built internally

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg: %w, stderr: %s", err, stderr.String())
	}

	// Write PCM data in a goroutine.
	writeErrCh := make(chan error, 1)
	go func() {
		defer func() { _ = stdin.Close() }()

		select {
		case <-ctx.Done():
			writeErrCh <- ctx.Err()
			return
		default:
		}

		_, writeErr := stdin.Write(pcmData)
		writeErrCh <- writeErr
	}()

	// Read stdout into a buffer concurrently.
	var outputBuf bytes.Buffer
	readErrCh := make(chan error, 1)
	go func() {
		_, readErr := io.Copy(&outputBuf, stdout)
		readErrCh <- readErr
	}()

	// Wait for both goroutines.
	var writeErr, readErr error
	select {
	case writeErr = <-writeErrCh:
		select {
		case readErr = <-readErrCh:
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
	}

	if writeErr != nil {
		return nil, fmt.Errorf("failed to write PCM data: %w", writeErr)
	}
	if readErr != nil {
		return nil, fmt.Errorf("failed to read FFmpeg output: %w", readErr)
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
		}
		return nil, fmt.Errorf("FFmpeg failed: %w, stderr: %s", err, stderr.String())
	}

	return &outputBuf, nil
}
