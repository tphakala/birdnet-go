package handlers

import (
	"fmt"
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
	"github.com/tphakala/birdnet-go/internal/logger"
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
	req := new(DetectionRequest)
	if err := c.Bind(req); err != nil {
		return h.NewHandlerError(err, "Invalid request parameters", http.StatusBadRequest)
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

	// Determine the type of query to execute based on the QueryType parameter
	switch req.QueryType {
	case "hourly":
		if req.Date == "" || req.Hour == "" {
			return h.NewHandlerError(fmt.Errorf("missing date or hour"), "Date and hour parameters are required for hourly detections", http.StatusBadRequest)
		}
		notes, err = h.DS.GetHourlyDetections(req.Date, req.Hour, req.Duration, req.NumResults, req.Offset)
		if err != nil {
			return h.NewHandlerError(err, "Failed to get hourly detections", http.StatusInternalServerError)
		}
		totalResults, err = h.DS.CountHourlyDetections(req.Date, req.Hour, req.Duration)
		if err != nil {
			return h.NewHandlerError(err, "Failed to count hourly detections", http.StatusInternalServerError)
		}
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
			"IsCloudflare":  h.CloudflareAccess.IsEnabled(c),
		},
	}

	// Render the list detections template with the data
	err = c.Render(http.StatusOK, "listDetections", data)
	if err != nil {
		detectionLogger := h.getDetectionLogger("list")
		if detectionLogger != nil {
			detectionLogger.Error("Failed to render listDetections template", "error", err)
		}
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}
	return nil
}

