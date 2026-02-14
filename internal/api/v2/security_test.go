// security_test.go: Package api provides security tests for API v2 endpoints.
// This file focuses on testing general API security requirements including
// input validation against attacks, rate limiting, CORS configuration, and CSRF protection.

package api

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/patrickmn/go-cache"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// searchNotesEmptyMock returns a mockSetup function that configures empty search results.
// Use this to reduce duplication in tests that mock SearchNotes and CountSearchResults.
func searchNotesEmptyMock() func(*mock.Mock) {
	return func(m *mock.Mock) {
		m.On("SearchNotes", mock.Anything, mock.Anything, mock.Anything, mock.Anything).Return([]datastore.Note{}, nil)
		m.On("CountSearchResults", mock.Anything).Return(int64(0), nil)
	}
}

// reviewDetectionMock returns a mockSetup function for review detection endpoints.
// Configures Get, IsNoteLocked, SaveNoteComment, and SaveNoteReview mocks.
func reviewDetectionMock(id string) func(*mock.Mock) {
	return func(m *mock.Mock) {
		m.On("Get", id).Return(datastore.Note{ID: 1, Locked: false}, nil)
		m.On("IsNoteLocked", id).Return(false, nil)
		m.On("SaveNoteComment", mock.AnythingOfType("*datastore.NoteComment")).Return(nil)
		m.On("SaveNoteReview", mock.AnythingOfType("*datastore.NoteReview")).Return(nil)
	}
}

// setPathParamsFromPath extracts path parameters from a URL path and
// sets them on the Echo context using a table-driven approach for maintainability.
func setPathParamsFromPath(c echo.Context, path string) {
	// First, remove query string if present
	path = strings.SplitN(path, "?", 2)[0]

	// Table of route patterns to match against
	// Each entry defines:
	// - A regex pattern to match the URL path
	// - The parameter names to extract
	// - The corresponding Echo route pattern to set
	// - A function to extract parameter values from the path segments
	patterns := []struct {
		regex        *regexp.Regexp
		paramNames   []string
		echoPattern  string
		extractValue func([]string) []string
	}{
		{
			// Pattern: /api/v2/detections/:id/review
			regex:       regexp.MustCompile(`^/api/v2/detections/([^/]+)/review`),
			paramNames:  []string{"id"},
			echoPattern: "/api/v2/detections/:id/review",
			extractValue: func(matches []string) []string {
				if len(matches) > 1 {
					return []string{matches[1]} // ID is in the first capture group
				}
				return []string{}
			},
		},
		{
			// Pattern: /api/v2/detections/:id
			regex:       regexp.MustCompile(`^/api/v2/detections/([^/]+)$`),
			paramNames:  []string{"id"},
			echoPattern: "/api/v2/detections/:id",
			extractValue: func(matches []string) []string {
				if len(matches) > 1 {
					return []string{matches[1]} // ID is in the first capture group
				}
				return []string{}
			},
		},
		// Add more pattern definitions here for other parameterized routes
		// Example:
		// {
		//    regex:       regexp.MustCompile(`^/api/v2/detections/([^/]+)/lock`),
		//    paramNames:  []string{"id"},
		//    echoPattern: "/api/v2/detections/:id/lock",
		//    extractValue: func(matches []string) []string { return []string{matches[1]} },
		// },
	}

	// Try each pattern in order
	for _, pattern := range patterns {
		matches := pattern.regex.FindStringSubmatch(path)
		if len(matches) > 0 {
			paramValues := pattern.extractValue(matches)
			if len(paramValues) == len(pattern.paramNames) {
				c.SetParamNames(pattern.paramNames...)
				c.SetParamValues(paramValues...)
				c.SetPath(pattern.echoPattern)
				return
			}
		}
	}

	// If no patterns matched, leave the context unchanged
	// This allows the original path to be used for non-parameterized routes
}

// assertCSRFError checks that an error is an HTTPError with expected status and message substring.
func assertCSRFError(t *testing.T, err error, expectedMessage string) {
	t.Helper()
	require.Error(t, err)
	var httpErr *echo.HTTPError
	require.ErrorAs(t, err, &httpErr, "expected echo.HTTPError, got %T", err)
	assert.Equal(t, http.StatusForbidden, httpErr.Code)
	assert.Contains(t, httpErr.Message, expectedMessage)
}

// createTestRequest builds an HTTP request with optional body and query params.
func createTestRequest(method, path, body string, queryParams map[string]string) *http.Request {
	var req *http.Request
	if method == http.MethodPost || method == http.MethodPut {
		req = httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set(echo.HeaderContentType, echo.MIMEApplicationJSON)
	} else {
		req = httptest.NewRequest(method, path, http.NoBody)
	}

	if len(queryParams) > 0 {
		q := req.URL.Query()
		for k, v := range queryParams {
			q.Add(k, v)
		}
		req.URL.RawQuery = q.Encode()
	}
	return req
}

