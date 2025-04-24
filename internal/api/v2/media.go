// internal/api/v2/media.go
package api

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
)

// safeFilenamePattern defines the acceptable characters for filenames
// Basic pattern: Only allow alphanumeric, underscore, hyphen, and period
var safeFilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

// Unicode-aware pattern: Allows Unicode letters and numbers plus safe symbols
// Uncomment and use this pattern if Unicode support is needed
// var safeFilenamePattern = regexp.MustCompile(`^[\p{L}\p{N}_\-.]+$`)

// Initialize media routes
func (c *Controller) initMediaRoutes() {
	// Original filename-based routes (keep for backward compatibility)
	c.Group.GET("/media/audio/:filename", c.ServeAudioClip)
	c.Group.GET("/media/spectrogram/:filename", c.ServeSpectrogram)

	// Add ID-based routes for the new frontend (full path matching frontend requests)
	c.Echo.GET("/api/v2/audio/:id", c.ServeAudioByID)
	c.Echo.GET("/api/v2/spectrogram/:id", c.ServeSpectrogramByID)

	// Convenient combined endpoint for both audio and URLs
	c.Group.GET("/media/audio", c.ServeAudioByQueryID)
}

// validateMediaPath ensures that a file path is within the allowed export directory and has a valid filename
// It also creates the export path directory if it doesn't exist
func (c *Controller) validateMediaPath(exportPath, filename string) (string, error) {
	// Check if filename is empty
	if filename == "" {
		return "", fmt.Errorf("empty filename")
	}

	// Allow only filenames with safe characters
	if !safeFilenamePattern.MatchString(filename) {
		return "", fmt.Errorf("invalid filename characters")
	}

	// Create the export directory if it doesn't exist
	if _, err := os.Stat(exportPath); os.IsNotExist(err) {
		if err := os.MkdirAll(exportPath, 0o755); err != nil {
			return "", fmt.Errorf("failed to create export directory: %w", err)
		}
	}

	// Sanitize the filename to prevent path traversal
	filename = filepath.Base(filename)

	// Create the full path
	fullPath := filepath.Join(exportPath, filename)

	// Get absolute paths for comparison
	absExportPath, err := filepath.Abs(exportPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve export path: %w", err)
	}

	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve file path: %w", err)
	}

	// Verify the path is still within the export directory after normalization
	if !strings.HasPrefix(absFullPath, absExportPath) {
		return "", fmt.Errorf("path traversal attempt detected")
	}

	return fullPath, nil
}

// ServeAudioClip serves an audio clip file
func (c *Controller) ServeAudioClip(ctx echo.Context) error {
	filename := ctx.Param("filename")
	exportPath := c.Settings.Realtime.Audio.Export.Path

	// Validate and sanitize the path
	fullPath, err := c.validateMediaPath(exportPath, filename)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid file request", http.StatusBadRequest)
	}

	// Check if the file exists
	fileInfo, err := os.Stat(fullPath)
	if err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Error accessing audio file", http.StatusInternalServerError)
	}

	// If file is smaller than 1MB, just serve it directly for efficiency
	if fileInfo.Size() < 1024*1024 {
		return ctx.File(fullPath)
	}

	// For larger files, check if we have a Range header for partial content
	rangeHeader := ctx.Request().Header.Get("Range")
	if rangeHeader == "" {
		// No range requested, serve the full file
		return ctx.File(fullPath)
	}

	// Parse the Range header
	ranges, err := parseRange(rangeHeader, fileInfo.Size())
	if err != nil {
		// If range is invalid, return a 416 Range Not Satisfiable response
		return ctx.NoContent(http.StatusRequestedRangeNotSatisfiable)
	}

	// If multiple ranges are requested, just use the first one
	// This is more efficient than serving the full file
	if len(ranges) > 0 {
		// Use the first range in the request
		rangeToServe := ranges[0]

		// Get the content type based on file extension
		contentType := getContentType(fullPath)

		// Open the file
		file, err := os.Open(fullPath)
		if err != nil {
			return c.HandleError(ctx, err, "Error opening audio file", http.StatusInternalServerError)
		}
		defer file.Close()

		// Set up the response for partial content
		start, length := rangeToServe.start, rangeToServe.length
		ctx.Response().Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, start+length-1, fileInfo.Size()))
		ctx.Response().Header().Set("Accept-Ranges", "bytes")
		ctx.Response().Header().Set("Content-Type", contentType)
		ctx.Response().Header().Set("Content-Length", fmt.Sprintf("%d", length))
		ctx.Response().WriteHeader(http.StatusPartialContent)

		// Seek to the start position
		_, err = file.Seek(start, 0)
		if err != nil {
			return c.HandleError(ctx, err, "Error seeking audio file", http.StatusInternalServerError)
		}

		// Copy the requested range to the response
		_, err = io.CopyN(ctx.Response(), file, length)
		if err != nil {
			return err
		}

		return nil
	}

	// If we somehow get here with no valid ranges, serve the full file
	return ctx.File(fullPath)
}

