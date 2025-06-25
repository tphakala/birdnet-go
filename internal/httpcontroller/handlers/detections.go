package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/weather"
	"gorm.io/gorm"
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
// API: GET /api/v1/detections
func (h *Handlers) Detections(c echo.Context) error {
	operationStart := time.Now()
	handlerName := "detections"

	// Bind and validate request
	req := new(DetectionRequest)
	if err := c.Bind(req); err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "bind_detection_request").
			Context("handler", handlerName).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "bind_request", "validation")
		}
		return h.NewHandlerError(enhancedErr, "Invalid request parameters", http.StatusBadRequest)
	}

	// Validate request parameters
	if req.NumResults < 0 || req.NumResults > 1000 {
		enhancedErr := errors.Newf("invalid numResults parameter: %d, must be between 0 and 1000", req.NumResults).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "validate_detection_request").
			Context("handler", handlerName).
			Context("num_results", req.NumResults).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "validate_request", "validation")
		}
		return h.NewHandlerError(enhancedErr, "Invalid numResults parameter", http.StatusBadRequest)
	}

	if req.Offset < 0 {
		enhancedErr := errors.Newf("invalid offset parameter: %d, must be non-negative", req.Offset).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "validate_detection_request").
			Context("handler", handlerName).
			Context("offset", req.Offset).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "validate_request", "validation")
		}
		return h.NewHandlerError(enhancedErr, "Invalid offset parameter", http.StatusBadRequest)
	}

	// Set configurable items per page
	itemsPerPage := 20

	// Validate and set default values
	if req.NumResults == 0 {
		req.NumResults = itemsPerPage
	}

	var notes []datastore.Note
	var totalResults int64
	var err error

	// Execute query based on type
	notes, totalResults, err = h.executeDetectionQuery(req, handlerName)
	if err != nil {
		return err
	}

	// Yield CPU to other goroutines
	runtime.Gosched()

	// This check is now redundant as we handle errors in each case above
	// but keeping for safety in case of unexpected flow
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "fallback_error_handler").
			Context("handler", handlerName).
			Build()
		return h.NewHandlerError(enhancedErr, "Failed to get detections", http.StatusInternalServerError)
	}

	// Check if weather provider is set, used to show weather data in the UI if enabled
	weatherEnabled := h.Settings.Realtime.Weather.Provider != "none"

	// Add weather and time of day information to the notes with telemetry
	weatherStart := time.Now()
	notesWithWeather, err := h.addWeatherAndTimeOfDay(notes)
	if h.Telemetry != nil {
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "add_weather_data", time.Since(weatherStart).Seconds())
		if err != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "add_weather_data", "system")
		} else {
			h.Telemetry.RecordHandlerOperation(handlerName, "add_weather_data", "success")
		}
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategorySystem).
			Context("operation", "add_weather_and_time_of_day").
			Context("handler", handlerName).
			Context("notes_count", len(notes)).
			Build()
		return h.NewHandlerError(enhancedErr, "Failed to add weather and time of day data", http.StatusInternalServerError)
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
		Security          map[string]interface{}
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
		Security: map[string]interface{}{
			"Enabled":       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
			"AccessAllowed": h.Server.IsAccessAllowed(c),
		},
	}

	// Render the list detections template with the data and telemetry
	renderStart := time.Now()
	err = c.Render(http.StatusOK, "listDetections", data)
	if h.Telemetry != nil {
		h.Telemetry.RecordTemplateRender("listDetections", time.Since(renderStart), err)
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "render_template", time.Since(renderStart).Seconds())
		if err != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "render_template", "template")
		} else {
			h.Telemetry.RecordHandlerOperation(handlerName, "render_template", "success")
		}
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategorySystem).
			Context("operation", "render_list_detections_template").
			Context("handler", handlerName).
			Context("template", "listDetections").
			Build()
		log.Printf("Failed to render listDetections template: %v", enhancedErr)
		return h.NewHandlerError(enhancedErr, "Failed to render template", http.StatusInternalServerError)
	}

	// Record successful operation completion
	if h.Telemetry != nil {
		totalElapsed := time.Since(operationStart).Seconds()
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "complete_request", totalElapsed)
		h.Telemetry.RecordHandlerOperation(handlerName, "complete_request", "success")
	}

	return nil
}

