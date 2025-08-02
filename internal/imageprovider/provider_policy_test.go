package imageprovider_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// TestProviderNameConsistency verifies that the Wikipedia provider uses
// the correct name "wikimedia" to match configuration expectations
func TestProviderNameConsistency(t *testing.T) {
	t.Parallel()

	settings := conf.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = "wikimedia"
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = "none"
	conf.SetTestSettings(settings)

	// Create Wikipedia provider and verify it registers with the correct name
	store := newMockStore()

	// Use the real Wikipedia provider constructor
	provider, err := imageprovider.NewWikiMediaProvider()
	require.NoError(t, err, "Failed to create WikiMedia provider")

	// Initialize cache with the provider (no metrics needed for testing)
	cache := imageprovider.InitCache("wikimedia", provider, nil, store)
	defer func() {
		err := cache.Close()
		assert.NoError(t, err, "Failed to close cache")
	}()

	// Create registry and register the cache
	registry := imageprovider.NewImageProviderRegistry()
	err = registry.Register("wikimedia", cache)
	require.NoError(t, err, "Failed to register wikimedia provider")

	// Verify the provider is accessible with "wikimedia" name
	_, found := registry.GetCache("wikimedia")
	assert.True(t, found, "wikimedia provider should be found in registry")

	// Verify "wikipedia" name is NOT registered
	_, found = registry.GetCache("wikipedia")
	assert.False(t, found, "wikipedia provider name should NOT be found in registry")
}

// TestFallbackPolicyEnforcement verifies that the fallback policy is respected
// in both batchLoadFromDB and Get methods
func TestFallbackPolicyEnforcement(t *testing.T) {
	tests := []struct {
		name           string
		fallbackPolicy string
		expectFallback bool
	}{
		{
			name:           "fallback_policy_none_prevents_fallback",
			fallbackPolicy: "none",
			expectFallback: false,
		},
		{
			name:           "fallback_policy_all_allows_fallback",
			fallbackPolicy: "all",
			expectFallback: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test isolation with new settings for each subtest
			settings := conf.GetTestSettings()
			settings.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
			settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = tt.fallbackPolicy
			conf.SetTestSettings(settings)

			store := newMockStore()

			// Create primary provider that will fail
			primaryProvider := &mockImageProvider{shouldFail: true}
			primaryCache := imageprovider.InitCache("avicommons", primaryProvider, nil, store)
			defer func() {
				err := primaryCache.Close()
				assert.NoError(t, err, "Failed to close primary cache")
			}()

			// Create fallback provider that will succeed
			fallbackProvider := &mockImageProvider{shouldFail: false}
			fallbackCache := imageprovider.InitCache("wikimedia", fallbackProvider, nil, store)
			defer func() {
				err := fallbackCache.Close()
				assert.NoError(t, err, "Failed to close fallback cache")
			}()

			// Create and setup registry
			registry := imageprovider.NewImageProviderRegistry()
			require.NoError(t, registry.Register("avicommons", primaryCache))
			require.NoError(t, registry.Register("wikimedia", fallbackCache))

			// Set registry on primary cache to enable fallback
			primaryCache.SetRegistry(registry)

			// Test Get method
			_, err := primaryCache.Get("Parus major")

			if tt.expectFallback {
				// With fallback enabled, the primary provider fails but fallback should work
				// So we should get a result (no error or specific not found error)
				assert.True(t, err == nil || isImageNotFoundError(err),
					"Expected success or not found error with fallback enabled, got: %v", err)
			} else {
				// Without fallback, we should get an error from the primary provider
				assert.Error(t, err, "Expected error when primary provider fails and fallback is disabled")
			}
		})
	}
}

