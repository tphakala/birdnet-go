package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
)

const defaultSearchTimeout = 60 * time.Second
const defaultPerPage = 20

// maxSearchSpeciesScientific caps how many client-resolved scientific names a
// single search may carry. The endpoint is public and each name triggers a label
// lookup, so this bounds the work an anonymous caller can request. A reverse
// dictionary match for an ambiguous or substring name stays well under this.
const maxSearchSpeciesScientific = 100

// initSearchRoutes registers the search-related routes
func (c *Controller) initSearchRoutes() {
	c.LogInfoIfEnabled("Initializing search routes")

	// Search endpoints - publicly accessible
	c.Group.POST("/search", c.HandleSearch)

	c.LogInfoIfEnabled("Search routes initialized successfully")
}

// SearchRequest defines the structure of the search API request
type SearchRequest struct {
	Species string `json:"species"`
	// SpeciesScientific carries exact scientific names the client resolved in the
	// browser from the per-visitor name dictionary (e.g. an ambiguous localized
	// common name resolving to several species). When set, these are matched in
	// addition to the free-text Species term. The list is capped server-side.
	SpeciesScientific []string `json:"speciesScientific,omitempty"`
	DateStart         string   `json:"dateStart"`
	DateEnd           string   `json:"dateEnd"`
	ConfidenceMin     float64  `json:"confidenceMin"`
	ConfidenceMax     float64  `json:"confidenceMax"`
	VerifiedStatus    string   `json:"verifiedStatus"`
	LockedStatus      string   `json:"lockedStatus"`
	DeviceFilter      string   `json:"deviceFilter"`
	TimeOfDay         string   `json:"timeOfDay"`
	Page              int      `json:"page"`
	SortBy            string   `json:"sortBy"`
}

// SearchResponse defines the structure of the search API response
type SearchResponse struct {
	Results     []datastore.DetectionRecord `json:"results"`
	Total       int                         `json:"total"`
	Pages       int                         `json:"pages"`
	CurrentPage int                         `json:"currentPage"`
}

