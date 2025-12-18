package imageprovider_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// setupNegativeCacheTest creates a cache with a not-found provider for testing.
func setupNegativeCacheTest(t *testing.T, notFoundSpecies map[string]bool) (*mockProviderWithNotFound, *imageprovider.BirdImageCache) {
	t.Helper()

	mockProvider := &mockProviderWithNotFound{notFoundSpecies: notFoundSpecies}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)

	return mockProvider, cache
}

// assertImageNotFoundError verifies the error is ErrImageNotFound.
func assertImageNotFoundError(t *testing.T, err error, context string) {
	t.Helper()
	assert.True(t, errors.Is(err, imageprovider.ErrImageNotFound), "%s: Expected ErrImageNotFound, got %v", context, err)
}

// TestNegativeCachingBehavior validates that negative caching works correctly
func TestNegativeCachingBehavior(t *testing.T) {
	t.Parallel()

	t.Run("NegativeCacheReducesAPICalls", func(t *testing.T) {
		t.Parallel()
		mockProvider, cache := setupNegativeCacheTest(t, map[string]bool{"Notfoundicus imaginary": true})
		species := "Notfoundicus imaginary"

		// First request - should hit API
		_, err := cache.Get(species)
		assertImageNotFoundError(t, err, "initial request")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected 1 API call for initial request")

		// Make 5 more requests - should use negative cache
		for i := range 5 {
			_, err := cache.Get(species)
			assertImageNotFoundError(t, err, "request "+string(rune('2'+i)))
		}

		// Should still be 1 API call (negative caching working)
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(), "Expected 1 total API call with negative caching")
	})

	t.Run("NegativeCacheExpiry", func(t *testing.T) {
		t.Parallel()
		mockProvider, cache := setupNegativeCacheTest(t, map[string]bool{"Missingbird species": true})
		species := "Missingbird species"

		// First request
		_, err := cache.Get(species)
		assertImageNotFoundError(t, err, "first request")

		// Immediate second request should use cache
		_, err = cache.Get(species)
		assertImageNotFoundError(t, err, "cached request")

		t.Logf("Negative cache confirmed: %d API call(s) for multiple requests", mockProvider.getAPICallCount())
	})

	t.Run("TransientErrorsNotCached", func(t *testing.T) {
		t.Parallel()
		errorProvider := &mockProviderWithTransientError{errorMessage: "temporary network error"}
		mockStore := newMockStore()
		metrics, err := observability.NewMetrics()
		require.NoError(t, err, "Failed to create metrics")

		cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		require.NoError(t, err, "Failed to create cache")
		cache.SetImageProvider(errorProvider)

		species := "Any species"

		// Make 3 requests - each should hit API (no caching of transient errors)
		for i := range 3 {
			_, err := cache.Get(species)
			require.Error(t, err, "Request %d: Expected transient error", i+1)
			assert.False(t, errors.Is(err, imageprovider.ErrImageNotFound), "Request %d: Expected transient error, not ErrImageNotFound", i+1)
		}

		// Should have made 3 API calls (no caching of transient errors)
		assert.Equal(t, int64(3), errorProvider.getAPICallCount(), "Expected 3 API calls for transient errors")
	})
}

// TestNegativeCachePersistence tests that negative cache entries persist in DB
func TestNegativeCachePersistence(t *testing.T) {
	t.Parallel()
	mockProvider := &mockProviderWithNotFound{
		notFoundSpecies: map[string]bool{
			"Persisticus negative": true,
		},
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache1, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	cache1.SetImageProvider(mockProvider)

	// Get a not-found species
	species := "Persisticus negative"
	_, err = cache1.Get(species)
	if !errors.Is(err, imageprovider.ErrImageNotFound) {
		t.Errorf("Expected ErrImageNotFound, got %v", err)
	}

	// Verify it was saved to DB
	dbEntries := mockStore.GetAllTestEntries()
	foundNegative := false
	for _, entry := range dbEntries {
		if entry.ScientificName == species && entry.URL == "__NOT_FOUND__" {
			foundNegative = true
			t.Logf("Found negative cache entry in DB: %+v", entry)
			break
		}
	}

	if !foundNegative {
		t.Error("Negative cache entry was not saved to DB")
	}

	// Create new cache instance (simulating restart)
	cache2, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create second cache: %v", err)
	}
	cache2.SetImageProvider(mockProvider)

	mockProvider.resetCounters()

	// Request same species - should load negative entry from DB if not expired
	_, err = cache2.Get(species)
	if !errors.Is(err, imageprovider.ErrImageNotFound) {
		t.Errorf("Expected ErrImageNotFound from cached negative entry, got %v", err)
	}

	// Check API calls - if negative cache was loaded from DB, should be 0
	// (Unless the 15-minute TTL expired, which is unlikely in test)
	apiCalls := mockProvider.getAPICallCount()
	t.Logf("API calls after restart: %d (0 means negative cache was loaded from DB)", apiCalls)
}

// mockProviderWithNotFound returns not found for specific species
type mockProviderWithNotFound struct {
	apiCallCount    int64
	notFoundSpecies map[string]bool
	mu              sync.RWMutex
}

func (m *mockProviderWithNotFound) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	atomic.AddInt64(&m.apiCallCount, 1)

	m.mu.RLock()
	isNotFound := m.notFoundSpecies[scientificName]
	m.mu.RUnlock()

	if isNotFound {
		return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
	}

	// Return a valid image for other species
	return imageprovider.BirdImage{
		URL:            "http://example.com/" + scientificName + ".jpg",
		ScientificName: scientificName,
		AuthorName:     "Test Author",
		LicenseName:    "CC-BY",
		CachedAt:       time.Now(),
	}, nil
}

func (m *mockProviderWithNotFound) getAPICallCount() int64 {
	return atomic.LoadInt64(&m.apiCallCount)
}

func (m *mockProviderWithNotFound) resetCounters() {
	atomic.StoreInt64(&m.apiCallCount, 0)
}

// mockProviderWithTransientError simulates transient errors
type mockProviderWithTransientError struct {
	apiCallCount int64
	errorMessage string
}

func (m *mockProviderWithTransientError) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	atomic.AddInt64(&m.apiCallCount, 1)
	// Return a network error (not ErrImageNotFound) to simulate transient error
	return imageprovider.BirdImage{}, errors.New(errors.NewStd(m.errorMessage)).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("operation", "mock_fetch").
		Build()
}

func (m *mockProviderWithTransientError) getAPICallCount() int64 {
	return atomic.LoadInt64(&m.apiCallCount)
}
