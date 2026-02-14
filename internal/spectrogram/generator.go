// Package spectrogram provides core spectrogram generation logic.
// This file contains the Generator type that consolidates Sox and FFmpeg generation
// used by both the pre-renderer (background mode) and API (on-demand mode).
package spectrogram

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

const (
	// defaultGenerationTimeout is the default timeout for spectrogram generation
	// Increased to 90s to accommodate slow storage (e.g., SD cards) under I/O pressure
	defaultGenerationTimeout = 90 * time.Second

	// ffmpegFallbackTimeout is the timeout for FFmpeg fallback when Sox fails.
	// This is independent of the Sox timeout to ensure FFmpeg has adequate time
	// even when Sox consumed most of the original timeout before failing (fixes #1503).
	ffmpegFallbackTimeout = 60 * time.Second

	// soxWaitFallbackTimeout is the fallback timeout for waiting on Sox process completion
	soxWaitFallbackTimeout = 30 * time.Second

	// defaultDynamicRange is the default dynamic range for sox spectrogram generation (-z parameter)
	// This is used as fallback when no valid setting is configured
	defaultDynamicRange = "100"

	// durationCacheTTL is how long duration cache entries remain valid
	durationCacheTTL = 10 * time.Minute

	// maxCacheEntries is the maximum number of entries in the audio duration cache
	// This prevents unbounded memory growth over long operation periods (fixes #1503)
	maxCacheEntries = 1000

	// cacheEvictionPercent is the percentage of entries to remove when cache exceeds max
	cacheEvictionPercent = 10

	// ffmpegGain controls the gain parameter for FFmpeg showspectrumpic filter
	ffmpegGain = "3"

	// ffmpegDrange controls the dynamic range parameter for FFmpeg showspectrumpic filter
	ffmpegDrange = "100"

	// heightRatio is the divisor for calculating spectrogram height from width (height = width / heightRatio)
	heightRatio = 2

	// durationRoundingOffset is added to duration before truncating to int for rounding
	durationRoundingOffset = 0.5

	// outputDirPermissions is the permission mode for creating output directories
	outputDirPermissions = 0o755

	// osWindows is the GOOS value for Windows operating system
	osWindows = "windows"

	// soxResampleRate is the target sample rate for Sox spectrogram generation
	soxResampleRate = "24k"
)

// getStyleArgs returns Sox spectrogram arguments for the given style preset.
// These arguments control the visual appearance of the spectrogram.
func getStyleArgs(style string) []string {
	switch style {
	case conf.SpectrogramStyleScientificDark:
		// Grayscale with Dolph window, dark background
		return []string{"-m", "-w", "dolph"}
	case conf.SpectrogramStyleHighContrastDark:
		// High color saturation with dark background
		return []string{"-h"}
	case conf.SpectrogramStyleScientific:
		// Grayscale with Dolph window, light background (xeno-canto style)
		return []string{"-m", "-l", "-w", "dolph"}
	default:
		// Default style - no extra args (colorful with dark background)
		return nil
	}
}

// durationCacheEntry stores cached audio duration with file validation info
type durationCacheEntry struct {
	duration  float64
	timestamp time.Time
	fileSize  int64
	modTime   time.Time
}

// audioDurationCache stores audio duration lookups to avoid repeated ffprobe calls
var audioDurationCache = struct {
	sync.RWMutex
	entries map[string]*durationCacheEntry
}{
	entries: make(map[string]*durationCacheEntry),
}

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
	logger   logger.Logger
}

// NewGenerator creates a new generator instance.
// If logger is nil, GetLogger() is used to prevent nil pointer panics.
func NewGenerator(settings *conf.Settings, sfs *securefs.SecureFS, log logger.Logger) *Generator {
	if log == nil {
		log = GetLogger()
	}
	return &Generator{
		settings: settings,
		sfs:      sfs,
		logger:   log,
	}
}

// getDynamicRange returns the configured dynamic range value for Sox -z parameter.
// Returns the default value ("100") if not configured or if an invalid value is set.
func (g *Generator) getDynamicRange() string {
	dr := g.settings.Realtime.Dashboard.Spectrogram.DynamicRange
	if dr == "" {
		return defaultDynamicRange
	}
	// Validate it's one of the known presets
	switch dr {
	case conf.SpectrogramDynamicRangeHighContrast,
		conf.SpectrogramDynamicRangeStandard,
		conf.SpectrogramDynamicRangeExtended:
		return dr
	default:
		return defaultDynamicRange
	}
}

