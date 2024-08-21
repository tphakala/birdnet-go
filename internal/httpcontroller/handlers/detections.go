package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// DetectionRequest represents the common parameters for detection requests
type DetectionRequest struct {
	Date       string `query:"date"`
	Hour       string `query:"hour"`
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
	TimeOfDay TimeOfDay
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

	switch req.QueryType {
	case "hourly":
		if req.Date == "" || req.Hour == "" {
			return h.NewHandlerError(fmt.Errorf("missing date or hour"), "Date and hour parameters are required for hourly detections", http.StatusBadRequest)
		}
		notes, err = h.DS.GetHourlyDetections(req.Date, req.Hour)
		totalResults = int64(len(notes))
	case "species":
		if req.Species == "" {
			return h.NewHandlerError(fmt.Errorf("missing species"), "Species parameter is required for species detections", http.StatusBadRequest)
		}
		notes, err = h.DS.SpeciesDetections(req.Species, req.Date, req.Hour, false, req.NumResults, req.Offset)
		totalResults, _ = h.DS.CountSpeciesDetections(req.Species, req.Date, req.Hour)
	case "search":
		if req.Search == "" {
			return h.NewHandlerError(fmt.Errorf("missing search query"), "Search query is required for search detections", http.StatusBadRequest)
		}
		notes, err = h.DS.SearchNotes(req.Search, false, req.NumResults, req.Offset)
		totalResults, _ = h.DS.CountSearchResults(req.Search)
	default:
		return h.NewHandlerError(fmt.Errorf("invalid query type"), "Invalid query type specified", http.StatusBadRequest)
	}

	if err != nil {
		return h.NewHandlerError(err, "Failed to get detections", http.StatusInternalServerError)
	}

	// Add weather and time of day information to the notes
	notesWithWeather, err := h.addWeatherAndTimeOfDay(notes)
	if err != nil {
		return h.NewHandlerError(err, "Failed to add weather and time of day data", http.StatusInternalServerError)
	}

	// Check if OpenWeather is enabled, used to show weather data in the UI if enabled
	weatherEnabled := h.Settings.Realtime.OpenWeather.Enabled

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
	return c.Render(http.StatusOK, "listDetections", data)
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
	return c.Render(http.StatusOK, "detectionDetails", data)
}

// GetLastDetections handles requests for the latest detections.
// It retrieves the last set of detections based on the specified count.
func (h *Handlers) RecentDetections(c echo.Context) error {
	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10) // Default value is 10

	// Retrieve the last detections from the database
	notes, err := h.DS.GetLastDetections(numDetections)
	if err != nil {
		return h.NewHandlerError(err, "Failed to fetch recent detections", http.StatusInternalServerError)
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

func (h *Handlers) addWeatherAndTimeOfDay(notes []datastore.Note) ([]NoteWithWeather, error) {
	notesWithWeather := make([]NoteWithWeather, len(notes))
	weatherEnabled := h.Settings.Realtime.OpenWeather.Enabled

	// Get the local time zone once
	localLoc, err := conf.GetLocalTimezone()
	if err != nil {
		return nil, fmt.Errorf("failed to get local timezone: %w", err)
	}

	// Group notes by date to reduce redundant calculations and database queries
	notesByDate := groupNotesByDate(notes)

	for date, dateNotes := range notesByDate {
		// Calculate sun events once per date
		sunEvents, err := h.getSunEvents(date, localLoc)
		if err != nil {
			log.Printf("Failed to get sun events for date %s: %v", date, err)
		}

		var hourlyWeather []datastore.HourlyWeather
		if weatherEnabled {
			// Fetch hourly weather data once per date
			hourlyWeather, err = h.DS.GetHourlyWeather(date)
			if err != nil {
				log.Printf("Failed to fetch hourly weather for date %s: %v", date, err)
			}
		}

		for i, note := range dateNotes {
			noteTime, err := time.ParseInLocation("2006-01-02 15:04:05", note.Date+" "+note.Time, localLoc)
			if err != nil {
				return nil, fmt.Errorf("failed to parse note time: %w", err)
			}

			noteWithWeather := NoteWithWeather{
				Note:      note,
				TimeOfDay: h.CalculateTimeOfDay(noteTime, sunEvents),
			}

			if weatherEnabled && len(hourlyWeather) > 0 {
				noteWithWeather.Weather = findClosestWeather(noteTime, hourlyWeather)
			}

			// Ensure we're not accessing an out-of-range index
			if i < len(notesWithWeather) {
				notesWithWeather[i] = noteWithWeather
			} else {
				log.Printf("Warning: Attempting to access out-of-range index %d in notesWithWeather slice", i)
			}
		}
	}

	return notesWithWeather, nil
}

// getSunEvents calculates sun events for a given date
func (h *Handlers) getSunEvents(date string, loc *time.Location) (suncalc.SunEventTimes, error) {
	dateTime, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		return suncalc.SunEventTimes{}, err
	}

	sunEvents, err := h.SunCalc.GetSunEventTimes(dateTime)
	if err != nil {
		// Use default values if sun events are not available
		return suncalc.SunEventTimes{
			CivilDawn: dateTime.Add(5 * time.Hour),
			Sunrise:   dateTime.Add(6 * time.Hour),
			Sunset:    dateTime.Add(18 * time.Hour),
			CivilDusk: dateTime.Add(19 * time.Hour),
		}, nil
	}

	return sunEvents, nil
}

// groupNotesByDate groups notes by their date
func groupNotesByDate(notes []datastore.Note) map[string][]datastore.Note {
	grouped := make(map[string][]datastore.Note)
	for _, note := range notes {
		grouped[note.Date] = append(grouped[note.Date], note)
	}
	return grouped
}

// findClosestWeather finds the closest hourly weather data to the given time
func findClosestWeather(noteTime time.Time, hourlyWeather []datastore.HourlyWeather) *datastore.HourlyWeather {
	if len(hourlyWeather) == 0 {
		return nil
	}

	var closestWeather *datastore.HourlyWeather
	minDiff := time.Duration(math.MaxInt64)

	for i := range hourlyWeather {
		diff := noteTime.Sub(hourlyWeather[i].Time).Abs()
		if diff < minDiff {
			minDiff = diff
			closestWeather = &hourlyWeather[i]
		}
	}

	return closestWeather
}
