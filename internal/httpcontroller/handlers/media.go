package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// MaxClipNameLength is the maximum allowed length for a clip name
const MaxClipNameLength = 255

// AllowedCharacters is a regex pattern for allowed characters in clip names
const AllowedCharacters = `^[a-zA-Z0-9_/.-]+$`

var (
	ErrEmptyClipName     = errors.New("empty clip name")
	ErrClipNameTooLong   = errors.New("clip name too long")
	ErrInvalidCharacters = errors.New("invalid characters in clip name")
	ErrPathTraversal     = errors.New("path traversal attempt detected")
)

// debugLogging controls whether debug logs are emitted from media.go
// This can be toggled since media operations can be very verbose
var debugLogging = false

// getMediaLogger returns a component-specific logger for media operations
func (h *Handlers) getMediaLogger(subComponent string) *logger.Logger {
	if h.Logger == nil {
		return nil
	}

	loggerName := "media"
	if subComponent != "" {
		loggerName += "." + subComponent
	}

	return h.Logger.Named(loggerName)
}

// sanitizeClipName performs sanity checks on the clip name and ensures it's a relative path
func (h *Handlers) sanitizeClipName(clipName string) (string, error) {
	logger := h.getMediaLogger("sanitize")

	// Check if the clip name is empty
	if clipName == "" {
		if logger != nil {
			logger.Warn("Empty clip name provided")
		}
		return "", ErrEmptyClipName
	}

	// Decode the clip name
	decodedClipName, err := h.decodeClipName(clipName, logger)
	if err != nil {
		return "", err
	}

	// Normalize path (clean and convert separators)
	cleanPath := h.normalizePath(decodedClipName, logger)

	// Get export path and validate path is within bounds
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	if err := h.validatePath(cleanPath, exportPath, logger); err != nil {
		return "", err
	}

	// Remove 'clips/' prefix if present
	cleanPath = h.removeClipsPrefix(cleanPath, logger)

	// Make path relative if absolute
	cleanPath, err = h.makePathRelative(cleanPath, exportPath, logger)
	if err != nil {
		return "", err
	}

	// Check path length limits
	if err := h.checkPathLength(cleanPath, exportPath, logger); err != nil {
		return "", err
	}

	// Convert to web-friendly format
	cleanPath = filepath.ToSlash(cleanPath)
	if logger != nil && h.debug {
		logger.Debug("Final path with forward slashes", "path", cleanPath)
	}

	return cleanPath, nil
}

// decodeClipName decodes a URL-encoded clip name and validates its length and characters
func (h *Handlers) decodeClipName(clipName string, logger *logger.Logger) (string, error) {
	// Decode the clip name
	decodedClipName, err := url.QueryUnescape(clipName)
	if err != nil {
		if logger != nil {
			logger.Error("Error decoding clip name",
				"clip_name", clipName,
				"error", err)
		}
		return "", fmt.Errorf("error decoding clip name: %w", err)
	}

	if logger != nil && h.debug {
		logger.Debug("Decoded clip name", "clip_name", decodedClipName)
	}

	// Check the length of the decoded clip name
	if len(decodedClipName) > MaxClipNameLength {
		if logger != nil {
			logger.Warn("Clip name exceeds maximum length",
				"clip_name", decodedClipName,
				"length", len(decodedClipName),
				"max_length", MaxClipNameLength)
		}
		return "", ErrClipNameTooLong
	}

	// Check for allowed characters
	if !regexp.MustCompile(AllowedCharacters).MatchString(decodedClipName) {
		if logger != nil {
			logger.Warn("Invalid characters in clip name",
				"clip_name", decodedClipName,
				"allowed_pattern", AllowedCharacters)
		}
		return "", ErrInvalidCharacters
	}

	return decodedClipName, nil
}

// normalizePath cleans the path and normalizes separators
func (h *Handlers) normalizePath(path string, logger *logger.Logger) string {
	// Clean the path and ensure it's relative
	cleanPath := filepath.Clean(path)

	// Convert to forward slashes and normalize multiple separators
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")

	if logger != nil && h.debug {
		logger.Debug("Cleaned path", "path", cleanPath)
	}

	return cleanPath
}

