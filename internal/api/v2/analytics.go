// internal/api/v2/analytics.go
package api

import (
	"net/http"
	"sort"
	"strconv"
	"time"

	"github.com/labstack/echo/v4"
)

// SpeciesDailySummary represents a bird in the daily species summary API response
type SpeciesDailySummary struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name"`
	SpeciesCode    string `json:"species_code,omitempty"`
	Count          int    `json:"count"`
	HourlyCounts   []int  `json:"hourly_counts"`
	HighConfidence bool   `json:"high_confidence"`
	First          string `json:"first_seen,omitempty"`
	Latest         string `json:"latest_seen,omitempty"`
	ThumbnailURL   string `json:"thumbnail_url,omitempty"`
}

// SpeciesSummary represents a bird in the overall species summary API response
type SpeciesSummary struct {
	ScientificName string  `json:"scientific_name"`
	CommonName     string  `json:"common_name"`
	SpeciesCode    string  `json:"species_code,omitempty"`
	Count          int     `json:"count"`
	FirstSeen      string  `json:"first_seen,omitempty"`
	LastSeen       string  `json:"last_seen,omitempty"`
	AvgConfidence  float64 `json:"avg_confidence,omitempty"`
	MaxConfidence  float64 `json:"max_confidence,omitempty"`
	ThumbnailURL   string  `json:"thumbnail_url,omitempty"`
}

// initAnalyticsRoutes registers all analytics-related API endpoints
func (c *Controller) initAnalyticsRoutes() {
	// Create analytics API group - publicly accessible
	analyticsGroup := c.Group.Group("/analytics")

	// Species analytics routes
	speciesGroup := analyticsGroup.Group("/species")
	speciesGroup.GET("/daily", c.GetDailySpeciesSummary)
	speciesGroup.GET("/summary", c.GetSpeciesSummary)

	// Time analytics routes (can be implemented later)
	timeGroup := analyticsGroup.Group("/time")
	timeGroup.GET("/hourly", c.GetHourlyAnalytics)
	timeGroup.GET("/daily", c.GetDailyAnalytics)
}

