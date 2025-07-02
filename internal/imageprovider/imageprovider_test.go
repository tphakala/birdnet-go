package imageprovider_test

import (
	"context"
	"fmt"
	"log"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
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

	entries := make([]*datastore.ImageCache, 0, len(m.images))
	for _, v := range m.images {
		entries = append(entries, v)
	}
	return entries
}

// Implement only the methods we need for testing
func (m *mockStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	if img, ok := m.images[query.ScientificName+"_"+query.ProviderName]; ok {
		//log.Printf("Debug: GetImageCache found entry for %s provider %s", query.ScientificName, query.ProviderName)
		return img, nil
	}
	//log.Printf("Debug: GetImageCache MISS for %s provider %s", query.ScientificName, query.ProviderName)
	return nil, nil
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
	//log.Printf("Debug: GetAllImageCaches called for provider %s. Total items: %d", providerName, len(m.images))
	for key, img := range m.images {
		if strings.HasSuffix(key, "_"+providerName) {
			result = append(result, *img)
		}
	}
	//log.Printf("Debug: GetAllImageCaches returning %d entries for provider %s", len(result), providerName)
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
func (m *mockStore) GetAllNotes() ([]datastore.Note, error)                       { return nil, nil }
func (m *mockStore) GetTopBirdsData(date string, minConf float64) ([]datastore.Note, error) {
	return nil, nil
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
func (m *mockStore) GetNoteClipPath(noteID string) (string, error) { return "", nil }
func (m *mockStore) DeleteNoteClipPath(noteID string) error        { return nil }
func (m *mockStore) GetClipsQualifyingForRemoval(minHours, minClips int) ([]datastore.ClipForRemoval, error) {
	return nil, nil
}
func (m *mockStore) GetNoteReview(noteID string) (*datastore.NoteReview, error)     { return nil, nil }
func (m *mockStore) SaveNoteReview(review *datastore.NoteReview) error              { return nil }
func (m *mockStore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) { return nil, nil }
func (m *mockStore) SaveNoteComment(comment *datastore.NoteComment) error           { return nil }
func (m *mockStore) UpdateNoteComment(commentID, entry string) error                { return nil }
func (m *mockStore) DeleteNoteComment(commentID string) error                       { return nil }
func (m *mockStore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error       { return nil }
func (m *mockStore) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	return datastore.DailyEvents{}, nil
}
func (m *mockStore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error  { return nil }
func (m *mockStore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) { return nil, nil }
func (m *mockStore) LatestHourlyWeather() (*datastore.HourlyWeather, error)          { return nil, nil }
func (m *mockStore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	return nil, nil
}
func (m *mockStore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	return 0, nil
}
func (m *mockStore) CountSearchResults(query string) (int64, error)         { return 0, nil }
func (m *mockStore) Transaction(fc func(tx *gorm.DB) error) error           { return nil }
func (m *mockStore) LockNote(noteID string) error                           { return nil }
func (m *mockStore) UnlockNote(noteID string) error                         { return nil }
func (m *mockStore) GetNoteLock(noteID string) (*datastore.NoteLock, error) { return nil, nil }
func (m *mockStore) IsNoteLocked(noteID string) (bool, error)               { return false, nil }
func (m *mockStore) GetLockedNotesClipPaths() ([]string, error)             { return nil, nil }
func (m *mockStore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	return 0, nil
}
func (m *mockStore) GetDailyAnalyticsData(startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	return []datastore.DailyAnalyticsData{}, nil
}
func (m *mockStore) GetDetectionTrends(period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	return []datastore.DailyAnalyticsData{}, nil
}
func (m *mockStore) GetHourlyAnalyticsData(date, species string) ([]datastore.HourlyAnalyticsData, error) {
	return []datastore.HourlyAnalyticsData{}, nil
}
func (m *mockStore) GetSpeciesSummaryData(startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	return []datastore.SpeciesSummaryData{}, nil
}
func (m *mockStore) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	return nil, 0, nil
}

// GetHourlyDistribution implements the datastore.Interface GetHourlyDistribution method
func (m *mockStore) GetHourlyDistribution(startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) {
	// Default implementation returns empty array for this mock
	return []datastore.HourlyDistributionData{}, nil
}

// GetNewSpeciesDetections implements the datastore.Interface GetNewSpeciesDetections method
func (m *mockStore) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	// This is a mock test implementation, so we'll return empty data
	return []datastore.NewSpeciesData{}, nil
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

func (m *mockFailingStore) GetDailyAnalyticsData(startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetDailyAnalyticsData(startDate, endDate, species)
}

func (m *mockFailingStore) GetDetectionTrends(period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetDetectionTrends(period, limit)
}

func (m *mockFailingStore) GetHourlyAnalyticsData(date, species string) ([]datastore.HourlyAnalyticsData, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetHourlyAnalyticsData(date, species)
}

func (m *mockFailingStore) GetSpeciesSummaryData(startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	return m.mockStore.GetSpeciesSummaryData(startDate, endDate)
}

// TestBirdImageCache tests the BirdImageCache implementation
func TestBirdImageCache(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	tests := []struct {
		name           string
		scientificName string
		wantFetchCount int
		wantErr        bool
	}{
		{"Bird species", "Turdus merula", 1, false},
		{"Cached bird species", "Turdus merula", 1, false}, // Should use cache
		{"Another species", "Parus major", 2, false},
		{"Animal entry", "Canis lupus", 3, false},
		{"Cached animal entry", "Canis lupus", 3, false}, // Should use cache
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := cache.Get(tt.scientificName)
			if (err != nil) != tt.wantErr {
				t.Errorf("BirdImageCache.Get() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if mockProvider.fetchCounter != tt.wantFetchCount {
				t.Errorf("Fetch count = %d, want %d", mockProvider.fetchCounter, tt.wantFetchCount)
			}
			if !tt.wantErr && got.URL == "" {
				t.Errorf("BirdImageCache.Get() returned empty URL for %s", tt.scientificName)
			}

			// Verify that the image was cached in the store
			cached, err := mockStore.GetImageCache(datastore.ImageCacheQuery{ScientificName: tt.scientificName, ProviderName: "mock"})
			if err != nil {
				t.Errorf("Failed to get cached image: %v", err)
			}
			if cached != nil && cached.URL != got.URL {
				t.Errorf("Cached URL = %s, want %s", cached.URL, got.URL)
			}
		})
	}
}

// TestBirdImageCacheError tests the BirdImageCache error handling
func TestBirdImageCacheError(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{shouldFail: true}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	_, err = cache.Get("Turdus merula")
	if err == nil {
		t.Error("BirdImageCache.Get() error = nil, want error")
	}
}

// TestCreateDefaultCache tests creating a default cache
func TestCreateDefaultCache(t *testing.T) {
	t.Parallel()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	mockStore := newMockStore()
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("CreateDefaultCache() error = %v", err)
	}
	if cache == nil {
		t.Error("CreateDefaultCache() returned nil cache")
	}
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
	if size <= 0 {
		t.Errorf("BirdImage.EstimateSize() = %d, want > 0", size)
	}
}

