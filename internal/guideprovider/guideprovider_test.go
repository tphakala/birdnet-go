package guideprovider

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// noopMetrics (a no-op GuideCacheMetrics) is defined in guideprovider.go and
// reused here and in ebird_test.go.

// fakeStore is an in-memory GuideStore for tests.
type fakeStore struct {
	mu           sync.Mutex
	entries      map[string]*GuideCacheEntry
	deleteAllErr error // when set, DeleteAll fails without clearing entries
}

func newFakeStore() *fakeStore {
	return &fakeStore{entries: make(map[string]*GuideCacheEntry)}
}

func fakeKey(name, locale, provider string) string {
	return name + "|" + locale + "|" + provider
}

func (s *fakeStore) Get(_ context.Context, name, locale, provider string) (*GuideCacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if e, ok := s.entries[fakeKey(name, locale, provider)]; ok {
		cp := *e
		return &cp, nil
	}
	return nil, ErrCacheEntryNotFound
}

func (s *fakeStore) Save(_ context.Context, entry *GuideCacheEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	cp := *entry
	s.entries[fakeKey(entry.ScientificName, entry.Locale, entry.Provider)] = &cp
	return nil
}

func (s *fakeStore) GetAll(_ context.Context) ([]GuideCacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GuideCacheEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, *e)
	}
	return out, nil
}

func (s *fakeStore) GetRecent(_ context.Context, limit int) ([]GuideCacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GuideCacheEntry, 0, len(s.entries))
	for _, e := range s.entries {
		out = append(out, *e)
	}
	// Most-recently-cached first, mirroring the GORM store's ordering.
	sort.Slice(out, func(i, j int) bool { return out[i].CachedAt.After(out[j].CachedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

func (s *fakeStore) Delete(_ context.Context, name, locale, provider string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.entries, fakeKey(name, locale, provider))
	return nil
}

func (s *fakeStore) DeleteAll(_ context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.deleteAllErr != nil {
		return s.deleteAllErr
	}
	clear(s.entries)
	return nil
}

func (s *fakeStore) count() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.entries)
}

// fakeProvider is a configurable GuideProvider for tests.
type fakeProvider struct {
	name   string
	mu     sync.Mutex
	calls  int
	result *SpeciesGuide
	err    error
}

func (p *fakeProvider) Name() string { return p.name }

func (p *fakeProvider) Fetch(_ context.Context, scientificName string, _ FetchOptions) (*SpeciesGuide, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	if p.err != nil {
		return nil, p.err
	}
	g := *p.result
	g.ScientificName = scientificName
	return &g, nil
}

func (p *fakeProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

func newTestCache(t *testing.T, store GuideStore, provider GuideProvider) *GuideCache {
	t.Helper()
	c := NewGuideCache(store, noopMetrics{})
	if provider != nil {
		c.RegisterProvider(provider.Name(), provider)
	}
	t.Cleanup(c.Close)
	return c
}

// TestGuideCache_CloseRacesBackgroundSpawns exercises the spawn-vs-Close path:
// PreFetch / WarmForSpecies / Get racing a concurrent Close must never call
// wg.Add concurrently with Close's wg.Wait. Run under -race to catch a
// regression (the unguarded closed-check + wg.Go this replaced was racy).
func TestGuideCache_CloseRacesBackgroundSpawns(t *testing.T) {
	t.Parallel()
	const iterations = 50
	const spawners = 8
	for range iterations {
		store := newFakeStore()
		prov := &fakeProvider{
			name:   WikipediaProviderName,
			result: &SpeciesGuide{CommonName: "Common Blackbird", Description: "A bird."},
		}
		c := NewGuideCache(store, noopMetrics{})
		c.RegisterProvider(prov.Name(), prov)
		c.Start()

		var wg sync.WaitGroup
		for s := range spawners {
			wg.Add(1)
			go func(n int) {
				defer wg.Done()
				name := "Genus species" + strconv.Itoa(n)
				c.PreFetch(t.Context(), name)
				c.WarmForSpecies([]string{name})
				_, _ = c.Get(t.Context(), name, FetchOptions{})
			}(s)
		}

		// Close concurrently with the in-flight spawners.
		c.Close()
		wg.Wait()
		// Idempotent under concurrency.
		c.Close()
	}
}

// TestGuideCache_NilStoreGetFallsThroughToProvider verifies that a cache built
// without a DB store does not panic on the DB tier: Get skips Tier 2 and serves
// from the provider (Tier 3), populating the memory tier so a repeat call needs
// no further fetch. Guards the nil-store dereference flagged at the c.store.Get
// call in Get.
func TestGuideCache_NilStoreGetFallsThroughToProvider(t *testing.T) {
	t.Parallel()
	prov := &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird", Description: "A bird."},
	}
	c := newTestCache(t, nil, prov)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Equal(t, "Common Blackbird", g.CommonName)

	// Second call is served from the memory tier — no extra provider fetch.
	g2, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, g2)
	assert.Equal(t, 1, prov.callCount())
}

