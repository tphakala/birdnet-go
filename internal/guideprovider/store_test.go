package guideprovider

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/observability/metrics"
)

// Regression: the species guide was silently disabled on every MySQL deployment.
//
// The three columns of idx_guide_cache_key were declared without a size, and the
// MySQL driver is opened without DefaultStringSize (internal/datastore/mysql.go),
// so GORM mapped them to longtext. MySQL refuses a unique index over TEXT columns
// without a key length, AutoMigrate failed, NewGORMGuideStoreWithMetrics returned
// an error, and initGuideCacheIfNeeded logged "guide cache disabled" and returned
// nil — while SQLite, which ignores the size, worked fine and hid it in CI.
//
// A real reproduction needs a MySQL instance, so assert the schema contract that
// prevents it instead: every column in the composite unique index must declare a
// size, matching the convention every other such column in internal/datastore uses.
func TestGuideCacheEntry_UniqueIndexColumnsDeclareSize(t *testing.T) {
	t.Parallel()

	entryType := reflect.TypeFor[GuideCacheEntry]()
	var checked int
	for field := range entryType.Fields() {
		tag := field.Tag.Get("gorm")
		if !strings.Contains(tag, "uniqueIndex:idx_guide_cache_key") {
			continue
		}
		checked++
		assert.Contains(t, tag, "size:",
			"%s is part of idx_guide_cache_key and must declare an explicit size, "+
				"or MySQL maps it to longtext and AutoMigrate fails on the unique index",
			field.Name)
	}
	require.Equal(t, 3, checked,
		"expected the composite unique index to cover exactly scientific_name, locale and provider")
}

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
	t.Cleanup(func() { _ = sqlDB.Close() })
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

func TestGORMGuideStore_GetRecent(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	now := time.Now()
	// Insert oldest-first so insertion order can't accidentally satisfy the
	// most-recent-first ordering the query must enforce.
	for i, name := range []string{"oldest", "middle", "newest"} {
		require.NoError(t, store.Save(ctx, &GuideCacheEntry{
			ScientificName: name, Locale: "en", Provider: WikipediaProviderName,
			CachedAt: now.Add(time.Duration(i) * time.Minute),
		}))
	}

	recent, err := store.GetRecent(ctx, 2, WikipediaProviderName)
	require.NoError(t, err)
	require.Len(t, recent, 2, "limit must bound the result set")
	assert.Equal(t, "newest", recent[0].ScientificName, "ordered most-recently-cached first")
	assert.Equal(t, "middle", recent[1].ScientificName)

	// A non-positive limit returns everything (matches GetAll).
	all, err := store.GetRecent(ctx, 0, WikipediaProviderName)
	require.NoError(t, err)
	assert.Len(t, all, 3)

	// Rows written by a different provider set are invisible to this one. The
	// memory key has no provider component, so without this filter a startup load
	// would seed the tier with a retired set's guides and serve them as Tier-1
	// hits the provider-keyed Tier-2 read would never have returned.
	require.NoError(t, store.Save(ctx, &GuideCacheEntry{
		ScientificName: "other-set", Locale: "en", Provider: "openfauna+wikipedia",
		CachedAt: now.Add(time.Hour),
	}))
	mine, err := store.GetRecent(ctx, 0, WikipediaProviderName)
	require.NoError(t, err)
	assert.Len(t, mine, 3, "a row from another provider set must not be returned")

	theirs, err := store.GetRecent(ctx, 0, "openfauna+wikipedia")
	require.NoError(t, err)
	require.Len(t, theirs, 1)
	assert.Equal(t, "other-set", theirs[0].ScientificName)

	// An empty provider set is an explicit "no filter" for administrative reads.
	every, err := store.GetRecent(ctx, 0, "")
	require.NoError(t, err)
	assert.Len(t, every, 4)
}

func TestGORMGuideStore_DeleteAll(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	ctx := t.Context()

	for _, name := range []string{"a", "b", "c"} {
		require.NoError(t, store.Save(ctx, &GuideCacheEntry{
			ScientificName: name, Locale: "en", Provider: WikipediaProviderName, CachedAt: time.Now(),
		}))
	}
	require.NoError(t, store.DeleteAll(ctx))

	all, err := store.GetAll(ctx)
	require.NoError(t, err)
	assert.Empty(t, all, "DeleteAll must clear every entry")
}

