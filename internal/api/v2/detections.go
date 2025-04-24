// internal/api/v2/detections.go
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/patrickmn/go-cache"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// initDetectionRoutes registers all detection-related API endpoints
func (c *Controller) initDetectionRoutes() {
	// Initialize the cache with a 5-minute default expiration and 10-minute cleanup interval
	c.detectionCache = cache.New(5*time.Minute, 10*time.Minute)

	// Detection endpoints - publicly accessible
	//
	// Note: Detection data is decoupled from weather data by design.
	// To get weather information for a specific detection, use the
	// /api/v2/weather/detection/:id endpoint after fetching the detection.
	c.Group.GET("/detections", c.GetDetections)
	c.Group.GET("/detections/:id", c.GetDetection)
	c.Group.GET("/detections/recent", c.GetRecentDetections)
	c.Group.GET("/detections/:id/time-of-day", c.GetDetectionTimeOfDay)

	// Protected detection management endpoints
	detectionGroup := c.Group.Group("/detections", c.AuthMiddleware)
	detectionGroup.DELETE("/:id", c.DeleteDetection)
	detectionGroup.POST("/:id/review", c.ReviewDetection)
	detectionGroup.POST("/:id/lock", c.LockDetection)
	detectionGroup.POST("/ignore", c.IgnoreSpecies)
}

// DetectionResponse represents a detection in the API response
type DetectionResponse struct {
	ID             uint     `json:"id"`
	Date           string   `json:"date"`
	Time           string   `json:"time"`
	Source         string   `json:"source"`
	BeginTime      string   `json:"beginTime"`
	EndTime        string   `json:"endTime"`
	SpeciesCode    string   `json:"speciesCode"`
	ScientificName string   `json:"scientificName"`
	CommonName     string   `json:"commonName"`
	Confidence     float64  `json:"confidence"`
	Verified       string   `json:"verified"`
	Locked         bool     `json:"locked"`
	Comments       []string `json:"comments,omitempty"`
}

// DetectionRequest represents the query parameters for listing detections
type DetectionRequest struct {
	Comment       string `json:"comment,omitempty"`
	Verified      string `json:"verified,omitempty"`
	IgnoreSpecies string `json:"ignoreSpecies,omitempty"`
	Locked        bool   `json:"locked,omitempty"`
}

// PaginatedResponse represents a paginated API response
type PaginatedResponse struct {
	Data        interface{} `json:"data"`
	Total       int64       `json:"total"`
	Limit       int         `json:"limit"`
	Offset      int         `json:"offset"`
	CurrentPage int         `json:"current_page"`
	TotalPages  int         `json:"total_pages"`
}

// TimeOfDayResponse represents the time of day response for a detection
type TimeOfDayResponse struct {
	TimeOfDay string `json:"timeOfDay"`
}

