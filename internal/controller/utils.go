package controller

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/tphakala/birdnet-go/internal/model"
)

// NoteWithIndex extends model.Note with additional fields used for template rendering.
type NoteWithIndex struct {
	model.Note
	HourlyCounts    [24]int // Hourly occurrence counts of the note
	TotalDetections int     // Total number of detections for the note
	Index           int     // Index in a list for rendering purposes
}

// getCurrentDate returns the current date in YYYY-MM-DD format.
func getCurrentDate() string {
	return time.Now().Format("2006-01-02")
}

// calcWidth calculates the width of a bar in a bar chart as a percentage.
// It normalizes the totalDetections based on a predefined maximum.
func calcWidth(totalDetections int) int {
	const maxDetections = 200 // Define the expected maximum number of detections
	widthPercentage := (totalDetections * 100) / maxDetections
	if widthPercentage > 100 {
		widthPercentage = 100 // Cap the width at 100%
	}
	return widthPercentage
}

// even checks if an integer is even. Useful for alternating styles in a loop.
func even(index int) bool {
	return index%2 == 0
}

// heatmapColor assigns a color based on the provided value.
// It uses predefined thresholds to determine the color.
func heatmapColor(value int) string {
	thresholds := []int{10, 20, 30, 40, 50, 60, 70, 80, 90}
	colors := []string{"1", "2", "3", "4", "5", "6", "7", "8", "9"}

	for i, threshold := range thresholds {
		if value <= threshold {
			return colors[i]
		}
	}
	return colors[len(colors)-1] // Default to the highest color for values above all thresholds
}

// getAudioURL constructs a URL for an audio clip based on its filename.
// Assumes clips are accessible under the '/clips/' URL path.
func getAudioURL(fullPath string) string {
	filename := filepath.Base(fullPath) // Extract the filename from the full path
	return "/clips/" + filename         // Construct the URL
}

// confidence converts a confidence value (0.0 - 1.0) to a percentage string.
func confidence(confidence float64) string {
	return fmt.Sprintf("%.0f%%", confidence*100)
}

// confidenceColor assigns a color based on the confidence value.
// Different ranges of confidence are mapped to different colors.
func confidenceColor(confidence float64) string {
	switch {
	case confidence >= 0.8:
		return "bg-green-500" // High confidence
	case confidence >= 0.4:
		return "bg-orange-400" // Moderate confidence
	default:
		return "bg-red-500" // Low confidence
	}
}

// GetSpectrogramPath returns the path to the spectrogram image for a given WAV file.
// It checks if the spectrogram image exists and creates it using SoX if not.
func GetSpectrogramPath(wavFileName string) (string, error) {
	spectrogramDir := "spectrograms"
	baseName := filepath.Base(wavFileName)
	ext := filepath.Ext(baseName)
	baseNameWithoutExt := baseName[:len(baseName)-len(ext)]
	spectrogramPath := fmt.Sprintf("%s/%s_spectrogram.png", spectrogramDir, baseNameWithoutExt)

	// Check if the spectrogram already exists
	if _, err := os.Stat(spectrogramPath); os.IsNotExist(err) {
		// Attempt to create the spectrogram
		if err := createSpectrogramWithSox(wavFileName, spectrogramPath); err != nil {
			return "", fmt.Errorf("error creating spectrogram with SoX: %w", err)
		}
	} else if err != nil {
		return "", fmt.Errorf("error checking for spectrogram file: %w", err)
	}

	return spectrogramPath, nil
}

// createSpectrogramWithSox generates a spectrogram for a WAV file using the SoX command-line tool.
// It checks if SoX is installed and accessible before attempting to create the spectrogram.
func createSpectrogramWithSox(wavFilePath, spectrogramPath string) error {
	// Check if SoX is installed
	if _, err := exec.LookPath("sox"); err != nil {
		return fmt.Errorf("SoX binary not found: %w", err)
	}

	// Construct and execute the SoX command
	cmd := exec.Command("sox", wavFilePath, "-n", "spectrogram", "-x", "300", "-y", "200", "-o", spectrogramPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SoX command failed: %w", err)
	}

	log.Printf("Spectrogram generated at '%s'", spectrogramPath)
	return nil
}
