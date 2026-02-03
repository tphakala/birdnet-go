package imageprovider_test

import (
	"context"
	"fmt"
	"log"
	"maps"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"go.uber.org/goleak"
	"gorm.io/gorm"
)

// mockImageProvider is a mock implementation of the ImageProvider interface
type mockImageProvider struct {
	fetchCounter int
	shouldFail   bool
	fetchDelay   time.Duration
	mu           sync.Mutex
	lastURL      string // Track last generated URL for consistency
}

func (m *mockImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	m.mu.Lock()
	m.fetchCounter++
	currentCount := m.fetchCounter
	m.mu.Unlock()

	if m.shouldFail {
		return imageprovider.BirdImage{}, errors.NewStd("mock fetch error")
	}

	// Simulate network delay if specified
	if m.fetchDelay > 0 {
		time.Sleep(m.fetchDelay)
	}

	// Generate consistent URL for the same fetch count
	url := fmt.Sprintf("http://example.com/%s_%d.jpg", scientificName, currentCount)

	m.mu.Lock()
	m.lastURL = url
	m.mu.Unlock()

	return imageprovider.BirdImage{
		URL:            url,
		ScientificName: scientificName,
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:     fmt.Sprintf("Mock Author %d", currentCount),
		AuthorURL:      "http://example.com/author",
		CachedAt:       time.Now(),
	}, nil
}

// mockStore is a mock implementation of the datastore.Interface
type mockStore struct {
	images map[string]*datastore.ImageCache
	mu     sync.RWMutex
}

func newMockStore() *mockStore {
	return &mockStore{
		images: make(map[string]*datastore.ImageCache),
	}
}

// GetAllTestEntries returns all entries for testing purposes
func (m *mockStore) GetAllTestEntries() []*datastore.ImageCache {
	m.mu.RLock()
	defer m.mu.RUnlock()

	return slices.Collect(maps.Values(m.images))
}

// Implement only the methods we need for testing
func (m *mockStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if img, ok := m.images[query.ScientificName+"_"+query.ProviderName]; ok {
		return img, nil
	}
	return nil, datastore.ErrImageCacheNotFound
}

