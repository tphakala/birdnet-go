package myaudio

import (
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// TempExt is the temporary file extension used when exporting audio with FFmpeg.
// Audio files are written with this suffix during recording and renamed upon
// completion to ensure atomic file operations.
const TempExt = ".temp"

// Audio format constants for FFmpeg export operations.
const (
	FormatAAC  = "aac"
	FormatFLAC = "flac"
	FormatALAC = "alac"
	FormatOpus = "opus"
	FormatMP3  = "mp3"
)

// ExportAudioWithFFmpeg exports PCM data to the specified format using FFmpeg
// outputPath is full path with audio file name and extension based on format
// pcmData is the PCM data to export
func ExportAudioWithFFmpeg(pcmData []byte, outputPath string, settings *conf.AudioSettings) error {
	start := time.Now()

	// Validate inputs
	if settings == nil {
		enhancedErr := errors.Newf("audio settings parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "export_audio_ffmpeg").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", "unknown", "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", "unknown", "nil_settings")
		}
		return enhancedErr
	}

	if settings.FfmpegPath == "" {
		enhancedErr := errors.Newf("FFmpeg path is not configured or invalid").
			Component("myaudio").
			Category(errors.CategoryConfiguration).
			Context("operation", "export_audio_ffmpeg").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", "unknown", "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", "unknown", "missing_ffmpeg_path")
		}
		return enhancedErr
	}

	if outputPath == "" {
		enhancedErr := errors.Newf("empty output path provided").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "export_audio_ffmpeg").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", "unknown", "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", "unknown", "empty_output_path")
		}
		return enhancedErr
	}

	if len(pcmData) == 0 {
		enhancedErr := errors.Newf("empty PCM data provided for export").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "export_audio_ffmpeg").
			Context("data_size", 0).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", settings.Export.Type, "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", settings.Export.Type, "empty_data")
		}
		return enhancedErr
	}

	// Debug: Log PCM data statistics before export to diagnose audio corruption
	if settings.Export.Debug {
		log := GetLogger()
		isAligned := len(pcmData)%2 == 0 // 16-bit alignment check

		// Calculate basic PCM statistics
		var maxSample, minSample int16
		var sumAbsSamples int64
		numSamples := len(pcmData) / 2
		if numSamples > 0 {
			// Initialize min/max with first sample to handle all-positive or all-negative audio
			firstSample := int16(pcmData[0]) | int16(pcmData[1])<<8
			maxSample, minSample = firstSample, firstSample

			for i := 0; i < len(pcmData)-1; i += 2 {
				sample := int16(pcmData[i]) | int16(pcmData[i+1])<<8
				if sample > maxSample {
					maxSample = sample
				}
				if sample < minSample {
					minSample = sample
				}
				if sample < 0 {
					sumAbsSamples += int64(-sample)
				} else {
					sumAbsSamples += int64(sample)
				}
			}
		}
		avgAbsSample := int64(0)
		if numSamples > 0 {
			avgAbsSample = sumAbsSamples / int64(numSamples)
		}

		// Get first 10 samples for inspection
		firstSamples := make([]int16, 0, 10)
		for i := 0; i < min(20, len(pcmData)-1); i += 2 {
			sample := int16(pcmData[i]) | int16(pcmData[i+1])<<8
			firstSamples = append(firstSamples, sample)
		}

		log.Debug("PCM data before FFmpeg export",
			logger.Int("pcm_size_bytes", len(pcmData)),
			logger.Int("num_samples", numSamples),
			logger.Bool("size_aligned", isAligned),
			logger.Int("max_sample", int(maxSample)),
			logger.Int("min_sample", int(minSample)),
			logger.Int64("avg_abs_sample", avgAbsSample),
			logger.String("first_10_samples", fmt.Sprintf("%v", firstSamples)),
			logger.String("output_path", outputPath),
			logger.String("format", settings.Export.Type))

		// Warn if audio appears to be silence or noise
		if avgAbsSample < 100 {
			log.Warn("PCM data appears to be near-silence",
				logger.Int64("avg_abs_sample", avgAbsSample),
				logger.String("output_path", outputPath))
		} else if avgAbsSample > 20000 {
			log.Warn("PCM data has unusually high amplitude - possible noise/corruption",
				logger.Int64("avg_abs_sample", avgAbsSample),
				logger.Int("max_sample", int(maxSample)),
				logger.String("output_path", outputPath))
		}
	}

	// Create a temporary file for FFmpeg output, returns full path with TempExt
	// temporary file is used to perform export as atomic file operation
	tempFilePath, err := createTempFile(outputPath)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "export_audio_ffmpeg").
			Context("file_operation", "create_temp_file").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", settings.Export.Type, "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", settings.Export.Type, "temp_file_creation_failed")
		}
		return enhancedErr
	}

	// Run the FFmpeg command to process the audio
	if err := runFFmpegCommand(settings.FfmpegPath, pcmData, tempFilePath, settings); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "export_audio_ffmpeg").
			Context("file_operation", "run_ffmpeg_command").
			Context("export_type", settings.Export.Type).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", settings.Export.Type, "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", settings.Export.Type, "ffmpeg_command_failed")
		}
		return enhancedErr
	}

	// Finalize the output by renaming the temporary file to the final audio file
	if err := finalizeOutput(tempFilePath); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "export_audio_ffmpeg").
			Context("file_operation", "finalize_output").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_ffmpeg", settings.Export.Type, "error")
			fileMetrics.RecordFileOperationError("export_ffmpeg", settings.Export.Type, "finalize_failed")
		}
		return enhancedErr
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("export_ffmpeg", settings.Export.Type, "success")
		fileMetrics.RecordFileOperationDuration("export_ffmpeg", settings.Export.Type, duration)
		fileMetrics.RecordFileSize("export_ffmpeg", settings.Export.Type, int64(len(pcmData)))
	}

	return nil
}

