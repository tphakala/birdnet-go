package ffmpeg

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// osWindows is runtime.GOOS on Windows, where the sox and ffprobe binaries carry
// a .exe suffix (see getSoxBinaryName / getFfprobeBinaryName).
const osWindows = "windows"

// TempExt is the temporary file extension used when exporting audio. It aliases
// audiotemp.Ext; the process-unique temp name and the atomic, Windows-safe rename
// live in the shared audiotemp package (see ExportAudio / GitHub #3323).
const TempExt = audiotemp.Ext

// minExportPhaseTimeout is the minimum time allowed for a single FFmpeg export phase.
const minExportPhaseTimeout = 30 * time.Second

// exportPhaseTimeoutMargin gives FFmpeg startup, muxing, and cleanup a buffer
// beyond the PCM duration for long extended-capture clips.
const exportPhaseTimeoutMargin = 30 * time.Second

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
		return errors.Newf("export options cannot be nil").
			Component("audiocore/ffmpeg").
			Category(errors.CategoryValidation).
			Context("operation", "export_validate").
			Build()
	}
	// Validate inputs.
	if err := ValidateFFmpegPath(opts.FFmpegPath); err != nil {
		// ValidateFFmpegPath already returns a fully enhanced, telemetry-tagged
		// error; return it directly. Re-wrapping it with another enhanced builder
		// would report the same failure to Sentry twice.
		return err
	}

	if opts.OutputPath == "" {
		return errors.Newf("empty output path provided").
			Component("audiocore/ffmpeg").
			Category(errors.CategoryValidation).
			Context("operation", "export_validate").
			Build()
	}

	if len(opts.PCMData) == 0 {
		return errors.Newf("empty PCM data provided for export").
			Component("audiocore/ffmpeg").
			Category(errors.CategoryValidation).
			Context("operation", "export_validate").
			Build()
	}

	phaseTimeout := exportPhaseTimeout(opts)

	// Create the output directory if needed.
	outputDir := filepath.Dir(opts.OutputPath)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		return errors.Newf("failed to create output directory for export: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryFileIO).
			Context("operation", "export_create_directory").
			Build()
	}

	// Write to a unique temp file first for atomic finalisation, so concurrent
	// exports targeting the same final path do not share one temp file (see
	// audiotemp / GitHub #3323).
	tempPath := audiotemp.UniquePath(opts.OutputPath)
	defer func() {
		// Best-effort cleanup of the temp file if export failed.
		if _, statErr := os.Stat(tempPath); statErr == nil {
			_ = os.Remove(tempPath)
		}
	}()

	filterCtx, filterCancel := context.WithTimeout(ctx, phaseTimeout)
	audioFilter, err := buildExportAudioFilter(filterCtx, opts)
	// Capture whether the phase deadline/cancellation fired before filterCancel()
	// is called; otherwise filterCtx.Err() would always read as cancelled below.
	filterTimedOut := filterCtx.Err() != nil
	filterCancel()
	if err != nil {
		// Today the filter pipeline only fails when loudnorm analysis is cancelled
		// or times out; that context error is operational, so return it untagged
		// (mirroring the native FLAC encoder). A future non-context filter failure
		// is tagged at the source here instead.
		if filterTimedOut {
			return err
		}
		return errors.Newf("failed to prepare audio export filter: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_prepare_filter").
			Build()
	}

	exportCtx, exportCancel := context.WithTimeout(ctx, phaseTimeout)
	err = runExportFFmpeg(exportCtx, opts, tempPath, audioFilter)
	exportCancel()
	if err != nil {
		// runExportFFmpeg tags its genuine FFmpeg failures at the source and
		// returns context cancellation/timeout untagged. Return its error directly
		// so it is reported exactly once; re-wrapping with another enhanced error
		// would double-report the already-tagged failures.
		return err
	}

	// Atomic rename to final path (Windows-safe under concurrent dedup).
	if err := audiotemp.Finalize(tempPath, opts.OutputPath); err != nil {
		return errors.Newf("failed to finalize export output: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryFileIO).
			Context("operation", "export_finalize_rename").
			Build()
	}

	return nil
}

