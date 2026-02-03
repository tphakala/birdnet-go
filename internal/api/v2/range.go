// range.go contains API v2 endpoints for range filter operations
package api

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Range filter constants (file-local)
const (
	weeksPerMonth = 4 // Simplified ML model uses 4 weeks per month (48 weeks/year)
	daysPerWeek   = 7 // Days in a week for week calculation
)

// rangeFilterMutex protects against concurrent modifications to global settings during testing
var rangeFilterMutex sync.Mutex

// validateRangeFilterRequest validates the range filter test request parameters.
// Returns user-facing error messages with capitalized first letter for API responses.
func validateRangeFilterRequest(req *RangeFilterTestRequest) error {
	if req.Latitude < -90 || req.Latitude > 90 {
		return fmt.Errorf("Latitude must be between -90 and 90") //nolint:staticcheck // user-facing API message
	}
	if req.Longitude < -180 || req.Longitude > 180 {
		return fmt.Errorf("Longitude must be between -180 and 180") //nolint:staticcheck // user-facing API message
	}
	if req.Threshold < 0 || req.Threshold > 1 {
		return fmt.Errorf("Threshold must be between 0 and 1") //nolint:staticcheck // user-facing API message
	}
	return nil
}

// parseTestDate parses the date from request or returns current time.
func parseTestDate(dateStr string) (time.Time, error) {
	if dateStr == "" {
		return time.Now(), nil
	}
	return time.Parse(time.DateOnly, dateStr)
}

// calculateWeek calculates the BirdNET week number from a date.
// BirdNET uses a custom 48-week year with 4 weeks per month.
func calculateWeek(date time.Time) float32 {
	month := int(date.Month())
	day := date.Day()
	weeksFromMonths := (month - 1) * weeksPerMonth
	weekInMonth := (day-1)/daysPerWeek + 1
	return float32(weeksFromMonths + weekInMonth)
}

// getBirdNETInstance returns the BirdNET instance or an error if unavailable.
func (c *Controller) getBirdNETInstance() (*birdnet.BirdNET, error) {
	if c.Processor == nil {
		return nil, fmt.Errorf("BirdNET processor not available")
	}
	instance := c.Processor.GetBirdNET()
	if instance == nil {
		return nil, fmt.Errorf("BirdNET instance not available")
	}
	return instance, nil
}

// swapRangeFilterSettings temporarily sets range filter settings and returns a restore function.
func (c *Controller) swapRangeFilterSettings(lat, lon float64, threshold float32) func() {
	originalLat := c.Settings.BirdNET.Latitude
	originalLon := c.Settings.BirdNET.Longitude
	originalThreshold := c.Settings.BirdNET.RangeFilter.Threshold

	c.Settings.BirdNET.Latitude = lat
	c.Settings.BirdNET.Longitude = lon
	c.Settings.BirdNET.RangeFilter.Threshold = threshold

	return func() {
		c.Settings.BirdNET.Latitude = originalLat
		c.Settings.BirdNET.Longitude = originalLon
		c.Settings.BirdNET.RangeFilter.Threshold = originalThreshold
	}
}

// Location represents geographic coordinates
type Location struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
}

// RangeFilterSpeciesCount represents the count response for range filter species
type RangeFilterSpeciesCount struct {
	Count       int       `json:"count"`
	LastUpdated time.Time `json:"lastUpdated"`
	Threshold   float32   `json:"threshold"`
	Location    Location  `json:"location"`
}

// RangeFilterSpecies represents a single species in the range filter
type RangeFilterSpecies struct {
	Label          string   `json:"label"`
	ScientificName string   `json:"scientificName"`
	CommonName     string   `json:"commonName"`
	Score          *float64 `json:"score,omitempty"` // Nullable - only present when individual scores are available
}

// RangeFilterSpeciesList represents the full list response for range filter species
type RangeFilterSpeciesList struct {
	Species     []RangeFilterSpecies `json:"species"`
	Count       int                  `json:"count"`
	LastUpdated time.Time            `json:"lastUpdated"`
	Threshold   float32              `json:"threshold"`
	Location    Location             `json:"location"`
}

