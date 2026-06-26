// internal/api/v2/species.go
package api

import (
	"context"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	rangeapi "github.com/tphakala/birdnet-go/internal/api/v2/range"
	"github.com/tphakala/birdnet-go/internal/classifier"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
)

// RarityStatus represents the rarity classification of a species
type RarityStatus string

const (
	RarityVeryCommon RarityStatus = "very_common"
	RarityCommon     RarityStatus = "common"
	RarityUncommon   RarityStatus = "uncommon"
	RarityRare       RarityStatus = "rare"
	RarityVeryRare   RarityStatus = "very_rare"
	RarityUnknown    RarityStatus = "unknown"
)

// Rarity threshold constants for score-based classification
const (
	RarityThresholdVeryCommon = 0.8
	RarityThresholdCommon     = 0.5
	RarityThresholdUncommon   = 0.2
	RarityThresholdRare       = 0.05
)

// SpeciesInfo represents extended information about a bird species
type SpeciesInfo struct {
	ScientificName string              `json:"scientific_name"`
	CommonName     string              `json:"common_name"`
	Rarity         *SpeciesRarityInfo  `json:"rarity,omitempty"`
	Taxonomy       *ebird.TaxonomyTree `json:"taxonomy,omitempty"`
	Metadata       map[string]any      `json:"metadata,omitempty"`
}

// SpeciesRarityInfo contains rarity information for a species
type SpeciesRarityInfo struct {
	Status           RarityStatus `json:"status"`
	Score            float64      `json:"score"`
	LocationBased    bool         `json:"location_based"`
	Latitude         float64      `json:"latitude,omitempty"`
	Longitude        float64      `json:"longitude,omitempty"`
	Date             string       `json:"date"`
	ThresholdApplied float64      `json:"threshold_applied"`
}

// taxonomyLookupResult holds the result of a taxonomy lookup with source info.
type taxonomyLookupResult struct {
	tree   *ebird.TaxonomyTree
	source string
}

// lookupTaxonomyTree attempts to find taxonomy for a species, trying local DB first then eBird.
// Returns nil result (not error) if taxonomy is unavailable from both sources.
func (c *Controller) lookupTaxonomyTree(ctx context.Context, scientificName string) *taxonomyLookupResult {
	// Try local taxonomy database first (fast, no network)
	if c.TaxonomyDB != nil {
		tree, err := c.TaxonomyDB.BuildFamilyTree(scientificName)
		if err == nil {
			c.Debug("Retrieved taxonomy for %s from local database", scientificName)
			return &taxonomyLookupResult{tree: tree, source: "local"}
		}
		c.Debug("Local taxonomy lookup failed for %s: %v, falling back to eBird API", scientificName, err)
	}

	// Fall back to eBird API
	if c.EBirdClient != nil {
		tree, err := c.EBirdClient.BuildFamilyTree(ctx, scientificName)
		if err != nil {
			c.Debug("Failed to get taxonomy info from eBird for species %s: %v", scientificName, err)
			return nil
		}
		return &taxonomyLookupResult{tree: tree, source: "ebird"}
	}

	return nil
}

// initSpeciesRoutes registers all species-related API endpoints
func (c *Controller) initSpeciesRoutes() {
	// Public endpoints for species information
	c.Group.GET("/species", c.GetSpeciesInfo)
	c.Group.GET("/species/all", c.GetAllSpecies)
	c.Group.GET("/species/taxonomy", c.GetSpeciesTaxonomy)
	c.Group.GET("/species/dictionary/:locale", c.ServeSpeciesDictionary)

	// RESTful thumbnail endpoint - uses species code from path
	c.Group.GET("/species/:code/thumbnail", c.GetSpeciesThumbnail)

	// New taxonomy endpoints using local database
	c.Group.GET("/taxonomy/genus/:genus", c.GetGenusSpecies)
	c.Group.GET("/taxonomy/family/:family", c.GetFamilySpecies)
	c.Group.GET("/taxonomy/tree/:scientific_name", c.GetSpeciesTree)
}

// AllSpeciesResponse represents the response for the all species endpoint
type AllSpeciesResponse struct {
	Species []rangeapi.RangeFilterSpecies `json:"species"`
	Count   int                           `json:"count"`
}