// GenerateFromFile creates a spectrogram from an audio file path.
// Used by API on-demand and user-requested modes.
// Tries Sox first (faster), falls back to FFmpeg if Sox fails.
//
// The audioPath and outputPath must be absolute paths.
// Width is in pixels, raw controls whether to show axes/legends.
//
// Context Timeout Behavior:
// This function enforces a 60-second timeout for spectrogram generation regardless
// of any parent context timeout. If the parent context has a shorter deadline, that
// will take precedence (Go's context behavior). This ensures:
//   - Long-running generations are bounded to prevent resource exhaustion
//   - Callers can still impose stricter timeouts if needed
//   - HTTP request cancellations are still respected via parent context
func (g *Generator) GenerateFromFile(ctx context.Context, audioPath, outputPath string, width int, raw bool) error {
	start := time.Now()
	g.logger.Debug("Starting spectrogram generation from file",
		logger.String("audio_path", audioPath),
		logger.String("output_path", outputPath),
		logger.Int("width", width),
		logger.Bool("raw", raw))

	// Validate inputs before filesystem operations
	if outputPath == "" {
		return errors.Newf("output path is empty").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_file").
			Build()
	}
	if !filepath.IsAbs(outputPath) {
		return errors.Newf("output path must be absolute").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_file").
			Context("output_path", outputPath).
			Build()
	}
	if width <= 0 {
		return errors.Newf("width must be positive").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_file").
			Context("width", width).
			Build()
	}

	// Ensure output directory exists using SecureFS (validates path automatically)
	if err := g.ensureOutputDirectory(outputPath); err != nil {
		return err
	}

	// Create context with timeout for Sox (see function documentation for timeout layering behavior)
	soxCtx, soxCancel := context.WithTimeout(ctx, defaultGenerationTimeout)
	defer soxCancel()

	// Try Sox first (faster, direct processing)
	if err := g.generateWithSoxFile(soxCtx, audioPath, outputPath, width, raw); err != nil {
		g.logger.Warn("Sox spectrogram generation failed, falling back to FFmpeg (style settings may not be applied)",
			logger.String("audio_path", audioPath),
			logger.Error(err),
			logger.Int64("sox_duration_ms", time.Since(start).Milliseconds()))

		// Create FRESH context for FFmpeg fallback with full timeout.
		// This ensures FFmpeg has adequate time even if Sox consumed most of the
		// original timeout before failing (e.g., killed by OOM after 55 seconds).
		// Fixes issue #1503 where FFmpeg failed with "context canceled".
		ffmpegCtx, ffmpegCancel := CreateFreshFFmpegContext(ctx)
		defer ffmpegCancel()

		// Fallback to FFmpeg pipeline with fresh context
		if ffmpegErr := g.generateWithFFmpeg(ffmpegCtx, audioPath, outputPath, width, raw); ffmpegErr != nil {
			g.logger.Error("Both Sox and FFmpeg generation failed",
				logger.String("audio_path", audioPath),
				logger.String("sox_error", err.Error()),
				logger.String("ffmpeg_error", ffmpegErr.Error()))
			return ffmpegErr
		}
	}

	g.logger.Debug("Spectrogram generation completed successfully",
		logger.String("audio_path", audioPath),
		logger.Int64("duration_ms", time.Since(start).Milliseconds()))

	return nil
}

