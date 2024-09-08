package handlers

import (
	"bytes"
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

// getSpectrogramPath generates the path to the spectrogram image file for a given audio file
func (h *Handlers) getSpectrogramPath(audioFileName string, width int) (string, error) {
	// Generate file paths
	dir := filepath.Dir(audioFileName)
	baseNameWithoutExt := strings.TrimSuffix(filepath.Base(audioFileName), filepath.Ext(audioFileName))
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
	if audioExists, err := fileExists(audioFileName); err != nil {
		log.Printf("error checking audio file: %s", err)
		return "", fmt.Errorf("error checking audio file: %w", err)
	} else if !audioExists {
		log.Printf("audio file does not exist: %s", audioFileName)
		return "", fmt.Errorf("audio file does not exist: %s", audioFileName)
	}

	// Create the spectrogram
	if err := createSpectrogramWithSoX(audioFileName, spectrogramPath, width); err != nil {
		log.Printf("error creating spectrogram with SoX: %s", err)
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

// createSpectrogramWithSoX generates a spectrogram for an audio file using ffmpeg and SoX.
// It supports various audio formats by using ffmpeg to pipe the audio to SoX.
func createSpectrogramWithSoX(audioClipPath, spectrogramPath string, width int) error {
	// Verify ffmpeg installation
	if _, err := exec.LookPath("ffmpeg"); err != nil {
		return fmt.Errorf("ffmpeg binary not found: %w", err)
	}

	// Verify SoX installation
	if _, err := exec.LookPath("sox"); err != nil {
		return fmt.Errorf("SoX binary not found: %w", err)
	}

	// Set height based on width
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	// Build ffmpeg command arguments
	ffmpegArgs := []string{"-hide_banner", "-i", audioClipPath, "-f", "sox", "-"}

	// Build SoX command arguments
	soxArgs := []string{"-t", "sox", "-", "-n", "rate", "24k", "spectrogram", "-x", widthStr, "-y", heightStr, "-o", spectrogramPath}
	if width < 800 {
		soxArgs = append(soxArgs, "-r")
	}

	// Determine the commands based on the OS
	var ffmpegCmd, soxCmd *exec.Cmd
	if runtime.GOOS == "windows" {
		// Directly use ffmpeg and SoX commands on Windows
		ffmpegCmd = exec.Command("ffmpeg", ffmpegArgs...)
		soxCmd = exec.Command("sox", soxArgs...)
	} else {
		// Prepend 'nice' to the commands on Unix-like systems
		ffmpegCmd = exec.Command("nice", append([]string{"-n", "19", "ffmpeg"}, ffmpegArgs...)...)
		soxCmd = exec.Command("nice", append([]string{"-n", "19", "sox"}, soxArgs...)...)
	}

	// Set up pipe between ffmpeg and sox
	var err error
	soxCmd.Stdin, err = ffmpegCmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("error creating pipe: %w", err)
	}

	// Capture combined output
	var combinedOutput bytes.Buffer
	soxCmd.Stderr = &combinedOutput
	ffmpegCmd.Stderr = &combinedOutput

	// Start sox command
	if err := soxCmd.Start(); err != nil {
		return fmt.Errorf("error starting SoX command: %w", err)
	}

	// Run ffmpeg command
	if err := ffmpegCmd.Run(); err != nil {
		return fmt.Errorf("ffmpeg command failed: %w\nOutput: %s", err, combinedOutput.String())
	}

	// Wait for sox command to finish
	if err := soxCmd.Wait(); err != nil {
		return fmt.Errorf("SoX command failed: %w\nOutput: %s", err, combinedOutput.String())
	}

	return nil
}
