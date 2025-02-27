// api_test.go: Package api provides tests for API v2 endpoints.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/suncalc"
	"gorm.io/gorm"
)

// MockDataStore implements the datastore.Interface for testing
// This is a complete implementation of the interface, which can make tests verbose.
// For specific test scenarios, consider using a partial mock instead, for example:
//
//	func TestSomeSpecificFeature(t *testing.T) {
//	    // Create a partial mock that only implements needed methods
//	    mockDS := &MockDataStore{}
//	    // Only set expectations for methods this test actually calls
//	    mockDS.On("GetLastDetections", 10).Return(mockNotes, nil)
//	    // No need to implement every method of the interface
//	}
//
// Alternatively, consider splitting the datastore.Interface into smaller
// interfaces based on functional areas (e.g., NoteReader, NoteWriter, ReviewManager)
// and then compose them as needed in your application and tests.
type MockDataStore struct {
	mock.Mock
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
	return args.Get(0).(datastore.Note), args.Error(1)
}

func (m *MockDataStore) GetAllNotes() ([]datastore.Note, error) {
	args := m.Called()
	return args.Get(0).([]datastore.Note), args.Error(1)
}

func (m *MockDataStore) GetTopBirdsData(selectedDate string, minConfidenceNormalized float64) ([]datastore.Note, error) {
	args := m.Called(selectedDate, minConfidenceNormalized)
	return args.Get(0).([]datastore.Note), args.Error(1)
}

func (m *MockDataStore) GetHourlyOccurrences(date, commonName string, minConfidenceNormalized float64) ([24]int, error) {
	args := m.Called(date, commonName, minConfidenceNormalized)
	return args.Get(0).([24]int), args.Error(1)
}

func (m *MockDataStore) SpeciesDetections(species, date, hour string, duration int, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(species, date, hour, duration, sortAscending, limit, offset)
	return args.Get(0).([]datastore.Note), args.Error(1)
}

func (m *MockDataStore) GetLastDetections(numDetections int) ([]datastore.Note, error) {
	args := m.Called(numDetections)
	return args.Get(0).([]datastore.Note), args.Error(1)
}

func (m *MockDataStore) GetAllDetectedSpecies() ([]datastore.Note, error) {
	args := m.Called()
	return args.Get(0).([]datastore.Note), args.Error(1)
}

func (m *MockDataStore) SearchNotes(query string, sortAscending bool, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(query, sortAscending, limit, offset)
	return args.Get(0).([]datastore.Note), args.Error(1)
}

// More mock methods for datastore.Interface
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
	return args.Get(0).([]datastore.NoteComment), args.Error(1)
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
	return args.Get(0).([]datastore.HourlyWeather), args.Error(1)
}

func (m *MockDataStore) LatestHourlyWeather() (*datastore.HourlyWeather, error) {
	args := m.Called()
	return args.Get(0).(*datastore.HourlyWeather), args.Error(1)
}

