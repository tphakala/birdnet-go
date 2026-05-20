package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"
)

// TaxonomyDatabase matches the production struct in internal/classifier/genus.go
type TaxonomyDatabase struct {
	Version      string                    `json:"version"`
	Description  string                    `json:"description"`
	Source       string                    `json:"source"`
	UpdatedAt    string                    `json:"updated_at"`
	License      string                    `json:"license"`
	Attribution  string                    `json:"attribution"`
	GenusCount   int                       `json:"genus_count"`
	FamilyCount  int                       `json:"family_count"`
	Genera       map[string]GenusMetadata  `json:"genera"`
	Families     map[string]FamilyMetadata `json:"families"`
	SpeciesIndex map[string]string         `json:"species_index"`
}

type GenusMetadata struct {
	Class        string   `json:"class"`
	Family       string   `json:"family"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Species      []string `json:"species"`
}

type FamilyMetadata struct {
	Class        string   `json:"class"`
	FamilyCommon string   `json:"family_common"`
	Order        string   `json:"order"`
	Genera       []string `json:"genera"`
	SpeciesCount int      `json:"species_count"`
}

// GBIFResponse represents a GBIF species match API response
type GBIFResponse struct {
	UsageKey       int    `json:"usageKey"`
	ScientificName string `json:"scientificName"`
	MatchType      string `json:"matchType"`
	Status         string `json:"status"`
	Kingdom        string `json:"kingdom"`
	Phylum         string `json:"phylum"`
	Class          string `json:"class"`
	Order          string `json:"order"`
	Family         string `json:"family"`
	Genus          string `json:"genus"`
	Species        string `json:"species"`
	Confidence     int    `json:"confidence"`
}

// GBIFCache stores cached GBIF responses to avoid re-fetching
type GBIFCache struct {
	Entries   map[string]*GBIFResponse `json:"entries"`
	UpdatedAt string                   `json:"updated_at"`
}

var errNoMatch = fmt.Errorf("no GBIF match")

const (
	gbifAPIURL   = "https://api.gbif.org/v1/species/match"
	cacheFile    = "/tmp/gbif_taxonomy_cache.json"
	requestDelay = 100 * time.Millisecond
)

func main() {
	if len(os.Args) < 3 {
		fmt.Fprintf(os.Stderr, "Usage: %s <existing_taxonomy.json> <perch_v2_labels.txt>\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "\nEnriches the existing bird-only taxonomy JSON with non-bird species\n")
		fmt.Fprintf(os.Stderr, "from the Perch v2 label set, using the GBIF Backbone Taxonomy API.\n")
		fmt.Fprintf(os.Stderr, "\nExample:\n")
		fmt.Fprintf(os.Stderr, "  go run %s internal/classifier/data/genus_taxonomy.json ~/onnx/perch_v2_labels.csv\n", os.Args[0])
		os.Exit(1)
	}

	existingPath := os.Args[1]
	labelsPath := os.Args[2]

	// Load existing taxonomy database
	log.Printf("Loading existing taxonomy from %s", existingPath)
	db, err := loadDatabase(existingPath)
	if err != nil {
		log.Fatalf("Failed to load existing taxonomy: %v", err)
	}
	log.Printf("Loaded: %d genera, %d families, %d species", len(db.Genera), len(db.Families), len(db.SpeciesIndex))

	// Add "class": "Aves" to all existing bird entries that lack it
	backfilled := addClassToExistingBirds(db)

	// Read Perch v2 labels
	log.Printf("Reading Perch v2 labels from %s", labelsPath)
	labels, err := readLabels(labelsPath)
	if err != nil {
		log.Fatalf("Failed to read labels: %v", err)
	}
	log.Printf("Read %d labels", len(labels))

	// Filter to species not already in the database, deduplicating labels
	seen := make(map[string]bool, len(labels))
	var unknown []string
	for _, label := range labels {
		key := strings.ToLower(label)
		if seen[key] {
			continue
		}
		seen[key] = true
		if _, exists := db.SpeciesIndex[key]; !exists {
			unknown = append(unknown, label)
		}
	}
	log.Printf("Found %d species not in existing taxonomy", len(unknown))

	if len(unknown) == 0 {
		if backfilled {
			log.Printf("No new species, but writing backfilled class fields to %s", existingPath)
			if err := writeDatabase(existingPath, db); err != nil {
				log.Fatalf("Failed to write database: %v", err)
			}
		}
		log.Println("No new species to add. Done.")
		return
	}

	// Load GBIF cache
	cache := loadCache()
	log.Printf("GBIF cache: %d entries", len(cache.Entries))

	// Query GBIF for unknown species
	client := &http.Client{Timeout: 10 * time.Second}
	matched, failed := 0, 0

	for i, species := range unknown {
		if (i+1)%100 == 0 {
			log.Printf("Progress: %d/%d (matched: %d, failed: %d)", i+1, len(unknown), matched, failed)
		}

		resp, err := queryGBIF(client, cache, species)
		if err != nil {
			if !errors.Is(err, errNoMatch) {
				log.Printf("GBIF error for %q: %v", species, err)
			}
			failed++
			continue
		}

		if resp.Class == "" || resp.Order == "" || resp.Family == "" || resp.Genus == "" {
			failed++
			continue
		}

		addSpeciesToDB(db, species, resp)
		matched++
	}

	// Save cache
	saveCache(cache)
	log.Printf("GBIF queries complete: %d matched, %d failed", matched, failed)

	// Recalculate counts
	db.GenusCount = len(db.Genera)
	db.FamilyCount = len(db.Families)
	db.Description = "Multi-taxa genus and family taxonomy with bidirectional species lookup"
	db.Source = "eBird API v2 (birds) + GBIF Backbone Taxonomy (non-bird taxa)"
	db.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	db.Attribution = "Bird taxonomy: Cornell Lab of Ornithology (eBird/Clements Checklist). " +
		"Non-bird taxonomy: GBIF Backbone Taxonomy (CC0). " +
		"https://doi.org/10.15468/39omei"

	// Write output
	outputPath := existingPath
	log.Printf("Writing updated taxonomy to %s", outputPath)
	if err := writeDatabase(outputPath, db); err != nil {
		log.Fatalf("Failed to write database: %v", err)
	}

	// Print summary
	classCounts := countByClass(db)
	log.Printf("Final database: %d genera, %d families, %d species", db.GenusCount, db.FamilyCount, len(db.SpeciesIndex))
	log.Println("Species by class:")
	for class, count := range classCounts {
		log.Printf("  %s: %d", class, count)
	}
}

func loadDatabase(path string) (*TaxonomyDatabase, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var db TaxonomyDatabase
	if err := json.Unmarshal(data, &db); err != nil {
		return nil, err
	}
	if db.Genera == nil {
		db.Genera = make(map[string]GenusMetadata)
	}
	if db.Families == nil {
		db.Families = make(map[string]FamilyMetadata)
	}
	if db.SpeciesIndex == nil {
		db.SpeciesIndex = make(map[string]string)
	}
	return &db, nil
}

func writeDatabase(path string, db *TaxonomyDatabase) error {
	data, err := json.MarshalIndent(db, "", "  ")
	if err != nil {
		return err
	}
	data = append(data, '\n')
	return os.WriteFile(path, data, 0o644)
}

func readLabels(path string) ([]string, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()

	var labels []string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Skip FSD50K sound events (contain underscores or are single words);
		// valid species have binomial names with at least one space
		if strings.Contains(line, "_") || !strings.Contains(line, " ") {
			continue
		}
		labels = append(labels, line)
	}
	return labels, scanner.Err()
}

func addClassToExistingBirds(db *TaxonomyDatabase) bool {
	changed := false
	for key, meta := range db.Genera {
		if meta.Class == "" {
			meta.Class = "Aves"
			db.Genera[key] = meta
			changed = true
		}
	}
	for key, meta := range db.Families {
		if meta.Class == "" {
			meta.Class = "Aves"
			db.Families[key] = meta
			changed = true
		}
	}
	return changed
}

func loadCache() *GBIFCache {
	cache := &GBIFCache{
		Entries:   make(map[string]*GBIFResponse),
		UpdatedAt: time.Now().UTC().Format(time.RFC3339),
	}
	data, err := os.ReadFile(cacheFile)
	if err != nil {
		return cache
	}
	if err := json.Unmarshal(data, cache); err != nil {
		log.Printf("Warning: cache file parse failed, starting fresh: %v", err)
	}
	if cache.Entries == nil {
		cache.Entries = make(map[string]*GBIFResponse)
	}
	return cache
}

func saveCache(cache *GBIFCache) {
	cache.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
	data, err := json.MarshalIndent(cache, "", "  ")
	if err != nil {
		log.Printf("Warning: failed to marshal cache: %v", err)
		return
	}
	if err := os.WriteFile(cacheFile, data, 0o644); err != nil {
		log.Printf("Warning: failed to save cache: %v", err)
	}
}

func queryGBIF(client *http.Client, cache *GBIFCache, species string) (*GBIFResponse, error) {
	key := strings.ToLower(species)

	// Check cache first
	if cached, ok := cache.Entries[key]; ok {
		if cached == nil {
			return nil, errNoMatch
		}
		return cached, nil
	}

	// Rate limit
	time.Sleep(requestDelay)

	params := url.Values{}
	params.Set("name", species)
	params.Set("kingdom", "Animalia")
	params.Set("strict", "true")
	reqURL := gbifAPIURL + "?" + params.Encode()
	resp, err := client.Get(reqURL)
	if err != nil {
		return nil, fmt.Errorf("HTTP request failed: %w", err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	var gbif GBIFResponse
	if err := json.NewDecoder(resp.Body).Decode(&gbif); err != nil {
		return nil, fmt.Errorf("JSON decode failed: %w", err)
	}

	if gbif.MatchType == "NONE" {
		cache.Entries[key] = nil
		return nil, errNoMatch
	}

	cache.Entries[key] = &gbif
	return &gbif, nil
}

func addSpeciesToDB(db *TaxonomyDatabase, species string, gbif *GBIFResponse) {
	speciesKey := strings.ToLower(species)
	genusKey := strings.ToLower(gbif.Genus)
	familyKey := strings.ToLower(gbif.Family)

	// Add to species index
	db.SpeciesIndex[speciesKey] = genusKey

	// Add/update genus
	genus, exists := db.Genera[genusKey]
	if !exists {
		genus = GenusMetadata{
			Class:  gbif.Class,
			Family: gbif.Family,
			Order:  gbif.Order,
		}
	}
	genus.Species = append(genus.Species, species)
	db.Genera[genusKey] = genus

	// Add/update family
	family, exists := db.Families[familyKey]
	if !exists {
		family = FamilyMetadata{
			Class: gbif.Class,
			Order: gbif.Order,
		}
	}
	if !slices.Contains(family.Genera, genusKey) {
		family.Genera = append(family.Genera, genusKey)
	}
	family.SpeciesCount++
	db.Families[familyKey] = family
}

func countByClass(db *TaxonomyDatabase) map[string]int {
	counts := make(map[string]int)
	for _, genus := range db.Genera {
		class := genus.Class
		if class == "" {
			class = "unknown"
		}
		counts[class] += len(genus.Species)
	}
	return counts
}