// DetectionDetails retrieves a single detection from the database and renders it.
// API: GET /api/v1/detections/details
func (h *Handlers) DetectionDetails(c echo.Context) error {
	operationStart := time.Now()
	handlerName := "detection_details"

	// Validate note ID parameter
	noteID := c.QueryParam("id")
	if noteID == "" {
		enhancedErr := errors.New(fmt.Errorf("empty note ID parameter")).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "validate_note_id").
			Context("handler", handlerName).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "validate_note_id", "validation")
		}
		return h.NewHandlerError(enhancedErr, "Note ID is required", http.StatusBadRequest)
	}

	// Retrieve the note from the database with telemetry
	dbStart := time.Now()
	note, err := h.DS.Get(noteID)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "get_note", time.Since(dbStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "get_note").
			Context("handler", handlerName).
			Context("note_id", noteID).
			Build()
		return h.NewHandlerError(enhancedErr, "Failed to retrieve note", http.StatusInternalServerError)
	}

	// set spectrogram width, height will be /2
	const width = 1000 // pixels

	// Generate the spectrogram path for the note with telemetry
	spectrogramStart := time.Now()
	spectrogramPath, err := h.getSpectrogramPath(note.ClipName, width)
	if h.Telemetry != nil {
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "generate_spectrogram_path", time.Since(spectrogramStart).Seconds())
		if err != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "generate_spectrogram_path", "system")
		} else {
			h.Telemetry.RecordHandlerOperation(handlerName, "generate_spectrogram_path", "success")
		}
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryFileIO).
			Context("operation", "generate_spectrogram_path").
			Context("handler", handlerName).
			Context("clip_name", note.ClipName).
			Context("width", width).
			Build()
		h.logError(&HandlerError{Err: enhancedErr, Message: fmt.Sprintf("Error generating spectrogram for %s", note.ClipName), Code: http.StatusInternalServerError})
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

	// Render the detectionDetails template with the data and telemetry
	renderStart := time.Now()
	err = c.Render(http.StatusOK, "detectionDetails", data)
	if h.Telemetry != nil {
		h.Telemetry.RecordTemplateRender("detectionDetails", time.Since(renderStart), err)
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "render_template", time.Since(renderStart).Seconds())
		if err != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "render_template", "template")
		} else {
			h.Telemetry.RecordHandlerOperation(handlerName, "render_template", "success")
		}
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategorySystem).
			Context("operation", "render_detection_details_template").
			Context("handler", handlerName).
			Context("template", "detectionDetails").
			Context("note_id", noteID).
			Build()
		log.Printf("Failed to render detectionDetails template: %v", enhancedErr)
		return h.NewHandlerError(enhancedErr, "Failed to render template", http.StatusInternalServerError)
	}

	// Record successful operation completion
	if h.Telemetry != nil {
		totalElapsed := time.Since(operationStart).Seconds()
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "complete_request", totalElapsed)
		h.Telemetry.RecordHandlerOperation(handlerName, "complete_request", "success")
	}

	return nil
}

