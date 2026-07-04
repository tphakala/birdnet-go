package analysis

import (
	"context"
	"testing"
	"time"

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
// returns canned data. The warm path prefers the ranked GetSpeciesSummaryData and
// falls back to GetAllDetectedSpecies only when the summary query errors. Any other
// datastore method hits the nil embed and panics, surfacing an unexpected dependency.
type warmSpyDatastore struct {
	datastore.Interface
	called     bool // GetSpeciesSummaryData (the primary ranked path) was queried
	allCalled  bool // GetAllDetectedSpecies (the unranked fallback) was queried
	summary    []datastore.SpeciesSummaryData
	summaryErr error
	notes      []datastore.Note
	err        error
}

func (w *warmSpyDatastore) GetSpeciesSummaryData(_ context.Context, _, _ string) ([]datastore.SpeciesSummaryData, error) {
	w.called = true
	return w.summary, w.summaryErr
}

func (w *warmSpyDatastore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	w.allCalled = true
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

	cache := initGuideCacheIfNeeded(settings, &noGormDatastore{}, nil)
	assert.Nil(t, cache, "no GORM handle means the guide cache cannot be built")
}

func TestInitGuideCacheIfNeeded_NilGormDBReturnsNil(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = true

	cache := initGuideCacheIfNeeded(settings, nilGormDatastore{}, nil)
	assert.Nil(t, cache, "a nil GORM handle means the guide cache cannot be built")
}

func TestInitGuideCacheIfNeeded_EnabledBuildsCache(t *testing.T) {
	t.Parallel()
	settings := &conf.Settings{}
	settings.Realtime.Dashboard.SpeciesGuide.Enabled = true
	// Opt into Wikipedia so both providers (OpenFauna primary + Wikipedia secondary)
	// are registered. Neither needs credentials and registration makes no network
	// call, so the build stays offline and deterministic.
	settings.Realtime.Dashboard.SpeciesGuide.EnableWikipedia = true

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
	cache := newUnstartedCache(t)
	ds := &warmSpyDatastore{}

	// Pass a real cache so this exercises the topN <= 0 guard independently of
	// the nil-cache short-circuit covered by TestWarmGuideCacheWithTopSpecies_NilCacheIsNoOp.
	warmGuideCacheWithTopSpecies(cache, ds, 0, GetLogger())
	assert.False(t, ds.called, "warming is disabled when topN <= 0")
}

func TestWarmGuideCacheWithTopSpecies_DatastoreErrorHandled(t *testing.T) {
	t.Parallel()
	cache := newUnstartedCache(t)
	// Both the ranked summary and the unranked fallback error, so warming resolves
	// to no names. This must be handled gracefully (logged, no panic).
	ds := &warmSpyDatastore{summaryErr: assert.AnError, err: assert.AnError}

	assert.NotPanics(t, func() {
		warmGuideCacheWithTopSpecies(cache, ds, 5, GetLogger())
	})
	assert.True(t, ds.called, "the ranked summary query must be attempted")
	assert.True(t, ds.allCalled, "a summary error must fall back to the unranked list")
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
		summary: []datastore.SpeciesSummaryData{
			{ScientificName: "Turdus merula", Count: 50},
			{ScientificName: "Parus major", Count: 5},
			{ScientificName: "Cyanistes caeruleus", Count: 20},
		},
	}

	assert.NotPanics(t, func() {
		warmGuideCacheWithTopSpecies(cache, ds, 2, GetLogger())
	})
	assert.True(t, ds.called, "detected species must be queried when warming is enabled")
}

// TestTopDetectedSpeciesNames_RanksByDetectionCount guards [5] F1: warming must select
// the MOST-DETECTED species (by count desc), not an arbitrary/alphabetical subset, and
// must skip empty scientific names without letting them consume a slot.
func TestTopDetectedSpeciesNames_RanksByDetectionCount(t *testing.T) {
	t.Parallel()
	ds := &warmSpyDatastore{
		summary: []datastore.SpeciesSummaryData{
			{ScientificName: "Parus major", Count: 5},
			{ScientificName: "Turdus merula", Count: 50},
			{ScientificName: "", Count: 100}, // highest count but empty: must be skipped
			{ScientificName: "Cyanistes caeruleus", Count: 20},
		},
	}

	got := topDetectedSpeciesNames(ds, 2, GetLogger())
	assert.Equal(t, []string{"Turdus merula", "Cyanistes caeruleus"}, got,
		"warm must pick the highest-count species in order, skipping the empty name")
	assert.True(t, ds.called)
	assert.False(t, ds.allCalled, "the ranked path must not fall back when the summary succeeds")
}

// TestTopDetectedSpeciesNames_FallsBackToUnrankedOnSummaryError guards the fallback:
// when the ranked summary query errors, warming still runs using GetAllDetectedSpecies.
func TestTopDetectedSpeciesNames_FallsBackToUnrankedOnSummaryError(t *testing.T) {
	t.Parallel()
	ds := &warmSpyDatastore{
		summaryErr: assert.AnError,
		notes: []datastore.Note{
			{ScientificName: "Turdus merula"},
			{ScientificName: "Parus major"},
		},
	}

	got := topDetectedSpeciesNames(ds, 5, GetLogger())
	assert.Equal(t, []string{"Turdus merula", "Parus major"}, got)
	assert.True(t, ds.called)
	assert.True(t, ds.allCalled, "a summary error must fall back to the unranked list")
}

// TestTopDetectedSpeciesNames_EmptySummaryReturnsNilNoFallback guards the third path:
// a successful but EMPTY summary means "no detections to rank", so warming resolves to
// nothing and must NOT fall back to the unranked list (the fallback is reserved for a
// summary ERROR, not an empty result).
func TestTopDetectedSpeciesNames_EmptySummaryReturnsNilNoFallback(t *testing.T) {
	t.Parallel()
	ds := &warmSpyDatastore{summary: nil} // no error, no rows

	got := topDetectedSpeciesNames(ds, 5, GetLogger())
	assert.Empty(t, got)
	assert.True(t, ds.called, "the ranked summary must be queried")
	assert.False(t, ds.allCalled, "an empty (non-error) summary must not trigger the unranked fallback")
}

// TestTopDetectedSpeciesNames_TieBreakByRecencyThenName exercises the ranking's
// secondary/tertiary sort keys: species with equal detection counts are ordered
// most-recently-seen first, and a further tie on recency breaks by scientific name.
func TestTopDetectedSpeciesNames_TieBreakByRecencyThenName(t *testing.T) {
	t.Parallel()
	older := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	newer := time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC)
	ds := &warmSpyDatastore{
		summary: []datastore.SpeciesSummaryData{
			{ScientificName: "Aaa aaa", Count: 10, LastSeen: older},
			{ScientificName: "Ccc ccc", Count: 10, LastSeen: newer},
			{ScientificName: "Bbb bbb", Count: 10, LastSeen: newer}, // ties Ccc on count+recency
		},
	}

	got := topDetectedSpeciesNames(ds, 3, GetLogger())
	// Equal counts → most-recent first (Ccc/Bbb before Aaa); the Ccc/Bbb recency tie
	// breaks by scientific name ascending (Bbb before Ccc).
	assert.Equal(t, []string{"Bbb bbb", "Ccc ccc", "Aaa aaa"}, got)
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