// httpRange specifies the byte range to be sent to the client
type httpRange struct {
	start, length int64
}

// parseRange parses a Range header string as per RFC 7233
func parseRange(rangeHeader string, size int64) ([]httpRange, error) {
	if !strings.HasPrefix(rangeHeader, "bytes=") {
		return nil, fmt.Errorf("invalid range header format")
	}
	rangeHeader = strings.TrimPrefix(rangeHeader, "bytes=")

	var ranges []httpRange
	for _, r := range strings.Split(rangeHeader, ",") {
		r = strings.TrimSpace(r)
		if r == "" {
			continue
		}

		parts := strings.Split(r, "-")
		if len(parts) != 2 {
			return nil, fmt.Errorf("invalid range format")
		}

		var start, end int64
		var err error

		if parts[0] == "" {
			// suffix range: -N
			end, err = strconv.ParseInt(parts[1], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid range format")
			}
			if end > size {
				end = size
			}
			start = size - end
			end = size - 1
		} else {
			// normal range: N-M or N-
			start, err = strconv.ParseInt(parts[0], 10, 64)
			if err != nil {
				return nil, fmt.Errorf("invalid range format")
			}

			if parts[1] == "" {
				// range: N-
				end = size - 1
			} else {
				// range: N-M
				end, err = strconv.ParseInt(parts[1], 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid range format")
				}
			}
		}

		if start > end || start < 0 || end >= size {
			// Invalid range
			continue
		}

		ranges = append(ranges, httpRange{start: start, length: end - start + 1})
	}

	if len(ranges) == 0 {
		return nil, fmt.Errorf("no valid ranges found")
	}

	return ranges, nil
}

// getContentType determines the content type based on file extension
func getContentType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".wav":
		return "audio/wav"
	case ".ogg":
		return "audio/ogg"
	case ".flac":
		return "audio/flac"
	default:
		return "application/octet-stream"
	}
}

// ServeAudioByID serves an audio clip file based on note ID
func (c *Controller) ServeAudioByID(ctx echo.Context) error {
	// Get note ID from request
	noteID := ctx.Param("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
	}

	// Fetch clip path for this note ID
	clipPath, err := c.DS.GetNoteClipPath(noteID)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get clip path for note", http.StatusNotFound)
	}

	// If the path is empty, no clip exists
	if clipPath == "" {
		return c.HandleError(ctx, fmt.Errorf("no audio file found"), "No audio clip available for this note", http.StatusNotFound)
	}

	// Check if the clipPath is an absolute path or just a filename
	var fullPath string
	if filepath.IsAbs(clipPath) {
		// It's already an absolute path
		fullPath = clipPath
	} else {
		// It's just a filename, so join with the export directory and validate
		var err error
		fullPath, err = c.validateMediaPath(c.Settings.Realtime.Audio.Export.Path, clipPath)
		if err != nil {
			return c.HandleError(ctx, err, "Invalid clip path", http.StatusBadRequest)
		}
	}

	// Verify the file exists
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Error accessing audio file", http.StatusInternalServerError)
	}

	// Get the filename for content type determination
	filename := filepath.Base(fullPath)

	// Get content type based on file extension
	contentType := getContentType(filename)

	// Set appropriate headers
	ctx.Response().Header().Set("Content-Type", contentType)
	ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", filename))

	// Serve the file directly
	return ctx.File(fullPath)
}

