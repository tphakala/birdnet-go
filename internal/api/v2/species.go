// internal/api/v2/species.go
package api

import (
	"context"
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observation"
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
	ScientificName string                 `json:"scientific_name"`
	CommonName     string                 `json:"common_name"`
	Rarity         *SpeciesRarityInfo     `json:"rarity,omitempty"`
	Taxonomy       *ebird.TaxonomyTree    `json:"taxonomy,omitempty"`
	Metadata       map[string]interface{} `json:"metadata,omitempty"`
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

// initSpeciesRoutes registers all species-related API endpoints
func (c *Controller) initSpeciesRoutes() {
	// Public endpoints for species information
	c.Group.GET("/species", c.GetSpeciesInfo)
	c.Group.GET("/species/taxonomy", c.GetSpeciesTaxonomy)
	
	// RESTful thumbnail endpoint - uses species code from path
	c.Group.GET("/species/:code/thumbnail", c.GetSpeciesThumbnail)
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
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) && enhancedErr.Category == errors.CategoryNotFound {
			return c.HandleError(ctx, err, "Species not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to get species information", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, speciesInfo)
}

// getSpeciesInfo retrieves species information including rarity status
func (c *Controller) getSpeciesInfo(ctx context.Context, scientificName string) (*SpeciesInfo, error) {
	// Get the BirdNET instance from the processor
	if c.Processor == nil || c.Processor.Bn == nil {
		return nil, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build()
	}

	bn := c.Processor.Bn

	// Find the full label for this species from BirdNET labels
	var matchedLabel string
	var commonName string

	for _, label := range bn.Settings.BirdNET.Labels {
		labelSci, labelCommon, _ := observation.ParseSpeciesString(label)
		if strings.EqualFold(labelSci, scientificName) {
			matchedLabel = label
			commonName = labelCommon
			break
		}
	}

	// If species not found in labels, return error
	if matchedLabel == "" {
		return nil, errors.Newf("species '%s' not found in BirdNET labels", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build()
	}

	// Create basic species info
	info := &SpeciesInfo{
		ScientificName: scientificName,
		CommonName:     commonName,
		Metadata:       make(map[string]interface{}),
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

	// Get taxonomy/family tree information from eBird if available
	if c.EBirdClient != nil {
		taxonomyTree, err := c.EBirdClient.BuildFamilyTree(ctx, scientificName)
		if err != nil {
			// The eBird client already creates enhanced errors with proper
			// categorization. These errors will be automatically published
			// to the event bus when Build() is called in the client.
			// We just log here for debugging purposes.
			c.Debug("Failed to get taxonomy info from eBird for species %s: %v", scientificName, err)
			// Continue without taxonomy info
		} else {
			info.Taxonomy = taxonomyTree
		}
	}

	return info, nil
}

// getSpeciesRarityInfo calculates the rarity status for a species
func (c *Controller) getSpeciesRarityInfo(bn *birdnet.BirdNET, speciesLabel string) (*SpeciesRarityInfo, error) {
	// Get current date
	today := time.Now().Truncate(24 * time.Hour)

	// Get probable species with scores
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
		Date:             today.Format("2006-01-02"),
		LocationBased:    bn.Settings.BirdNET.Latitude != 0 || bn.Settings.BirdNET.Longitude != 0,
		ThresholdApplied: float64(bn.Settings.BirdNET.RangeFilter.Threshold),
	}

	// Add location if available
	if rarityInfo.LocationBased {
		rarityInfo.Latitude = bn.Settings.BirdNET.Latitude
		rarityInfo.Longitude = bn.Settings.BirdNET.Longitude
	}

	// Find the species score
	var score float64
	found := false
	for _, ss := range speciesScores {
		if ss.Label == speciesLabel {
			score = ss.Score
			found = true
			break
		}
	}

	// If not found in probable species, it's very rare
	if !found {
		rarityInfo.Status = RarityVeryRare
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
	ScientificName     string                 `json:"scientific_name"`
	SpeciesCode        string                 `json:"species_code,omitempty"`
	Taxonomy           *TaxonomyHierarchy     `json:"taxonomy,omitempty"`
	Subspecies         []SubspeciesInfo       `json:"subspecies,omitempty"`
	Synonyms           []string               `json:"synonyms,omitempty"`
	ConservationStatus string                 `json:"conservation_status,omitempty"`
	NativeRegions      []string               `json:"native_regions,omitempty"`
	Metadata           map[string]interface{} `json:"metadata,omitempty"`
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
	includeHierarchy := ctx.QueryParam("include_hierarchy") != "false"  // default true

	// Get taxonomy info
	taxonomyInfo, err := c.getDetailedTaxonomy(ctx.Request().Context(), scientificName, locale, includeSubspecies, includeHierarchy)
	if err != nil {
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) && enhancedErr.Category == errors.CategoryNotFound {
			return c.HandleError(ctx, err, "Species not found", http.StatusNotFound)
		}
		return c.HandleError(ctx, err, "Failed to get taxonomy information", http.StatusInternalServerError)
	}

	return ctx.JSON(http.StatusOK, taxonomyInfo)
}

// getDetailedTaxonomy retrieves detailed taxonomy information from eBird
func (c *Controller) getDetailedTaxonomy(ctx context.Context, scientificName, locale string, includeSubspecies, includeHierarchy bool) (*TaxonomyInfo, error) {
	// Check if eBird client is available
	if c.EBirdClient == nil {
		return nil, errors.Newf("eBird integration not available").
			Category(errors.CategoryConfiguration).
			Component("api-species").
			Build()
	}

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
		Metadata: map[string]interface{}{
			"source":     "ebird",
			"updated_at": time.Now().Format(time.RFC3339),
			"locale":     locale,
		},
	}

	// Add hierarchy if requested
	if includeHierarchy {
		// Parse genus from scientific name
		parts := strings.Split(speciesEntry.ScientificName, " ")
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
	if c.apiLogger != nil {
		c.apiLogger.Debug("Retrieving thumbnail for species code",
			"species_code", speciesCode,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Check if BirdNET processor is available
	if c.Processor == nil || c.Processor.Bn == nil {
		return c.HandleError(ctx, errors.Newf("BirdNET processor not available").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "BirdNET service unavailable", http.StatusServiceUnavailable)
	}

	// Check if BirdImageCache is available
	if c.BirdImageCache == nil {
		return c.HandleError(ctx, errors.Newf("image service unavailable").
			Category(errors.CategorySystem).
			Component("api-species").
			Build(), "Image service unavailable", http.StatusServiceUnavailable)
	}

	// Get species name from the taxonomy map using the species code
	bn := c.Processor.Bn
	speciesName, exists := birdnet.GetSpeciesNameFromCode(bn.TaxonomyMap, speciesCode)
	
	if !exists {
		return c.HandleError(ctx, errors.Newf("species code '%s' not found in taxonomy", speciesCode).
			Category(errors.CategoryNotFound).
			Context("species_code", speciesCode).
			Component("api-species").
			Build(), "Species not found", http.StatusNotFound)
	}

	// Split the species name to get scientific name
	scientificName, _ := birdnet.SplitSpeciesName(speciesName)
	
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("invalid species name format for code '%s'", speciesCode).
			Category(errors.CategoryValidation).
			Context("species_code", speciesCode).
			Context("species_name", speciesName).
			Component("api-species").
			Build(), "Invalid species data", http.StatusInternalServerError)
	}

	// Now use the scientific name to get the bird image
	birdImage, err := c.BirdImageCache.Get(scientificName)
	if err != nil {
		// Check for "not found" errors - the cache returns a specific error message
		if strings.Contains(err.Error(), "not found") {
			return c.HandleError(ctx, errors.New(err).
				Context("species_code", speciesCode).
				Context("scientific_name", scientificName).
				Component("api-species").
				Build(), "Image not found for species", http.StatusNotFound)
		}
		// For other errors
		return c.HandleError(ctx, errors.New(err).
			Category(errors.CategoryProcessing).
			Context("species_code", speciesCode).
			Context("scientific_name", scientificName).
			Component("api-species").
			Build(), "Failed to fetch species image", http.StatusInternalServerError)
	}

	// Log successful retrieval
	if c.apiLogger != nil {
		c.apiLogger.Info("Successfully retrieved thumbnail",
			"species_code", speciesCode,
			"scientific_name", scientificName,
			"image_url", birdImage.URL,
			"ip", ctx.RealIP(),
			"path", ctx.Request().URL.Path,
		)
	}

	// Redirect to the image URL
	return ctx.Redirect(http.StatusFound, birdImage.URL)
}