// GetDetections handles GET requests for detections
func (c *Controller) GetDetections(ctx echo.Context) error {
	// Parse query parameters
	date := ctx.QueryParam("date")
	hour := ctx.QueryParam("hour")
	duration, _ := strconv.Atoi(ctx.QueryParam("duration"))
	species := ctx.QueryParam("species")
	search := ctx.QueryParam("search")
	numResults, _ := strconv.Atoi(ctx.QueryParam("numResults"))
	offset, _ := strconv.Atoi(ctx.QueryParam("offset"))
	queryType := ctx.QueryParam("queryType") // "hourly", "species", "search", or "all"

	// Set default values and enforce maximum limit
	if numResults <= 0 {
		numResults = 100
	} else if numResults > 1000 {
		// Enforce a maximum limit to prevent excessive loads
		numResults = 1000
	}

	// Ensure offset is non-negative for security and to prevent unexpected behavior
	if offset < 0 {
		offset = 0
	}

	// Set default duration
	if duration <= 0 {
		duration = 1
	}

	var notes []datastore.Note
	var err error
	var totalResults int64

	// Get notes based on query type
	switch queryType {
	case "hourly":
		notes, totalResults, err = c.getHourlyDetections(date, hour, duration, numResults, offset)
	case "species":
		notes, totalResults, err = c.getSpeciesDetections(species, date, hour, duration, numResults, offset)
	case "search":
		notes, totalResults, err = c.getSearchDetections(search, numResults, offset)
	default: // "all" or any other value
		notes, totalResults, err = c.getAllDetections(numResults, offset)
	}

	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	// Convert notes to response format
	detections := []DetectionResponse{}
	for i := range notes {
		note := &notes[i]
		detection := DetectionResponse{
			ID:             note.ID,
			Date:           note.Date,
			Time:           note.Time,
			Source:         note.Source,
			BeginTime:      note.BeginTime.Format(time.RFC3339),
			EndTime:        note.EndTime.Format(time.RFC3339),
			SpeciesCode:    note.SpeciesCode,
			ScientificName: note.ScientificName,
			CommonName:     note.CommonName,
			Confidence:     note.Confidence,
			Locked:         note.Locked,
		}

		// Handle verification status
		switch note.Verified {
		case "correct":
			detection.Verified = "correct"
		case "false_positive":
			detection.Verified = "false_positive"
		default:
			detection.Verified = "unverified"
		}

		// Get comments if any
		if len(note.Comments) > 0 {
			comments := []string{}
			for _, comment := range note.Comments {
				comments = append(comments, comment.Entry)
			}
			detection.Comments = comments
		}

		detections = append(detections, detection)
	}

	// Calculate pagination values
	currentPage := (offset / numResults) + 1
	totalPages := int((totalResults + int64(numResults) - 1) / int64(numResults))

	// Create paginated response
	response := PaginatedResponse{
		Data:        detections,
		Total:       totalResults,
		Limit:       numResults,
		Offset:      offset,
		CurrentPage: currentPage,
		TotalPages:  totalPages,
	}

	return ctx.JSON(http.StatusOK, response)
}

