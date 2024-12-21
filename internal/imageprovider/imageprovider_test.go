package imageprovider_test

import (
	"errors"
	"testing"

	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/telemetry"
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

// TestBirdImageCache tests the BirdImageCache implementation
func TestBirdImageCache(t *testing.T) {
	mockProvider := &mockImageProvider{}
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	cache, err := imageprovider.CreateDefaultCache(metrics)
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
		{"Cached bird species", "Turdus merula", 1, false},
		{"Another species", "Parus major", 2, false},
		{"Animal entry", "Canis lupus", 3, false}, // Dog species
		{"Cached animal entry", "Canis lupus", 3, false},
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
		})
	}
}

// TestBirdImageCacheError tests the BirdImageCache error handling
func TestBirdImageCacheError(t *testing.T) {
	mockProvider := &mockImageProvider{shouldFail: true}
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	cache, err := imageprovider.CreateDefaultCache(metrics)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}
	cache.SetImageProvider(mockProvider)

	_, err = cache.Get("Turdus merula")
	if err == nil {
		t.Error("BirdImageCache.Get() error = nil, want error")
	}
}

// TestBirdImageCacheSetImageProvider tests the BirdImageCache.SetImageProvider method
func TestCreateDefaultCache(t *testing.T) {
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	cache, err := imageprovider.CreateDefaultCache(metrics)
	if err != nil {
		t.Fatalf("CreateDefaultCache() error = %v", err)
	}
	if cache == nil {
		t.Error("CreateDefaultCache() returned nil cache")
	}
}

// TestBirdImageCacheSetImageProvider tests the BirdImageCache.SetImageProvider method
func TestBirdImageEstimateSize(t *testing.T) {
	img := imageprovider.BirdImage{
		URL:         "http://example.com/bird.jpg",
		LicenseName: "CC BY-SA 4.0",
		LicenseURL:  "https://creativecommons.org/licenses/by-sa/4.0/",
		AuthorName:  "Test Author",
		AuthorURL:   "http://example.com/author",
	}

	size := img.EstimateSize()
	if size <= 0 {
		t.Errorf("BirdImage.EstimateSize() = %d, want > 0", size)
	}
}

// TestBirdImageCacheMemoryUsage tests the BirdImageCache.MemoryUsage method
func TestBirdImageCacheMemoryUsage(t *testing.T) {
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create metrics: %v", err)
	}
	cache, err := imageprovider.CreateDefaultCache(metrics)
	if err != nil {
		t.Fatalf("Failed to create default cache: %v", err)
	}

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