// createTempFile creates a temporary file path for FFmpeg output
func createTempFile(outputPath string) (string, error) {
	start := time.Now()

	// Validate input
	if outputPath == "" {
		enhancedErr := errors.Newf("empty output path provided for temp file creation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "create_temp_file").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("create_temp", "unknown", "error")
			fileMetrics.RecordFileOperationError("create_temp", "unknown", "empty_path")
		}
		return "", enhancedErr
	}

	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, 0o750); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "create_temp_file").
			Context("file_operation", "create_directories").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("create_temp", "temp", "error")
			fileMetrics.RecordFileOperationError("create_temp", "temp", "directory_creation_failed")
		}
		return "", enhancedErr
	}

	tempFilePath := outputPath + TempExt

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("create_temp", "temp", "success")
		fileMetrics.RecordFileOperationDuration("create_temp", "temp", duration)
	}

	return tempFilePath, nil
}

// finalizeOutput path removes TempExt from the file name completing atomic file operation
func finalizeOutput(tempFilePath string) error {
	start := time.Now()

	// Validate input
	if tempFilePath == "" {
		enhancedErr := errors.Newf("empty temp file path provided for finalize operation").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "finalize_output").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("finalize_output", "unknown", "error")
			fileMetrics.RecordFileOperationError("finalize_output", "unknown", "empty_path")
		}
		return enhancedErr
	}

	if !strings.HasSuffix(tempFilePath, TempExt) {
		enhancedErr := errors.Newf("temp file path does not have expected temporary extension: %s", TempExt).
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "finalize_output").
			Context("expected_extension", TempExt).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("finalize_output", "temp", "error")
			fileMetrics.RecordFileOperationError("finalize_output", "temp", "invalid_temp_extension")
		}
		return enhancedErr
	}

	// Strip TempExt from the end of the path
	finalOutputPath := tempFilePath[:len(tempFilePath)-len(TempExt)]

	// Get file format from final output path
	format := strings.ToLower(filepath.Ext(finalOutputPath))
	if format != "" {
		format = format[1:] // Remove the dot
	} else {
		format = "unknown"
	}

	// Rename the temporary file to the final output path
	if err := os.Rename(tempFilePath, finalOutputPath); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategoryFileIO).
			Context("operation", "finalize_output").
			Context("file_operation", "rename_file").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("finalize_output", format, "error")
			fileMetrics.RecordFileOperationError("finalize_output", format, "rename_failed")
		}
		return enhancedErr
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("finalize_output", format, "success")
		fileMetrics.RecordFileOperationDuration("finalize_output", format, duration)
	}

	return nil
}