// RecentDetections handles requests for the latest detections.
// API: GET /api/v1/detections/recent
func (h *Handlers) RecentDetections(c echo.Context) error {
	operationStart := time.Now()
	handlerName := "recent_detections"
	h.Debug("RecentDetections: Starting handler")

	// Parse and validate numDetections parameter
	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10)
	if numDetections < 1 || numDetections > 1000 {
		enhancedErr := errors.Newf("invalid numDetections parameter: %d, must be between 1 and 1000", numDetections).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "validate_num_detections").
			Context("handler", handlerName).
			Context("num_detections", numDetections).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "validate_num_detections", "validation")
		}
		return h.NewHandlerError(enhancedErr, "Invalid numDetections parameter", http.StatusBadRequest)
	}

	h.Debug("RecentDetections: Fetching %d detections", numDetections)

	// Get recent detections with telemetry
	dbStart := time.Now()
	notes, err := h.DS.GetLastDetections(numDetections)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "get_last_detections", time.Since(dbStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "get_last_detections").
			Context("handler", handlerName).
			Context("num_detections", numDetections).
			Build()
		h.Debug("RecentDetections: Error fetching detections: %v", enhancedErr)
		return h.NewHandlerError(enhancedErr, "Failed to fetch recent detections", http.StatusInternalServerError)
	}

	h.Debug("RecentDetections: Found %d detections", len(notes))

	data := struct {
		Notes             []datastore.Note
		DashboardSettings conf.Dashboard
		Security          map[string]interface{}
	}{
		Notes:             notes,
		DashboardSettings: *h.DashboardSettings,
		Security: map[string]interface{}{
			"Enabled":       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
			"AccessAllowed": h.Server.IsAccessAllowed(c),
		},
	}

	// Render template with telemetry
	h.Debug("RecentDetections: Rendering template")
	renderStart := time.Now()
	err = c.Render(http.StatusOK, "recentDetections", data)
	if h.Telemetry != nil {
		h.Telemetry.RecordTemplateRender("recentDetections", time.Since(renderStart), err)
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "render_template", time.Since(renderStart).Seconds())
		if err != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "render_template", "template")
		} else {
			h.Telemetry.RecordHandlerOperation(handlerName, "render_template", "success")
		}
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategorySystem).
			Context("operation", "render_recent_detections_template").
			Context("handler", handlerName).
			Context("template", "recentDetections").
			Context("detections_count", len(notes)).
			Build()
		h.Debug("RecentDetections: Error rendering template: %v", enhancedErr)
		return h.NewHandlerError(enhancedErr, "Failed to render template", http.StatusInternalServerError)
	}

	// Record successful operation completion
	if h.Telemetry != nil {
		totalElapsed := time.Since(operationStart).Seconds()
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "complete_request", totalElapsed)
		h.Telemetry.RecordHandlerOperation(handlerName, "complete_request", "success")
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
// API: DELETE /api/v1/detections/delete
func (h *Handlers) DeleteDetection(c echo.Context) error {
	operationStart := time.Now()
	handlerName := "delete_detection"

	// Validate detection ID parameter
	id := c.QueryParam("id")
	if id == "" {
		enhancedErr := errors.New(fmt.Errorf("missing detection ID parameter")).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "validate_detection_id").
			Context("handler", handlerName).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "validate_detection_id", "validation")
		}
		h.SSE.SendNotification(Notification{
			Message: "Missing detection ID",
			Type:    "error",
		})
		return h.NewHandlerError(enhancedErr, "Missing detection ID", http.StatusBadRequest)
	}

	// Get the clip path before deletion with telemetry
	dbStart := time.Now()
	clipPath, err := h.DS.GetNoteClipPath(id)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "get_note_clip_path", time.Since(dbStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "get_note_clip_path").
			Context("handler", handlerName).
			Context("detection_id", id).
			Build()
		h.Debug("Failed to get clip path: %v", enhancedErr)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to get clip path: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(enhancedErr, "Failed to get clip path", http.StatusInternalServerError)
	}

	// Delete the note from the database with telemetry
	deleteStart := time.Now()
	err = h.DS.Delete(id)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "delete_note", time.Since(deleteStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "delete_note").
			Context("handler", handlerName).
			Context("detection_id", id).
			Build()
		h.Debug("Failed to delete note %s: %v", id, enhancedErr)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to delete note: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(enhancedErr, "Failed to delete note", http.StatusInternalServerError)
	}

	// If there was a clip associated, delete the audio file and spectrogram with telemetry
	if clipPath != "" {
		// Delete audio file with telemetry
		audioPath := fmt.Sprintf("%s/%s", h.Settings.Realtime.Audio.Export.Path, clipPath)
		audioDeleteStart := time.Now()
		audioErr := os.Remove(audioPath)
		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationDuration(handlerName, "delete_audio_file", time.Since(audioDeleteStart).Seconds())
			if audioErr != nil && !os.IsNotExist(audioErr) {
				h.Telemetry.RecordHandlerOperationError(handlerName, "delete_audio_file", "file_io")
			} else {
				h.Telemetry.RecordHandlerOperation(handlerName, "delete_audio_file", "success")
			}
		}
		if audioErr != nil && !os.IsNotExist(audioErr) {
			enhancedErr := errors.New(audioErr).
				Component("http-controller").
				Category(errors.CategoryFileIO).
				Context("operation", "delete_audio_file").
				Context("handler", handlerName).
				Context("audio_path", audioPath).
				Build()
			h.Debug("Failed to delete audio file %s: %v", audioPath, enhancedErr)
		}

		// Delete spectrogram file with telemetry
		spectrogramPath := fmt.Sprintf("%s/%s.png", h.Settings.Realtime.Audio.Export.Path, strings.TrimSuffix(clipPath, ".wav"))
		spectrogramDeleteStart := time.Now()
		spectrogramErr := os.Remove(spectrogramPath)
		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationDuration(handlerName, "delete_spectrogram_file", time.Since(spectrogramDeleteStart).Seconds())
			if spectrogramErr != nil && !os.IsNotExist(spectrogramErr) {
				h.Telemetry.RecordHandlerOperationError(handlerName, "delete_spectrogram_file", "file_io")
			} else {
				h.Telemetry.RecordHandlerOperation(handlerName, "delete_spectrogram_file", "success")
			}
		}
		if spectrogramErr != nil && !os.IsNotExist(spectrogramErr) {
			enhancedErr := errors.New(spectrogramErr).
				Component("http-controller").
				Category(errors.CategoryFileIO).
				Context("operation", "delete_spectrogram_file").
				Context("handler", handlerName).
				Context("spectrogram_path", spectrogramPath).
				Build()
			h.Debug("Failed to delete spectrogram file %s: %v", spectrogramPath, enhancedErr)
		}
	}

	// Log the successful deletion
	h.Debug("Successfully deleted detection %s", id)

	// Send success notification
	h.SSE.SendNotification(Notification{
		Message: "Detection deleted successfully",
		Type:    "success",
	})

	// Record successful operation completion
	if h.Telemetry != nil {
		totalElapsed := time.Since(operationStart).Seconds()
		h.Telemetry.RecordHandlerOperationDuration(handlerName, "complete_request", totalElapsed)
		h.Telemetry.RecordHandlerOperation(handlerName, "complete_request", "success")
	}

	// Set response header to refresh list
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}