// ServeSpectrogramByID serves a spectrogram image based on note ID
func (c *Controller) ServeSpectrogramByID(ctx echo.Context) error {
	// Get note ID from request
	noteID := ctx.Param("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required", http.StatusBadRequest)
	}

	// Fetch clip path for this note ID
	clipPath, err := c.DS.GetNoteClipPath(noteID)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get clip path for note", http.StatusNotFound)
	}

	// If the path is empty, no clip exists
	if clipPath == "" {
		return c.HandleError(ctx, fmt.Errorf("no audio file found"), "No audio clip available for this note", http.StatusNotFound)
	}

	// Parse width parameter
	width := 800 // Default width
	widthStr := ctx.QueryParam("width")
	if widthStr != "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 && parsedWidth <= 2000 { // Add upper limit for width
			width = parsedWidth
		}
	}

	// Check if the clipPath is an absolute path or just a filename
	var audioPath string
	if filepath.IsAbs(clipPath) {
		// It's already an absolute path
		audioPath = clipPath
	} else {
		// It's just a filename, so join with the export directory and validate
		var err error
		audioPath, err = c.validateMediaPath(c.Settings.Realtime.Audio.Export.Path, clipPath)
		if err != nil {
			return c.HandleError(ctx, err, "Invalid clip path", http.StatusBadRequest)
		}
	}

	// Use the internal helper to serve the spectrogram
	return c.serveSpectrogramInternal(ctx, audioPath, width)
}

// ServeAudioByQueryID serves an audio clip using query parameter for ID
func (c *Controller) ServeAudioByQueryID(ctx echo.Context) error {
	// Get ID from query parameter
	noteID := ctx.QueryParam("id")
	if noteID == "" {
		return c.HandleError(ctx, fmt.Errorf("missing ID"), "Note ID is required as query parameter", http.StatusBadRequest)
	}

	// Set as path parameter and delegate to the ID handler
	ctx.SetParamNames("id")
	ctx.SetParamValues(noteID)
	return c.ServeAudioByID(ctx)
}

// ServeSpectrogram serves a spectrogram image for an audio clip based on filename
func (c *Controller) ServeSpectrogram(ctx echo.Context) error {
	filename := ctx.Param("filename")
	exportPath := c.Settings.Realtime.Audio.Export.Path

	// Parse width parameter
	width := 800 // Default width
	widthStr := ctx.QueryParam("width")
	if widthStr != "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 && parsedWidth <= 2000 { // Add upper limit for width
			width = parsedWidth
		}
	}

	// Validate and sanitize the path for the audio file
	audioPath, err := c.validateMediaPath(exportPath, filename)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid file request", http.StatusBadRequest)
	}

	// Use the internal helper to serve the spectrogram
	return c.serveSpectrogramInternal(ctx, audioPath, width)
}

// serveSpectrogramInternal is a helper function to handle common spectrogram serving logic
func (c *Controller) serveSpectrogramInternal(ctx echo.Context, audioPath string, width int) error {
	// Verify the audio file exists
	if _, err := os.Stat(audioPath); err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Error accessing audio file", http.StatusInternalServerError)
	}

	// Generate the spectrogram or get path to existing one
	spectrogramPath, err := c.generateSpectrogram(audioPath, width)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to generate spectrogram", http.StatusInternalServerError)
	}

	// Get the base filename without extension for the response header
	// Use the original audioPath to derive the filename for the header
	baseFilename := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))
	spectrogramFilename := fmt.Sprintf("%s_%d.png", baseFilename, width)

	// Set appropriate headers for an image
	ctx.Response().Header().Set("Content-Type", "image/png")
	ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("inline; filename=\"%s\"", spectrogramFilename))

	// Serve the spectrogram image
	return ctx.File(spectrogramPath)
}