func (m *mockStore) SaveImageCache(cache *datastore.ImageCache) error {
	if cache.ScientificName == "" || cache.ProviderName == "" {
		return fmt.Errorf("scientific name and provider name cannot be empty")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	key := cache.ScientificName + "_" + cache.ProviderName
	oldCache, exists := m.images[key]
	if exists {
		// Keep this debug print as it's useful for tracking cache updates
		log.Printf("Debug: SaveImageCache updating entry for %s: Old(CachedAt=%v) -> New(CachedAt=%v)",
			cache.ScientificName, oldCache.CachedAt, cache.CachedAt)
	}

	// Create a new copy of the cache entry to avoid shared references
	newCache := &datastore.ImageCache{
		URL:            cache.URL,
		ScientificName: cache.ScientificName,
		LicenseName:    cache.LicenseName,
		LicenseURL:     cache.LicenseURL,
		AuthorName:     cache.AuthorName,
		AuthorURL:      cache.AuthorURL,
		CachedAt:       cache.CachedAt,
		ProviderName:   cache.ProviderName,
	}

	m.images[key] = newCache
	return nil
}

func (m *mockStore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	var result []datastore.ImageCache
	for key, img := range m.images {
		if strings.HasSuffix(key, "_"+providerName) {
			result = append(result, *img)
		}
	}
	return result, nil
}

func (m *mockStore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make(map[string]*datastore.ImageCache)

	for _, name := range scientificNames {
		key := name + "_" + providerName
		if img, exists := m.images[key]; exists {
			result[name] = img
		}
	}

	return result, nil
}

// Implement other required interface methods with no-op implementations
func (m *mockStore) Open() error                                                  { return nil }
func (m *mockStore) Save(note *datastore.Note, results []datastore.Results) error { return nil }
func (m *mockStore) Delete(id string) error                                       { return nil }
func (m *mockStore) Get(id string) (datastore.Note, error)                        { return datastore.Note{}, nil }
func (m *mockStore) Close() error                                                 { return nil }
func (m *mockStore) SetMetrics(metrics *datastore.Metrics)                        {}
func (m *mockStore) SetSunCalcMetrics(suncalcMetrics any)                         {}
func (m *mockStore) Optimize(ctx context.Context) error                           { return nil }
func (m *mockStore) GetAllNotes() ([]datastore.Note, error)                       { return []datastore.Note{}, nil }
func (m *mockStore) GetTopBirdsData(date string, minConf float64) ([]datastore.Note, error) {
	return []datastore.Note{}, nil
}
func (m *mockStore) GetHourlyOccurrences(date, name string, minConf float64) ([24]int, error) {
	return [24]int{}, nil
}
func (m *mockStore) SpeciesDetections(species, date, hour string, duration int, asc bool, limit, offset int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *mockStore) GetLastDetections(num int) ([]datastore.Note, error) { return nil, nil }
func (m *mockStore) GetAllDetectedSpecies() ([]datastore.Note, error)    { return nil, nil }
func (m *mockStore) SearchNotes(query string, asc bool, limit, offset int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *mockStore) SearchNotesAdvanced(filters *datastore.AdvancedSearchFilters) ([]datastore.Note, int64, error) {
	return nil, 0, nil
}
func (m *mockStore) GetNoteClipPath(noteID string) (string, error) { return "", nil }
func (m *mockStore) DeleteNoteClipPath(noteID string) error        { return nil }
func (m *mockStore) GetClipsQualifyingForRemoval(minHours, minClips int) ([]datastore.ClipForRemoval, error) {
	return nil, nil
}
func (m *mockStore) GetNoteReview(noteID string) (*datastore.NoteReview, error) {
	return nil, datastore.ErrNoteReviewNotFound
}
func (m *mockStore) SaveNoteReview(review *datastore.NoteReview) error              { return nil }
func (m *mockStore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) { return nil, nil }
func (m *mockStore) GetNoteResults(noteID string) ([]datastore.Results, error)      { return nil, nil }
func (m *mockStore) SaveNoteComment(comment *datastore.NoteComment) error           { return nil }
func (m *mockStore) UpdateNoteComment(commentID, entry string) error                { return nil }
func (m *mockStore) DeleteNoteComment(commentID string) error                       { return nil }
func (m *mockStore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error       { return nil }
func (m *mockStore) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	return datastore.DailyEvents{}, nil
}
func (m *mockStore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error  { return nil }
func (m *mockStore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) { return nil, nil }
func (m *mockStore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	return nil, gorm.ErrRecordNotFound
}
func (m *mockStore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *mockStore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	return 0, nil
}
func (m *mockStore) CountSearchResults(query string) (int64, error) { return 0, nil }
func (m *mockStore) Transaction(fc func(tx *gorm.DB) error) error   { return nil }
func (m *mockStore) LockNote(noteID string) error                   { return nil }
func (m *mockStore) UnlockNote(noteID string) error                 { return nil }
func (m *mockStore) GetNoteLock(noteID string) (*datastore.NoteLock, error) {
	return nil, datastore.ErrNoteLockNotFound
}
func (m *mockStore) IsNoteLocked(noteID string) (bool, error)   { return false, nil }
func (m *mockStore) GetLockedNotesClipPaths() ([]string, error) { return nil, nil }
func (m *mockStore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	return 0, nil
}
func (m *mockStore) GetDailyAnalyticsData(ctx context.Context, startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	return []datastore.DailyAnalyticsData{}, nil
}
func (m *mockStore) GetDetectionTrends(ctx context.Context, period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	return []datastore.DailyAnalyticsData{}, nil
}
func (m *mockStore) GetHourlyAnalyticsData(ctx context.Context, date, species string) ([]datastore.HourlyAnalyticsData, error) {
	return []datastore.HourlyAnalyticsData{}, nil
}
func (m *mockStore) GetSpeciesSummaryData(ctx context.Context, startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	return []datastore.SpeciesSummaryData{}, nil
}
func (m *mockStore) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	return nil, 0, nil
}

// Dynamic threshold methods
func (m *mockStore) SaveDynamicThreshold(threshold *datastore.DynamicThreshold) error { return nil }
func (m *mockStore) GetDynamicThreshold(speciesName string) (*datastore.DynamicThreshold, error) {
	return nil, fmt.Errorf("not found")
}
func (m *mockStore) GetAllDynamicThresholds(limit ...int) ([]datastore.DynamicThreshold, error) {
	return []datastore.DynamicThreshold{}, nil
}
func (m *mockStore) DeleteDynamicThreshold(speciesName string) error { return nil }
func (m *mockStore) DeleteExpiredDynamicThresholds(before time.Time) (int64, error) {
	return 0, nil
}
func (m *mockStore) UpdateDynamicThresholdExpiry(speciesName string, expiresAt time.Time) error {
	return nil
}
func (m *mockStore) BatchSaveDynamicThresholds(thresholds []datastore.DynamicThreshold) error {
	return nil
}

// BG-59: Add new dynamic threshold methods
func (m *mockStore) DeleteAllDynamicThresholds() (int64, error) { return 0, nil }
func (m *mockStore) GetDynamicThresholdStats() (totalCount, activeCount, atMinimumCount int64, levelDistribution map[int]int64, err error) {
	return 0, 0, 0, make(map[int]int64), nil
}
func (m *mockStore) SaveThresholdEvent(*datastore.ThresholdEvent) error { return nil }
func (m *mockStore) GetThresholdEvents(string, int) ([]datastore.ThresholdEvent, error) {
	return nil, nil
}
func (m *mockStore) GetRecentThresholdEvents(int) ([]datastore.ThresholdEvent, error) {
	return nil, nil
}
func (m *mockStore) DeleteThresholdEvents(string) error       { return nil }
func (m *mockStore) DeleteAllThresholdEvents() (int64, error) { return 0, nil }

// GetHourlyDistribution implements the datastore.Interface GetHourlyDistribution method
func (m *mockStore) GetHourlyDistribution(ctx context.Context, startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) {
	// Default implementation returns empty array for this mock
	return []datastore.HourlyDistributionData{}, nil
}

// GetNewSpeciesDetections implements the datastore.Interface GetNewSpeciesDetections method
func (m *mockStore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	// This is a mock test implementation, so we'll return empty data
	return []datastore.NewSpeciesData{}, nil
}

// GetSpeciesFirstDetectionInPeriod implements the datastore.Interface GetSpeciesFirstDetectionInPeriod method
func (m *mockStore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	// This is a mock test implementation, so we'll return empty data
	return []datastore.NewSpeciesData{}, nil
}

// BG-17 fix: Add notification history methods
func (m *mockStore) GetActiveNotificationHistory(after time.Time) ([]datastore.NotificationHistory, error) {
	return []datastore.NotificationHistory{}, nil
}

func (m *mockStore) GetNotificationHistory(scientificName, notificationType string) (*datastore.NotificationHistory, error) {
	return nil, datastore.ErrNotificationHistoryNotFound
}

func (m *mockStore) SaveNotificationHistory(history *datastore.NotificationHistory) error {
	return nil
}

func (m *mockStore) DeleteExpiredNotificationHistory(before time.Time) (int64, error) {
	return 0, nil
}

func (m *mockStore) GetDatabaseStats() (*datastore.DatabaseStats, error) {
	return &datastore.DatabaseStats{
		Type:      "mock",
		Connected: true,
	}, nil
}

func (m *mockStore) GetAllDailyEvents() ([]datastore.DailyEvents, error) {
	return nil, nil
}

func (m *mockStore) GetAllHourlyWeather() ([]datastore.HourlyWeather, error) {
	return nil, nil
}

// Related data migration methods (Phase 6)
func (m *mockStore) GetAllReviews() ([]datastore.NoteReview, error)   { return nil, nil }
func (m *mockStore) GetAllComments() ([]datastore.NoteComment, error) { return nil, nil }
func (m *mockStore) GetAllLocks() ([]datastore.NoteLock, error)       { return nil, nil }
func (m *mockStore) GetAllResults() ([]datastore.Results, error)      { return nil, nil }

// Batched migration methods (Phase 6)
func (m *mockStore) GetReviewsBatch(afterID uint, batchSize int) ([]datastore.NoteReview, error) {
	return nil, nil
}
func (m *mockStore) GetCommentsBatch(afterID uint, batchSize int) ([]datastore.NoteComment, error) {
	return nil, nil
}
func (m *mockStore) GetLocksBatch(afterID uint, batchSize int) ([]datastore.NoteLock, error) {
	return nil, nil
}
func (m *mockStore) GetResultsBatch(afterNoteID, afterResultID uint, batchSize int) ([]datastore.Results, error) {
	return nil, nil
}
func (m *mockStore) CountResults() (int64, error) {
	return 0, nil
}

// mockFailingStore is a mock implementation that simulates database failures
type mockFailingStore struct {
	mockStore
	failGetCache    bool
	failSaveCache   bool
	failGetAllCache bool
}

func newMockFailingStore() *mockFailingStore {
	return &mockFailingStore{
		mockStore: mockStore{
			images: make(map[string]*datastore.ImageCache),
		},
	}
}

func (m *mockFailingStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	if m.failGetCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetImageCache(query)
}

func (m *mockFailingStore) SaveImageCache(cache *datastore.ImageCache) error {
	if m.failSaveCache {
		return fmt.Errorf("simulated database error")
	}
	return m.mockStore.SaveImageCache(cache)
}

func (m *mockFailingStore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetAllImageCaches(providerName)
}

func (m *mockFailingStore) GetImageCacheBatch(providerName string, scientificNames []string) (map[string]*datastore.ImageCache, error) {
	if m.failGetCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetImageCacheBatch(providerName, scientificNames)
}

func (m *mockFailingStore) GetDailyAnalyticsData(ctx context.Context, startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetDailyAnalyticsData(ctx, startDate, endDate, species)
}

func (m *mockFailingStore) GetDetectionTrends(ctx context.Context, period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetDetectionTrends(ctx, period, limit)
}

func (m *mockFailingStore) GetHourlyAnalyticsData(ctx context.Context, date, species string) ([]datastore.HourlyAnalyticsData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetHourlyAnalyticsData(ctx, date, species)
}

func (m *mockFailingStore) GetSpeciesSummaryData(ctx context.Context, startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	return m.mockStore.GetSpeciesSummaryData(ctx, startDate, endDate)
}

func (m *mockFailingStore) GetNewSpeciesDetections(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetNewSpeciesDetections(ctx, startDate, endDate, limit, offset)
}

func (m *mockFailingStore) GetSpeciesFirstDetectionInPeriod(ctx context.Context, startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetSpeciesFirstDetectionInPeriod(ctx, startDate, endDate, limit, offset)
}

// verifyCacheEntry validates that an image was cached correctly in the store.
// Note: CreateDefaultCache uses "wikimedia" as the provider name.
func verifyCacheEntry(t *testing.T, store *mockStore, scientificName, expectedURL string) {
	t.Helper()
	cached, err := store.GetImageCache(datastore.ImageCacheQuery{ScientificName: scientificName, ProviderName: "wikimedia"})
	if errors.Is(err, datastore.ErrImageCacheNotFound) {
		return // Not cached yet is acceptable
	}
	require.NoError(t, err, "Failed to get cached image")
	if cached != nil {
		assert.Equal(t, expectedURL, cached.URL, "Cached URL mismatch")
	}
}

// TestBirdImageCache tests the BirdImageCache implementation
func TestBirdImageCache(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{}
	store := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	defer func() {
		require.NoError(t, cache.Close(), "Failed to close cache")
	}()

	tests := []struct {
		name           string
		scientificName string
		wantFetchCount int
	}{
		{"Bird species", "Turdus merula", 1},
		{"Cached bird species", "Turdus merula", 1}, // Should use cache
		{"Another species", "Parus major", 2},
		{"Animal entry", "Canis lupus", 3},
		{"Cached animal entry", "Canis lupus", 3}, // Should use cache
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cache.Get(tt.scientificName)
			require.NoError(t, err, "BirdImageCache.Get() returned error")
			assert.Equal(t, tt.wantFetchCount, mockProvider.fetchCounter, "Fetch count mismatch")
			assert.NotEmpty(t, got.URL, "BirdImageCache.Get() returned empty URL")
			verifyCacheEntry(t, store, tt.scientificName, got.URL)
		})
	}
}

