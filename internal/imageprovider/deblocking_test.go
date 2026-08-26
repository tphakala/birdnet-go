package imageprovider

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// blockingProvider parks every Fetch until released, so a test can prove a caller did
// not wait for the provider rather than merely that it was fast.
type blockingProvider struct {
	release chan struct{}
	calls   atomic.Int64
	// cancelled records that a parked fetch actually observed its context being
	// cancelled, so a test can assert the cancellation path rather than merely that
	// the caller stopped waiting.
	cancelled atomic.Bool
	result    BirdImage
	err       error
}

func newBlockingProvider(t *testing.T) *blockingProvider {
	t.Helper()
	p := &blockingProvider{release: make(chan struct{})}
	// Released on cleanup so a parked fetch cannot outlive the test and trip goleak.
	t.Cleanup(func() {
		select {
		case <-p.release:
		default:
			close(p.release)
		}
	})
	return p
}

func (p *blockingProvider) Fetch(scientificName string) (BirdImage, error) {
	p.calls.Add(1)
	<-p.release
	return p.result, p.err
}

func (p *blockingProvider) FetchWithContext(ctx context.Context, scientificName string) (BirdImage, error) {
	p.calls.Add(1)
	select {
	case <-p.release:
		return p.result, p.err
	case <-ctx.Done():
		p.cancelled.Store(true)
		return BirdImage{}, ctx.Err()
	}
}

// newDeblockingCache builds a cache with no datastore, so every lookup that is not in
// the memory map exercises the "nothing cached" path.
func newDeblockingCache(t *testing.T, provider ImageProvider) *BirdImageCache {
	t.Helper()
	cache := InitCache(wikiProviderName, provider, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })
	return cache
}

// TestGetWithContext_CancellationUnblocksCaller is the core of the de-blocking work:
// before it, BirdImageCache.Get took no context, so a caller that had already given up
// still waited out the provider's full retry and rate-limit budget (minutes for a cold
// species behind the 1 req/s global limiter).
func TestGetWithContext_CancellationUnblocksCaller(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := newDeblockingCache(t, provider)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := cache.GetWithContext(ctx, "Turdus merula")
		done <- err
	}()

	// Wait until the provider is actually parked, so the cancellation below is
	// unblocking a real in-flight fetch rather than racing the goroutine's start.
	require.Eventually(t, func() bool { return provider.calls.Load() > 0 }, 2*time.Second, 5*time.Millisecond)
	cancel()

	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("GetWithContext did not return after its context was cancelled")
	}
}

// TestGetWithContext_CancellationUnblocksLockWaiter covers the second half of the same
// problem: a caller can be stuck waiting for ANOTHER goroutine's fetch. The per-species
// initialization lock used to be a sync.Mutex, whose acquisition cannot be abandoned,
// so a request arriving during a cold fetch inherited that fetch's full duration.
func TestGetWithContext_CancellationUnblocksLockWaiter(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := newDeblockingCache(t, provider)

	// First caller takes the lock and parks in the provider.
	go func() { _, _ = cache.Get("Turdus merula") }()
	require.Eventually(t, func() bool { return provider.calls.Load() > 0 }, 2*time.Second, 5*time.Millisecond)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 1)
	go func() {
		_, err := cache.GetWithContext(ctx, "Turdus merula")
		done <- err
	}()

	cancel()
	select {
	case err := <-done:
		require.ErrorIs(t, err, context.Canceled)
	case <-time.After(2 * time.Second):
		t.Fatal("a caller waiting on the initialization lock ignored its cancelled context")
	}
}

// TestGetCached_NeverCallsProvider pins the property the HTTP handlers depend on.
func TestGetCached_NeverCallsProvider(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := newDeblockingCache(t, provider)

	img, found, negative := cache.GetCached("Turdus merula")

	assert.False(t, found)
	assert.False(t, negative)
	assert.Empty(t, img.URL)
	assert.Zero(t, provider.calls.Load(), "GetCached must not reach the provider")
}

// TestGetCached_DoesNotWaitForAnInFlightFetch asserts that a species someone else is
// already fetching reports "not cached" instead of queueing behind that fetch. Waiting
// there is exactly what a request goroutine must never do.
func TestGetCached_DoesNotWaitForAnInFlightFetch(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := newDeblockingCache(t, provider)

	go func() { _, _ = cache.Get("Turdus merula") }()
	require.Eventually(t, func() bool { return provider.calls.Load() > 0 }, 2*time.Second, 5*time.Millisecond)

	type result struct {
		found    bool
		negative bool
	}
	returned := make(chan result, 1)
	go func() {
		_, found, negative := cache.GetCached("Turdus merula")
		returned <- result{found: found, negative: negative}
	}()

	select {
	case got := <-returned:
		assert.False(t, got.found)
		assert.False(t, got.negative,
			"a species being fetched is indeterminate, not negative: reporting negative would "+
				"make the handler answer a 404 the browser caches for a day")
	case <-time.After(2 * time.Second):
		t.Fatal("GetCached blocked on an in-flight fetch")
	}
}

