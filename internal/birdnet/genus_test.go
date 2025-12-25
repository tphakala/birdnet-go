package birdnet

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestLoadTaxonomyDatabase tests the taxonomy database loading
func TestLoadTaxonomyDatabase(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "loader")

	db, err := LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")
	require.NotNil(t, db, "Expected non-nil database")

	// Verify basic structure
	assert.NotEmpty(t, db.Genera, "Expected non-empty genera map")
	assert.NotEmpty(t, db.Families, "Expected non-empty families map")
	assert.NotEmpty(t, db.SpeciesIndex, "Expected non-empty species index")

	// Verify metadata
	assert.NotEmpty(t, db.Version, "Expected version to be set")
	assert.NotEmpty(t, db.Source, "Expected source to be set")
	assert.NotEmpty(t, db.Attribution, "Expected attribution to be set")

	t.Logf("Loaded taxonomy database: %d genera, %d families, %d species",
		len(db.Genera), len(db.Families), len(db.SpeciesIndex))
}

// TestGetGenusByScientificName tests genus lookup by scientific name
func TestGetGenusByScientificName(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "lookup")

	db, err := LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

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
				require.Error(t, err, "Expected error but got none")
				// Verify error is properly categorized
				var enhancedErr *errors.EnhancedError
				if errors.As(err, &enhancedErr) {
					assert.Equal(t, errors.CategoryNotFound, enhancedErr.Category)
				}
				return
			}

			require.NoError(t, err, "Unexpected error")
			assert.Equal(t, tt.wantGenus, genusName)
			require.NotNil(t, metadata, "Expected non-nil metadata")
			assert.Equal(t, tt.wantFamily, metadata.Family)
			assert.Equal(t, tt.wantOrder, metadata.Order)
			assert.NotEmpty(t, metadata.Species, "Expected non-empty species list")
		})
	}
}

// TestGetAllSpeciesInGenus tests retrieving all species in a genus
func TestGetAllSpeciesInGenus(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "query")

	db, err := LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

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
				assert.Error(t, err, "Expected error but got none")
				return
			}

			require.NoError(t, err, "Unexpected error")
			assert.GreaterOrEqual(t, len(species), tt.minCount, "Expected at least %d species, got %d", tt.minCount, len(species))

			// Verify all species are properly formatted
			for _, sp := range species {
				assert.Contains(t, sp, " ", "Species name %q doesn't appear to be a proper scientific name", sp)
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
	require.NoError(t, err, "Failed to load taxonomy database")

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
				assert.Error(t, err, "Expected error but got none")
				return
			}

			require.NoError(t, err, "Unexpected error")
			assert.GreaterOrEqual(t, len(species), tt.minCount, "Expected at least %d species, got %d", tt.minCount, len(species))

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
	require.NoError(t, err, "Failed to load taxonomy database")

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
				assert.Error(t, err, "Expected error but got none")
				return
			}

			require.NoError(t, err, "Unexpected error")
			require.NotNil(t, result, "Expected non-nil result")
			require.NotNil(t, result.TaxonomyTree, "Expected non-nil taxonomy tree")

			tree := result.TaxonomyTree

			// Verify standard bird taxonomy
			assert.Equal(t, "Animalia", tree.Kingdom)
			assert.Equal(t, "Chordata", tree.Phylum)
			assert.Equal(t, "Aves", tree.Class)
			assert.Equal(t, tt.wantOrder, tree.Order)
			assert.Equal(t, tt.wantFamily, tree.Family)
			assert.Equal(t, tt.wantGenus, tree.Genus)
			assert.Equal(t, tt.scientificName, tree.Species)

			// Verify related species
			assert.NotEmpty(t, result.RelatedInGenus, "Expected non-empty related species list")
			assert.NotZero(t, result.TotalInGenus, "Expected non-zero total in genus")
			assert.NotZero(t, result.TotalInFamily, "Expected non-zero total in family")

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
	require.NoError(t, err, "Failed to load taxonomy database")

	tree, err := db.BuildFamilyTree("Turdus migratorius")
	require.NoError(t, err, "Failed to build family tree")
	require.NotNil(t, tree, "Expected non-nil tree")

	assert.Equal(t, "Turdidae", tree.Family)
	assert.Equal(t, "Turdus", tree.Genus)
}

// TestGetFamilyInfo tests retrieving family information
func TestGetFamilyInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "query")

	db, err := LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	familyInfo, err := db.GetFamilyInfo("strigidae")
	require.NoError(t, err, "Failed to get family info")
	require.NotNil(t, familyInfo, "Expected non-nil family info")

	assert.NotEmpty(t, familyInfo.FamilyCommon, "Expected non-empty family common name")
	assert.NotEmpty(t, familyInfo.Order, "Expected non-empty order")
	assert.NotEmpty(t, familyInfo.Genera, "Expected non-empty genera list")
	assert.NotZero(t, familyInfo.SpeciesCount, "Expected non-zero species count")

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

			assert.GreaterOrEqual(t, len(matches), tt.minMatch, "Expected at least %d matches, got %d", tt.minMatch, len(matches))

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
	require.NoError(t, err, "Failed to load taxonomy database")

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
	require.NoError(t, err, "Failed to load taxonomy database")

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
	require.NoError(t, err, "Failed to load taxonomy database")

	stats := db.Stats()
	require.NotNil(t, stats, "Expected non-nil stats")

	requiredKeys := []string{"version", "updated_at", "genus_count", "family_count", "species_count", "source"}
	for _, key := range requiredKeys {
		assert.Contains(t, stats, key, "Expected stats to contain key %q", key)
	}

	// Verify counts are reasonable
	genusCount, ok := stats["genus_count"].(int)
	require.True(t, ok, "genus_count should be an int")
	assert.GreaterOrEqual(t, genusCount, 2000, "Expected genus count >= 2000")

	familyCount, ok := stats["family_count"].(int)
	require.True(t, ok, "family_count should be an int")
	assert.GreaterOrEqual(t, familyCount, 200, "Expected family count >= 200")

	speciesCount, ok := stats["species_count"].(int)
	require.True(t, ok, "species_count should be an int")
	assert.GreaterOrEqual(t, speciesCount, 10000, "Expected species count >= 10000")

	t.Logf("Stats: %d genera, %d families, %d species",
		genusCount, familyCount, speciesCount)
}

// BenchmarkLoadTaxonomyDatabase benchmarks database loading
func BenchmarkLoadTaxonomyDatabase(b *testing.B) {
	b.ReportAllocs()

	for b.Loop() {
		db, err := LoadTaxonomyDatabase()
		require.NoError(b, err, "Failed to load database")
		require.NotNil(b, db, "Expected non-nil database")
	}
}

// BenchmarkGetGenusByScientificName benchmarks genus lookup
func BenchmarkGetGenusByScientificName(b *testing.B) {
	b.ReportAllocs()

	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	for b.Loop() {
		_, _, err := db.GetGenusByScientificName("Turdus migratorius")
		require.NoError(b, err, "Lookup failed")
	}
}

// BenchmarkGetSpeciesTree benchmarks tree building
func BenchmarkGetSpeciesTree(b *testing.B) {
	b.ReportAllocs()

	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	for b.Loop() {
		_, err := db.GetSpeciesTree("Turdus migratorius")
		require.NoError(b, err, "Tree building failed")
	}
}
