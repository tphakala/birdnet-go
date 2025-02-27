// internal/api/v2/detections.go
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// initDetectionRoutes registers all detection-related API endpoints
func (c *Controller) initDetectionRoutes() {
	// Detection endpoints - publicly accessible
	c.Group.GET("/detections", c.GetDetections)
	c.Group.GET("/detections/:id", c.GetDetection)
	c.Group.GET("/detections/recent", c.GetRecentDetections)

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
	// Weather information
	WeatherData *struct {
		Temperature float64 `json:"temperature,omitempty"`
		FeelsLike   float64 `json:"feels_like,omitempty"`
		WeatherMain string  `json:"weather_main,omitempty"`
		WeatherDesc string  `json:"weather_desc,omitempty"`
		WeatherIcon string  `json:"weather_icon,omitempty"`
		Humidity    int     `json:"humidity,omitempty"`
		WindSpeed   float64 `json:"wind_speed,omitempty"`
		WindDeg     int     `json:"wind_deg,omitempty"`
		IsDaytime   bool    `json:"is_daytime,omitempty"`
	} `json:"weather,omitempty"`
	// Weather error information
	WeatherError string `json:"weather_error,omitempty"`
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
	notes, err := c.DS.GetHourlyDetections(date, hour, duration, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := c.DS.CountHourlyDetections(date, hour, duration)
	if err != nil {
		return nil, 0, err
	}

	return notes, totalCount, nil
}

// getSpeciesDetections handles species query type logic
func (c *Controller) getSpeciesDetections(species, date, hour string, duration, numResults, offset int) ([]datastore.Note, int64, error) {
	notes, err := c.DS.SpeciesDetections(species, date, hour, duration, false, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSpeciesDetections(species, date, hour, duration)
	if err != nil {
		return nil, 0, err
	}

	return notes, totalCount, nil
}

// getSearchDetections handles search query type logic
func (c *Controller) getSearchDetections(search string, numResults, offset int) ([]datastore.Note, int64, error) {
	notes, err := c.DS.SearchNotes(search, false, numResults, offset)
	if err != nil {
		return nil, 0, err
	}

	totalCount, err := c.DS.CountSearchResults(search)
	if err != nil {
		return nil, 0, err
	}

	return notes, totalCount, nil
}

// getAllDetections handles default/all query type logic
func (c *Controller) getAllDetections(numResults, offset int) ([]datastore.Note, int64, error) {
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

	// Get weather data for the detection time
	includeWeather := ctx.QueryParam("include_weather")
	if includeWeather == "true" || includeWeather == "1" {
		// Get daily weather data
		dailyEvents, err := c.DS.GetDailyEvents(note.Date)
		if err == nil {
			// Get hourly weather data for the day
			hourlyWeather, err := c.DS.GetHourlyWeather(note.Date)
			if err == nil && len(hourlyWeather) > 0 {
				// Parse detection time
				detectionTimeStr := note.Date + " " + note.Time
				detectionTime, timeParseErr := time.Parse("2006-01-02 15:04:05", detectionTimeStr)
				if timeParseErr != nil {
					// Add error information to the response instead of silently failing
					detection.WeatherError = fmt.Sprintf("Could not parse detection time: %v", timeParseErr)
				} else {
					// Get the closest hourly weather reading
					var closestWeather *datastore.HourlyWeather
					var closestDiff time.Duration = 24 * time.Hour

					for i := range hourlyWeather {
						diff := hourlyWeather[i].Time.Sub(detectionTime)
						if diff < 0 {
							diff = -diff // Get absolute value
						}

						if diff < closestDiff {
							closestDiff = diff
							closestWeather = &hourlyWeather[i]
						}
					}

					if closestWeather != nil {
						// Determine if it's daytime based on sunrise/sunset
						isDaytime := false
						if dailyEvents.Sunrise > 0 && dailyEvents.Sunset > 0 {
							// Convert detection time to Unix timestamp
							detectionUnix := detectionTime.Unix()
							isDaytime = detectionUnix >= dailyEvents.Sunrise && detectionUnix <= dailyEvents.Sunset
						}

						// Add weather data to the response
						detection.WeatherData = &struct {
							Temperature float64 `json:"temperature,omitempty"`
							FeelsLike   float64 `json:"feels_like,omitempty"`
							WeatherMain string  `json:"weather_main,omitempty"`
							WeatherDesc string  `json:"weather_desc,omitempty"`
							WeatherIcon string  `json:"weather_icon,omitempty"`
							Humidity    int     `json:"humidity,omitempty"`
							WindSpeed   float64 `json:"wind_speed,omitempty"`
							WindDeg     int     `json:"wind_deg,omitempty"`
							IsDaytime   bool    `json:"is_daytime,omitempty"`
						}{
							Temperature: closestWeather.Temperature,
							FeelsLike:   closestWeather.FeelsLike,
							WeatherMain: closestWeather.WeatherMain,
							WeatherDesc: closestWeather.WeatherDesc,
							WeatherIcon: closestWeather.WeatherIcon,
							Humidity:    closestWeather.Humidity,
							WindSpeed:   closestWeather.WindSpeed,
							WindDeg:     closestWeather.WindDeg,
							IsDaytime:   isDaytime,
						}
					} else {
						detection.WeatherError = "No weather data available for the detection time"
					}
				}
			} else {
				detection.WeatherError = "No hourly weather data available for the date"
			}
		} else {
			detection.WeatherError = fmt.Sprintf("No daily weather events available: %v", err)
		}
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
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
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
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Detection not found"})
	}

	// Check if the note is locked
	if note.Locked {
		return ctx.JSON(http.StatusForbidden, map[string]string{"error": "Detection is locked"})
	}

	err = c.DS.Delete(idStr)
	if err != nil {
		return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
	}

	return ctx.NoContent(http.StatusNoContent)
}

// ReviewDetection updates a detection with verification status and optional comment
func (c *Controller) ReviewDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")
	note, err := c.DS.Get(idStr)
	if err != nil {
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Detection not found"})
	}

	// Check if the note is locked
	if note.Locked {
		return ctx.JSON(http.StatusForbidden, map[string]string{"error": "Detection is locked"})
	}

	// Parse request
	req := &DetectionRequest{}
	if err := ctx.Bind(req); err != nil {
		return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid request format"})
	}

	// Handle comment if provided
	if req.Comment != "" {
		// Save comment using the datastore method for adding comments
		err = c.AddComment(note.ID, req.Comment)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to add comment: %v", err)})
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
			return ctx.JSON(http.StatusBadRequest, map[string]string{"error": "Invalid verification status"})
		}

		// Save review using the datastore method for reviews
		err = c.AddReview(note.ID, verified)
		if err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": fmt.Sprintf("Failed to update verification: %v", err)})
		}

		// Handle ignored species
		if err := c.addToIgnoredSpecies(&note, req.Verified, req.IgnoreSpecies); err != nil {
			return ctx.JSON(http.StatusInternalServerError, map[string]string{"error": err.Error()})
		}
	}

	return ctx.NoContent(http.StatusNoContent)
}

// LockDetection locks or unlocks a detection
func (c *Controller) LockDetection(ctx echo.Context) error {
	idStr := ctx.Param("id")
	note, err := c.DS.Get(idStr)
	if err != nil {
		return ctx.JSON(http.StatusNotFound, map[string]string{"error": "Detection not found"})
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
