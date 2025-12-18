package imageprovider_test

import (
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// Benchmark scenarios:
// 1. Cache hit performance - measuring in-memory lookup speed
// 2. Cache miss with DB lookup - measuring DB fetch overhead
// 3. Cache miss with provider fetch - measuring full fetch cycle
// 4. Concurrent access patterns - measuring contention/locking overhead
// 5. Rate limiting impact - measuring how rate limiting affects throughput
// 6. Batch operations - measuring GetBatch performance

// benchmarkCacheSetup creates a cache for benchmarking with proper cleanup.
func benchmarkCacheSetup(b *testing.B, provider imageprovider.ImageProvider, store datastore.Interface) *imageprovider.BirdImageCache {
	b.Helper()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	cache.SetImageProvider(provider)

	b.Cleanup(func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	})

	return cache
}

// benchmarkPrePopulateCache pre-populates the cache with species for benchmarking.
func benchmarkPrePopulateCache(b *testing.B, cache *imageprovider.BirdImageCache, species []string) {
	b.Helper()
	for _, s := range species {
		if _, err := cache.Get(s); err != nil {
			b.Fatalf("Failed to pre-populate cache entry: %v", err)
		}
	}
}

// benchmarkSequentialGet runs sequential Get operations.
func benchmarkSequentialGet(b *testing.B, cache *imageprovider.BirdImageCache, species []string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	i := 0
	for b.Loop() {
		if _, err := cache.Get(species[i%len(species)]); err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		i++
	}
}

// benchmarkConcurrentGet runs concurrent Get operations.
func benchmarkConcurrentGet(b *testing.B, cache *imageprovider.BirdImageCache, species []string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if _, err := cache.Get(species[i%len(species)]); err != nil {
				b.Error("Unexpected error:", err)
				return
			}
			i++
		}
	})
}

// BenchmarkCacheHit measures the performance of cache hits (best case scenario)
func BenchmarkCacheHit(b *testing.B) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	cache := benchmarkCacheSetup(b, mockProvider, mockStore)

	// Pre-populate cache
	if _, err := cache.Get("Turdus merula"); err != nil {
		b.Fatalf("Failed to pre-populate cache: %v", err)
	}

	b.ReportAllocs()
	b.ResetTimer()

	for b.Loop() {
		_, err := cache.Get("Turdus merula")
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
	}
}

// BenchmarkCacheMissWithDBHit measures performance when item is in DB but not memory
func BenchmarkCacheMissWithDBHit(b *testing.B) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	// Pre-populate DB store
	for i := range 100 {
		species := fmt.Sprintf("Species_%d", i)
		if err := mockStore.SaveImageCache(&datastore.ImageCache{
			ScientificName: species,
			ProviderName:   "wikimedia",
			URL:            fmt.Sprintf("http://example.com/%s.jpg", species),
			CachedAt:       time.Now(),
		}); err != nil {
			b.Fatalf("Failed to pre-populate DB store: %v", err)
		}
	}

	// Create new cache without pre-loading memory
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()
	cache.SetImageProvider(mockProvider)

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		species := fmt.Sprintf("Species_%d", i%100)
		_, err := cache.Get(species)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		i++
	}
}

// BenchmarkCacheMissWithProviderFetch measures full fetch cycle performance
func BenchmarkCacheMissWithProviderFetch(b *testing.B) {
	mockProvider := &mockImageProvider{
		fetchDelay: 10 * time.Millisecond, // Simulate network latency
	}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()
	cache.SetImageProvider(mockProvider)

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		species := fmt.Sprintf("Species_unique_%d", i)
		_, err := cache.Get(species)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		i++
	}
}

// BenchmarkConcurrentCacheAccess measures performance under concurrent load
func BenchmarkConcurrentCacheAccess(b *testing.B) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()
	cache.SetImageProvider(mockProvider)

	// Pre-populate some cache entries
	species := []string{"Turdus merula", "Parus major", "Carduelis carduelis", "Sturnus vulgaris"}
	for _, s := range species {
		if _, err := cache.Get(s); err != nil {
			b.Fatalf("Failed to pre-populate cache entry: %v", err)
		}
	}

	b.ReportAllocs()
	b.ResetTimer()

	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			s := species[i%len(species)]
			_, err := cache.Get(s)
			if err != nil {
				b.Error("Unexpected error:", err)
				return
			}
			i++
		}
	})
}

// BenchmarkRateLimitedFetch measures the impact of rate limiting on fetch operations
func BenchmarkRateLimitedFetch(b *testing.B) {
	// This benchmark will use the actual Wikipedia provider to test rate limiting
	provider, err := imageprovider.NewWikiMediaProvider()
	if err != nil {
		b.Fatalf("Failed to create WikiMedia provider: %v", err)
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	cache := imageprovider.InitCache("wikimedia", provider, metrics, mockStore)
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()

	// Test species that are likely to exist in Wikipedia
	testSpecies := []string{
		"Turdus merula",
		"Parus major",
		"Carduelis carduelis",
		"Sturnus vulgaris",
		"Erithacus rubecula",
	}

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		species := testSpecies[i%len(testSpecies)]
		// Force direct fetch to test rate limiting
		_, err := provider.Fetch(species)
		if err != nil && !errors.Is(err, imageprovider.ErrImageNotFound) {
			b.Logf("Warning: fetch error for %s: %v", species, err)
		}
		i++
	}
}

