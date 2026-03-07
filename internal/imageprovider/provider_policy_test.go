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

const (
	providerAvicommons = "avicommons"
	providerWikimedia  = "wikimedia"
)

// TestProviderNameConsistency verifies that the Wikipedia provider uses
// the correct name "wikimedia" to match configuration expectations
func TestProviderNameConsistency(t *testing.T) {
	t.Parallel()

	settings := conf.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerWikimedia
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = "none"
	conf.SetTestSettings(settings)

	// Create Wikipedia provider and verify it registers with the correct name
	store := newMockStore()

	// Use the real Wikipedia provider constructor
	provider, err := imageprovider.NewWikiMediaProvider()
	require.NoError(t, err, "Failed to create WikiMedia provider")

	// Initialize cache with the provider (no metrics needed for testing)
	cache := imageprovider.InitCache(providerWikimedia, provider, nil, store)
	defer func() {
		err := cache.Close()
		assert.NoError(t, err, "Failed to close cache")
	}()

	// Create registry and register the cache
	registry := imageprovider.NewImageProviderRegistry()
	err = registry.Register(providerWikimedia, cache)
	require.NoError(t, err, "Failed to register wikimedia provider")

	// Verify the provider is accessible with "wikimedia" name
	_, found := registry.GetCache(providerWikimedia)
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
			settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
			settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = tt.fallbackPolicy
			conf.SetTestSettings(settings)

			store := newMockStore()

			// Create primary provider that will fail
			primaryProvider := &mockImageProvider{shouldFail: true}
			primaryCache := imageprovider.InitCache(providerAvicommons, primaryProvider, nil, store)
			defer func() {
				err := primaryCache.Close()
				assert.NoError(t, err, "Failed to close primary cache")
			}()

			// Create fallback provider that will succeed
			fallbackProvider := &mockImageProvider{shouldFail: false}
			fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProvider, nil, store)
			defer func() {
				err := fallbackCache.Close()
				assert.NoError(t, err, "Failed to close fallback cache")
			}()

			// Create and setup registry
			registry := imageprovider.NewImageProviderRegistry()
			require.NoError(t, registry.Register(providerAvicommons, primaryCache))
			require.NoError(t, registry.Register(providerWikimedia, fallbackCache))

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
				t.Helper()
				// Add image only for wikimedia provider
				err := store.SaveImageCache(&datastore.ImageCache{
					ScientificName: "Parus major",
					ProviderName:   providerWikimedia,
					URL:            "http://wiki.example.com/parus.jpg",
					CachedAt:       time.Now(),
				})
				require.NoError(t, err, "Failed to save test image to cache")
			},
			expectedProviders:  map[string]bool{providerAvicommons: true}, // Only avicommons should be checked
			expectedImageCount: 0,                                         // No images found because avicommons has none
		},
		{
			name:           "fallback_when_policy_all",
			fallbackPolicy: "all",
			setupStore: func(t *testing.T, store *mockStore) {
				t.Helper()
				// Add image only for wikimedia provider
				err := store.SaveImageCache(&datastore.ImageCache{
					ScientificName: "Parus major",
					ProviderName:   providerWikimedia,
					URL:            "http://wiki.example.com/parus.jpg",
					CachedAt:       time.Now(),
				})
				require.NoError(t, err, "Failed to save test image to cache")
			},
			expectedProviders:  map[string]bool{providerAvicommons: true, providerWikimedia: true},
			expectedImageCount: 1, // Should find the wikimedia image via fallback
		},
		{
			name:           "primary_provider_has_image",
			fallbackPolicy: "none",
			setupStore: func(t *testing.T, store *mockStore) {
				t.Helper()
				// Add image for primary provider
				err := store.SaveImageCache(&datastore.ImageCache{
					ScientificName: "Parus major",
					ProviderName:   providerAvicommons,
					URL:            "http://avi.example.com/parus.jpg",
					CachedAt:       time.Now(),
				})
				require.NoError(t, err, "Failed to save test image to cache")
			},
			expectedProviders:  map[string]bool{providerAvicommons: true},
			expectedImageCount: 1, // Should find avicommons image without fallback
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Set up test configuration
			settings := conf.GetTestSettings()
			settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
			settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = tc.fallbackPolicy
			conf.SetTestSettings(settings)

			// Create store and set up test data
			store := newMockStoreWithTracking()
			tc.setupStore(t, store.mockStore)

			// Create cache for avicommons (primary provider)
			mockProvider := &mockImageProvider{}
			cache := imageprovider.InitCache(providerAvicommons, mockProvider, nil, store)
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

// TestRefreshEntryFallbackToDBCache verifies that when a stale cache entry is refreshed
// and the primary provider has no image, the system falls back to a valid DB entry from
// another provider (Tier 1: DB-first, no network).
func TestRefreshEntryFallbackToDBCache(t *testing.T) {
	const species = "Acanthis flammea"

	settings := conf.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = "all"
	conf.SetTestSettings(settings)

	store := newMockStore()

	// Pre-populate: stale avicommons entry (triggers refresh)
	staleTime := time.Now().Add(-31 * 24 * time.Hour)
	require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   providerAvicommons,
		URL:            "http://old.example.com/redpoll.jpg",
		AuthorName:     "Old Author",
		LicenseName:    "CC BY-SA 4.0",
		CachedAt:       staleTime,
	}))

	// Pre-populate: valid wikimedia entry (fallback source)
	require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   providerWikimedia,
		URL:            "https://upload.wikimedia.org/Carduelis_flammea_CT6.jpg",
		AuthorName:     "Cephas",
		LicenseName:    "CC BY-SA 3.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/3.0/",
		CachedAt:       time.Now().Add(-5 * 24 * time.Hour), // 5 days old, not stale
	}))

	// Primary provider: always returns ErrImageNotFound
	primaryProvider := &mockNotFoundProvider{}
	primaryCache := imageprovider.InitCache(providerAvicommons, primaryProvider, nil, store)
	t.Cleanup(func() { assert.NoError(t, primaryCache.Close()) })

	// Fallback provider (needed for registry, won't be called for Tier 1)
	fallbackProvider := &mockImageProvider{}
	fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProvider, nil, store)
	t.Cleanup(func() { assert.NoError(t, fallbackCache.Close()) })

	// Set up registry
	registry := imageprovider.NewImageProviderRegistry()
	require.NoError(t, registry.Register(providerAvicommons, primaryCache))
	require.NoError(t, registry.Register(providerWikimedia, fallbackCache))
	primaryCache.SetRegistry(registry)

	// Trigger a Get which should detect stale entry and spawn background refresh.
	// The Get itself returns stale data immediately.
	img, err := primaryCache.Get(species)
	require.NoError(t, err, "Get should return stale data without error")
	assert.Equal(t, "http://old.example.com/redpoll.jpg", img.URL, "Should return stale data initially")

	// Wait for background refresh to complete
	time.Sleep(500 * time.Millisecond)

	// After refresh, the primary cache should now have the wikimedia image
	img2, err := primaryCache.Get(species)
	require.NoError(t, err, "Get after refresh should succeed")
	assert.Equal(t, "https://upload.wikimedia.org/Carduelis_flammea_CT6.jpg", img2.URL,
		"Should have fallback wikimedia URL after refresh")
	assert.Equal(t, "Cephas", img2.AuthorName, "Should preserve wikimedia attribution")
	assert.Equal(t, "CC BY-SA 3.0", img2.LicenseName, "Should preserve wikimedia license")

	// Verify DB was updated under primary provider name
	dbEntry, err := store.GetImageCache(datastore.ImageCacheQuery{
		ScientificName: species,
		ProviderName:   providerAvicommons,
	})
	require.NoError(t, err, "DB should have updated avicommons entry")
	assert.Equal(t, "https://upload.wikimedia.org/Carduelis_flammea_CT6.jpg", dbEntry.URL,
		"DB entry under avicommons should have wikimedia URL")
	assert.True(t, dbEntry.CachedAt.After(staleTime),
		"DB entry should have fresh timestamp")
}

