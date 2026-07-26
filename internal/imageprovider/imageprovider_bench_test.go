package imageprovider_test

import (
	"fmt"
	"os"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// benchLiveNetworkEnv gates benchmarks that issue real requests to third-party APIs.
const benchLiveNetworkEnv = "BIRDNET_BENCH_LIVE"

// Benchmark scenarios:
// 1. Cache hit performance - measuring in-memory lookup speed
// 2. Cache miss with DB lookup - measuring DB fetch overhead
// 3. Cache miss with provider fetch - measuring full fetch cycle
// 4. Concurrent access patterns - measuring contention/locking overhead
// 5. Rate limiting impact - measuring how rate limiting affects throughput

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
			URL:            fmt.Sprintf("http://127.0.0.1/%s.jpg", species),
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

// BenchmarkRateLimitedFetch measures the impact of rate limiting on fetch operations.
//
// This benchmark issues real requests to the live Wikipedia API, so it is opt-in: an
// unguarded `go test -bench=.` would otherwise hammer a third-party service from CI
// and produce numbers that depend on someone else's rate limiting. Run it with
// BIRDNET_BENCH_LIVE=1 when measuring the limiter deliberately.
func BenchmarkRateLimitedFetch(b *testing.B) {
	// ParseBool, not a non-empty check: BIRDNET_BENCH_LIVE=0 is how anyone would
	// disable a flag, and an emptiness test would have enabled it instead.
	if live, err := strconv.ParseBool(os.Getenv(benchLiveNetworkEnv)); err != nil || !live {
		b.Skipf("skipping live-network benchmark; set %s=1 to run it", benchLiveNetworkEnv)
	}

	// This benchmark will use the actual Wikipedia provider to test rate limiting
	provider, err := imageprovider.NewWikiMediaProvider()
	if err != nil {
		b.Fatalf("Failed to create WikiMedia provider: %v", err)
	}

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
	staleTime := time.Now().Add(-31 * 24 * time.Hour)
	for i := range 50 {
		species := fmt.Sprintf("StaleSpecies_%d", i)
		if err := mockStore.SaveImageCache(&datastore.ImageCache{
			ScientificName: species,
			ProviderName:   "wikimedia",
			URL:            fmt.Sprintf("http://127.0.0.1/old_%s.jpg", species),
			CachedAt:       staleTime,
		}); err != nil {
			b.Fatalf("Failed to save stale cache entry: %v", err)
		}
	}

	// InitCache with the mock already installed, not CreateDefaultCache followed
	// by SetImageProvider: the refresh goroutine starts immediately, and if it
	// reaches shouldSkipRefresh while the lazy Wikipedia provider is still in
	// place it returns and does not run again for an hour, so the poll below
	// would never see a fetch. Same reason TestBirdImageCacheRefresh gives.
	cache := imageprovider.InitCache("wikimedia", mockProvider, metrics, mockStore)
	defer func() {
		if err := cache.Close(); err != nil {
			b.Errorf("Failed to close cache: %v", err)
		}
	}()

	// Wait until the refresh cycle has actually started working rather than
	// sleeping for a duration guessed from refreshDelay. The sweep waits
	// refreshDelay (2s) before each entry, so the first fetch lands just past
	// that; poll for it with headroom instead of racing the boundary.
	require.Eventually(b, func() bool { return mockProvider.getFetchCount() > 0 },
		10*time.Second, 50*time.Millisecond,
		"the background refresh sweep should have started fetching stale entries")

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
