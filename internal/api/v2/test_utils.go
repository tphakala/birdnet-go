// test_utils.go: Package api provides shared test utilities for API v2 tests.

package api

import (
	"fmt"
	"io"
	"log"
	"os"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"github.com/tphakala/birdnet-go/internal/telemetry"
	"gorm.io/gorm"
)

// MockDataStore implements the datastore.Interface for testing
// This is a shared implementation that can be used across all test files
// It provides a full mock of all datastore methods with proper expectations
type MockDataStore struct {
	mock.Mock
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

// Implement required methods of the datastore.Interface
func (m *MockDataStore) Open() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDataStore) Close() error {
	args := m.Called()
	return args.Error(0)
}

func (m *MockDataStore) Save(note *datastore.Note, results []datastore.Results) error {
	args := m.Called(note, results)
	return args.Error(0)
}

func (m *MockDataStore) Delete(id string) error {
	args := m.Called(id)
	return args.Error(0)
}

func (m *MockDataStore) Get(id string) (datastore.Note, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return datastore.Note{}, args.Error(1)
	}
	return args.Get(0).(datastore.Note), args.Error(1)
}

func (m *MockDataStore) GetAllNotes() ([]datastore.Note, error) {
	args := m.Called()
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
	args := m.Called(selectedDate, minConfidenceNormalized)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
	args := m.Called(date, commonName, minConfidenceNormalized)
	return args.Get(0).([24]int), args.Error(1)
}

func (m *MockDataStore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(species, date, hour, duration, sortAscending, limit, offset)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) GetLastDetections(numDetections int) ([]datastore.Note, error) {
	args := m.Called(numDetections)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	args := m.Called()
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(query, sortAscending, limit, offset)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) GetNoteClipPath(noteID string) (string, error) {
	args := m.Called(noteID)
	return args.String(0), args.Error(1)
}

func (m *MockDataStore) DeleteNoteClipPath(noteID string) error {
	args := m.Called(noteID)
	return args.Error(0)
}

func (m *MockDataStore) GetNoteReview(noteID string) (*datastore.NoteReview, error) {
	args := m.Called(noteID)
	return args.Get(0).(*datastore.NoteReview), args.Error(1)
}

func (m *MockDataStore) SaveNoteReview(review *datastore.NoteReview) error {
	args := m.Called(review)
	return args.Error(0)
}

func (m *MockDataStore) GetNoteComments(noteID string) ([]datastore.NoteComment, error) {
	args := m.Called(noteID)
	return safeSlice[datastore.NoteComment](args, 0), args.Error(1)
}

func (m *MockDataStore) SaveNoteComment(comment *datastore.NoteComment) error {
	args := m.Called(comment)
	return args.Error(0)
}

func (m *MockDataStore) UpdateNoteComment(commentID, entry string) error {
	args := m.Called(commentID, entry)
	return args.Error(0)
}

func (m *MockDataStore) DeleteNoteComment(commentID string) error {
	args := m.Called(commentID)
	return args.Error(0)
}

func (m *MockDataStore) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error {
	args := m.Called(dailyEvents)
	return args.Error(0)
}

func (m *MockDataStore) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	args := m.Called(date)
	return args.Get(0).(datastore.DailyEvents), args.Error(1)
}

func (m *MockDataStore) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error {
	args := m.Called(hourlyWeather)
	return args.Error(0)
}

func (m *MockDataStore) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) {
	args := m.Called(date)
	return safeSlice[datastore.HourlyWeather](args, 0), args.Error(1)
}

func (m *MockDataStore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	args := m.Called()
	return args.Get(0).(*datastore.HourlyWeather), args.Error(1)
}

func (m *MockDataStore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(date, hour, duration, limit, offset)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStore) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	args := m.Called(species, date, hour, duration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockDataStore) CountSearchResults(query string) (int64, error) {
	args := m.Called(query)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockDataStore) Transaction(fc func(tx *gorm.DB) error) error {
	args := m.Called(fc)
	return args.Error(0)
}

func (m *MockDataStore) LockNote(noteID string) error {
	args := m.Called(noteID)
	return args.Error(0)
}

func (m *MockDataStore) UnlockNote(noteID string) error {
	args := m.Called(noteID)
	return args.Error(0)
}

func (m *MockDataStore) GetNoteLock(noteID string) (*datastore.NoteLock, error) {
	args := m.Called(noteID)
	return args.Get(0).(*datastore.NoteLock), args.Error(1)
}