func TestGuideCache_FetchAndMemoryHit(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird", Description: "A bird."},
	}
	c := newTestCache(t, store, prov)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.Equal(t, "Common Blackbird", g.CommonName)
	assert.Equal(t, "Turdus merula", g.ScientificName)
	assert.Equal(t, WikipediaProviderName, g.SourceProvider)
	assert.Equal(t, 1, prov.callCount())
	assert.Equal(t, 1, store.count())

	// Second call: memory hit, provider not called again.
	g2, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Common Blackbird", g2.CommonName)
	assert.Equal(t, 1, prov.callCount())
}

func TestGuideCache_NegativeEntryPersisted(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &fakeProvider{name: WikipediaProviderName, err: ErrGuideNotFound}
	c := newTestCache(t, store, prov)

	g, err := c.Get(t.Context(), "Nonexistent species", FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.True(t, g.IsNegativeEntry())
	assert.Equal(t, 1, store.count(), "negative entry should be persisted")
}

func TestGuideCache_TransientErrorNotPersisted(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &fakeProvider{name: WikipediaProviderName, err: NewTransientError(stubError("boom"))}
	c := newTestCache(t, store, prov)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.Error(t, err)
	assert.Nil(t, g)
	assert.Equal(t, 0, store.count(), "transient failure must not persist a negative entry")
}

// TestGuideCache_PlainProviderErrorNotNegativeCached verifies that a provider
// failure that is neither a definitive not-found nor explicitly transient (e.g. a
// 403 UA rejection or a response-decode error on a malformed 200) does NOT persist
// a negative entry and does NOT surface as not-found. Only a clean not-found may be
// cached negative; otherwise a recoverable failure would suppress retries for a
// species that exists for up to NegativeTTL.
func TestGuideCache_PlainProviderErrorNotNegativeCached(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &fakeProvider{name: WikipediaProviderName, err: stubError("boom")}
	c := newTestCache(t, store, prov)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.Error(t, err)
	assert.Nil(t, g)
	assert.False(t, errors.Is(err, ErrGuideNotFound), "a provider failure must not surface as not-found")
	assert.Equal(t, 0, store.count(), "a non-definitive failure must not persist a negative entry")
}

func TestGuideCache_StaleWhileRevalidate(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	// Seed a stale positive entry directly in the store.
	require.NoError(t, store.Save(t.Context(), &GuideCacheEntry{
		ScientificName: "Turdus merula",
		Locale:         "en",
		Provider:       WikipediaProviderName,
		CommonName:     "Old Name",
		Description:    "old",
		CachedAt:       time.Now().Add(-PositiveTTL - time.Hour),
	}))
	prov := &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Fresh Name", Description: "fresh"},
	}
	c := newTestCache(t, store, prov)

	// Stale DB hit returns immediately with the old data...
	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Old Name", g.CommonName)

	// ...and triggers a background refresh that eventually updates the store.
	require.Eventually(t, func() bool {
		e, gErr := store.Get(t.Context(), "Turdus merula", "en", WikipediaProviderName)
		return gErr == nil && e.CommonName == "Fresh Name"
	}, 3*time.Second, 20*time.Millisecond)
}

// blockingProvider blocks inside Fetch until release is closed, so a test can
// hold a single fetch in-flight while many concurrent refreshes pile up behind
// the singleflight group. It records how many times Fetch was actually entered.
type blockingProvider struct {
	name    string
	mu      sync.Mutex
	calls   int
	started chan struct{} // closed once, when the first Fetch begins
	release chan struct{} // close to unblock the in-flight Fetch(es)
	once    sync.Once
	result  *SpeciesGuide
}

