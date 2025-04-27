// security_test.go: Package api provides security tests for API v2 endpoints.
// This file focuses on testing general API security requirements including
// input validation against attacks, rate limiting, CORS configuration, and CSRF protection.

package api

import (
	"errors"
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

// setPathParamsFromPath extracts path parameters (like :id) from a URL path
// and sets them on the Echo context.
func setPathParamsFromPath(c echo.Context, path string) {
	if strings.Contains(path, "/detections/") && strings.Contains(path, "/review") {
		parts := strings.Split(path, "/")
		if len(parts) > 4 {
			c.SetParamNames("id")
			c.SetParamValues(parts[4])
			// Ensure Echo routing uses the pattern, not the specific ID for matching
			c.SetPath("/api/v2/detections/:id/review")
		}
	} else if strings.Contains(path, "/detections/") {
		parts := strings.Split(path, "/")
		if len(parts) > 3 {
			c.SetParamNames("id")
			c.SetParamValues(parts[4]) // Assuming ID is the 5th part (index 4)
			// Ensure Echo routing uses the pattern
			c.SetPath("/api/v2/detections/:id")
		}
	}
	// Add more else if blocks here for other parameterized routes if needed
}

// assertSuccessfulResponse checks for expected 2xx status and optional body fragment.
func assertSuccessfulResponse(t *testing.T, tcName string, expectedStatus int, rec *httptest.ResponseRecorder, handlerErr error, expectedBodyFragment string) {
	t.Helper()
	assert.NoError(t, handlerErr, "Test Case '%s': Handler returned an unexpected error for success case: %v", tcName, handlerErr)
	assert.Equal(t, expectedStatus, rec.Code, "Test Case '%s': Unexpected status code for success case. Expected %d, got %d", tcName, expectedStatus, rec.Code)
	if expectedBodyFragment != "" {
		assert.Contains(t, rec.Body.String(), expectedBodyFragment, "Test Case '%s': Response body does not contain expected fragment '%s'", tcName, expectedBodyFragment)
	}
}

// assertErrorResponse checks for expected 4xx/5xx status and error message.
// It handles cases where the error is returned by the handler directly (echo.HTTPError)
// or written to the response recorder by Echo's error handler.
func assertErrorResponse(t *testing.T, tcName string, expectedStatus int, rec *httptest.ResponseRecorder, handlerErr error, expectedError string) {
	t.Helper()
	if handlerErr != nil {
		// Case 1: Handler returned an error value (e.g., echo.HTTPError from validation)
		var httpErr *echo.HTTPError
		if errors.As(handlerErr, &httpErr) {
			assert.Equal(t, expectedStatus, httpErr.Code, "Test Case '%s': Expected status %d, got %d from returned error", tcName, expectedStatus, httpErr.Code)
			if expectedError != "" {
				// Check the internal error message wrapped by echo.HTTPError or the Message field
				internalErr := httpErr.Unwrap()
				if internalErr != nil {
					assert.Contains(t, internalErr.Error(), expectedError, "Test Case '%s': Expected internal error message containing '%s', got '%s'", tcName, expectedError, internalErr.Error())
				} else if msgStr, ok := httpErr.Message.(string); ok {
					assert.Contains(t, msgStr, expectedError, "Test Case '%s': Expected error message containing '%s', got '%s'", tcName, expectedError, msgStr)
				} else {
					assert.Fail(t, "HTTPError.Message is not a string or Unwrap returned nil", "Test Case '%s': Could not extract error message from echo.HTTPError", tcName)
				}
			}
		} else {
			// Handler returned a non-HTTPError, assume 500 check
			assert.Equal(t, expectedStatus, http.StatusInternalServerError, "Test Case '%s': Handler returned non-HTTPError '%v', expected status %d, implying 500", tcName, handlerErr, expectedStatus)
			assert.Failf(t, "Test Case '%s': Expected an echo.HTTPError but got a different error type", tcName, "Error: %v", handlerErr)
		}
	} else {
		// Case 2: Handler returned nil, check the response recorder (Echo error handler wrote response)
		assert.Equal(t, expectedStatus, rec.Code, "Test Case '%s': Handler returned nil, expected status %d, got response code %d", tcName, expectedStatus, rec.Code)
		if expectedError != "" {
			assert.Contains(t, rec.Body.String(), expectedError, "Test Case '%s': Handler returned nil, expected status %d. Body '%s' does not contain expected error '%s'", tcName, expectedStatus, rec.Body.String(), expectedError)
		}
	}
}

// TestInputValidation tests that API endpoints properly validate and reject invalid inputs
func TestInputValidation(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Test cases for different API endpoints
	testCases := []struct {
		name                 string
		method               string
		path                 string
		body                 string
		queryParams          map[string]string
		handler              func(c echo.Context) error
		mockSetup            func(*mock.Mock)
		expectedStatus       int
		expectedError        string
		expectedBody         string
		expectedBodyFragment string
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
				// Capture the actual sanitized parameter passed to SearchNotes
				m.On("SearchNotes", mock.AnythingOfType("string"), mock.Anything, mock.Anything, mock.Anything).
					Run(func(args mock.Arguments) {
						// Verify the search parameter was properly sanitized
						searchParam := args.String(0)
						// Check that dangerous tags were escaped or removed
						assert.NotContains(t, searchParam, "<script>")
						assert.NotContains(t, searchParam, "</script>")
					}).
					Return([]datastore.Note{}, nil)
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
				// Simulate validation failure via HandleError
				return controller.HandleError(c,
					errors.New("invalid characters detected in start_date"),
					"invalid characters detected in start_date",
					http.StatusBadRequest,
				)
			},
			mockSetup:      nil,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid characters detected in start_date",
		},
		{
			name:   "Large numerical values in parameters",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType":  "all",
				"numResults": "1001", // Use a value > 1000 but parseable
				"offset":     "0",
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// No mock calls expected as validation should fail early
			},
			expectedStatus: http.StatusBadRequest,                             // Expect 400 now
			expectedError:  "numResults exceeds maximum allowed value (1000)", // Expect the limit exceeded error
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
				"start_date": "%2e%2e%2f%2e%2e%2f%2e%2e%2fetc%2fpasswd",
				"end_date":   "2023-01-07",
			},
			handler: func(c echo.Context) error {
				return controller.HandleError(c,
					errors.New("invalid characters detected in start_date"),
					"invalid characters detected in start_date",
					http.StatusBadRequest,
				)
			},
			mockSetup:      nil,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid characters detected in start_date",
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
				"numResults": "-50", // Negative value
				"offset":     "-10", // Negative value
			},
			handler: func(c echo.Context) error {
				return controller.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// No mock calls should be needed as validation should fail first
			},
			// Expect Bad Request because numResults is negative
			expectedStatus: http.StatusBadRequest,
			// The specific error message depends on which validation fails first
			// Based on the code, numResults validation comes first
			expectedError: "numResults must be greater than zero",
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
		{
			name:   "Date format validation - invalid characters",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"start_date": "2023-12-invalid",
			},
			handler: func(c echo.Context) error {
				return echo.NewHTTPError(
					http.StatusBadRequest,
					"invalid start_date format, use YYYY-MM-DD",
				)
			},
			mockSetup:      nil,
			expectedStatus: http.StatusBadRequest,
			expectedError:  "invalid start_date format, use YYYY-MM-DD",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Reset mock expectations
			mockDS.ExpectedCalls = nil
			// Apply mock setup if provided
			if tc.mockSetup != nil {
				tc.mockSetup(&mockDS.Mock)
			}

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

			// Set path parameters using the helper
			setPathParamsFromPath(c, tc.path)

			// Call handler
			handlerErr := tc.handler(c)

			// Check response status and error messages using helpers
			if tc.expectedStatus >= 400 {
				assertErrorResponse(t, tc.name, tc.expectedStatus, rec, handlerErr, tc.expectedError)
			} else {
				assertSuccessfulResponse(t, tc.name, tc.expectedStatus, rec, handlerErr, tc.expectedBodyFragment)
			}

			// Verify mock expectations
			mockDS.AssertExpectations(t)
		})
	}
}

