package myaudio

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// tempExt is the temporary file extension used when exporting audio with FFmpeg
const tempExt = ".temp"

// ExportAudioWithFFmpeg exports PCM data to the specified format using FFmpeg
// outputPath is full path with audio file name and extension based on format
// pcmData is the PCM data to export
func ExportAudioWithFFmpeg(pcmData []byte, outputPath string, settings *conf.AudioSettings) error {
	// Validate the FFmpeg path
	if err := validateFFmpegPath(settings.FfmpegPath); err != nil {
		return err
	}

	// Create a temporary file for FFmpeg output, returns full path with tempExt
	// temporary file is used to perform export as atomic file operation
	tempFilePath, err := createTempFile(outputPath)
	if err != nil {
		return err
	}

	// Run the FFmpeg command to process the audio
	if err := runFFmpegCommand(settings.FfmpegPath, pcmData, tempFilePath, settings); err != nil {
		return err
	}

	// Finalize the output by renaming the temporary file to the final audio file
	return finalizeOutput(tempFilePath, outputPath, settings)
}

// createTempFile creates a temporary file path for FFmpeg output
func createTempFile(outputPath string) (string, error) {
	outputDir := filepath.Dir(outputPath)
	if err := os.MkdirAll(outputDir, os.ModePerm); err != nil {
		return "", fmt.Errorf("failed to create audio export directory: %w", err)
	}
	tempFilePath := outputPath + tempExt
	return tempFilePath, nil
}

// finalizeOutput path removes tempExt from the file name completing atomic file operation
func finalizeOutput(tempFilePath string, outputPath string, settings *conf.AudioSettings) error {
	// strip tempExt from the end of the path
	finalOutputPath := tempFilePath[:len(tempFilePath)-len(tempExt)]

	// Rename the temporary file to the final output path
	if err := os.Rename(tempFilePath, finalOutputPath); err != nil {
		return fmt.Errorf("failed to rename temporary audio file to final output: %w", err)
	}
	return nil
}

// runFFmpegCommand executes the FFmpeg command to process the audio
func runFFmpegCommand(ffmpegPath string, pcmData []byte, tempFilePath string, settings *conf.AudioSettings) error {
	// Build the FFmpeg command arguments
	args := buildFFmpegArgs(tempFilePath, settings)

	// Create the FFmpeg command
	cmd := exec.Command(ffmpegPath, args...)

	// Create a pipe to send PCM data to FFmpeg's stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("failed to create stdin pipe: %w", err)
	}

	// Start the FFmpeg command
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("failed to start FFmpeg: %w", err)
	}

	// Write PCM data to FFmpeg's stdin
	if _, err := stdin.Write(pcmData); err != nil {
		return fmt.Errorf("failed to write PCM data to FFmpeg: %w", err)
	}
	// Close stdin to signal end of input
	stdin.Close()

	// Wait for FFmpeg to finish processing
	if err := cmd.Wait(); err != nil {
		return fmt.Errorf("FFmpeg failed: %w", err)
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

	return []string{
		"-f", ffmpegFormat, // Input format based on bit depth
		"-ar", ffmpegSampleRate, // Sample rate
		"-ac", ffmpegNumChannels, // Number of channels
		"-i", "-", // Read from stdin
		"-c:a", outputEncoder,
		"-b:a", outputBitrate,
		"-f", outputFormat, // Specify the output format
		"-y",         // Overwrite output file if it exists
		tempFilePath, // Write to the temporary file
	}
}

// getCodec returns the appropriate codec to use with FFmpeg based on the format
func getEncoder(format string) string {
	switch format {
	case "flac":
		return "flac"
	case "alac":
		return "alac"
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

// getOutputFormat returns the appropriate output format for FFmpeg based on the export type
func getOutputFormat(exportType string) string {
	switch exportType {
	case "flac":
		return "flac"
	case "alac":
		return "ipod" // ALAC uses the iPod container format
	case "opus":
		return "opus"
	case "aac":
		return "mp4" // AAC typically uses the iPod/MP4 container format
	case "mp3":
		return "mp3"
	default:
		return exportType
	}
}

// getMaxBitrate limits the bitrate to the maximum allowed by the format
func getMaxBitrate(format, requestedBitrate string) string {
	switch format {
	case "opus":
		if requestedBitrate > "256k" {
			return "256k"
		}
	case "mp3":
		if requestedBitrate > "320k" {
			return "320k"
		}
	}
	return requestedBitrate
}
