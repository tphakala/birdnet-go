package handlers

import (
	"fmt"
	"html"
	"html/template"
	"log"
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
		// Return a placeholder image if the clip name is empty
		return c.File("assets/images/spectrogram-placeholder.svg")
	}

	// Construct the path to the spectrogram image
	spectrogramPath, err := h.getSpectrogramPath(clipName, 400) // Assuming 400px width
	if err != nil {
		// Return a placeholder image if spectrogram generation fails
		return c.File("assets/images/spectrogram-placeholder.svg")
	}

	// Serve the spectrogram image file
	return c.File(spectrogramPath)
}

// getSpectrogramPath generates the path to the spectrogram image file for a given WAV file
func (h *Handlers) getSpectrogramPath(wavFileName string, width int) (string, error) {
	// Generate file paths
	dir := filepath.Dir(wavFileName)
	baseNameWithoutExt := strings.TrimSuffix(filepath.Base(wavFileName), filepath.Ext(wavFileName))
	spectrogramFileName := fmt.Sprintf("%s_%dpx.png", baseNameWithoutExt, width)
	spectrogramPath := filepath.Join(dir, spectrogramFileName)

	// Convert to web-friendly path
	webFriendlyPath := strings.Replace(spectrogramPath, string(os.PathSeparator), "/", -1)

	// Check if the spectrogram already exists
	if spectrogramExists, err := fileExists(spectrogramPath); err != nil {
		return "", fmt.Errorf("error checking spectrogram file: %w", err)
	} else if spectrogramExists {
		return webFriendlyPath, nil
	}

	// Check if the original audio file exists
	if audioExists, err := fileExists(wavFileName); err != nil {
		return "", fmt.Errorf("error checking audio file: %w", err)
	} else if !audioExists {
		return "", fmt.Errorf("audio file does not exist: %s", wavFileName)
	}

	// Create the spectrogram
	if err := createSpectrogramWithSoX(wavFileName, spectrogramPath, width); err != nil {
		return "", fmt.Errorf("error creating spectrogram with SoX: %w", err)
	}

	return webFriendlyPath, nil
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
