// exhausted_species_integration_test.go: end-to-end tests verifying that the
// primary cache short-circuits the fallback chain once every registered
// provider has returned "not found" for a given species. This addresses the
// real-world symptom where species with no image (Siren, Human vocal, ...)
// caused a SQLite read on every detection cycle through the fallback path.
package imageprovider_test

import (
	"sync"
	"sync/atomic"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// fallbackPolicyAllValue is the string value for the "try every provider"
// fallback policy. Mirrors the unexported fallbackPolicyAll constant in the
// production package; declared here so test files can reference it without
// repeating the literal.
const fallbackPolicyAllValue = "all"

// countingStore wraps mockStore and records how many times GetImageCache is
// called for each provider name. This lets tests assert that the exhausted
// short-circuit prevents repeated DB reads on the fallback chain.
type countingStore struct {
	*mockStore
	totalGetCalls atomic.Int64
}

func newCountingStore() *countingStore {
	return &countingStore{
		mockStore: newMockStore(),
	}
}

func (s *countingStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	s.totalGetCalls.Add(1)
	return s.mockStore.GetImageCache(query)
}

func (s *countingStore) TotalGetCacheCalls() int64 {
	return s.totalGetCalls.Load()
}

// countingNotFoundProvider returns ErrImageNotFound for every species and
// records how many times Fetch was invoked.
type countingNotFoundProvider struct {
	callCount atomic.Int64
}

func (p *countingNotFoundProvider) Fetch(_ string) (imageprovider.BirdImage, error) {
	p.callCount.Add(1)
	return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
}

func (p *countingNotFoundProvider) Calls() int64 {
	return p.callCount.Load()
}

// countingTransientFailureProvider returns a non-ErrImageNotFound error on
// every fetch (simulating a network outage, DB error, or provider init
// failure) and records how many times Fetch was invoked. Critically, the
// returned error must NOT satisfy errors.Is(err, ErrImageNotFound).
type countingTransientFailureProvider struct {
	callCount atomic.Int64
}

func (p *countingTransientFailureProvider) Fetch(_ string) (imageprovider.BirdImage, error) {
	p.callCount.Add(1)
	// A plain sentinel error that is unrelated to ErrImageNotFound. This
	// is exactly the kind of failure that historically poisoned the
	// exhausted-species cache.
	return imageprovider.BirdImage{}, errors.Newf("simulated transient network failure").
		Component("imageprovider").
		Category(errors.CategoryNetwork).
		Build()
}

func (p *countingTransientFailureProvider) Calls() int64 {
	return p.callCount.Load()
}

// setupExhaustedCacheTest constructs a primary (avicommons) + fallback
// (wikimedia) cache pair backed by a counting store. Both providers are
// countingNotFoundProvider so every fetch yields ErrImageNotFound — the
// conditions that trigger the exhaustion path.
func setupExhaustedCacheTest(t *testing.T) (primary *imageprovider.BirdImageCache, primaryProv, fallbackProv *countingNotFoundProvider, trackedStore *countingStore) {
	t.Helper()

	// Capture and restore the global settings instance so tests in this
	// package do not bleed into each other. conf.SetTestSettings replaces a
	// process-wide singleton, so an unrestored mutation can flip the
	// fallback policy for any later test that calls conf.Setting().
	previousSettings := conf.GetSettings()
	t.Cleanup(func() {
		conf.SetTestSettings(previousSettings)
	})

	settings := conf.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = fallbackPolicyAllValue
	conf.SetTestSettings(settings)

	trackedStore = newCountingStore()
	primaryProv = &countingNotFoundProvider{}
	fallbackProv = &countingNotFoundProvider{}

	primary = imageprovider.InitCache(providerAvicommons, primaryProv, nil, trackedStore)
	t.Cleanup(func() { assert.NoError(t, primary.Close()) })

	fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProv, nil, trackedStore)
	t.Cleanup(func() { assert.NoError(t, fallbackCache.Close()) })

	registry := imageprovider.NewImageProviderRegistry()
	require.NoError(t, registry.Register(providerAvicommons, primary))
	require.NoError(t, registry.Register(providerWikimedia, fallbackCache))

	primary.SetRegistry(registry)
	fallbackCache.SetRegistry(registry)

	return primary, primaryProv, fallbackProv, trackedStore
}

