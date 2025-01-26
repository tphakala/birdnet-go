package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/weather"
)

// DetectionRequest represents the common parameters for detection requests
type DetectionRequest struct {
	Date       string `query:"date"`
	Hour       string `query:"hour"`
	Duration   int    `query:"duration"`
	Species    string `query:"species"`
	Search     string `query:"search"`
	NumResults int    `query:"numResults"`
	Offset     int    `query:"offset"`
	QueryType  string `query:"queryType"` // "hourly", "species", "search", or "all"
}

// NoteWithWeather extends the Note struct with weather and time of day information
type NoteWithWeather struct {
	datastore.Note
	Weather   *datastore.HourlyWeather
	TimeOfDay weather.TimeOfDay
}

// ListDetections handles requests for hourly, species-specific, and search detections
func (h *Handlers) Detections(c echo.Context) error {
	req := new(DetectionRequest)
	if err := c.Bind(req); err != nil {
		return h.NewHandlerError(err, "Invalid request parameters", http.StatusBadRequest)
	}

	// Set configurable items per page
	itemsPerPage := 25

	// Validate and set default values
	if req.NumResults == 0 {
		req.NumResults = itemsPerPage
	}

	var notes []datastore.Note
	var totalResults int64
	var err error

	// Determine the type of query to execute based on the QueryType parameter
	switch req.QueryType {
	case "hourly":
		if req.Date == "" || req.Hour == "" {
			return h.NewHandlerError(fmt.Errorf("missing date or hour"), "Date and hour parameters are required for hourly detections", http.StatusBadRequest)
		}
		notes, err = h.DS.GetHourlyDetections(req.Date, req.Hour, req.Duration)
		totalResults = int64(len(notes))
	case "species":
		if req.Species == "" {
			return h.NewHandlerError(fmt.Errorf("missing species"), "Species parameter is required for species detections", http.StatusBadRequest)
		}
		notes, err = h.DS.SpeciesDetections(req.Species, req.Date, req.Hour, req.Duration, false, req.NumResults, req.Offset)
		totalResults, _ = h.DS.CountSpeciesDetections(req.Species, req.Date, req.Hour, req.Duration)
	case "search":
		if req.Search == "" {
			return h.NewHandlerError(fmt.Errorf("missing search query"), "Search query is required for search detections", http.StatusBadRequest)
		}
		notes, err = h.DS.SearchNotes(req.Search, false, req.NumResults, req.Offset)
		totalResults, _ = h.DS.CountSearchResults(req.Search)
	default:
		return h.NewHandlerError(fmt.Errorf("invalid query type"), "Invalid query type specified", http.StatusBadRequest)
	}

	// Yield CPU to other goroutines
	runtime.Gosched()

	if err != nil {
		return h.NewHandlerError(err, "Failed to get detections", http.StatusInternalServerError)
	}

	// Check if weather provider is set, used to show weather data in the UI if enabled
	weatherEnabled := h.Settings.Realtime.Weather.Provider != "none"

	// Add weather and time of day information to the notes
	notesWithWeather, err := h.addWeatherAndTimeOfDay(notes)
	if err != nil {
		return h.NewHandlerError(err, "Failed to add weather and time of day data", http.StatusInternalServerError)
	}

	// Yield CPU to other goroutines
	runtime.Gosched()

	// Calculate pagination info
	currentPage := (req.Offset / req.NumResults) + 1
	totalPages := int(math.Ceil(float64(totalResults) / float64(req.NumResults)))
	showingFrom := req.Offset + 1
	showingTo := req.Offset + len(notes)
	if showingTo > int(totalResults) {
		showingTo = int(totalResults)
	}

	// Prepare data for rendering in the template
	data := struct {
		Date              string
		Hour              string
		Duration          int
		Species           string
		Search            string
		Notes             []NoteWithWeather
		NumResults        int
		Offset            int
		QueryType         string
		DashboardSettings *conf.Dashboard
		TotalResults      int64
		CurrentPage       int
		TotalPages        int
		ShowingFrom       int
		ShowingTo         int
		ItemsPerPage      int
		WeatherEnabled    bool
	}{
		Date:              req.Date,
		Hour:              req.Hour,
		Duration:          req.Duration,
		Species:           req.Species,
		Search:            req.Search,
		Notes:             notesWithWeather,
		NumResults:        req.NumResults,
		Offset:            req.Offset,
		QueryType:         req.QueryType,
		DashboardSettings: h.DashboardSettings,
		TotalResults:      totalResults,
		CurrentPage:       currentPage,
		TotalPages:        totalPages,
		ShowingFrom:       showingFrom,
		ShowingTo:         showingTo,
		ItemsPerPage:      itemsPerPage,
		WeatherEnabled:    weatherEnabled,
	}

	// Render the list detections template with the data
	err = c.Render(http.StatusOK, "listDetections", data)
	if err != nil {
		log.Printf("Failed to render listDetections template: %v", err)
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}
	return nil
}

