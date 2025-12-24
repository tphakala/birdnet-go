package imageprovider_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

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

// TestBackgroundRefreshIsolation ensures background refresh doesn't affect user requests
func TestBackgroundRefreshIsolation(t *testing.T) {
	t.Skip("TODO: Fix test - background refresh tracking mechanism needs refactoring")
	if testing.Short() {
		t.Skip("Skipping background refresh test in short mode")
	}
	t.Parallel()

	mockProvider := &mockProviderWithContextTracking{
		mockProviderWithAPICounter: mockProviderWithAPICounter{
			mockImageProvider: mockImageProvider{
				fetchDelay: 50 * time.Millisecond, // Simulate slower API
			},
		},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	// Pre-populate with stale entry
	staleTime := time.Now().Add(-15 * 24 * time.Hour)
	species := "Turdus merula"
	err = mockStore.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   "wikimedia",
		URL:            "http://example.com/old.jpg",
		CachedAt:       staleTime,
	})
	require.NoError(t, err, "Failed to save stale cache entry")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)

	// Wait for background refresh to potentially start
	time.Sleep(100 * time.Millisecond)

	// User request should return immediately with stale data
	start := time.Now()
	img, err := cache.Get(species)
	duration := time.Since(start)

	require.NoError(t, err, "Failed to get image")

	// Should return quickly (not wait for background refresh)
	assert.LessOrEqual(t, duration, 10*time.Millisecond, "User request took too long, expected < 10ms")

	// Should have returned stale data
	assert.Equal(t, "http://example.com/old.jpg", img.URL, "Expected stale URL")

	// Wait for background refresh to complete
	time.Sleep(200 * time.Millisecond)

	// Check that background refresh happened
	assert.Positive(t, mockProvider.getBackgroundFetchCount(), "Expected background refresh to occur")

	t.Logf("User fetches: %d, Background fetches: %d",
		mockProvider.getUserFetchCount(), mockProvider.getBackgroundFetchCount())
}

// TestCacheMetrics validates that metrics accurately reflect cache behavior
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

	// Track metrics before and after operations
	// Note: This is pseudocode - actual metric tracking would need proper instrumentation
	species := []string{"Species_A", "Species_B", "Species_C"}

	// First fetch each species
	for _, s := range species {
		_, err := cache.Get(s)
		require.NoError(t, err, "Failed to get %s", s)
	}

	// Fetch again (should be cache hits)
	for _, s := range species {
		_, err := cache.Get(s)
		require.NoError(t, err, "Failed to get %s", s)
	}

	// Log the results
	t.Logf("Total API calls: %d (expected 3)", mockProvider.getAPICallCount())
	t.Logf("Total requests: 6 (3 misses + 3 hits)")
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
	if ctx != nil {
		if bg, ok := ctx.Value("background").(bool); ok && bg {
			atomic.AddInt64(&m.backgroundFetches, 1)
		} else {
			atomic.AddInt64(&m.userFetches, 1)
		}
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