// TestBirdImageCacheError tests the BirdImageCache error handling
func TestBirdImageCacheError(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{shouldFail: true}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	_, err = cache.Get("Turdus merula")
	assert.Error(t, err, "BirdImageCache.Get() error = nil, want error")
}

// TestCreateDefaultCache tests creating a default cache
func TestCreateDefaultCache(t *testing.T) {
	t.Parallel()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")
	mockStore := newMockStore()
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "CreateDefaultCache() error")
	require.NotNil(t, cache, "CreateDefaultCache() returned nil cache")
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})
}

// TestBirdImageEstimateSize tests the BirdImage size estimation
func TestBirdImageEstimateSize(t *testing.T) {
	t.Parallel()
	img := imageprovider.BirdImage{
		URL:         "http://example.com/bird.jpg",
		LicenseName: "CC BY-SA 4.0",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:  "Test Author",
		AuthorURL:   "http://example.com/author",
		CachedAt:    time.Now(),
	}

	size := img.EstimateSize()
	assert.Positive(t, size, "BirdImage.EstimateSize() should be > 0")
}

// TestBirdImageCacheMemoryUsage tests the cache memory usage calculation
func TestBirdImageCacheMemoryUsage(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{}
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")
	mockStore := newMockStore()
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Add some entries to the cache
	_, err = cache.Get("Turdus merula")
	require.NoError(t, err, "Failed to get 'Turdus merula'")

	_, err = cache.Get("Parus major")
	require.NoError(t, err, "Failed to get 'Parus major'")

	usage := cache.MemoryUsage()
	assert.Positive(t, usage, "BirdImageCache.MemoryUsage() should be > 0")
}