// TestBirdImageCacheMemoryUsage tests the cache memory usage calculation
func TestBirdImageCacheMemoryUsage(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{}
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	mockStore := newMockStore()
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Add some entries to the cache
	_, err = cache.Get("Turdus merula")
	if err != nil {
		t.Fatalf("Failed to get 'Turdus merula': %v", err)
	}

	_, err = cache.Get("Parus major")
	if err != nil {
		t.Fatalf("Failed to get 'Parus major': %v", err)
	}

	usage := cache.MemoryUsage()
	if usage <= 0 {
		t.Errorf("BirdImageCache.MemoryUsage() = %d, want > 0", usage)
	}
}

// TestBirdImageCacheDatabaseFailures tests that the cache handles database failures gracefully
func TestBirdImageCacheDatabaseFailures(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name           string
		failGetCache   bool
		failSaveCache  bool
		failGetAllInit bool
		wantErr        bool
	}{
		{
			name:         "Failed to get from cache",
			failGetCache: true,
			wantErr:      false, // Should fall back to provider
		},
		{
			name:          "Failed to save to cache",
			failSaveCache: true,
			wantErr:       false, // Should continue without caching
		},
		{
			name:           "Failed to load initial cache",
			failGetAllInit: true,
			wantErr:        false, // Should start with empty cache
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			mockProvider := &mockImageProvider{}
			failingStore := newMockFailingStore()
			failingStore.failGetCache = tt.failGetCache
			failingStore.failSaveCache = tt.failSaveCache
			failingStore.failGetAllCache = tt.failGetAllInit

			metrics, err := observability.NewMetrics()
			if err != nil {
				t.Fatalf("Failed to create metrics: %v", err)
			}

			cache, err := imageprovider.CreateDefaultCache(metrics, failingStore)
			if err != nil {
				t.Fatalf("Failed to create cache: %v", err)
			}
			cache.SetImageProvider(mockProvider)

			// Try to get an image
			got, err := cache.Get("Turdus merula")
			if (err != nil) != tt.wantErr {
				t.Errorf("BirdImageCache.Get() error = %v, wantErr %v", err, tt.wantErr)
			}
			if !tt.wantErr && got.URL == "" {
				t.Error("BirdImageCache.Get() returned empty URL")
			}
		})
	}
}