// handleSpeciesExclusion handles the logic for managing species in the exclusion list
func (h *Handlers) handleSpeciesExclusion(note *datastore.Note, verified, ignoreSpecies string) error {
	settings := conf.Setting()

	if verified == "false_positive" && ignoreSpecies != "" {
		// Check if species is already excluded
		for _, s := range settings.Realtime.Species.Exclude {
			if s == ignoreSpecies {
				return nil
			}
		}

		// Add to excluded list
		settings.Realtime.Species.Exclude = append(settings.Realtime.Species.Exclude, ignoreSpecies)
		if err := conf.SaveSettings(); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}

		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("%s added to ignore list", ignoreSpecies),
			Type:    "success",
		})
	} else if verified == "correct" {
		// Check if species is in exclude list
		for _, s := range settings.Realtime.Species.Exclude {
			if s == note.CommonName {
				h.SSE.SendNotification(Notification{
					Message: fmt.Sprintf("%s is currently in ignore list. You may want to remove it from Settings.", note.CommonName),
					Type:    "warning",
				})
				break
			}
		}
	}
	return nil
}

// processComment handles saving or updating a comment for a note
func (h *Handlers) processComment(noteID uint, comment string, maxRetries int, baseDelay time.Duration) error {
	if comment == "" {
		return nil
	}

	// Validate comment
	if len(comment) > 1000 {
		h.SSE.SendNotification(Notification{
			Message: "Comment exceeds maximum length of 1000 characters",
			Type:    "error",
		})
		return fmt.Errorf("comment too long")
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := h.DS.Transaction(func(tx *gorm.DB) error {
			// Get existing comments
			var existingComments []datastore.NoteComment
			if err := tx.Where("note_id = ?", noteID).Find(&existingComments).Error; err != nil {
				return fmt.Errorf("failed to check existing comments: %w", err)
			}

			// If there are existing comments, update the first one
			if len(existingComments) > 0 {
				existingComments[0].Entry = comment
				existingComments[0].UpdatedAt = time.Now()
				if err := tx.Save(&existingComments[0]).Error; err != nil {
					return fmt.Errorf("failed to update comment: %w", err)
				}
			} else {
				// Create new comment
				newComment := &datastore.NoteComment{
					NoteID:    noteID,
					Entry:     comment,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}
				if err := tx.Create(newComment).Error; err != nil {
					return fmt.Errorf("failed to save comment: %w", err)
				}
			}
			return nil
		})

		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				h.Debug("Database locked during comment save, retrying in %v (attempt %d/%d)", delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = err
				continue
			}
			return err
		}

		// If we get here, the transaction was successful
		return nil
	}

	return lastErr
}