// TestBatchLoadFromDBFallbackPolicy verifies that batchLoadFromDB respects
// the fallback policy setting
func TestBatchLoadFromDBFallbackPolicy(t *testing.T) {
	// Helper to test GetBatchCachedOnly which uses batchLoadFromDB internally
	testCases := []struct {
		name               string
		fallbackPolicy     string
		setupStore         func(t *testing.T, store *mockStore)
		expectedProviders  map[string]bool
		expectedImageCount int
	}{
		{
			name:           "no_fallback_when_policy_none",
			fallbackPolicy: "none",
			setupStore: func(t *testing.T, store *mockStore) {
				// Add image only for wikimedia provider
				err := store.SaveImageCache(&datastore.ImageCache{
					ScientificName: "Parus major",
					ProviderName:   "wikimedia",
					URL:            "http://wiki.example.com/parus.jpg",
					CachedAt:       time.Now(),
				})
				require.NoError(t, err, "Failed to save test image to cache")
			},
			expectedProviders:  map[string]bool{"avicommons": true}, // Only avicommons should be checked
			expectedImageCount: 0, // No images found because avicommons has none
		},
		{
			name:           "fallback_when_policy_all",
			fallbackPolicy: "all",
			setupStore: func(t *testing.T, store *mockStore) {
				// Add image only for wikimedia provider
				err := store.SaveImageCache(&datastore.ImageCache{
					ScientificName: "Parus major",
					ProviderName:   "wikimedia",
					URL:            "http://wiki.example.com/parus.jpg",
					CachedAt:       time.Now(),
				})
				require.NoError(t, err, "Failed to save test image to cache")
			},
			expectedProviders:  map[string]bool{"avicommons": true, "wikimedia": true},
			expectedImageCount: 1, // Should find the wikimedia image via fallback
		},
		{
			name:           "primary_provider_has_image",
			fallbackPolicy: "none",
			setupStore: func(t *testing.T, store *mockStore) {
				// Add image for primary provider
				err := store.SaveImageCache(&datastore.ImageCache{
					ScientificName: "Parus major",
					ProviderName:   "avicommons",
					URL:            "http://avi.example.com/parus.jpg",
					CachedAt:       time.Now(),
				})
				require.NoError(t, err, "Failed to save test image to cache")
			},
			expectedProviders:  map[string]bool{"avicommons": true},
			expectedImageCount: 1, // Should find avicommons image without fallback
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up test configuration
			settings := conf.GetTestSettings()
			settings.Realtime.Dashboard.Thumbnails.ImageProvider = "avicommons"
			settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = tc.fallbackPolicy
			conf.SetTestSettings(settings)

			// Create store and set up test data
			store := newMockStoreWithTracking()
			tc.setupStore(t, store.mockStore)

			// Create cache for avicommons (primary provider)
			mockProvider := &mockImageProvider{}
			cache := imageprovider.InitCache("avicommons", mockProvider, nil, store)
			defer func() {
				err := cache.Close()
				assert.NoError(t, err, "Failed to close cache")
			}()

			// Test GetBatchCachedOnly which internally uses batchLoadFromDB
			species := []string{"Parus major"}
			results := cache.GetBatchCachedOnly(species)

			// Verify result count
			assert.Len(t, results, tc.expectedImageCount,
				"Expected %d images but got %d", tc.expectedImageCount, len(results))

			// Verify which providers were queried
			for provider, expected := range tc.expectedProviders {
				if expected {
					assert.True(t, store.WasProviderQueried(provider),
						"Expected provider %s to be queried", provider)
				} else {
					assert.False(t, store.WasProviderQueried(provider),
						"Expected provider %s NOT to be queried", provider)
				}
			}
		})
	}
}

// isImageNotFoundError checks if an error is an image not found error
func isImageNotFoundError(err error) bool {
	return err != nil && err.Error() == "image not found by provider"
}

// mockStoreWithTracking extends mockStore to track which providers were queried
type mockStoreWithTracking struct {
	*mockStore
	queriedProviders map[string]bool
}

func newMockStoreWithTracking() *mockStoreWithTracking {
	return &mockStoreWithTracking{
		mockStore:        newMockStore(),
		queriedProviders: make(map[string]bool),
	}
}

func (m *mockStoreWithTracking) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) {
	m.mu.Lock()
	m.queriedProviders[providerName] = true
	m.mu.Unlock()
	
	return m.mockStore.GetImageCacheBatch(providerName, scientificNames)
}

func (m *mockStoreWithTracking) WasProviderQueried(providerName string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.queriedProviders[providerName]
}