// TestRefreshEntryFallbackPolicyNone verifies that refresh does not attempt
// fallback when the policy is set to "none".
func TestRefreshEntryFallbackPolicyNone(t *testing.T) {
	const species = "Acanthis flammea"

	settings := conf.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = "none"
	conf.SetTestSettings(settings)

	store := newMockStore()

	// Stale avicommons entry
	require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   providerAvicommons,
		URL:            "http://old.example.com/redpoll.jpg",
		CachedAt:       time.Now().Add(-31 * 24 * time.Hour),
	}))

	// Valid wikimedia entry exists but should NOT be used
	require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   providerWikimedia,
		URL:            "https://upload.wikimedia.org/Carduelis_flammea_CT6.jpg",
		AuthorName:     "Cephas",
		CachedAt:       time.Now().Add(-5 * 24 * time.Hour),
	}))

	primaryProvider := &mockNotFoundProvider{}
	primaryCache := imageprovider.InitCache(providerAvicommons, primaryProvider, nil, store)
	t.Cleanup(func() { assert.NoError(t, primaryCache.Close()) })

	fallbackProvider := &mockImageProvider{}
	fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProvider, nil, store)
	t.Cleanup(func() { assert.NoError(t, fallbackCache.Close()) })

	registry := imageprovider.NewImageProviderRegistry()
	require.NoError(t, registry.Register(providerAvicommons, primaryCache))
	require.NoError(t, registry.Register(providerWikimedia, fallbackCache))
	primaryCache.SetRegistry(registry)

	// First Get returns stale data, triggers background refresh
	_, err := primaryCache.Get(species)
	require.NoError(t, err)

	time.Sleep(500 * time.Millisecond)

	// After refresh with policy "none", primary cache should NOT have wikimedia image
	img, err := primaryCache.Get(species)

	// The image should still be the old stale one (or a negative entry)
	if err == nil {
		assert.NotEqual(t, "https://upload.wikimedia.org/Carduelis_flammea_CT6.jpg", img.URL,
			"Should NOT use wikimedia fallback when policy is 'none'")
	}
}

// mockNotFoundProvider always returns ErrImageNotFound
type mockNotFoundProvider struct{}

func (m *mockNotFoundProvider) Fetch(_ string) (imageprovider.BirdImage, error) {
	return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
}