func (p *blockingProvider) Name() string { return p.name }

func (p *blockingProvider) Fetch(ctx context.Context, scientificName string, _ FetchOptions) (*SpeciesGuide, error) {
	p.mu.Lock()
	p.calls++
	p.mu.Unlock()
	p.once.Do(func() { close(p.started) })
	select {
	case <-p.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	g := *p.result
	g.ScientificName = scientificName
	return &g, nil
}

func (p *blockingProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.calls
}

// TestGuideCache_AsyncRefreshIsDeduplicated verifies that a burst of concurrent
// reads of one stale entry collapses to a single provider fetch. The async
// refresh is routed through the singleflight group, so the in-flight fetch
// absorbs every concurrent refresh instead of issuing one external call per
// reader (the thundering-herd guard). Regression test for that routing.
func TestGuideCache_AsyncRefreshIsDeduplicated(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &blockingProvider{
		name:    WikipediaProviderName,
		started: make(chan struct{}),
		release: make(chan struct{}),
		result:  &SpeciesGuide{CommonName: "Fresh", Description: "fresh"},
	}
	c := newTestCache(t, store, prov)

	// Seed a stale entry straight into the memory tier so every Get is a stale
	// memory hit that fires a background refresh (no synchronous fetch).
	c.storeMemory(cacheKey("Turdus merula", defaultLocale), &SpeciesGuide{
		ScientificName: "Turdus merula",
		CommonName:     "Stale",
		Description:    "stale",
		CachedAt:       time.Now().Add(-PositiveTTL - time.Hour),
	})

	const readers = 32
	var wg sync.WaitGroup
	for range readers {
		wg.Go(func() {
			// Each stale memory hit returns immediately and fires a background
			// refresh; whether it observes the stale or the just-refreshed value
			// is timing-dependent, so only the dedup (calls==1 below) is asserted.
			_, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
			assert.NoError(t, err)
		})
	}

	// Once the single coalesced refresh has entered Fetch, no other reader may
	// have spawned its own fetch: singleflight keeps exactly one leader in-flight
	// while it is blocked, and followers never call Fetch.
	<-prov.started
	assert.Equal(t, 1, prov.callCount(), "concurrent stale reads must collapse to one refresh fetch")

	close(prov.release)
	wg.Wait()
}

func TestGuideCache_GetAfterCloseStillReads(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Blackbird", Description: "desc"},
	}
	c := NewGuideCache(store, noopMetrics{})
	c.RegisterProvider(prov.Name(), prov)
	c.Start()

	_, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)

	c.Close()

	// Reads must still succeed from memory after Close.
	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Blackbird", g.CommonName)
}

func TestGuideCache_FallbackMergesProviders(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c := NewGuideCache(store, noopMetrics{})
	c.SetFallbackPolicy(conf.SpeciesGuideFallbackAll)
	// Production ordering: OpenFauna is the primary (offline taxonomy + common name);
	// Wikipedia is the secondary that fills the description and its attribution.
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird", Genus: "Turdus", Family: "Turdidae"},
	})
	c.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name: WikipediaProviderName,
		result: &SpeciesGuide{
			Description: "Wikipedia prose.",
			SourceURL:   "https://en.wikipedia.org/wiki/Turdus_merula",
			License:     "CC BY-SA 4.0",
			LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		},
	})
	t.Cleanup(c.Close)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Common Blackbird", g.CommonName, "primary (OpenFauna) common name wins")
	assert.Equal(t, "Turdus", g.Genus, "primary taxonomy retained")
	assert.Equal(t, "Turdidae", g.Family, "primary taxonomy retained")
	assert.Equal(t, "Wikipedia prose.", g.Description, "secondary fills the description gap")
	assert.Equal(t, "CC BY-SA 4.0", g.License, "Wikipedia attribution carried with the prose")
	assert.Equal(t, "https://en.wikipedia.org/wiki/Turdus_merula", g.SourceURL,
		"Wikipedia source URL carried with the prose")
}

