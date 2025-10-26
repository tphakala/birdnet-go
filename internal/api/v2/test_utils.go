// test_utils.go: Package api provides shared test utilities for API v2 tests.

package api

import (
	"fmt"
	"io"
	"log"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// safeSlice is a helper for mock methods returning slices.
// It safely handles nil arguments and performs type assertion.
func safeSlice[T any](args mock.Arguments, index int) []T {
	if arg := args.Get(index); arg != nil {
		// Check if the argument is already of the target slice type
		if slice, ok := arg.([]T); ok {
			return slice
		}
		// Fail fast – most likely the test registered a value of the wrong type.
		panic(fmt.Sprintf("safeSlice: expected []%T at index %d, got %T", *new(T), index, arg))
	}
	return nil // Return nil if the argument itself is nil
}

// safePointer is a helper for mock methods returning pointers.
// It safely handles nil arguments and performs type assertion.
func safePointer[T any](args mock.Arguments, index int) *T {
	if arg := args.Get(index); arg != nil {
		// Check if the argument is already of the target pointer type
		if ptr, ok := arg.(T); ok {
			return &ptr
		}
		// Fail fast – most likely the test registered a value of the wrong type.
		panic(fmt.Sprintf("safePointer: expected *%T at index %d, got %T", *new(T), index, arg))
	}
	return nil // Return nil if the argument itself is nil
}
// TestImageProvider implements the imageprovider.Provider interface for testing
// with a function field for easier test setup.
// Use this when you need a simple mock with customizable behavior via FetchFunc.
type TestImageProvider struct {
	FetchFunc func(scientificName string) (imageprovider.BirdImage, error)
}

// Fetch implements the ImageProvider Fetch method
func (m *TestImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	if m.FetchFunc != nil {
		return m.FetchFunc(scientificName)
	}
	return imageprovider.BirdImage{}, nil
}

// NewTestMetrics creates a new metrics instance for testing
func NewTestMetrics(t *testing.T) *observability.Metrics {
	t.Helper()
	metrics, err := observability.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create test metrics: %v", err)
	}
	return metrics
}

// setupAnalyticsTestEnvironment creates a test environment with Echo, mocks.MockInterface, and Controller
// for analytics tests
func setupAnalyticsTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()
	// Create a new Echo instance
	e := echo.New()

	// Create a test datastore
	mockDS := mocks.NewMockInterface(t)

	// Create a controller with the test datastore
	controller := &Controller{
		Group:  e.Group("/api/v2"),
		DS:     mockDS,
		logger: log.New(io.Discard, "", 0),
	}

	// Don't initialize routes as it causes nil pointer dereference in tests
	// controller.initRoutes()

	return e, mockDS, controller
}

// MockImageProvider is a mock implementation of imageprovider.ImageProvider interface
// that uses testify/mock for expectations and verification.
// Use this when you need to verify specific method calls and arguments.
type MockImageProvider struct {
	mock.Mock
}

// Fetch implements the ImageProvider interface
func (m *MockImageProvider) Fetch(scientificName string) (imageprovider.BirdImage, error) {
	args := m.Called(scientificName)
	return args.Get(0).(imageprovider.BirdImage), args.Error(1)
}

// Setup function to create a test environment
//
// This function creates a complete test environment for API tests with the following components:
// 1. Echo instance - A new Echo web framework instance for handling HTTP requests
// 2. mocks.MockInterface - A generated mock implementation of the datastore interface for test data
// 3. Settings - Default configuration settings for testing
// 4. Logger - A test logger that outputs to stdout
// 5. MockImageProvider - A mock image provider for bird images
// 6. BirdImageCache - An initialized image cache with the mock provider
// 7. SunCalc - A mock sun calculator for time-based calculations
// 8. Control channel - A channel for control messages between components
//
// The function returns the Echo instance, mocks.MockInterface, and Controller for use in tests.
// Note: Callers are responsible for closing any resources (like channels) when tests complete.
func setupTestEnvironment(t *testing.T) (*echo.Echo, *mocks.MockInterface, *Controller) {
	t.Helper()

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := mocks.NewMockInterface(t)

	// Create settings
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				Export: conf.ExportSettings{
					Path: t.TempDir(), // Set the required path
				},
			},
		},
	}

	// Create a test logger (use io.Discard to avoid noisy stdout in CI)
	logger := log.New(io.Discard, "API TEST: ", log.LstdFlags)

	// Create a mock ImageProvider for testing
	mockImageProvider := new(MockImageProvider)

	// Set default behavior to return an empty bird image for any species
	emptyBirdImage := imageprovider.BirdImage{
		URL:            "https://example.com/empty.jpg",
		ScientificName: "Test Species",
	}
	mockImageProvider.On("Fetch", mock.Anything).Return(emptyBirdImage, nil)

	// Create a properly initialized BirdImageCache with the mock provider
	birdImageCache := &imageprovider.BirdImageCache{
		// We can only set exported fields, so we'll use SetImageProvider method instead
	}
	birdImageCache.SetImageProvider(mockImageProvider)

	// Create sun calculator with test coordinates (Helsinki, Finland)
	sunCalc := suncalc.NewSunCalc(60.1699, 24.9384)

	// Create control channel with buffer to prevent blocking in tests
	// Size 10 is sufficient for concurrent test scenarios (e.g., TestConcurrentControlRequests uses 5)
	controlChan := make(chan string, 10)

	// Create mock metrics for testing
	mockMetrics, _ := observability.NewMetrics()

	// Create API controller without initializing routes to avoid starting background goroutines
	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, logger, nil, mockMetrics, false)
	if err != nil {
		t.Fatalf("Failed to create test API controller: %v", err)
	}

	// Register cleanup to stop background goroutines
	t.Cleanup(func() {
		// Shutdown the controller properly
		controller.Shutdown()
		// Close control channel to signal goroutines to exit
		close(controlChan)
	})

	return e, mockDS, controller
}
