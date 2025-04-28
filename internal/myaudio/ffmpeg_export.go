package myaudio

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// tempExt is the temporary file extension used when exporting audio with FFmpeg
const tempExt = ".temp"

// ExportAudioWithFFmpeg exports PCM data to the specified format using FFmpeg
// outputPath is full path with audio file name and extension based on format
// pcmData is the PCM data to export
func ExportAudioWithFFmpeg(pcmData []byte, outputPath string, settings *conf.AudioSettings) error {
	// Validate the FFmpeg path
	if err := validateFFmpegPathInternal(settings.FfmpegPath); err != nil {
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
	return finalizeOutput(tempFilePath)
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
func finalizeOutput(tempFilePath string) error {
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

// ExportAudioWithCustomFFmpegArgs exports PCM data using FFmpeg with custom arguments directly to a memory buffer.
// This avoids writing temporary files to disk.
// ffmpegPath is the path to the FFmpeg executable.
// customArgs is a slice of strings representing additional FFmpeg arguments (including output format/codec).
func ExportAudioWithCustomFFmpegArgs(pcmData []byte, ffmpegPath string, customArgs []string) (*bytes.Buffer, error) {
	// Validate the FFmpeg path
	if err := validateFFmpegPathInternal(ffmpegPath); err != nil {
		return nil, err
	}

	// Run the FFmpeg command, capturing output to a buffer
	outputBuffer, err := runCustomFFmpegCommandToBuffer(ffmpegPath, pcmData, customArgs)
	if err != nil {
		return nil, err // Error already includes FFmpeg output if execution failed
	}

	// Return the buffer containing the exported audio data
	return outputBuffer, nil
}

// runCustomFFmpegCommandToBuffer executes FFmpeg, piping PCM input and capturing codec output to a buffer.
func runCustomFFmpegCommandToBuffer(ffmpegPath string, pcmData []byte, customArgs []string) (*bytes.Buffer, error) {
	// Get standard input format arguments
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Build the base arguments for PCM input from stdin
	args := []string{
		"-f", ffmpegFormat, // Input format based on bit depth
		"-ar", ffmpegSampleRate, // Sample rate
		"-ac", ffmpegNumChannels, // Number of channels
		"-i", "-", // Read from stdin
	}

	// Append the custom arguments provided by the caller (should include codec, filters, format)
	args = append(args, customArgs...)

	// Append the output destination: pipe:1 (stdout)
	args = append(args, "pipe:1")

	// Create the FFmpeg command
	cmd := exec.Command(ffmpegPath, args...)

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
		defer stdin.Close() // Close stdin when writing is done
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

	// Wait for the command to finish
	waitErr := cmd.Wait()

	// Check for errors from writing, reading, and waiting
	writeErr := <-writeErrChan
	readErr := <-readDoneChan

	if writeErr != nil {
		return nil, fmt.Errorf("error writing PCM data to FFmpeg stdin: %w, stderr: %s", writeErr, stderr.String())
	}
	if readErr != nil {
		return nil, fmt.Errorf("error reading FFmpeg stdout: %w, stderr: %s", readErr, stderr.String())
	}
	if waitErr != nil {
		return nil, fmt.Errorf("FFmpeg failed: %w, stderr: %s", waitErr, stderr.String())
	}

	// Return the buffer if everything succeeded
	return outputBuffer, nil
}

// validateFFmpegPathInternal checks if the provided FFmpeg path is valid and executable.
func validateFFmpegPathInternal(ffmpegPath string) error {
	if ffmpegPath == "" {
		return fmt.Errorf("FFmpeg path is not configured")
	}
	// Check if the file exists
	if _, err := os.Stat(ffmpegPath); os.IsNotExist(err) {
		// If not found at the specified path, try looking in PATH
		if _, pathErr := exec.LookPath(ffmpegPath); pathErr != nil {
			return fmt.Errorf("FFmpeg not found at path '%s' or in system PATH: %w", ffmpegPath, err)
		}
		// If found in PATH, we can proceed (the path might just be the binary name)
	} else if err != nil {
		// Another error occurred during stat (e.g., permission denied)
		return fmt.Errorf("error accessing FFmpeg path '%s': %w", ffmpegPath, err)
	}
	// Basic check passed (exists either at path or in system PATH)
	return nil
}

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
func AnalyzeAudioLoudness(pcmData []byte, ffmpegPath string) (*LoudnessStats, error) {
	// Validate the FFmpeg path
	if err := validateFFmpegPathInternal(ffmpegPath); err != nil {
		return nil, err
	}

	// Get standard input format arguments
	ffmpegSampleRate, ffmpegNumChannels, ffmpegFormat := getFFmpegFormat(conf.SampleRate, conf.NumChannels, conf.BitDepth)

	// Build arguments for Pass 1 analysis
	args := []string{
		"-f", ffmpegFormat, // Input format
		"-ar", ffmpegSampleRate, // Sample rate
		"-ac", ffmpegNumChannels, // Channels
		"-i", "-", // Read from stdin
		"-af", "loudnorm=print_format=json", // Loudnorm filter in analysis mode
		"-f", "null", // Null output format
		"-", // Null output destination
	}

	cmd := exec.Command(ffmpegPath, args...)

	// Create pipe for stdin
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("loudness analysis: failed to create stdin pipe: %w", err)
	}

	// Capture stderr, as loudnorm prints JSON there
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	// Start command
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("loudness analysis: failed to start ffmpeg: %w", err)
	}

	// Write PCM data in a goroutine
	writeErrChan := make(chan error, 1)
	go func() {
		defer stdin.Close()
		_, writeErr := stdin.Write(pcmData)
		writeErrChan <- writeErr
	}()

	// Wait for command completion and check write error
	waitErr := cmd.Wait()
	writeErr := <-writeErrChan

	if writeErr != nil {
		return nil, fmt.Errorf("loudness analysis: error writing PCM data to ffmpeg: %w, stderr: %s", writeErr, stderr.String())
	}
	// Even if waitErr is nil, loudnorm might print JSON, so we continue.
	// If waitErr is not nil, it indicates a more serious FFmpeg problem.
	if waitErr != nil && stderr.Len() == 0 { // Only return error if FFmpeg truly failed AND didn't print JSON
		return nil, fmt.Errorf("loudness analysis: ffmpeg failed: %w, stderr: %s", waitErr, stderr.String())
	}

	// Extract JSON from stderr
	// The JSON output is usually the last part of the stderr.
	stderrStr := stderr.String()
	jsonStart := strings.LastIndex(stderrStr, "{")
	jsonEnd := strings.LastIndex(stderrStr, "}")

	if jsonStart == -1 || jsonEnd == -1 || jsonStart > jsonEnd {
		return nil, fmt.Errorf("loudness analysis: failed to find JSON in ffmpeg stderr. Output: %s", stderrStr)
	}

	jsonOutput := stderrStr[jsonStart : jsonEnd+1]

	// Parse JSON
	var stats LoudnessStats
	if err := json.Unmarshal([]byte(jsonOutput), &stats); err != nil {
		return nil, fmt.Errorf("loudness analysis: failed to parse JSON from ffmpeg: %w, json: %s", err, jsonOutput)
	}

	return &stats, nil
}
