package handlers

import (
	"fmt"
	"log"
	"math"
	"net/http"
	"os"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
	QueryType  string `query:"queryType"` // "hourly", "species", "search", "locked", or "all"
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
	case "locked":
		// Get all locked notes
		allLockedNotes, err := h.DS.GetAllLockedNotes()
		if err != nil {
			return h.NewHandlerError(err, "Failed to get locked detections", http.StatusInternalServerError)
		}
		
		 // Filter notes by search query if provided
		var filteredNotes []datastore.Note
		if req.Search != "" {
			h.Debug("Filtering locked detections with search term: %q", req.Search)
			// Case insensitive search across multiple fields
			searchLower := strings.ToLower(req.Search)
			for _, note := range allLockedNotes {
				// Search in common name, scientific name, date and time
				if strings.Contains(strings.ToLower(note.CommonName), searchLower) ||
				   strings.Contains(strings.ToLower(note.ScientificName), searchLower) ||
				   strings.Contains(strings.ToLower(note.Date), searchLower) ||
				   strings.Contains(strings.ToLower(note.Time), searchLower) ||
				   strings.Contains(strings.ToLower(note.ClipName), searchLower) {
					filteredNotes = append(filteredNotes, note)
				}
			}
			h.Debug("Found %d locked detections matching search %q out of %d total", 
					len(filteredNotes), req.Search, len(allLockedNotes))
		} else {
			filteredNotes = allLockedNotes
		}

		// Sort the notes by date and time with newest on top
		sort.Slice(filteredNotes, func(i, j int) bool {
			dateTimeI := filteredNotes[i].Date + " " + filteredNotes[i].Time
			dateTimeJ := filteredNotes[j].Date + " " + filteredNotes[j].Time
			return dateTimeI > dateTimeJ // Descending order (newest first)
		})

		totalResults = int64(len(filteredNotes))

		// Apply pagination - calculate end index
		end := req.Offset + req.NumResults
		if end > len(filteredNotes) {
			end = len(filteredNotes)
		}

		// Extract paginated subset
		if req.Offset < len(filteredNotes) {
			notes = filteredNotes[req.Offset:end]
		} else {
			notes = []datastore.Note{}
		}
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

	// Calculate previous and next page offsets
	previousOffset := req.Offset - req.NumResults
	if previousOffset < 0 {
		previousOffset = 0
	}
	nextOffset := req.Offset + req.NumResults

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
		PreviousOffset    int
		NextOffset        int
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
		PreviousOffset:    previousOffset,
		NextOffset:        nextOffset,
		Security: map[string]interface{}{
			"Enabled":       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
			"AccessAllowed": h.Server.IsAccessAllowed(c),
		},
	}

	// Render the list detections template with the data
	err = c.Render(http.StatusOK, "listDetections", data)
	if err != nil {
		log.Printf("Failed to render listDetections template: %v", err)
		return h.NewHandlerError(err, "Failed to render template", http.StatusInternalServerError)
	}
	return nil
}