// DetectionDetails retrieves a single detection from the database and renders it.
// API: GET /api/v1/detections/details
func (h *Handlers) DetectionDetails(c echo.Context) error {
	// Get a component-specific logger
	detailsLogger := h.getDetectionLogger("details")

	noteID := c.QueryParam("id")
	if noteID == "" {
		if detailsLogger != nil {
			detailsLogger.Warn("Empty note ID provided")
		}
		return h.NewHandlerError(fmt.Errorf("empty note ID"), "Note ID is required", http.StatusBadRequest)
	}

	// Retrieve the note from the database
	note, err := h.DS.Get(noteID)
	if err != nil {
		if detailsLogger != nil {
			detailsLogger.Error("Failed to retrieve note",
				"note_id", noteID,
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to retrieve note", http.StatusInternalServerError)
	}

	// set spectrogram width, height will be /2
	const width = 1000 // pixels

	// Generate the spectrogram path for the note
	spectrogramPath, err := h.getSpectrogramPath(note.ClipName, width)
	if err != nil {
		if detailsLogger != nil {
			detailsLogger.Error("Error generating spectrogram",
				"clip_name", note.ClipName,
				"error", err)
		}
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
		detectionLogger := h.getDetectionLogger("details")
		if detectionLogger != nil {
			detectionLogger.Error("Failed to render detectionDetails template", "error", err)
		}
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}

	if detailsLogger != nil && h.debug {
		detailsLogger.Debug("Successfully rendered detection details",
			"note_id", noteID,
			"common_name", note.CommonName)
	}
	return nil
}

// getDetectionLogger returns a component-specific logger for detection operations
func (h *Handlers) getDetectionLogger(subComponent string) *logger.Logger {
	if h.Logger == nil {
		return nil
	}

	loggerName := "detections"
	if subComponent != "" {
		loggerName += "." + subComponent
	}

	return h.Logger.Named(loggerName)
}

// Debug is a helper method for logging debug messages
// Deprecated: Use structured logging methods instead
func (h *Handlers) Debug(format string, v ...interface{}) {
	if h.debug {
		if h.Logger != nil {
			// When using structured logging, convert the format string to a message
			// and add any arguments as context fields
			if len(v) == 0 {
				h.Logger.Debug(format)
			} else {
				// For backward compatibility, format the message
				formattedMsg := fmt.Sprintf(format, v...)
				h.Logger.Debug(formattedMsg)
			}
		}
	}
}

// RecentDetections handles requests for the latest detections.
// API: GET /api/v1/detections/recent
func (h *Handlers) RecentDetections(c echo.Context) error {
	// Get a component-specific logger
	recentLogger := h.getDetectionLogger("recent")

	if recentLogger != nil && h.debug {
		recentLogger.Debug("Starting handler")
	}

	numDetections := parseNumDetections(c.QueryParam("numDetections"), 10)

	if recentLogger != nil && h.debug {
		recentLogger.Debug("Fetching detections", "count", numDetections)
	}

	notes, err := h.DS.GetLastDetections(numDetections)
	if err != nil {
		if recentLogger != nil {
			recentLogger.Error("Failed to fetch detections", "error", err)
		}
		return h.NewHandlerError(err, "Failed to fetch recent detections", http.StatusInternalServerError)
	}

	if recentLogger != nil && h.debug {
		recentLogger.Debug("Found detections", "count", len(notes))
	}

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
			"IsCloudflare":  h.CloudflareAccess.IsEnabled(c),
		},
	}

	if recentLogger != nil && h.debug {
		recentLogger.Debug("Rendering template")
	}

	err = c.Render(http.StatusOK, "recentDetections", data)
	if err != nil {
		if recentLogger != nil {
			recentLogger.Error("Failed to render template",
				"template", "recentDetections",
				"error", err)
		}
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}

	if recentLogger != nil && h.debug {
		recentLogger.Debug("Successfully completed")
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
			detectionLogger := h.getDetectionLogger("weather")
			if detectionLogger != nil {
				detectionLogger.Error("Failed to get sun events",
					"date", notes[i].Date,
					"error", err)
			}
			notesWithWeather[i] = NoteWithWeather{
				Note:      notes[i],
				Weather:   nil,
				TimeOfDay: weather.Day,
			}
			continue
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
					detectionLogger := h.getDetectionLogger("weather")
					if detectionLogger != nil {
						detectionLogger.Error("Failed to fetch hourly weather",
							"date", notes[i].Date,
							"error", err)
					}
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
	// Get a component-specific logger
	deleteLogger := h.getDetectionLogger("delete")

	id := c.QueryParam("id")

	if id == "" {
		if deleteLogger != nil {
			deleteLogger.Warn("Missing detection ID")
		}
		h.SSE.SendNotification(Notification{
			Message: "Missing detection ID",
			Type:    "error",
		})
		return h.NewHandlerError(fmt.Errorf("no ID provided"), "Missing detection ID", http.StatusBadRequest)
	}

	// Get the clip path before deletion
	clipPath, err := h.DS.GetNoteClipPath(id)
	if err != nil {
		if deleteLogger != nil {
			deleteLogger.Error("Failed to get clip path",
				"detection_id", id,
				"error", err)
		}
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to get clip path: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to get clip path", http.StatusInternalServerError)
	}

	// Delete the note from the database
	if err := h.DS.Delete(id); err != nil {
		if deleteLogger != nil {
			deleteLogger.Error("Failed to delete note",
				"detection_id", id,
				"error", err)
		}
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
			if deleteLogger != nil {
				deleteLogger.Warn("Failed to delete audio file",
					"path", audioPath,
					"error", err)
			}
		}

		// Delete spectrogram file
		spectrogramPath := fmt.Sprintf("%s/%s.png", h.Settings.Realtime.Audio.Export.Path, strings.TrimSuffix(clipPath, ".wav"))
		if err := os.Remove(spectrogramPath); err != nil && !os.IsNotExist(err) {
			if deleteLogger != nil {
				deleteLogger.Warn("Failed to delete spectrogram file",
					"path", spectrogramPath,
					"error", err)
			}
		}
	}

	// Log the successful deletion
	if deleteLogger != nil {
		deleteLogger.Info("Successfully deleted detection", "detection_id", id)
	}

	// Send success notification
	h.SSE.SendNotification(Notification{
		Message: "Detection deleted successfully",
		Type:    "success",
	})

	// Set response header to refresh list
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}