func exportPhaseTimeout(opts *ExportOptions) time.Duration {
	if opts == nil || opts.SampleRate <= 0 || opts.Channels <= 0 || opts.BitDepth <= 0 {
		return minExportPhaseTimeout
	}

	bytesPerSample := opts.BitDepth / 8
	if bytesPerSample <= 0 {
		return minExportPhaseTimeout
	}

	bytesPerSecond := int64(opts.SampleRate) * int64(opts.Channels) * int64(bytesPerSample)
	if bytesPerSecond <= 0 {
		return minExportPhaseTimeout
	}

	audioDuration := time.Duration(int64(len(opts.PCMData))) * time.Second / time.Duration(bytesPerSecond)
	if audioDuration <= 0 {
		return minExportPhaseTimeout
	}
	if audioDuration < minExportPhaseTimeout {
		return minExportPhaseTimeout
	}

	return audioDuration + exportPhaseTimeoutMargin
}

// runExportFFmpeg executes FFmpeg, writing PCM from stdin to the temp output file.
func runExportFFmpeg(ctx context.Context, opts *ExportOptions, tempPath, audioFilter string) error {
	args := buildExportFFmpegArgs(opts, tempPath, audioFilter)

	cmd := exec.CommandContext(ctx, opts.FFmpegPath, args...) //nolint:gosec // G204: FFmpegPath validated by ValidateFFmpegPath, args built internally

	stdin, err := cmd.StdinPipe()
	if err != nil {
		return errors.Newf("failed to create stdin pipe: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_ffmpeg_stdin_pipe").
			Build()
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("failed to start FFmpeg (context error): %w", ctx.Err())
		}
		return errors.Newf("failed to start FFmpeg: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_ffmpeg_start").
			Context("error_detail", stderr.String()).
			Build()
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
			// A write failure caused by context cancellation/timeout (either the
			// goroutine forwarding ctx.Err(), or a broken pipe after the killed
			// process closed stdin) is operational; return it untagged, mirroring
			// the cmd.Wait() timeout guard below.
			if ctx.Err() != nil {
				return fmt.Errorf("export cancelled: %w", ctx.Err())
			}
			return errors.Newf("failed to write PCM data to FFmpeg: %w", writeErr).
				Component("audiocore/ffmpeg").
				Category(errors.CategoryAudio).
				Context("operation", "export_ffmpeg_write").
				Context("error_detail", stderr.String()).
				Build()
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

		return errors.Newf("FFmpeg export failed (exit_code=%d): %w", exitCode, err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_ffmpeg_wait").
			Context("exit_code", exitCode).
			Context("error_detail", stderr.String()).
			Build()
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
func buildExportFFmpegArgs(opts *ExportOptions, tempPath, audioFilter string) []string {
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
	if audioFilter != "" {
		args = append(args, "-af", audioFilter)
	}
	if opts.Normalization.Enabled {
		// loudnorm internally upsamples to 192 kHz for true-peak detection.
		// Pin the encoded file back to the source rate so saved clips keep
		// their configured sample rate and avoid inflated FLAC output.
		args = append(args, "-ar", sampleRateStr)
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
func buildExportAudioFilter(ctx context.Context, opts *ExportOptions) (string, error) {
	if opts.Normalization.Enabled {
		return buildNormalizationFilter(ctx, opts)
	}

	if opts.GainDB != 0 {
		return buildVolumeFilter(opts.GainDB), nil
	}

	return "", nil
}

func buildNormalizationFilter(ctx context.Context, opts *ExportOptions) (string, error) {
	stats, err := AnalyzePCMLoudness(ctx, opts.PCMData, opts.FFmpegPath, opts.SampleRate, opts.BitDepth)
	if err != nil {
		if ctx.Err() != nil {
			return "", err
		}
		// Preserve the previous single-pass behavior if FFmpeg cannot produce
		// loudness stats for reasons other than gated near-silence.
		return buildSinglePassLoudnormFilter(opts.Normalization), nil
	}

	if stats == nil || !stats.isValid() {
		offsetDB, ok := loudnormGateFallbackOffset(stats, opts.Normalization)
		if !ok {
			return buildSinglePassLoudnormFilter(opts.Normalization), nil
		}
		return buildLoudnormOffsetFilter(opts.Normalization, offsetDB), nil
	}

	return buildTwoPassLoudnormFilter(opts.Normalization, stats), nil
}

func buildSinglePassLoudnormFilter(norm ExportNormalization) string {
	return fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f",
		norm.TargetLUFS,
		norm.TruePeak,
		norm.LoudnessRange,
	)
}

func buildTwoPassLoudnormFilter(norm ExportNormalization, stats *LoudnessStats) string {
	return fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f:measured_I=%s:measured_LRA=%s:measured_TP=%s:measured_thresh=%s:linear=true:offset=%s",
		norm.TargetLUFS,
		norm.TruePeak,
		norm.LoudnessRange,
		stats.InputI,
		stats.InputLRA,
		stats.InputTP,
		stats.InputThresh,
		stats.TargetOffset,
	)
}

func buildLoudnormOffsetFilter(norm ExportNormalization, offsetDB float64) string {
	return fmt.Sprintf("%s:offset=%.1f",
		buildSinglePassLoudnormFilter(norm),
		offsetDB,
	)
}

func buildVolumeFilter(gainDB float64) string {
	if gainDB > 0 {
		return fmt.Sprintf("volume=+%.1fdB", gainDB)
	}
	return fmt.Sprintf("volume=%.1fdB", gainDB) // negative sign included
}

func loudnormGateFallbackOffset(stats *LoudnessStats, norm ExportNormalization) (float64, bool) {
	if stats == nil {
		return 0, false
	}

	inputTP, ok := parseFiniteFloat(stats.InputTP)
	if !ok {
		return 0, false
	}
	offsetDB := norm.TruePeak - inputTP
	offsetDB = math.Max(-MaxGainDB, math.Min(MaxGainDB, offsetDB))
	if math.Abs(offsetDB) < 0.05 {
		return 0, false
	}

	return offsetDB, true
}

func parseFiniteFloat(value string) (float64, bool) {
	f, err := strconv.ParseFloat(value, 64)
	if err != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return 0, false
	}
	return f, true
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

// EffectiveBitrateKbps returns the bitrate in kbit/s that a clip export of
// format actually uses: the configured string parsed numerically and clamped to
// the format's ceiling, exactly as the FFmpeg command line resolves it. It
// returns 0 when the configured string cannot be parsed, which every encoder
// reads as "use the codec default".
//
// The native AAC and Opus encoders take a numeric bitrate rather than a command
// line, and call this so a clip codes at the same rate whichever encoder runs.
func EffectiveBitrateKbps(format, bitrate string) int {
	return parseBitrateKbps(getMaxBitrate(format, bitrate))
}

// ExportAudioToBuffer encodes PCM data using custom FFmpeg arguments and returns
// the result as an in-memory buffer. Useful for streaming responses.
func ExportAudioToBuffer(ctx context.Context, pcmData []byte, ffmpegPath string, sampleRate, channels, bitDepth int, customArgs []string) (*bytes.Buffer, error) {
	if err := ValidateFFmpegPath(ffmpegPath); err != nil {
		// ValidateFFmpegPath already returns a fully enhanced, telemetry-tagged
		// error; return it directly to avoid double-reporting to Sentry.
		return nil, err
	}

	if len(pcmData) == 0 {
		return nil, errors.Newf("empty PCM data provided").
			Component("audiocore/ffmpeg").
			Category(errors.CategoryValidation).
			Context("operation", "export_buffer_validate").
			Build()
	}

	if len(customArgs) == 0 {
		return nil, errors.Newf("empty custom FFmpeg arguments").
			Component("audiocore/ffmpeg").
			Category(errors.CategoryValidation).
			Context("operation", "export_buffer_validate").
			Build()
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
		return nil, errors.Newf("failed to create stdin pipe: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_buffer_stdin_pipe").
			Build()
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, errors.Newf("failed to create stdout pipe: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_buffer_stdout_pipe").
			Build()
	}

	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	if err := cmd.Start(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
		}
		return nil, errors.Newf("failed to start FFmpeg: %w", err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_buffer_start").
			Context("error_detail", stderr.String()).
			Build()
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
		// Reap the process so a failed write does not leave a zombie; Kill is a
		// no-op if FFmpeg already exited (which is what caused the broken pipe).
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		// A write failure caused by context cancellation/timeout is operational;
		// return it untagged, mirroring the cmd.Wait() guard below.
		if ctx.Err() != nil {
			return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
		}
		return nil, errors.Newf("failed to write PCM data: %w", writeErr).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_buffer_write").
			Context("error_detail", stderr.String()).
			Build()
	}
	if readErr != nil {
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		if ctx.Err() != nil {
			return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
		}
		return nil, errors.Newf("failed to read FFmpeg output: %w", readErr).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_buffer_read").
			Context("error_detail", stderr.String()).
			Build()
	}

	if err := cmd.Wait(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("export to buffer cancelled: %w", ctx.Err())
		}

		exitCode := -1
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			exitCode = exitErr.ExitCode()
		}

		return nil, errors.Newf("FFmpeg failed (exit_code=%d): %w", exitCode, err).
			Component("audiocore/ffmpeg").
			Category(errors.CategoryAudio).
			Context("operation", "export_buffer_wait").
			Context("exit_code", exitCode).
			Context("error_detail", stderr.String()).
			Build()
	}

	return &outputBuf, nil
}