// runFFmpegCommand executes the FFmpeg command to process the audio
// This version includes a context timeout to prevent hangs.
func runFFmpegCommand(ffmpegPath string, pcmData []byte, tempFilePath string, settings *conf.AudioSettings) error {
	log := GetLogger()
	// Build the FFmpeg command arguments
	args := buildFFmpegArgs(tempFilePath, settings)

	// Create a context with a timeout (e.g., 30 seconds)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	// Create the FFmpeg command with context
	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath is from validated settings, args built internally

	// Create a pipe to send PCM data to FFmpeg's stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Capture stderr for error reporting
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start the FFmpeg command
	if err := cmd.Start(); err != nil {
		// Check if the error is due to context cancellation
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("failed to start FFmpeg (timeout): %w", ctx.Err())
		}
		return fmt.Errorf("failed to start FFmpeg: %w, stderr: %s", err, stderr.String())
	}

	// Write PCM data to FFmpeg's stdin in a separate goroutine
	writeErrChan := make(chan error, 1)
	go func() {
		defer func() {
			if err := stdin.Close(); err != nil {
				log.Warn("failed to close FFmpeg stdin",
					logger.Error(err),
					logger.String("output_path", tempFilePath))
			}
		}() // Close stdin when writing is done

		// Check if context is already done before writing
		select {
		case <-ctx.Done():
			writeErrChan <- ctx.Err()
			return
		default:
			// Continue with writing
		}

		_, writeErr := stdin.Write(pcmData)
		writeErrChan <- writeErr
	}()

	// Wait for the write to complete or for context to be cancelled
	select {
	case writeErr := <-writeErrChan:
		if writeErr != nil {
			// Attempt to kill the process if write failed mid-way
			_ = cmd.Process.Kill()
			_ = cmd.Wait() // Clean up resources
			return fmt.Errorf("failed to write PCM data to FFmpeg: %w, stderr: %s", writeErr, stderr.String())
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		return fmt.Errorf("context cancelled during write: %w", ctx.Err())
	}

	// Wait for FFmpeg to finish processing
	if err := cmd.Wait(); err != nil {
		// Check if the error is due to context cancellation
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return fmt.Errorf("FFmpeg command timed out: %w", ctx.Err())
		}
		return fmt.Errorf("FFmpeg command failed: %w, stderr: %s", err, stderr.String())
	}

	// Return nil if everything succeeded
	return nil
}

// buildFFmpegArgs constructs the arguments for the FFmpeg command
func buildFFmpegArgs(tempFilePath string, settings *conf.AudioSettings) []string {
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	outputEncoder := getEncoder(settings.Export.Type)
	outputFormat := getOutputFormat(settings.Export.Type)
	outputBitrate := getMaxBitrate(settings.Export.Type, settings.Export.Bitrate)

	args := []string{
		"-hide_banner",     // Suppress FFmpeg banner output for cleaner logs
		"-f", ffmpegFormat, // Input format based on bit depth
		"-ar", ffmpegSampleRate, // Sample rate
		"-ac", ffmpegNumChannels, // Number of channels
		"-i", "-", // Read from stdin
	}

	// Add audio filters for normalization or gain
	audioFilter := buildAudioFilter(settings)
	if audioFilter != "" {
		args = append(args, "-af", audioFilter)
	}

	// Add output encoding settings
	args = append(args,
		"-c:a", outputEncoder,
		"-b:a", outputBitrate,
		"-f", outputFormat, // Specify the output format
		"-y",         // Overwrite output file if it exists
		tempFilePath, // Write to the temporary file
	)

	return args
}