// setupTransientFallbackTest constructs a primary cache that returns
// ErrImageNotFound (a legitimate "not found" condition that triggers the
// fallback chain) and a fallback cache whose provider returns a transient
// non-not-found error on every fetch. This is the exact scenario CodeRabbit
// flagged: a real outage on the fallback must NOT poison the
// exhausted-species cache for the TTL window.
func setupTransientFallbackTest(t *testing.T) (
	primary *imageprovider.BirdImageCache,
	primaryProv *countingNotFoundProvider,
	fallbackProv *countingTransientFailureProvider,
) {
	t.Helper()

	previousSettings := conf.GetSettings()
	t.Cleanup(func() {
		conf.SetTestSettings(previousSettings)
	})

	settings := conf.GetTestSettings()
	settings.Realtime.Dashboard.Thumbnails.ImageProvider = providerAvicommons
	settings.Realtime.Dashboard.Thumbnails.FallbackPolicy = fallbackPolicyAllValue
	conf.SetTestSettings(settings)

	store := newMockStore()
	primaryProv = &countingNotFoundProvider{}
	fallbackProv = &countingTransientFailureProvider{}

	primary = imageprovider.InitCache(providerAvicommons, primaryProv, nil, store)
	t.Cleanup(func() { assert.NoError(t, primary.Close()) })

	fallbackCache := imageprovider.InitCache(providerWikimedia, fallbackProv, nil, store)
	t.Cleanup(func() { assert.NoError(t, fallbackCache.Close()) })

	registry := imageprovider.NewImageProviderRegistry()
	require.NoError(t, registry.Register(providerAvicommons, primary))
	require.NoError(t, registry.Register(providerWikimedia, fallbackCache))

	primary.SetRegistry(registry)
	fallbackCache.SetRegistry(registry)

	return primary, primaryProv, fallbackProv
}

// TestExhaustedSpeciesCache_DoesNotRecordOnTransientFailure verifies the
// CORRECTNESS gate added in response to CodeRabbit feedback: when the primary
// returns ErrImageNotFound but a fallback fails with a non-not-found error
// (network error, DB error, provider init failure), the species must NOT be
// marked as exhausted. The next Get() must retry the fallback chain so the
// transient failure self-heals instead of being masked for the TTL window.
//
// Indirect observation strategy: after the first call, call Get() a second
// time. If the species had been incorrectly marked exhausted, the second
// call would short-circuit and the fallback provider's call count would NOT
// increase. With the fix in place, the fallback IS retried.
func TestExhaustedSpeciesCache_DoesNotRecordOnTransientFailure(t *testing.T) {
	const species = "Turdus merula"

	primaryCache, primaryProvider, fallbackProvider := setupTransientFallbackTest(t)

	// First call: primary returns not-found, fallback returns a transient
	// network error. The first Get must surface an error.
	_, err := primaryCache.Get(species)
	require.Error(t, err, "first Get should return an error")

	primaryAfterFirst := primaryProvider.Calls()
	fallbackAfterFirst := fallbackProvider.Calls()
	require.Equal(t, int64(1), primaryAfterFirst, "primary should be called once")
	require.Equal(t, int64(1), fallbackAfterFirst, "fallback should be called once")

	// Second call: because the fallback failure was transient (not
	// ErrImageNotFound), the exhausted-species cache must NOT have been
	// poisoned. The fallback chain must run again.
	_, err = primaryCache.Get(species)
	require.Error(t, err, "second Get should still return an error")

	assert.Equal(t, int64(2), fallbackProvider.Calls(),
		"fallback provider must be retried after a transient failure (not silently masked)")
}

// TestExhaustedSpeciesCache_RecordsAfterFallbackFails verifies that after the
// primary and fallback providers both return ErrImageNotFound, a subsequent
// Get for the same species does not re-run the providers.
func TestExhaustedSpeciesCache_RecordsAfterFallbackFails(t *testing.T) {
	const species = "Siren"

	primaryCache, primaryProvider, fallbackProvider, _ := setupExhaustedCacheTest(t)

	_, err := primaryCache.Get(species)
	require.Error(t, err, "first Get should fail because both providers return not-found")
	assert.True(t, errors.Is(err, imageprovider.ErrImageNotFound),
		"first Get error should be ErrImageNotFound, got %v", err)

	// After the first call, the primary should have been tried and the
	// fallback should have been tried via the registry.
	primaryAfterFirst := primaryProvider.Calls()
	fallbackAfterFirst := fallbackProvider.Calls()
	assert.Equal(t, int64(1), primaryAfterFirst, "primary provider should be called exactly once on first Get")
	assert.Equal(t, int64(1), fallbackAfterFirst, "fallback provider should be called exactly once on first Get")

	// Second call: exhaustion cache should short-circuit the fallback chain.
	_, err = primaryCache.Get(species)
	require.Error(t, err, "second Get should still fail (negative cache hit)")
	assert.True(t, errors.Is(err, imageprovider.ErrImageNotFound),
		"second Get error should be ErrImageNotFound, got %v", err)

	assert.Equal(t, primaryAfterFirst, primaryProvider.Calls(),
		"primary provider must not be called again after exhaustion")
	assert.Equal(t, fallbackAfterFirst, fallbackProvider.Calls(),
		"fallback provider must not be called again after exhaustion")
}