// validatePath checks if the path is within the export directory bounds
func (h *Handlers) validatePath(cleanPath, exportPath string, logger *logger.Logger) error {
	absExportPath, err := filepath.Abs(exportPath)
	if err != nil {
		if logger != nil {
			logger.Error("Failed to resolve export path",
				"export_path", exportPath,
				"error", err)
		}
		return fmt.Errorf("failed to resolve export path: %w", err)
	}

	// Join with export path and get absolute path
	fullPath := filepath.Join(exportPath, cleanPath)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		if logger != nil {
			logger.Error("Failed to resolve full path",
				"full_path", fullPath,
				"error", err)
		}
		return fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Check if the resolved path is within the export directory
	if !strings.HasPrefix(absPath, absExportPath) {
		if logger != nil {
			logger.Warn("Path traversal attempt detected",
				"path", absPath,
				"export_path", absExportPath)
		}
		return ErrPathTraversal
	}

	return nil
}

// removeClipsPrefix removes the 'clips/' prefix if present
func (h *Handlers) removeClipsPrefix(path string, logger *logger.Logger) string {
	prefixLower := strings.ToLower(path)
	if strings.HasPrefix(prefixLower, "clips/") {
		path = path[6:] // Remove "clips/" (6 characters)
	}

	if logger != nil && h.debug {
		logger.Debug("Path after removing clips prefix", "path", path)
	}

	return path
}

// makePathRelative converts absolute paths to relative paths
func (h *Handlers) makePathRelative(path, exportPath string, logger *logger.Logger) (string, error) {
	if !filepath.IsAbs(path) {
		return path, nil
	}

	if logger != nil && h.debug {
		logger.Debug("Found absolute path", "path", path)
		logger.Debug("Export path from settings", "export_path", exportPath)
	}

	// Case-insensitive check for Windows compatibility
	if runtime.GOOS == "windows" {
		pathLower := strings.ToLower(path)
		exportLower := strings.ToLower(exportPath)

		if strings.HasPrefix(pathLower, exportLower) {
			path = path[len(exportPath):]
			path = strings.TrimPrefix(path, string(os.PathSeparator))
		} else {
			if logger != nil {
				logger.Error("Invalid path: absolute path not under export directory",
					"path", path,
					"export_path", exportPath)
			}
			return "", fmt.Errorf("invalid path: absolute path not under export directory")
		}
	} else {
		if strings.HasPrefix(path, exportPath) {
			path = strings.TrimPrefix(path, exportPath)
			path = strings.TrimPrefix(path, string(os.PathSeparator))
		} else {
			if logger != nil {
				logger.Error("Invalid path: absolute path not under export directory",
					"path", path,
					"export_path", exportPath)
			}
			return "", fmt.Errorf("invalid path: absolute path not under export directory")
		}
	}

	if logger != nil && h.debug {
		logger.Debug("Converted to relative path", "path", path)
	}

	return path, nil
}

// checkPathLength ensures the path doesn't exceed OS limits
func (h *Handlers) checkPathLength(path, exportPath string, logger *logger.Logger) error {
	fullPath := filepath.Join(exportPath, path)
	if len(fullPath) > 250 { // Safe limit for most OS
		if logger != nil {
			logger.Warn("Final path length exceeds system limits",
				"path_length", len(fullPath),
				"path", fullPath)
		}
		return fmt.Errorf("final path length exceeds system limits")
	}
	return nil
}

// getFullPath returns the full filesystem path for a relative clip path
func getFullPath(relativePath string) string {
	// Clean the relative path first
	relativePath = filepath.Clean(relativePath)

	// Get the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path

	// If relativePath already starts with the export path, return it cleaned
	if strings.HasPrefix(strings.ToLower(relativePath), strings.ToLower(exportPath)) {
		return relativePath
	}

	// Join and clean the paths
	fullPath := filepath.Join(exportPath, relativePath)
	return filepath.Clean(fullPath)
}

// getWebPath converts a filesystem path to a web-safe path
func getWebPath(path string) string {
	// Convert absolute path to relative path if it starts with the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	if strings.HasPrefix(path, exportPath) {
		path = strings.TrimPrefix(path, exportPath)
		path = strings.TrimPrefix(path, string(os.PathSeparator))
	}

	// Convert path separators to forward slashes for web URLs
	return filepath.ToSlash(path)
}

