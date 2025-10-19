// Package spectrogram provides core spectrogram generation logic.
// This file contains the Generator type that consolidates Sox and FFmpeg generation
// used by both the pre-renderer (background mode) and API (on-demand mode).
package spectrogram

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

const (
	// defaultGenerationTimeout is the default timeout for spectrogram generation
	defaultGenerationTimeout = 60 * time.Second

	// dynamicRange for sox spectrogram generation (-z parameter)
	dynamicRange = "100"
)

// SoxInputType specifies the source of audio data for Sox
type SoxInputType int

const (
	// SoxInputPCM indicates PCM data from stdin
	SoxInputPCM SoxInputType = iota
	// SoxInputFile indicates audio file input (directly or via FFmpeg)
	SoxInputFile
)

// Generator handles core spectrogram generation logic.
// It is used by both the pre-renderer (background mode) and API (auto/user-requested modes).
type Generator struct {
	settings *conf.Settings
	sfs      *securefs.SecureFS
	logger   *slog.Logger
}

// NewGenerator creates a new generator instance.
func NewGenerator(settings *conf.Settings, sfs *securefs.SecureFS, logger *slog.Logger) *Generator {
	return &Generator{
		settings: settings,
		sfs:      sfs,
		logger:   logger,
	}
}

// GenerateFromFile creates a spectrogram from an audio file path.
// Used by API on-demand and user-requested modes.
// Tries Sox first (faster), falls back to FFmpeg if Sox fails.
//
// The audioPath and outputPath must be absolute paths.
// Width is in pixels, raw controls whether to show axes/legends.
func (g *Generator) GenerateFromFile(ctx context.Context, audioPath, outputPath string, width int, raw bool) error {
	start := time.Now()
	g.logger.Debug("Starting spectrogram generation from file",
		"audio_path", audioPath,
		"output_path", outputPath,
		"width", width,
		"raw", raw)

	// Ensure output directory exists using SecureFS (validates path automatically)
	if err := g.ensureOutputDirectory(outputPath); err != nil {
		return err
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, defaultGenerationTimeout)
	defer cancel()

	// Try Sox first (faster, direct processing)
	if err := g.generateWithSoxFile(ctx, audioPath, outputPath, width, raw); err != nil {
		g.logger.Debug("Sox generation failed, trying FFmpeg fallback",
			"audio_path", audioPath,
			"sox_error", err.Error())

		// Fallback to FFmpeg pipeline
		if ffmpegErr := g.generateWithFFmpeg(ctx, audioPath, outputPath, width, raw); ffmpegErr != nil {
			g.logger.Error("Both Sox and FFmpeg generation failed",
				"audio_path", audioPath,
				"sox_error", err.Error(),
				"ffmpeg_error", ffmpegErr.Error())
			return ffmpegErr
		}
	}

	g.logger.Debug("Spectrogram generation completed successfully",
		"audio_path", audioPath,
		"duration_ms", time.Since(start).Milliseconds())

	return nil
}

