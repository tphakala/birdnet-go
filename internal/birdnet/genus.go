// genus.go contains functions for working with local genus taxonomy data
package birdnet

import (
	_ "embed" // For embedding data
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/tphakala/birdnet-go/internal/ebird"
	"github.com/tphakala/birdnet-go/internal/errors"
)

//go:embed data/genus_taxonomy.json
var genusTaxonomyData []byte

// TaxonomyDatabase represents the complete genus/family taxonomy database
type TaxonomyDatabase struct {
	Version      string                     `json:"version"`
	Description  string                     `json:"description"`
	Source       string                     `json:"source"`
	UpdatedAt    string                     `json:"updated_at"`
	License      string                     `json:"license"`
	Attribution  string                     `json:"attribution"`
	GenusCount   int                        `json:"genus_count"`
	FamilyCount  int                        `json:"family_count"`
	Genera       map[string]*GenusMetadata  `json:"genera"`
	Families     map[string]*FamilyMetadata `json:"families"`
	SpeciesIndex map[string]string          `json:"species_index"`
}

// GenusMetadata represents metadata for a genus
type GenusMetadata struct {
	Family       string   `json:"family"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Species      []string `json:"species"`
}

// FamilyMetadata represents metadata for a family
type FamilyMetadata struct {
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Genera       []string `json:"genera"`
	SpeciesCount int      `json:"species_count"`
}

// SpeciesTreeResult represents a complete taxonomic tree with related species
type SpeciesTreeResult struct {
	TaxonomyTree    *ebird.TaxonomyTree `json:"taxonomy_tree"`
	RelatedInGenus  []string            `json:"related_in_genus"`
	RelatedInFamily []string            `json:"related_in_family,omitempty"`
	TotalInGenus    int                 `json:"total_in_genus"`
	TotalInFamily   int                 `json:"total_in_family"`
}

// LoadTaxonomyDatabase loads the embedded genus taxonomy database
func LoadTaxonomyDatabase() (*TaxonomyDatabase, error) {
	var db TaxonomyDatabase

	if err := json.Unmarshal(genusTaxonomyData, &db); err != nil {
		return nil, errors.New(err).
			Category(errors.CategoryProcessing).
			Component("birdnet-genus").
			Build()
	}

	// Validate database - require all core data structures to be populated and non-nil
	if db.Genera == nil || db.Families == nil || db.SpeciesIndex == nil ||
		len(db.Genera) == 0 || len(db.Families) == 0 || len(db.SpeciesIndex) == 0 {
		return nil, errors.Newf("taxonomy database is empty or invalid").
			Category(errors.CategoryValidation).
			Component("birdnet-genus").
			Build()
	}

	// Normalize all map keys to lowercase at load time
	// This prevents subtle lookup bugs from case mismatches
	db.Genera = normalizeGeneraKeys(db.Genera)
	db.Families = normalizeFamiliesKeys(db.Families)
	db.Families = normalizeFamilyGeneraValues(db.Families)
	db.SpeciesIndex = normalizeSpeciesIndexKeys(db.SpeciesIndex)
	db.SpeciesIndex = normalizeSpeciesIndexValues(db.SpeciesIndex)

	return &db, nil
}

// normalizeGeneraKeys normalizes all genus names to lowercase
func normalizeGeneraKeys(genera map[string]*GenusMetadata) map[string]*GenusMetadata {
	normalized := make(map[string]*GenusMetadata, len(genera))
	for key, value := range genera {
		normalized[strings.ToLower(key)] = value
	}
	return normalized
}

// normalizeFamiliesKeys normalizes all family names to lowercase
func normalizeFamiliesKeys(families map[string]*FamilyMetadata) map[string]*FamilyMetadata {
	normalized := make(map[string]*FamilyMetadata, len(families))
	for key, value := range families {
		normalized[strings.ToLower(key)] = value
	}
	return normalized
}

// normalizeSpeciesIndexKeys normalizes all species names to lowercase
func normalizeSpeciesIndexKeys(speciesIndex map[string]string) map[string]string {
	normalized := make(map[string]string, len(speciesIndex))
	for key, value := range speciesIndex {
		normalized[strings.ToLower(key)] = value
	}
	return normalized
}

// normalizeSpeciesIndexValues lower-cases all genus values in the species index
func normalizeSpeciesIndexValues(si map[string]string) map[string]string {
	normalized := make(map[string]string, len(si))
	for k, v := range si {
		normalized[k] = strings.ToLower(v)
	}
	return normalized
}

// normalizeFamilyGeneraValues lower-cases genera lists in each family
func normalizeFamilyGeneraValues(families map[string]*FamilyMetadata) map[string]*FamilyMetadata {
	normalized := make(map[string]*FamilyMetadata, len(families))
	for key, fm := range families {
		// Create normalized genera slice
		genera := make([]string, len(fm.Genera))
		for i, g := range fm.Genera {
			genera[i] = strings.ToLower(g)
		}
		// Create new FamilyMetadata with normalized genera
		normalized[key] = &FamilyMetadata{
			FamilyCommon: fm.FamilyCommon,
			Order:        fm.Order,
			Genera:       genera,
			SpeciesCount: fm.SpeciesCount,
		}
	}
	return normalized
}

// GetGenusByScientificName retrieves genus metadata by scientific name
// Returns the genus name and metadata, or error if not found
func (db *TaxonomyDatabase) GetGenusByScientificName(scientificName string) (string, *GenusMetadata, error) {
	if db == nil || db.SpeciesIndex == nil {
		return "", nil, errors.Newf("taxonomy database not initialized").
			Category(errors.CategorySystem).
			Component("birdnet-genus").
			Build()
	}

	// Normalize the scientific name (lowercase for case-insensitive lookup)
	normalized := strings.ToLower(strings.TrimSpace(scientificName))

	// Look up genus from species index
	genusName, exists := db.SpeciesIndex[normalized]
	if !exists {
		return "", nil, errors.Newf("species '%s' not found in taxonomy database", scientificName).
			Category(errors.CategoryNotFound).
			Context("scientific_name", scientificName).
			Component("birdnet-genus").
			Build()
	}

	// Get genus metadata
	genusMetadata, exists := db.Genera[genusName]
	if !exists {
		return "", nil, errors.Newf("genus '%s' metadata not found", genusName).
			Category(errors.CategoryNotFound).
			Context("genus", genusName).
			Context("scientific_name", scientificName).
			Component("birdnet-genus").
			Build()
	}

	return genusName, genusMetadata, nil
}

// GetAllSpeciesInGenus returns all species in a given genus
func (db *TaxonomyDatabase) GetAllSpeciesInGenus(genusName string) ([]string, error) {
	if db == nil || db.Genera == nil {
		return nil, errors.Newf("taxonomy database not initialized").
			Category(errors.CategorySystem).
			Component("birdnet-genus").
			Build()
	}

	// Normalize genus name (lowercase)
	normalized := strings.ToLower(strings.TrimSpace(genusName))

	genusMetadata, exists := db.Genera[normalized]
	if !exists {
		return nil, errors.Newf("genus '%s' not found in taxonomy database", genusName).
			Category(errors.CategoryNotFound).
			Context("genus", genusName).
			Component("birdnet-genus").
			Build()
	}

	// Return a copy to prevent mutation of internal state
	return slices.Clone(genusMetadata.Species), nil
}

// GetAllSpeciesInFamily returns all species in a given family
func (db *TaxonomyDatabase) GetAllSpeciesInFamily(familyName string) ([]string, error) {
	if db == nil || db.Families == nil || db.Genera == nil {
		return nil, errors.Newf("taxonomy database not initialized").
			Category(errors.CategorySystem).
			Component("birdnet-genus").
			Build()
	}

	// Normalize family name (lowercase)
	normalized := strings.ToLower(strings.TrimSpace(familyName))

	familyMetadata, exists := db.Families[normalized]
	if !exists {
		return nil, errors.Newf("family '%s' not found in taxonomy database", familyName).
			Category(errors.CategoryNotFound).
			Context("family", familyName).
			Component("birdnet-genus").
			Build()
	}

	// Collect all species from all genera in the family
	// Preallocate with known species count for efficiency
	allSpecies := make([]string, 0, familyMetadata.SpeciesCount)
	for _, genusName := range familyMetadata.Genera {
		genusMetadata, exists := db.Genera[genusName]
		if exists {
			allSpecies = append(allSpecies, genusMetadata.Species...)
		}
	}

	return allSpecies, nil
}

// GetSpeciesTree builds a complete taxonomic tree for a species
// This returns the same structure as eBird API for compatibility
func (db *TaxonomyDatabase) GetSpeciesTree(scientificName string) (*SpeciesTreeResult, error) {
	if db == nil {
		return nil, errors.Newf("taxonomy database not initialized").
			Category(errors.CategorySystem).
			Component("birdnet-genus").
			Build()
	}

	// Get genus information
	_, genusMetadata, err := db.GetGenusByScientificName(scientificName)
	if err != nil {
		return nil, err
	}

	// Extract genus from scientific name
	parts := strings.Fields(scientificName)
	if len(parts) == 0 {
		return nil, errors.Newf("invalid scientific name format: '%s'", scientificName).
			Category(errors.CategoryValidation).
			Context("scientific_name", scientificName).
			Component("birdnet-genus").
			Build()
	}
	genus := parts[0]

	// Get family metadata
	familyName := strings.ToLower(genusMetadata.Family)
	familyMetadata, exists := db.Families[familyName]
	if !exists {
		return nil, errors.Newf("family '%s' not found in taxonomy database", genusMetadata.Family).
			Category(errors.CategoryNotFound).
			Context("family", genusMetadata.Family).
			Context("scientific_name", scientificName).
			Component("birdnet-genus").
			Build()
	}

	// Build taxonomy tree compatible with eBird API
	// Use deterministic timestamp from DB metadata when available
	updatedAt := time.Now().UTC()
	if t, err := time.Parse(time.RFC3339, db.UpdatedAt); err == nil {
		updatedAt = t
	}

	tree := &ebird.TaxonomyTree{
		Kingdom:       "Animalia",
		Phylum:        "Chordata",
		Class:         "Aves",
		Order:         genusMetadata.Order,
		Family:        genusMetadata.Family,
		FamilyCommon:  genusMetadata.FamilyCommon,
		Genus:         genus,
		Species:       scientificName,
		SpeciesCommon: "", // Would need common name mapping
		UpdatedAt:     updatedAt,
	}

	// Build result with related species
	result := &SpeciesTreeResult{
		TaxonomyTree:   tree,
		RelatedInGenus: slices.Clone(genusMetadata.Species), // Clone to prevent mutation
		TotalInGenus:   len(genusMetadata.Species),
		TotalInFamily:  familyMetadata.SpeciesCount,
	}

	return result, nil
}

// BuildFamilyTree builds a family tree compatible with eBird API
// This method provides compatibility with the existing eBird client interface
func (db *TaxonomyDatabase) BuildFamilyTree(scientificName string) (*ebird.TaxonomyTree, error) {
	result, err := db.GetSpeciesTree(scientificName)
	if err != nil {
		return nil, err
	}
	return result.TaxonomyTree, nil
}

// GetFamilyInfo retrieves family information including all genera
func (db *TaxonomyDatabase) GetFamilyInfo(familyName string) (*FamilyMetadata, error) {
	if db == nil || db.Families == nil {
		return nil, errors.Newf("taxonomy database not initialized").
			Category(errors.CategorySystem).
			Component("birdnet-genus").
			Build()
	}

	// Normalize family name (lowercase)
	normalized := strings.ToLower(strings.TrimSpace(familyName))

	familyMetadata, exists := db.Families[normalized]
	if !exists {
		return nil, errors.Newf("family '%s' not found in taxonomy database", familyName).
			Category(errors.CategoryNotFound).
			Context("family", familyName).
			Component("birdnet-genus").
			Build()
	}

	return familyMetadata, nil
}

// GetGenusInfo retrieves genus information including all species
func (db *TaxonomyDatabase) GetGenusInfo(genusName string) (*GenusMetadata, error) {
	if db == nil || db.Genera == nil {
		return nil, errors.Newf("taxonomy database not initialized").
			Category(errors.CategorySystem).
			Component("birdnet-genus").
			Build()
	}

	// Normalize genus name (lowercase)
	normalized := strings.ToLower(strings.TrimSpace(genusName))

	genusMetadata, exists := db.Genera[normalized]
	if !exists {
		return nil, errors.Newf("genus '%s' not found in taxonomy database", genusName).
			Category(errors.CategoryNotFound).
			Context("genus", genusName).
			Component("birdnet-genus").
			Build()
	}

	return genusMetadata, nil
}

// SearchGenus performs a case-insensitive search for genera matching a pattern
func (db *TaxonomyDatabase) SearchGenus(pattern string) []string {
	if db == nil || db.Genera == nil {
		return nil
	}

	pattern = strings.ToLower(pattern)
	var matches []string

	for genusName := range db.Genera {
		if strings.Contains(genusName, pattern) {
			matches = append(matches, genusName)
		}
	}

	// Sort results alphabetically for stable output
	slices.Sort(matches)

	return matches
}

// SearchFamily performs a case-insensitive search for families matching a pattern
func (db *TaxonomyDatabase) SearchFamily(pattern string) []string {
	if db == nil || db.Families == nil {
		return nil
	}

	pattern = strings.ToLower(pattern)
	var matches []string

	for familyName := range db.Families {
		if strings.Contains(familyName, pattern) {
			matches = append(matches, familyName)
		}
	}

	// Sort results alphabetically for stable output
	slices.Sort(matches)

	return matches
}

// Stats returns statistics about the taxonomy database
func (db *TaxonomyDatabase) Stats() map[string]any {
	if db == nil {
		return map[string]any{
			"error": "database not initialized",
		}
	}

	return map[string]any{
		"version":       db.Version,
		"description":   db.Description,
		"updated_at":    db.UpdatedAt,
		"genus_count":   len(db.Genera),
		"family_count":  len(db.Families),
		"species_count": len(db.SpeciesIndex),
		"source":        db.Source,
		"license":       db.License,
		"attribution":   db.Attribution,
	}
}