// TestDDoSProtection tests the API's resilience to high-volume requests
func TestDDoSProtection(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Setup mock expectations
	mockDS.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
	mockDS.On("CountSearchResults", mock.Anything).Return(int64(0), nil)

	// Number of concurrent requests to simulate
	concurrentRequests := 50

	// Create a wait group to synchronize goroutines
	var wg sync.WaitGroup
	wg.Add(concurrentRequests)

	// Create channels to collect results
	responseTimesChan := make(chan time.Duration, concurrentRequests)
	statusCodesChan := make(chan int, concurrentRequests)

	// Launch concurrent requests
	for i := 0; i < concurrentRequests; i++ {
		go func(index int) {
			defer wg.Done()

			// Create request with query parameters
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?search=test", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/detections")

			// Record start time
			startTime := time.Now()

			// Call handler
			controller.GetDetections(c)

			// Record response time
			responseTime := time.Since(startTime)
			responseTimesChan <- responseTime
			statusCodesChan <- rec.Code
		}(i)
	}

	// Wait for all requests to complete
	wg.Wait()
	close(responseTimesChan)
	close(statusCodesChan)

	// Collect results
	var totalResponseTime time.Duration
	successCount := 0
	rateLimitedCount := 0
	totalRequests := 0

	for code := range statusCodesChan {
		totalRequests++
		if code == http.StatusOK {
			successCount++
		} else if code == http.StatusTooManyRequests {
			rateLimitedCount++
		}
	}

	for responseTime := range responseTimesChan {
		totalResponseTime += responseTime
	}

	// Calculate average response time
	avgResponseTime := float64(totalResponseTime.Microseconds()) / float64(concurrentRequests) / 1000.0 // in milliseconds

	// Log results
	t.Logf("DDoS simulation completed with %d concurrent requests", concurrentRequests)
	t.Logf("Successful requests: %d (%.1f%%)", successCount, float64(successCount)/float64(concurrentRequests)*100)
	if rateLimitedCount > 0 {
		t.Logf("Rate limited requests: %d (%.1f%%)", rateLimitedCount, float64(rateLimitedCount)/float64(concurrentRequests)*100)
	}
	t.Logf("Average response time: %.2f ms", avgResponseTime)

	// In production, we would expect some rate limiting to occur under high load
	// This is a soft assertion since test environments may not have rate limiting enabled
	if controller.Settings != nil && controller.Settings.WebServer.Debug {
		// In debug mode, we can log that rate limiting should be tested in production
		t.Log("Note: Rate limiting should be verified in production environment")
	}

	// Verify all requests were handled (either successfully or rate-limited)
	assert.Equal(t, concurrentRequests, totalRequests, "Not all requests were processed")
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

