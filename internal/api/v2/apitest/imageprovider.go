package apitest

import (
	"testing"

	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// MockImageProvider is a testify/mock implementation of the imageprovider
// provider interface. Use it when a test needs to verify specific Fetch calls
// and arguments.
type MockImageProvider struct {
	mock.Mock
}

// Fetch implements the image provider interface.
func (m *MockImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	args := m.Called(scientificName)
	return args.Get(0).(imageprovider.BirdImage), args.Error(1)
}

// TestImageProvider implements the image provider interface with a function field
// for simple, customizable behavior without testify/mock expectations. Use it
// when a test only needs to control Fetch's return value.
type TestImageProvider struct {
	FetchFunc func(scientificName string) (imageprovider.BirdImage, error)
}

// Fetch implements the image provider interface.
func (m *TestImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	if m.FetchFunc != nil {
		return m.FetchFunc(scientificName)
	}
	return imageprovider.BirdImage{}, nil
}

// NewMockBirdImageCache returns an imageprovider.BirdImageCache backed by a
// MockImageProvider whose Fetch returns an empty placeholder image for any
// species. It is the shared image-cache builder used by both the facade test
// setup and apitest.NewCore so the mock-cache construction lives in one place.
func NewMockBirdImageCache(t *testing.T) *imageprovider.BirdImageCache {
	t.Helper()
	provider := new(MockImageProvider)
	provider.On("Fetch", mock.Anything).Return(imageprovider.BirdImage{
		URL:            "https://example.com/empty.jpg",
		ScientificName: "Test Species",
	}, nil)
	// Only exported fields are settable; wire the provider via SetImageProvider.
	cache := &imageprovider.BirdImageCache{}
	cache.SetImageProvider(provider)
	return cache
}
