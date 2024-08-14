package handlers

import (
	"fmt"
	"html"
	"html/template"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"

	"github.com/labstack/echo/v4"
)

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

// serveSpectrogramHandler serves or generates a spectrogram for a given clip.
func (h *Handlers) ServeSpectrogram(c echo.Context) error {
	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")
	if clipName == "" {
		return h.NewHandlerError(fmt.Errorf("empty clip name"), "Clip name is required", http.StatusBadRequest)
	}

	// Construct the path to the spectrogram image
	spectrogramPath, err := h.getSpectrogramPath(clipName, 400) // Assuming 400px width
	if err != nil {
		log.Printf("Failed to get or generate spectrogram for clip %s: %v", clipName, err)
		return h.NewHandlerError(err, fmt.Sprintf("Failed to get or generate spectrogram for clip %s", clipName), http.StatusInternalServerError)
	}

	// Serve the spectrogram image file
	return c.File(spectrogramPath)
}

// createSpectrogramWithSoX generates a spectrogram for a WAV file using SoX.
func createSpectrogramWithSoX(audioClipPath, spectrogramPath string, width int) error {
	// Verify SoX installation
	if _, err := exec.LookPath("sox"); err != nil {
		return fmt.Errorf("SoX binary not found: %w", err)
	}

	// Set height based on width
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	// Build SoX command arguments
	args := []string{audioClipPath, "-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr, "-o", spectrogramPath}
	if width < 800 {
		args = append(args, "-r")
	}

	// Determine the command based on the OS
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Directly use SoX command on Windows
		cmd = exec.Command("sox", args...)
	} else {
		// Prepend 'nice' to the command on Unix-like systems
		args = append([]string{"-n", "10", "sox"}, args...) // '19' is a nice value for low priority
		cmd = exec.Command("nice", args...)
	}

	// Execute the command
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SoX command failed: %w", err)
	}

	return nil
}

// GetSpectrogramPath returns the web-friendly path to the spectrogram image for a WAV file, stored in the same directory.
func (h *Handlers) getSpectrogramPath(wavFileName string, width int) (string, error) {
	baseName := filepath.Base(wavFileName)
	dir := filepath.Dir(wavFileName)
	ext := filepath.Ext(baseName)
	baseNameWithoutExt := baseName[:len(baseName)-len(ext)]

	// Include width in the filename
	spectrogramFileName := fmt.Sprintf("%s_%dpx.png", baseNameWithoutExt, width)

	// Construct the file system path using filepath.Join to ensure it's valid on the current OS.
	spectrogramPath := filepath.Join(dir, spectrogramFileName)

	// Convert the file system path to a web-friendly path by replacing backslashes with forward slashes.
	webFriendlyPath := strings.Replace(spectrogramPath, "\\", "/", -1)

	// Check if spectrogram already exists
	if _, err := os.Stat(spectrogramPath); os.IsNotExist(err) {
		// Create the spectrogram if it doesn't exist
		if err := createSpectrogramWithSoX(wavFileName, spectrogramPath, width); err != nil {
			return "", fmt.Errorf("error creating spectrogram with SoX: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("error checking spectrogram file: %w", err)
	}

	// Return the web-friendly path
	return webFriendlyPath, nil
}