// getHourlyDetections handles hourly query type logic
func (c *Controller) getHourlyDetections(date, hour string, duration, numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("hourly:%s:%s:%d:%d:%d", date, hour, duration, numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// If not in cache, query the database
	notes, err := c.DS.GetHourlyDetections(date, hour, duration, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := c.DS.CountHourlyDetections(date, hour, duration)
	if err != nil {
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	return notes, totalCount, nil
}

// getSpeciesDetections handles species query type logic
func (c *Controller) getSpeciesDetections(species, date, hour string, duration, numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("species:%s:%s:%s:%d:%d:%d", species, date, hour, duration, numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// If not in cache, query the database
	notes, err := c.DS.SpeciesDetections(species, date, hour, duration, false, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSpeciesDetections(species, date, hour, duration)
	if err != nil {
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	return notes, totalCount, nil
}

// getSearchDetections handles search query type logic
func (c *Controller) getSearchDetections(search string, numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("search:%s:%d:%d", search, numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// If not in cache, query the database
	notes, err := c.DS.SearchNotes(search, false, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSearchResults(search)
	if err != nil {
		return nil, 0, err
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalCount}, cache.DefaultExpiration)

	return notes, totalCount, nil
}

// getAllDetections handles default/all query type logic
func (c *Controller) getAllDetections(numResults, offset int) ([]datastore.Note, int64, error) {
	// Generate a cache key based on parameters
	cacheKey := fmt.Sprintf("all:%d:%d", numResults, offset)

	// Check if data is in cache
	if cachedData, found := c.detectionCache.Get(cacheKey); found {
		cachedResult := cachedData.(struct {
			Notes []datastore.Note
			Total int64
		})
		return cachedResult.Notes, cachedResult.Total, nil
	}

	// Use the datastore.SearchNotes method with an empty query to get all notes
	notes, err := c.DS.SearchNotes("", false, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	// Estimate total by counting
	totalResults := int64(len(notes))
	if len(notes) == numResults {
		// If we got exactly the number requested, there may be more
		totalResults = int64(offset + numResults + 1) // This is an estimate
	}

	// Cache the results
	c.detectionCache.Set(cacheKey, struct {
		Notes []datastore.Note
		Total int64
	}{notes, totalResults}, cache.DefaultExpiration)

	return notes, totalResults, nil
}

// GetDetection returns a single detection by ID
func (c *Controller) GetDetection(ctx echo.Context) error {
	id := ctx.Param("id")
	note, err := c.DS.Get(id)
	if err != nil {
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Detection not found"})
	}

	detection := DetectionResponse{
		ID:             note.ID,
		Date:           note.Date,
		Time:           note.Time,
		Source:         note.Source,
		BeginTime:      note.BeginTime.Format(time.RFC3339),
		EndTime:        note.EndTime.Format(time.RFC3339),
		SpeciesCode:    note.SpeciesCode,
		ScientificName: note.ScientificName,
		CommonName:     note.CommonName,
		Confidence:     note.Confidence,
		Locked:         note.Locked,
	}

	// Handle verification status
	switch note.Verified {
	case "correct":
		detection.Verified = "correct"
	case "false_positive":
		detection.Verified = "false_positive"
	default:
		detection.Verified = "unverified"
	}

	// Get comments if any
	if len(note.Comments) > 0 {
		comments := []string{}
		for _, comment := range note.Comments {
			comments = append(comments, comment.Entry)
		}
		detection.Comments = comments
	}

	return ctx.JSON(http.StatusOK, detection)
}

// GetRecentDetections returns the most recent detections
func (c *Controller) GetRecentDetections(ctx echo.Context) error {
	limit, _ := strconv.Atoi(ctx.QueryParam("limit"))
	if limit <= 0 {
		limit = 10
	}

	notes, err := c.DS.GetLastDetections(limit)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get recent detections", http.StatusInternalServerError)
	}

	detections := []DetectionResponse{}
	for i := range notes {
		note := &notes[i]
		detection := DetectionResponse{
			ID:             note.ID,
			Date:           note.Date,
			Time:           note.Time,
			Source:         note.Source,
			BeginTime:      note.BeginTime.Format(time.RFC3339),
			EndTime:        note.EndTime.Format(time.RFC3339),
			SpeciesCode:    note.SpeciesCode,
			ScientificName: note.ScientificName,
			CommonName:     note.CommonName,
			Confidence:     note.Confidence,
			Locked:         note.Locked,
		}

		// Handle verification status
		switch note.Verified {
		case "correct":
			detection.Verified = "correct"
		case "false_positive":
			detection.Verified = "false_positive"
		default:
			detection.Verified = "unverified"
		}

		detections = append(detections, detection)
	}

	return ctx.JSON(http.StatusOK, detections)
}

// DeleteDetection deletes a detection by ID
func (c *Controller) DeleteDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")
	note, err := c.DS.Get(idStr)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	// Check if the note is locked
	if note.Locked {
		return c.HandleError(ctx, fmt.Errorf("detection is locked"), "Detection is locked", http.StatusForbidden)
	}

	err = c.DS.Delete(idStr)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to delete detection", http.StatusInternalServerError)
	}

	// Invalidate cache after deletion
	c.invalidateDetectionCache()

	return ctx.NoContent(http.StatusNoContent)
}

// invalidateDetectionCache clears the detection cache to ensure fresh data
// is fetched on subsequent requests. This should be called after any
// operation that modifies detection data.
func (c *Controller) invalidateDetectionCache() {
	// Clear all cached detection data to ensure fresh results
	c.detectionCache.Flush()
}

// checkAndHandleLock verifies if a detection is locked and manages lock state
// Returns the note and error if any
func (c *Controller) checkAndHandleLock(idStr string, shouldLock bool) (*datastore.Note, error) {
	// Get the note
	note, err := c.DS.Get(idStr)
	if err != nil {
		return nil, fmt.Errorf("detection not found: %w", err)
	}

	// Check if the note is already locked in memory
	if note.Locked {
		return nil, fmt.Errorf("detection is locked")
	}

	// Check if the note is locked in the database
	isLocked, err := c.DS.IsNoteLocked(idStr)
	if err != nil {
		return nil, fmt.Errorf("failed to check lock status: %w", err)
	}
	if isLocked {
		return nil, fmt.Errorf("detection is locked")
	}

	// If we should lock the note, try to acquire lock
	if shouldLock {
		if err := c.DS.LockNote(idStr); err != nil {
			return nil, fmt.Errorf("failed to acquire lock: %w", err)
		}
	}

	return &note, nil
}

// ReviewDetection updates a detection with verification status and optional comment
func (c *Controller) ReviewDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")

	// Use the shared lock helper
	note, err := c.checkAndHandleLock(idStr, true)
	if err != nil {
		// Check error type to determine the appropriate status code
		if strings.Contains(err.Error(), "failed to check lock status") {
			// Database error during lock check should be 500
			return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
		} else {
			// Lock conflicts should be 409
			return c.HandleError(ctx, err, err.Error(), http.StatusConflict)
		}
	}

	// Parse request
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Handle comment if provided
	if req.Comment != "" {
		// Save comment using the datastore method for adding comments
		err = c.AddComment(note.ID, req.Comment)
		if err != nil {
			return c.HandleError(ctx, err, fmt.Sprintf("Failed to add comment: %v", err), http.StatusInternalServerError)
		}
	}

	// Handle verification if provided
	if req.Verified != "" {
		var verified bool
		switch req.Verified {
		case "correct":
			verified = true
		case "false_positive":
			verified = false
		default:
			return c.HandleError(ctx, fmt.Errorf("invalid verification status"), "Invalid verification status", http.StatusBadRequest)
		}

		// Save review using the datastore method for reviews
		err = c.AddReview(note.ID, verified)
		if err != nil {
			return c.HandleError(ctx, err, fmt.Sprintf("Failed to update verification: %v", err), http.StatusInternalServerError)
		}

		// Handle ignored species
		if err := c.addToIgnoredSpecies(note, req.Verified, req.IgnoreSpecies); err != nil {
			return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
		}
	}

	// Invalidate cache after modification
	c.invalidateDetectionCache()

	// Return success response with 200 OK status
	return ctx.JSON(http.StatusOK, map[string]string{
		"status": "success",
	})
}

// LockDetection locks or unlocks a detection
func (c *Controller) LockDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")

	// Use the shared lock helper without acquiring a lock
	note, err := c.checkAndHandleLock(idStr, false)
	if err != nil {
		return ctx.JSON(http.StatusConflict, map[string]string{"error": err.Error()})
	}

	// Parse request
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
	}

	// Lock/unlock the detection
	err = c.AddLock(note.ID, req.Locked)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to update lock status: %v", err)})
	}

	// Invalidate cache after changing lock status
	c.invalidateDetectionCache()

	return ctx.NoContent(http.StatusNoContent)
}

// IgnoreSpeciesRequest represents the request body for ignoring a species
type IgnoreSpeciesRequest struct {
	CommonName string `json:"common_name"`
}

// IgnoreSpecies adds a species to the ignored list
func (c *Controller) IgnoreSpecies(ctx echo.Context) error {
	// Parse request body
	req := &IgnoreSpeciesRequest{}
	if err := ctx.Bind(req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
	}

	// Validate request
	if req.CommonName == "" {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Missing species name"})
	}

	// Add to ignored species list
	err := c.addSpeciesToIgnoredList(req.CommonName)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return ctx.NoContent(http.StatusNoContent)
}

// addToIgnoredSpecies handles the logic for adding species to the ignore list
func (c *Controller) addToIgnoredSpecies(note *datastore.Note, verified, ignoreSpecies string) error {
	if verified == "false_positive" && ignoreSpecies != "" {
		return c.addSpeciesToIgnoredList(ignoreSpecies)
	}
	return nil
}

// addSpeciesToIgnoredList adds a species to the ignore list with proper concurrency control.
// It uses a mutex to ensure thread-safety when multiple requests try to modify the
// excluded species list simultaneously. The function:
// 1. Locks the controller's mutex to prevent concurrent modifications
// 2. Gets the latest settings from the settings package
// 3. Checks if the species is already in the excluded list
// 4. If not excluded, creates a copy of the exclude list to avoid race conditions
// 5. Adds the species to the new list and updates the settings
// 6. Saves the settings using the package's thread-safe function
func (c *Controller) addSpeciesToIgnoredList(species string) error {
	if species == "" {
		return nil
	}

	// Use the controller's mutex to protect this operation
	c.speciesExcludeMutex.Lock()
	defer c.speciesExcludeMutex.Unlock()

	// Access the latest settings using the settings accessor function
	settings := conf.GetSettings()

	// Check if species is already in the excluded list
	isExcluded := false
	for _, s := range settings.Realtime.Species.Exclude {
		if s == species {
			isExcluded = true
			break
		}
	}

	// If not already excluded, add it
	if !isExcluded {
		// Create a copy of the current exclude list to avoid race conditions
		newExcludeList := make([]string, len(settings.Realtime.Species.Exclude))
		copy(newExcludeList, settings.Realtime.Species.Exclude)

		// Add the new species to the list
		newExcludeList = append(newExcludeList, species)

		// Update the settings with the new list
		settings.Realtime.Species.Exclude = newExcludeList

		// Save settings using the package function that handles concurrency
		if err := conf.SaveSettings(); err != nil {
			return fmt.Errorf("failed to save settings: %w", err)
		}
	}

	return nil
}

// AddComment creates a comment for a note
func (c *Controller) AddComment(noteID uint, commentText string) error {
	if commentText == "" {
		return nil // No comment to add
	}

	comment := &datastore.NoteComment{
		NoteID:    noteID,
		Entry:     commentText,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return c.DS.SaveNoteComment(comment)
}

// AddReview creates or updates a review for a note
func (c *Controller) AddReview(noteID uint, verified bool) error {
	// Convert bool to string value
	verifiedStr := map[bool]string{
		true:  "correct",
		false: "false_positive",
	}[verified]

	review := &datastore.NoteReview{
		NoteID:    noteID,
		Verified:  verifiedStr,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}

	return c.DS.SaveNoteReview(review)
}

// AddLock creates or removes a lock for a note
func (c *Controller) AddLock(noteID uint, locked bool) error {
	noteIDStr := strconv.FormatUint(uint64(noteID), 10)

	if locked {
		return c.DS.LockNote(noteIDStr)
	} else {
		return c.DS.UnlockNote(noteIDStr)
	}
}

// GetDetectionTimeOfDay calculates and returns the time of day for a detection
func (c *Controller) GetDetectionTimeOfDay(ctx echo.Context) error {
	id := ctx.Param("id")

	// Get the detection from the database
	note, err := c.DS.Get(id)
	if err != nil {
		return c.HandleError(ctx, err, "Detection not found", http.StatusNotFound)
	}

	// Parse the detection date and time
	dateTimeStr := fmt.Sprintf("%s %s", note.Date, note.Time)
	layout := "2006-01-02 15:04:05" // Adjust based on your actual date/time format

	detectionTime, err := time.Parse(layout, dateTimeStr)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to parse detection time", http.StatusInternalServerError)
	}

	// Check if SunCalc is initialized
	if c.SunCalc == nil {
		return c.HandleError(ctx, fmt.Errorf("sun calculator not initialized"), "Sun calculator not available", http.StatusInternalServerError)
	}

	// Calculate sun times for the detection date
	sunEvents, err := c.SunCalc.GetSunEventTimes(detectionTime)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to calculate sun times", http.StatusInternalServerError)
	}

	// Determine time of day based on the detection time and sun events
	timeOfDay := calculateTimeOfDay(detectionTime, &sunEvents)

	// Return the time of day
	return ctx.JSON(http.StatusOK, TimeOfDayResponse{
		TimeOfDay: timeOfDay,
	})
}

// calculateTimeOfDay determines the time of day based on the detection time and sun events
func calculateTimeOfDay(detectionTime time.Time, sunEvents *suncalc.SunEventTimes) string {
	// Convert all times to the same format for comparison
	detTime := detectionTime.Format("15:04:05")
	sunriseTime := sunEvents.Sunrise.Format("15:04:05")
	sunsetTime := sunEvents.Sunset.Format("15:04:05")

	// Define sunrise/sunset window (30 minutes before and after)
	sunriseStart := sunEvents.Sunrise.Add(-30 * time.Minute).Format("15:04:05")
	sunriseEnd := sunEvents.Sunrise.Add(30 * time.Minute).Format("15:04:05")
	sunsetStart := sunEvents.Sunset.Add(-30 * time.Minute).Format("15:04:05")
	sunsetEnd := sunEvents.Sunset.Add(30 * time.Minute).Format("15:04:05")

	switch {
	case detTime >= sunriseStart && detTime <= sunriseEnd:
		return "Sunrise"
	case detTime >= sunsetStart && detTime <= sunsetEnd:
		return "Sunset"
	case detTime >= sunriseTime && detTime < sunsetTime:
		return "Day"
	default:
		return "Night"
	}
}