// GetAllSpecies returns all known BirdNET species labels regardless of location or range filter.
// This is used for species include/exclude search where users need to find any species,
// not just those matching the current location's range filter.
// @Summary Get all BirdNET species
// @Description Returns all species from the loaded BirdNET labels, independent of range filter
// @Tags species
// @Produce json
// @Success 200 {object} AllSpeciesResponse
// @Failure 500 {object} ErrorResponse
// @Router /api/v2/species/all [get]
func (c *Controller) GetAllSpecies(ctx echo.Context) error {
	ip := ctx.RealIP()
	path := ctx.Request().URL.Path
	c.LogDebugIfEnabled("Retrieving all BirdNET species labels",
		logger.String("ip", ip),
		logger.String("path", path),
	)

	speciesList := buildAllSpeciesList(c.loadCommonNameMap(), c.allModelLabels())

	c.LogInfoIfEnabled("All species labels retrieved successfully",
		logger.Int("count", len(speciesList)),
		logger.String("ip", ip),
		logger.String("path", path),
	)

	return ctx.JSON(http.StatusOK, AllSpeciesResponse{
		Species: speciesList,
		Count:   len(speciesList),
	})
}

// buildAllSpeciesList builds the /api/v2/species/all payload for the species
// include/exclude picker. When the cached, resolver-localized scientific-to-common
// map (sciToCommon) is populated, the list is built from it: control_monitor seeds
// that map from the orchestrator's multi-model AllLabels union at startup and on
// hot-reload, so it already covers every loaded model's species (including
// secondary-model bats/Perch), localized to the configured BirdNET.Locale and
// deduplicated (scientific keys are unique). Output is sorted by scientific name for
// a deterministic response.
//
// When sciToCommon is empty (the brief startup window before control_monitor seeds
// the maps), it falls back to parsing fallbackLabels. The caller passes the
// orchestrator's AllLabels union there (not just the primary BirdNET labels), so the
// picker still includes secondary-model species during that window; the fallback
// preserves input order and the original label string.
func buildAllSpeciesList(sciToCommon map[string]string, fallbackLabels []string) []rangeapi.RangeFilterSpecies {
	if len(sciToCommon) > 0 {
		speciesList := make([]rangeapi.RangeFilterSpecies, 0, len(sciToCommon))
		for sci, common := range sciToCommon {
			speciesList = append(speciesList, rangeapi.RangeFilterSpecies{
				Label:          sci + "_" + common,
				ScientificName: sci,
				CommonName:     common,
			})
		}
		sort.Slice(speciesList, func(i, j int) bool {
			return speciesList[i].ScientificName < speciesList[j].ScientificName
		})
		return speciesList
	}

	speciesList := make([]rangeapi.RangeFilterSpecies, 0, len(fallbackLabels))
	seen := make(map[string]struct{}, len(fallbackLabels))
	for _, label := range fallbackLabels {
		sp := detection.ParseSpeciesString(label)
		// AllLabels unions models that may emit the same species in different label
		// forms ("Scientific_Common" vs scientific-only), so dedup by scientific name
		// to avoid duplicate rows; input order and the original label are preserved.
		key := strings.ToLower(sp.ScientificName)
		if _, dup := seen[key]; dup {
			continue
		}
		seen[key] = struct{}{}
		speciesList = append(speciesList, rangeapi.RangeFilterSpecies{
			Label:          label,
			ScientificName: sp.ScientificName,
			CommonName:     sp.CommonName,
		})
	}
	return speciesList
}

// allModelLabels returns the union of every loaded model's labels (primary plus
// secondary models such as the bat and Perch classifiers) from the orchestrator,
// falling back to the primary BirdNET labels from settings when the orchestrator is
// not yet available (e.g. early startup before the audio pipeline builds it).
func (c *Controller) allModelLabels() []string {
	if proc := c.Processor; proc != nil {
		if bn := proc.GetBirdNET(); bn != nil {
			if labels := bn.AllLabels(); len(labels) > 0 {
				return labels
			}
		}
	}
	if s := c.ControllerSettings(); s != nil {
		return s.BirdNET.Labels
	}
	return nil
}