// TestGetCached_ReportsStates walks the three answers callers switch on.
func TestGetCached_ReportsStates(t *testing.T) {
	t.Parallel()

	cache := newDeblockingCache(t, newBlockingProvider(t))

	t.Run("positive memory entry", func(t *testing.T) {
		cache.dataMap.Store("Parus major", &BirdImage{
			URL:            "https://example.invalid/parus.jpg",
			ScientificName: "Parus major",
			CachedAt:       time.Now(),
		})
		img, found, negative := cache.GetCached("Parus major")
		assert.True(t, found)
		assert.False(t, negative)
		assert.Equal(t, "https://example.invalid/parus.jpg", img.URL)
	})

	t.Run("fresh negative entry", func(t *testing.T) {
		cache.dataMap.Store("Nullus avis", &BirdImage{
			URL:            negativeEntryMarker,
			ScientificName: "Nullus avis",
			CachedAt:       time.Now(),
		})
		_, found, negative := cache.GetCached("Nullus avis")
		assert.False(t, found)
		assert.True(t, negative, "a valid negative entry is a definitive answer, not a miss")
	})

	t.Run("expired negative entry re-opens the lookup", func(t *testing.T) {
		cache.dataMap.Store("Expirus avis", &BirdImage{
			URL:            negativeEntryMarker,
			ScientificName: "Expirus avis",
			CachedAt:       time.Now().Add(-2 * negativeCacheTTL),
		})
		_, found, negative := cache.GetCached("Expirus avis")
		assert.False(t, found)
		assert.False(t, negative,
			"an expired negative entry must not be reported as an answer, or the species can never recover")
	})

	t.Run("exhausted species", func(t *testing.T) {
		cache.recordSpeciesExhausted("Exhaustus avis")
		_, found, negative := cache.GetCached("Exhaustus avis")
		assert.False(t, found)
		assert.True(t, negative)
	})

	t.Run("empty name", func(t *testing.T) {
		_, found, negative := cache.GetCached("")
		assert.False(t, found)
		assert.False(t, negative)
	})
}

// TestPrefetchAsync_DeduplicatesBySpecies is the property that keeps a dashboard
// rendering thirty uncached thumbnails from scheduling thirty provider fetches.
func TestPrefetchAsync_DeduplicatesBySpecies(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := newDeblockingCache(t, provider)

	var wg sync.WaitGroup
	for range 30 {
		wg.Go(func() { cache.PrefetchAsync("Turdus merula") })
	}
	wg.Wait()

	require.Eventually(t, func() bool { return provider.calls.Load() > 0 }, 2*time.Second, 5*time.Millisecond)
	// Never, not a settle sleep: a broken dedup would report a count above 1 at some
	// point during the window, whereas a sleep that is simply too short reports 1 and
	// passes. The failure direction has to be a red test, not a green one.
	require.Never(t, func() bool { return provider.calls.Load() > 1 }, 500*time.Millisecond, 10*time.Millisecond,
		"concurrent prefetches for one species must collapse to a single provider fetch")
}

// TestPrefetchAsync_ReleasesItsDedupSlot asserts the bookkeeping is not one-shot: a
// species must be prefetchable again once its prefetch finishes, or a failed first
// attempt would permanently prevent a retry.
func TestPrefetchAsync_ReleasesItsDedupSlot(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	provider.err = ErrImageNotFound
	close(provider.release) // resolve immediately
	cache := newDeblockingCache(t, provider)

	require.True(t, cache.PrefetchAsync("Turdus merula"))
	require.Eventually(t, func() bool {
		_, stillQueued := cache.prefetching.Load("Turdus merula")
		return !stillQueued
	}, 2*time.Second, 5*time.Millisecond, "the dedup entry must be cleared when the prefetch finishes")

	assert.Zero(t, cache.prefetchQueued.Load(), "the queue counter must return to zero")
}

// TestPrefetchAsync_RejectedAfterClose guards the shutdown path: tryGo refuses to add
// to the WaitGroup once Close has started, and the counter and dedup map must be
// unwound when it does, or the cache would leak a permanent "in flight" entry.
func TestPrefetchAsync_RejectedAfterClose(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	close(provider.release)
	cache := InitCache(wikiProviderName, provider, nil, nil)
	require.NoError(t, cache.Close())

	assert.False(t, cache.PrefetchAsync("Turdus merula"))
	_, queued := cache.prefetching.Load("Turdus merula")
	assert.False(t, queued)
	assert.Zero(t, cache.prefetchQueued.Load())
}