// TestExhaustedSpeciesCache_ShortCircuitsRepeatedCalls verifies that several
// repeated Get calls after exhaustion produce zero additional provider and
// datastore lookups. This directly addresses the reported symptom of a
// SQLite read every 1.5s for species like "Siren".
func TestExhaustedSpeciesCache_ShortCircuitsRepeatedCalls(t *testing.T) {
	const species = "Human vocal"

	primaryCache, primaryProvider, fallbackProvider, store := setupExhaustedCacheTest(t)

	// First call: primary tries, fails, fallback tries, fails. Exhaustion
	// is recorded. The first call makes DB lookups for both providers.
	_, err := primaryCache.Get(species)
	require.Error(t, err)

	baselineGetCalls := store.TotalGetCacheCalls()
	baselinePrimary := primaryProvider.Calls()
	baselineFallback := fallbackProvider.Calls()

	require.Positive(t, baselineGetCalls, "first call should perform at least one GetImageCache lookup")
	require.Equal(t, int64(1), baselinePrimary, "first call should hit primary provider once")
	require.Equal(t, int64(1), baselineFallback, "first call should hit fallback provider once")

	// Subsequent calls: should not touch providers or the datastore at all.
	const repeats = 5
	for i := range repeats {
		_, err := primaryCache.Get(species)
		require.Error(t, err, "repeat %d: expected ErrImageNotFound", i+1)
		assert.True(t, errors.Is(err, imageprovider.ErrImageNotFound),
			"repeat %d: error should be ErrImageNotFound, got %v", i+1, err)
	}

	assert.Equal(t, baselineGetCalls, store.TotalGetCacheCalls(),
		"no additional GetImageCache calls should occur after exhaustion")
	assert.Equal(t, baselinePrimary, primaryProvider.Calls(),
		"no additional primary provider calls should occur after exhaustion")
	assert.Equal(t, baselineFallback, fallbackProvider.Calls(),
		"no additional fallback provider calls should occur after exhaustion")
}

// TestExhaustedSpeciesCache_ConcurrentGetCalls spawns many goroutines that
// all call Get for the same exhausted species. With the fix, the provider
// chain must run at most once overall (racing goroutines may each trigger
// one provider call during the initial race, but repeated calls after
// exhaustion is recorded must be zero).
//
// This test catches both race conditions (via `go test -race`) and
// correctness regressions where the exhaustion cache fails to propagate.
func TestExhaustedSpeciesCache_ConcurrentGetCalls(t *testing.T) {
	const (
		species    = "Noise"
		goroutines = 32
	)

	primaryCache, primaryProvider, fallbackProvider, _ := setupExhaustedCacheTest(t)

	// Prime the exhaustion cache with a single call so concurrent callers
	// observe the short-circuit consistently. This is the steady-state
	// behavior we care about — preventing the "every 1.5s SQLite read"
	// symptom once the species has been marked exhausted.
	_, err := primaryCache.Get(species)
	require.Error(t, err, "priming call should return not-found")

	baselinePrimary := primaryProvider.Calls()
	baselineFallback := fallbackProvider.Calls()

	var wg sync.WaitGroup
	wg.Add(goroutines)
	errCh := make(chan error, goroutines)
	for range goroutines {
		go func() {
			defer wg.Done()
			_, err := primaryCache.Get(species)
			errCh <- err
		}()
	}
	wg.Wait()
	close(errCh)

	for e := range errCh {
		require.Error(t, e, "every concurrent Get should return an error")
		assert.True(t, errors.Is(e, imageprovider.ErrImageNotFound),
			"concurrent Get should return ErrImageNotFound, got %v", e)
	}

	assert.Equal(t, baselinePrimary, primaryProvider.Calls(),
		"concurrent Get calls must not re-invoke primary provider")
	assert.Equal(t, baselineFallback, fallbackProvider.Calls(),
		"concurrent Get calls must not re-invoke fallback provider")
}