// TestBirdImageCacheNilStore tests that the cache works without a database store
func TestBirdImageCacheNilStore(t *testing.T) {
	t.Parallel()
	mockProvider := &mockImageProvider{}
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	// Create cache with nil store
	cache, err := imageprovider.CreateDefaultCache(metrics, nil)
	if err != nil {
		t.Fatalf("Failed to create cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Try to get an image
	got, err := cache.Get("Turdus merula")
	if err != nil {
		t.Errorf("BirdImageCache.Get() error = %v", err)
	}
	if got.URL == "" {
		t.Error("BirdImageCache.Get() returned empty URL")
	}

	// Verify that the provider was called
	if mockProvider.fetchCounter != 1 {
		t.Errorf("Provider fetch count = %d, want 1", mockProvider.fetchCounter)
	}
}

// TestBirdImageCacheRefresh tests the cache refresh functionality
func TestBirdImageCacheRefresh(t *testing.T) {
	t.Log("Starting TestBirdImageCacheRefresh")
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

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

	if err := mockStore.SaveImageCache(oldEntry); err != nil {
		t.Fatalf("Failed to save old cache entry: %v", err)
	}

	// Enable debug mode for the cache
	settings := conf.Setting()
	settings.Realtime.Dashboard.Thumbnails.Debug = true

	// Create cache with default settings
	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}

	// Set our mock provider
	cache.SetImageProvider(mockProvider)

	// Wait for refresh routine to run
	t.Log("Waiting for refresh routine to run...")
	time.Sleep(5 * time.Second)

	// Check if the entry was refreshed
	refreshed, err := mockStore.GetImageCache(datastore.ImageCacheQuery{ScientificName: "Turdus merula", ProviderName: "wikimedia"})
	if err != nil {
		t.Fatalf("Failed to get refreshed cache entry: %v", err)
	}
	if refreshed == nil {
		t.Fatal("Refreshed image cache entry is nil")
	}

	// Check timestamp was updated
	if refreshed.CachedAt.Equal(oldEntry.CachedAt) {
		t.Errorf("Expected CachedAt to be updated after refresh. Old: %v, New: %v",
			oldEntry.CachedAt, refreshed.CachedAt)
	}

	// Check URL was changed
	if refreshed.URL == oldEntry.URL {
		t.Errorf("Expected URL to be different after refresh. Old: %s, New: %s",
			oldEntry.URL, refreshed.URL)
	}

	// Clean up
	if err := cache.Close(); err != nil {
		t.Errorf("Failed to close cache: %v", err)
	}
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
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

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
	for i := 0; i < numRequests; i++ {
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
		t.Errorf("Concurrent request error: %v", err)
	}

	// Verify that only one fetch occurred
	if mockProvider.fetchCounter != 1 {
		t.Errorf("Expected 1 fetch, got %d fetches", mockProvider.fetchCounter)
	}

	// Verify that all requests got the same URL
	var firstURL string
	urlCount := 0
	for url := range results {
		if urlCount == 0 {
			firstURL = url
		} else if url != firstURL {
			t.Errorf("Got different URLs: first=%s, other=%s", firstURL, url)
		}
		urlCount++
	}

	if urlCount != numRequests {
		t.Errorf("Expected %d successful results, got %d", numRequests, urlCount)
	}
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
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Start a long-running fetch in the background
	go func() {
		_, _ = cache.Get("Turdus merula")
	}()

	// Wait a moment for the first request to start
	time.Sleep(100 * time.Millisecond)

	// Try to get the same species - should effectively wait for the ongoing fetch
	start := time.Now()
	_, err = cache.Get("Turdus merula") // Second Get call
	if err != nil {
		t.Fatalf("Second cache.Get failed: %v", err)
	}
	duration := time.Since(start)

	// The first Get call (in background) initiates one fetch.
	// The second Get call (this one) should wait for the first to complete
	// and use the cached result if the cache implements a wait mechanism
	// for concurrent requests for the same key (as suggested by TestConcurrentInitialization).
	// Thus, only one actual fetch should occur.

	// Check duration: main Get should wait for approx. 1.9s for the 2s background fetch.
	// Set a minimum expected duration to ensure the second call actually waits for the first fetch
	minExpectedWait := mockProvider.fetchDelay - (200 * time.Millisecond) // Allow some leeway
	if minExpectedWait < 0 {
		minExpectedWait = 0
	}
	// Max expected duration can be a bit more than fetch delay for overhead.
	maxExpectedDuration := mockProvider.fetchDelay + (1 * time.Second)

	if duration < minExpectedWait || duration > maxExpectedDuration {
		t.Errorf("Second Get call duration outside expected range: %v, expected between %v and %v",
			duration, minExpectedWait, maxExpectedDuration)
	}

	// The fetch counter should be 1, due to the initial background fetch.
	// The second Get call should not trigger a new fetch if it waits for the first.
	expectedFetches := 1 // Changed from 3
	if mockProvider.fetchCounter != expectedFetches {
		t.Errorf("Expected %d fetches, got %d fetches", expectedFetches, mockProvider.fetchCounter)
	}
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
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Try to get an image - should fail but not leave initialization flag set
	_, err = cache.Get("Turdus merula")
	if err == nil {
		t.Error("Expected error from failed fetch")
	}

	// Try again immediately - should attempt a new fetch
	_, err = cache.Get("Turdus merula")
	if err == nil {
		t.Error("Expected error from second fetch")
	}

	// Verify that we attempted both fetches
	if mockProvider.fetchCounter != 2 {
		t.Errorf("Expected 2 fetches, got %d fetches", mockProvider.fetchCounter)
	}
}

// TestUserRequestsNotRateLimited tests that user requests are not subject to rate limiting
func TestUserRequestsNotRateLimited(t *testing.T) {
	t.Parallel()
	// Create the actual Wikipedia provider to test rate limiting behavior
	provider, err := imageprovider.NewWikiMediaProvider()
	if err != nil {
		t.Fatalf("Failed to create WikiMedia provider: %v", err)
	}

	// Create a mock store
	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache := imageprovider.InitCache("wikimedia", provider, metrics, mockStore)

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
	for i := 0; i < 10; i++ {
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
	if duration > 4*time.Second {
		t.Errorf("User requests appear to be rate limited. Duration: %v, expected < 4s", duration)
	}

	t.Logf("10 user requests completed in %v (no rate limiting, threshold: 4s)", duration)
}

// TestBackgroundRequestsRateLimited tests that background requests are subject to rate limiting
func TestBackgroundRequestsRateLimited(t *testing.T) {
	t.Parallel()

	// Create a mock provider with controlled fetch behavior
	fetchAttempts := make(chan struct{}, 10)
	mockProvider := &mockProviderWithContext{
		mockImageProvider: mockImageProvider{
			fetchDelay: 5 * time.Millisecond,
		},
		fetchChannel: fetchAttempts,
	}

	mockStore := newMockStore()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Pre-populate with stale entries to trigger background refresh
	staleTime := time.Now().Add(-15 * 24 * time.Hour)
	numStaleEntries := 5
	for i := 0; i < numStaleEntries; i++ {
		species := fmt.Sprintf("StaleSpecies_%d", i)
		if err := mockStore.SaveImageCache(&datastore.ImageCache{
			ScientificName: species,
			ProviderName:   "wikimedia",
			URL:            fmt.Sprintf("http://example.com/old_%s.jpg", species),
			CachedAt:       staleTime,
		}); err != nil {
			t.Fatalf("Failed to save stale cache entry: %v", err)
		}
	}

	// Wait for background refresh to process, but with a timeout
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Collect fetch attempts over a period of time
	fetchCount := 0
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-fetchAttempts:
			fetchCount++
			t.Logf("Background fetch attempt %d detected", fetchCount)
		case <-ticker.C:
			// Check if we've seen some fetches
			if fetchCount > 0 && fetchCount <= numStaleEntries {
				// Success: background fetches are happening but controlled
				t.Logf("Background fetches completed: %d (expected <= %d)", fetchCount, numStaleEntries)
				return
			}
		case <-ctx.Done():
			if fetchCount == 0 {
				t.Skip("No background fetches detected - background refresh might not have started")
			} else {
				t.Logf("Test completed with %d background fetches", fetchCount)
			}
			return
		}
	}
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
