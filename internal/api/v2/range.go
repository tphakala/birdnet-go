// range.go contains API v2 endpoints for range filter operations
package api

import (
	"bytes"
	"encoding/csv"
	"fmt"
	"maps"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Range filter constants (file-local)
const (
	weeksPerMonth = 4                  // Simplified ML model uses 4 weeks per month
	weeksPerYear  = weeksPerMonth * 12 // 48 weeks per year in BirdNET's model
	daysPerWeek   = 7                  // Days in a week for week calculation
)

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
	if req.Week != 0 && (req.Week < 1 || req.Week > weeksPerYear) {
		return fmt.Errorf("Week must be between 1 and %d", weeksPerYear) //nolint:staticcheck // user-facing API message
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
func (c *Controller) getBirdNETInstance() (*classifier.Orchestrator, error) {
	if c.Processor == nil {
		return nil, fmt.Errorf("BirdNET processor not available")
	}
	instance := c.Processor.GetBirdNET()
	if instance == nil {
		return nil, fmt.Errorf("BirdNET instance not available")
	}
	return instance, nil
}

// buildTestSettings creates a settings snapshot with the given test coordinates
// and threshold for range filter testing. The snapshot is a clone of the
// current settings with only the test values overridden, so it can be passed
// directly to GetProbableSpeciesWithSettings without modifying global state.
func (c *Controller) buildTestSettings(lat, lon float64, threshold float32) *conf.Settings {
	c.settingsMutex.RLock()
	testSnapshot := conf.CloneSettings(conf.CurrentOrFallback(c.Settings))
	c.settingsMutex.RUnlock()

	testSnapshot.BirdNET.Latitude = lat
	testSnapshot.BirdNET.Longitude = lon
	testSnapshot.BirdNET.RangeFilter.Threshold = threshold
	testSnapshot.BirdNET.LocationConfigured = true
	return testSnapshot
}

// convertSpeciesScores converts classifier.SpeciesScore entries to the API
// response format with probability score pointers.
func convertSpeciesScores(scores []classifier.SpeciesScore) []RangeFilterSpecies {
	species := make([]RangeFilterSpecies, 0, len(scores))
	for _, s := range scores {
		sp := detection.ParseSpeciesString(s.Label)
		score := s.Score
		species = append(species, RangeFilterSpecies{
			Label:          s.Label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
			Score:          &score,
		})
	}
	return species
}

// convertLabels converts string labels to the API response format without scores.
func convertLabels(labels []string) []RangeFilterSpecies {
	species := make([]RangeFilterSpecies, 0, len(labels))
	for _, label := range labels {
		sp := detection.ParseSpeciesString(label)
		species = append(species, RangeFilterSpecies{
			Label:          label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
		})
	}
	return species
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

// TaxonomyFamily represents a bird family with scientific and common names
type TaxonomyFamily struct {
	Name       string `json:"name"`       // Scientific name, e.g. "Strigidae"
	CommonName string `json:"commonName"` // Common name, e.g. "Owls"
}

// RangeFilterSpeciesList represents the full list response for range filter species
type RangeFilterSpeciesList struct {
	Species     []RangeFilterSpecies `json:"species"`
	Count       int                  `json:"count"`
	LastUpdated time.Time            `json:"lastUpdated"`
	Threshold   float32              `json:"threshold"`
	Location    Location             `json:"location"`
	Genera      []string             `json:"genera"`
	Families    []TaxonomyFamily     `json:"families"`
	Orders      []string             `json:"orders"`
}

// RangeFilterScoresResponse represents all species with their raw geomodel scores
type RangeFilterScoresResponse struct {
	Species   []RangeFilterSpecies `json:"species"`
	Count     int                  `json:"count"`
	Location  Location             `json:"location"`
	Week      int                  `json:"week"`
	Threshold float32              `json:"threshold"`
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
	// Range filter status and scores
	c.Group.GET("/range/status", c.GetRangeFilterStatus)
	c.Group.GET("/range/species/scores", c.GetRangeFilterSpeciesScores)

	// Range filter species routes
	c.Group.GET("/range/species/count", c.GetRangeFilterSpeciesCount)
	c.Group.GET("/range/species/list", c.GetRangeFilterSpeciesList)
	c.Group.GET("/range/species/csv", c.GetRangeFilterSpeciesCSV)
	c.Group.POST("/range/species/test", c.TestRangeFilter)
	c.Group.POST("/range/rebuild", c.RebuildRangeFilter)
}

// GetRangeFilterStatus returns introspection data about the active range filter
// @Summary Get range filter status
// @Description Returns per-classifier geomodel coverage, auto-selection status, and threshold
// @Tags range
// @Produce json
// @Success 200 {object} classifier.RangeFilterStatusResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/status [get]
func (c *Controller) GetRangeFilterStatus(ctx echo.Context) error {
	birdnetInstance, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, birdnetInstance.RangeFilterStatus())
}

// GetRangeFilterSpeciesScores returns all species with their raw geomodel probability scores
// @Summary Get range filter species scores
// @Description Returns all species with raw geomodel scores, using current or custom location and week
// @Tags range
// @Produce json
// @Param lat query number false "Custom latitude (uses current settings if not provided)"
// @Param lon query number false "Custom longitude (uses current settings if not provided)"
// @Param week query integer false "Custom week 1-48 (uses current date if not provided)"
// @Success 200 {object} RangeFilterScoresResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/species/scores [get]
func (c *Controller) GetRangeFilterSpeciesScores(ctx echo.Context) error {
	birdnetInstance, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	// Read defaults from latest published settings snapshot so UI changes
	// to coordinates take effect without restart.
	c.settingsMutex.RLock()
	settings := conf.CurrentOrFallback(c.Settings)
	lat := settings.BirdNET.Latitude
	lon := settings.BirdNET.Longitude
	c.settingsMutex.RUnlock()

	// Override with query params if provided
	if latStr := ctx.QueryParam("lat"); latStr != "" {
		parsed, err := parseFloat64(latStr)
		if err != nil {
			return c.HandleError(ctx, err, "Invalid latitude format", http.StatusBadRequest)
		}
		if parsed < -90 || parsed > 90 {
			return c.HandleError(ctx, nil, "Latitude must be between -90 and 90", http.StatusBadRequest)
		}
		lat = parsed
	}

	if lonStr := ctx.QueryParam("lon"); lonStr != "" {
		parsed, err := parseFloat64(lonStr)
		if err != nil {
			return c.HandleError(ctx, err, "Invalid longitude format", http.StatusBadRequest)
		}
		if parsed < -180 || parsed > 180 {
			return c.HandleError(ctx, nil, "Longitude must be between -180 and 180", http.StatusBadRequest)
		}
		lon = parsed
	}

	// Calculate week from current date or use provided value
	now := time.Now()
	week := calculateWeek(now)

	if weekStr := ctx.QueryParam("week"); weekStr != "" {
		parsed, err := strconv.Atoi(weekStr)
		if err != nil {
			return c.HandleError(ctx, err, "Invalid week format", http.StatusBadRequest)
		}
		if parsed < 1 || parsed > weeksPerYear {
			return c.HandleError(ctx, nil, fmt.Sprintf("Week must be between 1 and %d", weeksPerYear), http.StatusBadRequest)
		}
		week = float32(parsed)
	}

	// Build test settings with zero threshold to get ALL species with scores
	testSettings := c.buildTestSettings(lat, lon, 0)

	// Get all species with their raw scores
	speciesScores, err := birdnetInstance.GetProbableSpeciesWithSettings(now, week, testSettings)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get species scores", http.StatusInternalServerError)
	}

	speciesList := convertSpeciesScores(speciesScores)

	response := RangeFilterScoresResponse{
		Species:   speciesList,
		Count:     len(speciesList),
		Week:      int(week),
		Threshold: 0,
		Location: Location{
			Latitude:  lat,
			Longitude: lon,
		},
	}

	c.logAPIRequest(ctx, logger.LogLevelDebug, "Range filter species scores retrieved", logger.Int("species_count", len(speciesList)))
	return ctx.JSON(http.StatusOK, response)
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
	c.settingsMutex.RLock()
	settings := conf.CurrentOrFallback(c.Settings)
	c.settingsMutex.RUnlock()
	includedSpecies := settings.GetIncludedSpecies()

	response := RangeFilterSpeciesCount{
		Count:       len(includedSpecies),
		LastUpdated: settings.BirdNET.RangeFilter.LastUpdated,
		Threshold:   settings.BirdNET.RangeFilter.Threshold,
		Location: Location{
			Latitude:  settings.BirdNET.Latitude,
			Longitude: settings.BirdNET.Longitude,
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
	c.settingsMutex.RLock()
	settings := conf.CurrentOrFallback(c.Settings)
	c.settingsMutex.RUnlock()
	includedSpecies := settings.GetIncludedSpecies()
	speciesList := convertLabels(includedSpecies)

	// Extract taxonomy groups from species list via taxonomy DB
	var genera []string
	var families []TaxonomyFamily
	var orders []string

	if c.TaxonomyDB != nil {
		generaSet := make(map[string]struct{})
		familyMap := make(map[string]TaxonomyFamily) // keyed by lowercase family name
		orderSet := make(map[string]struct{})

		for _, sp := range speciesList {
			_, meta, ok := c.TaxonomyDB.LookupGenusByScientificName(sp.ScientificName)
			if !ok {
				continue // Species not in taxonomy DB — skip gracefully
			}

			// Extract genus (first word of scientific name)
			if genus, _, found := strings.Cut(sp.ScientificName, " "); found && len(genus) > 1 {
				generaSet[genus] = struct{}{}
			}

			// Extract family (with common name)
			familyKey := strings.ToLower(meta.Family)
			if _, exists := familyMap[familyKey]; !exists && meta.Family != "" {
				familyMap[familyKey] = TaxonomyFamily{
					Name:       meta.Family,
					CommonName: meta.FamilyCommon,
				}
			}

			// Extract order
			if meta.Order != "" {
				orderSet[meta.Order] = struct{}{}
			}
		}

		// Convert sets to sorted slices
		genera = slices.Collect(maps.Keys(generaSet))
		slices.Sort(genera)

		families = slices.Collect(maps.Values(familyMap))
		slices.SortFunc(families, func(a, b TaxonomyFamily) int {
			return strings.Compare(a.Name, b.Name)
		})

		orders = slices.Collect(maps.Keys(orderSet))
		slices.Sort(orders)
	}

	// Ensure non-nil slices for JSON ([] not null)
	if genera == nil {
		genera = []string{}
	}
	if families == nil {
		families = []TaxonomyFamily{}
	}
	if orders == nil {
		orders = []string{}
	}

	response := RangeFilterSpeciesList{
		Species:     speciesList,
		Count:       len(speciesList),
		LastUpdated: settings.BirdNET.RangeFilter.LastUpdated,
		Threshold:   settings.BirdNET.RangeFilter.Threshold,
		Location: Location{
			Latitude:  settings.BirdNET.Latitude,
			Longitude: settings.BirdNET.Longitude,
		},
		Genera:   genera,
		Families: families,
		Orders:   orders,
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
		return c.HandleError(ctx, err, "Invalid range filter parameters", http.StatusBadRequest)
	}

	// Parse date
	testDate, err := parseTestDate(req.Date)
	if err != nil {
		return c.HandleError(ctx, err, "Date must be in YYYY-MM-DD format", http.StatusBadRequest)
	}

	// Check if processor and BirdNET are available
	birdnetInstance, err := c.getBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	// Build a local settings snapshot with test values; no global state is
	// modified, so concurrent BuildRangeFilter calls are unaffected.
	testSettings := c.buildTestSettings(req.Latitude, req.Longitude, req.Threshold)

	// Calculate week if not provided
	week := req.Week
	if week == 0 {
		week = calculateWeek(testDate)
	}

	// Get probable species for the test parameters
	speciesScores, err := birdnetInstance.GetProbableSpeciesWithSettings(testDate, week, testSettings)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get probable species", http.StatusInternalServerError)
	}

	speciesList := convertSpeciesScores(speciesScores)

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

		// Read current settings under lock for defaults
		c.settingsMutex.RLock()
		defaults := conf.CurrentOrFallback(c.Settings)
		c.settingsMutex.RUnlock()
		testReq.Latitude = defaults.BirdNET.Latitude
		testReq.Longitude = defaults.BirdNET.Longitude
		testReq.Threshold = defaults.BirdNET.RangeFilter.Threshold

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
		c.settingsMutex.RLock()
		settings := conf.CurrentOrFallback(c.Settings)
		c.settingsMutex.RUnlock()

		speciesList = convertLabels(settings.GetIncludedSpecies())
		location = Location{
			Latitude:  settings.BirdNET.Latitude,
			Longitude: settings.BirdNET.Longitude,
		}
		threshold = settings.BirdNET.RangeFilter.Threshold
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

	testSettings := c.buildTestSettings(req.Latitude, req.Longitude, req.Threshold)

	// Use current date and calculate week
	testDate := time.Now()
	week := calculateWeek(testDate)

	// Get probable species for the test parameters
	speciesScores, err := birdnetInstance.GetProbableSpeciesWithSettings(testDate, week, testSettings)
	if err != nil {
		return nil, Location{}, 0, err
	}

	speciesList := convertSpeciesScores(speciesScores)

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
	fmt.Fprintf(&buf, "# Generated: %s\n", time.Now().Format(time.RFC3339))
	fmt.Fprintf(&buf, "# Location: %.6f, %.6f\n", location.Latitude, location.Longitude)
	fmt.Fprintf(&buf, "# Threshold: %.2f\n", threshold)
	fmt.Fprintf(&buf, "# Total Species: %d\n", len(species))
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
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	// Rebuild the range filter
	err = classifier.BuildRangeFilter(birdnetInstance)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to rebuild range filter", http.StatusInternalServerError)
	}

	// Read from the latest published snapshot so the just-published rebuild
	// result is reflected immediately.
	settings := conf.CurrentOrFallback(c.Settings)
	includedSpecies := settings.GetIncludedSpecies()
	lastUpdated := settings.BirdNET.RangeFilter.LastUpdated

	response := map[string]any{
		"success":     true,
		"message":     "Range filter rebuilt successfully",
		"count":       len(includedSpecies),
		"lastUpdated": lastUpdated,
	}

	c.logAPIRequest(ctx, logger.LogLevelInfo, "Range filter rebuilt successfully", logger.Int("species_count", len(includedSpecies)))
	return ctx.JSON(http.StatusOK, response)
}
