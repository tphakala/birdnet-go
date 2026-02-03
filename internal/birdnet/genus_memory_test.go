package birdnet

import (
	"runtime"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestConcurrentAccess tests concurrent access to the taxonomy database
func TestConcurrentAccess(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "concurrency")

	db, err := LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	// Test cases to run concurrently
	testCases := []struct {
		name           string
		scientificName string
	}{
		{"american robin", "Turdus migratorius"},
		{"common raven", "Corvus corax"},
		{"great horned owl", "Bubo virginianus"},
		{"northern cardinal", "Cardinalis cardinalis"}, //nolint:misspell // Cardinalis is correct genus name
		{"blue jay", "Cyanocitta cristata"},
	}

	// Run 100 concurrent goroutines performing lookups
	const numGoroutines = 100
	const lookupsPerGoroutine = 100

	var wg sync.WaitGroup
	wg.Add(numGoroutines)

	// Track errors from goroutines
	errChan := make(chan error, numGoroutines)

	for i := range numGoroutines {
		go func(routineID int) {
			defer wg.Done()

			for j := range lookupsPerGoroutine {
				tc := testCases[j%len(testCases)]

				// Test GetGenusByScientificName
				_, _, err := db.GetGenusByScientificName(tc.scientificName)
				if err != nil {
					errChan <- err
					return
				}

				// Test GetSpeciesTree
				_, err = db.GetSpeciesTree(tc.scientificName)
				if err != nil {
					errChan <- err
					return
				}

				// Test BuildFamilyTree
				_, err = db.BuildFamilyTree(tc.scientificName)
				if err != nil {
					errChan <- err
					return
				}
			}
		}(i)
	}

	wg.Wait()
	close(errChan)

	// Check for errors
	errors := make([]error, 0, numGoroutines)
	for err := range errChan { //nolint:gocritic // channel, not map
		errors = append(errors, err)
	}
	assert.Empty(t, errors, "Expected no concurrent access errors")

	t.Logf("Successfully completed %d concurrent goroutines with %d lookups each",
		numGoroutines, lookupsPerGoroutine)
}

// TestMemoryLeaks tests for memory leaks during repeated operations
func TestMemoryLeaks(t *testing.T) {
	if testing.Short() {
		t.Skip("Skipping memory leak test in short mode")
	}

	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "memory")

	db, err := LoadTaxonomyDatabase()
	require.NoError(t, err, "Failed to load taxonomy database")

	// Force GC and get baseline memory stats
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Perform many operations
	const iterations = 10000
	for i := range iterations {
		// Rotate through different species
		species := []string{
			"Turdus migratorius",
			"Corvus corax",
			"Bubo virginianus",
			"Cardinalis cardinalis", //nolint:misspell // Cardinalis is correct genus name
		}
		scientificName := species[i%len(species)]

		// Perform various operations
		_, _, _ = db.GetGenusByScientificName(scientificName)
		_, _ = db.GetSpeciesTree(scientificName)
		_, _ = db.BuildFamilyTree(scientificName)

		// Extract genus for lookup
		genusName := "corvus"
		if i%2 == 0 {
			genusName = "turdus"
		}
		_, _ = db.GetAllSpeciesInGenus(genusName)
	}

	// Force GC and get final memory stats
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	// Calculate memory growth
	allocGrowth := m2.TotalAlloc - m1.TotalAlloc
	heapGrowth := int64(m2.HeapAlloc) - int64(m1.HeapAlloc) //nolint:gosec // G115: heap alloc in bytes always fits int64 (max ~8 exabytes)

	t.Logf("After %d iterations:", iterations)
	t.Logf("  Total allocations: %d bytes", allocGrowth)
	t.Logf("  Heap growth: %d bytes", heapGrowth)
	t.Logf("  Mallocs: %d", m2.Mallocs-m1.Mallocs)
	t.Logf("  Frees: %d", m2.Frees-m1.Frees)

	// Heap growth should be minimal (less than 1MB for 10k iterations)
	const maxHeapGrowthMB = 1
	const maxHeapGrowthBytes = maxHeapGrowthMB * 1024 * 1024
	assert.LessOrEqual(t, heapGrowth, int64(maxHeapGrowthBytes),
		"Excessive heap growth: %d bytes (%.2f MB). Possible memory leak.",
		heapGrowth, float64(heapGrowth)/(1024*1024))
}

