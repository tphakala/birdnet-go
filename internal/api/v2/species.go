// internal/api/v2/species.go
package api

import (
	"net/http"
	"strings"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/tphakala/birdnet-go/internal/birdnet"
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

// SpeciesInfo represents extended information about a bird species
type SpeciesInfo struct {
	ScientificName string                 `json:"scientific_name"`
	CommonName     string                 `json:"common_name"`
	Rarity         *SpeciesRarityInfo     `json:"rarity,omitempty"`
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
	speciesInfo, err := c.getSpeciesInfo(scientificName)
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
func (c *Controller) getSpeciesInfo(scientificName string) (*SpeciesInfo, error) {
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
	case score > 0.8:
		return RarityVeryCommon
	case score > 0.5:
		return RarityCommon
	case score > 0.2:
		return RarityUncommon
	case score > 0.05:
		return RarityRare
	default:
		return RarityVeryRare
	}
}