// processReview handles the review status update and related operations
// API: POST /api/v1/detections/review
func (h *Handlers) processReview(noteID uint, verified string, lockDetection bool, maxRetries int, baseDelay time.Duration) error {
	h.Debug("processReview: Starting review process for note ID %d", noteID)
	h.Debug("processReview: Verified status: %s", verified)
	h.Debug("processReview: Lock detection: %v", lockDetection)

	// Validate review status if provided
	if verified != "" && verified != "correct" && verified != "false_positive" {
		return fmt.Errorf("invalid verification status")
	}

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		err := h.DS.Transaction(func(tx *gorm.DB) error {
			// Get note and existing review in a transaction
			note, err := h.DS.Get(strconv.FormatUint(uint64(noteID), 10))
			if err != nil {
				h.Debug("processReview: Failed to retrieve note: %v", err)
				return fmt.Errorf("failed to retrieve note: %w", err)
			}
			h.Debug("processReview: Retrieved note, current lock status: %v", note.Lock != nil)

			// Check if note is locked and trying to mark as false positive
			if note.Lock != nil && verified == "false_positive" {
				h.Debug("processReview: Attempt to mark locked note as false positive")
				return fmt.Errorf("cannot mark locked detection as false positive - unlock it first")
			}

			// Only process review status if it's provided
			if verified != "" {
				// Handle species exclusion
				if err := h.handleSpeciesExclusion(&note, verified, ""); err != nil {
					h.Debug("processReview: Failed to handle species exclusion: %v", err)
					return err
				}

				// Create or update the review using upsert
				review := &datastore.NoteReview{
					NoteID:    noteID,
					Verified:  verified,
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				}

				// Use upsert operation for the review
				if err := tx.Where("note_id = ?", noteID).
					Assign(*review).
					FirstOrCreate(review).Error; err != nil {
					h.Debug("processReview: Failed to save review: %v", err)
					return fmt.Errorf("failed to save review: %w", err)
				}
				h.Debug("processReview: Review saved successfully")
			}

			// Handle lock state changes
			if verified == "correct" || verified == "" { // Also handle lock changes when no review status is provided
				h.Debug("processReview: Handling lock state, lockDetection=%v", lockDetection)
				if lockDetection {
					// Lock the detection
					lock := &datastore.NoteLock{
						NoteID:   noteID,
						LockedAt: time.Now(),
					}

					// Use upsert operation within the same transaction
					if err := tx.Where("note_id = ?", noteID).
						Assign(*lock).
						FirstOrCreate(lock).Error; err != nil {
						h.Debug("processReview: Failed to lock note: %v", err)
						return fmt.Errorf("failed to lock note: %w", err)
					}
					h.Debug("processReview: Note locked successfully")
				} else {
					// Remove lock if exists
					if err := tx.Where("note_id = ?", noteID).Delete(&datastore.NoteLock{}).Error; err != nil {
						h.Debug("processReview: Failed to unlock note: %v", err)
						return fmt.Errorf("failed to unlock note: %w", err)
					}
					h.Debug("processReview: Note unlocked successfully")
				}
			}

			return nil
		})

		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				h.Debug("processReview: Database locked, retrying in %v (attempt %d/%d)", delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = err
				continue
			}
			return err
		}

		// If we get here, the transaction was successful
		h.Debug("processReview: Transaction completed successfully")
		return nil
	}

	h.Debug("processReview: All attempts failed, last error: %v", lastErr)
	return lastErr
}

