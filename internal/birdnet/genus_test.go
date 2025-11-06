package birdnet

import (
	"strings"
	"testing"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestLoadTaxonomyDatabase tests the taxonomy database loading
func TestLoadTaxonomyDatabase(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "loader")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	if db == nil {
		t.Fatal("Expected non-nil database")
	}

	// Verify basic structure
	if len(db.Genera) == 0 {
		t.Error("Expected non-empty genera map")
	}

	if len(db.Families) == 0 {
		t.Error("Expected non-empty families map")
	}

	if len(db.SpeciesIndex) == 0 {
		t.Error("Expected non-empty species index")
	}

	// Verify metadata
	if db.Version == "" {
		t.Error("Expected version to be set")
	}

	if db.Source == "" {
		t.Error("Expected source to be set")
	}

	if db.Attribution == "" {
		t.Error("Expected attribution to be set")
	}

	t.Logf("Loaded taxonomy database: %d genera, %d families, %d species",
		len(db.Genera), len(db.Families), len(db.SpeciesIndex))
}

// TestGetGenusByScientificName tests genus lookup by scientific name
func TestGetGenusByScientificName(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "lookup")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tests := []struct {
		name           string
		scientificName string
		wantGenus      string
		wantFamily     string
		wantOrder      string
		wantError      bool
	}{
		{
			name:           "american robin",
			scientificName: "Turdus migratorius",
			wantGenus:      "turdus",
			wantFamily:     "Turdidae",
			wantOrder:      "Passeriformes",
			wantError:      false,
		},
		{
			name:           "common raven",
			scientificName: "Corvus corax",
			wantGenus:      "corvus",
			wantFamily:     "Corvidae",
			wantOrder:      "Passeriformes",
			wantError:      false,
		},
		{
			name:           "case insensitive",
			scientificName: "TURDUS MIGRATORIUS",
			wantGenus:      "turdus",
			wantFamily:     "Turdidae",
			wantOrder:      "Passeriformes",
			wantError:      false,
		},
		{
			name:           "with leading/trailing spaces",
			scientificName: "  Corvus corax  ",
			wantGenus:      "corvus",
			wantFamily:     "Corvidae",
			wantOrder:      "Passeriformes",
			wantError:      false,
		},
		{
			name:           "nonexistent species",
			scientificName: "Nonexistent species",
			wantError:      true,
		},
		{
			name:           "empty string",
			scientificName: "",
			wantError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			genusName, metadata, err := db.GetGenusByScientificName(tt.scientificName)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				// Verify error is properly categorized
				var enhancedErr *errors.EnhancedError
				if errors.As(err, &enhancedErr) {
					if enhancedErr.Category != errors.CategoryNotFound {
						t.Errorf("Expected CategoryNotFound, got %v", enhancedErr.Category)
					}
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if genusName != tt.wantGenus {
				t.Errorf("Expected genus %q, got %q", tt.wantGenus, genusName)
			}

			if metadata == nil {
				t.Fatal("Expected non-nil metadata")
			}

			if metadata.Family != tt.wantFamily {
				t.Errorf("Expected family %q, got %q", tt.wantFamily, metadata.Family)
			}

			if metadata.Order != tt.wantOrder {
				t.Errorf("Expected order %q, got %q", tt.wantOrder, metadata.Order)
			}

			if len(metadata.Species) == 0 {
				t.Error("Expected non-empty species list")
			}
		})
	}
}

// TestGetAllSpeciesInGenus tests retrieving all species in a genus
func TestGetAllSpeciesInGenus(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "query")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tests := []struct {
		name      string
		genus     string
		wantError bool
		minCount  int
	}{
		{
			name:      "corvus genus",
			genus:     "corvus",
			wantError: false,
			minCount:  10, // Should have at least 10 corvus species
		},
		{
			name:      "turdus genus",
			genus:     "turdus",
			wantError: false,
			minCount:  50, // Turdus is a large genus
		},
		{
			name:      "case insensitive",
			genus:     "CORVUS",
			wantError: false,
			minCount:  10,
		},
		{
			name:      "nonexistent genus",
			genus:     "nonexistentgenus",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			species, err := db.GetAllSpeciesInGenus(tt.genus)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(species) < tt.minCount {
				t.Errorf("Expected at least %d species, got %d", tt.minCount, len(species))
			}

			// Verify all species are properly formatted
			for _, sp := range species {
				if !strings.Contains(sp, " ") {
					t.Errorf("Species name %q doesn't appear to be a proper scientific name", sp)
				}
			}

			t.Logf("Found %d species in genus %s", len(species), tt.genus)
		})
	}
}

