package imageprovider_test

import (
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gorm.io/gorm"
)

// mockImageProvider is a mock implementation of the ImageProvider interface
type mockImageProvider struct {
	fetchCounter int
	shouldFail   bool
}

func (m *mockImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	m.fetchCounter++
	if m.shouldFail {
		return imageprovider.BirdImage{}, errors.New("mock fetch error")
	}
	return imageprovider.BirdImage{
		URL:         "http://example.com/" + scientificName + ".jpg",
		LicenseName: "CC BY-SA 4.0",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:  "Mock Author",
		AuthorURL:   "http://example.com/author",
	}, nil
}

// mockStore is a mock implementation of the datastore.Interface
type mockStore struct {
	images map[string]*datastore.ImageCache
}

func newMockStore() *mockStore {
	return &mockStore{
		images: make(map[string]*datastore.ImageCache),
	}
}

// Implement only the methods we need for testing
func (m *mockStore) GetImageCache(scientificName string) (*datastore.ImageCache, error) {
	if img, ok := m.images[scientificName]; ok {
		return img, nil
	}
	return nil, nil
}

func (m *mockStore) SaveImageCache(cache *datastore.ImageCache) error {
	m.images[cache.ScientificName] = cache
	return nil
}

func (m *mockStore) GetAllImageCaches() ([]datastore.ImageCache, error) {
	var result []datastore.ImageCache
	for _, v := range m.images {
		result = append(result, *v)
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
func (m *mockStore) GetClipsQualifyingForRemoval(minHours int, minClips int) ([]datastore.ClipForRemoval, error) {
	return nil, nil
}
func (m *mockStore) GetNoteReview(noteID string) (*datastore.NoteReview, error)     { return nil, nil }
func (m *mockStore) SaveNoteReview(review *datastore.NoteReview) error              { return nil }
func (m *mockStore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) { return nil, nil }
func (m *mockStore) SaveNoteComment(comment *datastore.NoteComment) error           { return nil }
func (m *mockStore) UpdateNoteComment(commentID string, entry string) error         { return nil }
func (m *mockStore) DeleteNoteComment(commentID string) error                       { return nil }
func (m *mockStore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error       { return nil }
func (m *mockStore) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	return datastore.DailyEvents{}, nil
}
func (m *mockStore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error  { return nil }
func (m *mockStore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) { return nil, nil }
func (m *mockStore) LatestHourlyWeather() (*datastore.HourlyWeather, error)          { return nil, nil }
func (m *mockStore) GetHourlyDetections(date, hour string, duration int) ([]datastore.Note, error) {
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

func (m *mockFailingStore) GetImageCache(scientificName string) (*datastore.ImageCache, error) {
	if m.failGetCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetImageCache(scientificName)
}

func (m *mockFailingStore) SaveImageCache(cache *datastore.ImageCache) error {
	if m.failSaveCache {
		return fmt.Errorf("simulated database error")
	}
	return m.mockStore.SaveImageCache(cache)
}

func (m *mockFailingStore) GetAllImageCaches() ([]datastore.ImageCache, error) {
	if m.failGetAllCache {
		return nil, fmt.Errorf("simulated database error")
	}
	return m.mockStore.GetAllImageCaches()
}

// TestBirdImageCache tests the BirdImageCache implementation
func TestBirdImageCache(t *testing.T) {
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := telemetry.NewMetrics()
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
			cached, err := mockStore.GetImageCache(tt.scientificName)
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
	mockProvider := &mockImageProvider{shouldFail: true}
	mockStore := newMockStore()
	metrics, err := telemetry.NewMetrics()
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
	metrics, err := telemetry.NewMetrics()
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
	mockProvider := &mockImageProvider{}
	metrics, err := telemetry.NewMetrics()
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
			mockProvider := &mockImageProvider{}
			failingStore := newMockFailingStore()
			failingStore.failGetCache = tt.failGetCache
			failingStore.failSaveCache = tt.failSaveCache
			failingStore.failGetAllCache = tt.failGetAllInit

			metrics, err := telemetry.NewMetrics()
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
	mockProvider := &mockImageProvider{}
	metrics, err := telemetry.NewMetrics()
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
	mockProvider := &mockImageProvider{}
	mockStore := newMockStore()
	metrics, err := telemetry.NewMetrics()
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
	}
	mockStore.SaveImageCache(oldEntry)

	cache, err := imageprovider.CreateDefaultCache(metrics, mockStore)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	// Wait for refresh to happen
	time.Sleep(2 * time.Second)

	// Check if the entry was refreshed
	refreshed, err := mockStore.GetImageCache("Turdus merula")
	if err != nil {
		t.Fatalf("Failed to get refreshed cache entry: %v", err)
	}

	if refreshed == nil {
		t.Fatal("Cache entry was not found")
	}

	// Verify that the entry was updated
	if refreshed.CachedAt.Equal(oldEntry.CachedAt) {
		t.Error("Cache entry was not refreshed")
	}

	if refreshed.URL == oldEntry.URL {
		t.Error("Cache entry URL was not updated")
	}

	// Clean up
	if err := cache.Close(); err != nil {
		t.Errorf("Failed to close cache: %v", err)
	}
}
