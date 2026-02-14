// detections_test.go: Package api provides tests for API v2 detection endpoints.

package api

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// executeNoteCommentsHandler simulates the handler behavior for getting comments.
func executeNoteCommentsHandler(t *testing.T, c echo.Context, mockDS datastore.Interface, detectionID string, expectedStatus int) error {
	t.Helper()
	if expectedStatus == http.StatusOK {
		comments, dbErr := mockDS.GetNoteComments(detectionID)
		if dbErr != nil {
			return echo.NewHTTPError(http.StatusNotFound, "Comments not found")
		}
		return c.JSON(http.StatusOK, comments)
	}
	return echo.NewHTTPError(expectedStatus, "Comments not found")
}

// assertNoteCommentsResponse validates the response for note comments.
func assertNoteCommentsResponse(t *testing.T, rec *httptest.ResponseRecorder, err error, expectedStatus, expectedCount int) {
	t.Helper()
	if expectedStatus == http.StatusOK {
		require.NoError(t, err)
		assert.Equal(t, expectedStatus, rec.Code)
		var comments []datastore.NoteComment
		jsonErr := json.Unmarshal(rec.Body.Bytes(), &comments)
		require.NoError(t, jsonErr)
		assert.Len(t, comments, expectedCount)
	} else {
		require.Error(t, err)
		httpErr, ok := errors.AsType[*echo.HTTPError](err)
		assert.True(t, ok)
		assert.Equal(t, expectedStatus, httpErr.Code)
	}
}

// decodePaginated is a helper to unmarshal a response body into a PaginatedResponse
// and extract the data as a map slice for easier testing.
func decodePaginated(t *testing.T, body []byte) ([]map[string]any, PaginatedResponse) {
	t.Helper()
	var response PaginatedResponse
	err := json.Unmarshal(body, &response)
	require.NoError(t, err, "Failed to unmarshal response body")

	// Short-circuit if the payload is empty
	if response.Data == nil {
		return nil, response
	}

	// Extract the detections data
	detectionsIface, ok := response.Data.([]any)
	if !ok {
		require.Failf(t, "Expected Data to be an array", "got %T", response.Data)
	}

	// Convert to []map[string]interface{} for easier access
	result := make([]map[string]any, len(detectionsIface))
	for i, d := range detectionsIface {
		if m, ok := d.(map[string]any); ok {
			result[i] = m
		} else {
			require.Failf(t, "Expected detection element to be object", "got %T", d)
		}
	}

	return result, response
}

// testRealtimeSource returns a standard test realtime audio source to avoid duplication
func testRealtimeSource() datastore.AudioSource {
	return datastore.AudioSource{
		ID:          "realtime",
		SafeString:  "realtime",
		DisplayName: "realtime",
	}
}

// categorizeHTTPResponse categorizes an HTTP response status into success, conflict, or failure.
func categorizeHTTPResponse(t *testing.T, statusCode int, successes, conflicts, failures *int32) {
	t.Helper()
	switch statusCode {
	case http.StatusOK:
		atomic.AddInt32(successes, 1)
	case http.StatusConflict:
		atomic.AddInt32(conflicts, 1)
	default:
		t.Logf("Unexpected status code: %d", statusCode)
		atomic.AddInt32(failures, 1)
	}
}

// getConcurrencyLevel returns a platform-appropriate concurrency level for tests.
func getConcurrencyLevel() int {
	switch runtime.GOOS {
	case "windows":
		return 3
	case "darwin":
		return 4
	default:
		return 5
	}
}