// handleSpeciesExclusion handles the logic for managing species in the exclusion list
func (h *Handlers) handleSpeciesExclusion(note *datastore.Note, verified, ignoreSpecies string) error {
	// Get a component-specific logger
	exclusionLogger := h.getDetectionLogger("species-exclusion")

	settings := conf.Setting()

	if verified == "false_positive" && ignoreSpecies != "" {
		if exclusionLogger != nil && h.debug {
			exclusionLogger.Debug("Processing species exclusion",
				"species", ignoreSpecies,
				"action", "add_to_exclude")
		}

		// Check if species is already excluded
		for _, s := range settings.Realtime.Species.Exclude {
			if s == ignoreSpecies {
				if exclusionLogger != nil && h.debug {
					exclusionLogger.Debug("Species already in exclusion list",
						"species", ignoreSpecies)
				}
				return nil
			}
		}

		// Add to excluded list
		settings.Realtime.Species.Exclude = append(settings.Realtime.Species.Exclude, ignoreSpecies)
		if err := conf.SaveSettings(); err != nil {
			if exclusionLogger != nil {
				exclusionLogger.Error("Failed to save settings",
					"species", ignoreSpecies,
					"error", err)
			}
			return fmt.Errorf("failed to save settings: %w", err)
		}

		if exclusionLogger != nil && h.debug {
			exclusionLogger.Debug("Species added to exclusion list",
				"species", ignoreSpecies)
		}
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("%s added to ignore list", ignoreSpecies),
			Type:    "success",
		})
	} else if verified == "correct" {
		// Check if species is in exclude list
		for _, s := range settings.Realtime.Species.Exclude {
			if s == note.CommonName {
				if exclusionLogger != nil {
					exclusionLogger.Warn("Species marked as correct but is in exclusion list",
						"species", note.CommonName)
				}
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

	// Get a component-specific logger for comment operations
	commentLogger := h.getDetectionLogger("comments")

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
				if commentLogger != nil {
					commentLogger.Warn("Database locked during comment save, retrying",
						"note_id", noteID,
						"attempt", attempt+1,
						"max_retries", maxRetries,
						"delay_ms", delay.Milliseconds())
				}
				time.Sleep(delay)
				lastErr = err
				continue
			}
			return err
		}

		// If we get here, the transaction was successful
		if commentLogger != nil && h.debug {
			commentLogger.Debug("Comment saved successfully",
				"note_id", noteID)
		}
		return nil
	}

	if commentLogger != nil {
		commentLogger.Error("Failed to save comment after retries",
			"note_id", noteID,
			"max_retries", maxRetries,
			"last_error", lastErr)
	}
	return lastErr
}

// processReview handles the review status update and related operations
// API: POST /api/v1/detections/review
func (h *Handlers) processReview(noteID uint, verified string, lockDetection bool, maxRetries int, baseDelay time.Duration) error {
	// Get a component-specific logger for review operations
	reviewLogger := h.getDetectionLogger("review")

	if reviewLogger != nil && h.debug {
		reviewLogger.Debug("Starting review process",
			"note_id", noteID,
			"verified", verified,
			"lock_detection", lockDetection)
	}

	// Validate review status if provided
	if verified != "" && verified != "correct" && verified != "false_positive" {
		return fmt.Errorf("invalid verification status")
	}

	return h.executeWithRetry(func() error {
		return h.updateNoteReviewAndLock(noteID, verified, lockDetection, reviewLogger)
	}, maxRetries, baseDelay, reviewLogger, noteID)
}

// executeWithRetry executes the provided function with retry logic for database locks
func (h *Handlers) executeWithRetry(operation func() error, maxRetries int, baseDelay time.Duration,
	logger *logger.Logger, noteID uint) error {

	var lastErr error
	for attempt := 0; attempt < maxRetries; attempt++ {
		if err := operation(); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "database is locked") {
				delay := baseDelay * time.Duration(attempt+1)
				if logger != nil {
					logger.Warn("Database locked, retrying",
						"note_id", noteID,
						"attempt", attempt+1,
						"max_retries", maxRetries,
						"delay_ms", delay.Milliseconds())
				}
				time.Sleep(delay)
				lastErr = err
				continue
			}
			return err
		}

		// If we get here, the operation was successful
		if logger != nil && h.debug {
			logger.Debug("Transaction completed successfully", "note_id", noteID)
		}
		return nil
	}

	if logger != nil {
		logger.Error("All attempts failed",
			"note_id", noteID,
			"max_retries", maxRetries,
			"last_error", lastErr)
	}
	return lastErr
}

