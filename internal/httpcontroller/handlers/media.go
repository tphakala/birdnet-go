package handlers

import (
	"bytes"
	"errors"
	"fmt"
	"html"
	"html/template"
	"log"
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

// sanitizeClipName performs sanity checks on the clip name
func sanitizeClipName(clipName string) (string, error) {
	// Check if the clip name is empty
	if clipName == "" {
		return "", ErrEmptyClipName
	}

	// Decode the clip name
	decodedClipName, err := url.QueryUnescape(clipName)
	if err != nil {
		return "", fmt.Errorf("error decoding clip name: %w", err)
	}

	// Check the length of the decoded clip name
	if len(decodedClipName) > MaxClipNameLength {
		return "", ErrClipNameTooLong
	}

	// Check for allowed characters
	if !regexp.MustCompile(AllowedCharacters).MatchString(decodedClipName) {
		return "", ErrInvalidCharacters
	}

	// Check for potential path traversal attempts
	cleanPath := filepath.Clean(decodedClipName)
	if strings.Contains(cleanPath, "..") {
		return "", ErrPathTraversal
	}

	return cleanPath, nil
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
func (h *Handlers) ServeSpectrogram(c echo.Context) error {
	// Extract clip name from the query parameters
	clipName := c.QueryParam("clip")

	// Sanitize the clip name
	sanitizedClipName, err := sanitizeClipName(clipName)
	if err != nil {
		log.Printf("Error sanitizing clip name: %v", err)
		return c.File("assets/images/spectrogram-placeholder.svg")
	}

	// Construct the path to the spectrogram image
	spectrogramPath, err := h.getSpectrogramPath(sanitizedClipName, 400) // Assuming 400px width
	if err != nil {
		log.Printf("Error getting spectrogram path: %v", err)
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
	// Get ffmpeg and sox paths from settings
	ffmpegBinary := conf.Setting().Realtime.Audio.Ffmpeg
	soxBinary := conf.Setting().Realtime.Audio.Sox

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
		ffmpegCmd = exec.Command(ffmpegBinary, ffmpegArgs...)
		soxCmd = exec.Command(soxBinary, soxArgs...)
	} else {
		// Prepend 'nice' to the commands on Unix-like systems
		ffmpegCmd = exec.Command("nice", append([]string{"-n", "19", ffmpegBinary}, ffmpegArgs...)...)
		soxCmd = exec.Command("nice", append([]string{"-n", "19", soxBinary}, soxArgs...)...)
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
