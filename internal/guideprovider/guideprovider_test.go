package guideprovider

import (
	"context"
	"sort"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf8"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/errors"
)

// noopMetrics (a no-op GuideCacheMetrics) is defined in guideprovider.go and
// reused here and in ebird_test.go.

// fakeStore is an in-memory GuideStore for tests.
type fakeStore struct {
	mu      sync.Mutex
	entries map[string]*GuideCacheEntry
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

func (s *fakeStore) GetRecent(_ context.Context, limit int, providerSet string) ([]GuideCacheEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GuideCacheEntry, 0, len(s.entries))
	for _, e := range s.entries {
		// Mirror the GORM store's provider-set filter: rows written by a different
		// provider set must not seed this cache's memory tier.
		if providerSet != "" && e.Provider != providerSet {
			continue
		}
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

// closableProvider is a fakeProvider that records whether Close was called and
// can report a failure from it.
type closableProvider struct {
	fakeProvider
	closeErr error
	closed   atomic.Int32
}

func (p *closableProvider) Close() error {
	p.closed.Add(1)
	return p.closeErr
}

// TestGuideCache_CloseReleasesProviders pins the provider-teardown contract.
// GuideCache.Close probes each registered provider for an optional Close, which
// went untested while no provider implemented one — the Wikipedia provider now
// does (it owns a pooled HTTP client), so a regression here silently leaks its
// connections on every hot-reload rebuild.
func TestGuideCache_CloseReleasesProviders(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name     string
		closeErr error
	}{
		{name: "clean close"},
		{name: "failing close is survivable", closeErr: errors.NewStd("boom")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			prov := &closableProvider{
				name:     WikipediaProviderName,
				result:   &SpeciesGuide{CommonName: "x"},
				closeErr: tc.closeErr,
			}
			c := NewGuideCache(newFakeStore(), noopMetrics{})
			c.RegisterProvider(prov.Name(), prov)
			c.Start()

			c.Close()
			assert.Equal(t, int32(1), prov.closed.Load(), "the provider must be closed exactly once")

			// Close is documented as safe to call repeatedly, and must not close
			// the provider a second time.
			c.Close()
			assert.Equal(t, int32(1), prov.closed.Load(), "repeat Close must not re-close providers")
		})
	}
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
	prov := &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Fresh Name", Description: "fresh"},
	}
	c := newTestCache(t, store, prov)
	// Seed a stale positive entry directly in the store, under the key the cache
	// actually reads. Writing the bare provider name here would silently miss.
	require.NoError(t, store.Save(t.Context(), &GuideCacheEntry{
		ScientificName: "Turdus merula",
		Locale:         "en",
		Provider:       c.providerSetKey(),
		CommonName:     "Old Name",
		Description:    "old",
		CachedAt:       time.Now().Add(-PositiveTTL - time.Hour),
	}))

	// Stale DB hit returns immediately with the old data...
	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Old Name", g.CommonName)

	// ...and triggers a background refresh that eventually updates the store.
	require.Eventually(t, func() bool {
		e, gErr := store.Get(t.Context(), "Turdus merula", "en", c.providerSetKey())
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
		// A regional subtag survives ONLY when it can change the answer: a real
		// hyphenated Wikipedia edition, or a locale the embedded dataset carries
		// (which supplies regional common names).
		{" EN-UK ", "en-uk"},                         // trimmed + lowercased; en_uk is a dataset locale
		{"en_US", "en-us"},                           // underscore spelling canonicalized to the same key
		{wpEditionBeTarask, wpEditionBeTarask},       // 6-letter subtag (real Wikipedia subdomain)
		{wpEditionZhClassical, wpEditionZhClassical}, // 9-letter subtag (real Wikipedia subdomain)
		{wpEditionZhMinNan, wpEditionZhMinNan},       // two subtags (real Wikipedia subdomain)
		// Regional subtags with no dedicated Wikipedia edition and no dataset locale
		// collapse to their base language. Both providers already resolved them that
		// way, so keeping them distinct only minted duplicate keys holding identical
		// content — unbounded, since any well-formed subtag was accepted.
		{" PT-BR ", "pt"},            // no pt_br in the dataset, no pt-br.wikipedia.org
		{"zh-hans", "zh"},            // script subtag: not an edition, not a dataset locale
		{"en-superlongsubtag", "en"}, // subtag exceeds 10 chars -> default
		{"", "en"},                   // empty -> default
		{"english", "en"},            // too long, no subtag -> default
		{"ab-cd-ef-gh", "en"},        // more than two subtags -> default
		{"../etc", "en"},             // path traversal attempt -> default
		{"@evil.com", "en"},          // host-injection attempt -> default
		{"en.wikipedia", "en"},       // dotted -> default
		{"a", "en"},                  // too short -> default
	}
	for _, tt := range tests {
		assert.Equalf(t, tt.want, normalizeLocale(tt.in), "normalizeLocale(%q)", tt.in)
	}
}

