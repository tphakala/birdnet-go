// internal/api/v2/media.go
package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// safeFilenamePattern defines the acceptable characters for filenames
var safeFilenamePattern = regexp.MustCompile(`^[a-zA-Z0-9_\-.]+$`)

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
	if _, err := os.Stat(fullPath); err != nil {
		if os.IsNotExist(err) {
			return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Error accessing audio file", http.StatusInternalServerError)
	}

	// Serve the file
	return ctx.File(fullPath)
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