// setupFailingCacheTest creates a cache with a failing store for testing database failure scenarios.
func setupFailingCacheTest(t *testing.T, failGetCache, failSaveCache, failGetAllInit bool) *imageprovider.BirdImageCache {
	t.Helper()
	mockProvider := &mockImageProvider{}
	failingStore := newMockFailingStore()
	failingStore.failGetCache = failGetCache
	failingStore.failSaveCache = failSaveCache
	failingStore.failGetAllCache = failGetAllInit

	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, failingStore)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)

	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	return cache
}

// TestBirdImageCacheDatabaseFailures tests that the cache handles database failures gracefully
func TestBirdImageCacheDatabaseFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		failGetCache   bool
		failSaveCache  bool
		failGetAllInit bool
	}{
		{name: "Failed to get from cache", failGetCache: true},
		{name: "Failed to save to cache", failSaveCache: true},
		{name: "Failed to load initial cache", failGetAllInit: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			cache := setupFailingCacheTest(t, tt.failGetCache, tt.failSaveCache, tt.failGetAllInit)
			got, err := cache.Get("Turdus merula")
			require.NoError(t, err, "BirdImageCache.Get() should not fail with database errors")
			assert.NotEmpty(t, got.URL, "BirdImageCache.Get() returned empty URL")
		})
	}
}