// generateSpectrogram creates a spectrogram image for the given audio file
func (c *Controller) generateSpectrogram(audioPath string, width int) (string, error) {
	// Extract base filename without extension
	baseFilename := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))

	// Get the directory of the audio file
	audioDir := filepath.Dir(audioPath)

	// Generate spectrogram filename with width
	spectrogramFilename := fmt.Sprintf("%s_%d.png", baseFilename, width)
	spectrogramPath := filepath.Join(audioDir, spectrogramFilename)

	// Check if the spectrogram already exists
	if _, err := os.Stat(spectrogramPath); err == nil {
		return spectrogramPath, nil
	}

	// Check if the audio file exists
	if _, err := os.Stat(audioPath); err != nil {
		return "", fmt.Errorf("audio file does not exist: %w", err)
	}

	// Create the spectrogram using SoX or FFmpeg
	if err := createSpectrogramWithSoX(audioPath, spectrogramPath, width, c.Settings); err != nil {
		// Fallback to FFmpeg if SoX fails
		if err2 := createSpectrogramWithFFmpeg(audioPath, spectrogramPath, width, c.Settings); err2 != nil {
			return "", fmt.Errorf("failed to generate spectrogram with SoX: %w, and with FFmpeg: %w", err, err2)
		}
	}

	return spectrogramPath, nil
}

// Limit concurrent spectrogram generations to avoid overloading the system
const maxConcurrentSpectrograms = 4

var spectrogramSemaphore = make(chan struct{}, maxConcurrentSpectrograms)

