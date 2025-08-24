// range.go contains API v2 endpoints for range filter operations
package api

import (
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// rangeFilterMutex protects against concurrent modifications to global settings during testing
var rangeFilterMutex sync.Mutex

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
		scientificName, commonName, _ := observation.ParseSpeciesString(label)

		species := RangeFilterSpecies{
			Label:          label,
			ScientificName: scientificName,
			CommonName:     commonName,
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

	// Basic validation
	if req.Latitude < -90 || req.Latitude > 90 {
		return c.HandleError(ctx, nil, "Latitude must be between -90 and 90", http.StatusBadRequest)
	}
	if req.Longitude < -180 || req.Longitude > 180 {
		return c.HandleError(ctx, nil, "Longitude must be between -180 and 180", http.StatusBadRequest)
	}
	if req.Threshold < 0 || req.Threshold > 1 {
		return c.HandleError(ctx, nil, "Threshold must be between 0 and 1", http.StatusBadRequest)
	}

	// Parse date if provided
	var testDate time.Time
	var err error
	if req.Date != "" {
		testDate, err = time.Parse("2006-01-02", req.Date)
		if err != nil {
			return c.HandleError(ctx, err, "Date must be in YYYY-MM-DD format", http.StatusBadRequest)
		}
	} else {
		testDate = time.Now()
	}

	// Check if processor and BirdNET are available
	if c.Processor == nil {
		return c.HandleError(ctx, nil, "BirdNET processor not available", http.StatusInternalServerError)
	}

	birdnetInstance := c.Processor.GetBirdNET()
	if birdnetInstance == nil {
		return c.HandleError(ctx, nil, "BirdNET instance not available", http.StatusInternalServerError)
	}

	// Use mutex to protect against concurrent modifications to global settings
	rangeFilterMutex.Lock()
	defer rangeFilterMutex.Unlock()

	// Store original values from controller settings
	originalLat := c.Settings.BirdNET.Latitude
	originalLon := c.Settings.BirdNET.Longitude
	originalThreshold := c.Settings.BirdNET.RangeFilter.Threshold

	// Temporarily set test values in controller settings
	c.Settings.BirdNET.Latitude = req.Latitude
	c.Settings.BirdNET.Longitude = req.Longitude
	c.Settings.BirdNET.RangeFilter.Threshold = req.Threshold

	// Restore original settings after testing
	defer func() {
		c.Settings.BirdNET.Latitude = originalLat
		c.Settings.BirdNET.Longitude = originalLon
		c.Settings.BirdNET.RangeFilter.Threshold = originalThreshold
	}()

	// Calculate week if not provided
	week := req.Week
	if week == 0 {
		// BirdNET range filter model expects a custom week numbering system where each month
		// has exactly 4 weeks, totaling 48 weeks per year instead of the standard 52 weeks.
		// This is the expected format for the ML model and must be used consistently.
		// Use the same calculation as in range_filter.go
		month := int(testDate.Month())
		day := testDate.Day()
		weeksFromMonths := (month - 1) * 4
		weekInMonth := (day-1)/7 + 1
		week = float32(weeksFromMonths + weekInMonth)
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
		scientificName, commonName, _ := observation.ParseSpeciesString(speciesScore.Label)

		// Create score pointer for non-nil value
		score := speciesScore.Score
		species := RangeFilterSpecies{
			Label:          speciesScore.Label,
			ScientificName: scientificName,
			CommonName:     commonName,
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

	c.logAPIRequest(ctx, 1, "Range filter test completed", "species_count", len(speciesList))
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
			scientificName, commonName, _ := observation.ParseSpeciesString(label)
			
			species := RangeFilterSpecies{
				Label:          label,
				ScientificName: scientificName,
				CommonName:     commonName,
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
	csvContent := c.generateSpeciesCSV(speciesList, location, threshold)
	
	// Set headers for file download
	filename := "birdnet_range_filter_species_" + time.Now().Format("20060102_150405") + ".csv"
	ctx.Response().Header().Set("Content-Type", "text/csv; charset=utf-8")
	ctx.Response().Header().Set("Content-Disposition", "attachment; filename=\""+filename+"\"")
	
	c.logAPIRequest(ctx, 1, "Range filter species CSV exported", "species_count", len(speciesList))
	return ctx.String(http.StatusOK, csvContent)
}

// getTestSpeciesList gets species list with test parameters (helper for CSV export)
func (c *Controller) getTestSpeciesList(req RangeFilterTestRequest) ([]RangeFilterSpecies, Location, float32, error) {
	// Check if processor and BirdNET are available
	if c.Processor == nil {
		return nil, Location{}, 0, fmt.Errorf("BirdNET processor not available")
	}
	
	birdnetInstance := c.Processor.GetBirdNET()
	if birdnetInstance == nil {
		return nil, Location{}, 0, fmt.Errorf("BirdNET instance not available")
	}
	
	// Use mutex to protect against concurrent modifications to global settings
	rangeFilterMutex.Lock()
	defer rangeFilterMutex.Unlock()
	
	// Store original values from controller settings
	originalLat := c.Settings.BirdNET.Latitude
	originalLon := c.Settings.BirdNET.Longitude
	originalThreshold := c.Settings.BirdNET.RangeFilter.Threshold
	
	// Temporarily set test values in controller settings
	c.Settings.BirdNET.Latitude = req.Latitude
	c.Settings.BirdNET.Longitude = req.Longitude
	c.Settings.BirdNET.RangeFilter.Threshold = req.Threshold
	
	// Restore original settings after testing
	defer func() {
		c.Settings.BirdNET.Latitude = originalLat
		c.Settings.BirdNET.Longitude = originalLon
		c.Settings.BirdNET.RangeFilter.Threshold = originalThreshold
	}()
	
	// Use current date and calculate week
	testDate := time.Now()
	month := int(testDate.Month())
	day := testDate.Day()
	weeksFromMonths := (month - 1) * 4
	weekInMonth := (day-1)/7 + 1
	week := float32(weeksFromMonths + weekInMonth)
	
	// Get probable species for the test parameters
	speciesScores, err := birdnetInstance.GetProbableSpecies(testDate, week)
	if err != nil {
		return nil, Location{}, 0, err
	}
	
	// Convert to response format
	speciesList := make([]RangeFilterSpecies, 0, len(speciesScores))
	for _, speciesScore := range speciesScores {
		scientificName, commonName, _ := observation.ParseSpeciesString(speciesScore.Label)
		
		// Create score pointer for non-nil value
		score := speciesScore.Score
		species := RangeFilterSpecies{
			Label:          speciesScore.Label,
			ScientificName: scientificName,
			CommonName:     commonName,
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

// generateSpeciesCSV generates CSV content from species list
func (c *Controller) generateSpeciesCSV(species []RangeFilterSpecies, location Location, threshold float32) string {
	var csvBuilder strings.Builder
	
	// Write metadata header
	csvBuilder.WriteString("# BirdNET-Go Range Filter Species Export\n")
	csvBuilder.WriteString(fmt.Sprintf("# Generated: %s\n", time.Now().Format(time.RFC3339)))
	csvBuilder.WriteString(fmt.Sprintf("# Location: %.6f, %.6f\n", location.Latitude, location.Longitude))
	csvBuilder.WriteString(fmt.Sprintf("# Threshold: %.2f\n", threshold))
	csvBuilder.WriteString(fmt.Sprintf("# Total Species: %d\n", len(species)))
	csvBuilder.WriteString("#\n")
	
	// Write CSV header
	csvBuilder.WriteString("Scientific Name,Common Name,Probability Score\n")
	
	// Write species data
	for _, s := range species {
		// Escape commas in names if present
		scientificName := s.ScientificName
		commonName := s.CommonName
		
		// Quote fields if they contain commas
		// Using manual CSV escaping instead of %q to follow CSV RFC 4180 standard
		if strings.Contains(scientificName, ",") {
			scientificName = fmt.Sprintf("\"%s\"", strings.ReplaceAll(scientificName, "\"", "\"\"")) //nolint:gocritic // CSV escaping, not Go quoting
		}
		if strings.Contains(commonName, ",") {
			commonName = fmt.Sprintf("\"%s\"", strings.ReplaceAll(commonName, "\"", "\"\"")) //nolint:gocritic // CSV escaping, not Go quoting
		}
		
		// Format score (if available)
		scoreStr := "N/A"
		if s.Score != nil {
			scoreStr = fmt.Sprintf("%.4f", *s.Score)
		}
		
		csvBuilder.WriteString(fmt.Sprintf("%s,%s,%s\n", scientificName, commonName, scoreStr))
	}
	
	return csvBuilder.String()
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
	// Check if processor and BirdNET are available
	if c.Processor == nil {
		return c.HandleError(ctx, nil, "BirdNET processor not available", http.StatusInternalServerError)
	}

	birdnetInstance := c.Processor.GetBirdNET()
	if birdnetInstance == nil {
		return c.HandleError(ctx, nil, "BirdNET instance not available", http.StatusInternalServerError)
	}

	// Rebuild the range filter
	err := birdnet.BuildRangeFilter(birdnetInstance)
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

	c.logAPIRequest(ctx, 1, "Range filter rebuilt successfully", "species_count", len(includedSpecies))
	return ctx.JSON(http.StatusOK, response)
}
