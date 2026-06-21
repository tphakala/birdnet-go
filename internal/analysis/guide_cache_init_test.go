package analysis

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gorm.io/driver/sqlite"
	"gorm.io/gorm"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/guideprovider"
)

// --- test datastores -------------------------------------------------------

// noGormDatastore satisfies datastore.Interface (via the nil embed) but NOT
// datastore.GormDBProvider, so initGuideCacheIfNeeded cannot obtain a *gorm.DB.
type noGormDatastore struct{ datastore.Interface }

// nilGormDatastore implements GormDBProvider but returns a nil handle, exercising
// the "handle is nil" guard.
type nilGormDatastore struct{ datastore.Interface }

func (nilGormDatastore) GormDB() *gorm.DB { return nil }

// realGormDatastore implements GormDBProvider with a working in-memory handle.
type realGormDatastore struct {
	datastore.Interface
	db *gorm.DB
}

func (r *realGormDatastore) GormDB() *gorm.DB { return r.db }

// warmSpyDatastore records whether the warming path queried detected species and
// returns canned data. Any other datastore method hits the nil embed and panics,
// surfacing an unexpected dependency.
type warmSpyDatastore struct {
	datastore.Interface
	called bool
	notes  []datastore.Note
	err    error
}

func (w *warmSpyDatastore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	w.called = true
	return w.notes, w.err
}

func newMemoryDB(t *testing.T) *gorm.DB {
	t.Helper()
	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	require.NoError(t, err)
	return db
}

// --- initGuideCacheIfNeeded ------------------------------------------------

func TestInitGuideCacheIfNeeded_DisabledReturnsNil(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = false

	// A datastore that panics on any call proves the disabled short-circuit runs
	// before the datastore is ever touched.
	cache := initGuideCacheIfNeeded(settings, &noGormDatastore{}, nil)
	assert.Nil(t, cache)
}

func TestInitGuideCacheIfNeeded_NoGormProviderReturnsNil(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
	settings.Realtime.Dashboard.SpeciesGuide.Provider = conf.SpeciesGuideProviderWikipedia

	cache := initGuideCacheIfNeeded(settings, &noGormDatastore{}, nil)
	assert.Nil(t, cache, "no GORM handle means the guide cache cannot be built")
}

func TestInitGuideCacheIfNeeded_NilGormDBReturnsNil(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
	settings.Realtime.Dashboard.SpeciesGuide.Provider = conf.SpeciesGuideProviderWikipedia

	cache := initGuideCacheIfNeeded(settings, nilGormDatastore{}, nil)
	assert.Nil(t, cache, "a nil GORM handle means the guide cache cannot be built")
}

func TestInitGuideCacheIfNeeded_EnabledBuildsCache(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
	settings.Realtime.Dashboard.SpeciesGuide.Provider = conf.SpeciesGuideProviderWikipedia
	settings.Realtime.Dashboard.SpeciesGuide.FallbackPolicy = conf.SpeciesGuideFallbackAll
	// eBird is explicitly disabled so the build stays offline and deterministic.
	settings.Realtime.EBird.Enabled = false

	ds := &realGormDatastore{db: newMemoryDB(t)}

	cache := initGuideCacheIfNeeded(settings, ds, nil)
	require.NotNil(t, cache, "an enabled guide with a working DB handle must build a cache")
	// Started a refresh goroutine; close it to avoid leaking past the test.
	t.Cleanup(cache.Close)
}

// --- warmGuideCacheWithTopSpecies ------------------------------------------

func TestWarmGuideCacheWithTopSpecies_NilCacheIsNoOp(t *testing.T) {
	t.Parallel()
	ds := &warmSpyDatastore{}

	// A nil cache must short-circuit before the datastore is queried.
	warmGuideCacheWithTopSpecies(nil, ds, 5, GetLogger())
	assert.False(t, ds.called, "datastore must not be queried when the cache is nil")
}

func TestWarmGuideCacheWithTopSpecies_ZeroTopNIsNoOp(t *testing.T) {
	t.Parallel()
	ds := &warmSpyDatastore{}

	warmGuideCacheWithTopSpecies(nil, ds, 0, GetLogger())
	assert.False(t, ds.called, "warming is disabled when topN <= 0")
}

func TestWarmGuideCacheWithTopSpecies_DatastoreErrorHandled(t *testing.T) {
	t.Parallel()
	cache := newUnstartedCache(t)
	ds := &warmSpyDatastore{err: assert.AnError}

	// An error loading species must be handled gracefully (logged, no panic).
	assert.NotPanics(t, func() {
		warmGuideCacheWithTopSpecies(cache, ds, 5, GetLogger())
	})
	assert.True(t, ds.called)
}

func TestWarmGuideCacheWithTopSpecies_QueriesAndWarms(t *testing.T) {
	t.Parallel()
	cache := newUnstartedCache(t)
	// Close before warming so WarmForSpecies no-ops (no background provider
	// fetches). The query + name-selection logic under test runs in
	// warmGuideCacheWithTopSpecies *before* WarmForSpecies, so it is exercised
	// identically whether or not the cache is open.
	cache.Close()
	ds := &warmSpyDatastore{
		notes: []datastore.Note{
			{ScientificName: "Turdus merula"},
			{ScientificName: ""}, // skipped: empty scientific name
			{ScientificName: "Parus major"},
			{ScientificName: "Cyanistes caeruleus"},
		},
	}

	assert.NotPanics(t, func() {
		warmGuideCacheWithTopSpecies(cache, ds, 2, GetLogger())
	})
	assert.True(t, ds.called, "detected species must be queried when warming is enabled")
}

// newUnstartedCache builds a real GuideCache backed by an in-memory store but does
// not Start() it. Tests that reach WarmForSpecies must Close() it first to keep
// warming from spawning live provider fetches. The cleanup cancels its context.
func newUnstartedCache(t *testing.T) *guideprovider.GuideCache {
	t.Helper()
	store, err := guideprovider.NewGORMGuideStoreWithMetrics(newMemoryDB(t), nil)
	require.NoError(t, err)
	cache := guideprovider.NewGuideCache(store, nil)
	t.Cleanup(cache.Close)
	return cache
}