// GetSpeciesInfo retrieves extended information about a bird species
func (c *Controller) GetSpeciesInfo(ctx echo.Context) error {
	// Get scientific name from query parameter
	scientificName := ctx.QueryParam("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Validate the scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)
	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Get species info
	speciesInfo, err := c.getSpeciesInfo(ctx.Request().Context(), scientificName)
	if err != nil {
		return c.handleErrorWithNotFound(ctx, err, "Species not found", "Failed to get species information")
	}

	return ctx.JSON(http.StatusOK, speciesInfo)
}

// getSpeciesInfo retrieves species information including rarity status
func (c *Controller) getSpeciesInfo(ctx context.Context, scientificName string) (*SpeciesInfo, error) {
	// Snapshot to avoid TOCTOU race on c.Processor
	proc := c.Processor
	if proc == nil || proc.Bn == nil {
		return nil, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build()
	}

	bn := proc.Bn

	// Find the full label for this species from BirdNET labels
	var matchedLabel string
	var commonName string

	// Search the full multi-model label union (primary plus secondary models such
	// as the bat/Perch classifiers) so a secondary-model scientific name resolves
	// instead of 404ing.
	for _, label := range bn.AllLabels() {
		sp := detection.ParseSpeciesString(label)
		if strings.EqualFold(sp.ScientificName, scientificName) {
			matchedLabel = label
			commonName = sp.CommonName
			break
		}
	}

	// Secondary-model labels (bats, Perch) are scientific-only, so ParseSpeciesString
	// reports CommonName == ScientificName for them. Treat that (and an empty common
	// name) as "needs localizing" and resolve through the orchestrator's
	// OpenFauna-authoritative resolver, passing the configured locale explicitly.
	if matchedLabel != "" && (commonName == "" || strings.EqualFold(commonName, scientificName)) {
		if resolved := bn.ResolveName(scientificName, c.CurrentLocale()); resolved != "" {
			commonName = resolved
		}
	}

	// If species not found in any loaded model's labels, return error
	if matchedLabel == "" {
		return nil, errors.Newf("species '%s' not found in loaded model labels", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// Create basic species info
	info := &SpeciesInfo{
		ScientificName: scientificName,
		CommonName:     commonName,
		Metadata:       make(map[string]any),
	}

	// Get rarity information
	rarityInfo, err := c.getSpeciesRarityInfo(bn, matchedLabel)
	if err != nil {
		// Log error but don't fail the request
		c.Debug("Failed to get rarity info for species %s: %v", scientificName, err)
		// Continue without rarity info
	} else {
		info.Rarity = rarityInfo
	}

	// Get taxonomy/family tree information using fallback pattern
	if result := c.lookupTaxonomyTree(ctx, scientificName); result != nil {
		info.Taxonomy = result.tree
		info.Metadata["source"] = result.source
	}

	return info, nil
}

// getSpeciesRarityInfo calculates the rarity status for a species
// speciesHasGeomodelCoverage reports whether the scientific name is in the primary
// model's label set, i.e. the geomodel's classifiable vocabulary. Secondary-model-only
// species (e.g. bats) are absent from it and therefore have no geomodel occurrence
// probability to base a rarity on.
func speciesHasGeomodelCoverage(bn *classifier.Orchestrator, scientificName string) bool {
	for _, label := range bn.Labels() {
		if strings.EqualFold(detection.ExtractScientificName(label), scientificName) {
			return true
		}
	}
	return false
}

func (c *Controller) getSpeciesRarityInfo(bn *classifier.Orchestrator, speciesLabel string) (*SpeciesRarityInfo, error) {
	// Get current date
	today := time.Now().Truncate(HoursPerDay * time.Hour)
	settings := bn.CurrentSettings()

	// Rarity is the geomodel occurrence probability, so use the geomodel-backed
	// probable-species list, not the multi-model union: the union assigns synthetic
	// always-active scores (1.0) to secondary-model species (bats, Perch) that have
	// no real occurrence probability, which would misclassify them as "very common".
	speciesScores, err := bn.GetProbableSpecies(today, 0.0)
	if err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryProcessing).
			Context("species_label", speciesLabel).
			Component("api-species").
			Build()
	}

	// Create rarity info
	rarityInfo := &SpeciesRarityInfo{
		Date:             today.Format(time.DateOnly),
		LocationBased:    settings.BirdNET.LocationConfigured,
		ThresholdApplied: float64(settings.BirdNET.RangeFilter.Threshold),
	}

	// Add location if available
	if rarityInfo.LocationBased {
		rarityInfo.Latitude = settings.BirdNET.Latitude
		rarityInfo.Longitude = settings.BirdNET.Longitude
	}

	// Find the species score
	var score float64
	found := false
	targetSci := detection.ExtractScientificName(speciesLabel)
	for _, ss := range speciesScores {
		if strings.EqualFold(detection.ExtractScientificName(ss.Label), targetSci) {
			score = ss.Score
			found = true
			break
		}
	}

	// Not in today's probable list. A species the geomodel can classify but that is
	// below threshold today is genuinely very rare; a species with no geomodel
	// coverage at all (secondary-model-only species such as bats) has no occurrence
	// probability, so report it as unknown rather than a misleading rarity.
	if !found {
		if speciesHasGeomodelCoverage(bn, targetSci) {
			rarityInfo.Status = RarityVeryRare
		} else {
			rarityInfo.Status = RarityUnknown
		}
		rarityInfo.Score = 0.0
		return rarityInfo, nil
	}

	// Set score and calculate rarity status
	rarityInfo.Score = score
	rarityInfo.Status = calculateRarityStatus(score)

	return rarityInfo, nil
}

// calculateRarityStatus determines the rarity status based on the probability score
func calculateRarityStatus(score float64) RarityStatus {
	switch {
	case score > RarityThresholdVeryCommon:
		return RarityVeryCommon
	case score > RarityThresholdCommon:
		return RarityCommon
	case score > RarityThresholdUncommon:
		return RarityUncommon
	case score > RarityThresholdRare:
		return RarityRare
	default:
		return RarityVeryRare
	}
}

// TaxonomyInfo represents detailed taxonomy information for a species
type TaxonomyInfo struct {
	ScientificName     string             `json:"scientific_name"`
	SpeciesCode        string             `json:"species_code,omitempty"`
	Taxonomy           *TaxonomyHierarchy `json:"taxonomy,omitempty"`
	Subspecies         []SubspeciesInfo   `json:"subspecies,omitempty"`
	Synonyms           []string           `json:"synonyms,omitempty"`
	ConservationStatus string             `json:"conservation_status,omitempty"`
	NativeRegions      []string           `json:"native_regions,omitempty"`
	Metadata           map[string]any     `json:"metadata,omitempty"`
}

// TaxonomyHierarchy represents the full taxonomic classification
type TaxonomyHierarchy struct {
	Kingdom       string `json:"kingdom"`
	Phylum        string `json:"phylum"`
	Class         string `json:"class"`
	Order         string `json:"order"`
	Family        string `json:"family"`
	FamilyCommon  string `json:"family_common,omitempty"`
	Genus         string `json:"genus"`
	Species       string `json:"species"`
	SpeciesCommon string `json:"species_common,omitempty"`
}

// SubspeciesInfo represents information about a subspecies
type SubspeciesInfo struct {
	ScientificName string `json:"scientific_name"`
	CommonName     string `json:"common_name,omitempty"`
	Region         string `json:"region,omitempty"`
}

// GetSpeciesTaxonomy retrieves detailed taxonomy information for a species
func (c *Controller) GetSpeciesTaxonomy(ctx echo.Context) error {
	// Get parameters from query
	scientificName := ctx.QueryParam("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing required parameter", http.StatusBadRequest)
	}

	// Validate the scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)
	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Get optional parameters
	locale := ctx.QueryParam("locale")
	includeSubspecies := ctx.QueryParam("include_subspecies") != "false" // default true
	includeHierarchy := ctx.QueryParam("include_hierarchy") != "false"   // default true

	// Get taxonomy info
	taxonomyInfo, err := c.getDetailedTaxonomy(ctx.Request().Context(), scientificName, locale, includeSubspecies, includeHierarchy)
	if err != nil {
		return c.handleErrorWithNotFound(ctx, err, "Species not found", "Failed to get taxonomy information")
	}

	return ctx.JSON(http.StatusOK, taxonomyInfo)
}