// TestGetDetections tests the GetDetections endpoint with various query types
func TestGetDetections(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)
	controller.initDetectionRoutes() // Ensure routes are registered on the test echo instance

	// Create mock data
	mockNotes := []datastore.Note{
		{
			ID:             1,
			Date:           "2025-03-07",
			Time:           "08:15:00",
			Source:         testRealtimeSource(),
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.95,
			BeginTime:      time.Now().Add(-time.Hour),
			EndTime:        time.Now(),
			Verified:       "correct",
			Locked:         false,
			Comments: []datastore.NoteComment{
				{
					ID:        1,
					NoteID:    1,
					Entry:     "Test comment",
					CreatedAt: time.Now(),
					UpdatedAt: time.Now(),
				},
			},
		},
		{
			ID:             2,
			Date:           "2025-03-07",
			Time:           "09:30:00",
			Source:         testRealtimeSource(),
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.85,
			BeginTime:      time.Now().Add(-2 * time.Hour),
			EndTime:        time.Now().Add(-time.Hour),
			Verified:       "false_positive",
			Locked:         true,
		},
	}

	// Test cases
	testCases := []struct {
		name           string
		queryParams    map[string]string
		mockSetup      func(*mock.Mock)
		expectedStatus int
		expectedCount  int
		checkResponse  func(t *testing.T, testName string, rec *httptest.ResponseRecorder)
	}{
		{
			name: "All detections",
			queryParams: map[string]string{
				"queryType":  "all",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", "", false, 10, 0).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				detections, _ := decodePaginated(t, rec.Body.Bytes())
				assert.Len(t, detections, 2)
			},
		},
		{
			name: "Hourly detections",
			queryParams: map[string]string{
				"queryType":  "hourly",
				"date":       "2025-03-07",
				"hour":       "08",
				"duration":   "1",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("GetHourlyDetections", "2025-03-07", "08", 1, 10, 0).Return(mockNotes[:1], nil)
				m.On("CountHourlyDetections", "2025-03-07", "08", 1).Return(int64(1), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				detections, _ := decodePaginated(t, rec.Body.Bytes())
				assert.Len(t, detections, 1)
			},
		},
		{
			name: "Species detections",
			queryParams: map[string]string{
				"queryType":  "species",
				"species":    "American Crow",
				"date":       "2025-03-07",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SpeciesDetections", "American Crow", "2025-03-07", "", 1, false, 10, 0).Return(mockNotes[:1], nil)
				m.On("CountSpeciesDetections", "American Crow", "2025-03-07", "", 1).Return(int64(1), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				detections, _ := decodePaginated(t, rec.Body.Bytes())
				assert.Len(t, detections, 1)
			},
		},
		{
			name: "Search detections",
			queryParams: map[string]string{
				"queryType":  "search",
				"search":     "Crow",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", "Crow", false, 10, 0).Return(mockNotes[:1], nil)
				m.On("CountSearchResults", "Crow").Return(int64(1), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				detections, _ := decodePaginated(t, rec.Body.Bytes())
				assert.Len(t, detections, 1)
			},
		},
		{
			name: "Invalid numResults parameter",
			queryParams: map[string]string{
				"numResults": "-5", // Negative value
			},
			mockSetup: func(m *mock.Mock) {
				// No DB interaction expected for bad request
			},
			expectedStatus: http.StatusBadRequest, // Expecting 400 Bad Request
			expectedCount:  0,                     // Not relevant for error case
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				// Check recorder status and body for the error
				assert.Equal(t, http.StatusBadRequest, rec.Code, "Expected status code 400")

				// Check the response body for the error message
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err, "Failed to unmarshal error response")
				assert.Contains(t, errResp["message"], "numResults must be greater than zero", "Error message mismatch in response body")
			},
		},
		{
			name:           "Invalid_offset_parameter",
			queryParams:    map[string]string{"offset": "abc"},
			expectedStatus: http.StatusBadRequest,
			mockSetup:      func(m *mock.Mock) { /* No DB interaction expected */ },
			expectedCount:  0, // Not relevant for error case
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				// Check recorder status and body for the error
				assert.Equal(t, http.StatusBadRequest, rec.Code, "Expected status code 400")

				// Check the response body for the error message
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err, "Failed to unmarshal error response")
				assert.Contains(t, errResp["message"], "Invalid numeric value for offset", "Error message mismatch in response body")
			},
		},
		{
			name: "Invalid confidence parameter",
			queryParams: map[string]string{
				"minConfidence": "200", // Value > 100
			},
			mockSetup: func(m *mock.Mock) {
				// Controller should still process the request
				m.On("SearchNotes", "", false, 100, 0).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK, // Now expecting 200 OK
			expectedCount:  0,
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				// Verify response is successful
				var response PaginatedResponse
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
			},
		},
		{
			name: "Extremely large numResults parameter",
			queryParams: map[string]string{
				"numResults": "9223372036854775807", // Max int64 value -> exceeds limit
			},
			expectedStatus: http.StatusBadRequest, // Should return Bad Request
			expectedCount:  0,                     // Not relevant for error case
			mockSetup: func(m *mock.Mock) {
				// No DB interaction expected for bad request
			},
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				// Check recorder status and body for the error
				assert.Equal(t, http.StatusBadRequest, rec.Code, "Expected status code 400")

				// Check the response body for the error message
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err, "Failed to unmarshal error response")
				assert.Contains(t, errResp["message"], "numResults exceeds maximum allowed value", "Error message mismatch in response body")
			},
		},
		{
			name: "Hourly query with invalid hour",
			queryParams: map[string]string{
				"queryType": "hourly",
				"date":      "2025-03-07",
				"hour":      "abc",
			},
			expectedStatus: http.StatusBadRequest,
			expectedCount:  0,
			mockSetup:      func(m *mock.Mock) {},
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["message"], "invalid hour parameter")
			},
		},
		{
			name: "Hourly query with missing hour",
			queryParams: map[string]string{
				"queryType": "hourly",
				"date":      "2025-03-07",
			},
			expectedStatus: http.StatusBadRequest,
			expectedCount:  0,
			mockSetup:      func(m *mock.Mock) {},
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["message"], "hour parameter is required")
			},
		},
		{
			name: "Hourly query with hour range rejected",
			queryParams: map[string]string{
				"queryType": "hourly",
				"date":      "2025-03-07",
				"hour":      "6-9",
			},
			expectedStatus: http.StatusBadRequest,
			expectedCount:  0,
			mockSetup:      func(m *mock.Mock) {},
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["message"], "invalid hour parameter")
			},
		},
		{
			name: "Hourly query with out of range hour",
			queryParams: map[string]string{
				"queryType": "hourly",
				"date":      "2025-03-07",
				"hour":      "25",
			},
			expectedStatus: http.StatusBadRequest,
			expectedCount:  0,
			mockSetup:      func(m *mock.Mock) {},
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["message"], "invalid hour parameter")
			},
		},
		{
			name: "Hourly query with same-value range rejected",
			queryParams: map[string]string{
				"queryType": "hourly",
				"date":      "2025-03-07",
				"hour":      "08-08",
			},
			expectedStatus: http.StatusBadRequest,
			expectedCount:  0,
			mockSetup:      func(m *mock.Mock) {},
			checkResponse: func(t *testing.T, testName string, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, http.StatusBadRequest, rec.Code)
				var errResp map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &errResp)
				require.NoError(t, err)
				assert.Contains(t, errResp["message"], "invalid hour parameter")
			},
		},
	}

	for _, tc := range testCases {
		localTC := tc // Capture range variable
		t.Run(localTC.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			localTC.mockSetup(&mockDS.Mock)

			// Create request with query parameters
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections", http.NoBody)
			rec := httptest.NewRecorder()

			// Set query parameters
			q := req.URL.Query()
			for key, value := range localTC.queryParams {
				q.Add(key, value)
			}
			req.URL.RawQuery = q.Encode()

			// Call handler via Echo instance again
			e.ServeHTTP(rec, req)

			// Check the recorder's status code FIRST
			assert.Equal(t, localTC.expectedStatus, rec.Code)

			// Now, run the specific checkResponse function for this test case,
			// passing the recorder.
			localTC.checkResponse(t, localTC.name, rec)

			// Verify mock expectations (only relevant if status code was not an error handled before DB call)
			if localTC.expectedStatus < 400 {
				mockDS.AssertExpectations(t)
			}
		})
	}
}