// buildAudioFilter constructs the audio filter string for FFmpeg
func buildAudioFilter(settings *conf.AudioSettings) string {
	// Normalization takes precedence over gain
	if settings.Export.Normalization.Enabled {
		// Use loudnorm filter for EBU R128 normalization
		// Format: loudnorm=I=target:TP=truepeak:LRA=range
		return fmt.Sprintf("loudnorm=I=%.1f:TP=%.1f:LRA=%.1f",
			settings.Export.Normalization.TargetLUFS,
			settings.Export.Normalization.TruePeak,
			settings.Export.Normalization.LoudnessRange)
	}

	// Apply simple gain if specified (and normalization is disabled)
	if settings.Export.Gain != 0 {
		// Use volume filter for gain adjustment
		// Format: volume=+6dB or volume=-6dB
		if settings.Export.Gain > 0 {
			return fmt.Sprintf("volume=+%.1fdB", settings.Export.Gain)
		}
		return fmt.Sprintf("volume=%.1fdB", settings.Export.Gain) // Negative sign already included
	}

	return "" // No audio filtering needed
}

// getCodec returns the appropriate codec to use with FFmpeg based on the format
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

// getOutputFormat returns the appropriate output format for FFmpeg based on the export type
func getOutputFormat(exportType string) string {
	switch exportType {
	case FormatFLAC:
		return FormatFLAC
	case FormatALAC:
		return "ipod" // ALAC uses the iPod container format
	case FormatOpus:
		return FormatOpus
	case FormatAAC:
		return "mp4" // AAC typically uses the iPod/MP4 container format
	case FormatMP3:
		return FormatMP3
	default:
		return exportType
	}
}

// getMaxBitrate limits the bitrate to the maximum allowed by the format
func getMaxBitrate(format, requestedBitrate string) string {
	switch format {
	case FormatOpus:
		if requestedBitrate > "256k" {
			return "256k"
		}
	case FormatMP3:
		if requestedBitrate > "320k" {
			return "320k"
		}
	}
	return requestedBitrate
}

// ExportAudioWithCustomFFmpegArgs exports PCM data using FFmpeg with custom arguments directly to a memory buffer.
// This avoids writing temporary files to disk.
// ffmpegPath is the path to the FFmpeg executable.
// customArgs is a slice of strings representing additional FFmpeg arguments (including output format/codec).
func ExportAudioWithCustomFFmpegArgs(pcmData []byte, ffmpegPath string, customArgs []string) (*bytes.Buffer, error) {
	// Call the context-aware version with a background context
	return ExportAudioWithCustomFFmpegArgsContext(context.Background(), pcmData, ffmpegPath, customArgs)
}

// runCustomFFmpegCommandToBuffer executes FFmpeg, piping PCM input and capturing codec output to a buffer.
//
// Deprecated: Prefer runCustomFFmpegCommandToBufferWithContext for cancellation/timeout control.
func runCustomFFmpegCommandToBuffer(ffmpegPath string, pcmData []byte, customArgs []string) (*bytes.Buffer, error) {
	// Call the context-aware version with a background context
	return runCustomFFmpegCommandToBufferWithContext(context.Background(), ffmpegPath, pcmData, customArgs)
}