// getNoteHandler retrieves a single note from the database and renders it.
func (h *Handlers) DetectionDetails(c echo.Context) error {
	noteID := c.QueryParam("id")
	if noteID == "" {
		return h.NewHandlerError(fmt.Errorf("empty note ID"), "Note ID is required", http.StatusBadRequest)
	}

	// Retrieve the note from the database
	note, err := h.DS.Get(noteID)
	if err != nil {
		return h.NewHandlerError(err, "Failed to retrieve note", http.StatusInternalServerError)
	}

	// set spectrogram width, height will be /2
	const width = 1000 // pixels

	// Generate the spectrogram path for the note
	spectrogramPath, err := h.getSpectrogramPath(note.ClipName, width)
	if err != nil {
		h.logError(&HandlerError{Err: err, Message: fmt.Sprintf("Error generating spectrogram for %s", note.ClipName), Code: http.StatusInternalServerError})
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
	err = c.Render(http.StatusOK, "detectionDetails", data)
	if err != nil {
		log.Printf("Failed to render detectionDetails template: %v", err)
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}
	return nil
}

// RecentDetections handles requests for the latest detections.
func (h *Handlers) RecentDetections(c echo.Context) error {
	h.Debug("RecentDetections: Starting handler")

	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10)
	h.Debug("RecentDetections: Fetching %d detections", numDetections)

	notes, err := h.DS.GetLastDetections(numDetections)
	if err != nil {
		h.Debug("RecentDetections: Error fetching detections: %v", err)
		return h.NewHandlerError(err, "Failed to fetch recent detections", http.StatusInternalServerError)
	}

	h.Debug("RecentDetections: Found %d detections", len(notes))

	data := struct {
		Notes             []datastore.Note
		DashboardSettings conf.Dashboard
	}{
		Notes:             notes,
		DashboardSettings: *h.DashboardSettings,
	}

	h.Debug("RecentDetections: Rendering template")
	err = c.Render(http.StatusOK, "recentDetections", data)
	if err != nil {
		h.Debug("RecentDetections: Error rendering template: %v", err)
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}

	h.Debug("RecentDetections: Successfully completed")
	return nil
}

// addWeatherAndTimeOfDay adds weather and time of day information to each note
func (h *Handlers) addWeatherAndTimeOfDay(notes []datastore.Note) ([]NoteWithWeather, error) {
	// Initialize slice to store notes with weather information
	notesWithWeather := make([]NoteWithWeather, len(notes))

	// Check if weather data is enabled in the settings
	weatherEnabled := h.Settings.Realtime.Weather.Provider != "none"

	// Get the local time zone once to avoid repeated calls
	localLoc, err := conf.GetLocalTimezone()
	if err != nil {
		return nil, fmt.Errorf("failed to get local timezone: %w", err)
	}

	// Create a map to cache weather data for each date
	weatherCache := make(map[string][]datastore.HourlyWeather)

	// Process notes in their original order
	for i := range notes {
		// Parse the note's date and time into a time.Time object
		noteTime, err := time.ParseInLocation("2006-01-02 15:04:05", notes[i].Date+" "+notes[i].Time, localLoc)
		if err != nil {
			return nil, fmt.Errorf("failed to parse note time: %w", err)
		}

		// Get sun events for the note's date (SunCalc will use its internal cache)
		sunEvents, err := h.SunCalc.GetSunEventTimes(noteTime)
		if err != nil {
			log.Printf("Failed to get sun events for date %s: %v", notes[i].Date, err)
		}

		// Create a NoteWithWeather object
		noteWithWeather := NoteWithWeather{
			Note:      notes[i],
			TimeOfDay: h.CalculateTimeOfDay(noteTime, &sunEvents),
			Weather:   nil, // Initialize Weather as nil
		}

		// Check if we need to fetch weather data for this date
		if weatherEnabled {
			// If weather data for this date is not in cache, fetch it
			if _, exists := weatherCache[notes[i].Date]; !exists {
				hourlyWeather, err := h.DS.GetHourlyWeather(notes[i].Date)
				if err != nil {
					log.Printf("Failed to fetch hourly weather for date %s: %v", notes[i].Date, err)
				}
				weatherCache[notes[i].Date] = hourlyWeather
			}

			// If we have weather data for this date, find the closest weather data point
			if len(weatherCache[notes[i].Date]) > 0 {
				noteWithWeather.Weather = findClosestWeather(noteTime, weatherCache[notes[i].Date])
			}
		}

		// Add the processed note to the result slice
		notesWithWeather[i] = noteWithWeather

		// Add a yield point here, every 10 iterations
		if i%10 == 0 {
			runtime.Gosched()
		}
	}

	return notesWithWeather, nil
}