// DetectionDetails retrieves a single detection from the database and renders it.
// API: GET /api/v1/detections/details
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
// API: GET /api/v1/detections/recent
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
		Security          map[string]interface{}
	}{
		Notes:             notes,
		DashboardSettings: *h.DashboardSettings,
		Security: map[string]interface{}{
			"Enabled":       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
			"AccessAllowed": h.Server.IsAccessAllowed(c),
		},
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
// API: DELETE /api/v1/detections/delete
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

// LockedFilesHandler renders a page showing all locked detections
// API: GET /locked-detections
func (h *Handlers) LockedFilesHandler(c echo.Context) error {
    h.Debug("LockedFilesHandler called from %s with method %s", c.Request().URL.String(), c.Request().Method)
    h.Debug("LockedFilesHandler: Request headers: %v", c.Request().Header)
    
    isApiRequest := strings.HasPrefix(c.Path(), "/api/")
    isHtmxRequest := c.Request().Header.Get("HX-Request") == "true"
    
    if isApiRequest || isHtmxRequest {
        // Set up request parameters
        req := new(DetectionRequest)
        numResultsStr := c.QueryParam("numResults")
        offsetStr := c.QueryParam("offset")
        searchQuery := c.QueryParam("search")
        
        h.Debug("LockedFilesHandler: search query = %q", searchQuery)
        
        // Set default values
        req.QueryType = "locked"
        req.NumResults = 20
        req.Search = searchQuery
        
        // Parse parameters
        if numResultsStr != "" {
            if parsed, err := strconv.Atoi(numResultsStr); err == nil && parsed > 0 {
                req.NumResults = parsed
            }
        }
        
        if offsetStr != "" {
            if parsed, err := strconv.Atoi(offsetStr); err == nil {
                req.Offset = parsed
            }
        }
        
        // Get all locked notes
        h.Debug("LockedFilesHandler: Fetching ALL locked notes directly")
        allLockedNotes, err := h.DS.GetAllLockedNotes()
        if err != nil {
            h.Debug("LockedFilesHandler: Error fetching locked notes: %v", err)
            return h.NewHandlerError(err, "Failed to get locked detections", http.StatusInternalServerError)
        }
        
        // Filter by search query if provided
        var filteredNotes []datastore.Note
        if searchQuery != "" {
            h.Debug("LockedFilesHandler: Filtering with search term: %q", searchQuery)
            searchLower := strings.ToLower(searchQuery)
            for _, note := range allLockedNotes {
                if strings.Contains(strings.ToLower(note.CommonName), searchLower) ||
                   strings.Contains(strings.ToLower(note.ScientificName), searchLower) ||
                   strings.Contains(strings.ToLower(note.Date), searchLower) ||
                   strings.Contains(strings.ToLower(note.Time), searchLower) ||
                   strings.Contains(strings.ToLower(note.ClipName), searchLower) {
                    filteredNotes = append(filteredNotes, note)
                }
            }
            h.Debug("LockedFilesHandler: Found %d notes matching search %q", len(filteredNotes), searchQuery)
        } else {
            filteredNotes = allLockedNotes
        }
        
        // Sort notes by date and time, newest first
        sort.Slice(filteredNotes, func(i, j int) bool {
            dateTimeI := filteredNotes[i].Date + " " + filteredNotes[i].Time
            dateTimeJ := filteredNotes[j].Date + " " + filteredNotes[j].Time
            return dateTimeI > dateTimeJ
        })
        
        totalResults := int64(len(filteredNotes))
        h.Debug("LockedFilesHandler: Total filtered notes: %d", totalResults)
        
        // Apply pagination
        var notes []datastore.Note
        start := req.Offset
        end := req.Offset + req.NumResults
        
        if start < len(filteredNotes) {
            if end > len(filteredNotes) {
                end = len(filteredNotes)
            }
            notes = filteredNotes[start:end]
            h.Debug("LockedFilesHandler: Paginated notes: %d (range %d:%d)", len(notes), start, end)
        } else {
            notes = []datastore.Note{}
            h.Debug("LockedFilesHandler: No notes after pagination")
        }
        
        
        // Add weather and time of day information
        notesWithWeather, err := h.addWeatherAndTimeOfDay(notes)
        if err != nil {
            h.Debug("LockedFilesHandler: Error adding weather data: %v", err)
            return h.NewHandlerError(err, "Failed to add weather data", http.StatusInternalServerError)
        }
        
        // Calculate pagination info
        currentPage := (req.Offset / req.NumResults) + 1
        totalPages := int(math.Ceil(float64(totalResults) / float64(req.NumResults)))
        showingFrom := req.Offset + 1
        if totalResults == 0 {
            showingFrom = 0
        }
        showingTo := req.Offset + len(notes)
        if showingTo > int(totalResults) {
            showingTo = int(totalResults)
        }
        
        // Calculate previous and next page offsets
        previousOffset := req.Offset - req.NumResults
        if previousOffset < 0 {
            previousOffset = 0
        }
        nextOffset := req.Offset + req.NumResults
        
        // Check if weather provider is set
        weatherEnabled := h.Settings.Realtime.Weather.Provider != "none"
        
        // Create data structure compatible with listDetections template
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
            PreviousOffset    int
            NextOffset        int
            Security          map[string]interface{}
        }{
            QueryType:         "locked",
            Notes:             notesWithWeather,
            NumResults:        req.NumResults,
            Offset:            req.Offset,
            Search:            searchQuery,
            DashboardSettings: h.DashboardSettings,
            TotalResults:      totalResults,
            CurrentPage:       currentPage,
            TotalPages:        totalPages,
            ShowingFrom:       showingFrom,
            ShowingTo:         showingTo,
            ItemsPerPage:      20,
            WeatherEnabled:    weatherEnabled,
            PreviousOffset:    previousOffset,
            NextOffset:        nextOffset,
            Security: map[string]interface{}{
                "Enabled":       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
                "AccessAllowed": h.Server.IsAccessAllowed(c),
            },
        }
        
        h.Debug("LockedFilesHandler: Rendering template with %d notes", len(notesWithWeather))
        return c.Render(http.StatusOK, "listDetections", data)
    }
    
    // For regular page loads
    return c.Render(http.StatusOK, "index", echo.Map{
        "Title":       "Locked Detections",
        "Page":        "locked-detections",
        "ActiveMenu":  "lockeddetections",
        "Settings":    h.Settings,
        "ShowFooter":  true,
        "Security": map[string]interface{}{
            "Enabled":       h.Settings.Security.BasicAuth.Enabled || h.Settings.Security.GoogleAuth.Enabled || h.Settings.Security.GithubAuth.Enabled,
            "AccessAllowed": h.Server.IsAccessAllowed(c),
        },
    })
}
