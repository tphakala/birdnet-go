// utils.go
package httpcontroller

import (
	"context"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/shurcooL/graphql"
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

	log.Printf("Spectrogram generated at '%s'", spectrogramPath)
	return nil
}

// GetSpectrogramPath returns the web-friendly path to the spectrogram image for a WAV file, stored in the same directory.
func (s *Server) getSpectrogramPath(wavFileName string, width int) (string, error) {
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

/*
// wrapNotesWithSpectrogram wraps notes with their corresponding spectrogram paths.
func (s *Server) wrapNotesWithSpectrogram(notes []datastore.Note) ([]NoteWithSpectrogram, error) {
	notesWithSpectrogram := make([]NoteWithSpectrogram, len(notes))

	// Create a channel to communicate between goroutines for results
	type result struct {
		index int
		path  string
		err   error
	}
	results := make(chan result, len(notes))

	// Create a channel to limit the number of concurrent goroutines
	semaphore := make(chan struct{}, 4) // Limit to 4

	// Set the width of the spectrogram in pixels
	const width = 400 // pixels

	for i, note := range notes {
		// Acquire a slot in the semaphore before starting a goroutine
		semaphore <- struct{}{}

		// Launch a goroutine for each spectrogram generation
		go func(i int, note datastore.Note) {
			defer func() { <-semaphore }() // Release the slot when done

			spectrogramPath, err := s.getSpectrogramPath(note.ClipName, width)
			results <- result{i, spectrogramPath, err}
		}(i, note)
	}

	// Wait for all goroutines to finish
	for i := 0; i < len(notes); i++ {
		res := <-results
		if res.err != nil {
			log.Printf("Error generating spectrogram for %s: %v", notes[res.index].ClipName, res.err)
			continue
		}
		notesWithSpectrogram[res.index] = NoteWithSpectrogram{
			Note:        notes[res.index],
			Spectrogram: res.path,
		}
	}

	// Wait for all slots to be released ensuring all goroutines have completed
	for i := 0; i < cap(semaphore); i++ {
		semaphore <- struct{}{}
	}
	close(results)
	close(semaphore)

	return notesWithSpectrogram, nil
}*/

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

var (
	client            = graphql.NewClient("https://app.birdweather.com/graphql", nil)
	thumbnailMap      sync.Map
	thumbnailMutexMap sync.Map
)

func queryGraphQL(scientificName string) (string, error) {
	log.Printf("Fetching thumbnail for bird: %s\n", scientificName)

	var query struct {
		Species struct {
			ThumbnailUrl graphql.String
		} `graphql:"species(scientificName: $scientificName)"`
	}

	variables := map[string]interface{}{
		"scientificName": graphql.String(scientificName),
	}

	err := client.Query(context.Background(), &query, variables)
	if err != nil {
		return "", err
	}

	return string(query.Species.ThumbnailUrl), nil
}

// thumbnail returns the url of a given bird's thumbnail
func thumbnail(scientificName string) (string, error) {
	// Check if thumbnail is already cached
	if thumbnail, ok := thumbnailMap.Load(scientificName); ok {
		log.Printf("Bird: %s, Thumbnail (cached): %s\n", scientificName, thumbnail)
		return thumbnail.(string), nil
	}

	// Use a per-item mutex to ensure only one GraphQL query is performed per item
	mu, _ := thumbnailMutexMap.LoadOrStore(scientificName, &sync.Mutex{})
	mutex := mu.(*sync.Mutex)

	mutex.Lock()
	defer mutex.Unlock()

	// Check again if thumbnail is cached after acquiring the lock
	if thumbnail, ok := thumbnailMap.Load(scientificName); ok {
		log.Printf("Bird: %s, Thumbnail (cached): %s\n", scientificName, thumbnail)
		return thumbnail.(string), nil
	}

	thumbn, err := queryGraphQL(scientificName)
	if err != nil {
		return "", fmt.Errorf("error querying GraphQL endpoint: %v", err)
	}

	thumbnailMap.Store(scientificName, thumbn)
	log.Printf("Bird: %s, Thumbnail (fetched): %s\n", scientificName, thumbn)
	return thumbn, nil
}