// ExportAudioWithCustomFFmpegArgsContext exports PCM data using FFmpeg with custom arguments directly to a memory buffer.
// This is the context-aware version of ExportAudioWithCustomFFmpegArgs that allows timeout/cancellation.
// ffmpegPath is the path to the FFmpeg executable.
// customArgs is a slice of strings representing additional FFmpeg arguments (including output format/codec).
func ExportAudioWithCustomFFmpegArgsContext(ctx context.Context, pcmData []byte, ffmpegPath string, customArgs []string) (*bytes.Buffer, error) {
	start := time.Now()

	// Validate inputs
	if ctx == nil {
		enhancedErr := errors.Newf("context parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "export_custom_ffmpeg_context").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_custom_ffmpeg", "unknown", "error")
			fileMetrics.RecordFileOperationError("export_custom_ffmpeg", "unknown", "nil_context")
		}
		return nil, enhancedErr
	}

	if ffmpegPath == "" {
		enhancedErr := errors.Newf("FFmpeg path provided is empty").
			Component("myaudio").
			Category(errors.CategoryConfiguration).
			Context("operation", "export_custom_ffmpeg_context").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_custom_ffmpeg", "unknown", "error")
			fileMetrics.RecordFileOperationError("export_custom_ffmpeg", "unknown", "empty_ffmpeg_path")
		}
		return nil, enhancedErr
	}

	if len(pcmData) == 0 {
		enhancedErr := errors.Newf("empty PCM data provided for custom FFmpeg export").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "export_custom_ffmpeg_context").
			Context("data_size", 0).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_custom_ffmpeg", "custom", "error")
			fileMetrics.RecordFileOperationError("export_custom_ffmpeg", "custom", "empty_data")
		}
		return nil, enhancedErr
	}

	if len(customArgs) == 0 {
		enhancedErr := errors.Newf("empty custom arguments provided for FFmpeg export").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "export_custom_ffmpeg_context").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_custom_ffmpeg", "custom", "error")
			fileMetrics.RecordFileOperationError("export_custom_ffmpeg", "custom", "empty_args")
		}
		return nil, enhancedErr
	}

	// Run the FFmpeg command, capturing output to a buffer
	outputBuffer, err := runCustomFFmpegCommandToBufferWithContext(ctx, ffmpegPath, pcmData, customArgs)
	if err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "export_custom_ffmpeg_context").
			Context("custom_args_count", len(customArgs)).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("export_custom_ffmpeg", "custom", "error")
			fileMetrics.RecordFileOperationError("export_custom_ffmpeg", "custom", "ffmpeg_execution_failed")
		}
		return nil, enhancedErr
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("export_custom_ffmpeg", "custom", "success")
		fileMetrics.RecordFileOperationDuration("export_custom_ffmpeg", "custom", duration)
		fileMetrics.RecordFileSize("export_custom_ffmpeg", "custom", int64(len(pcmData)))
	}

	// Return the buffer containing the exported audio data
	return outputBuffer, nil
}

// runCustomFFmpegCommandToBufferWithContext executes FFmpeg, piping PCM input and capturing codec output to a buffer.
// This version accepts a context to allow for timeout/cancellation.
func runCustomFFmpegCommandToBufferWithContext(ctx context.Context, ffmpegPath string, pcmData []byte, customArgs []string) (*bytes.Buffer, error) {
	log := GetLogger()
	// Get standard input format arguments
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Build the base arguments for PCM input from stdin with preallocated capacity
	args := make([]string, 0, 9+len(customArgs)+1)
	args = append(args,
		"-hide_banner",     // Suppress FFmpeg banner output for cleaner logs
		"-f", ffmpegFormat, // Input format based on bit depth
		"-ar", ffmpegSampleRate, // Sample rate
		"-ac", ffmpegNumChannels, // Number of channels
		"-i", "-", // Read from stdin
	)

	// Append the custom arguments provided by the caller (should include codec, filters, format)
	args = append(args, customArgs...)

	// Append the output destination: pipe:1 (stdout)
	args = append(args, "pipe:1")

	// Create the FFmpeg command with context
	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath is from validated settings, args built internally

	// Create pipes for stdin and stdout
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdout pipe: %w", err)
	}

	// Capture stderr for better error reporting
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start the FFmpeg command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg: %w, stderr: %s", err, stderr.String())
	}

	// Use a separate goroutine to write PCM data to prevent blocking
	// and capture potential write errors
	writeErrChan := make(chan error, 1)
	go func() {
		defer func() {
			if err := stdin.Close(); err != nil {
				log.Warn("failed to close FFmpeg stdin",
					logger.Error(err),
					logger.String("operation", "custom_ffmpeg_to_buffer"))
			}
		}() // Close stdin when writing is done

		// Check if context is already done before writing
		select {
		case <-ctx.Done():
			writeErrChan <- ctx.Err()
			return
		default:
			// Continue with writing
		}

		_, writeErr := stdin.Write(pcmData)
		writeErrChan <- writeErr
	}()

	// Read stdout into a buffer
	outputBuffer := bytes.NewBuffer(nil)
	readDoneChan := make(chan error, 1)
	go func() {
		_, readErr := io.Copy(outputBuffer, stdout)
		readDoneChan <- readErr
	}()

	// Wait for both writing and reading to complete or for context to be cancelled
	var writeErr, readErr error
	select {
	case writeErr = <-writeErrChan:
		// Writing completed, now wait for reading to complete or context to be cancelled
		select {
		case readErr = <-readDoneChan:
			// Reading has also completed
		case <-ctx.Done():
			_ = cmd.Process.Kill() // best-effort kill
			_ = cmd.Wait()         // reap and free resources
			return nil, ctx.Err()
		}
	case <-ctx.Done():
		_ = cmd.Process.Kill() // best-effort kill
		_ = cmd.Wait()         // reap and free resources
		return nil, ctx.Err()
	}

	// Check for write error
	if writeErr != nil {
		return nil, fmt.Errorf("failed to write PCM data to FFmpeg: %w", writeErr)
	}

	// Check for read error
	if readErr != nil {
		return nil, fmt.Errorf("failed to read FFmpeg output: %w", readErr)
	}

	// Wait for FFmpeg to finish processing
	if err := cmd.Wait(); err != nil {
		// Check if the error is due to context cancellation
		if ctx.Err() != nil {
			// Context was cancelled *after* I/O completed but before Wait finished
			// Process might already be terminated by Kill in select block, but Wait still needed
			return nil, ctx.Err()
		}
		return nil, fmt.Errorf("FFmpeg failed: %w, stderr: %s", err, stderr.String())
	}

	// Return the buffer containing the exported audio data
	return outputBuffer, nil
}