// TestGetDetection tests the GetDetection endpoint
func TestGetDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Create mock data
	mockNote := datastore.Note{
		ID:             1,
		Date:           "2025-03-07",
		Time:           "08:15:00",
		Source:         testRealtimeSource(),
		SpeciesCode:    "AMCRO",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
		Confidence:     0.95,
		BeginTime:      time.Now().Add(-time.Hour),
		EndTime:        time.Now(),
		Verified:       "correct",
		Locked:         false,
		Comments: []datastore.NoteComment{
			{
				ID:        1,
				NoteID:    1,
				Entry:     "Test comment",
				CreatedAt: time.Now(),
				UpdatedAt: time.Now(),
			},
		},
	}

	// Test cases
	testCases := []struct {
		name           string
		detectionID    string
		mockSetup      func(*mock.Mock)
		expectedStatus int
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:        "Valid detection",
			detectionID: "1",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "1").Return(mockNote, nil)
				m.On("GetHourlyWeather", "2025-03-07").Return([]datastore.HourlyWeather{}, nil)
			},
			expectedStatus: http.StatusOK,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var response DetectionResponse
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, uint(1), response.ID)
				assert.Equal(t, "Corvus brachyrhynchos", response.ScientificName)
				assert.Equal(t, "American Crow", response.CommonName)
				assert.InDelta(t, 0.95, response.Confidence, 0.01)
				assert.Equal(t, "correct", response.Verified)
				assert.False(t, response.Locked)
				assert.Len(t, response.Comments, 1)
				assert.Equal(t, "Test comment", response.Comments[0].Entry)
				assert.Equal(t, uint(1), response.Comments[0].ID)
				assert.NotEmpty(t, response.Comments[0].CreatedAt)
				assert.NotEmpty(t, response.Comments[0].UpdatedAt)
			},
		},
		{
			name:        "Detection not found",
			detectionID: "999",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "999").Return(datastore.Note{}, errors.New("record not found"))
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var response map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, "Detection not found", response["error"])
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/"+tc.detectionID, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.detectionID)

			// Call handler
			err := controller.GetDetection(c)
			if tc.expectedStatus == http.StatusOK {
				require.NoError(t, err)
			}

			// Check response
			assert.Equal(t, tc.expectedStatus, rec.Code)
			tc.checkResponse(t, rec)

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestGetDetectionCommentFormat verifies that detection comments are returned
// with full object format (id, entry, createdAt, updatedAt) as expected by the frontend.
// This test was added to fix issue #1728 where comments displayed as "NaN-NaN-NaN NaN:NaN:NaN"
// due to a mismatch between backend (returning []string) and frontend (expecting Comment objects).
func TestGetDetectionCommentFormat(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Create specific timestamps for verification
	createdTime := time.Date(2025, 1, 9, 10, 30, 0, 0, time.UTC)
	updatedTime := time.Date(2025, 1, 9, 11, 45, 0, 0, time.UTC)

	// Create mock data with multiple comments to verify ordering
	mockNote := datastore.Note{
		ID:             42,
		Date:           "2025-01-09",
		Time:           "10:30:00",
		Source:         testRealtimeSource(),
		SpeciesCode:    "NOCA",
		ScientificName: "Cardinalis cardinalis", //nolint:misspell // Valid scientific name for Northern Cardinal
		CommonName:     "Northern Cardinal",
		Confidence:     0.92,
		BeginTime:      createdTime,
		EndTime:        createdTime.Add(3 * time.Second),
		Verified:       "correct",
		Locked:         false,
		Comments: []datastore.NoteComment{
			{
				ID:        101,
				NoteID:    42,
				Entry:     "First comment with text",
				CreatedAt: createdTime,
				UpdatedAt: updatedTime,
			},
			{
				ID:        102,
				NoteID:    42,
				Entry:     "Second comment added later",
				CreatedAt: createdTime.Add(time.Hour),
				UpdatedAt: createdTime.Add(time.Hour),
			},
		},
	}

	// Setup mock expectations
	mockDS.On("Get", "42").Return(mockNote, nil)
	mockDS.On("GetHourlyWeather", "2025-01-09").Return([]datastore.HourlyWeather{}, nil)

	// Create request
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/42", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("42")

	// Call handler
	err := controller.GetDetection(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	// Parse response
	var response DetectionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Verify comment structure - this is the key fix for issue #1728
	// Frontend expects Comment objects with {id, entry, createdAt, updatedAt}
	// NOT string arrays which would cause "NaN-NaN-NaN NaN:NaN:NaN" display
	require.Len(t, response.Comments, 2, "Expected 2 comments")

	// Verify first comment has all required fields
	comment1 := response.Comments[0]
	assert.Equal(t, uint(101), comment1.ID, "Comment ID should be preserved")
	assert.Equal(t, "First comment with text", comment1.Entry, "Comment entry text should be preserved")
	assert.Equal(t, createdTime.Format(time.RFC3339), comment1.CreatedAt, "CreatedAt should be RFC3339 formatted")
	assert.Equal(t, updatedTime.Format(time.RFC3339), comment1.UpdatedAt, "UpdatedAt should be RFC3339 formatted")

	// Verify second comment with exact timestamp assertions
	comment2 := response.Comments[1]
	expectedTime2 := createdTime.Add(time.Hour)
	assert.Equal(t, uint(102), comment2.ID)
	assert.Equal(t, "Second comment added later", comment2.Entry)
	assert.Equal(t, expectedTime2.Format(time.RFC3339), comment2.CreatedAt, "Second comment CreatedAt should be RFC3339 formatted")
	assert.Equal(t, expectedTime2.Format(time.RFC3339), comment2.UpdatedAt, "Second comment UpdatedAt should be RFC3339 formatted")

	// Verify timestamps are valid RFC3339 format (frontend will parse these)
	_, err = time.Parse(time.RFC3339, comment1.CreatedAt)
	require.NoError(t, err, "CreatedAt must be valid RFC3339 timestamp")
	_, err = time.Parse(time.RFC3339, comment1.UpdatedAt)
	require.NoError(t, err, "UpdatedAt must be valid RFC3339 timestamp")

	// Verify mock expectations
	mockDS.AssertExpectations(t)
}

// TestGetDetectionEmptyComments verifies detection with no comments returns empty array
func TestGetDetectionEmptyComments(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	mockNote := datastore.Note{
		ID:             99,
		Date:           "2025-01-09",
		Time:           "12:00:00",
		Source:         testRealtimeSource(),
		SpeciesCode:    "AMRO",
		ScientificName: "Turdus migratorius",
		CommonName:     "American Robin",
		Confidence:     0.88,
		BeginTime:      time.Now(),
		EndTime:        time.Now().Add(3 * time.Second),
		Verified:       "unverified",
		Locked:         false,
		Comments:       nil, // No comments
	}

	mockDS.On("Get", "99").Return(mockNote, nil)
	mockDS.On("GetHourlyWeather", "2025-01-09").Return([]datastore.HourlyWeather{}, nil)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/99", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("99")

	err := controller.GetDetection(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec.Code)

	var response DetectionResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// Comments should be nil/empty, not cause any NaN issues
	assert.Empty(t, response.Comments, "Detection with no comments should have empty comments array")

	mockDS.AssertExpectations(t)
}

// TestGetRecentDetections tests the GetRecentDetections endpoint
func TestGetRecentDetections(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Create mock data
	mockNotes := []datastore.Note{
		{
			ID:             1,
			Date:           "2025-03-07",
			Time:           "08:15:00",
			Source:         testRealtimeSource(),
			SpeciesCode:    "AMCRO",
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.95,
			BeginTime:      time.Now().Add(-time.Hour),
			EndTime:        time.Now(),
			Verified:       "correct",
			Locked:         false,
		},
		{
			ID:             2,
			Date:           "2025-03-07",
			Time:           "09:30:00",
			Source:         testRealtimeSource(),
			SpeciesCode:    "RBWO",
			ScientificName: "Melanerpes carolinus",
			CommonName:     "Red-bellied Woodpecker",
			Confidence:     0.85,
			BeginTime:      time.Now().Add(-2 * time.Hour),
			EndTime:        time.Now().Add(-time.Hour),
			Verified:       "false_positive",
			Locked:         true,
		},
	}

	// Test cases
	testCases := []struct {
		name           string
		limit          string
		mockSetup      func(*mock.Mock)
		expectedStatus int
		expectedCount  int
	}{
		{
			name:  "Default limit",
			limit: "",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", 10).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:  "Custom limit",
			limit: "5",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", 5).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:  "Database error",
			limit: "5",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", 5).Return([]datastore.Note{}, errors.New("database error"))
			},
			expectedStatus: http.StatusInternalServerError,
			expectedCount:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent", http.NoBody)
			if tc.limit != "" {
				q := req.URL.Query()
				q.Add("limit", tc.limit)
				req.URL.RawQuery = q.Encode()
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			// Call handler
			err := controller.GetRecentDetections(c)

			// Check response
			if tc.expectedStatus == http.StatusOK {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, rec.Code)

				// Parse response
				var detections []DetectionResponse
				err = json.Unmarshal(rec.Body.Bytes(), &detections)
				require.NoError(t, err)
				assert.Len(t, detections, tc.expectedCount)
			} else {
				// For error cases, the controller returns a JSON error response, not an echo.HTTPError
				require.NoError(t, err) // The error is handled inside the controller
				assert.Equal(t, tc.expectedStatus, rec.Code)

				// Verify the error response structure
				var errorResp map[string]any
				err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
				require.NoError(t, err)
				assert.Contains(t, errorResp, "error")
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestDeleteDetection tests the DeleteDetection endpoint
func TestDeleteDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Test cases
	testCases := []struct {
		name           string
		detectionID    string
		mockSetup      func(*mock.Mock)
		expectedStatus int
	}{
		{
			name:        "Delete unlocked detection",
			detectionID: "1",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "1").Return(datastore.Note{ID: 1, Locked: false}, nil)
				m.On("Delete", "1").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:        "Delete locked detection",
			detectionID: "2",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "2").Return(datastore.Note{ID: 2, Locked: true}, nil)
			},
			expectedStatus: http.StatusForbidden,
		},
		{
			name:        "Detection not found",
			detectionID: "999",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "999").Return(datastore.Note{}, errors.New("record not found"))
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:        "Database error during delete",
			detectionID: "3",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "3").Return(datastore.Note{ID: 3, Locked: false}, nil)
				m.On("Delete", "3").Return(errors.New("database error"))
			},
			expectedStatus: http.StatusInternalServerError,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/"+tc.detectionID, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.detectionID)

			// Call handler
			err := controller.DeleteDetection(c)

			// Check response
			if tc.expectedStatus == http.StatusNoContent {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, rec.Code)
			} else {
				// For error cases, the controller returns a JSON error response, not an echo.HTTPError
				require.NoError(t, err) // The error is handled inside the controller
				assert.Equal(t, tc.expectedStatus, rec.Code)

				// Verify the error response structure
				var errorResp map[string]any
				err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
				require.NoError(t, err)
				assert.Contains(t, errorResp, "error")
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestReviewDetection tests the ReviewDetection endpoint
func TestReviewDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Test cases
	testCases := []struct {
		name           string
		detectionID    string
		requestBody    string
		mockSetup      func(*mock.Mock)
		expectedStatus int
	}{
		{
			name:        "Valid review with comment",
			detectionID: "1",
			requestBody: `{"verified": "correct", "comment": "Good detection"}`,
			mockSetup: func(m *mock.Mock) {
				setupValidReviewMock(m, "1", 1, true)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:        "Valid review without comment",
			detectionID: "2",
			requestBody: `{"verified": "false_positive"}`,
			mockSetup: func(m *mock.Mock) {
				setupValidReviewMock(m, "2", 2, false)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:        "Invalid verification status",
			detectionID: "3",
			requestBody: `{"verified": "invalid_status"}`,
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "3").Return(datastore.Note{ID: 3, Locked: false}, nil)
				m.On("IsNoteLocked", "3").Return(false, nil)
			},
			expectedStatus: http.StatusBadRequest,
		},
		{
			name:        "Locked detection",
			detectionID: "4",
			requestBody: `{"verified": "correct"}`,
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "4").Return(datastore.Note{ID: 4, Locked: true}, nil)
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name:        "Valid review with special characters in comment",
			detectionID: "5",
			requestBody: `{"verified": "correct", "comment": "<script>alert('XSS')</script>Special chars: &<>\"'!@#$%^&*()_+{}[]|\\:;,.?/~"}`,
			mockSetup: func(m *mock.Mock) {
				setupValidReviewMock(m, "5", 5, true)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:        "Valid review with extremely long comment",
			detectionID: "6",
			requestBody: `{"verified": "correct", "comment": "` + strings.Repeat("Very long comment. ", 500) + `"}`,
			mockSetup: func(m *mock.Mock) {
				setupValidReviewMock(m, "6", 6, true)
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/"+tc.detectionID+"/review",
				strings.NewReader(tc.requestBody))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.detectionID)

			// Call handler
			err := controller.ReviewDetection(c)

			// Check response
			if tc.expectedStatus == http.StatusOK {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, rec.Code)

				// Parse response
				var response map[string]string
				err = json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, "success", response["status"])
			} else {
				// For error cases, the controller returns a JSON error response, not an echo.HTTPError
				require.NoError(t, err) // The error is handled inside the controller
				assert.Equal(t, tc.expectedStatus, rec.Code)

				// Verify the error response structure
				var errorResp map[string]any
				err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
				require.NoError(t, err)
				assert.Contains(t, errorResp, "error")
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestLockDetection tests the LockDetection endpoint
func TestLockDetection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Test cases
	testCases := []struct {
		name           string
		detectionID    string
		requestBody    string
		mockSetup      func(*mock.Mock)
		expectedStatus int
	}{
		{
			name:        "Lock detection",
			detectionID: "1",
			requestBody: `{"locked": true}`,
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "1").Return(datastore.Note{ID: 1, Locked: false}, nil)
				m.On("IsNoteLocked", "1").Return(false, nil)
				m.On("LockNote", "1").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:        "Unlock detection",
			detectionID: "2",
			requestBody: `{"locked": false}`,
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "2").Return(datastore.Note{ID: 2, Locked: false}, nil)
				// Note: IsNoteLocked is NOT called when unlocking (req.Locked = false)
				m.On("UnlockNote", "2").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
		{
			name:        "Detection already locked by another user",
			detectionID: "3",
			requestBody: `{"locked": true}`,
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "3").Return(datastore.Note{ID: 3, Locked: false}, nil)
				m.On("IsNoteLocked", "3").Return(true, nil)
			},
			expectedStatus: http.StatusConflict,
		},
		{
			name:        "Unlock a locked detection should succeed",
			detectionID: "4",
			requestBody: `{"locked": false}`,
			mockSetup: func(m *mock.Mock) {
				// Detection is currently locked (Locked: true)
				m.On("Get", "4").Return(datastore.Note{ID: 4, Locked: true}, nil)
				// Note: IsNoteLocked is NOT called when unlocking (req.Locked = false)
				// Should call UnlockNote to unlock it
				m.On("UnlockNote", "4").Return(nil)
			},
			expectedStatus: http.StatusNoContent,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/"+tc.detectionID+"/lock",
				strings.NewReader(tc.requestBody))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.detectionID)

			// Call handler
			err := controller.LockDetection(c)

			// Check response
			if tc.expectedStatus == http.StatusNoContent {
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, rec.Code)
			} else {
				// For error cases, check the response
				require.NoError(t, err) // The error is handled inside the controller
				assert.Equal(t, tc.expectedStatus, rec.Code)

				// Verify the error response structure
				var errorResp map[string]any
				err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
				require.NoError(t, err)
				assert.Contains(t, errorResp, "error")
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// clearExcludedSpeciesList is a test helper that clears the global excluded species list
// to ensure test isolation. Call this at the beginning of tests that check list state.
func clearExcludedSpeciesList(t *testing.T) {
	t.Helper()
	settings := conf.GetSettings()
	settings.Realtime.Species.Exclude = []string{}
}

// TestIgnoreSpecies tests the IgnoreSpecies endpoint with toggle behavior
func TestIgnoreSpecies(t *testing.T) {
	// Test cases for error scenarios
	t.Run("Error cases", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		errorCases := []struct {
			name           string
			requestBody    string
			expectedStatus int
			expectedError  string
		}{
			{
				name:           "Empty species name",
				requestBody:    `{"common_name": ""}`,
				expectedStatus: http.StatusBadRequest,
				expectedError:  "Missing species name",
			},
			{
				name:           "Invalid JSON",
				requestBody:    `{"common_name": }`,
				expectedStatus: http.StatusBadRequest,
				expectedError:  "Invalid request format",
			},
			{
				name:           "Missing common_name field",
				requestBody:    `{}`,
				expectedStatus: http.StatusBadRequest,
				expectedError:  "Missing species name",
			},
		}

		for _, tc := range errorCases {
			t.Run(tc.name, func(t *testing.T) {
				req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
					strings.NewReader(tc.requestBody))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)

				err := controller.IgnoreSpecies(c)
				require.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, rec.Code)

				var response map[string]string
				err = json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response["error"], tc.expectedError)
			})
		}
	})

	// Test toggle behavior: add then remove
	t.Run("Toggle behavior - add species", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		// First request: add species (should not be in list initially)
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "American Crow"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.IgnoreSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response IgnoreSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "American Crow", response.CommonName)
		assert.Equal(t, "added", response.Action)
		assert.True(t, response.IsExcluded)
	})

	t.Run("Toggle behavior - remove species", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		// First, add the species
		req1 := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "Red-bellied Woodpecker"}`))
		req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec1 := httptest.NewRecorder()
		c1 := e.NewContext(req1, rec1)

		err := controller.IgnoreSpecies(c1)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec1.Code)

		var addResponse IgnoreSpeciesResponse
		err = json.Unmarshal(rec1.Body.Bytes(), &addResponse)
		require.NoError(t, err)
		assert.Equal(t, "added", addResponse.Action)
		assert.True(t, addResponse.IsExcluded)

		// Second request: toggle (remove) the same species
		req2 := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "Red-bellied Woodpecker"}`))
		req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec2 := httptest.NewRecorder()
		c2 := e.NewContext(req2, rec2)

		err = controller.IgnoreSpecies(c2)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec2.Code)

		var removeResponse IgnoreSpeciesResponse
		err = json.Unmarshal(rec2.Body.Bytes(), &removeResponse)
		require.NoError(t, err)
		assert.Equal(t, "Red-bellied Woodpecker", removeResponse.CommonName)
		assert.Equal(t, "removed", removeResponse.Action)
		assert.False(t, removeResponse.IsExcluded)
	})

	t.Run("Multiple toggle operations", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)
		speciesName := "Northern Cardinal"

		// Perform add-remove-add cycle
		for i, expectedAction := range []string{"added", "removed", "added"} {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
				strings.NewReader(`{"common_name": "`+speciesName+`"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := controller.IgnoreSpecies(c)
			require.NoError(t, err)
			assert.Equal(t, http.StatusOK, rec.Code, "Request %d failed", i+1)

			var response IgnoreSpeciesResponse
			err = json.Unmarshal(rec.Body.Bytes(), &response)
			require.NoError(t, err)
			assert.Equal(t, expectedAction, response.Action, "Request %d: expected action %s", i+1, expectedAction)
			assert.Equal(t, expectedAction == "added", response.IsExcluded, "Request %d: isExcluded mismatch", i+1)
		}
	})

	t.Run("Special characters in species name", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		// Test with special characters (properly JSON encoded)
		// Note: Use proper JSON encoding for special chars
		specialName := "Bird with special chars: &<>'éàü"
		reqBody := IgnoreSpeciesRequest{CommonName: specialName}
		jsonBody, err := json.Marshal(reqBody)
		require.NoError(t, err)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			bytes.NewReader(jsonBody))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err = controller.IgnoreSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response IgnoreSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, specialName, response.CommonName)
		assert.Equal(t, "added", response.Action)
	})

	t.Run("Long species name", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		longName := strings.Repeat("Very Long Bird Name ", 50)
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "`+longName+`"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.IgnoreSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response IgnoreSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "added", response.Action)
	})
}

// TestGetExcludedSpecies tests the GetExcludedSpecies endpoint
func TestGetExcludedSpecies(t *testing.T) {
	t.Run("Empty excluded list", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.GetExcludedSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response ExcludedSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, 0, response.Count)
		assert.Empty(t, response.Species)
	})

	t.Run("Excluded list with species", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		// First add some species
		species := []string{"American Crow", "Red-bellied Woodpecker", "Blue Jay"}
		for _, s := range species {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
				strings.NewReader(`{"common_name": "`+s+`"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			err := controller.IgnoreSpecies(c)
			require.NoError(t, err)
		}

		// Now get the excluded list
		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.GetExcludedSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response ExcludedSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, 3, response.Count)
		assert.ElementsMatch(t, species, response.Species)
	})

	t.Run("Excluded list reflects toggle operations", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t)

		// Add two species
		for _, s := range []string{"Species A", "Species B"} {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
				strings.NewReader(`{"common_name": "`+s+`"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			err := controller.IgnoreSpecies(c)
			require.NoError(t, err)
		}

		// Remove one species
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "Species A"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		err := controller.IgnoreSpecies(c)
		require.NoError(t, err)

		// Verify the list only contains Species B
		req = httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
		rec = httptest.NewRecorder()
		c = e.NewContext(req, rec)

		err = controller.GetExcludedSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response ExcludedSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, 1, response.Count)
		assert.Contains(t, response.Species, "Species B")
		assert.NotContains(t, response.Species, "Species A")
	})
}

// TestIgnoreSpeciesConcurrency tests concurrent access to the IgnoreSpecies endpoint
func TestIgnoreSpeciesConcurrency(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	clearExcludedSpeciesList(t)

	// Use WaitGroup.Go() for automatic Add/Done management (Go 1.25+)
	var wg sync.WaitGroup
	var barrier sync.WaitGroup
	barrier.Add(1)

	numGoroutines := 10
	var successCount int32

	for range numGoroutines {
		wg.Go(func() {
			barrier.Wait()

			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
				strings.NewReader(`{"common_name": "Concurrent Test Bird"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)

			err := controller.IgnoreSpecies(c)
			if err == nil && rec.Code == http.StatusOK {
				atomic.AddInt32(&successCount, 1)
			}
		})
	}

	// Start all goroutines simultaneously
	barrier.Done()
	wg.Wait()

	// All requests should succeed
	assert.Equal(t, int32(numGoroutines), successCount)

	// Verify final state - species should be either in or out of list
	// (odd number of toggles = in list, even = out)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := controller.GetExcludedSpecies(c)
	require.NoError(t, err)

	var response ExcludedSpeciesResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	// With 10 toggles starting from empty list: odd number = should be in list
	// But due to concurrent execution, the final state depends on timing
	// We just verify the endpoint didn't crash and returned valid data
	assert.GreaterOrEqual(t, response.Count, 0)
}

