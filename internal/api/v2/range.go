// range.go contains API v2 endpoints for range filter operations
package api

import (
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/observation"
)

// RangeFilterSpeciesCount represents the count response for range filter species
type RangeFilterSpeciesCount struct {
	Count       int       `json:"count"`
	LastUpdated time.Time `json:"lastUpdated"`
	Threshold   float32   `json:"threshold"`
	Location    struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
}

// RangeFilterSpecies represents a single species in the range filter
type RangeFilterSpecies struct {
	Label          string  `json:"label"`
	ScientificName string  `json:"scientificName"`
	CommonName     string  `json:"commonName"`
	Score          float64 `json:"score"`
}

// RangeFilterSpeciesList represents the full list response for range filter species
type RangeFilterSpeciesList struct {
	Species     []RangeFilterSpecies `json:"species"`
	Count       int                  `json:"count"`
	LastUpdated time.Time            `json:"lastUpdated"`
	Threshold   float32              `json:"threshold"`
	Location    struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
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
	Species   []RangeFilterSpecies `json:"species"`
	Count     int                  `json:"count"`
	Threshold float32              `json:"threshold"`
	Location  struct {
		Latitude  float64 `json:"latitude"`
		Longitude float64 `json:"longitude"`
	} `json:"location"`
	TestDate   time.Time `json:"testDate"`
	Week       int       `json:"week"`
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
	settings := conf.Setting()

	// Get current included species
	includedSpecies := settings.GetIncludedSpecies()

	response := RangeFilterSpeciesCount{
		Count:       len(includedSpecies),
		LastUpdated: settings.BirdNET.RangeFilter.LastUpdated,
		Threshold:   settings.BirdNET.RangeFilter.Threshold,
	}

	response.Location.Latitude = settings.BirdNET.Latitude
	response.Location.Longitude = settings.BirdNET.Longitude

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
	settings := conf.Setting()

	// Get current included species
	includedSpecies := settings.GetIncludedSpecies()

	// Convert to response format with parsed names
	var speciesList []RangeFilterSpecies
	for _, label := range includedSpecies {
		scientificName, commonName, _ := observation.ParseSpeciesString(label)

		species := RangeFilterSpecies{
			Label:          label,
			ScientificName: scientificName,
			CommonName:     commonName,
			Score:          1.0, // Current range filter species don't store individual scores
		}

		speciesList = append(speciesList, species)
	}

	response := RangeFilterSpeciesList{
		Species:     speciesList,
		Count:       len(speciesList),
		LastUpdated: settings.BirdNET.RangeFilter.LastUpdated,
		Threshold:   settings.BirdNET.RangeFilter.Threshold,
	}

	response.Location.Latitude = settings.BirdNET.Latitude
	response.Location.Longitude = settings.BirdNET.Longitude

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

	// Get current settings
	settings := conf.Setting()

	// Store original values
	originalLat := settings.BirdNET.Latitude
	originalLon := settings.BirdNET.Longitude
	originalThreshold := settings.BirdNET.RangeFilter.Threshold

	// Temporarily set test values
	settings.BirdNET.Latitude = req.Latitude
	settings.BirdNET.Longitude = req.Longitude
	settings.BirdNET.RangeFilter.Threshold = req.Threshold

	// Restore original settings after testing
	defer func() {
		settings.BirdNET.Latitude = originalLat
		settings.BirdNET.Longitude = originalLon
		settings.BirdNET.RangeFilter.Threshold = originalThreshold
	}()

	// Calculate week if not provided
	week := req.Week
	if week == 0 {
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
	var speciesList []RangeFilterSpecies
	for _, speciesScore := range speciesScores {
		scientificName, commonName, _ := observation.ParseSpeciesString(speciesScore.Label)

		species := RangeFilterSpecies{
			Label:          speciesScore.Label,
			ScientificName: scientificName,
			CommonName:     commonName,
			Score:          speciesScore.Score,
		}

		speciesList = append(speciesList, species)
	}

	response := RangeFilterTestResponse{
		Species:   speciesList,
		Count:     len(speciesList),
		Threshold: req.Threshold,
		TestDate:  testDate,
		Week:      int(week),
	}

	response.Location.Latitude = req.Latitude
	response.Location.Longitude = req.Longitude

	// Store original input parameters for reference
	response.Parameters.InputLatitude = req.Latitude
	response.Parameters.InputLongitude = req.Longitude
	response.Parameters.InputThreshold = req.Threshold
	response.Parameters.InputDate = req.Date
	response.Parameters.InputWeek = req.Week

	c.logAPIRequest(ctx, 1, "Range filter test completed", "species_count", len(speciesList))
	return ctx.JSON(http.StatusOK, response)
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
	settings := conf.Setting()
	includedSpecies := settings.GetIncludedSpecies()

	response := map[string]interface{}{
		"success":     true,
		"message":     "Range filter rebuilt successfully",
		"count":       len(includedSpecies),
		"lastUpdated": settings.BirdNET.RangeFilter.LastUpdated,
	}

	c.logAPIRequest(ctx, 1, "Range filter rebuilt successfully", "species_count", len(includedSpecies))
	return ctx.JSON(http.StatusOK, response)
}