// GenerateFromPCM creates a spectrogram from in-memory PCM data.
// Used by pre-renderer (background mode).
// PCM format: s16le, 48kHz, mono
func (g *Generator) GenerateFromPCM(ctx context.Context, pcmData []byte, outputPath string, width int, raw bool) error {
	start := time.Now()
	g.logger.Debug("Starting spectrogram generation from PCM",
		"output_path", outputPath,
		"pcm_bytes", len(pcmData),
		"width", width,
		"raw", raw)

	// Ensure output directory exists using SecureFS (validates path automatically)
	if err := g.ensureOutputDirectory(outputPath); err != nil {
		return err
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(ctx, defaultGenerationTimeout)
	defer cancel()

	// Generate directly from PCM stdin (no FFmpeg needed)
	if err := g.generateWithSoxPCM(ctx, pcmData, outputPath, width, raw); err != nil {
		return err
	}

	g.logger.Debug("Spectrogram generation from PCM completed successfully",
		"output_path", outputPath,
		"duration_ms", time.Since(start).Milliseconds())

	return nil
}

// generateWithSoxFile generates a spectrogram using Sox from an audio file.
// If the file format is supported by Sox, it uses Sox directly.
// Otherwise, it uses FFmpeg to convert to Sox format and pipes to Sox.
func (g *Generator) generateWithSoxFile(ctx context.Context, audioPath, outputPath string, width int, raw bool) error {
	soxBinary := g.settings.Realtime.Audio.SoxPath
	if soxBinary == "" {
		return errors.Newf("sox binary not configured").
			Component("spectrogram").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_with_sox_file").
			Build()
	}

	// Check if the file extension is supported directly by SoX without needing FFmpeg
	ext := strings.ToLower(filepath.Ext(audioPath))
	ext = strings.TrimPrefix(ext, ".")
	useFFmpeg := true
	for _, soxType := range g.settings.Realtime.Audio.SoxAudioTypes {
		soxType = strings.TrimPrefix(strings.ToLower(soxType), ".")
		if ext == soxType {
			useFFmpeg = false
			break
		}
	}

	// If file is supported by Sox, use Sox directly
	if !useFFmpeg {
		return g.generateWithSoxDirect(ctx, audioPath, outputPath, width, raw)
	}

	// Otherwise, use FFmpeg to convert to Sox format
	return g.generateWithFFmpegSoxPipeline(ctx, audioPath, outputPath, width, raw)
}

// generateWithSoxDirect generates a spectrogram using only Sox (no FFmpeg).
// Used when the audio file format is natively supported by Sox.
func (g *Generator) generateWithSoxDirect(ctx context.Context, audioPath, outputPath string, width int, raw bool) error {
	soxBinary := g.settings.Realtime.Audio.SoxPath
	if soxBinary == "" {
		return errors.Newf("sox binary not configured").
			Component("spectrogram").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_with_sox_direct").
			Build()
	}

	// Build Sox arguments for file input
	soxArgs := g.getSoxArgs(audioPath, outputPath, width, raw, SoxInputFile)

	g.logger.Debug("Executing SoX command directly",
		"sox_binary", soxBinary,
		"audio_path", audioPath,
		"output_path", outputPath)

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, soxBinary, soxArgs...)
	} else {
		// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
	}

	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	runtime.Gosched()
	if err := cmd.Run(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "generate_with_sox_direct").
			Context("audio_path", audioPath).
			Context("output_path", outputPath).
			Context("sox_output", output.String()).
			Build()
	}
	runtime.Gosched()

	return nil
}