// Thumbnail returns the URL of a given bird's thumbnail image.
// It takes the bird's scientific name as input and returns the image URL as a string.
// If the image is not found or an error occurs, it returns an empty string.
func (h *Handlers) Thumbnail(scientificName string) string {
	logger := h.getMediaLogger("thumbnail")

	if h.BirdImageCache == nil {
		if logger != nil && h.debug {
			logger.Debug("Bird image cache not initialized")
		}
		// Return empty string if the cache is not initialized
		return ""
	}

	birdImage, err := h.BirdImageCache.Get(scientificName)
	if err != nil {
		if logger != nil && h.debug {
			logger.Debug("Failed to get bird image",
				"species", scientificName,
				"error", err)
		}
		// Return empty string if an error occurs
		return ""
	}

	return birdImage.URL
}

// ThumbnailAttribution returns the HTML-formatted attribution for a bird's thumbnail image.
// It takes the bird's scientific name as input and returns a template.HTML string.
// If the attribution information is incomplete or an error occurs, it returns an empty template.HTML.
func (h *Handlers) ThumbnailAttribution(scientificName string) template.HTML {
	logger := h.getMediaLogger("thumbnail")

	if h.BirdImageCache == nil {
		if logger != nil && h.debug {
			logger.Debug("Bird image cache not initialized")
		}
		// Return empty string if the cache is not initialized
		return template.HTML("")
	}

	birdImage, err := h.BirdImageCache.Get(scientificName)
	if err != nil {
		if logger != nil {
			logger.Warn("Error getting thumbnail info",
				"species", scientificName,
				"error", err)
		} else {
			log.Printf("Error getting thumbnail info for %s: %v", scientificName, err)
		}
		return template.HTML("")
	}

	if birdImage.AuthorName == "" || birdImage.LicenseName == "" {
		if logger != nil && h.debug {
			logger.Debug("Incomplete attribution information",
				"species", scientificName,
				"has_author", birdImage.AuthorName != "",
				"has_license", birdImage.LicenseName != "")
		}
		return template.HTML("")
	}

	var attribution string
	if birdImage.AuthorURL == "" {
		attribution = fmt.Sprintf("© %s / <a href=\"%q\">%s</a>",
			html.EscapeString(birdImage.AuthorName),
			html.EscapeString(birdImage.LicenseURL),
			html.EscapeString(birdImage.LicenseName))
	} else {
		attribution = fmt.Sprintf("© <a href=\"%q\">%s</a> / <a href=\"%q\">%s</a>",
			html.EscapeString(birdImage.AuthorURL),
			html.EscapeString(birdImage.AuthorName),
			html.EscapeString(birdImage.LicenseURL),
			html.EscapeString(birdImage.LicenseName))
	}

	return template.HTML(attribution)
}