// GetDailySpeciesSummary handles GET /api/v2/analytics/species/daily
// Provides a summary of bird species detected on a specific day
func (c *Controller) GetDailySpeciesSummary(ctx echo.Context) error {
	// Get request parameters
	selectedDate := ctx.QueryParam("date")
	if selectedDate == "" {
		selectedDate = time.Now().Format("2006-01-02")
	}

	// Parse min confidence parameter
	minConfidenceStr := ctx.QueryParam("min_confidence")
	minConfidence := 0.0
	if minConfidenceStr != "" {
		parsedConfidence, err := strconv.ParseFloat(minConfidenceStr, 64)
		if err == nil {
			minConfidence = parsedConfidence / 100.0 // Convert from percentage to decimal
		}
	}

	// Get top birds data from the database
	notes, err := c.DS.GetTopBirdsData(selectedDate, minConfidence)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get daily species data", http.StatusInternalServerError)
	}

	// Process notes to get hourly counts and other statistics
	birdData := make(map[string]struct {
		CommonName     string
		ScientificName string
		SpeciesCode    string
		Count          int
		HourlyCounts   [24]int
		HighConfidence bool
		First          string
		Latest         string
	})

	// Process each note
	for i := range notes {
		note := &notes[i]
		// Skip notes with confidence below threshold
		if note.Confidence < minConfidence {
			continue
		}

		// Get hourly counts for this species
		hourlyCounts, err := c.DS.GetHourlyOccurrences(selectedDate, note.CommonName, minConfidence)
		if err != nil {
			c.Debug("Error getting hourly counts: %v", err)
			continue
		}

		// Calculate total count from hourly counts
		totalCount := 0
		for _, count := range hourlyCounts {
			totalCount += count
		}

		// Create or update bird data entry
		birdKey := note.ScientificName
		data, exists := birdData[birdKey]

		if !exists {
			// Create new entry if it doesn't exist
			birdData[birdKey] = struct {
				CommonName     string
				ScientificName string
				SpeciesCode    string
				Count          int
				HourlyCounts   [24]int
				HighConfidence bool
				First          string
				Latest         string
			}{
				CommonName:     note.CommonName,
				ScientificName: note.ScientificName,
				SpeciesCode:    note.SpeciesCode,
				Count:          totalCount,
				HourlyCounts:   hourlyCounts,
				HighConfidence: note.Confidence >= 0.8, // Define high confidence
				First:          note.Time,
				Latest:         note.Time,
			}
		} else {
			// Update existing entry
			// Update first/latest times if needed
			if note.Time < data.First {
				data.First = note.Time
			}
			if note.Time > data.Latest {
				data.Latest = note.Time
			}

			// Update other fields
			data.Count = totalCount
			data.HourlyCounts = hourlyCounts
			data.HighConfidence = data.HighConfidence || note.Confidence >= 0.8

			// Save updated data back to map
			birdData[birdKey] = data
		}
	}

	// Convert map to slice for response
	var result []SpeciesDailySummary
	for key := range birdData {
		data := birdData[key]
		// Skip birds with no detections
		if data.Count == 0 {
			continue
		}

		// Convert hourly counts array to slice
		hourlyCounts := make([]int, 24)
		copy(hourlyCounts, data.HourlyCounts[:])

		// Get bird thumbnail URL if available
		var thumbnailURL string
		if c.BirdImageCache != nil {
			birdImage, err := c.BirdImageCache.Get(data.ScientificName)
			if err == nil {
				thumbnailURL = birdImage.URL
			}
		}

		// Add to result
		result = append(result, SpeciesDailySummary{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			SpeciesCode:    data.SpeciesCode,
			Count:          data.Count,
			HourlyCounts:   hourlyCounts,
			HighConfidence: data.HighConfidence,
			First:          data.First,
			Latest:         data.Latest,
			ThumbnailURL:   thumbnailURL,
		})
	}

	// Sort by count in descending order
	sort.Slice(result, func(i, j int) bool {
		return result[i].Count > result[j].Count
	})

	// Limit results if requested
	limitStr := ctx.QueryParam("limit")
	if limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err == nil && limit > 0 && limit < len(result) {
			result = result[:limit]
		}
	}

	return ctx.JSON(http.StatusOK, result)
}

