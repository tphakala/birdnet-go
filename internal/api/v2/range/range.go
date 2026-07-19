// Package rangeapi is the api/v2 range-filter domain handler. It owns the
// /api/v2/range/* endpoints (status, species scores/count/list/CSV, test, and
// rebuild). The package lives in directory internal/api/v2/range but cannot be
// named "range" (a Go reserved word), so it is named rangeapi and imported under
// that alias. The Handler embeds *apicore.Core by pointer so the shared
// dependencies and helpers (Processor, TaxonomyDB, HandleError, CurrentSettings,
// the logging helpers, GetBirdNETInstance) promote onto it; the facade
// constructs one Handler and calls RegisterRoutes to wire the routes in their
// existing order.
package rangeapi

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
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/dto"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// Handler serves the range-filter domain endpoints. It embeds *apicore.Core BY
// POINTER so the shared Core members promote onto it without re-wiring; Core
// carries atomic/lock-bearing fields and must never be copied by value.
type Handler struct {
	*apicore.Core
}

// New builds a range Handler around the shared core.
func New(core *apicore.Core) *Handler {
	return &Handler{core}
}

// RegisterRoutes registers all range-filter related API endpoints on the
// supplied API v2 group, preserving the exact routes and order the facade used
// before the range domain was extracted.
func (c *Handler) RegisterRoutes(g *echo.Group) {
	// Range filter status and scores
	g.GET("/range/status", c.GetRangeFilterStatus)
	g.GET("/range/species/scores", c.GetRangeFilterSpeciesScores)

	// Range filter species routes
	g.GET("/range/species/count", c.GetRangeFilterSpeciesCount)
	g.GET("/range/species/list", c.GetRangeFilterSpeciesList)
	g.GET("/range/species/csv", c.GetRangeFilterSpeciesCSV)
	g.POST("/range/species/test", c.TestRangeFilter)
	g.POST("/range/rebuild", c.RebuildRangeFilter)
}

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
	if req.Week != 0 && (req.Week < 1 || req.Week > apicore.WeeksPerYear) {
		return fmt.Errorf("Week must be between 1 and %d", apicore.WeeksPerYear) //nolint:staticcheck // user-facing API message
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
	weeksFromMonths := (month - 1) * apicore.WeeksPerMonth
	weekInMonth := min((day-1)/apicore.DaysPerWeek+1, apicore.WeeksPerMonth)
	return float32(weeksFromMonths + weekInMonth)
}

// buildTestSettings creates a settings snapshot with the given test coordinates
// and threshold for range filter testing. The snapshot is a clone of the
// current settings with only the test values overridden, so it can be passed
// directly to GetProbableSpeciesWithSettings without modifying global state.
func (c *Handler) buildTestSettings(lat, lon float64, threshold float32) *conf.Settings {
	testSnapshot := conf.CloneSettings(c.CurrentSettings())

	testSnapshot.BirdNET.Latitude = lat
	testSnapshot.BirdNET.Longitude = lon
	testSnapshot.BirdNET.RangeFilter.Threshold = threshold
	testSnapshot.BirdNET.LocationConfigured = true
	return testSnapshot
}

// resolveLocalizedName returns a locale-appropriate common name for the given
// species. It tries the name resolver first; if that returns empty it falls
// back to the common name parsed from the label string.
func resolveLocalizedName(resolver *classifier.Orchestrator, locale string, sp detection.Species) string {
	if resolver != nil && locale != "" {
		if resolved := resolver.ResolveName(sp.ScientificName, locale); resolved != "" {
			return resolved
		}
	}
	return sp.CommonName
}

// convertSpeciesScores converts classifier.SpeciesScore entries to the API
// response format with probability score pointers. When resolver and locale
// are provided, common names are resolved to the user's locale.
func convertSpeciesScores(scores []classifier.SpeciesScore, resolver *classifier.Orchestrator, locale string) []dto.RangeFilterSpecies {
	return buildRangeFilterSpecies(scores, func(label string) string {
		return resolveLocalizedName(resolver, locale, detection.ParseSpeciesString(label))
	})
}

// convertSpeciesScoresNoNames converts geomodel scores without resolving localized
// common names. Name resolution is the dominant cost when converting all geomodel
// species (threshold 0), so callers that only need scientificName->score skip it.
func convertSpeciesScoresNoNames(scores []classifier.SpeciesScore) []dto.RangeFilterSpecies {
	return buildRangeFilterSpecies(scores, nil)
}

