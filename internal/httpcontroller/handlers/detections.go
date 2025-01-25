package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"runtime"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
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
// It retrieves the last set of detections based on the specified count and view type.
func (h *Handlers) RecentDetections(c echo.Context) error {
	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10) // Default value is 10

	var data interface{}
	var templateName string

	// Use the existing detailed view
	notes, err := h.DS.GetLastDetections(numDetections)
	if err != nil {
		return h.NewHandlerError(err, "Failed to fetch recent detections", http.StatusInternalServerError)
	}

	data = struct {
		Notes             []datastore.Note
		DashboardSettings conf.Dashboard
	}{
		Notes:             notes,
		DashboardSettings: *h.DashboardSettings,
	}
	templateName = "recentDetections"

	// Render the appropriate template with the data
	err = c.Render(http.StatusOK, templateName, data)
	if err != nil {
		log.Printf("Failed to render %s template: %v", templateName, err)
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}
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

// getSunEvents calculates sun events for a given date
func (h *Handlers) getSunEvents(date string, loc *time.Location) (suncalc.SunEventTimes, error) {
	// Parse the input date string into a time.Time object using the provided location
	dateTime, err := time.ParseInLocation("2006-01-02", date, loc)
	if err != nil {
		// If parsing fails, return an empty SunEventTimes and the error
		return suncalc.SunEventTimes{}, err
	}

	// Attempt to get sun event times using the SunCalc
	sunEvents, err := h.SunCalc.GetSunEventTimes(dateTime)
	if err != nil {
		// If sun events are not available, use default values
		return suncalc.SunEventTimes{
			CivilDawn: dateTime.Add(5 * time.Hour),  // Set civil dawn to 5:00 AM
			Sunrise:   dateTime.Add(6 * time.Hour),  // Set sunrise to 6:00 AM
			Sunset:    dateTime.Add(18 * time.Hour), // Set sunset to 6:00 PM
			CivilDusk: dateTime.Add(19 * time.Hour), // Set civil dusk to 7:00 PM
		}, nil
	}

	// Return the calculated sun events
	return sunEvents, nil
}

// findClosestWeather finds the closest hourly weather data to the given time
func findClosestWeather(noteTime time.Time, hourlyWeather []datastore.HourlyWeather) *datastore.HourlyWeather {
	// If there's no weather data, return nil
	if len(hourlyWeather) == 0 {
		return nil
	}

	// Initialize variables to track the closest weather data
	var closestWeather *datastore.HourlyWeather
	minDiff := time.Duration(math.MaxInt64)

	// Iterate through all hourly weather data
	for i := range hourlyWeather {
		// Calculate the absolute time difference between the note time and weather time
		diff := noteTime.Sub(hourlyWeather[i].Time).Abs()

		// If this difference is smaller than the current minimum, update the closest weather
		if diff < minDiff {
			minDiff = diff
			closestWeather = &hourlyWeather[i]
		}
	}

	// Return the weather data closest to the note time
	return closestWeather
}