// HandleSearch processes search requests
func (c *Controller) HandleSearch(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	c.LogInfoIfEnabled("Handling search request", logger.String("path", path), logger.String("ip", ip))

	// Parse the request
	var req SearchRequest
	if err := ctx.Bind(&req); err != nil {
		c.LogErrorIfEnabled("Failed to bind search request", logger.Error(err), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Invalid request format", http.StatusBadRequest)
	}

	// Validate and normalize the request
	if err := c.validateAndNormalizeSearchRequest(ctx, &req); err != nil {
		return c.HandleError(ctx, err, "Invalid search parameters", http.StatusBadRequest)
	}

	// Bound and clean the client-resolved scientific name list before it reaches
	// the datastore (public endpoint; each name is a label lookup).
	req.SpeciesScientific = sanitizeSpeciesScientific(req.SpeciesScientific)

	originalSpecies := req.Species
	resolved, hit := c.resolveSpeciesToScientific(req.Species)
	req.Species = resolved
	if hit {
		c.LogDebugIfEnabled("Resolved common-name query to scientific name",
			logger.String("input", originalSpecies),
			logger.String("resolved", req.Species),
			logger.String("path", path),
			logger.String("ip", ip),
		)
	} else if originalSpecies != "" {
		// The species term did not map to a known scientific name; the query falls
		// back to substring/LIKE. Log it so "unresolvable name" is distinguishable
		// from "resolved but no detections" when triaging an empty result.
		c.LogDebugIfEnabled("Species query did not resolve to a scientific name, using substring search",
			logger.String("input", originalSpecies),
			logger.String("path", path),
			logger.String("ip", ip),
		)
	}

	// Log validated request parameters
	c.logValidatedRequest(path, ip, &req)

	// Create context with timeout
	ctxTimeout, cancel := context.WithTimeout(ctx.Request().Context(), defaultSearchTimeout)
	defer cancel()

	// Build filters
	filters := c.buildSearchFilters(&req, ctxTimeout)
	c.LogDebugIfEnabled("Executing search with filters", logger.Any("filters", filters), logger.String("path", path), logger.String("ip", ip))

	// Execute the search
	results, total, err := c.DS.SearchDetections(&filters)
	if err != nil {
		c.LogErrorIfEnabled("Search query failed", logger.Error(err), logger.String("filters", fmt.Sprintf("%+v", filters)), logger.String("path", path), logger.String("ip", ip))
		return c.HandleError(ctx, err, "Search failed", http.StatusInternalServerError)
	}

	// Source display names can embed internal host details for stream sources
	// without a user-configured name. This endpoint is public, so hide the
	// source from unauthenticated clients, matching the anonymization done by
	// the audio source listing endpoints.
	if !c.isClientAuthenticated(ctx) {
		for i := range results {
			results[i].Source = ""
		}
	}

	// Build and return response
	resp := c.buildSearchResponse(&req, results, total, filters.PerPage)
	c.LogInfoIfEnabled("Search completed successfully",
		logger.Int("total_results", resp.Total),
		logger.Int("results_returned", len(resp.Results)),
		logger.Int("total_pages", resp.Pages),
		logger.Int("current_page", resp.CurrentPage),
		logger.String("path", path),
		logger.String("ip", ip),
	)

	return ctx.JSON(http.StatusOK, resp)
}

// logValidatedRequest logs the validated parameters for debugging.
func (c *Controller) logValidatedRequest(path, ip string, req *SearchRequest) {
	c.Debug("Validated Search request: Species='%s', DateStart='%s', DateEnd='%s', ConfidenceMin=%f, ConfidenceMax=%f, VerifiedStatus='%s', LockedStatus='%s', TimeOfDay='%s', Page=%d, SortBy='%s'",
		req.Species, req.DateStart, req.DateEnd, req.ConfidenceMin, req.ConfidenceMax, req.VerifiedStatus,
		req.LockedStatus, req.TimeOfDay, req.Page, req.SortBy)
	c.LogDebugIfEnabled("Validated search request parameters",
		logger.String("species", req.Species),
		logger.String("dateStart", req.DateStart),
		logger.String("dateEnd", req.DateEnd),
		logger.Float64("confidenceMin", req.ConfidenceMin),
		logger.Float64("confidenceMax", req.ConfidenceMax),
		logger.String("verifiedStatus", req.VerifiedStatus),
		logger.String("lockedStatus", req.LockedStatus),
		logger.String("deviceFilter", req.DeviceFilter),
		logger.String("timeOfDay", req.TimeOfDay),
		logger.Int("page", req.Page),
		logger.String("sortBy", req.SortBy),
		logger.String("path", path),
		logger.String("ip", ip),
	)
}

// buildSearchFilters creates the datastore search filters from the request.
func (c *Controller) buildSearchFilters(req *SearchRequest, ctxTimeout context.Context) datastore.SearchFilters {
	return datastore.SearchFilters{
		Species:           req.Species,
		SpeciesScientific: req.SpeciesScientific,
		DateStart:         req.DateStart,
		DateEnd:           req.DateEnd,
		ConfidenceMin:     req.ConfidenceMin,
		ConfidenceMax:     req.ConfidenceMax,
		VerifiedOnly:      req.VerifiedStatus == VerificationStatusCorrect,
		UnverifiedOnly:    req.VerifiedStatus == VerificationStatusUnverified,
		FalsePositiveOnly: req.VerifiedStatus == VerificationStatusFalsePositive,
		LockedOnly:        req.LockedStatus == "locked",
		UnlockedOnly:      req.LockedStatus == "unlocked",
		Device:            req.DeviceFilter,
		TimeOfDay:         req.TimeOfDay,
		Page:              req.Page,
		PerPage:           defaultPerPage,
		SortBy:            req.SortBy,
		Ctx:               ctxTimeout,
	}
}

// sanitizeSpeciesScientific trims, drops empties, de-duplicates, and caps the
// client-supplied scientific name list. The search endpoint is public and each
// name triggers a label lookup, so the cap bounds the work an anonymous caller
// can request. Insertion order of the kept entries is preserved.
func sanitizeSpeciesScientific(in []string) []string {
	if len(in) == 0 {
		return nil
	}
	// Both the seen-set and the output are bounded by the cap, so size them to it
	// rather than to the (untrusted, body-limited but possibly large) input length.
	seen := make(map[string]struct{}, min(len(in), maxSearchSpeciesScientific))
	out := make([]string, 0, min(len(in), maxSearchSpeciesScientific))
	for _, s := range in {
		name := strings.TrimSpace(s)
		if name == "" {
			continue
		}
		if _, dup := seen[name]; dup {
			continue
		}
		seen[name] = struct{}{}
		out = append(out, name)
		if len(out) >= maxSearchSpeciesScientific {
			break
		}
	}
	return out
}

// buildSearchResponse constructs the API response from search results.
func (c *Controller) buildSearchResponse(req *SearchRequest, results []datastore.DetectionRecord, total, perPage int) SearchResponse {
	totalPages := 1
	if total > 0 && perPage > 0 {
		totalPages = (total + perPage - 1) / perPage
	}

	currentPage := 1
	if req.Page > 0 {
		currentPage = min(req.Page, totalPages) // Clamp current page to valid range
	}

	return SearchResponse{
		Results:     results,
		Total:       total,
		Pages:       totalPages,
		CurrentPage: currentPage,
	}
}

// validateAndNormalizeSearchRequest checks the search request parameters for validity,
// applies default values, and normalizes ranges.
// It modifies the passed SearchRequest pointer directly.
// Returns an error suitable for user display if validation fails.
func (c *Controller) validateAndNormalizeSearchRequest(ctx echo.Context, req *SearchRequest) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path

	// Validate Page
	if req.Page < 1 {
		c.LogWarnIfEnabled("Invalid page number requested, defaulting to 1", logger.Int("requested_page", req.Page), logger.String("path", path), logger.String("ip", ip))
		req.Page = 1
	}

	var err error
	err = c.validateSearchDates(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchStatusEnums(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchTimeOfDay(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchConfidenceRange(path, ip, req)
	if err != nil {
		return err
	}

	err = c.validateSearchSortBy(path, ip, req)
	if err != nil {
		return err
	}

	return nil // All validations passed
}

// validateSearchDates validates the DateStart and DateEnd parameters.
func (c *Controller) validateSearchDates(path, ip string, req *SearchRequest) error {
	if err := validateDateFormat(req.DateStart, "start date"); err != nil {
		c.LogErrorIfEnabled("Invalid start date format", logger.String("dateStart", req.DateStart), logger.String("path", path), logger.String("ip", ip))
		return err
	}
	if err := validateDateFormat(req.DateEnd, "end date"); err != nil {
		c.LogErrorIfEnabled("Invalid end date format", logger.String("dateEnd", req.DateEnd), logger.String("path", path), logger.String("ip", ip))
		return err
	}
	if err := validateDateOrder(req.DateStart, req.DateEnd); err != nil {
		c.LogErrorIfEnabled("Invalid date range", logger.String("dateStart", req.DateStart), logger.String("dateEnd", req.DateEnd), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("'dateStart' (%s) must be earlier than or equal to 'dateEnd' (%s)", req.DateStart, req.DateEnd)
	}
	return nil
}

// validateSearchStatusEnums validates VerifiedStatus and LockedStatus.
func (c *Controller) validateSearchStatusEnums(path, ip string, req *SearchRequest) error {
	validVerifiedStatus := map[string]bool{
		QueryValueAny:                   true,
		VerificationStatusCorrect:       true,
		VerificationStatusUnverified:    true,
		VerificationStatusFalsePositive: true,
	}
	if req.VerifiedStatus == "" {
		req.VerifiedStatus = QueryValueAny
	} else if !validVerifiedStatus[req.VerifiedStatus] {
		c.LogErrorIfEnabled("Invalid verified status parameter", logger.String("verifiedStatus", req.VerifiedStatus), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("invalid verified status %q. Use %q, %q, %q, or %q",
			req.VerifiedStatus,
			QueryValueAny,
			VerificationStatusCorrect,
			VerificationStatusUnverified,
			VerificationStatusFalsePositive)
	}

	validLockedStatus := map[string]bool{QueryValueAny: true, "locked": true, "unlocked": true}
	if req.LockedStatus == "" {
		req.LockedStatus = QueryValueAny
	} else if !validLockedStatus[req.LockedStatus] {
		c.LogErrorIfEnabled("Invalid locked status parameter", logger.String("lockedStatus", req.LockedStatus), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("invalid locked status '%s'. Use 'any', 'locked', or 'unlocked'", req.LockedStatus)
	}
	return nil
}

// validateSearchTimeOfDay validates the TimeOfDay parameter.
func (c *Controller) validateSearchTimeOfDay(path, ip string, req *SearchRequest) error {
	validTimeOfDay := map[string]bool{QueryValueAny: true, "day": true, "night": true, "sunrise": true, "sunset": true}
	if req.TimeOfDay == "" {
		req.TimeOfDay = QueryValueAny
	} else if !validTimeOfDay[req.TimeOfDay] {
		c.LogErrorIfEnabled("Invalid time of day parameter", logger.String("timeOfDay", req.TimeOfDay), logger.String("path", path), logger.String("ip", ip))
		return fmt.Errorf("invalid time of day '%s'. Use 'any', 'day', 'night', 'sunrise', or 'sunset'", req.TimeOfDay)
	}
	return nil
}

// validateSearchConfidenceRange validates and normalizes ConfidenceMin and ConfidenceMax.
func (c *Controller) validateSearchConfidenceRange(path, ip string, req *SearchRequest) error {
	// Clamp confidenceMin to [0, 1]
	if req.ConfidenceMin < 0 {
		c.logConfidenceAdjustment("confidenceMin", req.ConfidenceMin, 0, path, ip)
		req.ConfidenceMin = 0
	}

	// Clamp confidenceMax and handle defaults
	req.ConfidenceMax = c.normalizeConfidenceMax(req.ConfidenceMin, req.ConfidenceMax, path, ip)

	// Ensure min ≤ max
	if req.ConfidenceMin > req.ConfidenceMax {
		c.logConfidenceSwap(req.ConfidenceMin, req.ConfidenceMax, path, ip)
		req.ConfidenceMin, req.ConfidenceMax = req.ConfidenceMax, req.ConfidenceMin
	}
	return nil
}

// normalizeConfidenceMax clamps and normalizes the confidence max value
func (c *Controller) normalizeConfidenceMax(minConf, maxConf float64, path, ip string) float64 {
	switch {
	case maxConf > 1:
		c.logConfidenceAdjustment("confidenceMax", maxConf, 1, path, ip)
		return 1
	case maxConf < 0:
		c.logConfidenceAdjustment("confidenceMax", maxConf, 0, path, ip)
		return 0
	case maxConf == 0 && minConf == 0:
		c.LogDebugIfEnabled("Confidence range is [0, 0], defaulting ConfidenceMax to 1", logger.String("path", path), logger.String("ip", ip))
		return 1
	default:
		return maxConf
	}
}

// logConfidenceAdjustment logs when a confidence value is adjusted
func (c *Controller) logConfidenceAdjustment(field string, original, adjusted float64, path, ip string) {
	c.LogWarnIfEnabled("Invalid "+field+", adjusted", logger.Float64("original", original), logger.Float64("adjusted", adjusted), logger.String("path", path), logger.String("ip", ip))
}

// logConfidenceSwap logs when min/max values are swapped
func (c *Controller) logConfidenceSwap(minConf, maxConf float64, path, ip string) {
	c.LogWarnIfEnabled("ConfidenceMin > ConfidenceMax after normalization, swapping values",
		logger.Float64("normalizedConfidenceMin", minConf), logger.Float64("normalizedConfidenceMax", maxConf), logger.String("path", path), logger.String("ip", ip))
}

// validateSearchSortBy validates the SortBy parameter.
func (c *Controller) validateSearchSortBy(path, ip string, req *SearchRequest) error {
	allowedSortBy := map[string]struct{}{ // Use struct{} for memory efficiency
		"date_desc":       {},
		"date_asc":        {},
		"species_asc":     {},
		"species_desc":    {},
		"confidence_asc":  {},
		"confidence_desc": {},
		"status":          {},
	}
	if req.SortBy != "" { // Allow empty string for default sorting (handled by datastore)
		if _, ok := allowedSortBy[req.SortBy]; !ok {
			c.LogErrorIfEnabled("Invalid sortBy parameter", logger.String("sortBy", req.SortBy), logger.String("path", path), logger.String("ip", ip))
			// Create a list of allowed sort options for the error message
			allowedKeys := slices.Collect(maps.Keys(allowedSortBy))
			return fmt.Errorf("invalid sortBy parameter '%s'. Allowed values: %v", req.SortBy, allowedKeys)
		}
	}
	return nil
}

// resolveSpeciesToScientific maps a free-form species search term to a scientific
// name when the input is an exact case-insensitive match for a common name in the
// currently loaded BirdNET label list. Non-matching input (partial common-name
// fragments, Latin substrings, unknown text, ambiguous common names shared by
// multiple species) is returned trimmed but otherwise unchanged so the existing
// LIKE search on scientific_name keeps working.
//
// The BirdNET label list is already cached in memory via UpdateCommonNameMap; there
// is no I/O here. The lookup is O(1) for the common-name exact-match case.
//
// Returns the resolved (or trimmed passthrough) value and a hit flag. hit is true
// only when a common-name lookup succeeded, so callers can log resolution events
// without false positives from whitespace-only or unmatched input.
//
// This is an interim fix; the proper long-term fix is a persistent species_common_names
// table that decouples common-name storage from the active model's label file.
func (c *Controller) resolveSpeciesToScientific(input string) (resolved string, hit bool) {
	trimmed := strings.TrimSpace(input)
	if trimmed == "" {
		return "", false
	}
	lookup := c.loadCommonToScientificMap()
	if scientific, ok := lookup[normalizeForLookup(trimmed)]; ok {
		return scientific, true
	}
	return trimmed, false
}