// generateWithFFmpegSoxPipeline generates a spectrogram using FFmpeg piped to Sox.
// Used when the audio file format is not natively supported by Sox.
func (g *Generator) generateWithFFmpegSoxPipeline(ctx context.Context, audioPath, outputPath string, width int, raw bool) error {
	ffmpegBinary := g.settings.Realtime.Audio.FfmpegPath
	soxBinary := g.settings.Realtime.Audio.SoxPath

	if ffmpegBinary == "" {
		return errors.Newf("ffmpeg binary not configured").
			Component("spectrogram").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_with_ffmpeg_sox_pipeline").
			Build()
	}
	if soxBinary == "" {
		return errors.Newf("sox binary not configured").
			Component("spectrogram").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_with_ffmpeg_sox_pipeline").
			Build()
	}

	// FFmpeg converts audio to Sox format and pipes to Sox
	ffmpegArgs := []string{"-hide_banner", "-i", audioPath, "-f", "sox", "-"}
	soxArgs := append([]string{"-t", "sox", "-"}, g.getSoxSpectrogramArgs(ctx, audioPath, outputPath, width, raw)...)

	var ffmpegCmd, soxCmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// #nosec G204 - binaries are validated by exec.LookPath during config initialization
		ffmpegCmd = exec.CommandContext(ctx, ffmpegBinary, ffmpegArgs...)
		soxCmd = exec.CommandContext(ctx, soxBinary, soxArgs...)
	} else {
		// #nosec G204 - binaries are validated by exec.LookPath during config initialization
		ffmpegCmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
		soxCmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
	}

	// Connect FFmpeg stdout to Sox stdin
	var err error
	soxCmd.Stdin, err = ffmpegCmd.StdoutPipe()
	if err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "create_pipe").
			Context("audio_path", audioPath).
			Build()
	}

	var ffmpegOutput, soxOutput bytes.Buffer
	ffmpegCmd.Stderr = &ffmpegOutput
	soxCmd.Stderr = &soxOutput

	runtime.Gosched()

	// Start Sox first (consumer)
	if err := soxCmd.Start(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "start_sox").
			Context("audio_path", audioPath).
			Build()
	}

	// Store Sox PID early for cleanup logging
	soxPid := -1
	if soxCmd.Process != nil {
		soxPid = soxCmd.Process.Pid
	}

	// Track Sox process completion
	soxWaitDone := false
	defer func() {
		// Ensure Sox process is cleaned up to prevent zombies
		if !soxWaitDone && soxCmd.Process != nil {
			g.waitWithTimeout(soxCmd, 5*time.Second)
		}
	}()

	// Run FFmpeg (producer)
	if err := ffmpegCmd.Run(); err != nil {
		// FFmpeg failed - kill Sox since it won't receive valid input
		if soxCmd.Process != nil {
			if killErr := soxCmd.Process.Kill(); killErr != nil {
				g.logger.Debug("Failed to kill Sox process after FFmpeg failure",
					"error", killErr.Error(),
					"sox_pid", soxPid)
			}
		}
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "run_ffmpeg").
			Context("audio_path", audioPath).
			Context("ffmpeg_output", ffmpegOutput.String()).
			Context("sox_output", soxOutput.String()).
			Build()
	}

	runtime.Gosched()

	// FFmpeg succeeded - wait for Sox to finish processing
	soxWaitErr := g.waitWithTimeoutErr(soxCmd, 5*time.Second)
	soxWaitDone = true

	if soxWaitErr != nil {
		return errors.New(soxWaitErr).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "wait_sox").
			Context("audio_path", audioPath).
			Context("ffmpeg_output", ffmpegOutput.String()).
			Context("sox_output", soxOutput.String()).
			Build()
	}
	runtime.Gosched()

	return nil
}

// generateWithSoxPCM generates a spectrogram by feeding PCM data directly to Sox stdin.
// This bypasses FFmpeg entirely, reducing CPU overhead and memory usage.
// PCM format: s16le, 48kHz, mono
func (g *Generator) generateWithSoxPCM(ctx context.Context, pcmData []byte, outputPath string, width int, raw bool) error {
	soxBinary := g.settings.Realtime.Audio.SoxPath
	if soxBinary == "" {
		return errors.Newf("sox binary not configured").
			Component("spectrogram").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_with_sox_pcm").
			Build()
	}

	// Build Sox arguments for PCM stdin
	args := []string{
		"-t", "raw",              // Input type: raw/headerless PCM
		"-r", "48000",            // Sample rate: 48kHz (conf.SampleRate)
		"-e", "signed",           // Encoding: signed integer
		"-b", "16",               // Bit depth: 16-bit (conf.BitDepth)
		"-c", "1",                // Channels: mono
		"-",                      // Read from stdin
		"-n",                     // No audio output (null output)
		"rate", "24k",            // Resample to 24kHz for spectrogram
		"spectrogram",            // Effect: spectrogram
		"-x", strconv.Itoa(width), // Width in pixels
		"-y", strconv.Itoa(width / 2), // Height in pixels (half of width)
		"-o", outputPath,         // Output PNG file
	}

	// Add raw flag if requested (no axes/legend)
	if raw {
		args = append(args, "-r")
	}

	// Build command with low priority
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, soxBinary, args...)
	} else {
		// #nosec G204 - soxBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", soxBinary}, args...)...)
	}

	// Feed PCM data to stdin
	cmd.Stdin = bytes.NewReader(pcmData)

	// Capture stderr for debugging
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Run command
	if err := cmd.Run(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "generate_with_sox_pcm").
			Context("output_path", outputPath).
			Context("width", width).
			Context("raw", raw).
			Context("sox_stderr", stderr.String()).
			Context("pcm_bytes", len(pcmData)).
			Build()
	}

	return nil
}