func (m *MockDataStore) IsNoteLocked(noteID string) (bool, error) {
	args := m.Called(noteID)
	return args.Bool(0), args.Error(1)
}

func (m *MockDataStore) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	args := m.Called(query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datastore.ImageCache), args.Error(1)
}

func (m *MockDataStore) SaveImageCache(cache *datastore.ImageCache) error {
	args := m.Called(cache)
	return args.Error(0)
}

func (m *MockDataStore) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	args := m.Called(providerName)
	return safeSlice[datastore.ImageCache](args, 0), args.Error(1)
}

func (m *MockDataStore) GetLockedNotesClipPaths() ([]string, error) {
	args := m.Called()
	return safeSlice[string](args, 0), args.Error(1)
}

func (m *MockDataStore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	args := m.Called(date, hour, duration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockDataStore) GetSpeciesSummaryData(startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	args := m.Called(startDate, endDate)
	return safeSlice[datastore.SpeciesSummaryData](args, 0), args.Error(1)
}

func (m *MockDataStore) GetHourlyAnalyticsData(date, species string) ([]datastore.HourlyAnalyticsData, error) {
	args := m.Called(date, species)
	return safeSlice[datastore.HourlyAnalyticsData](args, 0), args.Error(1)
}

func (m *MockDataStore) GetDailyAnalyticsData(startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	args := m.Called(startDate, endDate, species)
	return safeSlice[datastore.DailyAnalyticsData](args, 0), args.Error(1)
}

func (m *MockDataStore) GetDetectionTrends(period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	args := m.Called(period, limit)
	return safeSlice[datastore.DailyAnalyticsData](args, 0), args.Error(1)
}

// GetHourlyDistribution implements the datastore.Interface GetHourlyDistribution method
func (m *MockDataStore) GetHourlyDistribution(startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) {
	args := m.Called(startDate, endDate, species)
	return safeSlice[datastore.HourlyDistributionData](args, 0), args.Error(1)
}

func (m *MockDataStore) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	args := m.Called(filters)
	return safeSlice[datastore.DetectionRecord](args, 0), args.Int(1), args.Error(2)
}

// GetNewSpeciesDetections implements the datastore.Interface GetNewSpeciesDetections method
func (m *MockDataStore) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	args := m.Called(startDate, endDate, limit, offset)
	return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
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
func NewTestMetrics(t *testing.T) *telemetry.Metrics {
	metrics, err := telemetry.NewMetrics()
	if err != nil {
		t.Fatalf("Failed to create test metrics: %v", err)
	}
	return metrics
}

// setupAnalyticsTestEnvironment creates a test environment with Echo, MockDataStore, and Controller
// for analytics tests
func setupAnalyticsTestEnvironment(t *testing.T) (*echo.Echo, *MockDataStore, *Controller) {
	// Create a new Echo instance
	e := echo.New()

	// Create a test datastore
	mockDS := new(MockDataStore)
	mockDS.On("Open").Return(nil)

	// Call Open to satisfy the mock expectation
	_ = mockDS.Open()

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

// MockDataStoreV2 provides a mock implementation for datastore.Interface
// Focused on methods used specifically in newer V2 analytics endpoints
// Embeds testify/mock for standard expectation handling
type MockDataStoreV2 struct {
	mock.Mock // Embed testify mock
	// Removed function fields like GetTopBirdsDataFunc, etc.
}

// Implement required methods of the datastore.Interface using testify/mock

func (m *MockDataStoreV2) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
	args := m.Called(selectedDate, minConfidenceNormalized)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}

func (m *MockDataStoreV2) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
	args := m.Called(date, commonName, minConfidenceNormalized)
	return args.Get(0).([24]int), args.Error(1)
}

// GetHourlyDistribution implements the datastore.Interface GetHourlyDistribution method
func (m *MockDataStoreV2) GetHourlyDistribution(startDate, endDate, species string) ([]datastore.HourlyDistributionData, error) {
	args := m.Called(startDate, endDate, species)
	return safeSlice[datastore.HourlyDistributionData](args, 0), args.Error(1)
}

// ---- Methods below are stubs required by the interface but likely unused in V2 analytics tests ----
// ---- If needed, implement them fully using m.Called() similar to above methods ----