// RangeFilterTestRequest represents the request for testing range filter
type RangeFilterTestRequest struct {
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	Threshold float32 `json:"threshold"`
	Date      string  `json:"date"` // optional, format: "2006-01-02"
	Week      float32 `json:"week"` // optional, calculated from date if not provided
}

// RangeFilterTestResponse represents the response for range filter testing
type RangeFilterTestResponse struct {
	Species    []RangeFilterSpecies `json:"species"`
	Count      int                  `json:"count"`
	Threshold  float32              `json:"threshold"`
	Location   Location             `json:"location"`
	TestDate   time.Time            `json:"testDate"`
	Week       int                  `json:"week"`
	Parameters struct {
		InputLatitude  float64 `json:"inputLatitude"`
		InputLongitude float64 `json:"inputLongitude"`
		InputThreshold float32 `json:"inputThreshold"`
		InputDate      string  `json:"inputDate,omitempty"`
		InputWeek      float32 `json:"inputWeek,omitempty"`
	} `json:"parameters"`
}

// initRangeRoutes sets up the range filter related routes
func (c *Controller) initRangeRoutes() {
	// Range filter routes
	c.Group.GET("/range/species/count", c.GetRangeFilterSpeciesCount)
	c.Group.GET("/range/species/list", c.GetRangeFilterSpeciesList)
	c.Group.GET("/range/species/csv", c.GetRangeFilterSpeciesCSV)
	c.Group.POST("/range/species/test", c.TestRangeFilter)
	c.Group.POST("/range/rebuild", c.RebuildRangeFilter)
}

// GetRangeFilterSpeciesCount returns the count of species in the current range filter
// @Summary Get range filter species count
// @Description Returns the count of species currently included in the range filter
// @Tags range
// @Produce json
// @Success 200 {object} RangeFilterSpeciesCount
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/species/count [get]
func (c *Controller) GetRangeFilterSpeciesCount(ctx echo.Context) error {
	// Get current included species
	includedSpecies := c.Settings.GetIncludedSpecies()

	response := RangeFilterSpeciesCount{
		Count:       len(includedSpecies),
		LastUpdated: c.Settings.BirdNET.RangeFilter.LastUpdated,
		Threshold:   c.Settings.BirdNET.RangeFilter.Threshold,
		Location: Location{
			Latitude:  c.Settings.BirdNET.Latitude,
			Longitude: c.Settings.BirdNET.Longitude,
		},
	}

	return ctx.JSON(http.StatusOK, response)
}

// GetRangeFilterSpeciesList returns the full list of species in the current range filter
// @Summary Get range filter species list
// @Description Returns the complete list of species currently included in the range filter with details
// @Tags range
// @Produce json
// @Success 200 {object} RangeFilterSpeciesList
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/species/list [get]
func (c *Controller) GetRangeFilterSpeciesList(ctx echo.Context) error {
	// Get current included species
	includedSpecies := c.Settings.GetIncludedSpecies()

	// Convert to response format with parsed names
	// Pre-allocate slice with capacity for all included species
	speciesList := make([]RangeFilterSpecies, 0, len(includedSpecies))
	for _, label := range includedSpecies {
		sp := detection.ParseSpeciesString(label)

		species := RangeFilterSpecies{
			Label:          label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
			Score:          nil, // No individual scores available for current range filter species
		}

		speciesList = append(speciesList, species)
	}

	response := RangeFilterSpeciesList{
		Species:     speciesList,
		Count:       len(speciesList),
		LastUpdated: c.Settings.BirdNET.RangeFilter.LastUpdated,
		Threshold:   c.Settings.BirdNET.RangeFilter.Threshold,
		Location: Location{
			Latitude:  c.Settings.BirdNET.Latitude,
			Longitude: c.Settings.BirdNET.Longitude,
		},
	}

	return ctx.JSON(http.StatusOK, response)
}