// GenerateFromPCM creates a spectrogram from in-memory PCM data.
// Used by pre-renderer (background mode).
// PCM format: s16le, 48kHz, mono
//
// Context Timeout Behavior:
// This function enforces a 60-second timeout for spectrogram generation.
// The pre-renderer also sets its own timeout (see prerenderer.go:416), so the
// effective timeout will be whichever is shorter. This layered approach ensures
// both the pre-renderer and generator have safety limits.
func (g *Generator) GenerateFromPCM(ctx context.Context, pcmData []byte, outputPath string, width int, raw bool) error {
	start := time.Now()
	g.logger.Debug("Starting spectrogram generation from PCM",
		logger.String("output_path", outputPath),
		logger.Int("pcm_bytes", len(pcmData)),
		logger.Int("width", width),
		logger.Bool("raw", raw))

	// Validate inputs before filesystem operations
	if outputPath == "" {
		return errors.Newf("output path is empty").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Build()
	}
	if !filepath.IsAbs(outputPath) {
		return errors.Newf("output path must be absolute").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Context("output_path", outputPath).
			Build()
	}
	if width <= 0 {
		return errors.Newf("width must be positive").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Context("width", width).
			Build()
	}
	if len(pcmData) == 0 {
		return errors.Newf("PCM data is empty").
			Component("spectrogram").
			Category(errors.CategoryValidation).
			Context("operation", "generate_from_pcm").
			Build()
	}

	// Ensure output directory exists using SecureFS (validates path automatically)
	if err := g.ensureOutputDirectory(outputPath); err != nil {
		return err
	}

	// Create context with timeout (see function documentation for timeout layering behavior)
	ctx, cancel := context.WithTimeout(ctx, defaultGenerationTimeout)
	defer cancel()

	// Generate directly from PCM stdin (no FFmpeg needed)
	if err := g.generateWithSoxPCM(ctx, pcmData, outputPath, width, raw); err != nil {
		return err
	}

	g.logger.Debug("Spectrogram generation from PCM completed successfully",
		logger.String("output_path", outputPath),
		logger.Int64("duration_ms", time.Since(start).Milliseconds()))

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
	soxArgs := g.getSoxArgs(ctx, audioPath, outputPath, width, raw, SoxInputFile)

	g.logger.Debug("Executing SoX command directly",
		logger.String("sox_binary", soxBinary),
		logger.String("audio_path", audioPath),
		logger.String("output_path", outputPath))

	cmd := createCommandWithNice(ctx, soxBinary, soxArgs)

	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	// Yield to other goroutines before/after blocking on external process
	runtime.Gosched()
	if err := cmd.Run(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "generate_with_sox_direct").
			Context("audio_path", audioPath).
			Context("output_path", outputPath).
			Context("width", width).
			Context("raw", raw).
			Context("sox_output", output.String()).
			Build()
	}
	runtime.Gosched()

	return nil
}

// createCommandWithNice creates an exec.Cmd with nice wrapper on non-Windows systems.
// This reduces cognitive complexity by extracting the OS-specific command creation logic.
func createCommandWithNice(ctx context.Context, binary string, args []string) *exec.Cmd {
	if runtime.GOOS == osWindows {
		return exec.CommandContext(ctx, binary, args...) // #nosec G204 - binaries validated by exec.LookPath
	}
	return exec.CommandContext(ctx, "nice", append([]string{"-n", "19", binary}, args...)...) // #nosec G204 - binaries validated by exec.LookPath
}