// TestGetCached_StaleDBEntryIsServedAndRefreshed guards a regression the cached-only
// path could easily have introduced.
//
// The blocking path served a stale positive DB entry AND spawned a background refresh.
// GetCached reports found=true for a stale entry, so its caller schedules nothing;
// without the refresh spawned here, a stale image would only ever be corrected by the
// hourly sweep.
func TestGetCached_StaleDBEntryIsServedAndRefreshed(t *testing.T) {
	t.Parallel()

	refreshed := make(chan string, 1)
	provider := &recordingProvider{fetched: refreshed, result: BirdImage{
		URL:            "https://example.invalid/fresh.jpg",
		ScientificName: "Turdus merula",
	}}

	store := &staleEntryStore{entry: &datastore.ImageCache{
		ScientificName: "Turdus merula",
		ProviderName:   wikiProviderName,
		URL:            "https://example.invalid/stale.jpg",
		CachedAt:       time.Now().Add(-2 * defaultCacheTTL),
	}}

	cache := InitCache(wikiProviderName, provider, nil, store)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })
	// loadCachedImages primes dataMap from the store, which would short-circuit the DB
	// branch under test.
	cache.dataMap.Delete("Turdus merula")

	img, found, negative := cache.GetCached("Turdus merula")

	require.True(t, found, "a stale positive entry is still served rather than withheld")
	assert.False(t, negative)
	assert.Equal(t, "https://example.invalid/stale.jpg", img.URL)

	select {
	case <-refreshed:
	case <-time.After(2 * time.Second):
		t.Fatal("a stale entry must schedule a background refresh, as the blocking path did")
	}
}

// TestPrefetchAsync_HonoursTheQueueCap asserts the backstop against a pathological
// caller, and specifically that a rejected request leaves no bookkeeping behind: a
// leaked dedup entry would mark a species as permanently in flight.
func TestPrefetchAsync_HonoursTheQueueCap(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := newDeblockingCache(t, provider)

	// Saturate the counter directly rather than scheduling 256 real prefetches.
	cache.prefetchQueued.Store(maxQueuedPrefetches)

	assert.False(t, cache.PrefetchAsync("Turdus merula"))
	_, queued := cache.prefetching.Load("Turdus merula")
	assert.False(t, queued, "a rejected prefetch must not leave a dedup entry behind")
	assert.Equal(t, int64(maxQueuedPrefetches), cache.prefetchQueued.Load(),
		"a rejected prefetch must not consume a queue slot")
}

// staleEntryStore serves one image-cache row and accepts writes. datastore.Interface is
// embedded rather than implemented: any method this test does not expect the cache to
// call panics on the nil interface, which is a louder failure than a silent zero value.
type staleEntryStore struct {
	datastore.Interface
	entry *datastore.ImageCache
}

func (s *staleEntryStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	if query.ScientificName == s.entry.ScientificName && query.ProviderName == s.entry.ProviderName {
		return s.entry, nil
	}
	return nil, datastore.ErrImageCacheNotFound
}

func (s *staleEntryStore) SaveImageCache(*datastore.ImageCache) error { return nil }

// GetAllImageCaches deliberately reports nothing, so loadCachedImages does not prime
// dataMap and the DB branch under test is actually reached.
func (s *staleEntryStore) GetAllImageCaches(string) ([]datastore.ImageCache, error) {
	return nil, nil
}

// recordingProvider reports each species it was asked to fetch.
type recordingProvider struct {
	fetched chan string
	result  BirdImage
}

func (p *recordingProvider) Fetch(scientificName string) (BirdImage, error) {
	select {
	case p.fetched <- scientificName:
	default:
	}
	return p.result, nil
}

// TestClose_DoesNotWaitForAParkedPrefetch is why bgCancel runs before wg.Wait: a
// prefetch can be parked in the provider for minutes, and Close must not inherit that.
func TestClose_DoesNotWaitForAParkedPrefetch(t *testing.T) {
	t.Parallel()

	provider := newBlockingProvider(t)
	cache := InitCache(wikiProviderName, provider, nil, nil)
	t.Cleanup(func() { assert.NoError(t, cache.Close()) })

	require.True(t, cache.PrefetchAsync("Turdus merula"))
	require.Eventually(t, func() bool { return provider.calls.Load() > 0 }, 2*time.Second, 5*time.Millisecond)

	closed := make(chan error, 1)
	go func() { closed <- cache.Close() }()

	select {
	case err := <-closed:
		require.NoError(t, err)
	case <-time.After(3 * time.Second):
		t.Fatal("Close blocked on an in-flight prefetch instead of cancelling it")
	}

	// Close returning promptly is necessary but not sufficient: assert the prefetch
	// actually saw bgCtx being cancelled, rather than Close having merely stopped
	// waiting for it.
	assert.Eventually(t, provider.cancelled.Load, 2*time.Second, 5*time.Millisecond,
		"the parked prefetch must observe bgCtx being cancelled")
}
