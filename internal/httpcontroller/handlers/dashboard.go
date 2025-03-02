// handlers.go: This file contains the request handlers for the web server.
package handlers

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// NoteWithSpectrogram extends the Note model with a spectrogram path.
type NoteWithSpectrogram struct {
	datastore.Note
	Spectrogram string
}

// topBirdsHandler handles requests for the top bird sightings.
// It retrieves data based on the specified date and minimum confidence,
// then renders it using the 'birdsTable' template.
func (h *Handlers) TopBirds(c echo.Context) error {
	// Get a component-specific logger for dashboard operations
	dashboardLogger := h.getDashboardLogger()

	// Retrieving query parameters
	selectedDate := c.QueryParam("date")
	if selectedDate == "" {
		selectedDate = getCurrentDate()
		if dashboardLogger != nil && h.debug {
			dashboardLogger.Debug("Using current date as default",
				"date", selectedDate)
		}
	}

	// Parse the selected date
	parsedDate, err := time.Parse("2006-01-02", selectedDate)
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Invalid date format",
				"date", selectedDate,
				"error", err)
		}
		return h.NewHandlerError(err, "Invalid date format", http.StatusBadRequest)
	}

	minConfidenceStr := c.QueryParam("minConfidence")
	minConfidence, err := strconv.ParseFloat(minConfidenceStr, 64)
	if err != nil {
		minConfidence = 0.0 // Default value on error
		if dashboardLogger != nil && h.debug {
			dashboardLogger.Debug("Using default confidence value",
				"default", minConfidence,
				"input", minConfidenceStr)
		}
	}
	minConfidenceNormalized := minConfidence / 100.0

	// Get top birds data from the database
	startTime := time.Now()
	notes, err := h.DS.GetTopBirdsData(selectedDate, minConfidenceNormalized)
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Failed to get top birds data",
				"date", selectedDate,
				"min_confidence", minConfidenceNormalized,
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to get top birds data", http.StatusInternalServerError)
	}

	if dashboardLogger != nil && h.debug {
		dashboardLogger.Debug("Retrieved top birds data",
			"date", selectedDate,
			"species_count", len(notes),
			"min_confidence", minConfidenceNormalized,
			"duration_ms", time.Since(startTime).Milliseconds())
	}

	// Process notes with additional data such as hourly occurrences and total detections
	notesWithIndex, err := h.ProcessNotes(notes, selectedDate, minConfidenceNormalized)
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Failed to process notes",
				"date", selectedDate,
				"min_confidence", minConfidenceNormalized,
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to process notes", http.StatusInternalServerError)
	}

	// Sorting the notes by total detections in descending order
	sort.Slice(notesWithIndex, func(i, j int) bool {
		return notesWithIndex[i].TotalDetections > notesWithIndex[j].TotalDetections
	})

	// Creating a slice with hours from 0 to 23
	hours := makeHoursSlice()

	// Get sunrise time
	sunrise, err := h.SunCalc.GetSunriseTime(parsedDate)
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Failed to get sunrise time",
				"date", selectedDate,
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to get sunrise time", http.StatusInternalServerError)
	}

	// Get sunset time
	sunset, err := h.SunCalc.GetSunsetTime(parsedDate)
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Failed to get sunset time",
				"date", selectedDate,
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to get sunset time", http.StatusInternalServerError)
	}

	sunriseHour := closestHour(sunrise)
	sunsetHour := closestHour(sunset)

	if dashboardLogger != nil && h.debug {
		dashboardLogger.Debug("Rendering birds table",
			"date", selectedDate,
			"species_count", len(notesWithIndex),
			"sunrise_hour", sunriseHour,
			"sunset_hour", sunsetHour)
	}

	// Preparing data for rendering in the template
	data := struct {
		NotesWithIndex    []NoteWithIndex
		Hours             []int
		SelectedDate      string
		DashboardSettings *conf.Dashboard
		Sunrise           int
		Sunset            int
	}{
		NotesWithIndex:    notesWithIndex,
		Hours:             hours,
		SelectedDate:      selectedDate,
		DashboardSettings: h.DashboardSettings,
		Sunrise:           sunriseHour,
		Sunset:            sunsetHour,
	}

	// Render the birdsTable template with the data
	err = c.Render(http.StatusOK, "birdsTable", data)
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Failed to render birds table template",
				"date", selectedDate,
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}

	if dashboardLogger != nil && h.debug {
		dashboardLogger.Debug("Successfully rendered birds table",
			"date", selectedDate,
			"total_processing_ms", time.Since(startTime).Milliseconds())
	}

	return nil
}

func closestHour(t time.Time) int {
	hour := t.Hour()
	minute := t.Minute()

	if minute >= 30 {
		hour++
	}

	return hour % 24
}

// Additional helper functions (processNotes, makeHoursSlice, updateClipNames, etc.) go here...
func (h *Handlers) ProcessNotes(notes []datastore.Note, selectedDate string, minConfidenceNormalized float64) ([]NoteWithIndex, error) {
	dashboardLogger := h.getDashboardLogger()

	startTime := time.Now()
	notesWithIndex := make([]NoteWithIndex, 0, len(notes))
	for i := range notes {
		hourlyCounts, err := h.DS.GetHourlyOccurrences(selectedDate, notes[i].CommonName, minConfidenceNormalized)
		if err != nil {
			if dashboardLogger != nil {
				dashboardLogger.Error("Failed to get hourly occurrences",
					"species", notes[i].CommonName,
					"date", selectedDate,
					"error", err)
			}
			return nil, err // Return error to be handled by the caller
		}

		totalDetections := sumHourlyCounts(&hourlyCounts)

		notesWithIndex = append(notesWithIndex, NoteWithIndex{
			Note:            notes[i],
			HourlyCounts:    hourlyCounts,
			TotalDetections: totalDetections,
		})

		// Add a yield point to not block CPU for too long
		if i > 0 && i%10 == 0 {
			if dashboardLogger != nil && h.debug {
				dashboardLogger.Debug("Processing species batch",
					"completed", i,
					"total", len(notes))
			}
			time.Sleep(1 * time.Millisecond) // Small yield
		}
	}

	processingTime := time.Since(startTime)
	if dashboardLogger != nil && h.debug {
		dashboardLogger.Debug("Processed notes with hourly counts",
			"count", len(notes),
			"duration_ms", processingTime.Milliseconds(),
			"avg_per_species_ms", float64(processingTime.Milliseconds())/float64(len(notes)))
	}

	// return notes with hourly counts and total detections
	return notesWithIndex, nil
}

// getAllNotes retrieves all notes from the database.
// It returns the notes in a JSON format.
func (h *Handlers) GetAllNotes(c echo.Context) error {
	dashboardLogger := h.getDashboardLogger()

	startTime := time.Now()
	notes, err := h.DS.GetAllNotes()
	if err != nil {
		if dashboardLogger != nil {
			dashboardLogger.Error("Failed to get all notes",
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to get all notes", http.StatusInternalServerError)
	}

	if dashboardLogger != nil && h.debug {
		dashboardLogger.Debug("Retrieved all notes",
			"count", len(notes),
			"duration_ms", time.Since(startTime).Milliseconds())
	}
	return c.JSON(http.StatusOK, notes)
}

// getDashboardLogger returns a component-specific logger for dashboard operations
func (h *Handlers) getDashboardLogger() *logger.Logger {
	if h.Logger == nil {
		return nil
	}
	return h.Logger.Named("http.dashboard")
}