// ReviewDetection handles the review status update and related operations
// API: POST /api/v1/detections/review
func (h *Handlers) ReviewDetection(c echo.Context) error {
	id := c.FormValue("id")
	if id == "" {
		return h.NewHandlerError(fmt.Errorf("no ID provided"), "Missing detection ID", http.StatusBadRequest)
	}

	noteID, err := strconv.ParseUint(id, 10, 32)
	if err != nil {
		return h.NewHandlerError(err, "Invalid detection ID format", http.StatusBadRequest)
	}

	comment := c.FormValue("comment")
	verified := c.FormValue("verified")
	lockDetection := c.FormValue("lock_detection") == "true"
	ignoreSpecies := c.FormValue("ignore_species")

	h.Debug("ReviewDetection: Processing review for note ID %d", noteID)
	h.Debug("ReviewDetection: Verified status: %s", verified)
	h.Debug("ReviewDetection: Lock detection: %v", lockDetection)
	h.Debug("ReviewDetection: Lock detection form value: %s", c.FormValue("lock_detection"))
	h.Debug("ReviewDetection: Ignore species: %s", ignoreSpecies)

	// Retry configuration for SQLite locking
	maxRetries := 5
	baseDelay := 100 * time.Millisecond

	// Handle comment first (this doesn't require review status)
	if err := h.processComment(uint(noteID), comment, maxRetries, baseDelay); err != nil {
		h.Debug("ReviewDetection: Failed to process comment: %v", err)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to process comment: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to process comment", http.StatusInternalServerError)
	}

	// Handle species exclusion if provided
	if verified == "false_positive" && ignoreSpecies != "" {
		h.Debug("ReviewDetection: Processing species exclusion for %s", ignoreSpecies)
		if err := h.handleSpeciesExclusion(nil, verified, ignoreSpecies); err != nil {
			h.Debug("ReviewDetection: Failed to handle species exclusion: %v", err)
			h.SSE.SendNotification(Notification{
				Message: fmt.Sprintf("Failed to handle species exclusion: %v", err),
				Type:    "error",
			})
			return h.NewHandlerError(err, "Failed to handle species exclusion", http.StatusInternalServerError)
		}
	}

	// Handle review status if provided
	if err := h.processReview(uint(noteID), verified, lockDetection, maxRetries, baseDelay); err != nil {
		h.Debug("ReviewDetection: Failed to process review: %v", err)
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to process review: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to process review", http.StatusInternalServerError)
	}

	if verified != "" {
		// Send success notification
		h.SSE.SendNotification(Notification{
			Message: "Review saved successfully",
			Type:    "success",
		})
	}
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}