// TestRangeFilter tests the range filter with custom parameters
// @Summary Test range filter with custom parameters
// @Description Tests the range filter with specified coordinates, threshold, and date to see what species would be included
// @Tags range
// @Accept json
// @Produce json
// @Param request body RangeFilterTestRequest true "Range filter test parameters"
// @Success 200 {object} RangeFilterTestResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/species/test [post]
func (c *Controller) TestRangeFilter(ctx echo.Context) error {
	var req RangeFilterTestRequest
	if err := ctx.Bind(&req); err != nil {
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Validate request parameters
	if err := validateRangeFilterRequest(&req); err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusBadRequest)
	}

	// Parse date
	testDate, err := parseTestDate(req.Date)
	if err != nil {
		return c.HandleError(ctx, err, "Date must be in YYYY-MM-DD format", http.StatusBadRequest)
	}

	// Check if processor and BirdNET are available
	birdnetInstance, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
	}

	// Use mutex to protect against concurrent modifications to global settings
	rangeFilterMutex.Lock()
	defer rangeFilterMutex.Unlock()

	// Temporarily swap settings for testing
	restore := c.swapRangeFilterSettings(req.Latitude, req.Longitude, req.Threshold)
	defer restore()

	// Calculate week if not provided
	week := req.Week
	if week == 0 {
		week = calculateWeek(testDate)
	}

	// Get probable species for the test parameters
	speciesScores, err := birdnetInstance.GetProbableSpecies(testDate, week)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get probable species", http.StatusInternalServerError)
	}

	// Convert to response format
	// Pre-allocate slice with capacity for all species scores
	speciesList := make([]RangeFilterSpecies, 0, len(speciesScores))
	for _, speciesScore := range speciesScores {
		sp := detection.ParseSpeciesString(speciesScore.Label)

		// Create score pointer for non-nil value
		score := speciesScore.Score
		species := RangeFilterSpecies{
			Label:          speciesScore.Label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
			Score:          &score, // Individual scores are available from GetProbableSpecies
		}

		speciesList = append(speciesList, species)
	}

	response := RangeFilterTestResponse{
		Species:   speciesList,
		Count:     len(speciesList),
		Threshold: req.Threshold,
		TestDate:  testDate,
		Week:      int(week),
		Location: Location{
			Latitude:  req.Latitude,
			Longitude: req.Longitude,
		},
	}

	// Store original input parameters for reference
	response.Parameters.InputLatitude = req.Latitude
	response.Parameters.InputLongitude = req.Longitude
	response.Parameters.InputThreshold = req.Threshold
	response.Parameters.InputDate = req.Date
	response.Parameters.InputWeek = req.Week

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Range filter test completed", logger.Int("species_count", len(speciesList)))
	return ctx.JSON(http.StatusOK, response)
}

