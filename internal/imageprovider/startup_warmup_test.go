package imageprovider_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// negativeCacheURL mirrors the unexported imageprovider.negativeEntryMarker. It is
// the persisted URL of a negative ("no image anywhere") cache entry; the value is
// effectively frozen because changing it would invalidate existing on-disk caches.
const negativeCacheURL = "__NOT_FOUND__"

// TestLoadCachedImagesWarmupPopulatesMemory is a regression test for the
// loadCachedImages double-pointer bug (Forgejo #1311): the warmup loop stored
// &birdImage (a **BirdImage) instead of the *BirdImage, so every reader's
// value.(*BirdImage) assertion failed and the whole startup warmup was dead.
//
// MemoryUsage counts each entry's key bytes unconditionally, but only adds the
// value's EstimateSize when the stored value type-asserts to *BirdImage. With the
// bug, MemoryUsage therefore equals exactly the sum of the key lengths; a correct
// warmup stores *BirdImage and adds EstimateSize (> 0) for every entry.
func TestLoadCachedImagesWarmupPopulatesMemory(t *testing.T) {
	t.Parallel()

	store := newMockStore()

	// Seed positive DB rows under "wikimedia" (the provider name CreateDefaultCache
	// uses), so loadCachedImages warms them into the cache's memory map at startup.
	species := []string{"Parus major", "Turdus merula", "Cyanistes caeruleus"}
	keyBytes := 0
	for _, s := range species {
		require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
			ScientificName: s,
			ProviderName:   providerWikimedia, // matches CreateDefaultCache's provider name
			URL:            "https://example.com/" + s + ".jpg",
			AuthorName:     "Test Author",
			LicenseName:    "CC BY-SA 4.0",
			CachedAt:       time.Now(),
		}))
		keyBytes += len(s)
	}

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	// Construction runs loadCachedImages, warming the three rows into memory.
	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, cache.Close()) })

	assert.Greater(t, cache.MemoryUsage(), keyBytes,
		"startup warmup must store *BirdImage values so readers can see them; "+
			"the **BirdImage double-pointer bug makes MemoryUsage count only key bytes")
}

// TestGetWarmedNegativePrimaryConsultsFallback is the safety regression that pins
// down the interaction between the double-pointer warmup fix and the negative
// cache. The fix makes startup-warmed entries (including negative "__NOT_FOUND__"
// entries) visible to the single-item Get() path via checkCachedEntryAfterLock,
// which the media proxy uses. A warmed negative primary entry must NOT be treated
// as a resolved result: Get must still fall through to the fallback provider.
// Without this guard, making warmed negatives visible could re-introduce GitHub
// #3806 (fallback masking) on the proxy path for warmed species.
//
// This is a forward-looking guard, not a test that fails on the double-pointer bug:
// it stays green on both the buggy (**BirdImage, negative re-read from DB) and the
// fixed (negative served from warmed memory) code, because both paths reach the same
// fallback. It locks in the invariant so a future change that makes a warmed negative
// short-circuit the fallback would fail here. TestLoadCachedImagesWarmupPopulatesMemory
// is the test that actually fails on the double-pointer bug.
func TestGetWarmedNegativePrimaryConsultsFallback(t *testing.T) {
	// Not parallel: mutates the process-global settings snapshot.
	const species = "Passer domesticus"

	settings := conftest.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = "all"
	applyGlobalSettings(t, settings)

	store := newMockStore()

	// Fresh NEGATIVE primary (avicommons) DB row: warmed into the primary cache's
	// in-memory map at startup by loadCachedImages.
	require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   providerAvicommons,
		URL:            negativeCacheURL,
		CachedAt:       time.Now(),
	}))
	// Positive fallback (wikimedia) DB row that the fallback chain must resolve.
	require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
		ScientificName: species,
		ProviderName:   providerWikimedia,
		URL:            "https://wiki.example.com/sparrow.jpg",
		AuthorName:     "Cephas",
		LicenseName:    "CC BY-SA 3.0",
		CachedAt:       time.Now(),
	}))

	primaryProvider := &mockNotFoundProvider{}
	primaryCache := imageprovider.InitCache(providerAvicommons, primaryProvider, nil, store)
	t.Cleanup(func() { assert.NoError(t, primaryCache.Close()) })

	fallbackProvider := &mockNotFoundProvider{}
	fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProvider, nil, store)
	t.Cleanup(func() { assert.NoError(t, fallbackCache.Close()) })

	registry := imageprovider.NewImageProviderRegistry()
	require.NoError(t, registry.Register(providerAvicommons, primaryCache))
	require.NoError(t, registry.Register(providerWikimedia, fallbackCache))
	primaryCache.SetRegistry(registry)

	img, err := primaryCache.Get(species)
	require.NoError(t, err, "a warmed negative primary entry must not block the fallback")
	assert.Equal(t, "https://wiki.example.com/sparrow.jpg", img.URL,
		"Get must return the fallback provider's image, not the primary's negative entry")
}

// TestGetDBFallbackHonorsPolicy is single-item parity coverage for the deleted
// batch policy tests (TestBatchLoadFromDBFallbackPolicy). It verifies that the
// DB-tier fallback the single-item Get() path performs (loadFromDBCache against the
// fallback provider) honors the configured FallbackPolicy: it resolves a
// fallback-only DB row under policy "all" and refuses to consult the fallback
// provider under policy "none".
func TestGetDBFallbackHonorsPolicy(t *testing.T) {
	testCases := []struct {
		name           string
		fallbackPolicy string
		expectImage    bool
	}{
		{"fallback_when_policy_all", "all", true},
		{"no_fallback_when_policy_none", "none", false},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Not parallel: mutates the process-global settings snapshot.
			const species = "Parus major"

			settings := conftest.GetTestSettings()
			settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
			settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = tc.fallbackPolicy
			applyGlobalSettings(t, settings)

			// Track which providers had their DB consulted via GetImageCache.
			store := newMockStoreWithTracking()
			// Image cached only under the fallback (wikimedia) provider.
			require.NoError(t, store.SaveImageCache(&datastore.ImageCache{
				ScientificName: species,
				ProviderName:   providerWikimedia,
				URL:            "https://wiki.example.com/parus.jpg",
				CachedAt:       time.Now(),
			}))

			// Primary returns not-found so resolution depends on the DB fallback.
			primaryProvider := &mockNotFoundProvider{}
			primaryCache := imageprovider.InitCache(providerAvicommons, primaryProvider, nil, store)
			t.Cleanup(func() { assert.NoError(t, primaryCache.Close()) })

			fallbackProvider := &mockNotFoundProvider{}
			fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProvider, nil, store)
			t.Cleanup(func() { assert.NoError(t, fallbackCache.Close()) })

			registry := imageprovider.NewImageProviderRegistry()
			require.NoError(t, registry.Register(providerAvicommons, primaryCache))
			require.NoError(t, registry.Register(providerWikimedia, fallbackCache))
			primaryCache.SetRegistry(registry)

			img, err := primaryCache.Get(species)
			if tc.expectImage {
				require.NoError(t, err, "policy=all should resolve via the fallback DB row")
				assert.Equal(t, "https://wiki.example.com/parus.jpg", img.URL,
					"Get must return the fallback provider's cached image")
				assert.True(t, store.WasProviderQueried(providerWikimedia),
					"fallback provider DB must be consulted under policy=all")
			} else {
				require.Error(t, err, "policy=none must not resolve via the fallback provider")
				assert.False(t, store.WasProviderQueried(providerWikimedia),
					"fallback provider DB must NOT be consulted under policy=none")
			}
		})
	}
}
