// internal/api/v2/media.go
package api

import (
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

// Initialize media routes
func (c *Controller) initMediaRoutes() {
	// Add media routes to the API group
	c.Group.GET("/media/audio/:filename", c.ServeAudioClip)
	c.Group.GET("/media/spectrogram/:filename", c.ServeSpectrogram)
}

// ServeAudioClip serves an audio clip file
func (c *Controller) ServeAudioClip(ctx echo.Context) error {
	filename := ctx.Param("filename")
	if filename == "" {
		return c.HandleError(ctx, fmt.Errorf("empty filename"), "Filename is required", http.StatusBadRequest)
	}

	// Sanitize the filename to prevent path traversal
	filename = filepath.Base(filename)

	// Get the full path to the audio file
	exportPath := c.Settings.Realtime.Audio.Export.Path
	fullPath := filepath.Join(exportPath, filename)

	// Check if the file exists
	if _, err := os.Stat(fullPath); os.IsNotExist(err) {
		return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
	}

	// Serve the file
	return ctx.File(fullPath)
}

// ServeSpectrogram serves a spectrogram image for an audio clip
func (c *Controller) ServeSpectrogram(ctx echo.Context) error {
	filename := ctx.Param("filename")
	if filename == "" {
		return c.HandleError(ctx, fmt.Errorf("empty filename"), "Filename is required", http.StatusBadRequest)
	}

	// Parse width parameter
	width := 800 // Default width
	widthStr := ctx.QueryParam("width")
	if widthStr != "" {
		parsedWidth, err := strconv.Atoi(widthStr)
		if err == nil && parsedWidth > 0 {
			width = parsedWidth
		}
	}

	// Sanitize the filename to prevent path traversal
	filename = filepath.Base(filename)

	// Get the base filename without extension
	baseFilename := strings.TrimSuffix(filename, filepath.Ext(filename))

	// Check if the corresponding audio file exists
	exportPath := c.Settings.Realtime.Audio.Export.Path
	audioPath := filepath.Join(exportPath, filename)
	if _, err := os.Stat(audioPath); os.IsNotExist(err) {
		return c.HandleError(ctx, err, "Audio file not found", http.StatusNotFound)
	}

	// Generate spectrogram filename with width
	spectrogramFilename := fmt.Sprintf("%s_%d.png", baseFilename, width)
	spectrogramPath := filepath.Join(exportPath, spectrogramFilename)

	// Check if the spectrogram already exists
	if _, err := os.Stat(spectrogramPath); os.IsNotExist(err) {
		// Spectrogram doesn't exist, generate it
		spectrogramPath, err = c.generateSpectrogram(audioPath, width)
		if err != nil {
			return c.HandleError(ctx, err, "Failed to generate spectrogram", http.StatusInternalServerError)
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
	spectrogramPath := filepath.Join(exportPath, spectrogramFilename)

	// do something with spectrogramPath
	fmt.Println(spectrogramPath)

	// TODO: Implement the spectrogram generation logic
	// This will depend on the specific libraries you're using for spectrogram generation

	// For now, we'll just return an error indicating this isn't implemented yet
	return "", fmt.Errorf("spectrogram generation not implemented yet")
}