func TestGuideCache_SecondaryNotFoundDoesNotMarkPartial(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c := NewGuideCache(store, noopMetrics{})
	c.SetFallbackPolicy(conf.SpeciesGuideFallbackAll)
	// OpenFauna (primary) resolves the species offline.
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird", Genus: "Turdus", Family: "Turdidae"},
	})
	// Wikipedia (the secondary description provider) has no article for this species.
	c.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name: WikipediaProviderName,
		err:  ErrGuideNotFound,
	})
	t.Cleanup(c.Close)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.False(t, g.Partial,
		"a secondary provider with no entry must not downgrade a complete primary guide")
}

func TestGuideCache_TransientSecondaryMarksPartial(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c := NewGuideCache(store, noopMetrics{})
	c.SetFallbackPolicy(conf.SpeciesGuideFallbackAll)
	// OpenFauna (primary) resolves the species offline.
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird", Genus: "Turdus", Family: "Turdidae"},
	})
	// Wikipedia (the secondary description provider) fails for a transient reason:
	// the merged guide is marked partial.
	c.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name: WikipediaProviderName,
		err:  NewTransientError(stubError("boom")),
	})
	t.Cleanup(c.Close)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.True(t, g.Partial, "a transient secondary failure must mark the guide partial")
}

func TestGuideCache_HasProvider(t *testing.T) {
	t.Parallel()
	c := NewGuideCache(newFakeStore(), noopMetrics{})
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{name: OpenFaunaProviderName})

	assert.True(t, c.HasProvider(OpenFaunaProviderName))
	assert.False(t, c.HasProvider(WikipediaProviderName), "unregistered provider reports absent")
	assert.False(t, (*GuideCache)(nil).HasProvider(OpenFaunaProviderName), "nil cache is safe")
}

func TestGuideCache_InvalidateAll(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c := NewGuideCache(store, noopMetrics{})
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Blackbird", Genus: "Turdus"},
	})
	t.Cleanup(c.Close)

	// A fetch populates both the DB and memory tiers.
	_, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.Positive(t, store.count(), "fetch should persist an entry")

	require.NoError(t, c.InvalidateAll(t.Context()))

	assert.Zero(t, store.count(), "DB tier cleared")
	memEntries := 0
	c.memory.Range(func(_, _ any) bool { memEntries++; return true })
	assert.Zero(t, memEntries, "memory tier cleared")
	assert.Zero(t, c.memCount.Load(), "memory count reset")
}

// TestGuideCache_InvalidateAll_DBFailureKeepsMemory verifies the persistent tier is
// cleared first: when DeleteAll fails, the memory tier is left intact so the two
// tiers stay consistent (both populated) instead of leaving memory empty while stale
// rows survive in the DB to reload on the next restart.
func TestGuideCache_InvalidateAll_DBFailureKeepsMemory(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	store.deleteAllErr = errors.NewStd("db unavailable")
	c := NewGuideCache(store, noopMetrics{})
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Blackbird", Genus: "Turdus"},
	})
	t.Cleanup(c.Close)

	_, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.Positive(t, store.count(), "fetch should persist an entry")

	err = c.InvalidateAll(t.Context())
	require.Error(t, err, "DB failure surfaces to the caller")

	memEntries := 0
	c.memory.Range(func(_, _ any) bool { memEntries++; return true })
	assert.Positive(t, memEntries, "memory tier preserved when DB delete fails")
	assert.Positive(t, c.memCount.Load(), "memory count unchanged when DB delete fails")
}

func TestIsCacheEntryStale(t *testing.T) {
	t.Parallel()
	c := &GuideCache{}

	tests := []struct {
		name string
		g    *SpeciesGuide
		want bool
	}{
		{"nil is stale", nil, true},
		{"fresh positive", &SpeciesGuide{CachedAt: time.Now()}, false},
		{"stale positive", &SpeciesGuide{CachedAt: time.Now().Add(-PositiveTTL - time.Minute)}, true},
		{"fresh negative", &SpeciesGuide{Negative: true, CachedAt: time.Now()}, false},
		{"stale negative", &SpeciesGuide{Negative: true, CachedAt: time.Now().Add(-NegativeTTL - time.Minute)}, true},
		{"positive within neg TTL but past neg TTL is still fresh", &SpeciesGuide{CachedAt: time.Now().Add(-time.Hour)}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, c.isCacheEntryStale(tt.g))
		})
	}
}