// BenchmarkGetBatch measures batch fetch performance
func BenchmarkGetBatch(b *testing.B) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	cache := benchmarkCacheSetup(b, mockProvider, mockStore)

	batchSizes := []int{10, 50, 100}
	for _, size := range batchSizes {
		b.Run(fmt.Sprintf("BatchSize_%d", size), func(b *testing.B) {
			species := generateSpeciesNames(size, "Species")
			benchmarkPrePopulateCache(b, cache, species[:size/2])

			b.ReportAllocs()
			b.ResetTimer()

			for b.Loop() {
				results := cache.GetBatch(species)
				if len(results) != size {
					b.Fatalf("Expected %d results, got %d", size, len(results))
				}
			}
		})
	}
}

// generateSpeciesNames creates a slice of species names with the given prefix.
func generateSpeciesNames(count int, prefix string) []string {
	species := make([]string, count)
	for i := range count {
		species[i] = fmt.Sprintf("%s_%d", prefix, i)
	}
	return species
}

// BenchmarkMemoryUsage measures the memory overhead of the cache
func BenchmarkMemoryUsage(b *testing.B) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()
	cache.SetImageProvider(mockProvider)

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		// Add unique entries to measure memory growth
		species := fmt.Sprintf("Species_mem_%d", i)
		_, err := cache.Get(species)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}

		// Periodically check memory usage
		if i%100 == 0 {
			usage := cache.MemoryUsage()
			b.Logf("Memory usage after %d entries: %d bytes", i, usage)
		}
		i++
	}
}

// BenchmarkCacheRefreshCycle measures the performance impact of background refresh
func BenchmarkCacheRefreshCycle(b *testing.B) {
	mockProvider := &mockImageProvider{
		fetchDelay: 5 * time.Millisecond,
	}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	// Add stale entries to trigger refresh
	staleTime := time.Now().Add(-15 * 24 * time.Hour)
	for i := range 50 {
		species := fmt.Sprintf("StaleSpecies_%d", i)
		if err := mockStore.SaveImageCache(&datastore.ImageCache{
			ScientificName: species,
			ProviderName:   "wikimedia",
			URL:            fmt.Sprintf("http://example.com/old_%s.jpg", species),
			CachedAt:       staleTime,
		}); err != nil {
			b.Fatalf("Failed to save stale cache entry: %v", err)
		}
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()
	cache.SetImageProvider(mockProvider)

	// Let refresh cycle run
	time.Sleep(2 * time.Second)

	b.ReportAllocs()

	// Benchmark cache access during refresh
	i := 0
	for b.Loop() {
		species := fmt.Sprintf("StaleSpecies_%d", i%50)
		_, err := cache.Get(species)
		if err != nil {
			b.Fatalf("Unexpected error: %v", err)
		}
		i++
	}
}

// BenchmarkProviderAccess measures the performance of provider access patterns
func BenchmarkProviderAccess(b *testing.B) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	cache := benchmarkCacheSetup(b, mockProvider, mockStore)

	species := []string{"Turdus merula", "Parus major", "Carduelis carduelis"}
	benchmarkPrePopulateCache(b, cache, species)

	b.Run("Sequential", func(b *testing.B) {
		benchmarkSequentialGet(b, cache, species)
	})

	b.Run("Concurrent", func(b *testing.B) {
		benchmarkConcurrentGet(b, cache, species)
	})

	b.Run("MixedReadWrite", func(b *testing.B) {
		benchmarkMixedReadWrite(b, cache, mockProvider, species)
	})
}

// benchmarkMixedReadWrite runs mixed read/write operations.
func benchmarkMixedReadWrite(b *testing.B, cache *imageprovider.BirdImageCache, provider imageprovider.ImageProvider, species []string) {
	b.Helper()
	b.ReportAllocs()
	b.ResetTimer()
	b.RunParallel(func(pb *testing.PB) {
		i := 0
		for pb.Next() {
			if i%100 == 0 {
				cache.SetImageProvider(provider)
			} else {
				if _, err := cache.Get(species[i%len(species)]); err != nil {
					b.Error("Unexpected error:", err)
					return
				}
			}
			i++
		}
	})
}

// BenchmarkConcurrentInitialization measures performance when multiple goroutines
// try to initialize the same cache entry simultaneously
func BenchmarkConcurrentInitialization(b *testing.B) {
	mockProvider := &mockImageProvider{
		fetchDelay: 50 * time.Millisecond, // Significant delay to test concurrent behavior
	}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		b.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		b.Fatalf("Failed to create cache: %v", err)
	}
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()
	cache.SetImageProvider(mockProvider)

	b.ReportAllocs()

	i := 0
	for b.Loop() {
		species := fmt.Sprintf("ConcurrentSpecies_%d", i)

		// Launch multiple goroutines trying to get the same species
		var wg sync.WaitGroup
		const numGoroutines = 10
		wg.Add(numGoroutines)

		start := time.Now()
		for range numGoroutines {
			go func() {
				defer wg.Done()
				_, err := cache.Get(species)
				if err != nil {
					b.Errorf("Unexpected error: %v", err)
				}
			}()
		}
		wg.Wait()

		elapsed := time.Since(start)
		i++
		// All goroutines should complete in roughly the time of one fetch
		if elapsed > mockProvider.fetchDelay*2 {
			b.Logf("Warning: concurrent fetch took %v, expected ~%v", elapsed, mockProvider.fetchDelay)
		}
	}
}