// validateFFmpegPathInternal checks if the provided FFmpeg path is valid and executable.
// If ffmpegPath is empty, it checks if "ffmpeg" is available in the system PATH.
/* func validateFFmpegPathInternal(ffmpegPath string) error {
	if ffmpegPath == "" {
		// If no path provided, check if "ffmpeg" is in the system PATH
		if _, err := exec.LookPath("ffmpeg"); err != nil {
			return fmt.Errorf("FFmpeg path is not configured and 'ffmpeg' executable not found in system PATH: %w", err)
		}
		// Found in PATH, valid configuration
		return nil
	}

	// If a path is provided, validate it specifically
	// Check if the file exists at the specified path
	if _, err := os.Stat(ffmpegPath); os.IsNotExist(err) {
		// If not found at the specified path, check if the name exists in PATH (e.g., user provided just "ffmpeg")
		if _, pathErr := exec.LookPath(ffmpegPath); pathErr != nil {
			// Not found at the path AND not found in system PATH
			return fmt.Errorf("FFmpeg not found at specified path '%s' or in system PATH: %w", ffmpegPath, err)
		}
		// Found in PATH (even though not at the literal path), consider it valid
		return nil
	} else if err != nil {
		// Another error occurred during stat (e.g., permission denied)
		return fmt.Errorf("error accessing specified FFmpeg path '%s': %w", ffmpegPath, err)
	}

	// Path exists and is accessible
	return nil
} */

// LoudnessStats holds the measured loudness statistics from FFmpeg's loudnorm filter.
type LoudnessStats struct {
	InputI            string `json:"input_i"`
	InputTP           string `json:"input_tp"`
	InputLRA          string `json:"input_lra"`
	InputThresh       string `json:"input_thresh"`
	OutputI           string `json:"output_i"`      // Not used for 2-pass, but part of JSON
	OutputTP          string `json:"output_tp"`     // Not used for 2-pass
	OutputLRA         string `json:"output_lra"`    // Not used for 2-pass
	OutputThresh      string `json:"output_thresh"` // Not used for 2-pass
	NormalizationType string `json:"normalization_type"`
	TargetOffset      string `json:"target_offset"` // Not used for 2-pass
}

// AnalyzeAudioLoudness runs the first pass of FFmpeg's loudnorm filter to get audio statistics.
//
// Deprecated: Prefer AnalyzeAudioLoudnessWithContext for cancellation/timeout control.
func AnalyzeAudioLoudness(pcmData []byte, ffmpegPath string) (*LoudnessStats, error) {
	// Call the context-aware version with a background context
	return AnalyzeAudioLoudnessWithContext(context.Background(), pcmData, ffmpegPath)
}