// killSoxProcess kills the Sox process and logs any failure.
func (g *Generator) killSoxProcess(soxCmd *exec.Cmd, soxPid int) {
	if soxCmd.Process == nil {
		return
	}
	if killErr := soxCmd.Process.Kill(); killErr != nil {
		g.logger.Debug("Failed to kill Sox process after FFmpeg failure",
			logger.Error(killErr),
			logger.Int("sox_pid", soxPid))
	}
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

	ffmpegCmd := createCommandWithNice(ctx, ffmpegBinary, ffmpegArgs)
	soxCmd := createCommandWithNice(ctx, soxBinary, soxArgs)

	// Connect FFmpeg stdout to Sox stdin
	var err error
	soxCmd.Stdin, err = ffmpegCmd.StdoutPipe()
	if err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "create_pipe").
			Context("audio_path", audioPath).
			Context("width", width).
			Context("raw", raw).
			Build()
	}

	var ffmpegOutput, soxOutput bytes.Buffer
	ffmpegCmd.Stderr = &ffmpegOutput
	soxCmd.Stderr = &soxOutput

	// Yield to other goroutines before starting pipeline
	runtime.Gosched()

	// Start Sox first (consumer)
	if err := soxCmd.Start(); err != nil {
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "start_sox").
			Context("audio_path", audioPath).
			Context("width", width).
			Context("raw", raw).
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
			timeout := computeRemainingTimeout(ctx, soxWaitFallbackTimeout)
			g.waitWithTimeout(soxCmd, timeout)
		}
	}()

	// Run FFmpeg (producer)
	if err := ffmpegCmd.Run(); err != nil {
		g.killSoxProcess(soxCmd, soxPid)
		return errors.New(err).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "run_ffmpeg").
			Context("audio_path", audioPath).
			Context("width", width).
			Context("raw", raw).
			Context("ffmpeg_output", ffmpegOutput.String()).
			Context("sox_output", soxOutput.String()).
			Build()
	}

	// Yield after FFmpeg completes before waiting on Sox
	runtime.Gosched()

	// FFmpeg succeeded - wait for Sox to finish processing
	timeout := computeRemainingTimeout(ctx, soxWaitFallbackTimeout)
	soxWaitErr := g.waitWithTimeoutErr(soxCmd, timeout)
	soxWaitDone = true

	if soxWaitErr != nil {
		return errors.New(soxWaitErr).
			Component("spectrogram").
			Category(errors.CategorySystem).
			Context("operation", "wait_sox").
			Context("audio_path", audioPath).
			Context("width", width).
			Context("raw", raw).
			Context("ffmpeg_output", ffmpegOutput.String()).
			Context("sox_output", soxOutput.String()).
			Build()
	}
	// Yield after pipeline completes to allow other work to proceed
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
	// PCM format parameters use constants from conf package for consistency
	args := []string{
		"-t", "raw", // Input type: raw/headerless PCM
		"-r", strconv.Itoa(conf.SampleRate), // Sample rate: 48kHz
		"-e", "signed", // Encoding: signed integer
		"-b", strconv.Itoa(conf.BitDepth), // Bit depth: 16-bit
		"-c", strconv.Itoa(conf.NumChannels), // Channels: mono
		"-",                     // Read from stdin
		"-n",                    // No audio output (null output)
		"rate", soxResampleRate, // Resample to 24kHz for spectrogram
		"spectrogram",             // Effect: spectrogram
		"-x", strconv.Itoa(width), // Width in pixels
		"-y", strconv.Itoa(width / heightRatio), // Height in pixels (half of width)
		"-z", g.getDynamicRange(), // Dynamic range in dB
		"-o", outputPath, // Output PNG file
	}

	// Add raw flag if requested (no axes/legend)
	if raw {
		args = append(args, "-r")
	}

	// Add style-specific arguments
	style := g.settings.Realtime.Dashboard.Spectrogram.Style
	if styleArgs := getStyleArgs(style); styleArgs != nil {
		args = append(args, styleArgs...)
	}

	// Build command with low priority
	cmd := createCommandWithNice(ctx, soxBinary, args)

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

	height := width / heightRatio
	var filterStr string
	if raw {
		// Raw spectrogram without frequency/time axes and legends
		filterStr = fmt.Sprintf("showspectrumpic=s=%dx%d:legend=0:gain=%s:drange=%s", width, height, ffmpegGain, ffmpegDrange)
	} else {
		// Standard spectrogram with frequency/time axes and legends
		filterStr = fmt.Sprintf("showspectrumpic=s=%dx%d:legend=1:gain=%s:drange=%s", width, height, ffmpegGain, ffmpegDrange)
	}

	ffmpegArgs := []string{
		"-hide_banner",
		"-y",
		"-i", audioPath,
		"-lavfi", filterStr,
		"-frames:v", "1",
		outputPath,
	}

	cmd := createCommandWithNice(ctx, ffmpegBinary, ffmpegArgs)

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
			Context("width", width).
			Context("raw", raw).
			Context("ffmpeg_output", output.String()).
			Build()
	}

	return nil
}

// getSoxArgs builds Sox arguments for file input.
// Used when Sox can directly read the audio file.
func (g *Generator) getSoxArgs(ctx context.Context, audioPath, outputPath string, width int, raw bool, inputType SoxInputType) []string {
	args := make([]string, 0, 16) // Preallocate capacity for input path + spectrogram args
	if inputType == SoxInputFile {
		args = append(args, audioPath)
	}

	args = append(args, g.getSoxSpectrogramArgs(ctx, audioPath, outputPath, width, raw)...)
	return args
}

