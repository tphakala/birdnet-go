package imageprovider_test

import (
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
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
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	return mockProvider, cache
}

// assertImageNotFoundError verifies the error is ErrImageNotFound.
func assertImageNotFoundError(t *testing.T, err error, context string) {
	t.Helper()
	require.ErrorIs(t, err, imageprovider.ErrImageNotFound, "%s: Expected ErrImageNotFound", context)
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
		// A negative entry older than the negative TTL must be re-queried rather than
		// served from cache.
		//
		// The seed age below is a reviewed literal, deliberately NOT derived from
		// NegativeCacheTTL. Deriving it (e.g. -2 * NegativeCacheTTL) makes the test
		// self-referential: raising the constant moves the seed with it and the test
		// keeps passing, so it cannot detect a wrong TTL. The companion assertion in
		// TestNegativeCacheTTLValue pins the constant itself.
		const (
			species         = "Expiredbird species"
			expiredEntryAge = 30 * time.Minute
		)

		mockProvider := &mockProviderWithNotFound{notFoundSpecies: map[string]bool{species: true}}
		mockStore := newMockStore()
		metrics, err := observability.NewMetrics()
		require.NoError(t, err, "Failed to create metrics")

		// Seed an already-expired negative entry, as a restart would load from the DB.
		require.NoError(t, mockStore.SaveImageCache(&datastore.ImageCache{
			ScientificName: species,
			ProviderName:   "wikimedia",
			URL:            imageprovider.NegativeEntryMarker,
			CachedAt:       time.Now().Add(-expiredEntryAge),
		}), "Failed to seed expired negative cache entry")

		cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
		require.NoError(t, err, "Failed to create cache")
		cache.SetImageProvider(mockProvider)
		t.Cleanup(func() { assert.NoError(t, cache.Close(), "Failed to close cache") })

		// The expired negative entry must not be served: the provider is consulted again.
		_, err = cache.Get(species)
		assertImageNotFoundError(t, err, "request against expired negative entry")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(),
			"expired negative entry must be re-queried, not served from cache")

		// The fresh negative entry written by that lookup is now honoured.
		_, err = cache.Get(species)
		assertImageNotFoundError(t, err, "request against refreshed negative entry")
		assert.Equal(t, int64(1), mockProvider.getAPICallCount(),
			"fresh negative entry must be served from cache without a second provider call")
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
		t.Cleanup(func() { assert.NoError(t, cache.Close(), "Failed to close cache") })

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
	require.NoError(t, err, "Failed to create metrics")

	cache1, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create cache")
	cache1.SetImageProvider(mockProvider)
	t.Cleanup(func() { assert.NoError(t, cache1.Close(), "Failed to close first cache") })

	// Get a not-found species
	species := "Persisticus negative"
	_, err = cache1.Get(species)
	require.ErrorIs(t, err, imageprovider.ErrImageNotFound, "Expected ErrImageNotFound")

	// Verify it was saved to DB
	dbEntries := mockStore.GetAllTestEntries()
	foundNegative := false
	for _, entry := range dbEntries {
		if entry.ScientificName == species && entry.URL == imageprovider.NegativeEntryMarker {
			foundNegative = true
			t.Logf("Found negative cache entry in DB: %+v", entry)
			break
		}
	}

	assert.True(t, foundNegative, "Negative cache entry was not saved to DB")

	// Create new cache instance (simulating restart)
	cache2, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create second cache")
	cache2.SetImageProvider(mockProvider)
	t.Cleanup(func() { assert.NoError(t, cache2.Close(), "Failed to close second cache") })

	mockProvider.resetCounters()

	// Request same species - should load negative entry from DB if not expired
	_, err = cache2.Get(species)
	require.ErrorIs(t, err, imageprovider.ErrImageNotFound, "Expected ErrImageNotFound from cached negative entry")

	// The negative entry was written moments ago, so it is well inside the negative
	// TTL and the fresh cache must serve it from the DB without touching the provider.
	assert.Equal(t, int64(0), mockProvider.getAPICallCount(),
		"negative cache entry was not loaded from the DB after restart")
}

// mockProviderWithNotFound returns not found for specific species
type mockProviderWithNotFound struct {
	apiCallCount    atomic.Int64
	notFoundSpecies map[string]bool
	mu              sync.RWMutex
}

func (m *mockProviderWithNotFound) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	m.apiCallCount.Add(1)

	m.mu.RLock()
	isNotFound := m.notFoundSpecies[scientificName]
	m.mu.RUnlock()

	if isNotFound {
		return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
	}

	// Return a valid image for other species
	return imageprovider.BirdImage{
		URL:            "http://127.0.0.1/" + scientificName + ".jpg",
		ScientificName: scientificName,
		AuthorName:     "Test Author",
		LicenseName:    "CC-BY",
		CachedAt:       time.Now(),
	}, nil
}

func (m *mockProviderWithNotFound) getAPICallCount() int64 {
	return m.apiCallCount.Load()
}

func (m *mockProviderWithNotFound) resetCounters() {
	m.apiCallCount.Store(0)
}

// mockProviderWithTransientError simulates transient errors
type mockProviderWithTransientError struct {
	apiCallCount atomic.Int64
	errorMessage string
}

func (m *mockProviderWithTransientError) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	m.apiCallCount.Add(1)
	// Return a network error (not ErrImageNotFound) to simulate transient error
	return imageprovider.BirdImage{}, errors.New(errors.NewStd(m.errorMessage)).
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Context("operation", "mock_fetch").
		Build()
}

func (m *mockProviderWithTransientError) getAPICallCount() int64 {
	return m.apiCallCount.Load()
}

// TestNegativeCacheTTLValue pins the negative-cache TTL.
//
// TestNegativeCachingBehavior/NegativeCacheExpiry exercises the expiry BEHAVIOUR with a
// literal seed age, which deliberately cannot detect a change to the constant. This
// asserts the value, so the two together catch both a broken staleness check and a
// wrong TTL. Mirrors TestCircuitBreakerNetworkDuration in network_failure_test.go.
func TestNegativeCacheTTLValue(t *testing.T) {
	t.Parallel()
	assert.Equal(t, 15*time.Minute, imageprovider.NegativeCacheTTL,
		"negative cache TTL changed; confirm the expiry seed age in NegativeCacheExpiry still exceeds it")
}