// AnalyzeAudioLoudnessWithContext analyzes audio loudness using FFmpeg's loudnorm filter in analyze mode
// This is the context-aware version of AnalyzeAudioLoudness that allows timeout/cancellation
func AnalyzeAudioLoudnessWithContext(ctx context.Context, pcmData []byte, ffmpegPath string) (*LoudnessStats, error) {
	log := GetLogger()
	// Assume ffmpegPath is valid (validated by caller, usually via conf.ValidateAudioSettings)
	if ffmpegPath == "" {
		return nil, fmt.Errorf("FFmpeg path provided is empty")
	}

	// Get standard input format arguments
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Build the FFmpeg command with loudnorm filter in print_format=json mode
	args := []string{
		"-hide_banner",     // Suppress FFmpeg banner output for cleaner logs
		"-f", ffmpegFormat, // Input format based on bit depth
		"-ar", ffmpegSampleRate, // Sample rate
		"-ac", ffmpegNumChannels, // Number of channels
		"-i", "-", // Read from stdin
		"-af", "loudnorm=I=-23:LRA=7:TP=-2:print_format=json", // Loudnorm analysis
		"-f", "null", // Output to null device
		"-", // Output to stdout (null device doesn't create any actual output)
	}

	// Create the FFmpeg command with context
	cmd := exec.CommandContext(ctx, ffmpegPath, args...) //nolint:gosec // G204: ffmpegPath is from validated settings, args built internally

	// Create a pipe to write PCM data to FFmpeg's stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Capture stderr for JSON output from loudnorm filter
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start the FFmpeg command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Write PCM data to FFmpeg's stdin in a separate goroutine to prevent blocking
	// while waiting for stderr to capture output
	writeErrChan := make(chan error, 1)
	go func() {
		defer func() {
			if err := stdin.Close(); err != nil {
				log.Warn("failed to close FFmpeg stdin",
					logger.Error(err),
					logger.String("operation", "analyze_loudness"))
			}
		}() // Close stdin when done

		// Check if context is already done before writing
		select {
		case <-ctx.Done():
			writeErrChan <- ctx.Err()
			return
		default:
			// Continue with writing
		}

		_, writeErr := stdin.Write(pcmData)
		writeErrChan <- writeErr
	}()

	// Wait for the write to complete or for context to be cancelled
	select {
	case writeErr := <-writeErrChan:
		if writeErr != nil {
			return nil, fmt.Errorf("failed to write PCM data to FFmpeg: %w", writeErr)
		}
	case <-ctx.Done():
		return nil, ctx.Err()
	}

	// Wait for FFmpeg to finish processing
	if err := cmd.Wait(); err != nil {
		// Check if the error is due to context cancellation
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		// The loudnorm filter ends with an error (code 1) because it doesn't output anything to stdout,
		// which is expected, and we capture the meaningful output from stderr
		// We'll continue parsing the output from stderr instead of returning an error
	}

	// Extract the JSON from the stderr output
	stderrStr := stderr.String()
	jsonStartIdx := strings.Index(stderrStr, "{")
	jsonEndIdx := strings.LastIndex(stderrStr, "}")
	if jsonStartIdx == -1 || jsonEndIdx == -1 || jsonEndIdx < jsonStartIdx {
		return nil, fmt.Errorf("failed to extract JSON from FFmpeg output: %s", stderrStr)
	}
	jsonStr := stderrStr[jsonStartIdx : jsonEndIdx+1]

	// Parse the JSON output
	var stats LoudnessStats
	if err := json.Unmarshal([]byte(jsonStr), &stats); err != nil {
		return nil, fmt.Errorf("failed to parse FFmpeg loudnorm analysis: %w", err)
	}

	return &stats, nil
}