// TestGetAllSpeciesInFamily tests retrieving all species in a family
func TestGetAllSpeciesInFamily(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "query")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tests := []struct {
		name      string
		family    string
		wantError bool
		minCount  int
	}{
		{
			name:      "owls (strigidae)",
			family:    "strigidae",
			wantError: false,
			minCount:  200, // Should have around 229 owl species
		},
		{
			name:      "corvids (corvidae)",
			family:    "corvidae",
			wantError: false,
			minCount:  100, // Large family
		},
		{
			name:      "case insensitive",
			family:    "STRIGIDAE",
			wantError: false,
			minCount:  200,
		},
		{
			name:      "nonexistent family",
			family:    "nonexistentfamily",
			wantError: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			species, err := db.GetAllSpeciesInFamily(tt.family)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if len(species) < tt.minCount {
				t.Errorf("Expected at least %d species, got %d", tt.minCount, len(species))
			}

			t.Logf("Found %d species in family %s", len(species), tt.family)
		})
	}
}

// TestGetSpeciesTree tests building a complete taxonomic tree
func TestGetSpeciesTree(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "query")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tests := []struct {
		name           string
		scientificName string
		wantGenus      string
		wantFamily     string
		wantOrder      string
		wantError      bool
	}{
		{
			name:           "american robin",
			scientificName: "Turdus migratorius",
			wantGenus:      "Turdus",
			wantFamily:     "Turdidae",
			wantOrder:      "Passeriformes",
			wantError:      false,
		},
		{
			name:           "common raven",
			scientificName: "Corvus corax",
			wantGenus:      "Corvus",
			wantFamily:     "Corvidae",
			wantOrder:      "Passeriformes",
			wantError:      false,
		},
		{
			name:           "nonexistent species",
			scientificName: "Nonexistent species",
			wantError:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, err := db.GetSpeciesTree(tt.scientificName)

			if tt.wantError {
				if err == nil {
					t.Error("Expected error but got none")
				}
				return
			}

			if err != nil {
				t.Fatalf("Unexpected error: %v", err)
			}

			if result == nil {
				t.Fatal("Expected non-nil result")
			}

			if result.TaxonomyTree == nil {
				t.Fatal("Expected non-nil taxonomy tree")
			}

			tree := result.TaxonomyTree

			// Verify standard bird taxonomy
			if tree.Kingdom != "Animalia" {
				t.Errorf("Expected kingdom Animalia, got %q", tree.Kingdom)
			}

			if tree.Phylum != "Chordata" {
				t.Errorf("Expected phylum Chordata, got %q", tree.Phylum)
			}

			if tree.Class != "Aves" {
				t.Errorf("Expected class Aves, got %q", tree.Class)
			}

			if tree.Order != tt.wantOrder {
				t.Errorf("Expected order %q, got %q", tt.wantOrder, tree.Order)
			}

			if tree.Family != tt.wantFamily {
				t.Errorf("Expected family %q, got %q", tt.wantFamily, tree.Family)
			}

			if tree.Genus != tt.wantGenus {
				t.Errorf("Expected genus %q, got %q", tt.wantGenus, tree.Genus)
			}

			if tree.Species != tt.scientificName {
				t.Errorf("Expected species %q, got %q", tt.scientificName, tree.Species)
			}

			// Verify related species
			if len(result.RelatedInGenus) == 0 {
				t.Error("Expected non-empty related species list")
			}

			if result.TotalInGenus == 0 {
				t.Error("Expected non-zero total in genus")
			}

			if result.TotalInFamily == 0 {
				t.Error("Expected non-zero total in family")
			}

			t.Logf("Species tree: %d species in genus, %d in family",
				result.TotalInGenus, result.TotalInFamily)
		})
	}
}

// TestBuildFamilyTree tests compatibility with eBird API interface
func TestBuildFamilyTree(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "compatibility")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tree, err := db.BuildFamilyTree("Turdus migratorius")
	if err != nil {
		t.Fatalf("Failed to build family tree: %v", err)
	}

	if tree == nil {
		t.Fatal("Expected non-nil tree")
	}

	if tree.Family != "Turdidae" {
		t.Errorf("Expected family Turdidae, got %q", tree.Family)
	}

	if tree.Genus != "Turdus" {
		t.Errorf("Expected genus Turdus, got %q", tree.Genus)
	}
}

// TestGetFamilyInfo tests retrieving family information
func TestGetFamilyInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "query")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	familyInfo, err := db.GetFamilyInfo("strigidae")
	if err != nil {
		t.Fatalf("Failed to get family info: %v", err)
	}

	if familyInfo == nil {
		t.Fatal("Expected non-nil family info")
	}

	if familyInfo.FamilyCommon == "" {
		t.Error("Expected non-empty family common name")
	}

	if familyInfo.Order == "" {
		t.Error("Expected non-empty order")
	}

	if len(familyInfo.Genera) == 0 {
		t.Error("Expected non-empty genera list")
	}

	if familyInfo.SpeciesCount == 0 {
		t.Error("Expected non-zero species count")
	}

	t.Logf("Family %s: %d genera, %d species",
		familyInfo.FamilyCommon, len(familyInfo.Genera), familyInfo.SpeciesCount)
}

