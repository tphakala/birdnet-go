package imageprovider

import (
	"testing"
)

type mockProvider struct {
	fetchCounter int
}

func (l *mockProvider) fetch(scientificName string) (BirdImage, error) {
	l.fetchCounter++
	return BirdImage{}, nil
}

// TestBirdImageCache ensures the bird image cache behaves as expected.
// It tests whether the cache correctly handles duplicate requests.
func TestBirdImageCache(t *testing.T) {
	// Create a mock image provider and initialize the cache.
	mockBirdProvider := &mockProvider{}
	mockNonBirdProvider := &mockProvider{}
	cache := &BirdImageCache{
		birdImageProvider:    mockBirdProvider,
		nonBirdImageProvider: mockNonBirdProvider,
	}

	entriesToTest := []string{
		"a",
		"b",
		"a",               // Duplicate request
		"Human non-vocal", // Non-bird request
	}

	for _, entry := range entriesToTest {
		_, err := cache.Get(entry)
		if err != nil {
			t.Errorf("Unexpected error for entry %s: %v", entry, err)
		}
	}

	// Verify that the bird provider's fetch method was called exactly twice.
	expectedBirdFetchCalls := 2
	if mockBirdProvider.fetchCounter != expectedBirdFetchCalls {
		t.Errorf("Expected %d calls to bird provider, got %d",
			expectedBirdFetchCalls, mockBirdProvider.fetchCounter)
	}

	// Verify that the non-bird provider's fetch method was called exactly once.
	expectedNonBirdFetchCalls := 1
	if mockNonBirdProvider.fetchCounter != expectedNonBirdFetchCalls {
		t.Errorf("Expected %d calls to non-bird provider, got %d",
			expectedNonBirdFetchCalls, mockNonBirdProvider.fetchCounter)
	}
}