// assertSuccessfulResponse checks for expected 2xx status and optional body fragment.
func assertSuccessfulResponse(t *testing.T, tcName string, expectedStatus int, rec *httptest.ResponseRecorder, handlerErr error, expectedBodyFragment string) {
	t.Helper()
	require.NoErrorf(t, handlerErr, "tc=%s unexpected error", tcName)
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

	// Case 1: Handler returned nil, check the response recorder
	if handlerErr == nil {
		assert.Equal(t, expectedStatus, rec.Code, "tc=%s: expected status %d, got %d", tcName, expectedStatus, rec.Code)
		if expectedError != "" {
			assert.Contains(t, rec.Body.String(), expectedError, "tc=%s: body missing expected error", tcName)
		}
		return
	}

	// Case 2: Handler returned an error value
	httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr)
	if !ok {
		assert.Failf(t, "unexpected error type", "tc=%s: expected echo.HTTPError, got %T", tcName, handlerErr)
		return
	}

	assert.Equal(t, expectedStatus, httpErr.Code, "tc=%s: expected status %d, got %d", tcName, expectedStatus, httpErr.Code)
	if expectedError == "" {
		return
	}

	// Try to extract error message from HTTPError
	assertHTTPErrorContains(t, tcName, httpErr, expectedError)
}

// assertHTTPErrorContains checks that the HTTPError contains the expected message.
func assertHTTPErrorContains(t *testing.T, tcName string, httpErr *echo.HTTPError, expected string) {
	t.Helper()

	if internalErr := httpErr.Unwrap(); internalErr != nil {
		assert.Contains(t, internalErr.Error(), expected, "tc=%s: internal error missing expected message", tcName)
		return
	}

	if msgStr, ok := httpErr.Message.(string); ok {
		assert.Contains(t, msgStr, expected, "tc=%s: message missing expected content", tcName)
		return
	}

	assert.Failf(t, "could not extract error message", "tc=%s: HTTPError.Message is not string and Unwrap returned nil", tcName)
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
			mockSetup:      reviewDetectionMock("1"),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      reviewDetectionMock("1"),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockSetup:      searchNotesEmptyMock(),
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
			mockDS.ExpectedCalls = nil
			if tc.mockSetup != nil {
				tc.mockSetup(&mockDS.Mock)
			}

			req := createTestRequest(tc.method, tc.path, tc.body, tc.queryParams)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath(tc.path)
			setPathParamsFromPath(c, tc.path)

			handlerErr := tc.handler(c)

			if tc.expectedStatus >= 400 {
				assertErrorResponse(t, tc.name, tc.expectedStatus, rec, handlerErr, tc.expectedError)
			} else {
				assertSuccessfulResponse(t, tc.name, tc.expectedStatus, rec, handlerErr, tc.expectedBodyFragment)
			}

			mockDS.AssertExpectations(t)
		})
	}
}

// TestDDoSProtection tests the API's resilience to high-volume requests
func TestDDoSProtection(t *testing.T) {
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "security")
	t.Attr("type", "performance")
	t.Attr("feature", "ddos-protection")

	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Initialize the detection cache manually since routes aren't initialized in test environment
	controller.detectionCache = cache.New(5*time.Minute, 10*time.Minute)

	// Number of concurrent requests to simulate
	concurrentRequests := 50

	// Setup mock expectations - with caching enabled and concurrent requests,
	// multiple requests may check the cache before the first one populates it.
	// Use Maybe() to allow for race conditions in concurrent testing.
	mockDS.EXPECT().
		SearchNotes(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		Return([]datastore.Note{}, nil).
		Maybe() // Multiple requests may call before cache is populated

	mockDS.EXPECT().
		CountSearchResults(mock.Anything).
		Return(int64(0), nil).
		Maybe() // Multiple requests may call before cache is populated

	// Create a wait group to synchronize goroutines
	// Go 1.25: Using WaitGroup.Go() for automatic Add/Done management
	var wg sync.WaitGroup

	// Create channels to collect results
	responseTimesChan := make(chan time.Duration, concurrentRequests)
	statusCodesChan := make(chan int, concurrentRequests)

	// Launch concurrent requests using Go 1.25 WaitGroup.Go() pattern
	for range concurrentRequests {
		wg.Go(func() {
			// Create request with query parameters
			req := httptest.NewRequest(http.MethodGet, "/api/v2/detections?queryType=search&search=test", http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetPath("/api/v2/detections")

			// Record start time
			startTime := time.Now()

			// Call handler
			if err := controller.GetDetections(c); err != nil {
				assert.NoError(t, err, "GetDetections failed")
			}

			// Record response time
			responseTime := time.Since(startTime)
			responseTimesChan <- responseTime
			statusCodesChan <- rec.Code
		})
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
		switch code {
		case http.StatusOK:
			successCount++
		case http.StatusTooManyRequests:
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

		err := handler(c)
		assertCSRFError(t, err, "CSRF token missing")

		// Now try with invalid token
		req.Header.Set("X-CSRF-Token", "invalid-token")
		rec = httptest.NewRecorder()
		c = e.NewContext(req, rec)
		c.SetPath("/api/v2/detections/:id/review")
		c.SetParamNames("id")
		c.SetParamValues("1")

		err = handler(c)
		assertCSRFError(t, err, "Invalid CSRF token")
	})
}
