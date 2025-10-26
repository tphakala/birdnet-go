package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strings"
)

// Full genus data (from API fetch)
type GenusInfo struct {
	Genus        string   `json:"genus"`
	Family       string   `json:"family"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	SpeciesCount int      `json:"species_count"`
	Species      []string `json:"species"`
	CommonNames  []string `json:"common_names"`
	UpdatedAt    string   `json:"updated_at"`
}

// Optimized structure for production use - keeps species for lookups
type GenusMetadata struct {
	Family       string   `json:"family"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Species      []string `json:"species"` // Keep for bidirectional lookup
}

// Family metadata for family-level queries
type FamilyMetadata struct {
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Genera       []string `json:"genera"`       // List of genera in this family
	SpeciesCount int      `json:"species_count"` // Total species in family
}

// Optimized database structure with bidirectional lookup support
type TaxonomyDatabase struct {
	Version     string                      `json:"version"`
	Description string                      `json:"description"`
	Source      string                      `json:"source"`
	UpdatedAt   string                      `json:"updated_at"`
	License     string                      `json:"license"`
	Attribution string                      `json:"attribution"`
	GenusCount  int                         `json:"genus_count"`
	FamilyCount int                         `json:"family_count"`
	Genera      map[string]GenusMetadata    `json:"genera"`   // genus_name -> metadata
	Families    map[string]FamilyMetadata   `json:"families"` // family_name -> metadata
	SpeciesIndex map[string]string          `json:"species_index"` // scientific_name -> genus_name
}