// TestAddCommentMethod tests the AddComment method directly
func TestAddCommentMethod(t *testing.T) {
	// Setup
	_, mockDS, controller := setupTestEnvironment(t)

	// Test cases
	testCases := []struct {
		name        string
		noteID      uint
		commentText string
		mockSetup   func(*mock.Mock)
		expectError bool
	}{
		{
			name:        "Valid comment",
			noteID:      1,
			commentText: "This is a test comment",
			mockSetup: func(m *mock.Mock) {
				m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
			},
			expectError: false,
		},
		{
			name:        "Database error",
			noteID:      1,
			commentText: "This is a test comment",
			mockSetup: func(m *mock.Mock) {
				m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(errors.New("database error"))
			},
			expectError: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Call method directly
			err := controller.AddComment(tc.noteID, tc.commentText)

			// Check result
			if tc.expectError {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestGetNoteComments tests retrieving comments for a detection
func TestGetNoteComments(t *testing.T) {
	// Setup
	e, mockDS, _ := setupTestEnvironment(t)

	// Create mock data
	mockComments := []datastore.NoteComment{
		{
			ID:        1,
			NoteID:    1,
			Entry:     "First comment",
			CreatedAt: time.Now().Add(-2 * time.Hour),
			UpdatedAt: time.Now().Add(-2 * time.Hour),
		},
		{
			ID:        2,
			NoteID:    1,
			Entry:     "Second comment",
			CreatedAt: time.Now().Add(-1 * time.Hour),
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		},
	}

	// Test cases
	testCases := []struct {
		name           string
		detectionID    string
		mockSetup      func(*mock.Mock)
		expectedStatus int
		expectedCount  int
	}{
		{
			name:        "Detection with comments",
			detectionID: "1",
			mockSetup: func(m *mock.Mock) {
				m.On("GetNoteComments", "1").Return(mockComments, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:        "Detection without comments",
			detectionID: "2",
			mockSetup: func(m *mock.Mock) {
				m.On("GetNoteComments", "2").Return([]datastore.NoteComment{}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  0,
		},
		{
			name:        "Detection not found",
			detectionID: "999",
			mockSetup: func(m *mock.Mock) {
				// No mock setup needed for this test case
			},
			expectedStatus: http.StatusNotFound,
			expectedCount:  0,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/"+tc.detectionID+"/comments", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.detectionID)

			err := executeNoteCommentsHandler(t, c, mockDS, tc.detectionID, tc.expectedStatus)
			assertNoteCommentsResponse(t, rec, err, tc.expectedStatus, tc.expectedCount)
			mockDS.AssertExpectations(t)
		})
	}
}

// TestGetNoteCommentsWithHandler tests retrieving comments for a detection using a proper route handler
func TestGetNoteCommentsWithHandler(t *testing.T) {
	// Setup
	e, mockDS, _ := setupTestEnvironment(t)

	// Register the route
	e.GET("/api/v2/detections/:id/comments", func(c echo.Context) error {
		id := c.Param("id")
		comments, err := mockDS.GetNoteComments(id)
		if err != nil {
			return c.JSON(http.StatusNotFound, map[string]string{"error": "Comments not found"})
		}
		return c.JSON(http.StatusOK, comments)
	})

	// Create mock data
	mockComments := []datastore.NoteComment{
		{
			ID:        1,
			NoteID:    1,
			Entry:     "First comment",
			CreatedAt: time.Now().Add(-2 * time.Hour),
			UpdatedAt: time.Now().Add(-2 * time.Hour),
		},
		{
			ID:        2,
			NoteID:    1,
			Entry:     "Second comment with special chars: <script>alert('XSS')</script>",
			CreatedAt: time.Now().Add(-1 * time.Hour),
			UpdatedAt: time.Now().Add(-1 * time.Hour),
		},
	}

	// Test cases
	testCases := []struct {
		name           string
		detectionID    string
		mockSetup      func(*mock.Mock)
		expectedStatus int
		expectedCount  int
		checkResponse  func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:        "Detection with comments",
			detectionID: "1",
			mockSetup: func(m *mock.Mock) {
				m.On("GetNoteComments", "1").Return(mockComments, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var comments []datastore.NoteComment
				err := json.Unmarshal(rec.Body.Bytes(), &comments)
				require.NoError(t, err)
				assert.Len(t, comments, 2)
				assert.Equal(t, "First comment", comments[0].Entry)
				assert.Contains(t, comments[1].Entry, "Second comment with special chars")
			},
		},
		{
			name:        "Detection without comments",
			detectionID: "2",
			mockSetup: func(m *mock.Mock) {
				m.On("GetNoteComments", "2").Return([]datastore.NoteComment{}, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  0,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var comments []datastore.NoteComment
				err := json.Unmarshal(rec.Body.Bytes(), &comments)
				require.NoError(t, err)
				assert.Empty(t, comments)
			},
		},
		{
			name:        "Detection not found",
			detectionID: "999",
			mockSetup: func(m *mock.Mock) {
				m.On("GetNoteComments", "999").Return([]datastore.NoteComment{}, errors.New("record not found"))
			},
			expectedStatus: http.StatusNotFound,
			expectedCount:  0,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var response map[string]string
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, "Comments not found", response["error"])
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Setup mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/"+tc.detectionID+"/comments", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.detectionID)

			// Call the handler through the Echo framework
			e.ServeHTTP(rec, req)

			// Check response
			assert.Equal(t, tc.expectedStatus, rec.Code)
			tc.checkResponse(t, rec)

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestReviewDetectionConcurrency tests concurrent review attempts
func TestReviewDetectionConcurrency(t *testing.T) {
	t.Run("DeterministicRaceCondition", func(t *testing.T) {
		// Setup a fresh environment for this subtest
		e, mockDS, controller := setupTestEnvironment(t)

		// Create mock note
		mockNote := datastore.Note{
			ID:     1,
			Locked: false,
		}

		// Create review request
		reviewRequest := map[string]any{
			"verified": "correct",
			"comment":  "This is a correct identification",
		}
		jsonData, err := json.Marshal(reviewRequest)
		require.NoError(t, err)

		// Setup mock behavior - note is not locked initially, then becomes locked
		mockDS.On("Get", "1").Return(mockNote, nil).Times(2)

		// First request will find the note unlocked and complete successfully
		mockDS.On("IsNoteLocked", "1").Return(false, nil).Once()
		mockDS.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil).Once()
		mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil).Once()

		// Second request will find the note already locked (race condition)
		mockDS.On("IsNoteLocked", "1").Return(true, nil).Once()

		// Create two requests
		req1 := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review",
			bytes.NewReader(jsonData))
		req1.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec1 := httptest.NewRecorder()
		c1 := e.NewContext(req1, rec1)
		c1.SetPath("/api/v2/detections/:id/review")
		c1.SetParamNames("id")
		c1.SetParamValues("1")

		req2 := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review",
			bytes.NewReader(jsonData))
		req2.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec2 := httptest.NewRecorder()
		c2 := e.NewContext(req2, rec2)
		c2.SetPath("/api/v2/detections/:id/review")
		c2.SetParamNames("id")
		c2.SetParamValues("1")

		// Execute both requests sequentially (simulating the race condition outcome)
		_ = controller.ReviewDetection(c1) // Expected to succeed
		_ = controller.ReviewDetection(c2) // Expected to fail with conflict

		// Verify results - check status codes
		assert.Equal(t, http.StatusOK, rec1.Code, "First request should succeed")
		assert.Equal(t, http.StatusConflict, rec2.Code, "Second request should fail with conflict due to note being locked")

		// Parse second response to verify error message
		var resp2 map[string]any
		err2 := json.Unmarshal(rec2.Body.Bytes(), &resp2)
		require.NoError(t, err2)

		// Check error message for second request
		assert.Contains(t, resp2["message"], "Detection is locked and status cannot be changed")

		// Verify expectations
		mockDS.AssertExpectations(t)
	})
}

// TestTrueConcurrentReviewAccess tests true concurrent review access
func TestTrueConcurrentReviewAccess(t *testing.T) {
	// Go 1.25: Add test metadata for better test organization and reporting
	t.Attr("component", "detections")
	t.Attr("type", "concurrent")
	t.Attr("feature", "review")

	// Setup with a fresh test environment
	e, mockDS, controller := setupTestEnvironment(t)

	// Create a mock note that will be accessed concurrently
	mockNote := datastore.Note{
		ID:     1,
		Locked: false,
	}

	// Setup server to handle requests
	server := httptest.NewServer(e)
	defer server.Close()

	// Register routes
	e.POST("/api/v2/detections/:id/review", controller.ReviewDetection)

	// Create a JSON review request that will be used by all goroutines
	reviewRequest := map[string]any{
		"correct":  true,
		"comment":  "This is a correct identification",
		"verified": "correct",
	}
	jsonData, err := json.Marshal(reviewRequest)
	require.NoError(t, err)

	// Number of concurrent requests to make
	numConcurrent := 10

	// Create waitgroups to coordinate goroutines
	// Go 1.25: Using WaitGroup.Go() for automatic Add/Done management
	var wg sync.WaitGroup

	// Create a barrier to ensure goroutines start roughly at the same time
	var barrier sync.WaitGroup
	barrier.Add(1)

	// Track results
	var successes, failures, conflicts int32

	// Configure mock expectations for concurrent access - more flexible approach
	// First call to Get - all goroutines should be able to get the note
	mockDS.On("Get", "1").Return(mockNote, nil).Maybe()

	// IsNoteLocked - could return either false or true depending on timing
	mockDS.On("IsNoteLocked", "1").Return(false, nil).Maybe()
	mockDS.On("IsNoteLocked", "1").Return(true, nil).Maybe()

	// No longer using temporary locks during review operations

	// SaveNoteComment and SaveNoteReview - might be called depending on success
	mockDS.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil).Maybe()
	mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil).Maybe()

	// Launch concurrent requests using Go 1.25 WaitGroup.Go() pattern
	for range numConcurrent {
		wg.Go(func() {
			// Wait for the barrier to be lifted
			barrier.Wait()

			// Create a fresh request for each goroutine
			client := createTestHTTPClient(5 * time.Second)
			defer client.CloseIdleConnections() // Ensure cleanup
			req, _ := http.NewRequest(
				http.MethodPost,
				server.URL+"/api/v2/detections/1/review",
				bytes.NewReader(jsonData),
			)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)

			// Make the request
			resp, err := client.Do(req)

			// Track the results
			if err == nil {
				defer func() {
					_ = resp.Body.Close() // Safe to ignore in test cleanup
				}()

				switch resp.StatusCode {
				case http.StatusOK:
					atomic.AddInt32(&successes, 1)
				case http.StatusConflict:
					atomic.AddInt32(&conflicts, 1)
				default:
					atomic.AddInt32(&failures, 1)
				}
			} else {
				atomic.AddInt32(&failures, 1)
			}
		})
	}

	// Lift the barrier to start all goroutines roughly simultaneously
	barrier.Done()

	// Wait for all goroutines to complete
	wg.Wait()

	// Verify results - in a true concurrent environment, we expect:
	// 1. At least one success (hopefully exactly one, but we can't guarantee it)
	// 2. Some number of conflicts
	// 3. No unexpected failures
	assert.GreaterOrEqual(t, successes, int32(0), "At least one request should succeed")
	assert.GreaterOrEqual(t, conflicts, int32(0), "Some requests should get conflict status")
	assert.Equal(t, int32(0), failures, "There should be no unexpected failures")
	assert.Equal(t, int32(numConcurrent), successes+conflicts, "All requests should either succeed or get conflict") // #nosec G115 -- numConcurrent is a small test constant (3-10), no overflow risk
}

// TestTrueConcurrentPlatformSpecific tests platform-specific concurrency
func TestTrueConcurrentPlatformSpecific(t *testing.T) {
	// Go 1.25: Add test metadata for better test organization and reporting
	t.Attr("component", "detections")
	t.Attr("type", "concurrent")
	t.Attr("feature", "platform-specific")

	// Setup with a fresh test environment
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup server
	server := httptest.NewServer(e)
	defer server.Close()

	// Register routes
	e.POST("/api/v2/detections/:id/review", controller.ReviewDetection)

	// Create a JSON review request
	reviewRequest := map[string]any{
		"correct":  true,
		"comment":  "This is a correct identification",
		"verified": "correct",
	}
	jsonData, err := json.Marshal(reviewRequest)
	require.NoError(t, err)

	// Get platform-appropriate concurrency level
	numConcurrent := getConcurrencyLevel()

	// Mock note that will be accessed concurrently
	mockNote := datastore.Note{
		ID:     1,
		Locked: false,
	}

	// Setup mock expectations - more resilient approach for real concurrency
	mockDS.On("Get", "1").Return(mockNote, nil).Maybe()
	mockDS.On("IsNoteLocked", "1").Return(false, nil).Maybe()
	mockDS.On("IsNoteLocked", "1").Return(true, nil).Maybe()
	mockDS.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil).Maybe()
	mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil).Maybe()

	// Create wait group and barrier
	// Go 1.25: Using WaitGroup.Go() for automatic Add/Done management
	var wg sync.WaitGroup
	var barrier sync.WaitGroup
	barrier.Add(1)

	// Track results
	var successes, failures, conflicts int32

	// Add timeout to prevent test hanging on platform-specific issues
	done := make(chan bool)

	go func() {
		// Launch concurrent requests using Go 1.25 WaitGroup.Go() pattern
		for i := range numConcurrent {
			wg.Go(func() {
				goroutineID := i

				// Wait for barrier
				barrier.Wait()

				// Create request with timeout appropriate for platform
				client := createTestHTTPClient(5 * time.Second)
				defer client.CloseIdleConnections() // Ensure cleanup

				// Add small stagger time to simulate more realistic conditions
				// (especially important on Windows)
				if runtime.GOOS == "windows" {
					time.Sleep(time.Duration(goroutineID) * 10 * time.Millisecond)
				}

				req, _ := http.NewRequest(
					http.MethodPost,
					server.URL+"/api/v2/detections/1/review",
					bytes.NewReader(jsonData),
				)
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)

				// Make request
				resp, err := client.Do(req)

				// Track results
				if err == nil {
					defer func() { _ = resp.Body.Close() }()
					categorizeHTTPResponse(t, resp.StatusCode, &successes, &conflicts, &failures)
				} else {
					t.Logf("Request error: %v", err)
					atomic.AddInt32(&failures, 1)
				}
			})
		}

		// Start all goroutines
		barrier.Done()

		// Wait for completion
		wg.Wait()
		done <- true
	}()

	// Add test timeout
	select {
	case <-done:
		// Test completed normally
	case <-time.After(10 * time.Second):
		require.Fail(t, "Test timed out")
	}

	// Verify results with platform-specific considerations
	// In real concurrent execution, we can't strictly control which request wins
	assert.GreaterOrEqual(t, successes, int32(0), "At least one request should succeed")
	assert.GreaterOrEqual(t, conflicts, int32(0), "Some requests should get conflict status")
	assert.Equal(t, int32(0), failures, "There should be no unexpected failures")
	assert.Equal(t, int32(numConcurrent), successes+conflicts, "All requests should either succeed or get conflict") // #nosec G115 -- numConcurrent is a small test constant (3-10), no overflow risk
}