// GetSpeciesSummary handles GET /api/v2/analytics/species/summary
// This provides an overall summary of species detections
func (c *Controller) GetSpeciesSummary(ctx echo.Context) error {
	// Retrieve species summary data from the datastore
	summaryData, err := c.DS.GetSpeciesSummaryData()
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get species summary data", http.StatusInternalServerError)
	}

	// Convert datastore model to API response model
	response := make([]SpeciesSummary, 0, len(summaryData))
	for i := range summaryData {
		data := &summaryData[i]
		// Format the times as strings
		firstSeen := ""
		lastSeen := ""

		if !data.FirstSeen.IsZero() {
			firstSeen = data.FirstSeen.Format("2006-01-02 15:04:05")
		}

		if !data.LastSeen.IsZero() {
			lastSeen = data.LastSeen.Format("2006-01-02 15:04:05")
		}

		// Get bird thumbnail URL if available
		var thumbnailURL string
		if c.BirdImageCache != nil {
			birdImage, err := c.BirdImageCache.Get(data.ScientificName)
			if err == nil {
				thumbnailURL = birdImage.URL
			}
		}

		// Add to response
		summary := SpeciesSummary{
			ScientificName: data.ScientificName,
			CommonName:     data.CommonName,
			SpeciesCode:    data.SpeciesCode,
			Count:          data.Count,
			FirstSeen:      firstSeen,
			LastSeen:       lastSeen,
			AvgConfidence:  data.AvgConfidence,
			MaxConfidence:  data.MaxConfidence,
			ThumbnailURL:   thumbnailURL,
		}

		response = append(response, summary)
	}

	// Limit results if requested
	limitStr := ctx.QueryParam("limit")
	if limitStr != "" {
		limit, err := strconv.Atoi(limitStr)
		if err == nil && limit > 0 && limit < len(response) {
			response = response[:limit]
		}
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetHourlyAnalytics handles GET /api/v2/analytics/time/hourly
// This provides hourly detection patterns
func (c *Controller) GetHourlyAnalytics(ctx echo.Context) error {
	// Get query parameters
	date := ctx.QueryParam("date")
	species := ctx.QueryParam("species")

	// Validate required parameters
	if date == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: date")
	}

	if species == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: species")
	}

	// Validate date format
	if _, err := time.Parse("2006-01-02", date); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid date format. Use YYYY-MM-DD")
	}

	// Get hourly analytics data from the datastore
	hourlyData, err := c.DS.GetHourlyAnalyticsData(date, species)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get hourly analytics data", http.StatusInternalServerError)
	}

	// Create a 24-hour array filled with zeros
	hourlyCountsArray := make([]int, 24)

	// Fill in the actual counts
	for i := range hourlyData {
		data := hourlyData[i]
		if data.Hour >= 0 && data.Hour < 24 {
			hourlyCountsArray[data.Hour] = data.Count
		}
	}

	// Build the response
	response := map[string]interface{}{
		"date":    date,
		"species": species,
		"counts":  hourlyCountsArray,
		"total":   sumCounts(hourlyCountsArray),
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetDailyAnalytics handles GET /api/v2/analytics/time/daily
// This provides daily detection patterns
func (c *Controller) GetDailyAnalytics(ctx echo.Context) error {
	// Get query parameters
	startDate := ctx.QueryParam("start_date")
	endDate := ctx.QueryParam("end_date")
	species := ctx.QueryParam("species")

	// For the tests, validate that start_date is required
	if startDate == "" {
		return echo.NewHTTPError(http.StatusBadRequest, "Missing required parameter: start_date")
	}

	// Validate date formats
	if _, err := time.Parse("2006-01-02", startDate); err != nil {
		return echo.NewHTTPError(http.StatusBadRequest, "Invalid start_date format. Use YYYY-MM-DD")
	}

	// If endDate is provided, validate its format
	if endDate != "" {
		if _, err := time.Parse("2006-01-02", endDate); err != nil {
			return echo.NewHTTPError(http.StatusBadRequest, "Invalid end_date format. Use YYYY-MM-DD")
		}
	} else {
		// If only start date is provided, use 30 days after that
		startTime, err := time.Parse("2006-01-02", startDate)
		if err == nil {
			endDate = startTime.AddDate(0, 0, 30).Format("2006-01-02")
		}
	}

	// Get daily analytics data from the datastore
	dailyData, err := c.DS.GetDailyAnalyticsData(startDate, endDate, species)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get daily analytics data", http.StatusInternalServerError)
	}

	// Build the response
	type DailyResponse struct {
		Date  string `json:"date"`
		Count int    `json:"count"`
	}

	response := struct {
		StartDate string          `json:"start_date"`
		EndDate   string          `json:"end_date"`
		Species   string          `json:"species,omitempty"`
		Data      []DailyResponse `json:"data"`
		Total     int             `json:"total"`
	}{
		StartDate: startDate,
		EndDate:   endDate,
		Species:   species,
		Data:      make([]DailyResponse, 0, len(dailyData)),
	}

	// Convert dailyData to response format and calculate total
	totalCount := 0
	for i := range dailyData {
		data := dailyData[i]
		response.Data = append(response.Data, DailyResponse{
			Date:  data.Date,
			Count: data.Count,
		})
		totalCount += data.Count
	}
	response.Total = totalCount

	return ctx.JSON(http.StatusOK, response)
}

// Helper function to sum array values
func sumCounts(counts []int) int {
	total := 0
	for _, count := range counts {
		total += count
	}
	return total
}