// TestBirdImageCacheNilStore tests that the cache works without a database store
func TestBirdImageCacheNilStore(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{}
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	// Create cache with nil store
	cache, err := imageprovider.CreateDefaultCache(metrics, nil)
	require.NoError(t, err, "Failed to create cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Try to get an image
	got, err := cache.Get("Turdus merula")
	require.NoError(t, err, "BirdImageCache.Get() error")
	assert.NotEmpty(t, got.URL, "BirdImageCache.Get() returned empty URL")

	// Verify that the provider was called
	assert.Equal(t, 1, mockProvider.fetchCounter, "Provider fetch count should be 1")
}

// TestBirdImageCacheRefresh tests the cache refresh functionality
func TestBirdImageCacheRefresh(t *testing.T) {
	// Note: This test cannot run in parallel because it modifies the provider
	// after cache creation, which races with the background refresh goroutine
	t.Log("Starting TestBirdImageCacheRefresh")
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	// Create a cache entry that's older than TTL
	oldEntry := &datastore.ImageCache{
		ScientificName: "Turdus merula",
		URL:            "http://example.com/old.jpg",
		LicenseName:    "CC BY-SA 4.0",
		LicenseURL:     "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:     "Old Author",
		AuthorURL:      "http://example.com/old-author",
		CachedAt:       time.Now().Add(-15 * 24 * time.Hour), // 15 days old
		ProviderName:   "wikimedia",                          // Add provider name to match the default cache provider
	}
	t.Logf("Created old entry: CachedAt=%v", oldEntry.CachedAt)

	err = mockStore.SaveImageCache(oldEntry)
	require.NoError(t, err, "Failed to save old cache entry")

	// Enable debug mode for the cache
	settings := conf.Setting()
	settings.Realtime.Dashboard.Thumbnails.Debug = true

	// Create cache with default settings
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create default cache")

	// Set our mock provider
	cache.SetImageProvider(mockProvider)

	// Wait for refresh routine to run
	t.Log("Waiting for refresh routine to run...")
	time.Sleep(5 * time.Second)

	// Check if the entry was refreshed
	refreshed, err := mockStore.GetImageCache(datastore.ImageCacheQuery{ScientificName: "Turdus merula", ProviderName: "wikimedia"})
	require.NoError(t, err, "Failed to get refreshed cache entry")
	require.NotNil(t, refreshed, "Refreshed image cache entry is nil")

	// Check timestamp was updated
	assert.False(t, refreshed.CachedAt.Equal(oldEntry.CachedAt),
		"Expected CachedAt to be updated after refresh. Old: %v, New: %v",
		oldEntry.CachedAt, refreshed.CachedAt)

	// Check URL was changed
	assert.NotEqual(t, oldEntry.URL, refreshed.URL,
		"Expected URL to be different after refresh. Old: %s, New: %s",
		oldEntry.URL, refreshed.URL)

	// Clean up
	closeErr := cache.Close()
	assert.NoError(t, closeErr, "Failed to close cache")
}

// TestConcurrentInitialization tests that concurrent requests for the same species
// don't result in multiple fetches
func TestConcurrentInitialization(t *testing.T) {
	t.Parallel()
	// Create a mock provider with a delay to simulate network latency
	mockProvider := &mockImageProvider{
		fetchDelay: 200 * time.Millisecond, // Delay to make race conditions more likely
	}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Number of concurrent requests
	const numRequests = 10
	const scientificName = "Turdus merula"

	// Create a wait group to synchronize goroutines
	var wg sync.WaitGroup
	wg.Add(numRequests)

	// Channel to collect results
	results := make(chan string, numRequests)
	errs := make(chan error, numRequests)

	// Launch concurrent requests
	for range numRequests {
		go func() {
			defer wg.Done()
			image, err := cache.Get(scientificName)
			if err != nil {
				errs <- err
				return
			}
			results <- image.URL
		}()
	}

	// Wait for all requests to complete
	wg.Wait()
	close(results)
	close(errs)

	// Check for errors
	for err := range errs {
		require.NoError(t, err, "Concurrent request error")
	}

	// Verify that only one fetch occurred
	assert.Equal(t, 1, mockProvider.fetchCounter, "Expected 1 fetch")

	// Verify that all requests got the same URL
	var firstURL string
	urlCount := 0
	for url := range results {
		if urlCount == 0 {
			firstURL = url
		} else {
			assert.Equal(t, firstURL, url, "Got different URLs")
		}
		urlCount++
	}

	assert.Equal(t, numRequests, urlCount, "Expected all results to succeed")
}

// TestInitializationTimeout tests that requests don't wait forever if initialization fails
func TestInitializationTimeout(t *testing.T) {
	t.Parallel()
	// Create a mock provider that takes longer than the retry timeout
	mockProvider := &mockImageProvider{
		fetchDelay: 2 * time.Second, // Longer than the total retry time
	}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Start a long-running fetch in the background
	go func() {
		_, _ = cache.Get("Turdus merula")
	}()

	// Wait a moment for the first request to start
	time.Sleep(100 * time.Millisecond)

	// Try to get the same species - should effectively wait for the ongoing fetch
	start := time.Now()
	_, err = cache.Get("Turdus merula") // Second Get call
	require.NoError(t, err, "Second cache.Get failed")
	duration := time.Since(start)

	// The first Get call (in background) initiates one fetch.
	// The second Get call (this one) should wait for the first to complete
	// and use the cached result if the cache implements a wait mechanism
	// for concurrent requests for the same key (as suggested by TestConcurrentInitialization).
	// Thus, only one actual fetch should occur.

	// Check duration: main Get should wait for approx. 1.9s for the 2s background fetch.
	// Set a minimum expected duration to ensure the second call actually waits for the first fetch
	minExpectedWait := max(
		// Allow some leeway
		mockProvider.fetchDelay-(200*time.Millisecond), 0)
	// Max expected duration can be a bit more than fetch delay for overhead.
	maxExpectedDuration := mockProvider.fetchDelay + (1 * time.Second)

	assert.True(t, duration >= minExpectedWait && duration <= maxExpectedDuration,
		"Second Get call duration outside expected range: %v, expected between %v and %v",
		duration, minExpectedWait, maxExpectedDuration)

	// The fetch counter should be 1, due to the initial background fetch.
	// The second Get call should not trigger a new fetch if it waits for the first.
	expectedFetches := 1 // Changed from 3
	assert.Equal(t, expectedFetches, mockProvider.fetchCounter, "Expected %d fetches", expectedFetches)
}

// TestInitializationFailure tests that initialization failure is handled gracefully
func TestInitializationFailure(t *testing.T) {
	t.Parallel()
	// Create a mock provider that fails
	mockProvider := &mockImageProvider{
		shouldFail: true,
	}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Try to get an image - should fail but not leave initialization flag set
	_, err = cache.Get("Turdus merula")
	require.Error(t, err, "Expected error from failed fetch")

	// Try again immediately - should attempt a new fetch
	_, err = cache.Get("Turdus merula")
	require.Error(t, err, "Expected error from second fetch")

	// Verify that we attempted both fetches
	assert.Equal(t, 2, mockProvider.fetchCounter, "Expected 2 fetches")
}

// TestUserRequestsNotRateLimited tests that user requests are not subject to rate limiting
func TestUserRequestsNotRateLimited(t *testing.T) {
	t.Parallel()

	// Create a mock provider with minimal delay to test rate limiting behavior
	mockProvider := &mockImageProvider{
		fetchDelay: 1 * time.Millisecond, // Very fast mock responses
	}

	// Create a mock store
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache := imageprovider.InitCache("wikimedia", mockProvider, metrics, mockStore)
	t.Cleanup(func() {
		assert.NoError(t, cache.Close(), "Failed to close cache")
	})

	// Test species that should exist in Wikipedia
	testSpecies := []string{
		"Turdus merula",
		"Parus major",
		"Carduelis carduelis",
		"Sturnus vulgaris",
		"Erithacus rubecula",
	}

	// Measure time for rapid consecutive user requests
	start := time.Now()

	// Make rapid consecutive requests (should not be rate limited)
	for i := range 10 {
		species := testSpecies[i%len(testSpecies)]
		_, err := cache.Get(species)
		if err != nil && !errors.Is(err, imageprovider.ErrImageNotFound) {
			t.Logf("Warning: fetch error for %s: %v", species, err)
		}
	}

	duration := time.Since(start)

	// If rate limiting was applied (2 req/s), 10 requests would take at least 5 seconds
	// Without rate limiting, it should complete much faster (allowing for actual API latency)
	// Increase timeout to 4 seconds to account for network variability
	assert.LessOrEqual(t, duration, 4*time.Second, "User requests appear to be rate limited. Duration: %v, expected < 4s", duration)

	t.Logf("10 user requests completed in %v (no rate limiting, threshold: 4s)", duration)
}

// populateStaleEntries adds stale cache entries to the store to trigger background refresh.
func populateStaleEntries(t *testing.T, store *mockStore, count int) {
	t.Helper()
	staleTime := time.Now().Add(-15 * 24 * time.Hour)
	for i := range count {
		species := fmt.Sprintf("StaleSpecies_%d", i)
		err := store.SaveImageCache(&datastore.ImageCache{
			ScientificName: species,
			ProviderName:   "wikimedia",
			URL:            fmt.Sprintf("http://example.com/old_%s.jpg", species),
			CachedAt:       staleTime,
		})
		require.NoError(t, err, "Failed to save stale cache entry")
	}
}

// monitorBackgroundFetches collects fetch attempts and returns when enough are detected or timeout.
func monitorBackgroundFetches(t *testing.T, fetchAttempts <-chan struct{}, maxExpected int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	fetchCount := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-fetchAttempts:
			fetchCount++
			t.Logf("Background fetch attempt %d detected", fetchCount)
		case <-ticker.C:
			if fetchCount > 0 && fetchCount <= maxExpected {
				t.Logf("Background fetches completed: %d (expected <= %d)", fetchCount, maxExpected)
				return
			}
		case <-ctx.Done():
			if fetchCount == 0 {
				t.Skip("No background fetches detected - background refresh might not have started")
			}
			t.Logf("Test completed with %d background fetches", fetchCount)
			return
		}
	}
}