// TestNilDatabaseReceivers tests methods called on nil database
func TestNilDatabaseReceivers(t *testing.T) {
	t.Parallel()
	t.Attr("component", "birdnet-genus")
	t.Attr("category", "robustness")

	var db *TaxonomyDatabase

	// All methods should return proper errors on nil receiver
	tests := []struct {
		name string
		fn   func() error
	}{
		{
			name: "GetGenusByScientificName",
			fn: func() error {
				_, _, err := db.GetGenusByScientificName("Turdus migratorius")
				return err
			},
		},
		{
			name: "GetAllSpeciesInGenus",
			fn: func() error {
				_, err := db.GetAllSpeciesInGenus("corvus")
				return err
			},
		},
		{
			name: "GetAllSpeciesInFamily",
			fn: func() error {
				_, err := db.GetAllSpeciesInFamily("corvidae")
				return err
			},
		},
		{
			name: "GetSpeciesTree",
			fn: func() error {
				_, err := db.GetSpeciesTree("Turdus migratorius")
				return err
			},
		},
		{
			name: "BuildFamilyTree",
			fn: func() error {
				_, err := db.BuildFamilyTree("Turdus migratorius")
				return err
			},
		},
		{
			name: "GetFamilyInfo",
			fn: func() error {
				_, err := db.GetFamilyInfo("corvidae")
				return err
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.fn()
			assert.Error(t, err, "Expected error for nil database")
		})
	}
}

// BenchmarkConcurrentLookups benchmarks concurrent lookups
func BenchmarkConcurrentLookups(b *testing.B) {
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		species := []string{
			"Turdus migratorius",
			"Corvus corax",
			"Bubo virginianus",
		}
		i := 0
		for pb.Next() {
			scientificName := species[i%len(species)]
			_, _, err := db.GetGenusByScientificName(scientificName)
			require.NoError(b, err, "Lookup failed")
			i++
		}
	})
}

// BenchmarkGetAllSpeciesInGenus benchmarks retrieving all species in a genus
func BenchmarkGetAllSpeciesInGenus(b *testing.B) {
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	b.ReportAllocs()

	for b.Loop() {
		_, err := db.GetAllSpeciesInGenus("turdus")
		require.NoError(b, err, "GetAllSpeciesInGenus failed")
	}
}

// BenchmarkGetAllSpeciesInFamily benchmarks retrieving all species in a family
func BenchmarkGetAllSpeciesInFamily(b *testing.B) {
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	b.ReportAllocs()

	for b.Loop() {
		_, err := db.GetAllSpeciesInFamily("strigidae")
		require.NoError(b, err, "GetAllSpeciesInFamily failed")
	}
}

// BenchmarkSearchGenus benchmarks genus search
func BenchmarkSearchGenus(b *testing.B) {
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	// Validate once before timing
	matches := db.SearchGenus("corv")
	require.NotEmpty(b, matches, "Expected matches")

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = db.SearchGenus("corv")
	}
}

// BenchmarkSearchFamily benchmarks family search
func BenchmarkSearchFamily(b *testing.B) {
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	// Validate once before timing
	matches := db.SearchFamily("strig")
	require.NotEmpty(b, matches, "Expected matches")

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_ = db.SearchFamily("strig")
	}
}

// BenchmarkMemoryFootprint measures memory footprint of loaded database
func BenchmarkMemoryFootprint(b *testing.B) {
	b.ReportAllocs()

	// Force GC before measurement
	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// Load database
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	// Force GC after loading
	runtime.GC()
	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)

	heapIncrease := m2.HeapAlloc - m1.HeapAlloc

	b.ReportMetric(float64(heapIncrease), "heap-bytes")
	b.ReportMetric(float64(heapIncrease)/(1024*1024), "heap-MB")

	// Keep reference to prevent early GC
	_ = db

	b.Logf("Taxonomy database heap footprint: %d bytes (%.2f MB)",
		heapIncrease, float64(heapIncrease)/(1024*1024))
}

// BenchmarkAllocationPattern analyzes allocation patterns for common operations
func BenchmarkAllocationPattern(b *testing.B) {
	db, err := LoadTaxonomyDatabase()
	require.NoError(b, err, "Failed to load database")

	b.Run("SingleLookup", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _, _ = db.GetGenusByScientificName("Turdus migratorius")
		}
	})

	b.Run("TreeBuilding", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = db.GetSpeciesTree("Turdus migratorius")
		}
	})

	b.Run("GenusSpeciesList", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = db.GetAllSpeciesInGenus("corvus")
		}
	})

	b.Run("FamilySpeciesList", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_, _ = db.GetAllSpeciesInFamily("corvidae")
		}
	})

	b.Run("Search", func(b *testing.B) {
		b.ReportAllocs()
		for i := 0; i < b.N; i++ {
			_ = db.SearchGenus("corv")
		}
	})
}
