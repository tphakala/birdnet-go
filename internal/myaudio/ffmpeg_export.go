package myaudio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// ExportAudioWithFFmpeg exports PCM data to the specified format using FFmpeg
func ExportAudioWithFFmpeg(pcmData []byte, outputPath string, settings *conf.AudioSettings) error {
	// Use the FFmpeg path from the settings
	ffmpegBinary := settings.Ffmpeg
	if ffmpegBinary == "" {
		return fmt.Errorf("FFmpeg is not available")
	}

	// Ensure the output directory exists
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return fmt.Errorf("failed to create output directory: %w", err)
	}

	// Create a temporary file in the same directory as the final output
	tempFile, err := os.CreateTemp(outputDir, "temp_audio_*"+filepath.Ext(outputPath))
	if err != nil {
		return fmt.Errorf("failed to create temporary file: %w", err)
	}
	tempFilePath := tempFile.Name()
	defer func() {
		tempFile.Close()
		os.Remove(tempFilePath) // Clean up the temp file in case of failure
	}()

	// Prepare FFmpeg command
	args := []string{
		"-f", "s16le", // Input format: signed 16-bit little-endian
		"-ar", "48000", // Sample rate: 48kHz
		"-ac", "1", // Channels: 1 (mono)
		"-i", "-", // Read from stdin
		"-c:a", getCodec(settings.Export.Type),
		"-b:a", settings.Export.Bitrate,
		"-y",         // Overwrite output file if it exists
		tempFilePath, // Write to the temporary file
	}

	// Create FFmpeg command
	cmd := exec.Command(ffmpegBinary, args...)

	// Create a pipe to send data to FFmpeg's stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Start the FFmpeg process
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Write PCM data to FFmpeg's stdin
	_, err = stdin.Write(pcmData)
	if err != nil {
		return fmt.Errorf("failed to write PCM data to FFmpeg: %w", err)
	}

	// Close stdin to signal EOF to FFmpeg
	stdin.Close()

	// Wait for FFmpeg to finish
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("FFmpeg failed: %w", err)
	}

	// Atomically rename the temporary file to the final output file
	if err := os.Rename(tempFilePath, outputPath); err != nil {
		return fmt.Errorf("failed to rename temporary file to final output: %w", err)
	}

	return nil
}

// getCodec returns the appropriate codec based on the format
func getCodec(format string) string {
	switch format {
	case "flac":
		return "flac"
	case "opus":
		return "libopus"
	case "aac":
		return "aac"
	case "mp3":
		return "libmp3lame"
	default:
		return format
	}
}