// buildRangeFilterSpecies converts geomodel scores into API species entries. When
// resolveName is non-nil it supplies the localized common name for each label;
// passing nil skips common-name resolution entirely (the expensive step for large
// species sets). The scientific name is extracted with detection.ExtractScientificName,
// which avoids the per-label slice allocation of ParseSpeciesString.
func buildRangeFilterSpecies(scores []classifier.SpeciesScore, resolveName func(label string) string) []dto.RangeFilterSpecies {
	species := make([]dto.RangeFilterSpecies, 0, len(scores))
	for _, s := range scores {
		score := s.Score
		entry := dto.RangeFilterSpecies{
			Label:          s.Label,
			ScientificName: detection.ExtractScientificName(s.Label),
			Score:          &score,
		}
		if s.HasCustomConfig {
			b := true
			entry.HasCustomConfig = &b
		}
		if s.IsManuallyIncluded {
			b := true
			entry.IsManuallyIncluded = &b
		}
		if resolveName != nil {
			entry.CommonName = resolveName(s.Label)
		}
		species = append(species, entry)
	}
	return species
}

// convertLabels converts string labels to the API response format without scores.
// When resolver and locale are provided, common names are resolved to the
// user's locale.
func convertLabels(labels []string, resolver *classifier.Orchestrator, locale string) []dto.RangeFilterSpecies {
	species := make([]dto.RangeFilterSpecies, 0, len(labels))
	for _, label := range labels {
		sp := detection.ParseSpeciesString(label)
		species = append(species, dto.RangeFilterSpecies{
			Label:          label,
			ScientificName: sp.ScientificName,
			CommonName:     resolveLocalizedName(resolver, locale, sp),
		})
	}
	return species
}

// dedupeSpeciesForDisplay collapses rows that resolve to the same displayed
// species into a single row, for the user-facing range-filter species lists.
//
// Two entries are the same species when their localized common names match
// (case- and Unicode-NFC-insensitive); entries without a common name fall back
// to their scientific name so genuinely unresolved labels are not all merged
// into one bucket. This intentionally collapses both a geomodel-scored species
// and its force-include override copy (which carry different label strings for
// the same species) and a pair of taxonomic synonyms that localize to the same
// common name (e.g. "Cnephaeus nilssonii" and "Eptesicus nilssonii" both
// resolving to the Finnish "pohjanlepakko").
//
// De-duplication happens only here, at the display boundary, never in the
// functional inclusion set: conf.Settings.IsSpeciesIncluded matches detections
// on the scientific-name set, so both synonyms must remain in the included
// species for the engine to detect either name. The key is the same common name
// already resolved for display, so the survivor row is exactly what the user
// sees.
//
// Because the key is the resolved common name, two genuinely distinct species
// that happen to share one localized common name would also collapse. That is
// an accepted trade-off: OpenFauna common names are authoritative for display,
// the effect is display-only, and both scientific names remain in the inclusion
// set so detection of the hidden species is unaffected.
//
// On collision the higher score wins, so a force-included species at the
// always-active 1.0 sentinel survives over its lower range-filter probability.
// The first occurrence's position is preserved (the input arrives sorted by
// score descending), keeping the output order stable and deterministic.
func dedupeSpeciesForDisplay(species []dto.RangeFilterSpecies) []dto.RangeFilterSpecies {
	if len(species) < 2 {
		return species
	}
	indexByKey := make(map[string]int, len(species))
	deduped := make([]dto.RangeFilterSpecies, 0, len(species))
	for _, sp := range species {
		key := apicore.NormalizeForLookup(sp.CommonName)
		if key == "" {
			key = apicore.NormalizeForLookup(sp.ScientificName)
		}
		if key == "" {
			// No name to key on: keep the row rather than collapsing every
			// identity-less row into a single bucket.
			deduped = append(deduped, sp)
			continue
		}
		if idx, ok := indexByKey[key]; ok {
			var hasCustomConfig *bool
			if (sp.HasCustomConfig != nil && *sp.HasCustomConfig) || (deduped[idx].HasCustomConfig != nil && *deduped[idx].HasCustomConfig) {
				t := true
				hasCustomConfig = &t
			}
			var isManuallyIncluded *bool
			if (sp.IsManuallyIncluded != nil && *sp.IsManuallyIncluded) || (deduped[idx].IsManuallyIncluded != nil && *deduped[idx].IsManuallyIncluded) {
				t := true
				isManuallyIncluded = &t
			}

			if speciesScoreHigher(sp, deduped[idx]) {
				// Preserve the first occurrence's position, but surface the
				// higher-scored variant (defensive: the input is already sorted
				// score-descending, so this rarely triggers).
				sp.HasCustomConfig = hasCustomConfig
				sp.IsManuallyIncluded = isManuallyIncluded
				deduped[idx] = sp
			} else {
				deduped[idx].HasCustomConfig = hasCustomConfig
				deduped[idx].IsManuallyIncluded = isManuallyIncluded
			}
			continue
		}
		indexByKey[key] = len(deduped)
		deduped = append(deduped, sp)
	}
	return deduped
}