// LockDetection handles the locking and unlocking of detections
// API: POST /api/v1/detections/lock
func (h *Handlers) LockDetection(c echo.Context) error {
	id := c.QueryParam("id")
	if id == "" {
		h.SSE.SendNotification(Notification{
			Message: "Missing detection ID",
			Type:    "error",
		})
		return h.NewHandlerError(fmt.Errorf("no ID provided"), "Missing detection ID", http.StatusBadRequest)
	}

	// Get the current note first to have the species name for messages
	note, err := h.DS.Get(id)
	if err != nil {
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to get detection: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to get detection", http.StatusInternalServerError)
	}

	// Retry configuration for SQLite locking
	maxRetries := 5
	baseDelay := 500 * time.Millisecond

	var message string
	var lastErr error

	// Check current lock status first
	isLocked, err := h.DS.IsNoteLocked(id)
	if err != nil {
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to check lock status: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to check lock status", http.StatusInternalServerError)
	}

	// Attempt the lock operation with retries
	for attempt := 0; attempt < maxRetries; attempt++ {
		var err error
		if isLocked {
			err = h.DS.UnlockNote(id)
			if err == nil {
				message = fmt.Sprintf("Detection of %s unlocked successfully", note.CommonName)
			}
		} else {
			err = h.DS.LockNote(id)
			if err == nil {
				message = fmt.Sprintf("Detection of %s locked successfully", note.CommonName)
			}
		}

		if err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				h.Debug("Database locked during lock operation, retrying in %v (attempt %d/%d)", delay, attempt+1, maxRetries)
				time.Sleep(delay)
				lastErr = err
				continue
			}
			h.SSE.SendNotification(Notification{
				Message: err.Error(),
				Type:    "error",
			})
			return h.NewHandlerError(err, "Failed to process lock operation", http.StatusInternalServerError)
		}

		// If we get here, the operation was successful
		break
	}

	// Check if all retries failed
	if lastErr != nil {
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed after %d attempts: %v", maxRetries, lastErr),
			Type:    "error",
		})
		return h.NewHandlerError(lastErr, "Failed to process lock operation after retries", http.StatusInternalServerError)
	}

	// Send success notification
	h.SSE.SendNotification(Notification{
		Message: message,
		Type:    "success",
	})

	// Set response header to refresh list
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}

// executeDetectionQuery executes the appropriate database query based on the request type
func (h *Handlers) executeDetectionQuery(req *DetectionRequest, handlerName string) ([]datastore.Note, int64, error) {
	var notes []datastore.Note
	var totalResults int64
	var err error

	switch req.QueryType {
	case "hourly":
		if req.Date == "" || req.Hour == "" {
			enhancedErr := errors.New(fmt.Errorf("missing date or hour parameters")).
				Component("http-controller").
				Category(errors.CategoryValidation).
				Context("operation", "validate_hourly_request").
				Context("handler", handlerName).
				Context("date", req.Date).
				Context("hour", req.Hour).
				Build()

			if h.Telemetry != nil {
				h.Telemetry.RecordHandlerOperationError(handlerName, "validate_hourly_request", "validation")
			}
			return nil, 0, h.NewHandlerError(enhancedErr, "Date and hour parameters are required for hourly detections", http.StatusBadRequest)
		}

		notes, totalResults, err = h.getHourlyDetections(req, handlerName)

	case "species":
		if req.Species == "" {
			enhancedErr := errors.New(fmt.Errorf("missing species parameter")).
				Component("http-controller").
				Category(errors.CategoryValidation).
				Context("operation", "validate_species_request").
				Context("handler", handlerName).
				Build()

			if h.Telemetry != nil {
				h.Telemetry.RecordHandlerOperationError(handlerName, "validate_species_request", "validation")
			}
			return nil, 0, h.NewHandlerError(enhancedErr, "Species parameter is required for species detections", http.StatusBadRequest)
		}

		notes, totalResults, err = h.getSpeciesDetections(req, handlerName)

	case "search":
		if req.Search == "" {
			enhancedErr := errors.New(fmt.Errorf("missing search query parameter")).
				Component("http-controller").
				Category(errors.CategoryValidation).
				Context("operation", "validate_search_request").
				Context("handler", handlerName).
				Build()

			if h.Telemetry != nil {
				h.Telemetry.RecordHandlerOperationError(handlerName, "validate_search_request", "validation")
			}
			return nil, 0, h.NewHandlerError(enhancedErr, "Search query is required for search detections", http.StatusBadRequest)
		}

		notes, totalResults, err = h.getSearchDetections(req, handlerName)

	default:
		enhancedErr := errors.Newf("invalid query type: %s", req.QueryType).
			Component("http-controller").
			Category(errors.CategoryValidation).
			Context("operation", "validate_query_type").
			Context("handler", handlerName).
			Context("query_type", req.QueryType).
			Build()

		if h.Telemetry != nil {
			h.Telemetry.RecordHandlerOperationError(handlerName, "validate_query_type", "validation")
		}
		return nil, 0, h.NewHandlerError(enhancedErr, "Invalid query type specified", http.StatusBadRequest)
	}

	return notes, totalResults, err
}