// DeleteDetection handles the deletion of a detection and its associated files
func (h *Handlers) DeleteDetection(c echo.Context) error {
	id := c.QueryParam("id")

	if id == "" {
		h.SSE.SendNotification(Notification{
			Message: "Missing detection ID",
			Type:    "error",
		})
		return h.NewHandlerError(fmt.Errorf("no ID provided"), "Missing detection ID", http.StatusBadRequest)
	}

	// Get the clip path before deletion
	clipPath, err := h.DS.GetNoteClipPath(id)
	if err != nil {
		h.Debug("Failed to get clip path: %v", err)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to get clip path: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to get clip path", http.StatusInternalServerError)
	}

	// Delete the note from the database
	if err := h.DS.Delete(id); err != nil {
		h.Debug("Failed to delete note %s: %v", id, err)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to delete note: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to delete note", http.StatusInternalServerError)
	}

	// If there was a clip associated, delete the audio file and spectrogram
	if clipPath != "" {
		// Delete audio file
		audioPath := fmt.Sprintf("%s/%s", h.Settings.Realtime.Audio.Export.Path, clipPath)
		if err := os.Remove(audioPath); err != nil && !os.IsNotExist(err) {
			h.Debug("Failed to delete audio file %s: %v", audioPath, err)
		}

		// Delete spectrogram file
		spectrogramPath := fmt.Sprintf("%s/%s.png", h.Settings.Realtime.Audio.Export.Path, strings.TrimSuffix(clipPath, ".wav"))
		if err := os.Remove(spectrogramPath); err != nil && !os.IsNotExist(err) {
			h.Debug("Failed to delete spectrogram file %s: %v", spectrogramPath, err)
		}
	}

	// Log the successful deletion
	h.Debug("Successfully deleted detection %s", id)

	// Send success notification
	h.SSE.SendNotification(Notification{
		Message: "Detection deleted successfully",
		Type:    "success",
	})

	// Set response header to refresh list
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}

// ReviewDetection handles the verification of a detection as either correct or false positive
func (h *Handlers) ReviewDetection(c echo.Context) error {
	id := c.FormValue("id")
	if id == "" {
		return h.NewHandlerError(fmt.Errorf("no ID provided"), "Missing detection ID", http.StatusBadRequest)
	}

	verified := c.FormValue("verified")
	if verified != "correct" && verified != "false_positive" {
		return h.NewHandlerError(fmt.Errorf("invalid verification status"), "Invalid verification status", http.StatusBadRequest)
	}

	comment := c.FormValue("comment")

	// Verify that the note exists
	if _, err := h.DS.Get(id); err != nil {
		return h.NewHandlerError(err, "Failed to retrieve note", http.StatusInternalServerError)
	}

	// Update only the verification status and comment
	if err := h.DS.UpdateNote(id, map[string]interface{}{
		"verified": verified,
		"comment":  comment,
	}); err != nil {
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to save review: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to save note", http.StatusInternalServerError)
	}

	// Send success notification
	h.SSE.SendNotification(Notification{
		Message: "Detection review saved successfully",
		Type:    "success",
	})

	// Set response header to refresh list
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}