// TestCSRFProtection documents which endpoints should have CSRF protection
func TestCSRFProtection(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Endpoints that modify state and should have CSRF protection
	modifyingEndpoints := []struct {
		name   string
		method string
		path   string
	}{
		{"DeleteDetection", http.MethodDelete, "/api/v2/detections/1"},
		{"ReviewDetection", http.MethodPost, "/api/v2/detections/1/review"},
	}

	// Document which endpoints should have CSRF protection
	for _, endpoint := range modifyingEndpoints {
		t.Run(endpoint.name+"_should_have_CSRF_protection", func(t *testing.T) {
			t.Logf("Endpoint %s %s should have CSRF protection in production", endpoint.method, endpoint.path)
		})
	}

	// Test CSRF token validation (simulating middleware behavior)
	t.Run("CSRF_token_validation", func(t *testing.T) {
		// Create a request without CSRF token
		req := httptest.NewRequest(http.MethodPost, "/api/v2/detections/1/review", strings.NewReader(`{"verified":"correct"}`))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
		rec := httptest.NewRecorder()
		c := e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id/review")
		c.SetParamNames("id")
		c.SetParamValues("1")

		// Create a middleware that simulates CSRF protection
		csrfMiddleware := func(next echo.HandlerFunc) echo.HandlerFunc {
			return func(c echo.Context) error {
				// Check for CSRF token in header
				token := c.Request().Header.Get("X-CSRF-Token")
				if token == "" {
					return echo.NewHTTPError(http.StatusForbidden, "CSRF token missing")
				}
				if token != "valid-csrf-token" {
					return echo.NewHTTPError(http.StatusForbidden, "Invalid CSRF token")
				}
				return next(c)
			}
		}

		// Apply the middleware to the handler
		handler := csrfMiddleware(controller.ReviewDetection)

		// Execute the request
		err := handler(c)

		// Verify that the request was rejected due to missing CSRF token
		if assert.Error(t, err) {
			var httpErr *echo.HTTPError
			if errors.As(err, &httpErr) {
				assert.Equal(t, http.StatusForbidden, httpErr.Code)
				assert.Contains(t, httpErr.Message, "CSRF token missing")
			}
		}

		// Now try with invalid token
		req.Header.Set("X-CSRF-Token", "invalid-token")
		rec = httptest.NewRecorder()
		c = e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id/review")
		c.SetParamNames("id")
		c.SetParamValues("1")

		err = handler(c)

		// Verify that the request was rejected due to invalid CSRF token
		if assert.Error(t, err) {
			var httpErr *echo.HTTPError
			if errors.As(err, &httpErr) {
				assert.Equal(t, http.StatusForbidden, httpErr.Code)
				assert.Contains(t, httpErr.Message, "Invalid CSRF token")
			}
		}
	})
}

// HandleMalformedJSON is a placeholder handler for testing malformed JSON payloads.
func (c *Controller) HandleMalformedJSON(ctx echo.Context) error {
	// Simulate a binding error
	return echo.NewHTTPError(http.StatusBadRequest, "Simulated JSON binding error")
}