// generateWithFFmpeg generates a spectrogram using only FFmpeg (no Sox).
// This is a fallback when Sox is not available or fails.
func (g *Generator) generateWithFFmpeg(ctx context.Context, audioPath, outputPath string, width int, raw bool) error {
	ffmpegBinary := g.settings.Realtime.Audio.FfmpegPath
	if ffmpegBinary == "" {
		return errors.Newf("ffmpeg binary not configured").
			Component("spectrogram").
			Category(errors.CategoryConfiguration).
			Context("operation", "generate_with_ffmpeg").
			Build()
	}

	height := width / 2
	var filterStr string
	if raw {
		// Raw spectrogram without frequency/time axes and legends
		filterStr = fmt.Sprintf("showspectrumpic=s=%dx%d:legend=0:gain=3:drange=100", width, height)
	} else {
		// Standard spectrogram with frequency/time axes and legends
		filterStr = fmt.Sprintf("showspectrumpic=s=%dx%d:legend=1:gain=3:drange=100", width, height)
	}

	ffmpegArgs := []string{
		"-hide_banner",
		"-y",
		"-i", audioPath,
		"-lavfi", filterStr,
		"-frames:v", "1",
		outputPath,
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// #nosec G204 - ffmpegBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, ffmpegBinary, ffmpegArgs...)
	} else {
		// #nosec G204 - ffmpegBinary is validated by exec.LookPath during config initialization
		cmd = exec.CommandContext(ctx, "nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
	}

	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	if err := cmd.Run(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "generate_with_ffmpeg").
			Context("audio_path", audioPath).
			Context("output_path", outputPath).
			Context("ffmpeg_output", output.String()).
			Build()
	}

	return nil
}

// getSoxArgs builds Sox arguments for file input.
// Used when Sox can directly read the audio file.
func (g *Generator) getSoxArgs(audioPath, outputPath string, width int, raw bool, inputType SoxInputType) []string {
	var args []string
	if inputType == SoxInputFile {
		args = []string{audioPath}
	}

	args = append(args, g.getSoxSpectrogramArgs(context.Background(), audioPath, outputPath, width, raw)...)
	return args
}