// GetRangeFilterSpeciesCSV exports the range filter species list as CSV
// @Summary Export range filter species list as CSV
// @Description Downloads the species list from range filter as a CSV file
// @Tags range
// @Produce text/csv
// @Param latitude query number false "Custom latitude (uses current settings if not provided)"
// @Param longitude query number false "Custom longitude (uses current settings if not provided)"
// @Param threshold query number false "Custom threshold (uses current settings if not provided)"
// @Success 200 {file} csv
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/species/csv [get]
func (c *Controller) GetRangeFilterSpeciesCSV(ctx echo.Context) error {
	// Check for custom parameters in query string
	customLat := ctx.QueryParam("latitude")
	customLon := ctx.QueryParam("longitude")
	customThreshold := ctx.QueryParam("threshold")

	var speciesList []RangeFilterSpecies
	var location Location
	var threshold float32

	// If custom parameters provided, test with those parameters
	if customLat != "" || customLon != "" || customThreshold != "" {
		// Parse custom parameters
		var testReq RangeFilterTestRequest

		// Use current settings as defaults
		testReq.Latitude = c.Settings.BirdNET.Latitude
		testReq.Longitude = c.Settings.BirdNET.Longitude
		testReq.Threshold = c.Settings.BirdNET.RangeFilter.Threshold

		// Override with custom values if provided
		if customLat != "" {
			lat, err := parseFloat64(customLat)
			if err != nil {
				return c.HandleError(ctx, err, "Invalid latitude format", http.StatusBadRequest)
			}
			if lat < -90 || lat > 90 {
				return c.HandleError(ctx, nil, "Latitude must be between -90 and 90", http.StatusBadRequest)
			}
			testReq.Latitude = lat
		}

		if customLon != "" {
			lon, err := parseFloat64(customLon)
			if err != nil {
				return c.HandleError(ctx, err, "Invalid longitude format", http.StatusBadRequest)
			}
			if lon < -180 || lon > 180 {
				return c.HandleError(ctx, nil, "Longitude must be between -180 and 180", http.StatusBadRequest)
			}
			testReq.Longitude = lon
		}

		if customThreshold != "" {
			thr, err := parseFloat32(customThreshold)
			if err != nil {
				return c.HandleError(ctx, err, "Invalid threshold format", http.StatusBadRequest)
			}
			if thr < 0 || thr > 1 {
				return c.HandleError(ctx, nil, "Threshold must be between 0 and 1", http.StatusBadRequest)
			}
			testReq.Threshold = thr
		}

		// Get species with custom parameters
		var err error
		speciesList, location, threshold, err = c.getTestSpeciesList(testReq)
		if err != nil {
			return c.HandleError(ctx, err, "Failed to get species list", http.StatusInternalServerError)
		}
	} else {
		// Use current range filter settings
		includedSpecies := c.Settings.GetIncludedSpecies()

		// Convert to species list format
		speciesList = make([]RangeFilterSpecies, 0, len(includedSpecies))
		for _, label := range includedSpecies {
			sp := detection.ParseSpeciesString(label)

			species := RangeFilterSpecies{
				Label:          label,
				ScientificName: sp.ScientificName,
				CommonName:     sp.CommonName,
				Score:          nil,
			}

			speciesList = append(speciesList, species)
		}

		location = Location{
			Latitude:  c.Settings.BirdNET.Latitude,
			Longitude: c.Settings.BirdNET.Longitude,
		}
		threshold = c.Settings.BirdNET.RangeFilter.Threshold
	}

	// Generate CSV content
	csvBytes, csvErr := c.generateSpeciesCSV(speciesList, location, threshold)
	if csvErr != nil {
		return c.HandleError(ctx, csvErr, "Failed to generate CSV", http.StatusInternalServerError)
	}

	// Set headers for file download
	filename := "birdnet_range_filter_species_" + time.Now().Format("20060102_150405") + ".csv"
	// RFC 5987: Include both filename and filename* for UTF-8 support
	encodedFilename := url.QueryEscape(filename)
	ctx.Response().Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%q; filename*=UTF-8''%s", filename, encodedFilename))

	// Add cache control headers to prevent browser caching
	ctx.Response().Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
	ctx.Response().Header().Set("Pragma", "no-cache")
	ctx.Response().Header().Set("Expires", "0")

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Range filter species CSV exported", logger.Int("species_count", len(speciesList)))
	return ctx.Blob(http.StatusOK, "text/csv; charset=utf-8", csvBytes)
}

// getTestSpeciesList gets species list with test parameters (helper for CSV export)
func (c *Controller) getTestSpeciesList(req RangeFilterTestRequest) ([]RangeFilterSpecies, Location, float32, error) {
	// Check if BirdNET is available
	birdnetInstance, err := c.getBirdNETInstance()
	if err != nil {
		return nil, Location{}, 0, err
	}

	// Use mutex to protect against concurrent modifications to global settings
	rangeFilterMutex.Lock()
	defer rangeFilterMutex.Unlock()

	// Temporarily set test values and restore after testing
	restore := c.swapRangeFilterSettings(req.Latitude, req.Longitude, req.Threshold)
	defer restore()

	// Use current date and calculate week
	testDate := time.Now()
	week := calculateWeek(testDate)

	// Get probable species for the test parameters
	speciesScores, err := birdnetInstance.GetProbableSpecies(testDate, week)
	if err != nil {
		return nil, Location{}, 0, err
	}

	// Convert to response format
	speciesList := make([]RangeFilterSpecies, 0, len(speciesScores))
	for _, speciesScore := range speciesScores {
		sp := detection.ParseSpeciesString(speciesScore.Label)

		// Create score pointer for non-nil value
		score := speciesScore.Score
		species := RangeFilterSpecies{
			Label:          speciesScore.Label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
			Score:          &score,
		}

		speciesList = append(speciesList, species)
	}

	location := Location{
		Latitude:  req.Latitude,
		Longitude: req.Longitude,
	}

	return speciesList, location, req.Threshold, nil
}