func (c *GuideCache) memLen() int {
	c.memMu.RLock()
	defer c.memMu.RUnlock()
	return len(c.memory)
}

// TestStoreMemory_CapEvictsRatherThanFreezing pins the fix for a cache that could
// wedge permanently. The tier used to REFUSE every new key at the cap, and positive
// entries are refreshed in place and never removed — so once full it never admitted
// another species for the life of the process, turning every subsequent lookup into
// a guaranteed miss with no way to recover.
func TestStoreMemory_CapEvictsRatherThanFreezing(t *testing.T) {
	t.Parallel()
	c := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(c.Close)

	// Store well past the cap with distinct keys.
	for i := range maxMemoryEntries + 500 {
		c.storeMemory(cacheKey("species", strconvI(i)), &SpeciesGuide{CommonName: "x"})
	}
	assert.Equal(t, maxMemoryEntries, c.memLen(), "memory tier must stay bounded at the cap")

	// The tier is full. A brand-new key must still be admitted (evicting a victim),
	// not silently dropped.
	newKey := cacheKey("Turdus merula", defaultLocale)
	c.storeMemory(newKey, &SpeciesGuide{CommonName: "Common Blackbird"})
	got, ok := c.lookupMemory(newKey)
	require.True(t, ok, "a full tier must still admit a new species by evicting one")
	assert.Equal(t, "Common Blackbird", got.CommonName)
	assert.Equal(t, maxMemoryEntries, c.memLen(), "eviction must hold the tier at the cap")

	// Updating an existing key replaces in place and must not evict anything.
	c.storeMemory(newKey, &SpeciesGuide{CommonName: "updated"})
	got, ok = c.lookupMemory(newKey)
	require.True(t, ok)
	assert.Equal(t, "updated", got.CommonName)
	assert.Equal(t, maxMemoryEntries, c.memLen(), "updating a key must not change the entry count")
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
	require.Equal(t, 2, c.memLen())

	c.refreshStaleEntries()

	_, negStillThere := c.lookupMemory(cacheKey("Gone species", defaultLocale))
	assert.False(t, negStillThere, "expired negative entry must be evicted")
	_, posStillThere := c.lookupMemory(cacheKey("Turdus merula", defaultLocale))
	assert.True(t, posStillThere, "fresh positive entry must be retained")
	assert.Equal(t, 1, c.memLen(), "entry count must reflect the eviction")
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

// --- Background prefetch / warm ---

func TestGuideCache_PreFetchWarmsThenIsReadable(t *testing.T) {
	store := newFakeStore()
	prov := &fakeProvider{name: WikipediaProviderName, result: &SpeciesGuide{CommonName: "Blackbird", Description: "A thrush."}}
	c := newTestCache(t, store, prov)

	c.PreFetch(t.Context(), "Turdus merula")
	c.PreFetch(nil, "Corvus corone") //nolint:staticcheck // nil ctx exercises the cache-context fallback
	c.PreFetch(t.Context(), "")      // empty name: no-op

	// Wait for the background fetches to land before the cleanup Close (which
	// would otherwise cancel in-flight warms via shouldQuit).
	require.Eventually(t, func() bool { return prov.callCount() >= 2 }, 2*time.Second, 5*time.Millisecond)
	assert.Positive(t, store.count(), "prefetched guides are persisted")
}

func TestGuideCache_PreFetchOnClosedCacheIsNoop(t *testing.T) {
	c := newTestCache(t, newFakeStore(), &fakeProvider{name: WikipediaProviderName, result: &SpeciesGuide{Description: "x"}})
	c.Close()
	c.PreFetch(t.Context(), "Turdus merula") // must not panic or spawn work
}

func TestGuideCache_WarmForSpeciesUpdatesPopulationRatio(t *testing.T) {
	store := newFakeStore()
	prov := &fakeProvider{name: WikipediaProviderName, result: &SpeciesGuide{CommonName: "X", Description: "y"}}
	c := newTestCache(t, store, prov)
	c.SetWarmTopN(2)

	c.WarmForSpecies([]string{"Turdus merula", "Corvus corone", "  "}) // blank is skipped
	// Wait for the warm goroutine to fetch both; it then runs
	// updateCachePopulationRatio before finishing (drained by the cleanup Close).
	require.Eventually(t, func() bool { return prov.callCount() >= 2 }, 2*time.Second, 5*time.Millisecond)
}

func TestGuideCache_WarmForSpeciesGuards(t *testing.T) {
	c := newTestCache(t, newFakeStore(), &fakeProvider{name: WikipediaProviderName, result: &SpeciesGuide{Description: "x"}})
	c.WarmForSpecies(nil)               // empty slice: no-op
	c.WarmForSpecies([]string{"", " "}) // all-blank after normalization: no-op
	c.Close()
	c.WarmForSpecies([]string{"Turdus merula"}) // closed: no-op
}

// --- Nil-receiver and empty-arg guards on the setup methods ---

func TestGuideCache_SetupMethodGuards(t *testing.T) {
	t.Parallel()
	var nilCache *GuideCache
	nilCache.SetWarmTopN(5)      // nil receiver: no-op
	nilCache.SetWarmLocale("de") // nil receiver: no-op
	nilCache.RegisterProvider("x", &fakeProvider{name: "x"})
	nilCache.PreFetch(t.Context(), "x")    // nil receiver: no-op
	nilCache.WarmForSpecies([]string{"x"}) // nil receiver: no-op

	c := newTestCache(t, newFakeStore(), nil)
	c.RegisterProvider("", &fakeProvider{name: "x"}) // empty name: skipped
	c.RegisterProvider("x", nil)                     // nil provider: skipped
	assert.False(t, c.HasProvider("x"), "no provider should have been registered")
}

func TestResolveProviderName(t *testing.T) {
	t.Parallel()
	// No providers: falls back to the default provider name.
	c := newTestCache(t, newFakeStore(), nil)
	assert.NotEmpty(t, c.resolveProviderName())

	// First registered provider wins as the primary/DB-key provider.
	c2 := newTestCache(t, newFakeStore(), nil)
	c2.RegisterProvider(OpenFaunaProviderName, &fakeProvider{name: OpenFaunaProviderName})
	c2.RegisterProvider(WikipediaProviderName, &fakeProvider{name: WikipediaProviderName})
	assert.Equal(t, OpenFaunaProviderName, c2.resolveProviderName())
}

// --- Pure helpers ---

func TestSplitCacheKey(t *testing.T) {
	t.Parallel()
	name, locale := splitCacheKey(cacheKey("Turdus merula", "en"))
	assert.Equal(t, "Turdus merula", name)
	assert.Equal(t, "en", locale)

	// A key without the separator yields the whole string as the name and the
	// default locale.
	n2, l2 := splitCacheKey("noseparator")
	assert.Equal(t, "noseparator", n2)
	assert.Equal(t, defaultLocale, l2)
}

// TestEntryQuality pins the metrics classification to the SHARED rule in
// ClassifyQuality, so the cache-quality metric and the API's user-facing badge cannot
// drift apart: prose without section headers is intro_only for both, not "full" for
// one and "intro_only" for the other.
func TestEntryQuality(t *testing.T) {
	t.Parallel()
	longProse := strings.Repeat("a", 250)

	assert.Equal(t, qualityNegative, entryQuality(&SpeciesGuide{Negative: true}),
		"a negative entry is classified before any description rule")
	assert.Equal(t, qualityStub, entryQuality(&SpeciesGuide{Description: ""}),
		"no description is a stub")
	assert.Equal(t, qualityStub, entryQuality(&SpeciesGuide{Description: "short intro"}),
		"under the stub threshold is a stub, not intro_only")
	assert.Equal(t, qualityIntroOnly, entryQuality(&SpeciesGuide{Partial: true, Description: longProse + SectionMarker + "Voice\nSings."}),
		"a failed provider downgrades even sectioned prose to intro_only")
	assert.Equal(t, qualityIntroOnly, entryQuality(&SpeciesGuide{Description: longProse}),
		"long prose with no section headers is intro_only")
	assert.Equal(t, qualityFull, entryQuality(&SpeciesGuide{Description: longProse + SectionMarker + "Voice\nSings."}),
		"long prose with at least one section header is full")
}

// TestClassifyQuality_MatchesEntryQuality guards the unification itself: entryQuality
// must delegate rather than re-implement, so a future threshold change cannot be
// applied to one classifier and forgotten in the other.
func TestClassifyQuality_MatchesEntryQuality(t *testing.T) {
	t.Parallel()
	for _, desc := range []string{
		"",
		"tiny",
		strings.Repeat("a", DescriptionStubMaxLength-1),
		strings.Repeat("a", 250),
		strings.Repeat("a", 250) + SectionMarker + "Voice\nSings.",
	} {
		for _, partial := range []bool{false, true} {
			assert.Equal(t, ClassifyQuality(desc, partial),
				entryQuality(&SpeciesGuide{Description: desc, Partial: partial}),
				"entryQuality must agree with ClassifyQuality (len=%d partial=%v)", len(desc), partial)
		}
	}
}

func TestTrimToUTF8Boundary_EdgeCases(t *testing.T) {
	t.Parallel()
	assert.Equal(t, "abc", TrimToUTF8Boundary("abc", 10), "under the cap is unchanged")
	assert.Equal(t, "ab", TrimToUTF8Boundary("abcd", 2), "ASCII trims at the cap")
	// "é" is two bytes: a cap landing mid-rune must back off to a rune boundary.
	trimmed := TrimToUTF8Boundary("aéb", 2)
	assert.True(t, utf8.ValidString(trimmed), "result stays valid UTF-8")
	assert.LessOrEqual(t, len(trimmed), 2)
}

// TestOpenFaunaProvider_EmbeddedLookup exercises the production embeddedOpenFauna
// lookup (Meta + CommonName over the vendored dataset), not a stub.
func TestOpenFaunaProvider_EmbeddedLookup(t *testing.T) {
	t.Parallel()
	p := NewOpenFaunaGuideProviderWithMetrics(noopMetrics{})

	g, err := p.Fetch(t.Context(), "Turdus merula", FetchOptions{Locale: "en"})
	require.NoError(t, err)
	assert.Equal(t, "Turdus", g.Genus, "genus is the binomial's first token")
	assert.Equal(t, OpenFaunaProviderName, g.SourceProvider)

	// A species absent from the embedded dataset yields ErrGuideNotFound so it
	// never downgrades an otherwise-complete primary guide.
	_, err = p.Fetch(t.Context(), "Notarealgenus fakename", FetchOptions{Locale: "en"})
	assert.True(t, errors.Is(err, ErrGuideNotFound))
}

// localeSpyProvider records the FetchOptions.Locale of each Fetch so tests can assert
// which locale the warm/pre-fetch paths request.
type localeSpyProvider struct {
	name    string
	locales chan string
	result  *SpeciesGuide
}

func (p *localeSpyProvider) Name() string { return p.name }

func (p *localeSpyProvider) Fetch(_ context.Context, scientificName string, opts FetchOptions) (*SpeciesGuide, error) {
	select {
	case p.locales <- opts.Locale:
	default:
	}
	g := *p.result
	g.ScientificName = scientificName
	return &g, nil
}

// TestGuideCache_WarmForSpecies_UsesWarmLocale guards [15] F1: startup warming must
// fetch the configured dashboard locale (SetWarmLocale), not the default "en", so the
// warmed entry keys to the locale the UI will actually request.
func TestGuideCache_WarmForSpecies_UsesWarmLocale(t *testing.T) {
	t.Parallel()
	prov := &localeSpyProvider{
		name:    OpenFaunaProviderName,
		locales: make(chan string, 4),
		result:  &SpeciesGuide{Description: "desc", CachedAt: time.Now()},
	}
	c := newTestCache(t, newFakeStore(), prov)
	c.SetWarmLocale("de")

	c.WarmForSpecies([]string{"Turdus merula"})

	select {
	case got := <-prov.locales:
		assert.Equal(t, "de", got, "warm fetch must use the configured dashboard locale, not the default")
	case <-time.After(2 * time.Second):
		t.Fatal("warm fetch did not occur")
	}
}

// TestGuideCache_PreFetch_UsesWarmLocale guards [15] F1 for the per-detection pre-fetch
// path: it too must fetch the configured dashboard locale.
func TestGuideCache_PreFetch_UsesWarmLocale(t *testing.T) {
	t.Parallel()
	prov := &localeSpyProvider{
		name:    OpenFaunaProviderName,
		locales: make(chan string, 4),
		result:  &SpeciesGuide{Description: "desc", CachedAt: time.Now()},
	}
	c := newTestCache(t, newFakeStore(), prov)
	c.SetWarmLocale("fr")

	c.PreFetch(t.Context(), "Turdus merula")

	select {
	case got := <-prov.locales:
		assert.Equal(t, "fr", got, "pre-fetch must use the configured dashboard locale, not the default")
	case <-time.After(2 * time.Second):
		t.Fatal("pre-fetch did not occur")
	}
}

// TestGuideCache_SaveGuide_SkipsWriteOnCancelledContext pins the guard that keeps a
// retired cache from re-polluting the shared table after a reconfigure.
//
// singleflight spawns goroutines the wait group does not track, so Close can return
// while one sits between fetchFromProviders (whose provider read lock Close does wait
// on) and the DB write. Every fetchAndStore runs on c.ctx, which Close cancels first,
// so the write must be skipped — otherwise the straggler re-inserts a row under the
// retired provider set after handleReconfigureSpeciesGuide invalidated the table, and
// the freshly built cache serves it.
//
// fakeStore deliberately ignores the context, so this asserts the CACHE enforces the
// guard rather than the database driver doing it incidentally.
func TestGuideCache_SaveGuide_SkipsWriteOnCancelledContext(t *testing.T) {
	t.Parallel()

	store := newFakeStore()
	c := newTestCache(t, store, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{Description: "desc", CachedAt: time.Now()},
	})

	guide := &SpeciesGuide{ScientificName: "Turdus merula", Description: "desc", CachedAt: time.Now()}

	// A live context persists normally: proves the test exercises a real write path.
	c.saveGuide(t.Context(), "Turdus merula", "en", guide)
	_, err := store.Get(t.Context(), "Turdus merula", "en", c.providerSetKey())
	require.NoError(t, err, "a live context must still persist the guide")

	// A cancelled context must not write. Use a distinct species so a skipped write is
	// unambiguous rather than masked by the row above.
	cancelled, cancel := context.WithCancel(t.Context())
	cancel()
	c.saveGuide(cancelled, "Turdus pilaris", "en", guide)

	_, err = store.Get(t.Context(), "Turdus pilaris", "en", c.providerSetKey())
	require.ErrorIs(t, err, ErrCacheEntryNotFound,
		"a cancelled cache context must not persist a guide")
}

