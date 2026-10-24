// detections_test.go: Package api provides tests for API v2 detection endpoints.

package detections

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/analysis/species"
	"github.com/tphakala/birdnet-go/internal/api/v2/apicore"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

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

// invalidQueryParamsMessage is the message GetDetections answers a rejected query with, except for malformed date parameters, which carry their own.
const invalidQueryParamsMessage = "Invalid detection query parameters"

// TestGetDetections tests the GetDetections endpoint with various query types
func TestGetDetections(t *testing.T) {
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
	}{
		{
			name: "All detections",
			queryParams: map[string]string{
				"queryType":  "all",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", "", false, 10, 0).Return(mockNotes, int64(2), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
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
				m.On("SearchNotes", "Crow", false, 10, 0).Return(mockNotes[:1], int64(1), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
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
		},
		{
			name:           "Invalid_offset_parameter",
			queryParams:    map[string]string{"offset": "abc"},
			expectedStatus: http.StatusBadRequest,
			mockSetup:      func(m *mock.Mock) { /* No DB interaction expected */ },
			expectedCount:  0, // Not relevant for error case
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
		},
		// Auto-inference: species param without queryType should infer queryType=species
		{
			name: "Species param without queryType infers species query",
			queryParams: map[string]string{
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
		},
		// Auto-inference: search param without queryType should infer queryType=search
		{
			name: "Search param without queryType infers search query",
			queryParams: map[string]string{
				"search":     "Crow",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", "Crow", false, 10, 0).Return(mockNotes[:1], int64(1), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
		},
		// Date param without queryType should route through advanced search
		{
			name: "Date param without queryType uses advanced search",
			queryParams: map[string]string{
				"date":       "2025-03-07",
				"numResults": "10",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotesAdvanced", mock.AnythingOfType("*datastore.AdvancedSearchFilters")).
					Return(mockNotes[:1], int64(1), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  1,
		},
		// limit as alias for numResults
		{
			name: "Limit param as alias for numResults",
			queryParams: map[string]string{
				"queryType": "all",
				"limit":     "5",
				"offset":    "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", "", false, 5, 0).Return(mockNotes, int64(2), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		// numResults takes precedence over limit
		{
			name: "numResults takes precedence over limit alias",
			queryParams: map[string]string{
				"queryType":  "all",
				"numResults": "10",
				"limit":      "5",
				"offset":     "0",
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", "", false, 10, 0).Return(mockNotes, int64(2), nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
	}

	for _, tc := range testCases {
		localTC := tc // Capture range variable
		t.Run(localTC.name, func(t *testing.T) {
			// Each subtest gets its own handler and cache, so a cached page from one
			// case can never answer another.
			e, mockDS, controller := setupTestEnvironment(t)
			controller.RegisterDetectionRoutes(controller.Group) // Ensure routes are registered on the test echo instance
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

			if localTC.expectedStatus == http.StatusOK {
				detections, _ := decodePaginated(t, rec.Body.Bytes())
				assert.Len(t, detections, localTC.expectedCount)
			} else {
				var errResp map[string]any
				require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &errResp))
				assert.Contains(t, errResp["message"], invalidQueryParamsMessage)
			}

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
				assert.Equal(t, "bird", response.ModelType)
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
				m.On("Get", "999").Return(datastore.Note{}, errors.NewStd("record not found"))
			},
			expectedStatus: http.StatusNotFound,
			checkResponse: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				var response apicore.ErrorResponse
				err := json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Equal(t, "Detection not found", response.Message)
				assert.NotEmpty(t, response.CorrelationID)
				assert.Equal(t, http.StatusNotFound, response.Code)
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

// TestGetDetection_ClipNameSerialized is a HANDLER-level guard for the REST
// detection response contract: the JSON payload MUST carry a clipName field,
// basename-stripped to match the SSE privacy contract, and MUST omit it when the
// detection has no clip. The frontend gates per-detection audio playback on this
// field; if the endpoint stops serializing it, every export-enabled user silently
// loses media. Asserting on the raw JSON (not just an unmarshaled struct) makes
// the test fail if the struct field or its json tag is ever removed.
func TestGetDetection_ClipNameSerialized(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)

	const clipPath = "2025/03/corvus_brachyrhynchos_95p_20250307T081500Z.wav"
	const clipBase = "corvus_brachyrhynchos_95p_20250307T081500Z.wav"

	baseNote := datastore.Note{
		ID:             7,
		Date:           "2025-03-07",
		Time:           "08:15:00",
		Source:         testRealtimeSource(),
		SpeciesCode:    "AMCRO",
		ScientificName: "Corvus brachyrhynchos",
		CommonName:     "American Crow",
		Confidence:     0.95,
		BeginTime:      time.Now().Add(-time.Hour),
		EndTime:        time.Now(),
	}

	t.Run("clip present is serialized as basename", func(t *testing.T) {
		note := baseNote
		note.ClipName = clipPath
		mockDS.ExpectedCalls = nil
		mockDS.On("Get", "7").Return(note, nil)
		mockDS.On("GetHourlyWeather", "2025-03-07").Return([]datastore.HourlyWeather{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/7", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("7")

		require.NoError(t, controller.GetDetection(c))
		require.Equal(t, http.StatusOK, rec.Code)

		// Raw JSON must contain the field so removing it from the struct fails here.
		var payload map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		clip, ok := payload["clipName"]
		require.True(t, ok, "response JSON must include clipName")
		assert.Equal(t, clipBase, clip, "clipName must be basename-stripped (SSE privacy contract)")
		assert.NotContains(t, rec.Body.String(), "2025/03",
			"clipName must not leak the on-disk directory layout")
	})

	t.Run("empty clip is omitted", func(t *testing.T) {
		note := baseNote
		note.ClipName = ""
		mockDS.ExpectedCalls = nil
		mockDS.On("Get", "7").Return(note, nil)
		mockDS.On("GetHourlyWeather", "2025-03-07").Return([]datastore.HourlyWeather{}, nil)

		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/7", http.NoBody)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("7")

		require.NoError(t, controller.GetDetection(c))
		require.Equal(t, http.StatusOK, rec.Code)
		var payload map[string]any
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
		_, ok := payload["clipName"]
		assert.False(t, ok, "clipName must be omitted when the detection has no clip")
	})
}

// TestGetRecentDetections_ClipNameSerialized covers the list converter path so
// the per-detection clipName signal is present in list responses too.
func TestGetRecentDetections_ClipNameSerialized(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)

	notes := []datastore.Note{
		{
			ID:             1,
			Date:           "2025-03-07",
			Time:           "08:15:00",
			Source:         testRealtimeSource(),
			ScientificName: "Corvus brachyrhynchos",
			CommonName:     "American Crow",
			Confidence:     0.95,
			ClipName:       "2025/03/corvus_brachyrhynchos_95p_20250307T081500Z.wav",
		},
	}
	mockDS.On("GetLastDetections", 10).Return(notes, nil).Once()

	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/recent", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	require.NoError(t, controller.GetRecentDetections(c))
	require.Equal(t, http.StatusOK, rec.Code)

	var payload []map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &payload))
	require.Len(t, payload, 1)
	assert.Equal(t, "corvus_brachyrhynchos_95p_20250307T081500Z.wav", payload[0]["clipName"],
		"list responses must carry the basename-stripped clipName")
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
	assert.Equal(t, "bird", response.ModelType)
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
	assert.Equal(t, "bird", response.ModelType)
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
			name:  "Default limit when none is given (#2361)",
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
			name:  "Limit at the maximum",
			limit: "1000",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", maxNumResults).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:  "Limit above the maximum is clamped",
			limit: "1001",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", maxNumResults).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:  "Negative limit uses default",
			limit: "-5",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", 10).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:  "Non-numeric limit uses default",
			limit: "abc",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", 10).Return(mockNotes, nil)
			},
			expectedStatus: http.StatusOK,
			expectedCount:  2,
		},
		{
			name:  "Database error",
			limit: "5",
			mockSetup: func(m *mock.Mock) {
				m.On("GetLastDetections", 5).Return([]datastore.Note{}, errors.NewStd("database error"))
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
				m.On("Get", "999").Return(datastore.Note{}, errors.NewStd("record not found"))
			},
			expectedStatus: http.StatusNotFound,
		},
		{
			name:        "Database error during delete",
			detectionID: "3",
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "3").Return(datastore.Note{ID: 3, Locked: false}, nil)
				m.On("Delete", "3").Return(errors.NewStd("database error"))
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

// TestDeleteDetectionRemovesFiles verifies that deleting a detection also
// removes the associated audio clip and spectrogram files from disk.
func TestDeleteDetectionRemovesFiles(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)

	// Create a subdirectory structure matching real clip paths
	baseDir := controller.SFS.BaseDir()
	clipDir := filepath.Join(baseDir, "2025", "01", "15")
	require.NoError(t, os.MkdirAll(clipDir, 0o750))

	clipFile := "Eurasian_Blue_Tit_85p_20250115T100000Z.wav"
	clipPath := filepath.Join(clipDir, clipFile)

	// Create the audio clip file
	require.NoError(t, os.WriteFile(clipPath, []byte("fake-audio"), 0o600))

	// Create spectrogram files in the naming patterns used by the API
	baseName := "Eurasian_Blue_Tit_85p_20250115T100000Z"
	spectrogramFiles := []string{
		baseName + "_258px.png",
		baseName + "_1026px.png",
		baseName + "_1026px-legend.png",
		// Bat frequency-profile renders carry a versioned "-bat-v3" token; the earlier
		// "-bat-v2" and "-bat" files may still be on disk. All of them
		// must be removed (regression test for orphaned bat spectrograms on delete).
		baseName + "_1026px-bat.png",
		baseName + "_1026px-bat-legend.png",
		baseName + "_1026px-bat-v2.png",
		baseName + "_1026px-bat-v2-legend.png",
		baseName + "_1026px-bat-v3.png",
		baseName + "_1026px-bat-v3-legend.png",
		// Non-default visual style / dynamic-range / combined suffixes must also be
		// removed (regression test for orphaned styled spectrograms on delete).
		baseName + "_1026px-scientific_dark.png",
		baseName + "_1026px-dr80.png",
		baseName + "_1026px-high_contrast_dark-bat-legend.png",
	}
	for _, sf := range spectrogramFiles {
		require.NoError(t, os.WriteFile(filepath.Join(clipDir, sf), []byte("fake-png"), 0o600))
	}

	// Files that must survive deletion: a different clip's spectrogram in the same
	// directory, and a name that shares this clip's "<base>_<width>px" prefix but
	// with a "_" separator (not a render of this clip) - guards the deletion anchor.
	survivingFiles := []string{
		"Some_Other_Bird_50p_20250115T100001Z_1026px.png",
		baseName + "_1026px_1026px.png",
	}
	for _, sf := range survivingFiles {
		require.NoError(t, os.WriteFile(filepath.Join(clipDir, sf), []byte("fake-png"), 0o600))
	}

	// The ClipName stored in the DB is typically a relative path like
	// "clips/2025/01/15/file.wav" or just the path relative to export dir.
	// The controller normalizes it using the clips prefix.
	relativeClipName := filepath.Join("2025", "01", "15", clipFile)

	mockDS.On("Get", "10").Return(datastore.Note{
		ID:       10,
		Locked:   false,
		ClipName: relativeClipName,
	}, nil)
	mockDS.On("Delete", "10").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/10", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("10")

	err := controller.DeleteDetection(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	// Verify the audio clip was removed
	_, statErr := os.Stat(clipPath)
	assert.True(t, os.IsNotExist(statErr), "audio clip file should have been removed")

	// Verify spectrogram files were removed
	for _, sf := range spectrogramFiles {
		_, statErr := os.Stat(filepath.Join(clipDir, sf))
		assert.True(t, os.IsNotExist(statErr), "spectrogram file %s should have been removed", sf)
	}

	// Verify unrelated files were left intact
	for _, sf := range survivingFiles {
		_, statErr := os.Stat(filepath.Join(clipDir, sf))
		require.NoError(t, statErr, "unrelated file %s must not be removed", sf)
	}

	mockDS.AssertExpectations(t)
}

// TestDeleteDetectionMissingFilesNoError verifies that deleting a detection
// succeeds even when the associated files are already missing from disk.
func TestDeleteDetectionMissingFilesNoError(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)

	mockDS.On("Get", "20").Return(datastore.Note{
		ID:       20,
		Locked:   false,
		ClipName: "2025/01/15/nonexistent.wav",
	}, nil)
	mockDS.On("Delete", "20").Return(nil)

	req := httptest.NewRequest(http.MethodDelete, "/api/v2/detections/20", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("20")

	err := controller.DeleteDetection(c)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNoContent, rec.Code)

	mockDS.AssertExpectations(t)
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
			// The note read is unlocked, but the database re-check sees a lock
			// taken since: the review is refused and the comment is not saved.
			name:        "Locked in the database after the read",
			detectionID: "4",
			requestBody: `{"verified": "correct", "comment": "Late comment"}`,
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "4").Return(datastore.Note{ID: 4, Locked: false}, nil)
				m.On("IsNoteLocked", "4").Return(true, nil)
			},
			expectedStatus: http.StatusConflict,
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

// TestReviewDetectionFalsePositiveLocalizedResolution verifies the false-positive
// review path resolves a localized common name to its scientific name before
// adding it to the exclude list, mirroring the IgnoreSpecies endpoint.
func TestReviewDetectionFalsePositiveLocalizedResolution(t *testing.T) {
	e, mockDS, controller := setupTestEnvironmentWithBatName(t)
	clearExcludedSpeciesList(t, controller.Settings.Load())

	mockDS.ExpectedCalls = nil
	setupValidReviewMock(&mockDS.Mock, "7", 7, false)

	// Payload uses the snake_case "ignore_species" key that the frontend actually
	// sends (see ReviewCard.svelte / ReviewModal.svelte); a camelCase key would not
	// bind to DetectionRequest.IgnoreSpecies and would silently skip the exclusion.
	req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/7/review",
		strings.NewReader(`{"verified": "false_positive", "ignore_species": "mopsilepakko"}`))
	req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("id")
	c.SetParamValues("7")

	require.NoError(t, controller.ReviewDetection(c))
	assert.Equal(t, http.StatusOK, rec.Code)

	// The exclude list holds the scientific name, not the localized common name,
	// so the per-detection filter can match it.
	assert.Contains(t, controller.Settings.Load().Realtime.Species.Exclude, "Barbastella barbastellus")
	assert.NotContains(t, controller.Settings.Load().Realtime.Species.Exclude, "mopsilepakko")

	mockDS.AssertExpectations(t)
}

// TestReviewDetectionLockState verifies that a locked detection is frozen against changes
// unless the request is explicitly unlocking it.
func TestReviewDetectionLockState(t *testing.T) {
	e, mockDS, controller := setupTestEnvironment(t)

	t.Run("Unlock a locked detection should succeed", func(t *testing.T) {
		mockDS.ExpectedCalls = nil

		note := datastore.Note{ID: 10, Locked: true}
		mockDS.On("Get", "10").Return(note, nil)

		// checkDetectionNotLocked is bypassed when unlocking, so we process the review.
		mockDS.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
		mockDS.On("UnlockNote", "10").Return(nil)

		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/10/review",
			strings.NewReader(`{"verified":"false_positive","lock_detection":false}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("10")

		err := controller.ReviewDetection(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		mockDS.AssertExpectations(t)
	})

	t.Run("Locking an already locked detection should return 409", func(t *testing.T) {
		mockDS.ExpectedCalls = nil

		note := datastore.Note{ID: 11, Locked: true}
		mockDS.On("Get", "11").Return(note, nil)
		// No IsNoteLocked check is needed since in-memory check fails first.

		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/11/review",
			strings.NewReader(`{"verified":"false_positive","lock_detection":true}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("11")

		err := controller.ReviewDetection(c)
		require.NoError(t, err) // Handled error logic within controller
		assert.Equal(t, http.StatusConflict, rec.Code)

		mockDS.AssertExpectations(t)
	})

	t.Run("Verify-only without lock_detection on a locked detection stays 409", func(t *testing.T) {
		mockDS.ExpectedCalls = nil

		note := datastore.Note{ID: 12, Locked: true}
		mockDS.On("Get", "12").Return(note, nil)

		// lock_detection is omitted, so LockDetection binds to nil. That is NOT an
		// unlock, so the freeze guard applies and the detection is left untouched.
		// A plain bool would read the omitted field as false and silently unlock it.
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/12/review",
			strings.NewReader(`{"verified":"false_positive"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetParamNames("id")
		c.SetParamValues("12")

		err := controller.ReviewDetection(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusConflict, rec.Code)

		mockDS.AssertExpectations(t)
	})
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

// clearExcludedSpeciesList is a test helper that clears the excluded species list
// on the given settings to ensure test isolation. Call this at the beginning of
// tests that check list state.
func clearExcludedSpeciesList(t *testing.T, settings *conf.Settings) {
	t.Helper()
	settings.Realtime.Species.Exclude = []string{}
}

// TestIgnoreSpecies tests the IgnoreSpecies endpoint with toggle behavior
func TestIgnoreSpecies(t *testing.T) {
	// Test cases for error scenarios
	t.Run("Error cases", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())

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

				var response apicore.ErrorResponse
				err = json.Unmarshal(rec.Body.Bytes(), &response)
				require.NoError(t, err)
				assert.Contains(t, response.Message, tc.expectedError)
				assert.Equal(t, tc.expectedStatus, response.Code)
				assert.NotEmpty(t, response.CorrelationID)
			})
		}
	})

	t.Run("Localized name resolution", func(t *testing.T) {
		e, _, controller := setupTestEnvironmentWithBatName(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())

		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "mopsilepakko"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)

		err := controller.IgnoreSpecies(c)
		require.NoError(t, err)
		assert.Equal(t, http.StatusOK, rec.Code)

		var response IgnoreSpeciesResponse
		err = json.Unmarshal(rec.Body.Bytes(), &response)
		require.NoError(t, err)
		assert.Equal(t, "mopsilepakko", response.CommonName) // Original name is returned
		assert.Equal(t, "added", response.Action)
		assert.True(t, response.IsExcluded)

		// Verify settings actually contain the scientific name
		assert.Contains(t, controller.Settings.Load().Realtime.Species.Exclude, "Barbastella barbastellus")
		assert.NotContains(t, controller.Settings.Load().Realtime.Species.Exclude, "mopsilepakko")
	})

	t.Run("Localized name resolution round-trip remove", func(t *testing.T) {
		e, _, controller := setupTestEnvironmentWithBatName(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())

		toggle := func() IgnoreSpeciesResponse {
			req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
				strings.NewReader(`{"common_name": "mopsilepakko"}`))
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			rec := httptest.NewRecorder()
			require.NoError(t, controller.IgnoreSpecies(e.NewContext(req, rec)))
			require.Equal(t, http.StatusOK, rec.Code)
			var resp IgnoreSpeciesResponse
			require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
			return resp
		}

		// First toggle adds the resolved scientific name.
		added := toggle()
		assert.Equal(t, "added", added.Action)
		assert.True(t, added.IsExcluded)
		assert.Contains(t, controller.Settings.Load().Realtime.Species.Exclude, "Barbastella barbastellus")

		// Second toggle of the same localized name resolves to the same scientific
		// name and removes it: the localized round-trip is symmetric.
		removed := toggle()
		assert.Equal(t, "removed", removed.Action)
		assert.False(t, removed.IsExcluded)
		assert.NotContains(t, controller.Settings.Load().Realtime.Species.Exclude, "Barbastella barbastellus")
	})

	t.Run("Toggle reconciles a legacy localized entry instead of duplicating", func(t *testing.T) {
		e, _, controller := setupTestEnvironmentWithBatName(t)
		settings := controller.Settings.Load()
		clearExcludedSpeciesList(t, settings)

		// Simulate a pre-fix exclude list that stored the localized common name verbatim.
		settings.Realtime.Species.Exclude = []string{"mopsilepakko"}

		// Toggling the same localized name resolves to the scientific name; the legacy
		// common-name entry must be removed, not left as an un-removable orphan with a
		// scientific-name duplicate appended.
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "mopsilepakko"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		require.NoError(t, controller.IgnoreSpecies(e.NewContext(req, rec)))
		require.Equal(t, http.StatusOK, rec.Code)

		var resp IgnoreSpeciesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &resp))
		assert.Equal(t, "removed", resp.Action)
		assert.False(t, resp.IsExcluded)
		assert.Empty(t, controller.Settings.Load().Realtime.Species.Exclude)
	})

	t.Run("Multiple toggle operations", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())
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
}

// TestGetExcludedSpecies tests the GetExcludedSpecies endpoint
func TestGetExcludedSpecies(t *testing.T) {
	t.Run("Empty excluded list", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())

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
		clearExcludedSpeciesList(t, controller.Settings.Load())

		// First add some species
		speciesList := []string{"American Crow", "Red-bellied Woodpecker", "Blue Jay"}
		for _, s := range speciesList {
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
		assert.ElementsMatch(t, speciesList, response.Species)
	})

	t.Run("Excluded list reflects toggle operations", func(t *testing.T) {
		e, _, controller := setupTestEnvironment(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())

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

	t.Run("Reverse-resolves scientific names to common names", func(t *testing.T) {
		e, _, controller := setupTestEnvironmentWithBatName(t)
		clearExcludedSpeciesList(t, controller.Settings.Load())

		// Ignore via the localized name; the exclude list stores the scientific name.
		ignoreReq := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
			strings.NewReader(`{"common_name": "mopsilepakko"}`))
		ignoreReq.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		ignoreRec := httptest.NewRecorder()
		require.NoError(t, controller.IgnoreSpecies(e.NewContext(ignoreReq, ignoreRec)))
		require.Equal(t, http.StatusOK, ignoreRec.Code)
		require.Contains(t, controller.Settings.Load().Realtime.Species.Exclude, "Barbastella barbastellus")

		// GetExcludedSpecies reverse-resolves the stored scientific name back to the
		// common name the detection cards key their "ignored" badge on.
		req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
		rec := httptest.NewRecorder()
		require.NoError(t, controller.GetExcludedSpecies(e.NewContext(req, rec)))
		assert.Equal(t, http.StatusOK, rec.Code)

		var response ExcludedSpeciesResponse
		require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &response))
		assert.Equal(t, []string{"mopsilepakko"}, response.Species)
		assert.NotContains(t, response.Species, "Barbastella barbastellus")
	})
}

// TestIgnoreSpeciesConcurrency tests concurrent access to the IgnoreSpecies endpoint
func TestIgnoreSpeciesConcurrency(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	clearExcludedSpeciesList(t, controller.Settings.Load())

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

	// Ten sequential toggles of one species from an empty list end with it
	// removed, so a lost update under concurrency leaves Count at 1.
	req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)

	err := controller.GetExcludedSpecies(c)
	require.NoError(t, err)

	var response ExcludedSpeciesResponse
	err = json.Unmarshal(rec.Body.Bytes(), &response)
	require.NoError(t, err)

	assert.Equal(t, 0, response.Count, "an even number of toggles must leave the species out of the list")
}

// TestGetExcludedSpeciesConcurrentReadsWithToggle exercises GetExcludedSpecies
// (the lock-free reader) concurrently with IgnoreSpecies (the copy-on-write
// writer) under -race. Controller.Settings is an atomic.Pointer published via
// Settings.Store, and CloneSettings deep-copies the exclude slice, so a reader
// never observes a slice mutated in place and needs no lock. This guards that
// documented lock-free-read invariant against regressions.
func TestGetExcludedSpeciesConcurrentReadsWithToggle(t *testing.T) {
	e, _, controller := setupTestEnvironment(t)
	clearExcludedSpeciesList(t, controller.Settings.Load())

	names := []string{
		"Alpha Bird", "Beta Bird", "Gamma Bird", "Delta Bird",
		"Epsilon Bird", "Zeta Bird", "Eta Bird", "Theta Bird",
	}
	const iterations = 25

	// Spawned goroutines record failures atomically rather than asserting inline,
	// since require/assert FailNow must run on the test goroutine, not a worker.
	var failures atomic.Int32
	var wg sync.WaitGroup
	barrier := make(chan struct{})

	// Writers toggle their own species in and out of the exclude list.
	for _, name := range names {
		wg.Go(func() {
			<-barrier
			for range iterations {
				req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/ignore",
					strings.NewReader(`{"common_name": "`+name+`"}`))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
				rec := httptest.NewRecorder()
				if err := controller.IgnoreSpecies(e.NewContext(req, rec)); err != nil {
					failures.Add(1)
				}
			}
		})
	}

	// Readers hammer GetExcludedSpecies while the list is being mutated.
	for range names {
		wg.Go(func() {
			<-barrier
			for range iterations {
				req := httptest.NewRequest(http.MethodGet, "/api/v2/detections/ignored", http.NoBody)
				rec := httptest.NewRecorder()
				if err := controller.GetExcludedSpecies(e.NewContext(req, rec)); err != nil || rec.Code != http.StatusOK {
					failures.Add(1)
				}
			}
		})
	}

	close(barrier)
	wg.Wait()

	assert.Zero(t, failures.Load(), "concurrent ignore/get-excluded requests should all succeed")
}

// TestAddCommentMethod pins that AddComment saves the given text for the given
// note, returns the datastore error, and saves nothing for an empty comment.
func TestAddCommentMethod(t *testing.T) {
	const noteID uint = 7

	testCases := []struct {
		name        string
		commentText string
		saveErr     error
		expectSave  bool
	}{
		{name: "Valid comment", commentText: "This is a test comment", expectSave: true},
		{name: "Database error", commentText: "This is a test comment", saveErr: errors.NewStd("database error"), expectSave: true},
		{name: "Empty comment saves nothing", commentText: ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			_, mockDS, controller := setupTestEnvironment(t)
			if tc.expectSave {
				mockDS.On("SaveNoteComment", mock.MatchedBy(func(c *datastore.NoteComment) bool {
					return c.NoteID == noteID && c.Entry == tc.commentText
				})).Return(tc.saveErr).Once()
			}

			err := controller.AddComment(noteID, tc.commentText)
			if tc.saveErr != nil {
				require.ErrorIs(t, err, tc.saveErr)
			} else {
				require.NoError(t, err)
			}
			mockDS.AssertExpectations(t)
		})
	}
}

// TestApplySpeciesTrackingMetadata_NoveltyFlags verifies that the isNewSpecies,
// isNewThisYear, and isNewThisSeason flags are true only when the detection date
// matches the species' first-seen date for each period. Regression test for the
// species-tracking novelty-flag bug.
func TestApplySpeciesTrackingMetadata_NoveltyFlags(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "unit")
	t.Attr("feature", "novelty-flags")

	// The yearly reset date is set explicitly: a zero month and day make
	// shouldResetYear (species/season.go) reset the yearly set on every call
	// made on 30 November, which would clear the seeded species.
	settings := &conf.SpeciesTrackingSettings{
		Enabled:              true,
		NewSpeciesWindowDays: 30,
		SyncIntervalMinutes:  60,
		YearlyTracking: conf.YearlyTrackingSettings{
			Enabled:    true,
			WindowDays: 30,
			ResetMonth: conf.DefaultYearlyTrackingResetMonth,
			ResetDay:   conf.DefaultYearlyTrackingResetDay,
		},
		SeasonalTracking: conf.SeasonalTrackingSettings{Enabled: true, WindowDays: 30},
	}

	tracker := species.NewTrackerFromSettings(nil, settings)

	// Seed the first sighting at the current time. The handler reads the status
	// at time.Now(), so a fixed seed date stops counting as this year's (and this
	// season's) first sighting once the calendar moves on.
	firstSeen := time.Now()
	_, _ = tracker.CheckAndUpdateSpecies("Parus major", firstSeen)

	controller := &Handler{Core: &apicore.Core{Processor: &processor.Processor{
		NewSpeciesTracker: tracker,
	}}}

	tests := []struct {
		name          string
		detectionDate string
		wantNew       bool
	}{
		{"detection on first-seen date is new", firstSeen.Format(time.DateOnly), true},
		{"detection on later date is not new", firstSeen.AddDate(0, 0, 1).Format(time.DateOnly), false},
		{"detection on much later date is not new", firstSeen.AddDate(0, 3, 0).Format(time.DateOnly), false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			var det DetectionResponse
			controller.applySpeciesTrackingMetadata(&det, "Parus major", tt.detectionDate)
			assert.Equal(t, tt.wantNew, det.IsNewSpecies, "isNewSpecies for date %s", tt.detectionDate)
			assert.Equal(t, tt.wantNew, det.IsNewThisYear, "isNewThisYear for date %s", tt.detectionDate)
			assert.Equal(t, tt.wantNew, det.IsNewThisSeason, "isNewThisSeason for date %s", tt.detectionDate)
		})
	}
}

// TestNoteToDetectionResponse_SourceInfo verifies that noteToDetectionResponse
// populates DetectionResponse.Source as *SourceInfo when note.Source.ID is set,
// leaves it nil when ID is empty, and derives Type from the SafeString.
func TestNoteToDetectionResponse_SourceInfo(t *testing.T) {
	t.Parallel()
	t.Attr("component", "detections")
	t.Attr("type", "unit")
	t.Attr("feature", "source-info")

	controller := &Handler{Core: &apicore.Core{}}
	controller.Settings.Store(apitest.NewValidTestSettings())

	tests := []struct {
		name        string
		source      datastore.AudioSource
		wantNil     bool
		wantID      string
		wantType    string
		wantDisplay string
	}{
		{
			name:    "empty source stays nil",
			source:  datastore.AudioSource{},
			wantNil: true,
		},
		{
			name: "rtsp source populates id, type, and display name",
			source: datastore.AudioSource{
				ID:          "rtsp_87b89761",
				SafeString:  "rtsp://camera.local/stream",
				DisplayName: "Front Yard",
			},
			wantID:      "rtsp_87b89761",
			wantType:    "rtsp",
			wantDisplay: "Front Yard",
		},
		{
			name: "alsa source without friendly name falls back to raw device",
			source: datastore.AudioSource{
				ID:          "hw:1,0",
				SafeString:  "hw:1,0",
				DisplayName: "hw:1,0",
			},
			wantID:      "hw:1,0",
			wantType:    "alsa",
			wantDisplay: "hw:1,0",
		},
		{
			name: "unknown safe string yields type unknown",
			source: datastore.AudioSource{
				ID:         "custom-source",
				SafeString: "file:///tmp/audio.wav",
			},
			wantID:   "custom-source",
			wantType: "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			note := &datastore.Note{
				ID:     42,
				Date:   "2026-04-14",
				Time:   "09:30:00",
				Source: tt.source,
			}
			resp := controller.noteToDetectionResponse(note, false, nil)

			if tt.wantNil {
				assert.Nil(t, resp.Source, "Source should be nil when note.Source.ID is empty")
				return
			}
			require.NotNil(t, resp.Source, "Source should be populated when note.Source.ID is set")
			assert.Equal(t, tt.wantID, resp.Source.ID)
			assert.Equal(t, tt.wantType, resp.Source.Type)
			assert.Equal(t, tt.wantDisplay, resp.Source.DisplayName)
		})
	}
}