// testSearchHelper is a helper function to test search functionality
func testSearchHelper(t *testing.T, searchFn func(string) []string, testCases []struct {
	name     string
	pattern  string
	minMatch int
}) {
	t.Helper()

	for _, tt := range testCases {
		t.Run(tt.name, func(t *testing.T) {
			matches := searchFn(tt.pattern)

			if len(matches) < tt.minMatch {
				t.Errorf("Expected at least %d matches, got %d", tt.minMatch, len(matches))
			}

			t.Logf("Found %d matches for pattern %q", len(matches), tt.pattern)
		})
	}
}

// TestSearchGenus tests genus search functionality
func TestSearchGenus(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "search")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tests := []struct {
		name     string
		pattern  string
		minMatch int
	}{
		{
			name:     "search corvus",
			pattern:  "corv",
			minMatch: 1,
		},
		{
			name:     "search turdus",
			pattern:  "turd",
			minMatch: 1,
		},
		{
			name:     "case insensitive",
			pattern:  "CORV",
			minMatch: 1,
		},
		{
			name:     "nonexistent pattern",
			pattern:  "zzzzzzz",
			minMatch: 0,
		},
	}

	testSearchHelper(t, db.SearchGenus, tests)
}

// TestSearchFamily tests family search functionality
func TestSearchFamily(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "search")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	tests := []struct {
		name     string
		pattern  string
		minMatch int
	}{
		{
			name:     "search strigidae",
			pattern:  "strig",
			minMatch: 1,
		},
		{
			name:     "search corvidae",
			pattern:  "corvid",
			minMatch: 1,
		},
		{
			name:     "case insensitive",
			pattern:  "STRIG",
			minMatch: 1,
		},
		{
			name:     "nonexistent pattern",
			pattern:  "zzzzzzz",
			minMatch: 0,
		},
	}

	testSearchHelper(t, db.SearchFamily, tests)
}

// TestStats tests database statistics
func TestStats(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "metadata")

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		t.Fatalf("Failed to load taxonomy database: %v", err)
	}

	stats := db.Stats()

	if stats == nil {
		t.Fatal("Expected non-nil stats")
	}

	requiredKeys := []string{"version", "updated_at", "genus_count", "family_count", "species_count", "source"}
	for _, key := range requiredKeys {
		if _, exists := stats[key]; !exists {
			t.Errorf("Expected stats to contain key %q", key)
		}
	}

	// Verify counts are reasonable
	genusCount, ok := stats["genus_count"].(int)
	if !ok || genusCount < 2000 {
		t.Errorf("Expected genus count >= 2000, got %v", genusCount)
	}

	familyCount, ok := stats["family_count"].(int)
	if !ok || familyCount < 200 {
		t.Errorf("Expected family count >= 200, got %v", familyCount)
	}

	speciesCount, ok := stats["species_count"].(int)
	if !ok || speciesCount < 10000 {
		t.Errorf("Expected species count >= 10000, got %v", speciesCount)
	}

	t.Logf("Stats: %d genera, %d families, %d species",
		genusCount, familyCount, speciesCount)
}

// BenchmarkLoadTaxonomyDatabase benchmarks database loading
func BenchmarkLoadTaxonomyDatabase(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		db, err := LoadTaxonomyDatabase()
		if err != nil {
			b.Fatalf("Failed to load database: %v", err)
		}
		if db == nil {
			b.Fatal("Expected non-nil database")
		}
	}
}

// BenchmarkGetGenusByScientificName benchmarks genus lookup
func BenchmarkGetGenusByScientificName(b *testing.B) {
	b.ReportAllocs()

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		b.Fatalf("Failed to load database: %v", err)
	}

	for b.Loop() {
		_, _, err := db.GetGenusByScientificName("Turdus migratorius")
		if err != nil {
			b.Fatalf("Lookup failed: %v", err)
		}
	}
}

// BenchmarkGetSpeciesTree benchmarks tree building
func BenchmarkGetSpeciesTree(b *testing.B) {
	b.ReportAllocs()

	db, err := LoadTaxonomyDatabase()
	if err != nil {
		b.Fatalf("Failed to load database: %v", err)
	}

	for b.Loop() {
		_, err := db.GetSpeciesTree("Turdus migratorius")
		if err != nil {
			b.Fatalf("Tree building failed: %v", err)
		}
	}
}
