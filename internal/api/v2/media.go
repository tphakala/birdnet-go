// internal/api/v2/media.go
package api

import (
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// safeFilenamePattern defines the acceptable characters for filenames
// Basic pattern: Only allow alphanumeric, underscore, hyphen, and period
var safeFilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

// Unicode-aware pattern: Allows Unicode letters and numbers plus safe symbols
// Uncomment and use this pattern if Unicode support is needed
// var safeFilenamePattern = regexp.MustCompile(`^[\p{L}\p{N}_\-.]+$`)

// Initialize media routes
func (c *Controller) initMediaRoutes() {
	// Add media routes to the API group
	c.Group.GET("/media/audio/:filename", c.ServeAudioClip)
	c.Group.GET("/media/spectrogram/:filename", c.ServeSpectrogram)
}

// validateMediaPath ensures that a file path is within the allowed export directory and has a valid filename
func (c *Controller) validateMediaPath(exportPath, filename string) (string, error) {
	// Check if filename is empty
	if filename == "" {
		return "", fmt.Errorf("empty filename")
	}

	// Allow only filenames with safe characters
	if !safeFilenamePattern.MatchString(filename) {
		return "", fmt.Errorf("invalid filename characters")
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
		// If range is invalid, serve the full file
		return ctx.File(fullPath)
	}

	// We only support a single range for now
	if len(ranges) != 1 {
		// If multiple ranges, serve the full file for simplicity
		return ctx.File(fullPath)
	}

	// Get the content type based on file extension
	contentType := getContentType(fullPath)

	// Open the file
	file, err := os.Open(fullPath)
	if err != nil {
		return c.HandleError(ctx, err, "Error opening audio file", http.StatusInternalServerError)
	}
	defer file.Close()

	// Set up the response for partial content
	start, length := ranges[0].start, ranges[0].length
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

// ServeSpectrogram serves a spectrogram image for an audio clip
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

	// Check if the audio file exists
	if _, err := os.Stat(audioPath); err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Error accessing audio file", http.StatusInternalServerError)
	}

	// Get the base filename without extension
	baseFilename := strings.TrimSuffix(filepath.Base(filename), filepath.Ext(filename))

	// Generate spectrogram filename with width
	spectrogramFilename := fmt.Sprintf("%s_%d.png", baseFilename, width)

	// Validate the spectrogram path
	spectrogramPath, err := c.validateMediaPath(exportPath, spectrogramFilename)
	if err != nil {
		return c.HandleError(ctx, err, "Invalid spectrogram path", http.StatusBadRequest)
	}

	// Check if the spectrogram already exists
	if _, err := os.Stat(spectrogramPath); err != nil {
		if os.IsNotExist(err) {
			// Spectrogram doesn't exist, generate it
			spectrogramPath, err = c.generateSpectrogram(audioPath, width)
			if err != nil {
				return c.HandleError(ctx, err, "Failed to generate spectrogram", http.StatusInternalServerError)
			}
		} else {
			return c.HandleError(ctx, err, "Error accessing spectrogram file", http.StatusInternalServerError)
		}
	}

	// Serve the spectrogram image
	return ctx.File(spectrogramPath)
}

// generateSpectrogram creates a spectrogram image for the given audio file
func (c *Controller) generateSpectrogram(audioPath string, width int) (string, error) {
	// Extract base filename without extension
	baseFilename := strings.TrimSuffix(filepath.Base(audioPath), filepath.Ext(audioPath))

	// Generate spectrogram filename with width
	exportPath := c.Settings.Realtime.Audio.Export.Path
	spectrogramFilename := fmt.Sprintf("%s_%d.png", baseFilename, width)

	// Validate the spectrogram path
	spectrogramPath, err := c.validateMediaPath(exportPath, spectrogramFilename)
	if err != nil {
		return "", fmt.Errorf("invalid spectrogram path: %w", err)
	}

	// TODO: Implement the spectrogram generation logic
	// This will depend on the specific libraries you're using for spectrogram generation

	// For now, we'll just return an error indicating this isn't implemented yet
	return spectrogramPath, fmt.Errorf("spectrogram generation not implemented yet")
}