// EncodePCMtoWAVWithContext encodes PCM data in WAV format using context for cancellation/timeout
func EncodePCMtoWAVWithContext(ctx context.Context, pcmData []byte) (*bytes.Buffer, error) {
	start := time.Now()

	// Validate inputs
	if ctx == nil {
		enhancedErr := errors.Newf("context parameter is nil").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "encode_pcm_to_wav_context").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("encode_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("encode_wav", "wav", "nil_context")
		}
		return nil, enhancedErr
	}

	if len(pcmData) == 0 {
		enhancedErr := errors.Newf("PCM data is empty for WAV encoding").
			Component("myaudio").
			Category(errors.CategoryValidation).
			Context("operation", "encode_pcm_to_wav_context").
			Context("data_size", 0).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("encode_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("encode_wav", "wav", "empty_data")
		}
		return nil, enhancedErr
	}

	// Constants for WAV format
	const bitDepth = conf.BitDepth       // Bits per sample
	const sampleRate = conf.SampleRate   // Sample rate
	const numChannels = conf.NumChannels // Mono audio

	// Calculating sizes and rates
	byteRate := sampleRate * numChannels * (bitDepth / 8) // 48000 * 1 * 2 = 96000 bytes per second
	blockAlign := numChannels * (bitDepth / 8)            // 1 * 2 = 2 bytes per frame
	subChunk2Size := uint32(len(pcmData))                 //nolint:gosec // G115: PCM data length bounded by available memory
	chunkSize := 36 + subChunk2Size                       // 36 is fixed size for header

	// Initialize a buffer to build the WAV file
	buffer := bytes.NewBuffer(nil)

	// List of data elements to write sequentially to the buffer
	elements := []any{
		[]byte("RIFF"), chunkSize, []byte("WAVE"),
		[]byte("fmt "), uint32(16), uint16(1), uint16(numChannels),
		uint32(sampleRate), uint32(byteRate), uint16(blockAlign), uint16(bitDepth),
		[]byte("data"), subChunk2Size,
	}

	// Check if context is done before proceeding
	select {
	case <-ctx.Done():
		enhancedErr := errors.New(ctx.Err()).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "encode_pcm_to_wav_context").
			Context("stage", "context_check").
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("encode_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("encode_wav", "wav", "context_cancelled")
		}
		return nil, enhancedErr
	default:
		// Continue with writing
	}

	// Sequential write operation handling errors
	for _, elem := range elements {
		if b, ok := elem.([]byte); ok {
			// Ensure all byte slices are properly converted before writing
			if _, err := buffer.Write(b); err != nil {
				return nil, fmt.Errorf("failed to write byte slice to buffer: %w", err)
			}
		} else {
			// Handle all other data types
			if err := binary.Write(buffer, binary.LittleEndian, elem); err != nil {
				enhancedErr := errors.New(err).
					Component("myaudio").
					Category(errors.CategorySystem).
					Context("operation", "encode_pcm_to_wav_context").
					Context("stage", "write_header_element").
					Build()

				if fileMetrics != nil {
					fileMetrics.RecordFileOperation("encode_wav", "wav", "error")
					fileMetrics.RecordFileOperationError("encode_wav", "wav", "header_write_failed")
				}
				return nil, enhancedErr
			}
		}
	}

	// Write PCM data to buffer
	if _, err := buffer.Write(pcmData); err != nil {
		enhancedErr := errors.New(err).
			Component("myaudio").
			Category(errors.CategorySystem).
			Context("operation", "encode_pcm_to_wav_context").
			Context("stage", "write_pcm_data").
			Context("pcm_data_size", len(pcmData)).
			Build()

		if fileMetrics != nil {
			fileMetrics.RecordFileOperation("encode_wav", "wav", "error")
			fileMetrics.RecordFileOperationError("encode_wav", "wav", "pcm_write_failed")
		}
		return nil, enhancedErr
	}

	// Record successful operation
	if fileMetrics != nil {
		duration := time.Since(start).Seconds()
		fileMetrics.RecordFileOperation("encode_wav", "wav", "success")
		fileMetrics.RecordFileOperationDuration("encode_wav", "wav", duration)
		fileMetrics.RecordFileSize("encode_wav", "wav", int64(len(pcmData)))
	}

	return buffer, nil
}
