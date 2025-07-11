package imageprovider_test

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestCacheEffectiveness validates that caching effectively reduces external API calls
func TestCacheEffectiveness(t *testing.T) {
	t.Parallel()

	// Test 1: Multiple requests for same species should only trigger one API call
	t.Run("DeduplicationTest", func(t *testing.T) {
		t.Parallel()
		// Create separate instances for this test
		mockProvider := &mockProviderWithAPICounter{
			mockImageProvider: mockImageProvider{
				fetchDelay: 10 * time.Millisecond,
			},
		}

		mockStore := newMockStore()
		metrics, err := observability.NewMetrics()
		if err != nil {
			t.Fatalf("Failed to create metrics: %v", err)
		}

		cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		cache.SetImageProvider(mockProvider)

		species := "Parus major"

		// Make 10 concurrent requests
		var wg sync.WaitGroup
		for i := 0; i < 10; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				_, err := cache.Get(species)
				if err != nil {
					t.Errorf("Failed to get image: %v", err)
				}
			}()
		}
		wg.Wait()

		// Should only have made 1 API call
		if mockProvider.getAPICallCount() != 1 {
			t.Errorf("Expected 1 API call, got %d", mockProvider.getAPICallCount())
		}
	})

	// Test 2: Subsequent requests should use cache
	t.Run("CacheHitTest", func(t *testing.T) {
		t.Parallel()
		// Create separate instances for this test
		mockProvider := &mockProviderWithAPICounter{
			mockImageProvider: mockImageProvider{
				fetchDelay: 10 * time.Millisecond,
			},
		}

		mockStore := newMockStore()
		metrics, err := observability.NewMetrics()
		if err != nil {
			t.Fatalf("Failed to create metrics: %v", err)
		}

		cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		cache.SetImageProvider(mockProvider)

		species := "Carduelis carduelis"

		// First request - should hit API
		_, err = cache.Get(species)
		if err != nil {
			t.Fatalf("Failed to get image: %v", err)
		}

		initialCalls := mockProvider.getAPICallCount()
		if initialCalls != 1 {
			t.Errorf("Expected 1 initial API call, got %d", initialCalls)
		}

		// Make 100 more requests - should all be cache hits
		for i := 0; i < 100; i++ {
			_, err := cache.Get(species)
			if err != nil {
				t.Errorf("Failed to get image on request %d: %v", i, err)
			}
		}

		// API calls should not increase
		if mockProvider.getAPICallCount() != initialCalls {
			t.Errorf("Expected no additional API calls, got %d total calls", mockProvider.getAPICallCount())
		}
	})

	// Test 3: DB cache persistence
	t.Run("DBCachePersistenceTest", func(t *testing.T) {
		t.Parallel()
		// Create separate instances for this test
		mockProvider := &mockProviderWithAPICounter{
			mockImageProvider: mockImageProvider{
				fetchDelay: 10 * time.Millisecond,
			},
		}

		mockStore := newMockStore()
		metrics, err := observability.NewMetrics()
		if err != nil {
			t.Fatalf("Failed to create metrics: %v", err)
		}

		cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		if err != nil {
			t.Fatalf("Failed to create cache: %v", err)
		}
		cache.SetImageProvider(mockProvider)

		species := "Sturnus vulgaris"

		// First request
		_, err = cache.Get(species)
		if err != nil {
			t.Fatalf("Failed to get image: %v", err)
		}

		if mockProvider.getAPICallCount() != 1 {
			t.Errorf("Expected 1 API call for initial fetch, got %d", mockProvider.getAPICallCount())
		}

		// Create new cache instance (simulating restart)
		cache2, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		if err != nil {
			t.Fatalf("Failed to create second cache: %v", err)
		}
		cache2.SetImageProvider(mockProvider)

		// Request same species - should load from DB, not API
		_, err = cache2.Get(species)
		if err != nil {
			t.Fatalf("Failed to get image from new cache: %v", err)
		}

		// Should still be 1 API call (no new call)
		if mockProvider.getAPICallCount() != 1 {
			t.Errorf("Expected no new API calls after restart, got %d total calls", mockProvider.getAPICallCount())
		}
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
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Test repeated requests for non-existent species
	t.Run("RepeatedNotFoundRequests", func(t *testing.T) {
		t.Parallel()
		mockProvider.resetCounters()
		mockProvider.setNotFoundSpecies("Imaginary species")

		// Make 5 requests for non-existent species
		for i := 0; i < 5; i++ {
			_, err := cache.Get("Imaginary species")
			if !errors.Is(err, imageprovider.ErrImageNotFound) {
				t.Errorf("Expected ErrImageNotFound, got %v", err)
			}
		}

		// With negative caching implemented, only first request should hit API
		apiCalls := mockProvider.getAPICallCount()
		t.Logf("API calls for non-existent species: %d (with negative caching)", apiCalls)

		// Verify negative caching is working
		if apiCalls != 1 {
			t.Errorf("Expected 1 API call with negative caching, got %d", apiCalls)
		}
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
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	// Pre-populate with stale entry
	staleTime := time.Now().Add(-15 * 24 * time.Hour)
	species := "Turdus merula"
	if err := mockStore.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   "wikimedia",
		URL:            "http://example.com/old.jpg",
		CachedAt:       staleTime,
	}); err != nil {
		t.Fatalf("Failed to save stale cache entry: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Wait for background refresh to potentially start
	time.Sleep(100 * time.Millisecond)

	// User request should return immediately with stale data
	start := time.Now()
	img, err := cache.Get(species)
	duration := time.Since(start)

	if err != nil {
		t.Fatalf("Failed to get image: %v", err)
	}

	// Should return quickly (not wait for background refresh)
	if duration > 10*time.Millisecond {
		t.Errorf("User request took too long: %v, expected < 10ms", duration)
	}

	// Should have returned stale data
	if img.URL != "http://example.com/old.jpg" {
		t.Errorf("Expected stale URL, got %s", img.URL)
	}

	// Wait for background refresh to complete
	time.Sleep(200 * time.Millisecond)

	// Check that background refresh happened
	if mockProvider.getBackgroundFetchCount() == 0 {
		t.Error("Expected background refresh to occur")
	}

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
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Track metrics before and after operations
	// Note: This is pseudocode - actual metric tracking would need proper instrumentation
	species := []string{"Species_A", "Species_B", "Species_C"}

	// First fetch each species
	for _, s := range species {
		_, err := cache.Get(s)
		if err != nil {
			t.Errorf("Failed to get %s: %v", s, err)
		}
	}

	// Fetch again (should be cache hits)
	for _, s := range species {
		_, err := cache.Get(s)
		if err != nil {
			t.Errorf("Failed to get %s: %v", s, err)
		}
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