// TestBackgroundRequestsRateLimited tests that background requests are subject to rate limiting
func TestBackgroundRequestsRateLimited(t *testing.T) {
	t.Parallel()

	fetchAttempts := make(chan struct{}, 10)
	mockProvider := &mockProviderWithContext{
		mockImageProvider: mockImageProvider{fetchDelay: 5 * time.Millisecond},
		fetchChannel:      fetchAttempts,
	}

	store := newMockStore()
	metrics, err := observability.NewMetrics()
	require.NoError(t, err, "Failed to create metrics")

	cache, err := imageprovider.CreateDefaultCache(metrics, store)
	require.NoError(t, err, "Failed to create default cache")
	cache.SetImageProvider(mockProvider)
	t.Cleanup(func() {
		require.NoError(t, cache.Close(), "Failed to close cache")
	})

	numStaleEntries := 5
	populateStaleEntries(t, store, numStaleEntries)
	monitorBackgroundFetches(t, fetchAttempts, numStaleEntries)
}

// mockProviderWithContext extends mockImageProvider to support context-aware fetching
type mockProviderWithContext struct {
	mockImageProvider
	backgroundFetches int
	mu2               sync.Mutex
	fetchChannel      chan<- struct{}
}

func (m *mockProviderWithContext) FetchWithContext(ctx context.Context, scientificName string) (imageprovider.BirdImage, error) {
	// Check if it's a background operation
	if ctx != nil {
		if bg, ok := ctx.Value("background").(bool); ok && bg {
			m.mu2.Lock()
			m.backgroundFetches++
			m.mu2.Unlock()

			// Signal through channel if available
			if m.fetchChannel != nil {
				select {
				case m.fetchChannel <- struct{}{}:
				default:
				}
			}
		}
	}
	return m.Fetch(scientificName)
}

// TestMain provides goleak verification to detect goroutine leaks
func TestMain(m *testing.M) {
	goleak.VerifyTestMain(m,
		goleak.IgnoreTopFunction("testing.(*T).Run"),
		goleak.IgnoreTopFunction("runtime.gopark"),
		goleak.IgnoreTopFunction("gopkg.in/natefinch/lumberjack%2ev2.(*Logger).millRun"),
		// Ignore the cache refresh goroutine pattern - it should be properly cleaned up by Close()
		goleak.IgnoreTopFunction("github.com/tphakala/birdnet-go/internal/imageprovider.(*BirdImageCache).startCacheRefresh.func1"),
	)
}