func TestMergeGuides(t *testing.T) {
	t.Parallel()

	// OpenFauna-like primary (taxonomy, no prose/source) + Wikipedia-like secondary
	// (prose with CC BY-SA attribution). The merge keeps the primary's taxonomy and
	// common name, fills the description, and carries the description's source URL and
	// license from the secondary so the prose stays correctly attributed.
	primary := &SpeciesGuide{CommonName: "Primary", Genus: "Turdus", Family: "Turdidae"}
	secondary := &SpeciesGuide{
		CommonName:  "Secondary",
		Description: "filled",
		SourceURL:   "https://de.wikipedia.org/wiki/Turdus_merula",
		License:     "CC BY-SA 4.0",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
	}
	merged := mergeGuides(primary, secondary)
	assert.Equal(t, "Primary", merged.CommonName, "primary common name wins")
	assert.Equal(t, "Turdus", merged.Genus, "primary taxonomy retained")
	assert.Equal(t, "filled", merged.Description, "empty primary field filled by secondary")
	assert.Equal(t, secondary.SourceURL, merged.SourceURL, "prose source URL carried from secondary")
	assert.Equal(t, secondary.License, merged.License, "prose license carried from secondary")
	assert.Equal(t, secondary.LicenseURL, merged.LicenseURL, "prose license URL carried from secondary")

	// A primary that already has source/license keeps its own (not overwritten).
	primaryWithSource := &SpeciesGuide{SourceURL: "https://primary.example", License: "primary-license"}
	keep := mergeGuides(primaryWithSource, secondary)
	assert.Equal(t, "https://primary.example", keep.SourceURL, "primary source URL is not overwritten")
	assert.Equal(t, "primary-license", keep.License, "primary license is not overwritten")

	assert.Equal(t, secondary, mergeGuides(nil, secondary))
	freshPrimary := &SpeciesGuide{CommonName: "P"}
	assert.Equal(t, freshPrimary, mergeGuides(freshPrimary, nil))
}

func TestTruncateDescription(t *testing.T) {
	t.Parallel()
	short := "short"
	assert.Equal(t, short, truncateDescription(short))

	long := strings.Repeat("a", maxDescriptionLength+500)
	got := truncateDescription(long)
	assert.LessOrEqual(t, len(got), maxDescriptionLength)
}

func TestTrimToUTF8Boundary(t *testing.T) {
	t.Parallel()
	// "héllo" — 'é' is two bytes (0xC3 0xA9). Cutting at byte 2 must back off
	// to a rune boundary so no partial rune is produced.
	s := "héllo"
	got := TrimToUTF8Boundary(s, 2)
	assert.True(t, utf8ValidString(got))
	assert.Equal(t, "h", got)
}

func TestNormalizeHelpers(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "Turdus merula", normalizeScientificName("  Turdus merula  "))
	assert.Equal(t, "en", normalizeLocale(""))
	assert.Equal(t, "de", normalizeLocale(" de "))

	name, locale := splitCacheKey(cacheKey("Turdus merula", "de"))
	assert.Equal(t, "Turdus merula", name)
	assert.Equal(t, "de", locale)
}

func TestNormalizeLocale_Validation(t *testing.T) {
	t.Parallel()
	tests := []struct {
		in, want string
	}{
		{"en", "en"},
		{"de", "de"},
		{" PT-BR ", "pt-br"},             // trimmed + lowercased
		{"zh-hans", "zh-hans"},           // 4-letter subtag allowed
		{"be-tarask", "be-tarask"},       // 6-letter subtag (real Wikipedia subdomain)
		{"zh-classical", "zh-classical"}, // 9-letter subtag (real Wikipedia subdomain)
		{"zh-min-nan", "zh-min-nan"},     // two subtags (real Wikipedia subdomain)
		{"", "en"},                       // empty -> default
		{"english", "en"},                // too long, no subtag -> default
		{"ab-cd-ef-gh", "en"},            // more than two subtags -> default
		{"en-superlongsubtag", "en"},     // subtag exceeds 10 chars -> default
		{"en_US", "en"},                  // underscore not allowed -> default
		{"../etc", "en"},                 // path traversal attempt -> default
		{"@evil.com", "en"},              // host-injection attempt -> default
		{"en.wikipedia", "en"},           // dotted -> default
		{"a", "en"},                      // too short -> default
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, normalizeLocale(tt.in), "normalizeLocale(%q)", tt.in)
	}
}

