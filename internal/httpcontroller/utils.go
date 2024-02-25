// utils.go
package httpcontroller

import (
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// NoteWithIndex extends model.Note with additional fields for template rendering.
type NoteWithIndex struct {
	datastore.Note
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
	const maxDetections = 200 // Maximum number of detections expected
	widthPercentage := (totalDetections * 100) / maxDetections
	if widthPercentage > 100 {
		widthPercentage = 100 // Limit width to 100% if exceeded
	}
	return widthPercentage
}

// even checks if an integer is even. Useful for alternating styles in loops.
func even(index int) bool {
	return index%2 == 0
}

// heatmapColor assigns a color based on a provided value using predefined thresholds.
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

// confidence converts a confidence value (0.0 - 1.0) to a percentage string.
func confidence(confidence float64) string {
	return fmt.Sprintf("%.0f%%", confidence*100)
}

// confidenceColor assigns a color based on the confidence value.
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

// createSpectrogramWithSoX generates a spectrogram for a WAV file using SoX.
func createSpectrogramWithSoX(wavFilePath, spectrogramPath string, width int) error {
	// Verify SoX installation
	if _, err := exec.LookPath("sox"); err != nil {
		return fmt.Errorf("SoX binary not found: %w", err)
	}

	// set heigth based on width
	heightStr := strconv.Itoa(width / 2)
	widthStr := strconv.Itoa(width)

	// Execute SoX command to create spectrogram
	cmd := exec.Command("sox", wavFilePath, "-n", "rate", "30k", "spectrogram", "-r", "-x", widthStr, "-y", heightStr, "-o", spectrogramPath)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("SoX command failed: %w", err)
	}

	log.Printf("Spectrogram generated at '%s'", spectrogramPath)
	return nil
}

// GetSpectrogramPath returns the web-friendly path to the spectrogram image for a WAV file, stored in the same directory.
func getSpectrogramPath(wavFileName string, width int) (string, error) {
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

// wrapNotesWithSpectrogram wraps notes with their corresponding spectrogram paths.
func wrapNotesWithSpectrogram(notes []datastore.Note) ([]NoteWithSpectrogram, error) {
	notesWithSpectrogram := make([]NoteWithSpectrogram, len(notes))

	// Set the width of the spectrogram in pixels
	const width = 400 // pixels

	for i, note := range notes {
		spectrogramPath, err := getSpectrogramPath(note.ClipName, width)
		if err != nil {
			log.Printf("Error generating spectrogram for %s: %v", note.ClipName, err)
			continue
		}

		notesWithSpectrogram[i] = NoteWithSpectrogram{
			Note:        note,
			Spectrogram: spectrogramPath,
		}
	}
	return notesWithSpectrogram, nil
}

// sumHourlyCounts calculates the total counts from hourly counts.
func sumHourlyCounts(hourlyCounts [24]int) int {
	total := 0
	for _, count := range hourlyCounts {
		total += count
	}
	return total
}

// makeHoursSlice creates a slice representing 24 hours.
func makeHoursSlice() []int {
	hours := make([]int, 24)
	for i := range hours {
		hours[i] = i
	}
	return hours
}

// parseNumDetections parses a string to an integer or returns a default value.
func parseNumDetections(numDetectionsStr string, defaultValue int) int {
	if numDetectionsStr == "" {
		return defaultValue
	}
	numDetections, err := strconv.Atoi(numDetectionsStr)
	if err != nil || numDetections <= 0 {
		return defaultValue
	}
	return numDetections
}

// parseOffset converts the offset query parameter to an integer.
func parseOffset(offsetStr string, defaultOffset int) int {
	if offsetStr == "" {
		return defaultOffset
	}
	offset, err := strconv.Atoi(offsetStr)
	if err != nil {
		return defaultOffset
	}
	return offset
}
