package guideprovider

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mockGuideProvider is a test double for GuideProvider.
type mockGuideProvider struct {
	fetchFunc func(ctx context.Context, scientificName string) (SpeciesGuide, error)
}

func (m *mockGuideProvider) Fetch(ctx context.Context, scientificName string) (SpeciesGuide, error) {
	return m.fetchFunc(ctx, scientificName)
}

// mockGuideStore is an in-memory test double for GuideStore.
type mockGuideStore struct {
	entries map[string]*GuideCacheEntry
}

func newMockGuideStore() *mockGuideStore {
	return &mockGuideStore{entries: make(map[string]*GuideCacheEntry)}
}

func (s *mockGuideStore) GetGuideCache(_ context.Context, scientificName, providerName string) (*GuideCacheEntry, error) {
	key := providerName + ":" + scientificName
	entry, ok := s.entries[key]
	if !ok {
		return nil, nil
	}
	return entry, nil
}

func (s *mockGuideStore) SaveGuideCache(_ context.Context, entry *GuideCacheEntry) error {
	key := entry.ProviderName + ":" + entry.ScientificName
	s.entries[key] = entry
	return nil
}

func (s *mockGuideStore) GetAllGuideCaches(_ context.Context, providerName string) ([]GuideCacheEntry, error) {
	var result []GuideCacheEntry
	for _, entry := range s.entries {
		if entry.ProviderName == providerName {
			result = append(result, *entry)
		}
	}
	return result, nil
}

func TestSpeciesGuide_IsNegativeEntry(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		guide    SpeciesGuide
		expected bool
	}{
		{
			name:     "positive entry",
			guide:    SpeciesGuide{SourceProvider: WikipediaProviderName},
			expected: false,
		},
		{
			name:     "negative entry",
			guide:    SpeciesGuide{SourceProvider: negativeEntryMarker},
			expected: true,
		},
		{
			name:     "empty provider",
			guide:    SpeciesGuide{SourceProvider: ""},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, tt.guide.IsNegativeEntry())
		})
	}
}

func TestIsCacheEntryStale(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		cachedAt   time.Time
		isNegative bool
		expected   bool
	}{
		{
			name:       "fresh positive entry",
			cachedAt:   time.Now().Add(-1 * time.Hour),
			isNegative: false,
			expected:   false,
		},
		{
			name:       "stale positive entry",
			cachedAt:   time.Now().Add(-8 * 24 * time.Hour),
			isNegative: false,
			expected:   true,
		},
		{
			name:       "fresh negative entry",
			cachedAt:   time.Now().Add(-5 * time.Minute),
			isNegative: true,
			expected:   false,
		},
		{
			name:       "stale negative entry",
			cachedAt:   time.Now().Add(-31 * time.Minute),
			isNegative: true,
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.expected, isCacheEntryStale(tt.cachedAt, tt.isNegative))
		})
	}
}

func TestMergeGuides(t *testing.T) {
	t.Parallel()

	primary := SpeciesGuide{
		ScientificName: "Turdus merula",
		CommonName:     "Common Blackbird",
		Description:    "A species of true thrush.",
		SourceProvider: WikipediaProviderName,
	}

	secondary := SpeciesGuide{
		ScientificName:     "Turdus merula",
		CommonName:         "Eurasian Blackbird",
		ConservationStatus: "Least Concern",
		SourceProvider:     EBirdProviderName,
	}

	result := mergeGuides(primary, secondary)

	// Primary fields take precedence
	assert.Equal(t, "Common Blackbird", result.CommonName)
	assert.Equal(t, "A species of true thrush.", result.Description)

	// Secondary fills gaps
	assert.Equal(t, "Least Concern", result.ConservationStatus)

	// Partial is false because description is populated
	assert.False(t, result.Partial)
}

func TestMergeGuides_PrimaryEmpty(t *testing.T) {
	t.Parallel()

	primary := SpeciesGuide{
		ScientificName: "Turdus merula",
	}

	secondary := SpeciesGuide{
		ScientificName: "Turdus merula",
		CommonName:     "Common Blackbird",
		Description:    "A bird.",
	}

	result := mergeGuides(primary, secondary)
	assert.Equal(t, "Common Blackbird", result.CommonName)
	assert.Equal(t, "A bird.", result.Description)
	assert.False(t, result.Partial)
}

