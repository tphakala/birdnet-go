// utils.go: This file contains utility functions for the HTTP controller package.
package handlers

import (
	"log"
	"strconv"
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

// sumHourlyCounts calculates the total counts from hourly counts.
func sumHourlyCounts(hourlyCounts *[24]int) int {
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

// Debug logs debug messages if debug mode is enabled
func (h *Handlers) Debug(format string, v ...interface{}) {
	if h.Settings.WebServer.Debug {
		if len(v) == 0 {
			log.Print(format)
		} else {
			log.Printf(format, v...)
		}
	}
}