func (m *MockDataStore) GetHourlyDetections(date, hour string, duration, limit, offset int) ([]datastore.Note, error) {
	args := m.Called(date, hour, duration, limit, offset)
	return args.Get(0).([]datastore.Note), args.Error(1)
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

func (m *MockDataStore) GetImageCache(scientificName string) (*datastore.ImageCache, error) {
	args := m.Called(scientificName)
	return args.Get(0).(*datastore.ImageCache), args.Error(1)
}

func (m *MockDataStore) SaveImageCache(cache *datastore.ImageCache) error {
	args := m.Called(cache)
	return args.Error(0)
}

func (m *MockDataStore) GetAllImageCaches() ([]datastore.ImageCache, error) {
	args := m.Called()
	return args.Get(0).([]datastore.ImageCache), args.Error(1)
}

func (m *MockDataStore) GetLockedNotesClipPaths() ([]string, error) {
	args := m.Called()
	return args.Get(0).([]string), args.Error(1)
}

func (m *MockDataStore) CountHourlyDetections(date, hour string, duration int) (int64, error) {
	args := m.Called(date, hour, duration)
	return args.Get(0).(int64), args.Error(1)
}

func (m *MockDataStore) GetSpeciesSummaryData() ([]datastore.SpeciesSummaryData, error) {
	args := m.Called()
	return args.Get(0).([]datastore.SpeciesSummaryData), args.Error(1)
}

func (m *MockDataStore) GetHourlyAnalyticsData(date, species string) ([]datastore.HourlyAnalyticsData, error) {
	args := m.Called(date, species)
	return args.Get(0).([]datastore.HourlyAnalyticsData), args.Error(1)
}

func (m *MockDataStore) GetDailyAnalyticsData(startDate, endDate, species string) ([]datastore.DailyAnalyticsData, error) {
	args := m.Called(startDate, endDate, species)
	return args.Get(0).([]datastore.DailyAnalyticsData), args.Error(1)
}

func (m *MockDataStore) GetDetectionTrends(period string, limit int) ([]datastore.DailyAnalyticsData, error) {
	args := m.Called(period, limit)
	return args.Get(0).([]datastore.DailyAnalyticsData), args.Error(1)
}

// Setup function to create a test environment
func setupTestEnvironment(t *testing.T) (*echo.Echo, *MockDataStore, *Controller) {
	t.Helper()

	// Create Echo instance
	e := echo.New()

	// Create mock datastore
	mockDS := new(MockDataStore)

	// Create settings
	settings := &conf.Settings{
		WebServer: struct {
			Debug   bool
			Enabled bool
			Port    string
			Log     conf.LogConfig
		}{
			Debug: true,
		},
	}

	// Create a test logger
	logger := log.New(os.Stdout, "API TEST: ", log.LstdFlags)

	// Mock the image cache constructor
	birdImageCache := &imageprovider.BirdImageCache{}

	// Mock the sun calculator constructor
	sunCalc := &suncalc.SunCalc{}

	// Create control channel
	controlChan := make(chan string)

	// Create API controller
	controller := New(e, mockDS, settings, birdImageCache, sunCalc, controlChan, logger)

	return e, mockDS, controller
}

// TestHealthCheck tests the health check endpoint
func TestHealthCheck(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create a request to the health check endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v2/health", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/health")

	// Test
	if assert.NoError(t, controller.HealthCheck(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Equal(t, "healthy", response["status"])

		// Future extensions - these fields may be added later
		// If they exist, they should have the correct type
		if version, exists := response["version"]; exists {
			assert.IsType(t, "", version, "version should be a string")
		}

		if env, exists := response["environment"]; exists {
			assert.IsType(t, "", env, "environment should be a string")
		}

		if uptime, exists := response["uptime"]; exists {
			// Uptime could be represented as a number (seconds) or as a formatted string
			switch v := uptime.(type) {
			case float64:
				assert.GreaterOrEqual(t, v, float64(0), "uptime should be non-negative")
			case string:
				assert.NotEmpty(t, v, "uptime string should not be empty")
			default:
				assert.Fail(t, "uptime should be a number or string")
			}
		}

		// If additional system metrics are added
		if metrics, exists := response["metrics"]; exists {
			assert.IsType(t, map[string]interface{}{}, metrics, "metrics should be an object")
		}
	}
}

// TestGetRecentDetections tests the recent detections endpoint
func TestGetRecentDetections(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Create mock data
	now := time.Now()
	mockNotes := []datastore.Note{
		{
			ID:             1,
			Date:           "2023-01-01",
			Time:           "12:00:00",
			Latitude:       42.0,
			Longitude:      -72.0,
			CommonName:     "American Robin",
			Confidence:     0.85,
			ScientificName: "Turdus migratorius",
			BeginTime:      now.Add(-time.Hour),
			EndTime:        now,
		},
		{
			ID:             2,
			Date:           "2023-01-01",
			Time:           "12:10:00",
			Latitude:       42.1,
			Longitude:      -72.1,
			CommonName:     "Blue Jay",
			Confidence:     0.92,
			ScientificName: "Cyanocitta cristata",
			BeginTime:      now.Add(-2 * time.Hour),
			EndTime:        now.Add(-time.Hour),
		},
	}

	// Setup mock expectations
	mockDS.On("GetLastDetections", 10).Return(mockNotes, nil)

	// Create a request to the recent detections endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent?limit=10", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/recent")
	c.QueryParams().Set("limit", "10")

	// Test
	if assert.NoError(t, controller.GetRecentDetections(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response []map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Len(t, response, 2)
		assert.Equal(t, float64(1), response[0]["id"])
		assert.Equal(t, "American Robin", response[0]["common_name"])
		assert.Equal(t, float64(2), response[1]["id"])
		assert.Equal(t, "Blue Jay", response[1]["common_name"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetRecentDetectionsError tests error handling in the recent detections endpoint
func TestGetRecentDetectionsError(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup mock to return an error
	mockError := gorm.ErrRecordNotFound
	mockDS.On("GetLastDetections", 10).Return([]datastore.Note{}, mockError)

	// Create a request to the recent detections endpoint
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent?limit=10", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/recent")
	c.QueryParams().Set("limit", "10")

	// Test - we expect the controller to handle the error and return an HTTP error
	controller.GetRecentDetections(c)

	// We should get an error response
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var errorResponse map[string]interface{}
	jsonErr := json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	assert.NoError(t, jsonErr)

	// Check error response content
	assert.Contains(t, errorResponse, "error")
	assert.Contains(t, errorResponse, "message")
	assert.Contains(t, errorResponse, "code")
	assert.Equal(t, float64(http.StatusInternalServerError), errorResponse["code"])

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestDeleteDetection tests the delete detection endpoint
func TestDeleteDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup mock expectations
	mockDS.On("Delete", "1").Return(nil)

	// Create a request to the delete detection endpoint
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/1", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/:id")
	c.SetParamNames("id")
	c.SetParamValues("1")

	// We need to bypass auth middleware for this test
	// In a real test, you might want to test the auth middleware separately
	// and then use proper authentication tokens here
	handler := func(c echo.Context) error {
		return controller.DeleteDetection(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusNoContent, rec.Code)
		// No content should be returned with 204 status
		assert.Empty(t, rec.Body.String())
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestDeleteDetectionNotFound tests the delete detection endpoint when record is not found
func TestDeleteDetectionNotFound(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup mock expectations to return a record not found error
	mockDS.On("Delete", "999").Return(gorm.ErrRecordNotFound)

	// Create a request to the delete detection endpoint
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/999", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/:id")
	c.SetParamNames("id")
	c.SetParamValues("999")

	// Bypass auth middleware
	handler := func(c echo.Context) error {
		return controller.DeleteDetection(c)
	}

	// Test
	handler(c)

	// We should get an error or error response
	assert.NotEqual(t, http.StatusNoContent, rec.Code)

	// Parse error response if it's a JSON response
	if rec.Header().Get(echo.HeaderContentType) == echo.MIMEApplicationJSON {
		var errorResponse map[string]interface{}
		jsonErr := json.Unmarshal(rec.Body.Bytes(), &errorResponse)
		if jsonErr == nil {
			// Check error response content
			assert.Contains(t, errorResponse, "error")
		}
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestDeleteDetectionDatabaseError tests the delete detection endpoint when a database error occurs
func TestDeleteDetectionDatabaseError(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup mock expectations to return a database error
	dbErr := errors.New("database connection lost")
	mockDS.On("Delete", "1").Return(dbErr)

	// Create a request to the delete detection endpoint
	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/1", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/:id")
	c.SetParamNames("id")
	c.SetParamValues("1")

	// Bypass auth middleware
	handler := func(c echo.Context) error {
		return controller.DeleteDetection(c)
	}

	// Test
	handler(c)

	// We should get an error status
	assert.Equal(t, http.StatusInternalServerError, rec.Code)

	// Parse error response
	var errorResponse map[string]interface{}
	jsonErr := json.Unmarshal(rec.Body.Bytes(), &errorResponse)
	assert.NoError(t, jsonErr)

	// Check error response content
	assert.Contains(t, errorResponse, "error")
	assert.Contains(t, errorResponse, "code")
	assert.Equal(t, float64(http.StatusInternalServerError), errorResponse["code"])

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestReviewDetection tests the review detection endpoint
func TestReviewDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Create review request
	reviewRequest := map[string]interface{}{
		"correct": true,
		"comment": "This is a correct identification",
	}

	// Convert to JSON
	jsonData, err := json.Marshal(reviewRequest)
	assert.NoError(t, err)

	// Setup mock expectations for IsNoteLocked and SaveNoteReview
	mockDS.On("IsNoteLocked", "1").Return(false, nil)
	mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)

	// Create a request to the review detection endpoint
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review",
		bytes.NewReader(jsonData))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetPath("/api/v2/detections/:id/review")
	c.SetParamNames("id")
	c.SetParamValues("1")

	// We need to bypass auth middleware for this test
	handler := func(c echo.Context) error {
		return controller.ReviewDetection(c)
	}

	// Test
	if assert.NoError(t, handler(c)) {
		// Check response
		assert.Equal(t, http.StatusOK, rec.Code)

		// Parse response body
		var response map[string]interface{}
		err := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, err)

		// Check response content
		assert.Equal(t, "success", response["status"])
	}

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestReviewDetectionConcurrency tests concurrency handling in the review detection endpoint
// Note: This test simulates concurrency scenarios by mocking different responses,
// but does not test actual concurrent execution with multiple goroutines.
//
// For a true concurrency stress test, consider implementing something like:
//
//	func TestTrueConcurrentAccess(t *testing.T) {
//	    // Setup with real test server
//	    ts := httptest.NewServer(controller.e)
//	    defer ts.Close()
//
//	    // Create a wait group to synchronize goroutines
//	    var wg sync.WaitGroup
//	    numConcurrent := 10
//	    wg.Add(numConcurrent)
//
//	    // Create a barrier to ensure goroutines start roughly at the same time
//	    var barrier sync.WaitGroup
//	    barrier.Add(1)
//
//	    // Track results
//	    var successes, failures int32
//
//	    // Launch concurrent requests
//	    for i := 0; i < numConcurrent; i++ {
//	        go func() {
//	            defer wg.Done()
//
//	            // Wait for the barrier to be lifted
//	            barrier.Wait()
//
//	            // Make the request
//	            resp, err := http.Post(ts.URL+"/api/v2/detections/1/review",
//	                "application/json", strings.NewReader(`{"correct":true}`))
//
//	            if err == nil && resp.StatusCode == http.StatusOK {
//	                atomic.AddInt32(&successes, 1)
//	            } else {
//	                atomic.AddInt32(&failures, 1)
//	            }
//	        }()
//	    }
//
//	    // Lift the barrier to start all goroutines roughly simultaneously
//	    barrier.Done()
//
//	    // Wait for all goroutines to complete
//	    wg.Wait()
//
//	    // Check results - exactly one should succeed, others should get conflict
//	    assert.Equal(t, int32(1), successes)
//	    assert.Equal(t, int32(numConcurrent-1), failures)
//	}
func TestReviewDetectionConcurrency(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Create review request
	reviewRequest := map[string]interface{}{
		"correct": true,
		"comment": "This is a correct identification",
	}

	// Convert to JSON
	jsonData, err := json.Marshal(reviewRequest)
	assert.NoError(t, err)

	// Scenario 1: Note is already locked by another user
	t.Run("NoteLocked", func(t *testing.T) {
		// Reset mock
		mockDS = new(MockDataStore)
		controller.DS = mockDS

		// Mock note is already locked
		mockDS.On("IsNoteLocked", "1").Return(true, nil)

		// Note: We don't expect SaveNoteReview to be called when note is locked

		// Create a request
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review",
			bytes.NewReader(jsonData))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id/review")
		c.SetParamNames("id")
		c.SetParamValues("1")

		// Test
		controller.ReviewDetection(c)

		// Should return conflict or forbidden status
		assert.Equal(t, http.StatusConflict, rec.Code)

		// Parse response
		var response map[string]interface{}
		jsonErr := json.Unmarshal(rec.Body.Bytes(), &response)
		assert.NoError(t, jsonErr)

		// Verify response indicates locked resource
		assert.Contains(t, response["message"], "locked")

		// Verify expectations - SaveNoteReview should not have been called
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
	})

	// Scenario 2: Database error during lock check
	t.Run("LockCheckError", func(t *testing.T) {
		// Reset mock
		mockDS = new(MockDataStore)
		controller.DS = mockDS

		// Mock database error during lock check
		dbErr := errors.New("database error")
		mockDS.On("IsNoteLocked", "1").Return(false, dbErr)

		// Create request
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review",
			bytes.NewReader(jsonData))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id/review")
		c.SetParamNames("id")
		c.SetParamValues("1")

		// Test
		controller.ReviewDetection(c)

		// Should return error status
		assert.Equal(t, http.StatusInternalServerError, rec.Code)

		// Verify expectations - SaveNoteReview should not have been called
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
	})

	// Scenario 3: Race condition when locking note
	t.Run("RaceCondition", func(t *testing.T) {
		// Reset mock
		mockDS = new(MockDataStore)
		controller.DS = mockDS

		// Mock race condition: note is not locked in check but fails to acquire lock
		mockDS.On("IsNoteLocked", "1").Return(false, nil)
		mockDS.On("LockNote", "1").Return(errors.New("concurrent access"))

		// Create request
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review",
			bytes.NewReader(jsonData))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id/review")
		c.SetParamNames("id")
		c.SetParamValues("1")

		// Test
		controller.ReviewDetection(c)

		// Should return conflict status
		assert.Equal(t, http.StatusConflict, rec.Code)

		// Verify expectations - SaveNoteReview should not have been called
		mockDS.AssertNotCalled(t, "SaveNoteReview", mock.Anything)
	})
}

// Add more test functions for other endpoints as needed

// TestHandleError tests error handling functionality
func TestHandleError(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Create a request context
	req := httptest.NewRequest(http.MethodGet, "/api/v2/health", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	// Test error handling
	err := controller.HandleError(c, echo.NewHTTPError(http.StatusBadRequest, "Test error"),
		"Error message", http.StatusBadRequest)

	// Assertions
	assert.NoError(t, err)
	assert.Equal(t, http.StatusBadRequest, rec.Code)

	// Parse response body
	var response ErrorResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	assert.NoError(t, err)

	// Check response content
	assert.Equal(t, "code=400, message=Test error", response.Error)
	assert.Equal(t, "Error message", response.Message)
	assert.Equal(t, http.StatusBadRequest, response.Code)
}