// sanitizeCSVField sanitizes CSV fields to prevent spreadsheet formula injection
func sanitizeCSVField(field string) string {
	if field == "" {
		return field
	}
	// Check if field starts with dangerous characters
	if strings.HasPrefix(field, "=") || strings.HasPrefix(field, "+") ||
		strings.HasPrefix(field, "-") || strings.HasPrefix(field, "@") {
		// Prefix with single quote to neutralize formula
		return "'" + field
	}
	return field
}

// generateSpeciesCSV generates CSV content from species list using the standard CSV library
func (c *Controller) generateSpeciesCSV(species []RangeFilterSpecies, location Location, threshold float32) ([]byte, error) {
	var buf bytes.Buffer

	// Write UTF-8 BOM for Excel compatibility
	buf.WriteString("\uFEFF")

	// Write metadata headers as comments (not part of CSV data)
	buf.WriteString("# BirdNET-Go Range Filter Species Export\n")
	buf.WriteString(fmt.Sprintf("# Generated: %s\n", time.Now().Format(time.RFC3339)))
	buf.WriteString(fmt.Sprintf("# Location: %.6f, %.6f\n", location.Latitude, location.Longitude))
	buf.WriteString(fmt.Sprintf("# Threshold: %.2f\n", threshold))
	buf.WriteString(fmt.Sprintf("# Total Species: %d\n", len(species)))
	buf.WriteString("#\n")

	// Create CSV writer
	writer := csv.NewWriter(&buf)

	// Write CSV header (sanitized)
	headerRow := []string{
		sanitizeCSVField("Scientific Name"),
		sanitizeCSVField("Common Name"),
		sanitizeCSVField("Probability Score"),
	}
	if err := writer.Write(headerRow); err != nil {
		return nil, fmt.Errorf("failed to write CSV header: %w", err)
	}

	// Write species data
	for _, s := range species {
		// Format score (if available)
		scoreStr := "N/A"
		if s.Score != nil {
			scoreStr = fmt.Sprintf("%.4f", *s.Score)
		}

		// Sanitize all fields before writing
		row := []string{
			sanitizeCSVField(s.ScientificName),
			sanitizeCSVField(s.CommonName),
			sanitizeCSVField(scoreStr),
		}

		if err := writer.Write(row); err != nil {
			return nil, fmt.Errorf("failed to write CSV row: %w", err)
		}
	}

	// Flush any buffered data
	writer.Flush()

	// Check for any errors during writing
	if err := writer.Error(); err != nil {
		return nil, fmt.Errorf("CSV writer error: %w", err)
	}

	return buf.Bytes(), nil
}

// parseFloat64 is a helper function to parse string to float64
func parseFloat64(s string) (float64, error) {
	return strconv.ParseFloat(s, 64)
}

// parseFloat32 is a helper function to parse string to float32
func parseFloat32(s string) (float32, error) {
	f, err := strconv.ParseFloat(s, 32)
	return float32(f), err
}

// RebuildRangeFilter rebuilds the range filter with current settings
// @Summary Rebuild range filter
// @Description Rebuilds the range filter using current location and threshold settings
// @Tags range
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/rebuild [post]
func (c *Controller) RebuildRangeFilter(ctx echo.Context) error {
	// Check if BirdNET is available
	birdnetInstance, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, err.Error(), http.StatusInternalServerError)
	}

	// Rebuild the range filter
	err = birdnet.BuildRangeFilter(birdnetInstance)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to rebuild range filter", http.StatusInternalServerError)
	}

	// Get the updated count
	includedSpecies := c.Settings.GetIncludedSpecies()

	response := map[string]any{
		"success":     true,
		"message":     "Range filter rebuilt successfully",
		"count":       len(includedSpecies),
		"lastUpdated": c.Settings.BirdNET.RangeFilter.LastUpdated,
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Range filter rebuilt successfully", logger.Int("species_count", len(includedSpecies)))
	return ctx.JSON(http.StatusOK, response)
}
