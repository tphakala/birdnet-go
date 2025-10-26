// internal/api/v2/species_taxonomy.go
package api

import (
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// Cache headers for taxonomy endpoints (taxonomy data is static)
const (
	taxonomyCacheControl = "public, max-age=86400"
	taxonomyVary         = "Accept-Encoding"
)

// GenusSpeciesResponse represents the response for genus species lookup
type GenusSpeciesResponse struct {
	Genus        string   `json:"genus"`
	Family       string   `json:"family"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Species      []string `json:"species"`
	TotalCount   int      `json:"total_count"`
}

// FamilySpeciesResponse represents the response for family species lookup
type FamilySpeciesResponse struct {
	Family       string   `json:"family"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Genera       []string `json:"genera"`
	Species      []string `json:"species"`
	TotalCount   int      `json:"total_count"`
}

// titleFirst returns a string with its first rune converted to title case
// and the rest converted to lowercase. This is Unicode-safe and handles
// empty strings without panicking.
func titleFirst(s string) string {
	if s == "" {
		return s
	}
	r, size := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToTitle(r)) + strings.ToLower(s[size:])
}

// GetGenusSpecies retrieves all species in a given genus
// GET /api/v2/taxonomy/genus/:genus
func (c *Controller) GetGenusSpecies(ctx echo.Context) error {
	genus := ctx.Param("genus")
	if genus == "" {
		return c.HandleError(ctx, errors.Newf("genus parameter is required").
			Category(errors.CategoryValidation).
			Component("api-taxonomy").
			Build(), "Missing genus parameter", http.StatusBadRequest)
	}

	// Validate genus name format (basic validation)
	genus = strings.TrimSpace(genus)
	if len(genus) < 2 {
		return c.HandleError(ctx, errors.Newf("invalid genus name format").
			Category(errors.CategoryValidation).
			Context("genus", genus).
			Component("api-taxonomy").
			Build(), "Invalid genus name format", http.StatusBadRequest)
	}

	// Check if taxonomy database is available
	if c.TaxonomyDB == nil {
		return c.HandleError(ctx, errors.Newf("taxonomy database not available").
			Category(errors.CategorySystem).
			Component("api-taxonomy").
			Build(), "Taxonomy database not available", http.StatusServiceUnavailable)
	}

	// Get genus metadata
	genusMetadata, err := c.TaxonomyDB.GetGenusInfo(genus)
	if err != nil {
		return c.HandleError(ctx, err, "Genus not found", http.StatusNotFound)
	}

	response := GenusSpeciesResponse{
		Genus:        titleFirst(genus),
		Family:       genusMetadata.Family,
		FamilyCommon: genusMetadata.FamilyCommon,
		Order:        genusMetadata.Order,
		Species:      genusMetadata.Species,
		TotalCount:   len(genusMetadata.Species),
	}

	// Set cache headers (taxonomy data is static)
	ctx.Response().Header().Set("Cache-Control", taxonomyCacheControl)
	ctx.Response().Header().Set("Vary", taxonomyVary)

	return ctx.JSON(http.StatusOK, response)
}

// GetFamilySpecies retrieves all species in a given family
// GET /api/v2/taxonomy/family/:family
func (c *Controller) GetFamilySpecies(ctx echo.Context) error {
	family := ctx.Param("family")
	if family == "" {
		return c.HandleError(ctx, errors.Newf("family parameter is required").
			Category(errors.CategoryValidation).
			Component("api-taxonomy").
			Build(), "Missing family parameter", http.StatusBadRequest)
	}

	// Validate family name format (basic validation)
	family = strings.TrimSpace(family)
	if len(family) < 3 {
		return c.HandleError(ctx, errors.Newf("invalid family name format").
			Category(errors.CategoryValidation).
			Context("family", family).
			Component("api-taxonomy").
			Build(), "Invalid family name format", http.StatusBadRequest)
	}

	// Check if taxonomy database is available
	if c.TaxonomyDB == nil {
		return c.HandleError(ctx, errors.Newf("taxonomy database not available").
			Category(errors.CategorySystem).
			Component("api-taxonomy").
			Build(), "Taxonomy database not available", http.StatusServiceUnavailable)
	}

	// Get family information
	familyMetadata, err := c.TaxonomyDB.GetFamilyInfo(family)
	if err != nil {
		return c.HandleError(ctx, err, "Family not found", http.StatusNotFound)
	}

	// Get all species in family
	species, err := c.TaxonomyDB.GetAllSpeciesInFamily(family)
	if err != nil {
		return c.HandleError(ctx, err, "Failed to retrieve species list", http.StatusInternalServerError)
	}

	response := FamilySpeciesResponse{
		Family:       titleFirst(family),
		FamilyCommon: familyMetadata.FamilyCommon,
		Order:        familyMetadata.Order,
		Genera:       familyMetadata.Genera,
		Species:      species,
		TotalCount:   len(species),
	}

	// Set cache headers (taxonomy data is static)
	ctx.Response().Header().Set("Cache-Control", taxonomyCacheControl)
	ctx.Response().Header().Set("Vary", taxonomyVary)

	return ctx.JSON(http.StatusOK, response)
}

// GetSpeciesTree retrieves the complete taxonomic tree for a species
// GET /api/v2/taxonomy/tree/:scientific_name
func (c *Controller) GetSpeciesTree(ctx echo.Context) error {
	scientificName := ctx.Param("scientific_name")
	if scientificName == "" {
		return c.HandleError(ctx, errors.Newf("scientific_name parameter is required").
			Category(errors.CategoryValidation).
			Component("api-taxonomy").
			Build(), "Missing scientific_name parameter", http.StatusBadRequest)
	}

	// Validate scientific name format (basic validation)
	scientificName = strings.TrimSpace(scientificName)

	// Properly decode URL-encoded characters
	decodedName, err := url.PathUnescape(scientificName)
	if err != nil {
		return c.HandleError(ctx, errors.Newf("invalid URL encoding in scientific name").
			Category(errors.CategoryValidation).
			Context("raw_name", scientificName).
			Component("api-taxonomy").
			Build(), "Invalid URL encoding in scientific name", http.StatusBadRequest)
	}
	scientificName = strings.TrimSpace(decodedName)

	if len(scientificName) < 3 || !strings.Contains(scientificName, " ") {
		return c.HandleError(ctx, errors.Newf("invalid scientific name format").
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("api-taxonomy").
			Build(), "Invalid scientific name format", http.StatusBadRequest)
	}

	// Check if taxonomy database is available
	if c.TaxonomyDB == nil {
		return c.HandleError(ctx, errors.Newf("taxonomy database not available").
			Category(errors.CategorySystem).
			Component("api-taxonomy").
			Build(), "Taxonomy database not available", http.StatusServiceUnavailable)
	}

	// Get complete species tree
	result, err := c.TaxonomyDB.GetSpeciesTree(scientificName)
	if err != nil {
		return c.HandleError(ctx, err, "Species not found", http.StatusNotFound)
	}

	// Set cache headers (taxonomy data is static)
	ctx.Response().Header().Set("Cache-Control", taxonomyCacheControl)
	ctx.Response().Header().Set("Vary", taxonomyVary)

	return ctx.JSON(http.StatusOK, result)
}
