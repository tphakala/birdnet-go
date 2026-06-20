package guideprovider

import (
	"context"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/tphakala/birdnet-go/internal/conf"
)

// noopMetrics is a no-op GuideCacheMetrics for tests.
type noopMetrics struct{}

func (noopMetrics) RecordCacheHit(_, _ string)           {}
func (noopMetrics) RecordCacheMiss(_ string)             {}
func (noopMetrics) RecordFetch(_, _ string, _ float64)   {}
func (noopMetrics) RecordDBError(_, _ string)            {}
func (noopMetrics) RecordNegativeEntry()                 {}
func (noopMetrics) UpdateCachePopulationRatio(_ float64) {}

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
	c.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Blackbird", Description: "Wikipedia prose."},
	})
	c.RegisterProvider(EBirdProviderName, &fakeProvider{
		name:   EBirdProviderName,
		result: &SpeciesGuide{Genus: "Turdus", Family: "Turdidae"},
	})
	t.Cleanup(c.Close)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.Equal(t, "Wikipedia prose.", g.Description, "primary wins")
	assert.Equal(t, "Turdus", g.Genus, "secondary fills gap")
	assert.Equal(t, "Turdidae", g.Family, "secondary fills gap")
}

func TestGuideCache_SecondaryNotFoundDoesNotMarkPartial(t *testing.T) {
	t.Parallel()
	store := newFakeStore()
	c := NewGuideCache(store, noopMetrics{})
	c.SetFallbackPolicy(conf.SpeciesGuideFallbackAll)
	c.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Blackbird", Description: "Complete Wikipedia prose."},
	})
	// eBird enrichment definitively has no entry for this species.
	c.RegisterProvider(EBirdProviderName, &fakeProvider{
		name: EBirdProviderName,
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
	c.RegisterProvider(WikipediaProviderName, &fakeProvider{
		name:   WikipediaProviderName,
		result: &SpeciesGuide{CommonName: "Blackbird", Description: "Complete Wikipedia prose."},
	})
	// eBird enrichment fails for a transient reason: the merged guide is partial.
	c.RegisterProvider(EBirdProviderName, &fakeProvider{
		name: EBirdProviderName,
		err:  NewTransientError(stubError("boom")),
	})
	t.Cleanup(c.Close)

	g, err := c.Get(t.Context(), "Turdus merula", FetchOptions{})
	require.NoError(t, err)
	assert.True(t, g.Partial, "a transient secondary failure must mark the guide partial")
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
	primary := &SpeciesGuide{CommonName: "Primary", Description: ""}
	secondary := &SpeciesGuide{CommonName: "Secondary", Description: "filled", Genus: "Turdus"}
	merged := mergeGuides(primary, secondary)
	assert.Equal(t, "Primary", merged.CommonName, "primary common name wins")
	assert.Equal(t, "filled", merged.Description, "empty primary field filled by secondary")
	assert.Equal(t, "Turdus", merged.Genus)

	assert.Equal(t, secondary, mergeGuides(nil, secondary))
	assert.Equal(t, primary, mergeGuides(primary, nil))
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
	got := trimToUTF8Boundary(s, 2)
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
		{" PT-BR ", "pt-br"},   // trimmed + lowercased
		{"zh-hans", "zh-hans"}, // 4-letter subtag allowed
		{"", "en"},             // empty -> default
		{"english", "en"},      // too long -> default
		{"en_US", "en"},        // underscore not allowed -> default
		{"../etc", "en"},       // path traversal attempt -> default
		{"@evil.com", "en"},    // host-injection attempt -> default
		{"en.wikipedia", "en"}, // dotted -> default
		{"a", "en"},            // too short -> default
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
	assert.LessOrEqual(t, count, maxMemoryEntries, "memory tier must not exceed the cap")

	// Updating an existing key must not change the count or be rejected.
	c.storeMemory(cacheKey("species", strconvI(0)), &SpeciesGuide{CommonName: "updated"})
	v, ok := c.memory.Load(cacheKey("species", strconvI(0)))
	assert.True(t, ok)
	g, _ := v.(*SpeciesGuide)
	assert.Equal(t, "updated", g.CommonName)
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
