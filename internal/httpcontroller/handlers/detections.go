package handlers

import (
	"log"
	"net/http"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// speciesDetectionsHandler handles requests for species-specific detections.
// It retrieves detection data for a given species and date, then renders it.
func (h *Handlers) SpeciesDetections(c echo.Context) error {
	species, date, hour := c.QueryParam("species"), c.QueryParam("date"), c.QueryParam("hour")

	// Check if the required parameters are provided
	if species == "" || date == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Species and date parameters are required.")
	}

	// Number of results to return
	numResults := parseNumDetections(c.QueryParam("numResults"), 25) // default 25

	// Pagination: Calculate offset
	offset := parseOffset(c.QueryParam("offset"), 0) // default 25

	notes, err := h.DS.SpeciesDetections(species, date, hour, false, numResults, offset)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Prepare data for rendering in the template
	data := struct {
		CommonName string
		Date       string
		Hour       string
		Notes      []datastore.Note
		NumResults int
		Offset     int
	}{
		CommonName: species,
		Date:       date,
		Hour:       hour,
		Notes:      notes,
		NumResults: numResults,
		Offset:     offset,
	}

	// render the speciesDetections template with the data
	return c.Render(http.StatusOK, "speciesDetections", data)
}

// hourlyDetectionsHandler handles requests for hourly detections
func (h *Handlers) HourlyDetections(c echo.Context) error {
	date := c.QueryParam("date")
	hour := c.QueryParam("hour")

	if date == "" || hour == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Date and hour are required.")
	}

	// Fetch all detections for the specified date and hour
	detections, err := h.DS.GetHourlyDetections(date, hour)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, err.Error())
	}

	// Prepare data for rendering in the template
	data := struct {
		Date              string
		Hour              string
		Detections        []datastore.Note
		DashboardSettings *conf.Dashboard
	}{
		Date:              date,
		Hour:              hour,
		Detections:        detections,
		DashboardSettings: h.DashboardSettings,
	}

	// Render the hourlyDetections template with the data
	return c.Render(http.StatusOK, "hourlyDetections", data)
}

// getNoteHandler retrieves a single note from the database and renders it.
func (h *Handlers) DetectionDetails(c echo.Context) error {
	noteID := c.QueryParam("id")
	if noteID == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Note ID is required.")
	}

	// Retrieve the note from the database
	note, err := h.DS.Get(noteID)
	if err != nil {
		return echo.NewHTTPError(http.StatusInternalServerError, "Failed to retrieve note: "+err.Error())
	}

	// set spectrogram width, height will be /2
	const width = 1000 // pixels

	// Generate the spectrogram path for the note
	spectrogramPath, err := h.getSpectrogramPath(note.ClipName, width)
	if err != nil {
		log.Printf("Error generating spectrogram for %s: %v", note.ClipName, err)
		spectrogramPath = "" // Set to empty string to avoid breaking the template
	}

	// Prepare data for rendering in the template
	data := struct {
		Note        datastore.Note
		Spectrogram string
	}{
		Note:        note,
		Spectrogram: spectrogramPath,
	}

	// render the detectionDetails template with the data
	return c.Render(http.StatusOK, "detectionDetails", data)
}

// GetLastDetections handles requests for the latest detections.
// It retrieves the last set of detections based on the specified count.
func (h *Handlers) RecentDetections(c echo.Context) error {
	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10) // Default value is 10

	// Retrieve the last detections from the database
	notes, err := h.DS.GetLastDetections(numDetections)
	if err != nil {
		return c.JSON(http.StatusInternalServerError, map[string]string{"error": "Error fetching detections"})
	}

	// Preparing data for rendering in the template
	data := struct {
		Notes             []datastore.Note
		DashboardSettings conf.Dashboard
	}{
		Notes:             notes,
		DashboardSettings: *h.DashboardSettings,
	}

	// render the recentDetections template with the data
	return c.Render(http.StatusOK, "recentDetections", data)
}