// TestGuideCache_ProviderSetIsolatesCachedRows pins the fix for stale prose
// surviving a provider change across a restart.
//
// Rows are keyed by the provider set that produced them. A cache running with only
// OpenFauna must not see rows a Wikipedia-enabled cache wrote, so switching
// Wikipedia off in config.yaml and restarting stops serving its prose immediately —
// rather than continuing to serve it (with its CC BY-SA attribution) for a full
// PositiveTTL because the startup path ran no invalidation.
func TestGuideCache_ProviderSetIsolatesCachedRows(t *testing.T) {
	t.Parallel()
	store := newFakeStore()

	// A cache with Wikipedia enabled caches a guide with prose.
	withWiki := NewGuideCache(store, noopMetrics{})
	withWiki.RegisterProvider(OpenFaunaProviderName, &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird"},
	})
	withWiki.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{Description: "Long Wikipedia prose about the blackbird."},
	})
	t.Cleanup(withWiki.Close)

	g, err := withWiki.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.Contains(t, g.Description, "Wikipedia prose")
	require.Equal(t, 1, store.count(), "the guide is persisted")

	// Restart with Wikipedia disabled: a fresh cache, same store, OpenFauna only.
	offlineProv := &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird"},
	}
	withoutWiki := NewGuideCache(store, noopMetrics{})
	withoutWiki.RegisterProvider(OpenFaunaProviderName, offlineProv)
	t.Cleanup(withoutWiki.Close)

	// The startup pre-load must not adopt the other set's row...
	withoutWiki.Start()
	assert.Zero(t, withoutWiki.memLen(), "rows from another provider set must not seed memory")

	// ...and a lookup must re-fetch rather than serve the retired set's prose.
	g2, err := withoutWiki.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Empty(t, g2.Description, "Wikipedia prose must not survive disabling Wikipedia")
	assert.Equal(t, 1, offlineProv.callCount(), "the guide must be re-fetched under the new set")

	// Both rows coexist, keyed by their own set, and age out on normal retention.
	assert.Equal(t, 2, store.count())
}