// speciesScoreHigher reports whether a has a strictly higher score than b. A nil
// score (label-only display rows) sorts below any real score, so a scored entry
// always wins over an unscored one.
func speciesScoreHigher(a, b dto.RangeFilterSpecies) bool {
	if a.Score == nil {
		return false
	}
	if b.Score == nil {
		return true
	}
	return *a.Score > *b.Score
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

// TaxonomyFamily represents a bird family with scientific and common names
type TaxonomyFamily struct {
	Name       string `json:"name"`       // Scientific name, e.g. "Strigidae"
	CommonName string `json:"commonName"` // Common name, e.g. "Owls"
}

// RangeFilterSpeciesList represents the full list response for range filter species
type RangeFilterSpeciesList struct {
	Species     []dto.RangeFilterSpecies `json:"species"`
	Count       int                      `json:"count"`
	LastUpdated time.Time                `json:"lastUpdated"`
	Threshold   float32                  `json:"threshold"`
	Location    Location                 `json:"location"`
	Genera      []string                 `json:"genera"`
	Families    []TaxonomyFamily         `json:"families"`
	Orders      []string                 `json:"orders"`
}

// RangeFilterScoresResponse represents all species with their raw geomodel scores
type RangeFilterScoresResponse struct {
	Species   []dto.RangeFilterSpecies `json:"species"`
	Count     int                      `json:"count"`
	Location  Location                 `json:"location"`
	Week      int                      `json:"week"`
	Threshold float32                  `json:"threshold"`
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
	Species    []dto.RangeFilterSpecies `json:"species"`
	Count      int                      `json:"count"`
	Threshold  float32                  `json:"threshold"`
	Location   Location                 `json:"location"`
	TestDate   time.Time                `json:"testDate"`
	Week       int                      `json:"week"`
	Parameters struct {
		InputLatitude  float64 `json:"inputLatitude"`
		InputLongitude float64 `json:"inputLongitude"`
		InputThreshold float32 `json:"inputThreshold"`
		InputDate      string  `json:"inputDate,omitempty"`
		InputWeek      float32 `json:"inputWeek,omitempty"`
	} `json:"parameters"`
}

// GetRangeFilterStatus returns introspection data about the active range filter
// @Summary Get range filter status
// @Description Returns per-classifier geomodel coverage, auto-selection status, and threshold
// @Tags range
// @Produce json
// @Success 200 {object} classifier.RangeFilterStatusResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/status [get]
func (c *Handler) GetRangeFilterStatus(ctx echo.Context) error {
	birdnetInstance, err := c.GetBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, birdnetInstance.RangeFilterStatus())
}