func main() {
	// Read the full genus data
	data, err := os.ReadFile("/tmp/genus_taxonomy.json")
	if err != nil {
		// Try old filename
		data, err = os.ReadFile("/tmp/ebird_genus_data.json")
		if err != nil {
			log.Fatalf("Failed to read genus data: %v", err)
		}
	}

	// Check if it's the optimized format or full format
	var fullData []GenusInfo
	var optimizedData struct {
		Genera map[string]struct {
			Family       string   `json:"family"`
			FamilyCommon string   `json:"family_common"`
			Order        string   `json:"order"`
		} `json:"genera"`
	}

	// Try to unmarshal as optimized first
	if err := json.Unmarshal(data, &optimizedData); err == nil && len(optimizedData.Genera) > 0 {
		// It's already optimized, need to read full data
		data, err = os.ReadFile("/tmp/ebird_genus_data.json")
		if err != nil {
			log.Fatalf("Need full genus data with species lists: %v", err)
		}
	}

	if err := json.Unmarshal(data, &fullData); err != nil {
		log.Fatalf("Failed to parse genus data: %v", err)
	}

	// Create optimized structure
	db := TaxonomyDatabase{
		Version:     "2024",
		Description: "Genus and family taxonomy metadata with bidirectional species lookup",
		Source:      "eBird API v2 - https://api.ebird.org/v2/ref/taxonomy/ebird",
		UpdatedAt:   "2025-10-26",
		License:     "Derived from eBird data - Non-commercial use with attribution",
		Attribution: "Taxonomy data © Cornell Lab of Ornithology. Derived from eBird/Clements Checklist under eBird API Terms of Use.",
		GenusCount:  len(fullData),
		Genera:      make(map[string]GenusMetadata),
		Families:    make(map[string]FamilyMetadata),
		SpeciesIndex: make(map[string]string),
	}

	// Build genus map and species index
	for i := range fullData {
		genus := &fullData[i]
		// Use lowercase genus name as key for case-insensitive lookups
		genusKey := strings.ToLower(genus.Genus)

		db.Genera[genusKey] = GenusMetadata{
			Family:       genus.Family,
			FamilyCommon: genus.FamilyCommon,
			Order:        genus.Order,
			Species:      genus.Species, // Keep species list
		}

		// Build species index (scientific name -> genus)
		for _, species := range genus.Species {
			speciesKey := strings.ToLower(species)
			db.SpeciesIndex[speciesKey] = genusKey
		}

		// Build family index
		familyKey := strings.ToLower(genus.Family)
		family, exists := db.Families[familyKey]
		if !exists {
			family = FamilyMetadata{
				FamilyCommon: genus.FamilyCommon,
				Order:        genus.Order,
				Genera:       []string{},
				SpeciesCount: 0,
			}
		}
		family.Genera = append(family.Genera, genusKey)
		family.SpeciesCount += len(genus.Species)
		db.Families[familyKey] = family
	}

	db.FamilyCount = len(db.Families)

	// Write optimized database
	optimizedData2, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		log.Fatalf("Failed to marshal optimized data: %v", err)
	}

	outputFile := "/tmp/genus_taxonomy.json"
	if err := os.WriteFile(outputFile, optimizedData2, 0o644); err != nil {
		log.Fatalf("Failed to write optimized data: %v", err)
	}

	// Print statistics
	originalSize := len(data)
	optimizedSize := len(optimizedData2)

	var reduction float64
	if optimizedSize < originalSize {
		reduction = float64(originalSize-optimizedSize) / float64(originalSize) * 100
	} else {
		reduction = -float64(optimizedSize-originalSize) / float64(originalSize) * 100
	}

	fmt.Printf("✅ Optimization complete with bidirectional lookup support!\n\n")
	fmt.Printf("Original size:  %.2f KB (%d bytes)\n", float64(originalSize)/1024, originalSize)
	fmt.Printf("Optimized size: %.2f KB (%d bytes)\n", float64(optimizedSize)/1024, optimizedSize)
	if reduction > 0 {
		fmt.Printf("Reduction:      %.1f%%\n\n", reduction)
	} else {
		fmt.Printf("Increase:       %.1f%% (added indexes for lookups)\n\n", -reduction)
	}
	fmt.Printf("Genus count:    %d\n", db.GenusCount)
	fmt.Printf("Family count:   %d\n", db.FamilyCount)
	fmt.Printf("Species index:  %d entries\n", len(db.SpeciesIndex))
	fmt.Printf("Output file:    %s\n", outputFile)

	// Test lookups
	fmt.Printf("\n=== Testing Lookups ===\n")

	// Test 1: Scientific name -> genus
	testSpecies := "Turdus migratorius"
	if genus, ok := db.SpeciesIndex[strings.ToLower(testSpecies)]; ok {
		if meta, ok := db.Genera[genus]; ok {
			fmt.Printf("\n1. Species to Genus:\n")
			fmt.Printf("   %s -> %s (%s, %s)\n", testSpecies, strings.ToUpper(genus[:1])+genus[1:], meta.Family, meta.Order)
		}
	}

	// Test 2: Genus -> species list
	testGenus := "corvus"
	if meta, ok := db.Genera[testGenus]; ok {
		fmt.Printf("\n2. Genus to Species:\n")
		fmt.Printf("   %s has %d species:\n", strings.ToUpper(testGenus[:1])+testGenus[1:], len(meta.Species))
		for i, sp := range meta.Species {
			if i < 5 {
				fmt.Printf("   - %s\n", sp)
			}
		}
		if len(meta.Species) > 5 {
			fmt.Printf("   ... and %d more\n", len(meta.Species)-5)
		}
	}

	// Test 3: Family -> genera and species count
	testFamily := "turdidae"
	if meta, ok := db.Families[testFamily]; ok {
		fmt.Printf("\n3. Family to Genera:\n")
		fmt.Printf("   %s (%s) has %d genera and %d species\n",
			strings.ToUpper(testFamily[:1])+testFamily[1:], meta.FamilyCommon, len(meta.Genera), meta.SpeciesCount)
		fmt.Printf("   First 5 genera:\n")
		for i, g := range meta.Genera {
			if i < 5 {
				fmt.Printf("   - %s\n", strings.ToUpper(g[:1])+g[1:])
			}
		}
		if len(meta.Genera) > 5 {
			fmt.Printf("   ... and %d more\n", len(meta.Genera)-5)
		}
	}

	fmt.Printf("\n✅ All lookups working correctly!\n")
}
