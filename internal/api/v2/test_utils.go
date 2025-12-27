// test_utils.go: Package api provides shared test utilities for API v2 tests.

package api

import (
	"fmt"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/observability"
	"github.com/tphakala/birdnet-go/internal/suncalc"
)

// Test environment constants
const (
	testHelsinkiLatitude  = 60.1699 // Helsinki, Finland latitude for SunCalc tests
	testHelsinkiLongitude = 24.9384 // Helsinki, Finland longitude for SunCalc tests
	testControlChannelBuf = 10      // Control channel buffer size for concurrent test scenarios
)

// newMinimalController creates a Controller for simple validation tests.
// Use this for tests that only need to call handler methods without database or full infrastructure.
func newMinimalController() *Controller {
	return &Controller{}
}

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
		Group: e.Group("/api/v2"),
		DS:    mockDS,
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
	sunCalc := suncalc.NewSunCalc(testHelsinkiLatitude, testHelsinkiLongitude)

	// Create control channel with buffer to prevent blocking in tests
	// Size 10 is sufficient for concurrent test scenarios (e.g., TestConcurrentControlRequests uses 5)
	controlChan := make(chan string, testControlChannelBuf)

	// Create mock metrics for testing
	mockMetrics, _ := observability.NewMetrics()

	// Create API controller without initializing routes to avoid starting background goroutines
	controller, err := NewWithOptions(e, mockDS, settings, birdImageCache, sunCalc, controlChan, mockMetrics, false)
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

// assertRoutesRegistered verifies that all expected routes are registered in the Echo instance.
// It creates a map from expected route strings (e.g., "GET /api/v2/control/actions"),
// checks each registered route against this map, and asserts that all expected routes were found.
func assertRoutesRegistered(t *testing.T, e *echo.Echo, expectedRoutes []string) {
	t.Helper()

	// Create a map to track which routes were found
	routeFound := make(map[string]bool, len(expectedRoutes))
	for _, route := range expectedRoutes {
		routeFound[route] = false
	}

	// Check each registered route
	for _, r := range e.Routes() {
		routePath := r.Method + " " + r.Path
		if _, exists := routeFound[routePath]; exists {
			routeFound[routePath] = true
		}
	}

	// Verify all expected routes were found
	for route, found := range routeFound {
		assert.True(t, found, "Route not registered: %s", route)
	}
}

// SpeciesDailySummaryExpected contains expected values for species daily summary assertions.
type SpeciesDailySummaryExpected struct {
	CommonName          string
	SpeciesCode         string
	Count               int
	HourlyCounts        []int
	FirstHeard          string
	LatestHeard         string
	HighConfidence      bool
	ThumbnailURLContain string // Substring that ThumbnailURL should contain
}

// assertSpeciesDailySummary verifies that a SpeciesDailySummary matches expected values.
// This reduces duplication in tests that verify species summary fields.
func assertSpeciesDailySummary(t *testing.T, species *SpeciesDailySummary, expected *SpeciesDailySummaryExpected) {
	t.Helper()

	assert.Equal(t, expected.CommonName, species.CommonName, "%s common name mismatch", expected.CommonName)
	assert.Equal(t, expected.SpeciesCode, species.SpeciesCode, "%s species code mismatch", expected.CommonName)
	assert.Equal(t, expected.Count, species.Count, "%s count mismatch", expected.CommonName)
	assert.Equal(t, expected.HourlyCounts, species.HourlyCounts, "%s hourly counts mismatch", expected.CommonName)
	assert.Equal(t, expected.FirstHeard, species.FirstHeard, "%s first heard time mismatch", expected.CommonName)
	assert.Equal(t, expected.LatestHeard, species.LatestHeard, "%s latest heard time mismatch", expected.CommonName)
	assert.Equal(t, expected.HighConfidence, species.HighConfidence, "%s high confidence mismatch", expected.CommonName)
	assert.Contains(t, species.ThumbnailURL, expected.ThumbnailURLContain, "%s thumbnail URL mismatch", expected.CommonName)
}

// setupValidReviewMock configures mock expectations for a valid review operation.
// This is used for detection review tests where the note is not locked and saves succeed.
func setupValidReviewMock(m *mock.Mock, id string, noteID uint, withComment bool) {
	m.On("Get", id).Return(datastore.Note{ID: noteID, Locked: false}, nil)
	m.On("IsNoteLocked", id).Return(false, nil)
	if withComment {
		m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
	}
	m.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
}