// updateNoteReviewAndLock handles the database transaction for updating note review and lock status
func (h *Handlers) updateNoteReviewAndLock(noteID uint, verified string, lockDetection bool,
	reviewLogger *logger.Logger) error {

	return h.DS.Transaction(func(tx *gorm.DB) error {
		// Get note in a transaction
		note, err := h.DS.Get(strconv.FormatUint(uint64(noteID), 10))
		if err != nil {
			if reviewLogger != nil {
				reviewLogger.Error("Failed to retrieve note",
					"note_id", noteID,
					"error", err)
			}
			return fmt.Errorf("failed to retrieve note: %w", err)
		}

		if reviewLogger != nil && h.debug {
			reviewLogger.Debug("Retrieved note",
				"note_id", noteID,
				"is_locked", note.Lock != nil)
		}

		// Process verification status if provided
		if verified != "" {
			if err := h.processVerificationStatus(tx, &note, noteID, verified, reviewLogger); err != nil {
				return err
			}
		}

		// Process lock state if applicable
		if verified == "correct" || verified == "" {
			if err := h.processLockState(tx, noteID, lockDetection, reviewLogger); err != nil {
				return err
			}
		}

		return nil
	})
}

// processVerificationStatus handles the verification status update
func (h *Handlers) processVerificationStatus(tx *gorm.DB, note *datastore.Note, noteID uint,
	verified string, reviewLogger *logger.Logger) error {

	// Check if note is locked and trying to mark as false positive
	if note.Lock != nil && verified == "false_positive" {
		if reviewLogger != nil {
			reviewLogger.Warn("Attempt to mark locked note as false positive",
				"note_id", noteID,
				"common_name", note.CommonName)
		}
		return fmt.Errorf("cannot mark locked detection as false positive - unlock it first")
	}

	// Handle species exclusion
	if err := h.handleSpeciesExclusion(note, verified, ""); err != nil {
		if reviewLogger != nil {
			reviewLogger.Error("Failed to handle species exclusion",
				"common_name", note.CommonName,
				"error", err)
		}
		return err
	}

	// Create or update the review
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
		if reviewLogger != nil {
			reviewLogger.Error("Failed to save review",
				"note_id", noteID,
				"error", err)
		}
		return fmt.Errorf("failed to save review: %w", err)
	}

	if reviewLogger != nil && h.debug {
		reviewLogger.Debug("Review saved successfully", "note_id", noteID)
	}

	return nil
}

// processLockState handles the lock state update
func (h *Handlers) processLockState(tx *gorm.DB, noteID uint, lockDetection bool,
	reviewLogger *logger.Logger) error {

	if reviewLogger != nil && h.debug {
		reviewLogger.Debug("Handling lock state",
			"note_id", noteID,
			"lock_detection", lockDetection)
	}

	if lockDetection {
		return h.createLock(tx, noteID, reviewLogger)
	}
	return h.removeLock(tx, noteID, reviewLogger)
}

// createLock adds a lock to a note
func (h *Handlers) createLock(tx *gorm.DB, noteID uint, reviewLogger *logger.Logger) error {
	// Lock the detection
	lock := &datastore.NoteLock{
		NoteID:   noteID,
		LockedAt: time.Now(),
	}

	// Use upsert operation
	if err := tx.Where("note_id = ?", noteID).
		Assign(*lock).
		FirstOrCreate(lock).Error; err != nil {
		if reviewLogger != nil {
			reviewLogger.Error("Failed to lock note",
				"note_id", noteID,
				"error", err)
		}
		return fmt.Errorf("failed to lock note: %w", err)
	}

	if reviewLogger != nil && h.debug {
		reviewLogger.Debug("Note locked successfully", "note_id", noteID)
	}
	return nil
}

// removeLock removes a lock from a note
func (h *Handlers) removeLock(tx *gorm.DB, noteID uint, reviewLogger *logger.Logger) error {
	// Remove lock if exists
	if err := tx.Where("note_id = ?", noteID).Delete(&datastore.NoteLock{}).Error; err != nil {
		if reviewLogger != nil {
			reviewLogger.Error("Failed to unlock note",
				"note_id", noteID,
				"error", err)
		}
		return fmt.Errorf("failed to unlock note: %w", err)
	}

	if reviewLogger != nil && h.debug {
		reviewLogger.Debug("Note unlocked successfully", "note_id", noteID)
	}
	return nil
}