// getHourlyDetections retrieves hourly detections with telemetry
func (h *Handlers) getHourlyDetections(req *DetectionRequest, handlerName string) ([]datastore.Note, int64, error) {
	// Get hourly detections with telemetry
	dbStart := time.Now()
	notes, err := h.DS.GetHourlyDetections(req.Date, req.Hour, req.Duration, req.NumResults, req.Offset)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "get_hourly_detections", time.Since(dbStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "get_hourly_detections").
			Context("handler", handlerName).
			Context("date", req.Date).
			Context("hour", req.Hour).
			Context("duration", req.Duration).
			Build()
		return nil, 0, h.NewHandlerError(enhancedErr, "Failed to get hourly detections", http.StatusInternalServerError)
	}

	// Count hourly detections with telemetry
	countStart := time.Now()
	totalResults, err := h.DS.CountHourlyDetections(req.Date, req.Hour, req.Duration)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "count_hourly_detections", time.Since(countStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "count_hourly_detections").
			Context("handler", handlerName).
			Context("date", req.Date).
			Context("hour", req.Hour).
			Build()
		return nil, 0, h.NewHandlerError(enhancedErr, "Failed to count hourly detections", http.StatusInternalServerError)
	}

	return notes, totalResults, nil
}

// getSpeciesDetections retrieves species detections with telemetry
func (h *Handlers) getSpeciesDetections(req *DetectionRequest, handlerName string) ([]datastore.Note, int64, error) {
	// Get species detections with telemetry
	dbStart := time.Now()
	notes, err := h.DS.SpeciesDetections(req.Species, req.Date, req.Hour, req.Duration, false, req.NumResults, req.Offset)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "get_species_detections", time.Since(dbStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "get_species_detections").
			Context("handler", handlerName).
			Context("species", req.Species).
			Build()
		return nil, 0, h.NewHandlerError(enhancedErr, "Failed to get species detections", http.StatusInternalServerError)
	}

	// Count species detections with telemetry
	countStart := time.Now()
	totalResults, err := h.DS.CountSpeciesDetections(req.Species, req.Date, req.Hour, req.Duration)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "count_species_detections", time.Since(countStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "count_species_detections").
			Context("handler", handlerName).
			Context("species", req.Species).
			Build()
		// Count errors are non-fatal, log but continue
		log.Printf("Warning: Failed to count species detections: %v", enhancedErr)
		totalResults = int64(len(notes)) // Fallback to current result count
	}

	return notes, totalResults, nil
}

// getSearchDetections retrieves search detections with telemetry
func (h *Handlers) getSearchDetections(req *DetectionRequest, handlerName string) ([]datastore.Note, int64, error) {
	// Search notes with telemetry
	dbStart := time.Now()
	notes, err := h.DS.SearchNotes(req.Search, false, req.NumResults, req.Offset)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "search_notes", time.Since(dbStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "search_notes").
			Context("handler", handlerName).
			Context("search_query", req.Search).
			Build()
		return nil, 0, h.NewHandlerError(enhancedErr, "Failed to search notes", http.StatusInternalServerError)
	}

	// Count search results with telemetry
	countStart := time.Now()
	totalResults, err := h.DS.CountSearchResults(req.Search)
	if h.Telemetry != nil {
		h.Telemetry.RecordDatabaseOperation(handlerName, "count_search_results", time.Since(countStart), err)
	}
	if err != nil {
		enhancedErr := errors.New(err).
			Component("http-controller").
			Category(errors.CategoryDatabase).
			Context("operation", "count_search_results").
			Context("handler", handlerName).
			Context("search_query", req.Search).
			Build()
		// Count errors are non-fatal, log but continue
		log.Printf("Warning: Failed to count search results: %v", enhancedErr)
		totalResults = int64(len(notes)) // Fallback to current result count
	}

	return notes, totalResults, nil
}