func TestGORMGuideStore_SaveNilIsNoop(t *testing.T) {
	t.Parallel()
	store := newTestStore(t)
	require.NoError(t, store.Save(t.Context(), nil))
}

func TestNewGORMGuideStore_NilDB(t *testing.T) {
	t.Parallel()
	_, err := NewGORMGuideStoreWithMetrics(nil, nil)
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryDatabase))
}

func TestNewGORMGuideStore_MigrateError(t *testing.T) {
	t.Parallel()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close()) // AutoMigrate on a closed DB fails

	_, err = NewGORMGuideStoreWithMetrics(db, nil)
	require.Error(t, err)
	assert.True(t, errors.IsCategory(err, errors.CategoryDatabase))
}

func TestTransientError(t *testing.T) {
	t.Parallel()
	assert.NoError(t, NewTransientError(nil)) //nolint:testifylint // asserting the nil-in → nil-out contract

	wrapped := NewTransientError(errors.NewStd("boom"))
	require.Error(t, wrapped)
	assert.Equal(t, "boom", wrapped.Error())
	assert.True(t, IsTransient(wrapped))
	assert.False(t, IsTransient(errors.NewStd("plain")))
}

// newClosedStore returns a store (with a real metrics instance) whose underlying
// connection is closed, so every DB operation fails — exercising the recordDBError
// (metrics != nil) + wrapDBError paths on all methods.
func newClosedStore(t *testing.T) *GORMGuideStore {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	sqlDB, err := db.DB()
	require.NoError(t, err)
	sqlDB.SetMaxOpenConns(1)
	m, err := metrics.NewGuideProviderMetrics(prometheus.NewRegistry())
	require.NoError(t, err)
	store, err := NewGORMGuideStoreWithMetrics(db, m)
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close()) // migration done; now every op errors
	return store
}

func TestGORMGuideStore_DBErrorsAreWrappedAndCounted(t *testing.T) {
	t.Parallel()
	store := newClosedStore(t)
	ctx := t.Context()

	// Get on a closed DB surfaces a non-NotFound error, so it takes the
	// wrap-and-count path rather than mapping to ErrCacheEntryNotFound.
	_, err := store.Get(ctx, "x", "en", WikipediaProviderName)
	require.Error(t, err)
	assert.False(t, errors.Is(err, ErrCacheEntryNotFound))
	assert.True(t, errors.IsCategory(err, errors.CategoryDatabase))

	require.Error(t, store.Save(ctx, &GuideCacheEntry{ScientificName: "x", Locale: "en", Provider: WikipediaProviderName}))
	_, err = store.GetAll(ctx)
	require.Error(t, err)
	_, err = store.GetRecent(ctx, 5, WikipediaProviderName)
	require.Error(t, err)
	require.Error(t, store.Delete(ctx, "x", "en", WikipediaProviderName))
	require.Error(t, store.DeleteAll(ctx))
	require.Error(t, store.Cleanup(ctx))
	// Each failed op above ran recordDBError against the real metrics instance
	// (store.metrics != nil), exercising both the record and wrap paths.
}

func TestEntryGuideRoundTrip(t *testing.T) {
	t.Parallel()
	g := &SpeciesGuide{
		CommonName:  "Common Blackbird",
		Description: "## Description\nA bird.",
		Genus:       "Turdus",
		Family:      "Turdidae",
		SourceURL:   "https://en.wikipedia.org/wiki/Common_blackbird",
		License:     "CC BY-SA 4.0",
		CachedAt:    time.Now().Truncate(time.Second),
		Partial:     true,
	}
	entry := guideToEntry("Turdus merula", "en", WikipediaProviderName, g)
	back := entryToGuide(entry)

	assert.Equal(t, "Turdus merula", back.ScientificName)
	assert.Equal(t, g.CommonName, back.CommonName)
	assert.Equal(t, g.Description, back.Description)
	assert.Equal(t, g.Genus, back.Genus)
	assert.Equal(t, WikipediaProviderName, back.SourceProvider)
	assert.True(t, back.Partial)
}