// getSoxSpectrogramArgs returns the common Sox arguments for spectrogram generation.
//
// The -d (duration) parameter must always be explicitly provided to Sox. Without it,
// Sox interprets the -x (width in pixels) as seconds of audio time, causing spectrograms
// to show truncated audio durations (see issue #1484).
func (g *Generator) getSoxSpectrogramArgs(ctx context.Context, audioPath, outputPath string, width int, raw bool) []string {
	heightStr := strconv.Itoa(width / heightRatio)
	widthStr := strconv.Itoa(width)

	// Build base args without duration parameter
	args := []string{"-n", "rate", soxResampleRate, "spectrogram", "-x", widthStr, "-y", heightStr}

	// Always provide explicit duration via -d parameter to ensure spectrogram
	// shows the full audio duration regardless of image width (fixes #1484)
	duration := getCachedAudioDuration(ctx, audioPath)
	if duration <= 0 {
		// Fallback: Use configured capture length if ffprobe fails
		captureLength := g.settings.Realtime.Audio.Export.Length
		duration = float64(captureLength)
		g.logger.Warn("FFprobe failed, using configured fallback duration",
			logger.Float64("fallback_duration_seconds", duration),
			logger.String("audio_path", audioPath))
	}

	// Convert duration to string, rounding to nearest integer
	captureLengthStr := strconv.Itoa(int(duration + durationRoundingOffset))

	// Add duration and remaining common parameters
	args = append(args, "-d", captureLengthStr, "-z", g.getDynamicRange(), "-o", outputPath)

	// Add raw flag if requested (no axes/legends)
	if raw {
		args = append(args, "-r")
	}

	// Add style-specific arguments
	style := g.settings.Realtime.Dashboard.Spectrogram.Style
	if styleArgs := getStyleArgs(style); styleArgs != nil {
		args = append(args, styleArgs...)
	}

	return args
}