// createSpectrogramWithSoX generates a spectrogram for an audio file using ffmpeg and SoX.
// It supports various audio formats by using ffmpeg to pipe the audio to SoX when necessary.
func createSpectrogramWithSoX(audioClipPath, spectrogramPath string, width int, settings *conf.Settings) error {
	// Get ffmpeg and sox paths from settings
	ffmpegBinary := settings.Realtime.Audio.FfmpegPath
	soxBinary := settings.Realtime.Audio.SoxPath

	// Verify ffmpeg and SoX paths
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}
	if soxBinary == "" {
		return fmt.Errorf("SoX path not set in settings")
	}

	// Acquire semaphore to limit concurrent spectrogram generations
	spectrogramSemaphore <- struct{}{}
	defer func() { <-spectrogramSemaphore }()

	// Set height based on width
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	// Determine if we need to use ffmpeg based on file extension
	ext := strings.ToLower(filepath.Ext(audioClipPath))
	// remove prefix dot
	ext = strings.TrimPrefix(ext, ".")
	useFFmpeg := true
	for _, soxType := range settings.Realtime.Audio.SoxAudioTypes {
		if strings.EqualFold(ext, soxType) {
			useFFmpeg = false
			break
		}
	}

	var cmd *exec.Cmd
	var soxCmd *exec.Cmd

	// Decode audio using ffmpeg and pipe to sox for spectrogram creation
	if useFFmpeg {
		// Build ffmpeg command arguments
		ffmpegArgs := []string{"-hide_banner", "-i", audioClipPath, "-f", "sox", "-"}

		// Build SoX command arguments
		soxArgs := append([]string{"-t", "sox", "-"}, getSoxSpectrogramArgs(widthStr, heightStr, spectrogramPath)...)

		// Set up commands
		if runtime.GOOS == "windows" {
			cmd = exec.Command(ffmpegBinary, ffmpegArgs...)
			soxCmd = exec.Command(soxBinary, soxArgs...)
		} else {
			cmd = exec.Command("nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
			soxCmd = exec.Command("nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
		}

		// Set up pipe between ffmpeg and sox
		var err error
		soxCmd.Stdin, err = cmd.StdoutPipe()
		if err != nil {
			return fmt.Errorf("error creating pipe: %w", err)
		}

		// Capture combined output
		var ffmpegOutput, soxOutput bytes.Buffer
		cmd.Stderr = &ffmpegOutput
		soxCmd.Stderr = &soxOutput

		// Allow other goroutines to run before starting SoX
		runtime.Gosched()

		// Start sox command
		if err := soxCmd.Start(); err != nil {
			return fmt.Errorf("error starting SoX command: %w", err)
		}

		// Run ffmpeg command
		if err := cmd.Run(); err != nil {
			// Stop the SoX command to clean up resources
			if killErr := soxCmd.Process.Kill(); killErr != nil {
				log.Printf("Failed to kill SoX process: %v", killErr)
			}

			// Wait for SoX to finish and collect its error, if any
			waitErr := soxCmd.Wait()

			// Prepare additional error information
			var additionalInfo string
			if waitErr != nil && !errors.Is(waitErr, os.ErrProcessDone) {
				additionalInfo = fmt.Sprintf("sox wait error: %v", waitErr)
			}

			return fmt.Errorf("ffmpeg command failed: %w\nffmpeg output: %s\nsox output: %s\n%s",
				err, ffmpegOutput.String(), soxOutput.String(), additionalInfo)
		}

		// Allow other goroutines to run before waiting for SoX to finish
		runtime.Gosched()

		// Wait for sox command to finish
		if err := soxCmd.Wait(); err != nil {
			return fmt.Errorf("SoX command failed: %w\nffmpeg output: %s\nsox output: %s",
				err, ffmpegOutput.String(), soxOutput.String())
		}

		// Allow other goroutines to run after SoX finishes
		runtime.Gosched()
	} else {
		// Use SoX directly for supported formats
		soxArgs := append([]string{audioClipPath}, getSoxSpectrogramArgs(widthStr, heightStr, spectrogramPath)...)

		if runtime.GOOS == "windows" {
			soxCmd = exec.Command(soxBinary, soxArgs...)
		} else {
			soxCmd = exec.Command("nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
		}

		// Capture output
		var soxOutput bytes.Buffer
		soxCmd.Stderr = &soxOutput
		soxCmd.Stdout = &soxOutput

		// Allow other goroutines to run before running SoX
		runtime.Gosched()

		// Run SoX command
		if err := soxCmd.Run(); err != nil {
			return fmt.Errorf("SoX command failed: %w\nOutput: %s", err, soxOutput.String())
		}

		// Allow other goroutines to run after SoX finishes
		runtime.Gosched()
	}

	return nil
}

// getSoxSpectrogramArgs returns the common SoX arguments for generating a spectrogram
func getSoxSpectrogramArgs(widthStr, heightStr, spectrogramPath string) []string {
	// Default settings for spectrogram generation
	const audioLength = "15"
	const dynamicRange = "100"

	args := []string{"-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr, "-d", audioLength, "-z", dynamicRange, "-o", spectrogramPath}
	width, _ := strconv.Atoi(widthStr)
	if width < 800 {
		args = append(args, "-r")
	}
	return args
}

// createSpectrogramWithFFmpeg generates a spectrogram for an audio file using only ffmpeg.
// It supports various audio formats and is used as a fallback if SoX fails.
func createSpectrogramWithFFmpeg(audioClipPath, spectrogramPath string, width int, settings *conf.Settings) error {
	// Get ffmpeg path from settings
	ffmpegBinary := settings.Realtime.Audio.FfmpegPath

	// Verify ffmpeg path
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}

	// Acquire semaphore to limit concurrent spectrogram generations
	spectrogramSemaphore <- struct{}{}
	defer func() { <-spectrogramSemaphore }()

	// Set height based on width
	height := width / 2
	heightStr := strconv.Itoa(height)
	widthStr := strconv.Itoa(width)

	// Build ffmpeg command arguments
	ffmpegArgs := []string{
		"-hide_banner",
		"-y", // answer yes to overwriting the output file if it already exists
		"-i", audioClipPath,
		"-lavfi", fmt.Sprintf("showspectrumpic=s=%sx%s:legend=0:gain=3:drange=100", widthStr, heightStr),
		"-frames:v", "1", // Generate only one frame instead of animation
		spectrogramPath,
	}

	// Determine the command based on the OS
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Directly use ffmpeg command on Windows
		cmd = exec.Command(ffmpegBinary, ffmpegArgs...)
	} else {
		// Prepend 'nice' to the command on Unix-like systems
		cmd = exec.Command("nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
	}

	// Capture combined output
	var output bytes.Buffer
	cmd.Stderr = &output
	cmd.Stdout = &output

	// Run ffmpeg command
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg command failed: %w\nOutput: %s", err, output.String())
	}

	return nil
}