// TestGuideCache_StaleNegativeIsTreatedAsMiss pins that an EXPIRED not-found marker
// is re-fetched rather than served. Stale-while-revalidate is right for a stale
// positive but wrong for a negative: the API maps a negative to HTTP 404, so serving
// an expired one reports "no guide" for a species whose article now exists.
func TestGuideCache_StaleNegativeIsTreatedAsMiss(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	prov := &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird", Description: "It exists now."},
	}
	c := newTestCache(t, store, prov)

	// Seed an expired negative marker directly into the memory tier.
	c.storeMemory(cacheKey("Turdus merula", defaultLocale), &SpeciesGuide{
		ScientificName: "Turdus merula",
		Negative:       true,
		CachedAt:       time.Now().Add(-NegativeTTL - time.Hour),
	})

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	require.NotNil(t, g)
	assert.False(t, g.IsNegativeEntry(), "an expired negative must not be served as a 404")
	assert.Equal(t, "Common Blackbird", g.CommonName)
	assert.Equal(t, 1, prov.callCount(), "an expired negative must trigger a re-fetch")
}

// TestProviderSetKey_CannotCollideWithLegacyRows pins the upgrade case.
//
// Before rows were keyed by provider set, the provider column held
// resolveProviderName() — providers[0].name — and OpenFauna is always registered
// first, so every row an older build wrote reads "openfauna" no matter which
// provider supplied the prose. Deriving an unprefixed key would make an
// OpenFauna-only cache match those legacy rows exactly, and it would go on serving
// Wikipedia prose for a full PositiveTTL after the user disabled Wikipedia — the
// bug provider-set keying exists to prevent, surviving the one scenario (upgrade)
// where it matters most.
func TestProviderSetKey_CannotCollideWithLegacyRows(t *testing.T) {
	t.Parallel()

	c := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(c.Close)
	c.RegisterProvider(OpenFaunaProviderName, &fakeProvider{name: OpenFaunaProviderName})

	key := c.providerSetKey()
	assert.NotEqual(t, OpenFaunaProviderName, key,
		"an OpenFauna-only set must NOT derive the bare legacy value, or every row an older build wrote is still a hit")
	assert.Equal(t, providerSetKeyPrefix+OpenFaunaProviderName, key)

	// A legacy row, written by an older build under the bare provider name.
	store := newFakeStore()
	require.NoError(t, store.Save(t.Context(), &GuideCacheEntry{
		ScientificName: "Turdus merula",
		Locale:         defaultLocale,
		Provider:       OpenFaunaProviderName, // legacy semantics
		Description:    "Wikipedia prose from before the upgrade.",
		CachedAt:       time.Now(),
	}))

	prov := &fakeProvider{
		name:   OpenFaunaProviderName,
		result: &SpeciesGuide{CommonName: "Common Blackbird"},
	}
	upgraded := newTestCache(t, store, prov)
	upgraded.Start()
	assert.Zero(t, upgraded.memLen(), "a legacy row must not seed the memory tier after upgrade")

	g, err := upgraded.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Empty(t, g.Description, "legacy Wikipedia prose must not survive the upgrade")
	assert.Equal(t, 1, prov.callCount(), "the guide must be re-fetched under the new key")
}

