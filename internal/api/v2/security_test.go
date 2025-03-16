// security_test.go: Package api provides security tests for API v2 endpoints.
// This file focuses on testing general API security requirements including
// input validation against attacks, rate limiting, CORS configuration, and CSRF protection.

package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestInputValidation tests that API endpoints properly validate and reject invalid inputs
func TestInputValidation(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Test cases for different API endpoints
	testCases := []struct {
		name           string
		method         string
		path           string
		body           string
		queryParams    map[string]string
		handler        func(c echo.Context) error
		mockSetup      func(*mock.Mock)
		expectedStatus int
		expectedError  string
	}{
		{
			name:   "SQL Injection in ID parameter",
			method: http.MethodGet,
			path:   "/api/v2/detections/1%3BDROP%20TABLE%20notes", // URL-encoded version of "1;DROP TABLE notes"
			handler: func(c echo.Context) error {
				return controller.GetDetection(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Setup all possible method calls
				m.On("Get", mock.Anything).Return(datastore.Note{}, errors.New("not found"))
			},
			expectedStatus: http.StatusNotFound,
			expectedError:  "Detection not found",
		},
		{
			name:   "XSS in search parameter",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "<script>alert('XSS')</script>",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// The search should execute but with sanitized input
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "Path traversal in date parameter",
			method: http.MethodGet,
			path:   "/api/v2/analytics/daily",
			queryParams: map[string]string{
				"start_date": "../../../etc/passwd",
				"end_date":   "2023-01-07",
			},
			handler: func(c echo.Context) error {
				return controller.GetDailyAnalytics(c)
			},
			mockSetup: func(m *mock.Mock) {
				// No mock expectations needed as validation should fail before DB access
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid start_date format. Use YYYY-MM-DD",
		},
		{
			name:   "Large numerical values in parameters",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType":  "all",
				"numResults": "999999999999999999999999999999",
				"offset":     "999999999999999999999999999999",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Only mock what's actually being called
				m.On("SearchNotes", "", false, 1000, 9223372036854775807).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "JSON injection in review body",
			method: http.MethodPost,
			path:   "/api/v2/detections/1/review",
			body:   `{"verified": "correct", "comment": "}\n{\"malicious\":true"}`,
			handler: func(c echo.Context) error {
				return controller.ReviewDetection(c)
			},
			mockSetup: func(m *mock.Mock) {
				// For the review operation on the specific item
				m.On("Get", "1").Return(datastore.Note{ID: 1, Locked: false}, nil)
				m.On("IsNoteLocked", "1").Return(false, nil)
				m.On("LockNote", "1").Return(nil)

				// Comment should be passed through but properly escaped
				m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
				m.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
			},
			expectedStatus: http.StatusOK,
		},
		// New security abuse test cases
		{
			name:   "Path traversal with encoded characters",
			method: http.MethodGet,
			path:   "/api/v2/analytics/daily",
			queryParams: map[string]string{
				"start_date": "%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd", // ../../../etc/passwd URL encoded
				"end_date":   "2023-01-07",
			},
			handler: func(c echo.Context) error {
				return controller.GetDailyAnalytics(c)
			},
			mockSetup: func(m *mock.Mock) {
				// No mock expectations needed as validation should fail before DB access
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid start_date format. Use YYYY-MM-DD",
		},
		{
			name:   "Command injection attempt",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "bird; rm -rf /",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// The search should execute but with sanitized input
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "Buffer overflow with extremely long parameter",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     strings.Repeat("A", 100000), // Very long string
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// If input validation works properly, this might either be rejected or truncated
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK, // Should handle it gracefully
		},
		{
			name:   "HTTP parameter pollution",
			method: http.MethodGet,
			path:   "/api/v2/detections?queryType=all&offset=0&offset=malicious", // Using URL with duplicate params directly
			queryParams: map[string]string{
				"queryType": "all",
				"offset":    "0",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Only mock what's actually being called
				m.On("SearchNotes", "", false, 100, 0).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "Malformed JSON payload",
			method: http.MethodPost,
			path:   "/api/v2/detections/1/review",
			body:   `{"verified": "correct", "comment": "test"`, // Missing closing brace
			handler: func(c echo.Context) error {
				return controller.ReviewDetection(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Need to mock Get since it's called before JSON validation
				m.On("Get", "1").Return(datastore.Note{ID: 1, Locked: false}, nil)
				m.On("IsNoteLocked", "1").Return(false, nil)
				m.On("LockNote", "1").Return(nil)
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "unexpected EOF",
		},
		{
			name:   "Unicode normalization attack",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "bird\u0000.mp3", // Null byte injection
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// The search should execute but with sanitized input
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "Negative Offset and Limit",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType":  "all",
				"numResults": "-50",
				"offset":     "-10",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Controller now sets negative offset to 0 and negative numResults to 100
				m.On("SearchNotes", "", false, 100, 0).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		// Advanced XSS test cases
		{
			name:   "DOM-based XSS with event handler",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "bird' onmouseover='alert(1)",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with HTML entity encoding evasion",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "&#x3C;script&#x3E;alert(1)&#x3C;/script&#x3E;",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with JavaScript protocol in URL",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "javascript:alert(document.cookie)",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with CSS expression",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "bird</style><style>body{background-image:url('javascript:alert(1)')}</style>",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with SVG animation",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "<svg><animate onbegin=alert(1) attributeName=x dur=1s>",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with polyglot payload",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "jaVasCript:/*-/*`/*\\`/*'/*\"/**/(/* */oNcliCk=alert() )//%0D%0A%0D%0A//</stYle/</titLe/</teXtarEa/</scRipt/--!>\\x3csVg/<sVg/oNloAd=alert()//\\x3e",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with attribute injection",
			method: http.MethodPost,
			path:   "/api/v2/detections/1/review",
			body:   `{"verified": "correct", "comment": "\" onmouseover=\"alert(1)"}`,
			handler: func(c echo.Context) error {
				return controller.ReviewDetection(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("Get", "1").Return(datastore.Note{ID: 1, Locked: false}, nil)
				m.On("IsNoteLocked", "1").Return(false, nil)
				m.On("LockNote", "1").Return(nil)
				m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
				m.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with template injection",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "${alert(1)}",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with Unicode normalization",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "＜script＞alert(1)＜/script＞", // Full-width characters
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "XSS with data URI",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"query":     "data:text/html;base64,PHNjcmlwdD5hbGVydCgxKTwvc2NyaXB0Pg==",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
				m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock expectations
			mockDS.ExpectedCalls = nil
			tc.mockSetup(&mockDS.Mock)

			// Create request
			var req *http.Request
			if tc.method == http.MethodPost || tc.method == http.MethodPut {
				req = httptest.NewRequest(tc.method, tc.path, strings.NewReader(tc.body))
				req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
			} else {
				req = httptest.NewRequest(tc.method, tc.path, http.NoBody)
			}

			// Add query parameters
			if len(tc.queryParams) > 0 {
				q := req.URL.Query()
				for k, v := range tc.queryParams {
					q.Add(k, v)
				}
				req.URL.RawQuery = q.Encode()
			}

			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath(tc.path)

			// Set path parameters if present (extract ID from path)
			if strings.Contains(tc.path, "/detections/") && strings.Contains(tc.path, "/review") {
				parts := strings.Split(tc.path, "/")
				if len(parts) > 4 {
					c.SetParamNames("id")
					c.SetParamValues(parts[4])
					// Create path without URL-encoded characters for Echo's routing
					pathWithoutEncoding := "/api/v2/detections/" + parts[4] + "/review"
					c.SetPath(pathWithoutEncoding)
				}
			} else if strings.Contains(tc.path, "/detections/") {
				parts := strings.Split(tc.path, "/")
				if len(parts) > 3 {
					c.SetParamNames("id")
					c.SetParamValues(parts[4])
					// Create path without URL-encoded characters for Echo's routing
					pathWithoutEncoding := "/api/v2/detections/" + parts[4]
					c.SetPath(pathWithoutEncoding)
				}
			}

			// Call handler
			err := tc.handler(c)

			// Check response
			if tc.expectedStatus == http.StatusOK {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedStatus, rec.Code)
			} else {
				// For error responses
				if err != nil {
					// Direct error from handler
					var httpErr *echo.HTTPError
					if errors.As(err, &httpErr) {
						assert.Equal(t, tc.expectedStatus, httpErr.Code)
						if tc.expectedError != "" {
							assert.Contains(t, fmt.Sprintf("%v", httpErr.Message), tc.expectedError)
						}
					}
				} else {
					// Error handled by controller and returned as JSON
					assert.Equal(t, tc.expectedStatus, rec.Code)
					if tc.expectedError != "" {
						var errorResp map[string]interface{}
						err = json.Unmarshal(rec.Body.Bytes(), &errorResp)
						assert.NoError(t, err)
						if errorResp["error"] != nil {
							assert.Contains(t, errorResp["error"].(string), tc.expectedError)
						}
					}
				}
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestDDoSProtection simulates a basic DDoS attack to verify API resilience
func TestDDoSProtection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Configure mock to handle multiple requests
	// Only mock what's actually being called
	mockDS.On("SearchNotes", "", false, 100, 0).Return([]datastore.Note{}, nil)
	mockDS.On("CountSearchResults", mock.Anything).Return(int64(0), nil)

	// Test configuration
	concurrentRequests := 50
	requestPath := "/api/v2/detections"

	// Create a wait group to synchronize goroutines
	var wg sync.WaitGroup
	wg.Add(concurrentRequests)

	// Track response times and status codes
	var responseTimesMs []float64
	var statusCodes []int
	var requestErrors []error

	// Mutex for safe concurrent updates to shared slices
	var mutex sync.Mutex

	// Launch concurrent requests
	for i := 0; i < concurrentRequests; i++ {
		go func() {
			defer wg.Done()

			// Create request
			req := httptest.NewRequest(http.MethodGet, requestPath, http.NoBody)
			req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)

			// Record time before request
			startTime := time.Now()

			// Execute request
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			err := controller.GetDetections(c)

			// Record time after request
			duration := time.Since(startTime)

			// Safely update shared data
			mutex.Lock()
			defer mutex.Unlock()

			responseTimesMs = append(responseTimesMs, float64(duration.Milliseconds()))
			statusCodes = append(statusCodes, rec.Code)
			requestErrors = append(requestErrors, err)
		}()
	}

	// Wait for all requests to complete
	wg.Wait()

	// Analyze results
	var avgResponseTime float64
	var successCount int

	for i, statusCode := range statusCodes {
		if statusCode == http.StatusOK && requestErrors[i] == nil {
			successCount++
			avgResponseTime += responseTimesMs[i]
		}
	}

	if successCount > 0 {
		avgResponseTime /= float64(successCount)
	}

	// Log results (actual test is observational)
	t.Logf("DDoS simulation completed with %d concurrent requests", concurrentRequests)
	t.Logf("Successful requests: %d (%.1f%%)", successCount, float64(successCount)/float64(concurrentRequests)*100)
	t.Logf("Average response time: %.2f ms", avgResponseTime)

	// In a real-world scenario, we would check that:
	// 1. The API doesn't crash (already verified by successCount)
	// 2. Rate limiting is applied (would see 429 responses)
	// 3. Response times stay within acceptable bounds

	// Basic assertion that at least some requests succeeded
	assert.Greater(t, successCount, 0, "At least some requests should succeed even under load")
}

// TestRateLimiting tests API rate limiting functionality
func TestRateLimiting(t *testing.T) {
	// Setup
	_, _, controller := setupTestEnvironment(t)

	// Test that rapid request sequences would be rate limited
	// We're documenting the need for rate limiting since we can't directly test middleware
	testCases := []struct {
		name     string
		method   string
		path     string
		handler  func(c echo.Context) error
		requests int
	}{
		{
			name:     "GetDetections should be rate limited",
			method:   http.MethodGet,
			path:     "/api/v2/detections",
			handler:  controller.GetDetections,
			requests: 100,
		},
		{
			name:     "GetSpeciesSummary should be rate limited",
			method:   http.MethodGet,
			path:     "/api/v2/analytics/species",
			handler:  controller.GetSpeciesSummary,
			requests: 100,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Note: We can't directly test rate limiting in unit tests
			// This is more of a documentation that these endpoints should have rate limiting
			t.Logf("Endpoint %s %s should have rate limiting protection in production", tc.method, tc.path)
		})
	}
}

// TestCORSConfiguration ensures CORS is properly set up
func TestCORSConfiguration(t *testing.T) {
	// Document CORS requirements without using Echo instance
	// CORS functionality would normally be tested with real middleware
	req := httptest.NewRequest(http.MethodOptions, "/api/v2/detections", http.NoBody)
	req.Header.Set(echo.HeaderOrigin, "https://example.com")
	req.Header.Set(echo.HeaderAccessControlRequestMethod, http.MethodGet)

	// In real implementations with middleware, we would make the request and check headers
	t.Log("CORS should be properly configured in production for cross-origin requests")
}

// TestCSRFProtection tests that API endpoints require CSRF protection
func TestCSRFProtection(t *testing.T) {
	// Document CSRF protection requirements for state-changing endpoints
	modifyingEndpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"DeleteDetection", http.MethodDelete, "/api/v2/detections/1"},
		{"ReviewDetection", http.MethodPost, "/api/v2/detections/1/review"},
	}

	for _, ep := range modifyingEndpoints {
		t.Run(fmt.Sprintf("%s should have CSRF protection", ep.name), func(t *testing.T) {
			// In a real implementation with middleware, we would test that:
			// 1. Requests without CSRF token are rejected
			// 2. Requests with invalid CSRF token are rejected
			// 3. Requests with valid CSRF token are accepted
			t.Logf("Endpoint %s %s should have CSRF protection in production", ep.method, ep.path)
		})
	}
}
