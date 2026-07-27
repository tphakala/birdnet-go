package imageprovider_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// setupTestCache creates a new cache instance with mock provider for testing
func setupTestCache(t *testing.T) (*mockProviderWithAPICounter, *imageprovider.BirdImageCache) {
	t.Helper()

	mockProvider := &mockProviderWithAPICounter{
		mockImageProvider: mockImageProvider{
			fetchDelay: 10 * time.Millisecond,
		},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	return mockProvider, cache
}

// setupTestCacheWithSharedStore creates a new cache instance with shared store for testing
func setupTestCacheWithSharedStore(t *testing.T) (*mockProviderWithAPICounter, *imageprovider.BirdImageCache, datastore.Interface, *observability.Metrics) {
	t.Helper()

	mockProvider := &mockProviderWithAPICounter{
		mockImageProvider: mockImageProvider{
			fetchDelay: 10 * time.Millisecond,
		},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	return mockProvider, cache, mockStore, metrics
}

// runConcurrentGets runs concurrent Get requests and returns any errors.
func runConcurrentGets(cache *imageprovider.BirdImageCache, species string, count int) []error {
	var wg sync.WaitGroup
	var mu sync.Mutex
	var errs []error

	for range count {
		wg.Go(func() {
			if _, err := cache.Get(species); err != nil {
				mu.Lock()
				errs = append(errs, err)
				mu.Unlock()
			}
		})
	}
	wg.Wait()
	return errs
}

// TestCacheEffectiveness validates that caching effectively reduces external API calls
func TestCacheEffectiveness(t *testing.T) {
	t.Parallel()

	t.Run("DeduplicationTest", func(t *testing.T) {
		t.Parallel()
		mockProvider, cache := setupTestCache(t)
		errs := runConcurrentGets(cache, "Parus major", 10)
		assert.Empty(t, errs, "Expected no errors from concurrent requests")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected 1 API call for concurrent requests")
	})

	t.Run("CacheHitTest", func(t *testing.T) {
		t.Parallel()
		mockProvider, cache := setupTestCache(t)
		species := "Carduelis carduelis"

		_, err := cache.Get(species)
		require.NoError(t, err, "Failed to get image")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected 1 initial API call")

		for range 100 {
			_, err := cache.Get(species)
			require.NoError(t, err)
		}
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected no additional API calls")
	})

	t.Run("DBCachePersistenceTest", func(t *testing.T) {
		t.Parallel()
		mockProvider, cache, mockStore, metrics := setupTestCacheWithSharedStore(t)
		species := "Sturnus vulgaris"

		_, err := cache.Get(species)
		require.NoError(t, err, "Failed to get image")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected 1 API call for initial fetch")

		cache2, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		require.NoError(t, err, "Failed to create second cache")
		cache2.SetImageProvider(mockProvider)
		t.Cleanup(func() {
			assert.NoError(t, cache2.Close(), "Failed to close second cache")
		})

		_, err = cache2.Get(species)
		require.NoError(t, err, "Failed to get image from new cache")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected no new API calls after restart")
	})
}

// TestNegativeCaching validates behavior for non-existent species
func TestNegativeCaching(t *testing.T) {
	t.Parallel()
	mockProvider := &mockProviderWithAPICounter{
		mockImageProvider: mockImageProvider{
			shouldFail: false, // Will return not found for specific species
		},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Test repeated requests for non-existent species
	t.Run("RepeatedNotFoundRequests", func(t *testing.T) {
		t.Parallel()
		mockProvider.resetCounters()
		mockProvider.setNotFoundSpecies("Imaginary species")

		// Make 5 requests for non-existent species
		for range 5 {
			_, err := cache.Get("Imaginary species")
			require.ErrorIs(t, err, imageprovider.ErrImageNotFound, "Expected ErrImageNotFound")
		}

		// With negative caching implemented, only first request should hit API
		apiCalls := mockProvider.getAPICallCount()
		t.Logf("API calls for non-existent species: %d (with negative caching)", apiCalls)

		// Verify negative caching is working
		assert.Equal(t, int64(1), apiCalls, "Expected 1 API call with negative caching")
	})
}

// TestBackgroundRefreshIsolation ensures background refresh doesn't affect user requests.
//
// This test was permanently skipped for two independent reasons: an unconditional
// t.Skip, and a mock that read the background-operation context key as an untyped
// string, so getBackgroundFetchCount() was always zero. Both are fixed.
func TestBackgroundRefreshIsolation(t *testing.T) {
	t.Parallel()

	// providerFetchDelay must be comfortably larger than the latency budget below so
	// "returned without waiting for the provider" is a real discrimination and not a
	// timing coincidence on a loaded CI runner.
	const (
		providerFetchDelay = 400 * time.Millisecond
		userLatencyBudget  = 150 * time.Millisecond
	)

	mockProvider := &mockProviderWithContextTracking{
		mockProviderWithAPICounter: mockProviderWithAPICounter{
			mockImageProvider: mockImageProvider{
				fetchDelay: providerFetchDelay, // Simulate slow API
			},
		},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	// Pre-populate with stale entry
	staleTime := time.Now().Add(-31 * 24 * time.Hour)
	species := "Turdus merula"
	err = mockStore.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   "wikimedia",
		URL:            "http://127.0.0.1/old.jpg",
		CachedAt:       staleTime,
	})
	require.NoError(t, err, "Failed to save stale cache entry")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// User request must return stale data without blocking on the in-flight refresh.
	start := time.Now()
	img, err := cache.Get(species)
	duration := time.Since(start)

	require.NoError(t, err, "Failed to get image")
	assert.Less(t, duration, userLatencyBudget,
		"user request blocked on the background provider fetch (%s delay)", providerFetchDelay)
	assert.Equal(t, "http://127.0.0.1/old.jpg", img.URL, "Expected stale URL")

	// Background refresh must actually run. Poll rather than sleep a fixed interval.
	assert.Eventually(t, func() bool {
		return mockProvider.getBackgroundFetchCount() > 0
	}, backgroundFetchWaitTimeout, 20*time.Millisecond, "Expected background refresh to occur")

	t.Logf("User fetches: %d, Background fetches: %d",
		mockProvider.getUserFetchCount(), mockProvider.getBackgroundFetchCount())
}

// TestCacheMetrics validates that metrics accurately reflect cache behavior.
//
// The previous version was explicitly labelled pseudocode and asserted nothing about
// metrics at all; it only logged counts. Prometheus counters are read directly here
// via testutil.ToFloat64.
func TestCacheMetrics(t *testing.T) {
	t.Parallel()
	mockProvider := &mockProviderWithAPICounter{
		mockImageProvider: mockImageProvider{},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	species := []string{"Species_A", "Species_B", "Species_C"}

	hitsBefore := testutil.ToFloat64(metrics.ImageProvider.CacheHits)
	missesBefore := testutil.ToFloat64(metrics.ImageProvider.CacheMisses)

	// First fetch of each species: all misses, each reaching the provider.
	for _, s := range species {
		_, err := cache.Get(s)
		require.NoError(t, err, "Failed to get %s", s)
	}

	missesAfterFirstPass := testutil.ToFloat64(metrics.ImageProvider.CacheMisses)
	assert.InDelta(t, float64(len(species)), missesAfterFirstPass-missesBefore, 0.001,
		"expected one cache miss per species on first fetch")
	assert.Equal(t, int64(len(species)), mockProvider.getAPICallCount(),
		"expected one provider call per species on first fetch")

	// Second fetch of each species: all served from the in-memory cache.
	for _, s := range species {
		_, err := cache.Get(s)
		require.NoError(t, err, "Failed to get %s", s)
	}

	assert.InDelta(t, float64(len(species)), testutil.ToFloat64(metrics.ImageProvider.CacheHits)-hitsBefore, 0.001,
		"expected one cache hit per species on second fetch")
	assert.InDelta(t, missesAfterFirstPass, testutil.ToFloat64(metrics.ImageProvider.CacheMisses), 0.001,
		"second fetch must not record additional cache misses")
	assert.Equal(t, int64(len(species)), mockProvider.getAPICallCount(),
		"second fetch must not reach the provider")
}

// mockProviderWithAPICounter tracks API calls
type mockProviderWithAPICounter struct {
	mockImageProvider
	apiCallCount    int64
	notFoundSpecies map[string]bool
	mu2             sync.RWMutex
}

func (m *mockProviderWithAPICounter) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	atomic.AddInt64(&m.apiCallCount, 1)

	m.mu2.RLock()
	if m.notFoundSpecies != nil && m.notFoundSpecies[scientificName] {
		m.mu2.RUnlock()
		return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
	}
	m.mu2.RUnlock()

	return m.mockImageProvider.Fetch(scientificName)
}

func (m *mockProviderWithAPICounter) getAPICallCount() int64 {
	return atomic.LoadInt64(&m.apiCallCount)
}

func (m *mockProviderWithAPICounter) resetCounters() {
	atomic.StoreInt64(&m.apiCallCount, 0)
}

func (m *mockProviderWithAPICounter) setNotFoundSpecies(species string) {
	m.mu2.Lock()
	if m.notFoundSpecies == nil {
		m.notFoundSpecies = make(map[string]bool)
	}
	m.notFoundSpecies[species] = true
	m.mu2.Unlock()
}

// mockProviderWithContextTracking tracks background vs user fetches
type mockProviderWithContextTracking struct {
	mockProviderWithAPICounter
	backgroundFetches int64
	userFetches       int64
}

func (m *mockProviderWithContextTracking) FetchWithContext(ctx context.Context, scientificName string) (imageprovider.BirdImage, error) {
	// Track whether this is a background fetch
	if imageprovider.IsBackgroundContext(ctx) {
		atomic.AddInt64(&m.backgroundFetches, 1)
	} else {
		atomic.AddInt64(&m.userFetches, 1)
	}

	return m.Fetch(scientificName)
}

func (m *mockProviderWithContextTracking) getBackgroundFetchCount() int64 {
	return atomic.LoadInt64(&m.backgroundFetches)
}

func (m *mockProviderWithContextTracking) getUserFetchCount() int64 {
	return atomic.LoadInt64(&m.userFetches)
}
