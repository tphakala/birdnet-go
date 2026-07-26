// cache_pressure_test.go covers what an unresolved or stale species costs per
// request under the retry contract introduced with the non-blocking proxy.
//
// The proxy answers 503 with a Retry-After and the client polls, so anything the
// request path does per lookup is done once per poll, per species, forever.
package imageprovider

import (
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
)

// countingStore records how many image-cache reads reach the database and serves
// nothing, so a lookup that consults it is visible as a count.
type countingStore struct {
	datastore.Interface
	reads atomic.Int64
}

func (s *countingStore) GetImageCache(datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	s.reads.Add(1)
	return nil, datastore.ErrImageCacheNotFound
}

func (s *countingStore) SaveImageCache(*datastore.ImageCache) error { return nil }

func (s *countingStore) GetAllImageCaches(string) ([]datastore.ImageCache, error) {
	return nil, nil
}

// TestGetCachedLocal_DoesNotRepeatADatabaseMissPerPoll is the steady-poll guard.
//
// Every request for an unresolved species used to reach SQLite, and the client's
// retry schedule decides how often that is. On a cold dashboard with ~20
// unresolved species that is several SELECTs per second on a 1-core Pi.
func TestGetCachedLocal_DoesNotRepeatADatabaseMissPerPoll(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	store := &countingStore{}
	cache := &BirdImageCache{providerName: wikiProviderName, store: store}

	// The first lookup reads the database and finds nothing.
	_, found, negative := cache.getCachedLocal(species)
	assert.False(t, found)
	assert.False(t, negative)
	require.Equal(t, int64(1), store.reads.Load())

	// Subsequent polls within the window trust that miss.
	for range 5 {
		_, found, negative = cache.getCachedLocal(species)
		assert.False(t, found, "an unresolved species stays unresolved")
		assert.False(t, negative, "and must not be reported as 'no image exists'")
	}
	assert.Equal(t, int64(1), store.reads.Load(), "polling must not cost one SELECT per poll")

	// A resolution clears the marker and the database is authoritative again.
	cache.clearResolutionMarkers(species)
	_, _, _ = cache.getCachedLocal(species)
	assert.Equal(t, int64(2), store.reads.Load())
}

// TestGetCachedLocal_AStaleRowIsStillServedWhileItRefreshes is the guard against
// over-applying the shortcut.
//
// A species with a stale but perfectly servable row also has a refresh in
// flight. Suppressing its database read on that basis would answer 503 for an
// image already on disk, which is worse than the load it saves.
func TestGetCachedLocal_AStaleRowIsStillServedWhileItRefreshes(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	store := &staleEntryStore{entry: &datastore.ImageCache{
		ScientificName: species,
		ProviderName:   wikiProviderName,
		URL:            "https://127.0.0.1/blackbird.jpg",
		CachedAt:       time.Now().Add(-defaultCacheTTL - time.Hour),
	}}
	cache := InitCache(wikiProviderName, &recordingProvider{fetched: make(chan string, 1)}, nil, store)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	// The first lookup serves the stale row and registers a refresh, which is
	// what puts the species into the in-flight prefetch set.
	for range 3 {
		img, found, negative := cache.getCachedLocal(species)
		assert.True(t, found, "a stale row is still an image")
		assert.False(t, negative)
		assert.Equal(t, "https://127.0.0.1/blackbird.jpg", img.URL)
	}
}

// TestPrefetchAsync_BacksOffAfterAFailedAttempt is the other half: without a
// marker, every client retry scheduled a fresh goroutine and a fresh provider
// attempt, so the retry rate was set by the client rather than by us.
func TestPrefetchAsync_BacksOffAfterAFailedAttempt(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	cache := InitCache(wikiProviderName, &recordingProvider{fetched: make(chan string, 1)}, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	require.True(t, cache.PrefetchAsync(species), "the first attempt is scheduled")

	cache.recordFailedAttempt(species)
	for range 5 {
		assert.False(t, cache.PrefetchAsync(species),
			"a species whose resolution just failed must not be re-attempted on every poll")
	}

	cache.clearResolutionMarkers(species)
	assert.True(t, cache.PrefetchAsync(species), "the backoff must expire, not latch")
}

// TestRecentlyAttempted_ExpiresLazily pins that the marker is a backoff and not a
// second negative cache: a failed attempt is usually transient, so it has to age
// out on its own.
func TestRecentlyAttempted_ExpiresLazily(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	cache := &BirdImageCache{providerName: wikiProviderName}

	assert.False(t, cache.recentlyAttempted(species), "nothing is known yet")

	cache.recordFailedAttempt(species)
	assert.True(t, cache.recentlyAttempted(species))

	// Backdate past the window rather than sleeping for it.
	cache.recentAttempts.Store(species, time.Now().Add(-recentAttemptTTL-time.Second))
	assert.False(t, cache.recentlyAttempted(species))
	_, stillStored := cache.recentAttempts.Load(species)
	assert.False(t, stillStored, "an expired marker is dropped rather than re-checked forever")
}

// TestCheckCachedEntryAfterLock_PositiveEntriesExpire is the memory-cache TTL.
//
// A positive memory entry used to be returned with no staleness check at all, so
// once a wrong image landed in dataMap nothing on the request path could
// re-derive it for the process lifetime, and clearing the database cache still
// handed back the same wrong image.
func TestCheckCachedEntryAfterLock_PositiveEntriesExpire(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	cache := &BirdImageCache{providerName: wikiProviderName}
	log := GetLogger()

	fresh := &BirdImage{
		URL:            "https://example.invalid/blackbird.jpg",
		ScientificName: species,
		CachedAt:       time.Now(),
	}
	cache.dataMap.Store(species, fresh)

	img, found, shouldReturn, err := cache.checkCachedEntryAfterLock(species, log)
	require.NoError(t, err)
	assert.True(t, found)
	assert.False(t, shouldReturn)
	assert.Equal(t, fresh.URL, img.URL)

	stale := *fresh
	stale.CachedAt = time.Now().Add(-defaultCacheTTL - time.Hour)
	cache.dataMap.Store(species, &stale)

	_, found, shouldReturn, err = cache.checkCachedEntryAfterLock(species, log)
	require.NoError(t, err)
	assert.False(t, found, "an expired positive entry is not an answer")
	assert.False(t, shouldReturn)
	_, stillInMemory := cache.dataMap.Load(species)
	assert.False(t, stillInMemory,
		"the expired entry is dropped so the database, which schedules the refresh, is consulted")
}