// ensureOutputDirectory creates the output directory if it doesn't exist.
// Uses SecureFS for path validation.
func (g *Generator) ensureOutputDirectory(outputPath string) error {
	outputDir := filepath.Dir(outputPath)
	if err := g.sfs.MkdirAll(outputDir, outputDirPermissions); err != nil {
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

// computeRemainingTimeout computes the remaining time until context deadline.
// If ctx has no deadline or remaining time is <= 0, returns the fallback duration.
// This ensures cleanup operations respect the caller's timeout constraints.
func computeRemainingTimeout(ctx context.Context, fallback time.Duration) time.Duration {
	if deadline, ok := ctx.Deadline(); ok {
		remaining := time.Until(deadline)
		if remaining > 0 {
			return remaining
		}
	}
	return fallback
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
				logger.Int("pid", pid),
				logger.Error(err))
		}
	case <-time.After(timeout):
		g.logger.Warn("Process wait timed out",
			logger.Int("pid", pid),
			logger.Float64("timeout_seconds", timeout.Seconds()))
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			select {
			case <-done:
			case <-time.After(1 * time.Second):
				g.logger.Error("Failed to reap process after kill",
					logger.Int("pid", pid))
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
			logger.Int("pid", pid),
			logger.Float64("timeout_seconds", timeout.Seconds()))
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

// getCachedAudioDuration retrieves audio duration from cache or fetches it using ffprobe.
// The cache is invalidated if the file has been modified (size or modTime changed).
// Returns 0 if duration cannot be determined (caller should use configured fallback).
func getCachedAudioDuration(ctx context.Context, audioPath string) float64 {
	// Get file info for cache validation
	fileInfo, err := os.Stat(audioPath)
	if err != nil {
		return 0
	}

	cacheKey := audioPath
	currentSize := fileInfo.Size()
	currentModTime := fileInfo.ModTime()

	// Check cache with read lock
	audioDurationCache.RLock()
	entry, exists := audioDurationCache.entries[cacheKey]
	audioDurationCache.RUnlock()

	if exists {
		// Validate cache entry
		age := time.Since(entry.timestamp)
		if age < durationCacheTTL &&
			entry.fileSize == currentSize &&
			entry.modTime.Equal(currentModTime) {
			return entry.duration
		}
	}

	// Cache miss or invalid - fetch duration via ffprobe
	duration, err := getAudioDurationViaFFprobe(ctx, audioPath)
	if err != nil {
		return 0 // Caller will use configured fallback
	}

	// Store in cache with write lock, evicting old entries if needed
	audioDurationCache.Lock()
	evictOldCacheEntriesLocked()
	audioDurationCache.entries[cacheKey] = &durationCacheEntry{
		duration:  duration,
		timestamp: time.Now(),
		fileSize:  currentSize,
		modTime:   currentModTime,
	}
	audioDurationCache.Unlock()

	return duration
}

// evictOldCacheEntriesLocked removes oldest entries when cache exceeds maxCacheEntries.
// Must be called while holding audioDurationCache.Lock().
// This prevents unbounded memory growth during long operation periods (fixes #1503).
func evictOldCacheEntriesLocked() {
	if len(audioDurationCache.entries) < maxCacheEntries {
		return
	}

	// Find and remove oldest entries (by timestamp) until we're under the limit
	// Remove cacheEvictionPercent% of entries to avoid frequent eviction
	entriesToRemove := len(audioDurationCache.entries) - maxCacheEntries + maxCacheEntries/cacheEvictionPercent

	// Find the oldest entries
	type keyTime struct {
		key       string
		timestamp time.Time
	}
	entries := make([]keyTime, 0, len(audioDurationCache.entries))
	for k, v := range audioDurationCache.entries {
		entries = append(entries, keyTime{k, v.timestamp})
	}

	// Sort by timestamp (oldest first) using O(n log n) algorithm
	slices.SortFunc(entries, func(a, b keyTime) int {
		return a.timestamp.Compare(b.timestamp)
	})

	// Remove oldest entries
	for i := 0; i < entriesToRemove && i < len(entries); i++ {
		delete(audioDurationCache.entries, entries[i].key)
	}
}

// getAudioDurationViaFFprobe calls ffprobe to get audio duration.
// Uses ffprobe directly to avoid circular dependency with myaudio package.
func getAudioDurationViaFFprobe(ctx context.Context, audioPath string) (float64, error) {
	ffprobePath := "ffprobe"

	cmd := exec.CommandContext(ctx, ffprobePath,
		"-v", "error",
		"-show_entries", "format=duration",
		"-of", "default=noprint_wrappers=1:nokey=1",
		audioPath)

	output, err := cmd.Output()
	if err != nil {
		return 0, fmt.Errorf("ffprobe failed: %w", err)
	}

	var duration float64
	if _, err := fmt.Sscanf(string(output), "%f", &duration); err != nil {
		return 0, fmt.Errorf("failed to parse duration: %w", err)
	}

	return duration, nil
}

// GetSoxSpectrogramArgsForTest exposes getSoxSpectrogramArgs for testing.
// This method is exported to allow tests in other packages to verify the
// FFmpeg version optimization logic.
func (g *Generator) GetSoxSpectrogramArgsForTest(ctx context.Context, audioPath, outputPath string, width int, raw bool) []string {
	return g.getSoxSpectrogramArgs(ctx, audioPath, outputPath, width, raw)
}

// CreateFreshFFmpegContext creates a new context for FFmpeg fallback with full timeout.
// This ensures FFmpeg has adequate time even when the parent context (used by Sox)
// is nearly exhausted or cancelled.
//
// IMPORTANT TRADEOFF: This function intentionally uses context.Background() as the
// parent, which means FFmpeg will NOT respect parent context cancellation (e.g., HTTP
// request cancellation or server shutdown signals). This is an intentional design
// decision to solve issue #1503 where FFmpeg failed with "context canceled" after
// Sox was killed by OOM - in that scenario, giving FFmpeg a fair chance to succeed
// is more valuable than immediate cancellation responsiveness.
//
// The ffmpegFallbackTimeout (60s) provides an upper bound on how long FFmpeg can run.
func CreateFreshFFmpegContext(_ context.Context) (context.Context, context.CancelFunc) {
	// Use Background() to avoid inheriting cancellation from parent.
	// This is intentional: when Sox fails (possibly due to OOM/kill),
	// we want FFmpeg to have a fair chance with its own timeout.
	return context.WithTimeout(context.Background(), ffmpegFallbackTimeout)
}
