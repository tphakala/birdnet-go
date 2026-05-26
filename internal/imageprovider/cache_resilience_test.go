package imageprovider_test

import (
	"fmt"
	"sync/atomic"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// emptyNameProvider returns a BirdImage with a populated URL but an empty
// ScientificName, simulating providers that hand back partial metadata.
type emptyNameProvider struct {
	fetchCount atomic.Int32
}

func (p *emptyNameProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	p.fetchCount.Add(1)
	return imageprovider.BirdImage{
		URL:         fmt.Sprintf("http://example.com/%s.jpg", scientificName),
		LicenseName: "CC BY 4.0",
		AuthorName:  "Test Author",
		CachedAt:    time.Now(),
	}, nil
}

// TestStoreSuccessfulFetchPopulatesScientificName verifies that fetched images
// are persisted with the requested scientific name even when the provider
// returns a BirdImage with an empty ScientificName. Regression test for
// Forgejo #756 (NOT NULL constraint on image_caches.scientific_name during
// the warmup path).
func TestStoreSuccessfulFetchPopulatesScientificName(t *testing.T) {
	t.Parallel()

	provider := &emptyNameProvider{}
	store := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err)
	cache.SetImageProvider(provider)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	const speciesName = "Pipistrellus pygmaeus"
	got, err := cache.Get(speciesName)
	require.NoError(t, err)
	assert.NotEmpty(t, got.URL, "Get should return the fetched image")

	cached, err := store.GetImageCache(datastore.ImageCacheQuery{
		ScientificName: speciesName,
		ProviderName:   "wikimedia",
	})
	require.NoError(t, err, "image must be persisted with the requested scientific name")
	require.NotNil(t, cached)
	assert.Equal(t, speciesName, cached.ScientificName,
		"saveToDB must receive ScientificName from the request even when the provider omits it")
}

// errCorrupt simulates a SQLite "database disk image is malformed" error
// that the datastore corruption detector should recognize.
var errCorrupt = errors.NewStd("database disk image is malformed")

// mockCorruptStore is a mockStore that returns a corruption error from
// configurable image-cache read/write methods. It counts how many times each
// method was invoked so tests can verify the cache short-circuits subsequent
// calls once corruption has been detected (Forgejo #762).
type mockCorruptStore struct {
	mockStore
	corruptGet     atomic.Bool
	corruptSave    atomic.Bool
	corruptBatch   atomic.Bool
	corruptLoadAll atomic.Bool
	getCalls       atomic.Int32
	saveCalls      atomic.Int32
	batchCalls     atomic.Int32
	loadAllCalls   atomic.Int32
}

func newMockCorruptStore() *mockCorruptStore {
	return &mockCorruptStore{
		mockStore: mockStore{
			images: make(map[string]*datastore.ImageCache),
		},
	}
}

func (m *mockCorruptStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	m.getCalls.Add(1)
	if m.corruptGet.Load() {
		return nil, errCorrupt
	}
	return m.mockStore.GetImageCache(query)
}

func (m *mockCorruptStore) SaveImageCache(cache *datastore.ImageCache) error {
	m.saveCalls.Add(1)
	if m.corruptSave.Load() {
		return errCorrupt
	}
	return m.mockStore.SaveImageCache(cache)
}

func (m *mockCorruptStore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) {
	m.batchCalls.Add(1)
	if m.corruptBatch.Load() {
		return nil, errCorrupt
	}
	return m.mockStore.GetImageCacheBatch(providerName, scientificNames)
}

func (m *mockCorruptStore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	m.loadAllCalls.Add(1)
	if m.corruptLoadAll.Load() {
		return nil, errCorrupt
	}
	return m.mockStore.GetAllImageCaches(providerName)
}

// TestImageCacheDisablesReadsOnCorruption verifies that once GetImageCache
// reports SQLite corruption, the cache stops issuing further reads or writes
// for the rest of the session. Without this latch the same fatal error gets
// reported to Sentry on every detection cycle (Forgejo #762 collected 1,763
// events from a single corrupted file before this fix).
func TestImageCacheDisablesReadsOnCorruption(t *testing.T) {
	t.Parallel()

	provider := &mockImageProvider{}
	store := newMockCorruptStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err)
	cache.SetImageProvider(provider)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	// Switch the read path to corruption AFTER startup so the cache's
	// initialization-time GetAllImageCaches call succeeds normally.
	store.corruptGet.Store(true)

	// First Get must attempt the corrupted read so the cache can latch the
	// disabled-DB flag. It then falls through to the provider.
	_, err = cache.Get("Turdus merula")
	require.NoError(t, err)
	require.GreaterOrEqual(t, store.getCalls.Load(), int32(1),
		"first call must reach GetImageCache so corruption can be detected")

	getsAfterFirst := store.getCalls.Load()
	savesAfterFirst := store.saveCalls.Load()

	// Subsequent reads (different species, to bypass the in-memory cache)
	// must not exercise the corrupted DB.
	_, err = cache.Get("Parus major")
	require.NoError(t, err)
	_, err = cache.Get("Cyanistes caeruleus")
	require.NoError(t, err)

	assert.Equal(t, getsAfterFirst, store.getCalls.Load(),
		"GetImageCache must not be retried after corruption is latched")
	assert.Equal(t, savesAfterFirst, store.saveCalls.Load(),
		"SaveImageCache must not run after corruption is latched on the read path")
}