func TestStoreMemory_Caps(t *testing.T) {
	t.Parallel()
	c := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(c.Close)

	// Store well past the cap with distinct keys.
	for i := range maxMemoryEntries + 500 {
		c.storeMemory(cacheKey("species", strconvI(i)), &SpeciesGuide{CommonName: "x"})
	}
	count := 0
	c.memory.Range(func(_, _ any) bool {
		count++
		return true
	})
	assert.Equal(t, maxMemoryEntries, count, "memory tier must fill exactly to the cap")
	// The counter must exactly track the map size (no overshoot / drift).
	assert.Equal(t, int64(count), c.memCount.Load(), "memCount must equal the actual entry count")

	// Updating an existing key must not change the count or be rejected.
	c.storeMemory(cacheKey("species", strconvI(0)), &SpeciesGuide{CommonName: "updated"})
	v, ok := c.memory.Load(cacheKey("species", strconvI(0)))
	assert.True(t, ok)
	g, _ := v.(*SpeciesGuide)
	assert.Equal(t, "updated", g.CommonName)
	assert.Equal(t, int64(maxMemoryEntries), c.memCount.Load(), "updating a key must not change the count")
}

// TestStoreMemory_ExactCountUnderConcurrency drives concurrent distinct-key
// writes past the cap and asserts the lock-free reserve-then-rollback keeps the
// entry count exactly at the cap (no overshoot) and memCount in sync. Run under
// -race to catch a regression in the atomic reservation.
func TestStoreMemory_ExactCountUnderConcurrency(t *testing.T) {
	t.Parallel()
	c := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(c.Close)

	const writers = 16
	const perWriter = 1000 // 16k distinct keys >> maxMemoryEntries (5000)
	var wg sync.WaitGroup
	for w := range writers {
		wg.Go(func() {
			for i := range perWriter {
				key := cacheKey("species", strconvI(w*perWriter+i))
				c.storeMemory(key, &SpeciesGuide{CommonName: "x"})
			}
		})
	}
	wg.Wait()

	count := 0
	c.memory.Range(func(_, _ any) bool {
		count++
		return true
	})
	assert.Equal(t, maxMemoryEntries, count, "concurrent writes must not overshoot the cap")
	assert.Equal(t, int64(count), c.memCount.Load(), "memCount must stay exact under concurrency")
}

// TestRefreshStaleEntries_EvictsExpiredNegatives verifies the refresh sweep drops
// expired negative entries from memory (freeing slots) while keeping fresh ones.
func TestRefreshStaleEntries_EvictsExpiredNegatives(t *testing.T) {
	t.Parallel()
	c := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(c.Close)

	// A stale negative entry (past NegativeTTL) and a fresh positive one.
	c.storeMemory(cacheKey("Gone species", defaultLocale), &SpeciesGuide{
		ScientificName: "Gone species",
		Negative:       true,
		CachedAt:       time.Now().Add(-NegativeTTL - time.Hour),
	})
	c.storeMemory(cacheKey("Turdus merula", defaultLocale), &SpeciesGuide{
		ScientificName: "Turdus merula",
		CommonName:     "Common Blackbird",
		CachedAt:       time.Now(),
	})
	require.Equal(t, int64(2), c.memCount.Load())

	c.refreshStaleEntries()

	_, negStillThere := c.memory.Load(cacheKey("Gone species", defaultLocale))
	assert.False(t, negStillThere, "expired negative entry must be evicted")
	_, posStillThere := c.memory.Load(cacheKey("Turdus merula", defaultLocale))
	assert.True(t, posStillThere, "fresh positive entry must be retained")
	assert.Equal(t, int64(1), c.memCount.Load(), "counter must reflect the eviction")
}

func strconvI(i int) string {
	return strconv.Itoa(i)
}

// stubError is a tiny error helper for tests.
type stubError string

func (e stubError) Error() string { return string(e) }

func utf8ValidString(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}
