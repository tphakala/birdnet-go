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
var debugLogging = true

// mediaDebug logs a debug message if media debug logging is enabled
func (h *Handlers) mediaDebug(msg string, fields ...interface{}) {
	// Only log if debugLogging is enabled AND we have a logger
	if debugLogging && h.Logger != nil {
		h.Logger.Debug(msg, fields...)
	}
}

// sanitizeClipName performs sanity checks on the clip name and ensures it's a relative path
func (h *Handlers) sanitizeClipName(clipName string) (string, error) {
	// Check if the clip name is empty
	if clipName == "" {
		return "", ErrEmptyClipName
	}

	// Decode the clip name
	decodedClipName, err := url.QueryUnescape(clipName)
	if err != nil {
		return "", fmt.Errorf("error decoding clip name: %w", err)
	}
	h.mediaDebug("sanitizeClipName: Decoded clip name", "clip_name", decodedClipName)

	// Check the length of the decoded clip name
	if len(decodedClipName) > MaxClipNameLength {
		return "", ErrClipNameTooLong
	}

	// Check for allowed characters
	if !regexp.MustCompile(AllowedCharacters).MatchString(decodedClipName) {
		h.mediaDebug("sanitizeClipName: Invalid characters in clip name", "clip_name", decodedClipName)
		return "", ErrInvalidCharacters
	}

	// Clean the path and ensure it's relative
	cleanPath := filepath.Clean(decodedClipName)

	// Convert to forward slashes and normalize multiple separators
	cleanPath = strings.ReplaceAll(cleanPath, "\\", "/")
	cleanPath = strings.ReplaceAll(cleanPath, "//", "/")
	h.mediaDebug("sanitizeClipName: Cleaned path", "path", cleanPath)

	// Get absolute paths for comparison
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	absExportPath, err := filepath.Abs(exportPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve export path: %w", err)
	}

	// Join with export path and get absolute path
	fullPath := filepath.Join(exportPath, cleanPath)
	absPath, err := filepath.Abs(fullPath)
	if err != nil {
		return "", fmt.Errorf("failed to resolve full path: %w", err)
	}

	// Check if the resolved path is within the export directory
	if !strings.HasPrefix(absPath, absExportPath) {
		h.mediaDebug("sanitizeClipName: Path traversal attempt detected",
			"path", absPath,
			"export_path", absExportPath)
		return "", ErrPathTraversal
	}

	// Remove 'clips/' prefix if present (case-insensitive for Windows compatibility)
	prefixLower := strings.ToLower(cleanPath)
	if strings.HasPrefix(prefixLower, "clips/") {
		cleanPath = cleanPath[6:] // Remove "clips/" (6 characters)
	}
	h.mediaDebug("sanitizeClipName: Path after removing clips prefix", "path", cleanPath)

	// If the path is absolute, make it relative to the export path
	if filepath.IsAbs(cleanPath) {
		h.mediaDebug("sanitizeClipName: Found absolute path", "path", cleanPath)
		exportPath := conf.Setting().Realtime.Audio.Export.Path
		h.mediaDebug("sanitizeClipName: Export path from settings", "export_path", exportPath)

		// Case-insensitive check for Windows compatibility
		if runtime.GOOS == "windows" {
			cleanPathLower := strings.ToLower(cleanPath)
			exportPathLower := strings.ToLower(exportPath)
			if strings.HasPrefix(cleanPathLower, exportPathLower) {
				cleanPath = cleanPath[len(exportPath):]
				cleanPath = strings.TrimPrefix(cleanPath, string(os.PathSeparator))
			} else {
				return "", fmt.Errorf("invalid path: absolute path not under export directory")
			}
		} else {
			if strings.HasPrefix(cleanPath, exportPath) {
				cleanPath = strings.TrimPrefix(cleanPath, exportPath)
				cleanPath = strings.TrimPrefix(cleanPath, string(os.PathSeparator))
			} else {
				return "", fmt.Errorf("invalid path: absolute path not under export directory")
			}
		}
		h.mediaDebug("sanitizeClipName: Converted to relative path", "path", cleanPath)
	}

	// Check final path length including the export path
	fullPath = filepath.Join(conf.Setting().Realtime.Audio.Export.Path, cleanPath)
	if len(fullPath) > 250 { // Safe limit for most OS
		return "", fmt.Errorf("final path length exceeds system limits")
	}

	// Convert to forward slashes for web URLs
	cleanPath = filepath.ToSlash(cleanPath)
	h.mediaDebug("sanitizeClipName: Final path with forward slashes", "path", cleanPath)

	return cleanPath, nil
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
	if h.BirdImageCache == nil {
		// Return empty string if the cache is not initialized
		return ""
	}

	birdImage, err := h.BirdImageCache.Get(scientificName)
	if err != nil {
		// Return empty string if an error occurs
		return ""
	}

	return birdImage.URL
}