// TestImageCacheDisablesWritesOnCorruption verifies that a corruption error
// from SaveImageCache also latches the disabled-DB flag, preventing further
// save attempts from generating Sentry events (Forgejo #762 save path).
func TestImageCacheDisablesWritesOnCorruption(t *testing.T) {
	t.Parallel()

	provider := &mockImageProvider{}
	store := newMockCorruptStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err)
	cache.SetImageProvider(provider)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	store.corruptSave.Store(true)

	// First Get fetches from the provider and triggers a save that will hit
	// the corrupted write path.
	_, err = cache.Get("Turdus merula")
	require.NoError(t, err)
	savesAfterFirst := store.saveCalls.Load()
	require.GreaterOrEqual(t, savesAfterFirst, int32(1),
		"first call must reach SaveImageCache so corruption can be detected")

	// Subsequent calls must not retry the write.
	_, err = cache.Get("Parus major")
	require.NoError(t, err)
	_, err = cache.Get("Cyanistes caeruleus")
	require.NoError(t, err)

	assert.Equal(t, savesAfterFirst, store.saveCalls.Load(),
		"SaveImageCache must not be retried after corruption is latched")
}

// TestImageCacheDisablesBatchOnCorruption verifies that a corruption error
// from GetImageCacheBatch (the dashboard's batch thumbnail path) latches the
// flag too. Without this guard, every dashboard render would re-issue a batch
// query against the corrupted DB (Forgejo #762 batch path).
func TestImageCacheDisablesBatchOnCorruption(t *testing.T) {
	t.Parallel()

	provider := &mockImageProvider{}
	store := newMockCorruptStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err)
	cache.SetImageProvider(provider)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	store.corruptBatch.Store(true)

	// First batch fetch triggers the corrupted batch read.
	_ = cache.GetBatch([]string{"Turdus merula", "Parus major"})
	batchesAfterFirst := store.batchCalls.Load()
	require.GreaterOrEqual(t, batchesAfterFirst, int32(1),
		"first batch call must reach GetImageCacheBatch so corruption can be detected")

	// Subsequent batch fetches must not retry the corrupted call.
	_ = cache.GetBatch([]string{"Cyanistes caeruleus", "Sitta europaea"})
	_ = cache.GetBatch([]string{"Erithacus rubecula"})

	assert.Equal(t, batchesAfterFirst, store.batchCalls.Load(),
		"GetImageCacheBatch must not be retried after corruption is latched")
}

// TestImageCacheCorruptionAtStartup verifies that a corruption error raised
// during the warmup load (loadCachedImages) latches the flag, lets init
// complete cleanly, and prevents any further GetAll calls. Without this the
// startup error would propagate and abort image cache init entirely
// (Forgejo #762 startup path).
func TestImageCacheCorruptionAtStartup(t *testing.T) {
	t.Parallel()

	provider := &mockImageProvider{}
	store := newMockCorruptStore()
	store.corruptLoadAll.Store(true)

	metrics, err := observability.NewMetrics()
	require.NoError(t, err)

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err)
	cache.SetImageProvider(provider)
	t.Cleanup(func() {
		require.NoError(t, cache.Close())
	})

	// loadCachedImages must have run once during init and surfaced corruption.
	require.GreaterOrEqual(t, store.loadAllCalls.Load(), int32(1),
		"startup load must reach GetAllImageCaches so corruption can be detected")

	// Provider-backed fetches still work; the corrupted DB is bypassed.
	_, err = cache.Get("Turdus merula")
	require.NoError(t, err)

	assert.Zero(t, store.getCalls.Load(),
		"GetImageCache must not run after startup corruption is latched")
	assert.Zero(t, store.saveCalls.Load(),
		"SaveImageCache must not run after startup corruption is latched")
}

// TestIsDatabaseCorruptionExposesHelper verifies that the datastore package
// exposes a corruption detector that callers (such as the image cache) can
// invoke to recognise malformed-database errors. Without this helper, the
// imageprovider package cannot tell corruption apart from transient errors
// and ends up reporting the same fatal condition to Sentry over and over.
func TestIsDatabaseCorruptionExposesHelper(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name string
		err  error
		want bool
	}{
		{"malformed", errors.NewStd("database disk image is malformed"), true},
		{"not_a_database", errors.NewStd("file is not a database"), true},
		{"generic_corrupt", errors.NewStd("table is corrupt"), true},
		{"locked", errors.NewStd("database is locked"), false},
		{"transient", errors.NewStd("connection reset"), false},
		{"nil", nil, false},
		{"wrapped",
			errors.New(errors.NewStd("database disk image is malformed")).
				Component("imageprovider").
				Category(errors.CategoryImageCache).
				Build(),
			true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tc.want, datastore.IsDatabaseCorruption(tc.err))
		})
	}
}
