package ebird

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func BenchmarkBuildFamilyTree(b *testing.B) {
	// Create a large taxonomy response for realistic benchmarking
	var taxonomyJSON strings.Builder
	taxonomyJSON.WriteString(`[`)
	for i := range 1000 {
		if i > 0 {
			taxonomyJSON.WriteString(",")
		}
		taxonomyJSON.WriteString(`{
			"sciName": "Species` + string(rune('A'+i%26)) + `",
			"comName": "Common Name",
			"speciesCode": "spec` + string(rune('a'+i%26)) + `",
			"category": "species",
			"order": "Passeriformes",
			"familySciName": "Familidae",
			"familyComName": "Family Name"
		}`)
	}
	// Add our target species
	taxonomyJSON.WriteString(`,{
		"sciName": "Turdus migratorius",
		"comName": "American Robin",
		"speciesCode": "amerob",
		"category": "species",
		"order": "Passeriformes",
		"familySciName": "Turdidae",
		"familyComName": "Thrushes and Allies"
	}]`)

	server := setupMockServer(b, map[string]mockResponse{
		"/ref/taxonomy/ebird?fmt=json": {
			status: http.StatusOK,
			body:   taxonomyJSON.String(),
		},
	})
	defer server.Close()

	client := setupTestClient(b, server)
	disableLogging(b)
	ctx := b.Context()

	// Warm up the cache
	_, err := client.BuildFamilyTree(ctx, "Turdus migratorius")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.BuildFamilyTree(ctx, "Turdus migratorius")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkGetTaxonomyWithCache(b *testing.B) {
	server := setupMockServer(b, map[string]mockResponse{
		"/ref/taxonomy/ebird?fmt=json": {
			status: http.StatusOK,
			body:   `[{"sciName": "Test species", "comName": "Test", "speciesCode": "test1"}]`,
		},
	})
	defer server.Close()

	client := setupTestClient(b, server)
	disableLogging(b)
	ctx := b.Context()

	// Warm up the cache
	_, err := client.GetTaxonomy(ctx, "")
	if err != nil {
		b.Fatal(err)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		_, err := client.GetTaxonomy(ctx, "")
		if err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkCacheLookup(b *testing.B) {
	// Create a minimal test server for client initialization
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := setupTestClient(b, server)
	disableLogging(b)

	// Pre-populate cache with various keys
	for i := range 100 {
		key := "taxonomy:" + string(rune('a'+i%26))
		client.cache.Set(key, []TaxonomyEntry{{ScientificName: "Test"}}, 1*time.Hour)
	}

	b.ResetTimer()
	b.ReportAllocs()
	for b.Loop() {
		if _, found := client.cache.Get("taxonomy:m"); !found {
			b.Fatal("Cache lookup failed")
		}
	}
}