// TestProviderSetKey_MemoizedAndOrderIndependent pins that the key is stable
// regardless of registration order (so it cannot orphan rows spuriously) and is
// derived once rather than rebuilt on every DB read and save.
func TestProviderSetKey_MemoizedAndOrderIndependent(t *testing.T) {
	t.Parallel()

	forward := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(forward.Close)
	forward.RegisterProvider(OpenFaunaProviderName, &fakeProvider{name: OpenFaunaProviderName})
	forward.RegisterProvider(WikipediaProviderName, &fakeProvider{name: WikipediaProviderName})

	reverse := NewGuideCache(newFakeStore(), noopMetrics{})
	t.Cleanup(reverse.Close)
	reverse.RegisterProvider(WikipediaProviderName, &fakeProvider{name: WikipediaProviderName})
	reverse.RegisterProvider(OpenFaunaProviderName, &fakeProvider{name: OpenFaunaProviderName})

	assert.Equal(t, forward.providerSetKey(), reverse.providerSetKey(),
		"registration order must not change the key, or a reorder would orphan every row")

	// Latched on first use: the key is derived once, not rebuilt per DB read and
	// save. Registering another provider afterwards would change the DERIVED value,
	// so an unchanged key proves the memo held rather than merely being deterministic.
	latched := forward.providerSetKey()
	forward.RegisterProvider("late-provider", &fakeProvider{name: "late-provider"})
	assert.Equal(t, latched, forward.providerSetKey(),
		"providerSetKey must latch on first use rather than recompute")
	assert.NotEqual(t, latched, providerSetKeyPrefix+forward.computeProviderSetKey(),
		"guard the test itself: the late registration must actually change the derived value")
}
