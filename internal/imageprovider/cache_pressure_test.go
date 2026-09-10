// cache_pressure_test.go covers what an unresolved or stale species costs per
// request under the retry contract introduced with the non-blocking proxy.
//
// The proxy answers 503 with a Retry-After and the client polls, so anything the
// request path does per lookup is done once per poll, per species, forever.
package imageprovider

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// dbReadCountingStore records how many image-cache reads reach the database and serves
// nothing, so a lookup that consults it is visible as a count.
type dbReadCountingStore struct {
	datastore.Interface
	reads atomic.Int64
}

func (s *dbReadCountingStore) GetImageCache(datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	s.reads.Add(1)
	return nil, datastore.ErrImageCacheNotFound
}

func (s *dbReadCountingStore) SaveImageCache(*datastore.ImageCache) error { return nil }

func (s *dbReadCountingStore) GetAllImageCaches(string) ([]datastore.ImageCache, error) {
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
	store := &dbReadCountingStore{}
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

// TestCheckCachedEntryAfterLock_ServesAStalePositiveEntry pins the memory-cache
// TTL decision.
//
// A stale positive entry is served rather than discarded, exactly as a stale
// database row is, and the caller schedules the refresh that re-derives it.
// Deleting it here instead would make every request for a species older than
// the TTL pay a fresh SQLite read, because the row is the same age and is
// promoted back with the same timestamp, so the next lookup expires it again;
// and on a store-less or corruption-latched cache it would throw away the only
// copy the process has.
func TestCheckCachedEntryAfterLock_ServesAStalePositiveEntry(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	cache := &BirdImageCache{providerName: wikiProviderName}
	log := GetLogger()

	stale := &BirdImage{
		URL:            "https://127.0.0.1/blackbird.jpg",
		ScientificName: species,
		CachedAt:       time.Now().Add(-defaultCacheTTL - time.Hour),
	}
	cache.dataMap.Store(species, stale)

	img, found, shouldReturn, err := cache.checkCachedEntryAfterLock(species, log)
	require.NoError(t, err)
	assert.True(t, found, "a stale image is still an image")
	assert.False(t, shouldReturn)
	assert.Equal(t, stale.URL, img.URL)
	_, stillInMemory := cache.dataMap.Load(species)
	assert.True(t, stillInMemory, "the entry must not be discarded; the refresh replaces it")
}

// TestCheckCachedEntryAfterLock_NegativeEntriesExpire keeps the other half of
// the TTL: a negative entry that has aged out is not an answer.
func TestCheckCachedEntryAfterLock_NegativeEntriesExpire(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	cache := &BirdImageCache{providerName: wikiProviderName}
	log := GetLogger()

	fresh := &BirdImage{URL: negativeEntryMarker, ScientificName: species, CachedAt: time.Now()}
	cache.dataMap.Store(species, fresh)
	_, _, shouldReturn, err := cache.checkCachedEntryAfterLock(species, log)
	require.Error(t, err)
	assert.True(t, shouldReturn, "a live negative entry is a definitive answer")

	expired := *fresh
	expired.CachedAt = time.Now().Add(-negativeCacheTTL - time.Minute)
	cache.dataMap.Store(species, &expired)
	_, found, shouldReturn, err := cache.checkCachedEntryAfterLock(species, log)
	require.NoError(t, err)
	assert.False(t, found)
	assert.False(t, shouldReturn, "an expired negative entry must let the caller re-derive")
	_, stillInMemory := cache.dataMap.Load(species)
	assert.False(t, stillInMemory)
}

// TestRecordMarkerIsBounded pins the cap on the marker maps.
//
// The keys are caller-supplied scientific names that the media endpoints pass
// straight through, so the key space is request traffic rather than the label
// set. Both markers are pure optimizations, so clearing is always safe.
func TestRecordMarkerIsBounded(t *testing.T) {
	t.Parallel()

	cache := &BirdImageCache{providerName: wikiProviderName}
	for i := range maxMarkerEntries + 100 {
		cache.recordDBAbsent(fmt.Sprintf("Species %d", i))
	}

	live := 0
	cache.dbAbsent.Range(func(_, _ any) bool { live++; return true })
	assert.LessOrEqual(t, live, maxMarkerEntries,
		"the marker map must stay bounded however many distinct names arrive")
	assert.Positive(t, live, "clearing must not leave the map permanently empty")
}

// failingProvider always fails with a transient error, which is the case
// runPrefetch must record a backoff for.
type failingProvider struct{ calls atomic.Int64 }

func (p *failingProvider) Fetch(scientificName string) (BirdImage, error) {
	p.calls.Add(1)
	return BirdImage{}, errors.Newf("simulated transient provider failure").
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Build()
}

// TestRunPrefetch_RecordsBackoffOnTransientFailure covers the production wiring
// of the backoff, not just the marker helpers.
//
// Without it the retry rate is the client's: the proxy answers 503 with a
// Retry-After, and every poll would schedule a fresh goroutine and a fresh
// provider attempt because nothing recorded that the last one had just failed.
func TestRunPrefetch_RecordsBackoffOnTransientFailure(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	provider := &failingProvider{}
	cache := InitCache(wikiProviderName, provider, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	require.True(t, cache.PrefetchAsync(species))
	require.Eventually(t, func() bool { return provider.calls.Load() > 0 },
		2*time.Second, 5*time.Millisecond, "the prefetch should reach the provider")
	require.Eventually(t, func() bool { return cache.recentlyAttempted(species) },
		2*time.Second, 5*time.Millisecond,
		"a transient provider failure must arm the backoff, or the client sets the retry rate")

	assert.False(t, cache.PrefetchAsync(species),
		"the next poll must be declined while the backoff is live")
}

// TestRunPrefetch_ClearsMarkersOnSuccess is the other half: the backoff must not
// outlive the condition it was recorded for.
//
// The markers are recorded while the prefetch is parked inside the provider, so
// only runPrefetch's own clear can satisfy the assertion. Recording them before
// scheduling would make the check vacuous: the predicate would already hold at
// the first tick, and deleting the production clear would leave the test green.
func TestRunPrefetch_ClearsMarkersOnSuccess(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	provider := newBlockingProvider(t)
	provider.result = BirdImage{URL: "https://127.0.0.1/blackbird.jpg", ScientificName: species}
	cache := InitCache(wikiProviderName, provider, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	require.True(t, cache.PrefetchAsync(species))
	require.Eventually(t, func() bool { return provider.calls.Load() > 0 },
		2*time.Second, 5*time.Millisecond, "the prefetch must be parked in the provider")

	// Recorded mid-flight, so the production clear is the only thing that can
	// remove them.
	cache.recordFailedAttempt(species)
	cache.recordDBAbsent(species)
	require.True(t, cache.recentlyAttempted(species))
	require.True(t, cache.dbKnownAbsent(species))

	close(provider.release)

	require.Eventually(t, func() bool {
		return !cache.recentlyAttempted(species) && !cache.dbKnownAbsent(species)
	}, 2*time.Second, 5*time.Millisecond,
		"a resolved species must not keep either marker")
}

// TestGetCachedLocal_StaleMemoryEntryIsRefreshed is the closure test for serving
// a stale positive entry instead of deleting it.
//
// Serving it is only correct if the refresh that re-derives it is still
// scheduled. Without that the entry stays resident for the whole 30-day TTL and
// nothing on the request path can correct it, which is the exact condition the
// memory TTL exists to end: a wrong image immortal in memory.
func TestGetCachedLocal_StaleMemoryEntryIsRefreshed(t *testing.T) {
	t.Parallel()

	const species = "Turdus merula"
	provider := &recordingProvider{
		fetched: make(chan string, 1),
		result:  BirdImage{URL: "https://127.0.0.1/fresh.jpg", ScientificName: species},
	}
	cache := InitCache(wikiProviderName, provider, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	cache.dataMap.Store(species, &BirdImage{
		URL:            "https://127.0.0.1/stale.jpg",
		ScientificName: species,
		CachedAt:       time.Now().Add(-defaultCacheTTL - time.Hour),
	})

	img, found, negative := cache.getCachedLocal(species)
	require.True(t, found, "a stale entry is still served")
	assert.False(t, negative)
	assert.Equal(t, "https://127.0.0.1/stale.jpg", img.URL)

	select {
	case got := <-provider.fetched:
		assert.Equal(t, species, got, "serving a stale entry must also schedule its refresh")
	case <-time.After(2 * time.Second):
		t.Fatal("no refresh was scheduled for the stale memory entry, so nothing can ever re-derive it")
	}
}

// TestScheduleRefresh_IsBounded pins the cap on background refreshes.
//
// Refreshes do not pass through the prefetch semaphore, so without a bound a
// dashboard rendering many stale species spawns one goroutine per species, each
// parked on the provider's global rate limiter.
func TestScheduleRefresh_IsBounded(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := InitCache(wikiProviderName, provider, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	// Every refresh parks in the provider, so none of them frees its slot.
	for i := range maxQueuedRefreshes + 25 {
		cache.scheduleRefresh(fmt.Sprintf("Species %d", i))
	}

	assert.LessOrEqual(t, cache.refreshQueued.Load(), int64(maxQueuedRefreshes),
		"registered refreshes must stay within their budget")

	// The budget is separate from the prefetch one, so a burst of refreshes must
	// not consume the queue that resolves species nothing is known about.
	assert.True(t, cache.PrefetchAsync("Turdus merula"),
		"a saturated refresh budget must not starve prefetches")
}