// getDetailedTaxonomy retrieves detailed taxonomy information
// Tries local database first, falls back to eBird API if needed
func (c *Controller) getDetailedTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) (*TaxonomyInfo, error) {
	// Try local taxonomy database first
	if info := c.tryLocalTaxonomy(ctx, scientificName, locale, includeSubspecies, includeHierarchy); info != nil {
		return info, nil
	}

	// Fall back to eBird API
	if c.EBirdClient != nil {
		return c.getEBirdTaxonomy(ctx, scientificName, locale, includeSubspecies)
	}

	// Neither local DB nor eBird API available
	return nil, errors.Newf("taxonomy data not available (no local database or eBird API)").
		Category(errors.CategoryConfiguration).
		Priority(errors.PriorityLow).
		Context("scientific_name", scientificName).
		Component("api-species").
		Build()
}

// tryLocalTaxonomy attempts to retrieve taxonomy from the local database.
// Returns nil if local DB is unavailable or lookup fails.
func (c *Controller) tryLocalTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) *TaxonomyInfo {
	if c.TaxonomyDB == nil {
		return nil
	}

	taxonomyTree, err := c.TaxonomyDB.BuildFamilyTree(scientificName)
	if err != nil {
		c.Debug("Local taxonomy lookup failed for %s: %v, falling back to eBird API", scientificName, err)
		return nil
	}

	info := &TaxonomyInfo{
		ScientificName: scientificName,
		Metadata: map[string]any{
			"source":     "local",
			"updated_at": c.TaxonomyDB.UpdatedAt,
		},
	}

	// Add hierarchy if requested
	if includeHierarchy && taxonomyTree != nil {
		info.Taxonomy = convertToTaxonomyHierarchy(taxonomyTree)
	}

	// Enhance with eBird data if needed
	c.enhanceWithEBirdData(ctx, info, scientificName, locale, includeSubspecies)

	return info
}