// ServeSpectrogram serves or generates a spectrogram for a given clip.
// API: GET /api/v1/media/spectrogram
func (h *Handlers) ServeSpectrogram(c echo.Context) error {
	logger := h.getMediaLogger("spectrogram")

	if logger != nil && h.debug {
		logger.Debug("Handler called", "url", c.Request().URL.String())
	}

	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	if logger != nil && h.debug {
		logger.Debug("Raw clip name from query", "clip", clipName)
	}

	// Sanitize the clip name
	sanitizedClipName, err := h.sanitizeClipName(clipName)
	if err != nil {
		if logger != nil {
			logger.Warn("Error sanitizing clip name",
				"clip", clipName,
				"error", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if logger != nil && h.debug {
		logger.Debug("Sanitized clip name", "clip", sanitizedClipName)
	}

	// Get the full path to the audio file using consistent path handling
	fullPath := getFullPath(sanitizedClipName)
	if logger != nil && h.debug {
		logger.Debug("Full audio path", "path", fullPath)
	}

	// Verify that the audio file exists
	exists, err := fileExists(fullPath)
	if err != nil {
		if logger != nil {
			logger.Error("Error checking audio file", "path", fullPath, "error", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if !exists {
		if logger != nil {
			logger.Warn("Audio file not found", "path", fullPath)
		}
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if logger != nil && h.debug {
		logger.Debug("Audio file exists", "path", fullPath)
	}

	// Construct the path to the spectrogram image
	spectrogramPath, err := h.getSpectrogramPath(fullPath, 400) // Assuming 400px width
	if err != nil {
		if logger != nil {
			logger.Error("Error getting spectrogram path", "audio_path", fullPath, "error", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if logger != nil && h.debug {
		logger.Debug("Final spectrogram path", "path", spectrogramPath)
	}

	// Verify the spectrogram exists
	exists, err = fileExists(spectrogramPath)
	if err != nil {
		if logger != nil {
			logger.Error("Error checking spectrogram file", "path", spectrogramPath, "error", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if !exists {
		if logger != nil {
			logger.Info("Spectrogram file not found, attempting to create it", "path", spectrogramPath)
		}
		// Try to create the spectrogram
		if err := createSpectrogramWithSoX(fullPath, spectrogramPath, 400); err != nil {
			if logger != nil {
				logger.Error("Failed to create spectrogram",
					"audio_path", fullPath,
					"spectrogram_path", spectrogramPath,
					"error", err)
			}
			c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
			return c.File("assets/images/spectrogram-placeholder.svg")
		}
		if logger != nil {
			logger.Info("Successfully created spectrogram", "path", spectrogramPath)
		}
	}

	// Final check if the spectrogram exists after potential creation
	exists, _ = fileExists(spectrogramPath)
	if !exists {
		if logger != nil {
			logger.Error("Spectrogram still not found after creation attempt", "path", spectrogramPath)
		}
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}

	if logger != nil && h.debug {
		logger.Debug("Serving spectrogram file", "path", spectrogramPath)
	}
	// Set the correct Content-Type header for PNG images
	c.Response().Header().Set(echo.HeaderContentType, "image/png")
	c.Response().Header().Set("Cache-Control", "public, max-age=2592000, immutable") // Cache spectrograms for 30 days
	return c.File(spectrogramPath)
}

// getSpectrogramPath generates the path to the spectrogram image file for a given audio file
func (h *Handlers) getSpectrogramPath(audioFileName string, width int) (string, error) {
	logger := h.getMediaLogger("spectrogram")

	// Clean the audio file path first
	audioFileName = filepath.Clean(audioFileName)
	if logger != nil && h.debug {
		logger.Debug("Input audio path", "path", audioFileName)
	}

	// Get the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	if logger != nil && h.debug {
		logger.Debug("Export path", "path", exportPath)
	}

	// Convert both paths to forward slashes for consistent comparison
	audioFileNameSlash := strings.ReplaceAll(audioFileName, "\\", "/")
	exportPathSlash := strings.ReplaceAll(exportPath, "\\", "/")

	// Ensure we're working with the correct base directory
	if !strings.HasPrefix(strings.ToLower(audioFileNameSlash), strings.ToLower(exportPathSlash)) {
		// If the path doesn't already include the export path, add it
		audioFileName = filepath.Clean(filepath.Join(exportPath, audioFileName))
	}
	if logger != nil && h.debug {
		logger.Debug("Full audio path", "path", audioFileName)
	}

	// Generate file paths using the same directory as the audio file
	dir := filepath.Dir(audioFileName)
	if logger != nil && h.debug {
		logger.Debug("Directory path", "dir", dir)
	}

	baseNameWithoutExt := strings.TrimSuffix(filepath.Base(audioFileName), filepath.Ext(audioFileName))
	if logger != nil && h.debug {
		logger.Debug("Base name without extension", "basename", baseNameWithoutExt)
	}

	spectrogramFileName := fmt.Sprintf("%s_%dpx.png", baseNameWithoutExt, width)
	if logger != nil && h.debug {
		logger.Debug("Spectrogram filename", "filename", spectrogramFileName)
	}

	// Join paths using OS-specific separators and clean the result
	spectrogramPath := filepath.Clean(filepath.Join(dir, spectrogramFileName))
	if logger != nil && h.debug {
		logger.Debug("Final spectrogram path", "path", spectrogramPath)
	}

	// Check if the spectrogram already exists
	exists, err := fileExists(spectrogramPath)
	if err != nil {
		if logger != nil {
			logger.Error("Error checking spectrogram existence", "path", spectrogramPath, "error", err)
		}
		return "", fmt.Errorf("error checking spectrogram file: %w", err)
	}
	if exists {
		if logger != nil && h.debug {
			logger.Debug("Existing spectrogram found", "path", spectrogramPath)
		}
		return spectrogramPath, nil
	}
	if logger != nil && h.debug {
		logger.Debug("No existing spectrogram found", "path", spectrogramPath)
	}

	// Check if the original audio file exists
	exists, err = fileExists(audioFileName)
	if err != nil {
		if logger != nil {
			logger.Error("Error checking audio file", "path", audioFileName, "error", err)
		}
		return "", fmt.Errorf("error checking audio file: %w", err)
	}
	if !exists {
		if logger != nil {
			logger.Warn("Audio file does not exist", "path", audioFileName)
		}
		return "", fmt.Errorf("audio file does not exist: %s", audioFileName)
	}
	if logger != nil && h.debug {
		logger.Debug("Audio file exists", "path", audioFileName)
	}

	return spectrogramPath, nil
}

// fileExists checks if a file exists and is not a directory
func fileExists(filename string) (bool, error) {
	info, err := os.Stat(filename)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return !info.IsDir(), nil
}

// createSpectrogramWithSoX generates a spectrogram for an audio file using ffmpeg and SoX.
// It supports various audio formats by using ffmpeg to pipe the audio to SoX when necessary.
func createSpectrogramWithSoX(audioClipPath, spectrogramPath string, width int) error {
	// Get ffmpeg and sox paths from settings
	ffmpegBinary := conf.Setting().Realtime.Audio.FfmpegPath
	soxBinary := conf.Setting().Realtime.Audio.SoxPath

	// Verify ffmpeg and SoX paths
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}
	if soxBinary == "" {
		return fmt.Errorf("SoX path not set in settings")
	}

	// Set height based on width
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	// Determine if we need to use ffmpeg based on file extension
	ext := strings.ToLower(filepath.Ext(audioClipPath))
	// remove prefix dot
	ext = strings.TrimPrefix(ext, ".")
	useFFmpeg := true
	for _, soxType := range conf.Setting().Realtime.Audio.SoxAudioTypes {
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
			log.Printf("SoX cmd: %s", soxCmd.String())
			return fmt.Errorf("error starting SoX command: %w", err)
		}

		// Define error message template
		const errFFmpegSoxFailed = "ffmpeg command failed: %v\nffmpeg output: %s\nsox output: %s\n%s"

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

			// Use fmt.Errorf with the constant format string
			return fmt.Errorf(errFFmpegSoxFailed, err, ffmpegOutput.String(), soxOutput.String(), additionalInfo)
		}

		// Allow other goroutines to run before waiting for SoX to finish
		runtime.Gosched()

		// Wait for sox command to finish
		if err := soxCmd.Wait(); err != nil {
			return fmt.Errorf("SoX command failed: %w\nffmpeg output: %s\nsox output: %s", err, ffmpegOutput.String(), soxOutput.String())
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
	// TODO: make these dynamic based on audio length and gain
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
// It supports various audio formats and applies the same practices as createSpectrogramWithSoX.
func createSpectrogramWithFFmpeg(audioClipPath, spectrogramPath string, width int) error {
	// Get ffmpeg path from settings
	ffmpegBinary := conf.Setting().Realtime.Audio.FfmpegPath

	// Verify ffmpeg path
	if ffmpegBinary == "" {
		return fmt.Errorf("ffmpeg path not set in settings")
	}

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

	log.Printf("ffmpeg command: %s", cmd.String())

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

// sanitizeContentDispositionFilename sanitizes a filename for use in Content-Disposition header
func sanitizeContentDispositionFilename(filename string) string {
	// Remove any characters that could cause issues in headers
	// Replace quotes with single quotes, remove control characters, and escape special characters
	sanitized := strings.Map(func(r rune) rune {
		switch {
		case r == '"':
			return '\''
		case r < 32: // Control characters
			return -1
		case r == '\\' || r == '/' || r == ':' || r == '*' || r == '?' || r == '<' || r == '>' || r == '|':
			return '_'
		default:
			return r
		}
	}, filename)

	// URL encode the filename to handle non-ASCII characters
	encoded := url.QueryEscape(sanitized)

	return encoded
}

// ServeAudioClip serves an audio clip file
// API: GET /api/v1/media/audio
func (h *Handlers) ServeAudioClip(c echo.Context) error {
	logger := h.getMediaLogger("audio")

	if logger != nil && h.debug {
		logger.Debug("Starting to handle request", "path", c.Request().URL.String())
	}

	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	if logger != nil && h.debug {
		logger.Debug("Raw clip name from query", "clip", clipName)
	}

	// Sanitize the clip name
	sanitizedClipName, err := h.sanitizeClipName(clipName)
	if err != nil {
		if logger != nil {
			logger.Warn("Error sanitizing clip name",
				"clip", clipName,
				"error", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/plain")
		return c.String(http.StatusNotFound, "Audio file not found")
	}
	if logger != nil && h.debug {
		logger.Debug("Sanitized clip name", "clip", sanitizedClipName)
	}

	// Get the full path to the audio file
	fullPath := getFullPath(sanitizedClipName)
	if logger != nil && h.debug {
		logger.Debug("Full path", "path", fullPath)
	}

	// Verify that the full path is within the export directory
	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		if logger != nil {
			logger.Error("Error obtaining absolute path",
				"path", fullPath,
				"error", err)
		}
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	absExportPath, err := filepath.Abs(conf.Setting().Realtime.Audio.Export.Path)
	if err != nil {
		if logger != nil {
			logger.Error("Error obtaining absolute export path",
				"export_path", conf.Setting().Realtime.Audio.Export.Path,
				"error", err)
		}
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	if !strings.HasPrefix(absFullPath, absExportPath) {
		if logger != nil {
			logger.Warn("Resolved path outside export directory",
				"path", absFullPath,
				"export_path", absExportPath)
		}
		return c.String(http.StatusForbidden, "Forbidden")
	}

	// Check if the file exists
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			if logger != nil {
				logger.Warn("Audio file not found", "path", fullPath)
			}
		} else {
			if logger != nil {
				logger.Error("Error checking audio file", "path", fullPath, "error", err)
			}
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/plain")
		return c.String(http.StatusNotFound, "Audio file not found")
	}
	if logger != nil && h.debug {
		logger.Debug("File exists at path", "path", fullPath)
	}

	// Get the filename for Content-Disposition
	filename := filepath.Base(sanitizedClipName)
	safeFilename := sanitizeContentDispositionFilename(filename)
	if logger != nil && h.debug {
		logger.Debug("Using filename for disposition",
			"filename", filename,
			"safe_filename", safeFilename)
	}

	// Get MIME type
	mimeType := getAudioMimeType(fullPath)
	if logger != nil && h.debug {
		logger.Debug("MIME type for file", "mime_type", mimeType)
	}

	// Set response headers
	c.Response().Header().Set(echo.HeaderContentType, mimeType)
	c.Response().Header().Set("Content-Transfer-Encoding", "binary")
	c.Response().Header().Set("Content-Description", "File Transfer")
	// Set both ASCII and UTF-8 versions of the filename for better browser compatibility
	c.Response().Header().Set(echo.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, //nolint:gocritic // %s is correct here, %q will mangle filename
			safeFilename,
			safeFilename))

	if logger != nil && h.debug {
		logger.Debug("Set headers",
			"content_type", c.Response().Header().Get(echo.HeaderContentType),
			"disposition", c.Response().Header().Get(echo.HeaderContentDisposition))
	}

	// Serve the file
	if logger != nil && h.debug {
		logger.Debug("Attempting to serve file", "path", fullPath)
	}
	return c.File(fullPath)
}

// getAudioMimeType returns the MIME type for an audio file based on its extension
func getAudioMimeType(filename string) string {
	ext := strings.ToLower(filepath.Ext(filename))
	switch ext {
	case ".mp3":
		return "audio/mpeg"
	case ".ogg", ".opus":
		return "audio/ogg"
	case ".wav":
		return "audio/wav"
	case ".flac":
		return "audio/flac"
	case ".aac":
		return "audio/aac"
	case ".m4a":
		return "audio/mp4"
	case ".alac":
		return "audio/x-alac"
	default:
		return "application/octet-stream"
	}
}