// Satisfy the remaining methods of the datastore.Interface (with empty implementations)
// These need to be implemented to satisfy the interface, even if not used directly in tests.
// If a test needs a specific behavior for one of these, define an expectation using m.On(...)

func (m *MockDataStoreV2) Open() error { args := m.Called(); return args.Error(0) }
func (m *MockDataStoreV2) Save(note *datastore.Note, results []datastore.Results) error {
	args := m.Called(note, results)
	return args.Error(0)
}
func (m *MockDataStoreV2) Delete(id string) error { args := m.Called(id); return args.Error(0) }
func (m *MockDataStoreV2) Get(id string) (datastore.Note, error) {
	args := m.Called(id)
	return args.Get(0).(datastore.Note), args.Error(1)
}
func (m *MockDataStoreV2) Close() error { args := m.Called(); return args.Error(0) }
func (m *MockDataStoreV2) GetAllNotes() ([]datastore.Note, error) {
	args := m.Called()
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(species, date, hour, duration, sortAscending, limit, offset)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) GetLastDetections(numDetections int) ([]datastore.Note, error) {
	args := m.Called(numDetections)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) GetAllDetectedSpecies() ([]datastore.Note, error) {
	args := m.Called()
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) SearchNotes(query string, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(query, sortAscending, limit, offset)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) GetNoteClipPath(noteID string) (string, error) {
	args := m.Called(noteID)
	return args.String(0), args.Error(1)
}
func (m *MockDataStoreV2) DeleteNoteClipPath(noteID string) error {
	args := m.Called(noteID)
	return args.Error(0)
}
func (m *MockDataStoreV2) GetNoteReview(noteID string) (*datastore.NoteReview, error) {
	args := m.Called(noteID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datastore.NoteReview), args.Error(1)
}
func (m *MockDataStoreV2) SaveNoteReview(review *datastore.NoteReview) error {
	args := m.Called(review)
	return args.Error(0)
}
func (m *MockDataStoreV2) GetNoteComments(noteID string) ([]datastore.NoteComment, error) {
	args := m.Called(noteID)
	return safeSlice[datastore.NoteComment](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) SaveNoteComment(comment *datastore.NoteComment) error {
	args := m.Called(comment)
	return args.Error(0)
}
func (m *MockDataStoreV2) UpdateNoteComment(commentID, entry string) error {
	args := m.Called(commentID, entry)
	return args.Error(0)
}
func (m *MockDataStoreV2) DeleteNoteComment(commentID string) error {
	args := m.Called(commentID)
	return args.Error(0)
}
func (m *MockDataStoreV2) SaveDailyEvents(dailyEvents *datastore.DailyEvents) error {
	args := m.Called(dailyEvents)
	return args.Error(0)
}
func (m *MockDataStoreV2) GetDailyEvents(date string) (datastore.DailyEvents, error) {
	args := m.Called(date)
	return args.Get(0).(datastore.DailyEvents), args.Error(1)
}
func (m *MockDataStoreV2) SaveHourlyWeather(hourlyWeather *datastore.HourlyWeather) error {
	args := m.Called(hourlyWeather)
	return args.Error(0)
}
func (m *MockDataStoreV2) GetHourlyWeather(date string) ([]datastore.HourlyWeather, error) {
	args := m.Called(date)
	return safeSlice[datastore.HourlyWeather](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	args := m.Called()
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datastore.HourlyWeather), args.Error(1)
}
func (m *MockDataStoreV2) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(date, hour, duration, limit, offset)
	return safeSlice[datastore.Note](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) CountSpeciesDetections(species, date, hour string, duration int) (int64, error) {
	args := m.Called(species, date, hour, duration)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockDataStoreV2) CountSearchResults(query string) (int64, error) {
	args := m.Called(query)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockDataStoreV2) Transaction(fc func(tx *gorm.DB) error) error {
	args := m.Called(fc)
	return args.Error(0)
}
func (m *MockDataStoreV2) LockNote(noteID string) error {
	args := m.Called(noteID)
	return args.Error(0)
}
func (m *MockDataStoreV2) UnlockNote(noteID string) error {
	args := m.Called(noteID)
	return args.Error(0)
}
func (m *MockDataStoreV2) GetNoteLock(noteID string) (*datastore.NoteLock, error) {
	args := m.Called(noteID)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datastore.NoteLock), args.Error(1)
}
func (m *MockDataStoreV2) IsNoteLocked(noteID string) (bool, error) {
	args := m.Called(noteID)
	return args.Bool(0), args.Error(1)
}
func (m *MockDataStoreV2) GetImageCache(query datastore.ImageCacheQuery) (*datastore.ImageCache, error) {
	args := m.Called(query)
	if args.Get(0) == nil {
		return nil, args.Error(1)
	}
	return args.Get(0).(*datastore.ImageCache), args.Error(1)
}
func (m *MockDataStoreV2) SaveImageCache(cache *datastore.ImageCache) error {
	args := m.Called(cache)
	return args.Error(0)
}
func (m *MockDataStoreV2) GetAllImageCaches(providerName string) ([]datastore.ImageCache, error) {
	args := m.Called(providerName)
	return safeSlice[datastore.ImageCache](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) GetLockedNotesClipPaths() ([]string, error) {
	args := m.Called()
	return safeSlice[string](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	args := m.Called(date, hour, duration)
	return args.Get(0).(int64), args.Error(1)
}
func (m *MockDataStoreV2) GetSpeciesSummaryData(startDate, endDate string) ([]datastore.SpeciesSummaryData, error) {
	args := m.Called(startDate, endDate)
	return safeSlice[datastore.SpeciesSummaryData](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) GetHourlyAnalyticsData(date, species string) ([]datastore.HourlyAnalyticsData, error) {
	args := m.Called(date, species)
	return safeSlice[datastore.HourlyAnalyticsData](args, 0), args.Error(1)
}
func (m *MockDataStoreV2) GetDailyAnalyticsData(startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	args := m.Called(startDate, endDate, species)
	return safeSlice[datastore.DailyAnalyticsData](args, 0), args.Error(1)
}

// GetNewSpeciesDetections implements the datastore.Interface GetNewSpeciesDetections method
func (m *MockDataStoreV2) GetNewSpeciesDetections(startDate, endDate string, limit, offset int) ([]datastore.NewSpeciesData, error) {
	args := m.Called(startDate, endDate, limit, offset)
	return safeSlice[datastore.NewSpeciesData](args, 0), args.Error(1)
}

// GetDetectionTrends implements the datastore.Interface GetDetectionTrends method
func (m *MockDataStoreV2) GetDetectionTrends(period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	args := m.Called(period, limit)
	return safeSlice[datastore.DailyAnalyticsData](args, 0), args.Error(1)
}

// SearchDetections implements the datastore.Interface SearchDetections method
func (m *MockDataStoreV2) SearchDetections(filters *datastore.SearchFilters) ([]datastore.DetectionRecord, int, error) {
	args := m.Called(filters)
	return safeSlice[datastore.DetectionRecord](args, 0), args.Int(1), args.Error(2)
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
// 2. MockDataStore - A mock implementation of the datastore interface for test data
// 3. Settings - Default configuration settings for testing
// 4. Logger - A test logger that outputs to stdout
// 5. MockImageProvider - A mock image provider for bird images
// 6. BirdImageCache - An initialized image cache with the mock provider
// 7. SunCalc - A mock sun calculator for time-based calculations
// 8. Control channel - A channel for control messages between components
//
// The function returns the Echo instance, MockDataStore, and Controller for use in tests.
// Note: Callers are responsible for closing any resources (like channels) when tests complete.
func setupTestEnvironment(t *testing.T) (*echo.Echo, *MockDataStore, *Controller) {
	t.Helper()

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := new(MockDataStore)

	// Create settings
	settings := &conf.Settings{
		WebServer: conf.WebServerSettings{
			Debug: true,
		},
	}

	// Create a test logger
	logger := log.New(os.Stdout, "API TEST: ", log.LstdFlags)

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

	// Mock the sun calculator constructor
	sunCalc := &suncalc.SunCalc{}

	// Create control channel
	controlChan := make(chan string)

	// Create API controller
	controller, err := New(e, mockDS, settings, birdImageCache, sunCalc, controlChan, logger)
	if err != nil {
		t.Fatalf("Failed to create test API controller: %v", err)
	}

	return e, mockDS, controller
}

func (m *MockDataStore) GetNote(id int) (datastore.Note, error) {
	args := m.Called(id)
	if args.Get(0) == nil {
		return datastore.Note{}, args.Error(1)
	}
	return args.Get(0).(datastore.Note), args.Error(1)
}