// convertToTaxonomyHierarchy converts an ebird.TaxonomyTree to TaxonomyHierarchy.
func convertToTaxonomyHierarchy(tree *ebird.TaxonomyTree) *TaxonomyHierarchy {
	return &TaxonomyHierarchy{
		Kingdom:       tree.Kingdom,
		Phylum:        tree.Phylum,
		Class:         tree.Class,
		Order:         tree.Order,
		Family:        tree.Family,
		FamilyCommon:  tree.FamilyCommon,
		Genus:         tree.Genus,
		Species:       tree.Species,
		SpeciesCommon: tree.SpeciesCommon,
	}
}

// enhanceWithEBirdData adds subspecies and locale data from eBird API to local taxonomy info.
func (c *Controller) enhanceWithEBirdData(ctx context.Context, info *TaxonomyInfo, scientificName, locale string, includeSubspecies bool) {
	if c.EBirdClient == nil || (!includeSubspecies && locale == "") {
		return
	}

	c.Debug("Enhancing local taxonomy data with eBird API for subspecies/locale")
	ebirdInfo, err := c.getEBirdTaxonomy(ctx, scientificName, locale, includeSubspecies)
	if err != nil {
		return
	}

	if includeSubspecies && len(ebirdInfo.Subspecies) > 0 {
		info.Subspecies = ebirdInfo.Subspecies
	}
	if ebirdInfo.SpeciesCode != "" {
		info.SpeciesCode = ebirdInfo.SpeciesCode
	}
	info.Metadata["source"] = "local+ebird"
	if locale != "" {
		info.Metadata["locale"] = locale
	}
}