// ThumbnailAttribution returns the HTML-formatted attribution for a bird's thumbnail image.
// It takes the bird's scientific name as input and returns a template.HTML string.
// If the attribution information is incomplete or an error occurs, it returns an empty template.HTML.
func (h *Handlers) ThumbnailAttribution(scientificName string) template.HTML {
	if h.BirdImageCache == nil {
		// Return empty string if the cache is not initialized
		return template.HTML("")
	}

	birdImage, err := h.BirdImageCache.Get(scientificName)
	if err != nil {
		log.Printf("Error getting thumbnail info for %s: %v", scientificName, err)
		return template.HTML("")
	}

	if birdImage.AuthorName == "" || birdImage.LicenseName == "" {
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
	h.mediaDebug("ServeSpectrogram: Handler called", "url", c.Request().URL.String())

	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	h.mediaDebug("ServeSpectrogram: Raw clip name from query", "clip", clipName)

	// Sanitize the clip name
	sanitizedClipName, err := h.sanitizeClipName(clipName)
	if err != nil {
		h.mediaDebug("ServeSpectrogram: Error sanitizing clip name", "error", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	h.mediaDebug("ServeSpectrogram: Sanitized clip name", "clip", sanitizedClipName)

	// Get the full path to the audio file using consistent path handling
	fullPath := getFullPath(sanitizedClipName)
	h.mediaDebug("ServeSpectrogram: Full audio path", "path", fullPath)

	// Verify that the audio file exists
	exists, err := fileExists(fullPath)
	if err != nil {
		h.mediaDebug("ServeSpectrogram: Error checking audio file", "error", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if !exists {
		h.mediaDebug("ServeSpectrogram: Audio file not found", "path", fullPath)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	h.mediaDebug("ServeSpectrogram: Audio file exists", "path", fullPath)

	// Construct the path to the spectrogram image
	spectrogramPath, err := h.getSpectrogramPath(fullPath, 400) // Assuming 400px width
	if err != nil {
		h.mediaDebug("ServeSpectrogram: Error getting spectrogram path", "error", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	h.mediaDebug("ServeSpectrogram: Final spectrogram path", "path", spectrogramPath)

	// Verify the spectrogram exists
	exists, err = fileExists(spectrogramPath)
	if err != nil {
		h.mediaDebug("ServeSpectrogram: Error checking spectrogram file", "error", err)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}
	if !exists {
		h.mediaDebug("ServeSpectrogram: Spectrogram file not found, attempting to create it")
		// Try to create the spectrogram
		if err := createSpectrogramWithSoX(fullPath, spectrogramPath, 400); err != nil {
			h.mediaDebug("ServeSpectrogram: Failed to create spectrogram", "error", err)
			c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
			return c.File("assets/images/spectrogram-placeholder.svg")
		}
		h.mediaDebug("ServeSpectrogram: Successfully created spectrogram", "path", spectrogramPath)
	}

	// Final check if the spectrogram exists after potential creation
	exists, _ = fileExists(spectrogramPath)
	if !exists {
		h.mediaDebug("ServeSpectrogram: Spectrogram still not found after creation attempt", "path", spectrogramPath)
		c.Response().Header().Set(echo.HeaderContentType, "image/svg+xml")
		return c.File("assets/images/spectrogram-placeholder.svg")
	}

	h.mediaDebug("ServeSpectrogram: Serving spectrogram file", "path", spectrogramPath)
	// Set the correct Content-Type header for PNG images
	c.Response().Header().Set(echo.HeaderContentType, "image/png")
	c.Response().Header().Set("Cache-Control", "public, max-age=2592000, immutable") // Cache spectrograms for 30 days
	return c.File(spectrogramPath)
}

// getSpectrogramPath generates the path to the spectrogram image file for a given audio file
func (h *Handlers) getSpectrogramPath(audioFileName string, width int) (string, error) {
	// Clean the audio file path first
	audioFileName = filepath.Clean(audioFileName)
	h.mediaDebug("getSpectrogramPath: Input audio path", "path", audioFileName)

	// Get the export path
	exportPath := conf.Setting().Realtime.Audio.Export.Path
	h.mediaDebug("getSpectrogramPath: Export path", "path", exportPath)

	// Convert both paths to forward slashes for consistent comparison
	audioFileNameSlash := strings.ReplaceAll(audioFileName, "\\", "/")
	exportPathSlash := strings.ReplaceAll(exportPath, "\\", "/")

	// Ensure we're working with the correct base directory
	if !strings.HasPrefix(strings.ToLower(audioFileNameSlash), strings.ToLower(exportPathSlash)) {
		// If the path doesn't already include the export path, add it
		audioFileName = filepath.Clean(filepath.Join(exportPath, audioFileName))
	}
	h.mediaDebug("getSpectrogramPath: Full audio path", "path", audioFileName)

	// Generate file paths using the same directory as the audio file
	dir := filepath.Dir(audioFileName)
	h.mediaDebug("getSpectrogramPath: Directory path", "dir", dir)

	baseNameWithoutExt := strings.TrimSuffix(filepath.Base(audioFileName), filepath.Ext(audioFileName))
	h.mediaDebug("getSpectrogramPath: Base name without extension", "basename", baseNameWithoutExt)

	spectrogramFileName := fmt.Sprintf("%s_%dpx.png", baseNameWithoutExt, width)
	h.mediaDebug("getSpectrogramPath: Spectrogram filename", "filename", spectrogramFileName)

	// Join paths using OS-specific separators and clean the result
	spectrogramPath := filepath.Clean(filepath.Join(dir, spectrogramFileName))
	h.mediaDebug("getSpectrogramPath: Final spectrogram path", "path", spectrogramPath)

	// Check if the spectrogram already exists
	exists, err := fileExists(spectrogramPath)
	if err != nil {
		h.mediaDebug("getSpectrogramPath: Error checking spectrogram existence", "error", err)
		return "", fmt.Errorf("error checking spectrogram file: %w", err)
	}
	if exists {
		h.mediaDebug("getSpectrogramPath: Existing spectrogram found", "path", spectrogramPath)
		return spectrogramPath, nil
	}
	h.mediaDebug("getSpectrogramPath: No existing spectrogram found", "path", spectrogramPath)

	// Check if the original audio file exists
	exists, err = fileExists(audioFileName)
	if err != nil {
		h.mediaDebug("getSpectrogramPath: Error checking audio file", "error", err)
		return "", fmt.Errorf("error checking audio file: %w", err)
	}
	if !exists {
		h.mediaDebug("getSpectrogramPath: Audio file does not exist", "path", audioFileName)
		return "", fmt.Errorf("audio file does not exist: %s", audioFileName)
	}
	h.mediaDebug("getSpectrogramPath: Audio file exists", "path", audioFileName)

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
	h.mediaDebug("ServeAudioClip: Starting to handle request", "path", c.Request().URL.String())

	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	h.mediaDebug("ServeAudioClip: Raw clip name from query", "clip", clipName)

	// Sanitize the clip name
	sanitizedClipName, err := h.sanitizeClipName(clipName)
	if err != nil {
		h.mediaDebug("ServeAudioClip: Error sanitizing clip name", "error", err)
		c.Response().Header().Set(echo.HeaderContentType, "text/plain")
		return c.String(http.StatusNotFound, "Audio file not found")
	}
	h.mediaDebug("ServeAudioClip: Sanitized clip name", "clip", sanitizedClipName)

	// Get the full path to the audio file
	fullPath := getFullPath(sanitizedClipName)
	h.mediaDebug("ServeAudioClip: Full path", "path", fullPath)

	// Verify that the full path is within the export directory
	absFullPath, err := filepath.Abs(fullPath)
	if err != nil {
		h.mediaDebug("ServeAudioClip: Error obtaining absolute path", "error", err)
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	absExportPath, err := filepath.Abs(conf.Setting().Realtime.Audio.Export.Path)
	if err != nil {
		h.mediaDebug("ServeAudioClip: Error obtaining absolute export path", "error", err)
		return c.String(http.StatusInternalServerError, "Internal server error")
	}
	if !strings.HasPrefix(absFullPath, absExportPath) {
		h.mediaDebug("ServeAudioClip: Resolved path outside export directory",
			"path", absFullPath,
			"export_path", absExportPath)
		return c.String(http.StatusForbidden, "Forbidden")
	}

	// Check if the file exists
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			h.mediaDebug("ServeAudioClip: Audio file not found", "path", fullPath)
		} else {
			h.mediaDebug("ServeAudioClip: Error checking audio file", "error", err)
		}
		c.Response().Header().Set(echo.HeaderContentType, "text/plain")
		return c.String(http.StatusNotFound, "Audio file not found")
	}
	h.mediaDebug("ServeAudioClip: File exists at path", "path", fullPath)

	// Get the filename for Content-Disposition
	filename := filepath.Base(sanitizedClipName)
	safeFilename := sanitizeContentDispositionFilename(filename)
	h.mediaDebug("ServeAudioClip: Using filename for disposition",
		"filename", filename,
		"safe_filename", safeFilename)

	// Get MIME type
	mimeType := getAudioMimeType(fullPath)
	h.mediaDebug("ServeAudioClip: MIME type for file", "mime_type", mimeType)

	// Set response headers
	c.Response().Header().Set(echo.HeaderContentType, mimeType)
	c.Response().Header().Set("Content-Transfer-Encoding", "binary")
	c.Response().Header().Set("Content-Description", "File Transfer")
	// Set both ASCII and UTF-8 versions of the filename for better browser compatibility
	c.Response().Header().Set(echo.HeaderContentDisposition,
		fmt.Sprintf(`attachment; filename="%s"; filename*=UTF-8''%s`, //nolint:gocritic // %s is correct here, %q will mangle filename
			safeFilename,
			safeFilename))

	h.mediaDebug("ServeAudioClip: Set headers",
		"content_type", c.Response().Header().Get(echo.HeaderContentType),
		"disposition", c.Response().Header().Get(echo.HeaderContentDisposition))

	// Serve the file
	h.mediaDebug("ServeAudioClip: Attempting to serve file", "path", fullPath)
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
