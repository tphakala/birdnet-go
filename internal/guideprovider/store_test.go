package guideprovider

import (
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
)

func newTestStore(t *testing.T) *GORMGuideStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	// Pin the pool to a single connection. A default pool can open several
	// connections to ":memory:", and each one is a separate in-memory database,
	// which causes intermittent "no such table" failures under parallel tests.
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	store, err := NewGORMGuideStoreWithMetrics(db, nil)
	require.NoError(t, err)
	return store
}

func TestGORMGuideStore_SaveGetDelete(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	entry := &GuideCacheEntry{
		ScientificName: "Turdus merula",
		Locale:         "en",
		Provider:       WikipediaProviderName,
		CommonName:     "Common Blackbird",
		Description:    "A bird.",
		CachedAt:       time.Now(),
	}
	require.NoError(t, store.Save(ctx, entry))

	got, err := store.Get(ctx, "Turdus merula", "en", WikipediaProviderName)
	require.NoError(t, err)
	assert.Equal(t, "Common Blackbird", got.CommonName)

	// Missing key returns ErrCacheEntryNotFound.
	_, err = store.Get(ctx, "Missing species", "en", WikipediaProviderName)
	require.Error(t, err)
	assert.True(t, errors.Is(err, ErrCacheEntryNotFound))

	require.NoError(t, store.Delete(ctx, "Turdus merula", "en", WikipediaProviderName))
	_, err = store.Get(ctx, "Turdus merula", "en", WikipediaProviderName)
	assert.True(t, errors.Is(err, ErrCacheEntryNotFound))
}

func TestGORMGuideStore_SaveUpsert(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	base := &GuideCacheEntry{
		ScientificName: "Turdus merula", Locale: "en", Provider: WikipediaProviderName,
		CommonName: "Old", CachedAt: time.Now(),
	}
	require.NoError(t, store.Save(ctx, base))
	updated := &GuideCacheEntry{
		ScientificName: "Turdus merula", Locale: "en", Provider: WikipediaProviderName,
		CommonName: "New", CachedAt: time.Now(),
	}
	require.NoError(t, store.Save(ctx, updated))

	all, err := store.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1, "upsert must not create a duplicate row")
	assert.Equal(t, "New", all[0].CommonName)
}

func TestGORMGuideStore_LocaleProviderIsolation(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Turdus merula", Locale: "en", Provider: WikipediaProviderName,
		CommonName: "Blackbird", CachedAt: time.Now(),
	}))
	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Turdus merula", Locale: "de", Provider: WikipediaProviderName,
		CommonName: "Amsel", CachedAt: time.Now(),
	}))

	all, err := store.GetAll(ctx)
	require.NoError(t, err)
	assert.Len(t, all, 2, "different locales are distinct entries")
}

func TestGORMGuideStore_Cleanup(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Old species", Locale: "en", Provider: WikipediaProviderName,
		CachedAt: time.Now().Add(-DBRetention - time.Hour),
	}))
	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Fresh species", Locale: "en", Provider: WikipediaProviderName,
		CachedAt: time.Now(),
	}))

	require.NoError(t, store.Cleanup(ctx))

	all, err := store.GetAll(ctx)
	require.NoError(t, err)
	require.Len(t, all, 1)
	assert.Equal(t, "Fresh species", all[0].ScientificName)
}

func TestGORMGuideStore_CleanupNegativeRetention(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	// Negative entry older than the (short) negative retention but well within
	// the (long) positive retention: it must be purged.
	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Stale negative", Locale: "en", Provider: WikipediaProviderName,
		Negative: true, CachedAt: time.Now().Add(-NegativeDBRetention - time.Hour),
	}))
	// Recent negative entry: must survive.
	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Fresh negative", Locale: "en", Provider: WikipediaProviderName,
		Negative: true, CachedAt: time.Now(),
	}))
	// Positive entry older than negative retention but within positive retention:
	// must survive (it is not negative).
	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "Old positive", Locale: "en", Provider: WikipediaProviderName,
		CachedAt: time.Now().Add(-NegativeDBRetention - time.Hour),
	}))

	require.NoError(t, store.Cleanup(ctx))

	all, err := store.GetAll(ctx)
	require.NoError(t, err)
	names := make([]string, 0, len(all))
	for i := range all {
		names = append(names, all[i].ScientificName)
	}
	assert.ElementsMatch(t, []string{"Fresh negative", "Old positive"}, names)
}

func TestEncodeDecodeSimilarSpecies(t *testing.T) {
	t.Parallel()
	in := []SimilarSpecies{
		{ScientificName: "Turdus pilaris", CommonName: "Fieldfare", Relationship: "same_genus"},
		{ScientificName: "Turdus iliacus", CommonName: "Redwing", Relationship: "same_genus"},
	}
	encoded := encodeSimilarSpecies(in)
	assert.NotEmpty(t, encoded)
	out := decodeSimilarSpecies(encoded)
	assert.Equal(t, in, out)

	assert.Empty(t, encodeSimilarSpecies(nil))
	assert.Nil(t, decodeSimilarSpecies(""))
	assert.Nil(t, decodeSimilarSpecies("not json"))
}

func TestEntryGuideRoundTrip(t *testing.T) {
	t.Parallel()
	g := &SpeciesGuide{
		CommonName:     "Common Blackbird",
		Description:    "## Description\nA bird.",
		Genus:          "Turdus",
		Family:         "Turdidae",
		SourceURL:      "https://en.wikipedia.org/wiki/Common_blackbird",
		License:        "CC BY-SA 4.0",
		SimilarSpecies: []SimilarSpecies{{ScientificName: "Turdus pilaris", Relationship: "same_genus"}},
		CachedAt:       time.Now().Truncate(time.Second),
		Partial:        true,
	}
	entry := guideToEntry("Turdus merula", "en", WikipediaProviderName, g)
	back := entryToGuide(entry)

	assert.Equal(t, "Turdus merula", back.ScientificName)
	assert.Equal(t, g.CommonName, back.CommonName)
	assert.Equal(t, g.Description, back.Description)
	assert.Equal(t, g.Genus, back.Genus)
	assert.Equal(t, WikipediaProviderName, back.SourceProvider)
	assert.Equal(t, g.SimilarSpecies, back.SimilarSpecies)
	assert.True(t, back.Partial)
}