func TestDbEntryToGuide(t *testing.T) {
	t.Parallel()

	now := time.Now()
	entry := &GuideCacheEntry{
		ScientificName:     "Turdus merula",
		CommonName:         "Common Blackbird",
		Description:        "A species.",
		ConservationStatus: "Least Concern",
		SourceProvider:     WikipediaProviderName,
		SourceURL:          "https://en.wikipedia.org/wiki/Common_blackbird",
		LicenseName:        "CC BY-SA 4.0",
		LicenseURL:         "https://creativecommons.org/licenses/by-sa/4.0/",
		CachedAt:           now,
	}

	guide := dbEntryToGuide(entry)
	assert.Equal(t, "Turdus merula", guide.ScientificName)
	assert.Equal(t, "Common Blackbird", guide.CommonName)
	assert.Equal(t, "A species.", guide.Description)
	assert.Equal(t, WikipediaProviderName, guide.SourceProvider)
	assert.Equal(t, now, guide.CachedAt)
	assert.False(t, guide.Partial) // Description is non-empty
}

func TestGuideCache_GetFromMemory(t *testing.T) {
	t.Parallel()

	cache := NewGuideCache(nil)
	defer cache.Close()

	// Pre-populate memory cache
	guide := &SpeciesGuide{
		ScientificName: "Turdus merula",
		CommonName:     "Common Blackbird",
		Description:    "A species.",
		SourceProvider: WikipediaProviderName,
		CachedAt:       time.Now(),
	}
	cache.dataMap.Store("Turdus merula", guide)

	result, err := cache.Get(context.Background(), "Turdus merula")
	require.NoError(t, err)
	assert.Equal(t, "Common Blackbird", result.CommonName)
}

func TestGuideCache_NegativeMemoryCacheHit(t *testing.T) {
	t.Parallel()

	cache := NewGuideCache(nil)
	defer cache.Close()

	// Pre-populate with negative entry
	negative := &SpeciesGuide{
		ScientificName: "Unknown species",
		SourceProvider: negativeEntryMarker,
		CachedAt:       time.Now(),
	}
	cache.dataMap.Store("Unknown species", negative)

	_, err := cache.Get(context.Background(), "Unknown species")
	assert.ErrorIs(t, err, ErrGuideNotFound)
}

func TestGuideCache_FetchFromProvider(t *testing.T) {
	t.Parallel()

	store := newMockGuideStore()
	cache := NewGuideCache(store)
	defer cache.Close()

	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, scientificName string) (SpeciesGuide, error) {
			if scientificName == "Turdus merula" {
				return SpeciesGuide{
					ScientificName: "Turdus merula",
					CommonName:     "Common Blackbird",
					Description:    "A species of true thrush.",
					SourceProvider: WikipediaProviderName,
				}, nil
			}
			return SpeciesGuide{}, ErrGuideNotFound
		},
	}
	cache.RegisterProvider(WikipediaProviderName, provider)

	// First fetch should go to the provider
	result, err := cache.Get(context.Background(), "Turdus merula")
	require.NoError(t, err)
	assert.Equal(t, "Common Blackbird", result.CommonName)
	assert.Equal(t, "A species of true thrush.", result.Description)

	// Verify it was cached in memory
	cached, ok := cache.dataMap.Load("Turdus merula")
	assert.True(t, ok)
	assert.Equal(t, "Common Blackbird", cached.(*SpeciesGuide).CommonName)

	// Verify it was saved to the store
	entry, err := store.GetGuideCache(context.Background(), "Turdus merula", WikipediaProviderName)
	require.NoError(t, err)
	require.NotNil(t, entry)
	assert.Equal(t, "A species of true thrush.", entry.Description)
}

func TestGuideCache_ProviderNotFound(t *testing.T) {
	t.Parallel()

	store := newMockGuideStore()
	cache := NewGuideCache(store)
	defer cache.Close()

	provider := &mockGuideProvider{
		fetchFunc: func(_ context.Context, _ string) (SpeciesGuide, error) {
			return SpeciesGuide{}, ErrGuideNotFound
		},
	}
	cache.RegisterProvider(WikipediaProviderName, provider)

	_, err := cache.Get(context.Background(), "Nonexistent species")
	assert.ErrorIs(t, err, ErrGuideNotFound)

	// Verify negative entry was cached
	cached, ok := cache.dataMap.Load("Nonexistent species")
	assert.True(t, ok)
	assert.True(t, cached.(*SpeciesGuide).IsNegativeEntry())
}

func TestGuideCacheEntry_TableName(t *testing.T) {
	t.Parallel()
	entry := GuideCacheEntry{}
	assert.Equal(t, "guide_caches", entry.TableName())
}