// ReviewDetection handles the review status update and related operations
// API: POST /api/v1/detections/review
func (h *Handlers) ReviewDetection(c echo.Context) error {
	// Get a component-specific logger
	reviewLogger := h.getDetectionLogger("review-handler")

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

	if reviewLogger != nil && h.debug {
		reviewLogger.Debug("Processing review",
			"note_id", noteID,
			"verified", verified,
			"lock_detection", lockDetection,
			"lock_detection_value", c.FormValue("lock_detection"),
			"ignore_species", ignoreSpecies)
	}

	// Retry configuration for SQLite locking
	maxRetries := 5
	baseDelay := 100 * time.Millisecond

	// Handle comment first (this doesn't require review status)
	if err := h.processComment(uint(noteID), comment, maxRetries, baseDelay); err != nil {
		if reviewLogger != nil {
			reviewLogger.Error("Failed to process comment",
				"note_id", noteID,
				"error", err)
		}
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to process comment: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to process comment", http.StatusInternalServerError)
	}

	// Handle species exclusion if provided
	if verified == "false_positive" && ignoreSpecies != "" {
		if reviewLogger != nil && h.debug {
			reviewLogger.Debug("Processing species exclusion",
				"species", ignoreSpecies)
		}

		if err := h.handleSpeciesExclusion(nil, verified, ignoreSpecies); err != nil {
			if reviewLogger != nil {
				reviewLogger.Error("Failed to handle species exclusion",
					"species", ignoreSpecies,
					"error", err)
			}
			h.SSE.SendNotification(Notification{
				Message: fmt.Sprintf("Failed to handle species exclusion: %v", err),
				Type:    "error",
			})
			return h.NewHandlerError(err, "Failed to handle species exclusion", http.StatusInternalServerError)
		}
	}

	// Handle review status if provided
	if err := h.processReview(uint(noteID), verified, lockDetection, maxRetries, baseDelay); err != nil {
		if reviewLogger != nil {
			reviewLogger.Error("Failed to process review",
				"note_id", noteID,
				"error", err)
		}
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to process review: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to process review", http.StatusInternalServerError)
	}

	if verified != "" {
		// Send success notification
		if reviewLogger != nil && h.debug {
			reviewLogger.Debug("Review saved successfully",
				"note_id", noteID,
				"verified", verified,
				"lock_detection", lockDetection)
		}
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
	// Get a component-specific logger
	lockLogger := h.getDetectionLogger("lock-handler")

	id := c.QueryParam("id")
	if id == "" {
		if lockLogger != nil {
			lockLogger.Error("Missing detection ID")
		}
		h.SSE.SendNotification(Notification{
			Message: "Missing detection ID",
			Type:    "error",
		})
		return h.NewHandlerError(fmt.Errorf("no ID provided"), "Missing detection ID", http.StatusBadRequest)
	}

	// Get the current note first to have the species name for messages
	note, err := h.DS.Get(id)
	if err != nil {
		if lockLogger != nil {
			lockLogger.Error("Failed to get detection",
				"id", id,
				"error", err)
		}
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
		if lockLogger != nil {
			lockLogger.Error("Failed to check lock status",
				"id", id,
				"error", err)
		}
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed to check lock status: %v", err),
			Type:    "error",
		})
		return h.NewHandlerError(err, "Failed to check lock status", http.StatusInternalServerError)
	}

	if lockLogger != nil && h.debug {
		lockLogger.Debug("Processing lock operation",
			"id", id,
			"species", note.CommonName,
			"current_lock_status", isLocked)
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
				if lockLogger != nil {
					lockLogger.Warn("Database locked during lock operation, retrying",
						"id", id,
						"attempt", attempt+1,
						"max_retries", maxRetries,
						"delay_ms", delay.Milliseconds())
				}
				time.Sleep(delay)
				lastErr = err
				continue
			}
			if lockLogger != nil {
				lockLogger.Error("Failed to process lock operation",
					"id", id,
					"is_locked", isLocked,
					"error", err)
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
		if lockLogger != nil {
			lockLogger.Error("Failed after all retries",
				"id", id,
				"max_retries", maxRetries,
				"last_error", lastErr)
		}
		h.SSE.SendNotification(Notification{
			Message: fmt.Sprintf("Failed after %d attempts: %v", maxRetries, lastErr),
			Type:    "error",
		})
		return h.NewHandlerError(lastErr, "Failed to process lock operation after retries", http.StatusInternalServerError)
	}

	// Send success notification
	if lockLogger != nil && h.debug {
		lockLogger.Debug("Lock operation successful",
			"id", id,
			"species", note.CommonName,
			"action", map[bool]string{true: "unlock", false: "lock"}[isLocked])
	}
	h.SSE.SendNotification(Notification{
		Message: message,
		Type:    "success",
	})

	// Set response header to refresh list
	c.Response().Header().Set("HX-Trigger", "refreshListEvent")

	return c.NoContent(http.StatusOK)
}