// getSoxSpectrogramArgs returns the common Sox arguments for spectrogram generation.
// This implements FFmpeg version-aware optimization for duration handling.
//
// FFmpeg 5.x Bug: The sox protocol (-f sox) has a bug where duration information is not
// correctly passed to SoX. This requires an explicit -d (duration) parameter via ffprobe.
//
// FFmpeg 7.x+ Fix: The sox protocol correctly passes duration metadata, eliminating the
// need for the expensive ffprobe call and -d parameter.
func (g *Generator) getSoxSpectrogramArgs(ctx context.Context, audioPath, outputPath string, width int, raw bool) []string {
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	// Build base args without duration parameter
	args := []string{"-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr}

	// Check if we need to explicitly provide duration based on FFmpeg version
	needsExplicitDuration := true
	if g.settings.Realtime.Audio.HasFfmpegVersion() {
		if g.settings.Realtime.Audio.FfmpegMajor >= 7 {
			// FFmpeg 7.x+: sox protocol works correctly, skip expensive ffprobe call
			needsExplicitDuration = false
			g.logger.Debug("FFmpeg 7.x+ detected: skipping explicit duration parameter",
				"ffmpeg_version", g.settings.Realtime.Audio.FfmpegVersion,
				"optimization", "enabled")
		}
	}

	// For FFmpeg <7.x, explicitly provide duration via -d parameter
	if needsExplicitDuration {
		// Get audio file duration via ffprobe (with caching)
		duration := getCachedAudioDuration(ctx, audioPath)
		if duration <= 0 {
			// Fallback: Use configured capture length if ffprobe fails
			captureLength := g.settings.Realtime.Audio.Export.Length
			duration = float64(captureLength)
			g.logger.Warn("FFprobe failed, using configured fallback duration",
				"fallback_duration_seconds", duration,
				"audio_path", audioPath)
		}

		// Convert duration to string, rounding to nearest integer
		captureLengthStr := strconv.Itoa(int(duration + 0.5))
		args = append(args, "-d", captureLengthStr)
	}

	// Add remaining common parameters
	args = append(args, "-z", dynamicRange, "-o", outputPath)

	// Add raw flag if requested (no axes/legends)
	if raw {
		args = append(args, "-r")
	}

	return args
}

// ensureOutputDirectory creates the output directory if it doesn't exist.
// Uses SecureFS for path validation.
func (g *Generator) ensureOutputDirectory(outputPath string) error {
	outputDir := filepath.Dir(outputPath)
	if err := g.sfs.MkdirAll(outputDir, 0o755); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategoryFileIO).
			Context("operation", "ensure_output_directory").
			Context("output_dir", outputDir).
			Context("output_path", outputPath).
			Build()
	}
	return nil
}

// waitWithTimeout waits for a command to finish with a timeout.
// Prevents zombie processes by ensuring Wait() is called.
func (g *Generator) waitWithTimeout(cmd *exec.Cmd, timeout time.Duration) {
	pid := -1
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		if err != nil {
			g.logger.Debug("Process wait completed with error",
				"pid", pid,
				"error", err.Error())
		}
	case <-time.After(timeout):
		g.logger.Warn("Process wait timed out",
			"pid", pid,
			"timeout_seconds", timeout.Seconds())
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				g.logger.Error("Failed to reap process after kill",
					"pid", pid)
			}
		}
	}
}

// waitWithTimeoutErr is like waitWithTimeout but returns an error.
func (g *Generator) waitWithTimeoutErr(cmd *exec.Cmd, timeout time.Duration) error {
	pid := -1
	if cmd.Process != nil {
		pid = cmd.Process.Pid
	}

	done := make(chan error, 1)
	go func() {
		done <- cmd.Wait()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(timeout):
		g.logger.Warn("Process wait timed out",
			"pid", pid,
			"timeout_seconds", timeout.Seconds())
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			select {
			case err := <-done:
				return fmt.Errorf("process wait timed out after %v (killed, exit error: %w)", timeout, err)
			case <-time.After(1 * time.Second):
				return fmt.Errorf("process wait timed out after %v and failed to kill", timeout)
			}
		}
		return fmt.Errorf("process wait timed out after %v", timeout)
	}
}

// getCachedAudioDuration is a placeholder for the audio duration caching logic.
// This will be implemented separately or imported from the API package.
// For now, it returns 0 to indicate duration should be determined from config.
func getCachedAudioDuration(ctx context.Context, audioPath string) float64 {
	// TODO: Implement audio duration caching
	// This is currently implemented in internal/api/v2/media.go
	// We'll need to either:
	// 1. Move it to a shared package
	// 2. Pass it as a dependency to Generator
	// 3. Accept that Generator doesn't need it for PCM input
	return 0
}

// GetSoxSpectrogramArgsForTest exposes getSoxSpectrogramArgs for testing.
// This method is exported to allow tests in other packages to verify the
// FFmpeg version optimization logic.
func (g *Generator) GetSoxSpectrogramArgsForTest(ctx context.Context, audioPath, outputPath string, width int, raw bool) []string {
	return g.getSoxSpectrogramArgs(ctx, audioPath, outputPath, width, raw)
}