// getEBirdTaxonomy retrieves taxonomy information from eBird API
func (c *Controller) getEBirdTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies bool) (*TaxonomyInfo, error) {
	// Get full taxonomy data with locale if specified
	taxonomyData, err := c.EBirdClient.GetTaxonomy(ctx, locale)
	if err != nil {
		return nil, err
	}

	// Find the species in taxonomy
	var speciesEntry *ebird.TaxonomyEntry
	for i := range taxonomyData {
		if strings.EqualFold(taxonomyData[i].ScientificName, scientificName) {
			speciesEntry = &taxonomyData[i]
			break
		}
	}

	if speciesEntry == nil {
		return nil, errors.Newf("species '%s' not found in eBird taxonomy", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// Create taxonomy info
	info := &TaxonomyInfo{
		ScientificName: speciesEntry.ScientificName,
		SpeciesCode:    speciesEntry.SpeciesCode,
		Metadata: map[string]any{
			"source":     "ebird",
			"updated_at": time.Now().Format(time.RFC3339),
			"locale":     locale,
		},
	}

	// Parse genus from scientific name
	parts := strings.Fields(speciesEntry.ScientificName)
	genus := ""
	if len(parts) > 0 {
		genus = parts[0]
	}

	info.Taxonomy = &TaxonomyHierarchy{
		Kingdom:       "Animalia", // All birds are in kingdom Animalia
		Phylum:        "Chordata", // All birds are in phylum Chordata
		Class:         "Aves",     // All entries are birds
		Order:         speciesEntry.Order,
		Family:        speciesEntry.FamilySciName,
		FamilyCommon:  speciesEntry.FamilyComName,
		Genus:         genus,
		Species:       speciesEntry.ScientificName,
		SpeciesCommon: speciesEntry.CommonName,
	}

	// Add subspecies if requested and it's a species entry
	if includeSubspecies && speciesEntry.Category == "species" {
		subspecies := c.findDetailedSubspecies(taxonomyData, speciesEntry.SpeciesCode)
		info.Subspecies = subspecies
	}

	// TODO: Add conservation status and native regions when available from eBird API

	return info, nil
}

// findDetailedSubspecies finds all subspecies with detailed information
func (c *Controller) findDetailedSubspecies(taxonomy []ebird.TaxonomyEntry, speciesCode string) []SubspeciesInfo {
	var subspecies []SubspeciesInfo

	for i := range taxonomy {
		// Check if this entry reports as our species and is a subspecies category
		if taxonomy[i].ReportAs == speciesCode &&
			(taxonomy[i].Category == "issf" || taxonomy[i].Category == "form") {

			// Extract region from common name if present (often in parentheses)
			region := ""
			commonName := taxonomy[i].CommonName
			if start := strings.Index(commonName, "("); start != -1 {
				if end := strings.Index(commonName[start:], ")"); end != -1 {
					region = strings.TrimSpace(commonName[start+1 : start+end])
				}
			}

			subspecies = append(subspecies, SubspeciesInfo{
				ScientificName: taxonomy[i].ScientificName,
				CommonName:     taxonomy[i].CommonName,
				Region:         region,
			})
		}
	}

	return subspecies
}

// GetSpeciesThumbnail retrieves a bird thumbnail image by species code
// GET /api/v2/species/:code/thumbnail
func (c *Controller) GetSpeciesThumbnail(ctx echo.Context) error {
	speciesCode := ctx.Param("code")
	if speciesCode == "" {
		return c.HandleError(ctx, errors.Newf("species code parameter is required").
			Category(errors.CategoryValidation).
			Component("api-species").
			Build(), "Missing species code", http.StatusBadRequest)
	}

	// Log the request if API logger is available
	c.LogDebugIfEnabled("Retrieving thumbnail for species code",
		logger.String("species_code", speciesCode),
		logger.String("ip", ctx.RealIP()),
		logger.String("path", ctx.Request().URL.Path),
	)

	// Snapshot to avoid TOCTOU race on c.Processor
	proc := c.Processor
	if proc == nil || proc.Bn == nil {
		return c.HandleError(ctx, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "BirdNET service unavailable", http.StatusServiceUnavailable)
	}

	cache := c.BirdImageCache
	if cache == nil {
		return c.HandleError(ctx, errors.Newf("image service unavailable").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "Image service unavailable", http.StatusServiceUnavailable)
	}

	// Get species name from the taxonomy map using the species code
	bn := proc.Bn
	speciesName, exists := bn.GetSpeciesNameFromCode(speciesCode)

	if !exists {
		return c.HandleError(ctx, errors.Newf("species code '%s' not found in taxonomy", speciesCode).
			Category(errors.CategoryNotFound).
			Context("species_code", speciesCode).
			Component("api-species").
			Build(), "Species not found", http.StatusNotFound)
	}

	// Split the species name to get scientific name
	scientificName, _ := classifier.SplitSpeciesName(speciesName)

	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("invalid species name format for code '%s'", speciesCode).
			Category(errors.CategoryValidation).
			Context("species_code", speciesCode).
			Context("species_name", speciesName).
			Component("api-species").
			Build(), "Invalid species data", http.StatusInternalServerError)
	}

	// Delegate to the image proxy handler
	ctx.SetParamNames("scientific_name")
	ctx.SetParamValues(scientificName)
	return c.ServeSpeciesImageProxy(ctx)
}