// GetRangeFilterSpeciesScores returns all species with their raw geomodel probability scores.
//
// This is a geomodel diagnostic endpoint: it intentionally uses the primary-only
// GetProbableSpeciesWithSettings, NOT the multi-model union. Always-active
// secondary-model species (bats/Perch) are excluded by design because they have
// no geomodel and therefore no probability score; representing them with the
// always-active 1.0 sentinel here would misrepresent it as a genuine 100%
// geomodel confidence. Endpoints that report the active species set (POST
// /range/species/test and the CSV export) do include secondary-model species.
// @Summary Get range filter species scores
// @Description Returns all species with raw geomodel scores (primary model only), using current or custom location and week. Always-active secondary-model species (e.g. bats) are excluded by design as they have no geomodel score.
// @Tags range
// @Produce json
// @Param lat query number false "Custom latitude (uses current settings if not provided)"
// @Param lon query number false "Custom longitude (uses current settings if not provided)"
// @Param week query integer false "Custom week 1-48 (uses current date if not provided)"
// @Success 200 {object} RangeFilterScoresResponse
// @Failure 400 {object} ErrorResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/species/scores [get]
func (c *Handler) GetRangeFilterSpeciesScores(ctx echo.Context) error {
	birdnetInstance, err := c.GetBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	// Read defaults from latest published settings snapshot so UI changes
	// to coordinates take effect without restart.
	settings := c.CurrentSettings()
	lat := settings.BirdNET.Latitude
	lon := settings.BirdNET.Longitude
	locale := settings.BirdNET.Locale

	// Override with query params if provided
	if latStr := ctx.QueryParam("lat"); latStr != "" {
		parsed, err := apicore.ParseFloat64(latStr)
		if err != nil {
			return c.HandleError(ctx, err, "Invalid latitude format", http.StatusBadRequest)
		}
		if parsed < -90 || parsed > 90 {
			return c.HandleError(ctx, nil, "Latitude must be between -90 and 90", http.StatusBadRequest)
		}
		lat = parsed
	}

	if lonStr := ctx.QueryParam("lon"); lonStr != "" {
		parsed, err := apicore.ParseFloat64(lonStr)
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
		if parsed < 1 || parsed > apicore.WeeksPerYear {
			return c.HandleError(ctx, nil, fmt.Sprintf("Week must be between 1 and %d", apicore.WeeksPerYear), http.StatusBadRequest)
		}
		week = float32(parsed)
	}

	// Build test settings with zero threshold to get ALL geomodel species with scores
	testSettings := c.buildTestSettings(lat, lon, 0)

	// Primary-only by design: this endpoint reports raw geomodel scores, so
	// always-active secondary-model species (bats/Perch) that have no geomodel
	// score are intentionally not included here. See the function doc comment.
	speciesScores, err := birdnetInstance.GetProbableSpeciesWithSettings(now, week, testSettings)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get species scores", http.StatusInternalServerError)
	}

	// Resolving localized common names is O(species) and dominates latency when
	// all geomodel species are requested (threshold 0). Callers that only need
	// scientificName->score (e.g. rare-species highlighting) pass names=false to
	// skip it. Names are included by default for backward compatibility.
	var speciesList []dto.RangeFilterSpecies
	if ctx.QueryParam("names") == "false" {
		speciesList = convertSpeciesScoresNoNames(speciesScores)
	} else {
		speciesList = convertSpeciesScores(speciesScores, birdnetInstance, locale)
	}

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

	c.LogAPIRequest(ctx, logger.LogLevelDebug, "Range filter species scores retrieved", logger.Int("species_count", len(speciesList)))
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
func (c *Handler) GetRangeFilterSpeciesCount(ctx echo.Context) error {
	settings := c.CurrentSettings()

	// Count the de-duplicated display list so the count matches what
	// /range/species/list renders (the same collapse of force-include override
	// copies and localized taxonomic synonyms).
	birdnetInstance, _ := c.GetBirdNETInstance()
	speciesList := dedupeSpeciesForDisplay(convertLabels(settings.GetIncludedSpecies(), birdnetInstance, settings.BirdNET.Locale))

	response := RangeFilterSpeciesCount{
		Count:       len(speciesList),
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
func (c *Handler) GetRangeFilterSpeciesList(ctx echo.Context) error {
	settings := c.CurrentSettings()
	includedSpecies := settings.GetIncludedSpecies()

	birdnetInstance, _ := c.GetBirdNETInstance()
	speciesList := dedupeSpeciesForDisplay(convertLabels(includedSpecies, birdnetInstance, settings.BirdNET.Locale))

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
				continue // Species not in taxonomy DB - skip gracefully
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
func (c *Handler) TestRangeFilter(ctx echo.Context) error {
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
	birdnetInstance, err := c.GetBirdNETInstance()
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
	speciesScores, err := birdnetInstance.GetAllProbableSpeciesWithSettings(testDate, week, testSettings)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to get probable species", http.StatusInternalServerError)
	}

	speciesList := dedupeSpeciesForDisplay(convertSpeciesScores(speciesScores, birdnetInstance, c.CurrentLocale()))

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

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Range filter test completed", logger.Int("species_count", len(speciesList)))
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
func (c *Handler) GetRangeFilterSpeciesCSV(ctx echo.Context) error {
	// Check for custom parameters in query string
	customLat := ctx.QueryParam("latitude")
	customLon := ctx.QueryParam("longitude")
	customThreshold := ctx.QueryParam("threshold")

	var speciesList []dto.RangeFilterSpecies
	var location Location
	var threshold float32

	// If custom parameters provided, test with those parameters
	if customLat != "" || customLon != "" || customThreshold != "" {
		// Parse custom parameters
		var testReq RangeFilterTestRequest

		// Read current settings from the lock-free atomic snapshot for defaults
		defaults := c.CurrentSettings()
		testReq.Latitude = defaults.BirdNET.Latitude
		testReq.Longitude = defaults.BirdNET.Longitude
		testReq.Threshold = defaults.BirdNET.RangeFilter.Threshold

		// Override with custom values if provided
		if customLat != "" {
			lat, err := apicore.ParseFloat64(customLat)
			if err != nil {
				return c.HandleError(ctx, err, "Invalid latitude format", http.StatusBadRequest)
			}
			if lat < -90 || lat > 90 {
				return c.HandleError(ctx, nil, "Latitude must be between -90 and 90", http.StatusBadRequest)
			}
			testReq.Latitude = lat
		}

		if customLon != "" {
			lon, err := apicore.ParseFloat64(customLon)
			if err != nil {
				return c.HandleError(ctx, err, "Invalid longitude format", http.StatusBadRequest)
			}
			if lon < -180 || lon > 180 {
				return c.HandleError(ctx, nil, "Longitude must be between -180 and 180", http.StatusBadRequest)
			}
			testReq.Longitude = lon
		}

		if customThreshold != "" {
			thr, err := apicore.ParseFloat32(customThreshold)
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
		// No custom parameters: export the currently persisted range filter
		// species (the applied filter, consistent with /range/species/list and
		// /range/species/count). The custom-parameter branch above recomputes the
		// full active set via getTestSpeciesList, which also includes always-active
		// secondary-model species. The settings UI always sends parameters, so it
		// hits that branch; this branch is the no-argument fallback.
		settings := c.CurrentSettings()

		birdnetInstance, _ := c.GetBirdNETInstance()
		speciesList = dedupeSpeciesForDisplay(convertLabels(settings.GetIncludedSpecies(), birdnetInstance, settings.BirdNET.Locale))
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

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Range filter species CSV exported", logger.Int("species_count", len(speciesList)))
	return ctx.Blob(http.StatusOK, "text/csv; charset=utf-8", csvBytes)
}

// getTestSpeciesList gets species list with test parameters (helper for CSV export).
//
// It uses GetAllProbableSpeciesWithSettings (the multi-model union) rather than
// the primary-only GetProbableSpeciesWithSettings so the CSV export matches what
// the user sees in the Active Species view for the same coordinates and
// threshold: range-filtered bird species plus always-active secondary-model
// species (bats/Perch). This is the path the settings UI's CSV download takes
// (it always sends parameters), so a user who sees bats in the Active Species
// preview gets the same bats when exporting those parameters to CSV.
func (c *Handler) getTestSpeciesList(req RangeFilterTestRequest) ([]dto.RangeFilterSpecies, Location, float32, error) {
	// Check if BirdNET is available
	birdnetInstance, err := c.GetBirdNETInstance()
	if err != nil {
		return nil, Location{}, 0, err
	}

	testSettings := c.buildTestSettings(req.Latitude, req.Longitude, req.Threshold)

	// Use current date and calculate week
	testDate := time.Now()
	week := calculateWeek(testDate)

	// Get probable species for the test parameters, including always-active
	// secondary-model species so the export stays consistent with the Active
	// Species view (POST /range/species/test uses the same union method).
	speciesScores, err := birdnetInstance.GetAllProbableSpeciesWithSettings(testDate, week, testSettings)
	if err != nil {
		return nil, Location{}, 0, err
	}

	speciesList := dedupeSpeciesForDisplay(convertSpeciesScores(speciesScores, birdnetInstance, c.CurrentLocale()))

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
func (c *Handler) generateSpeciesCSV(species []dto.RangeFilterSpecies, location Location, threshold float32) ([]byte, error) {
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

// RebuildRangeFilter rebuilds the range filter with current settings
// @Summary Rebuild range filter
// @Description Rebuilds the range filter using current location and threshold settings
// @Tags range
// @Produce json
// @Success 200 {object} map[string]interface{}
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/range/rebuild [post]
func (c *Handler) RebuildRangeFilter(ctx echo.Context) error {
	// Check if BirdNET is available
	birdnetInstance, err := c.GetBirdNETInstance()
	if err != nil {
		return c.HandleError(ctx, err, "BirdNET service not available", http.StatusInternalServerError)
	}

	// Rebuild the range filter (triggers heatmap cache invalidation via callback)
	err = classifier.BuildRangeFilter(birdnetInstance)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to rebuild range filter", http.StatusInternalServerError)
	}

	// Read from the latest published snapshot so the just-published rebuild
	// result is reflected immediately.
	settings := c.CurrentSettings()
	includedSpecies := settings.GetIncludedSpecies()
	lastUpdated := settings.BirdNET.RangeFilter.LastUpdated

	response := map[string]any{
		"success":     true,
		"message":     "Range filter rebuilt successfully",
		"count":       len(includedSpecies),
		"lastUpdated": lastUpdated,
	}

	c.LogAPIRequest(ctx, logger.LogLevelInfo, "Range filter rebuilt successfully", logger.Int("species_count", len(includedSpecies)))
	return ctx.JSON(http.StatusOK, response)
}
