// security_test.go: Package api provides security tests for API v2 endpoints.
// This file focuses on input validation against hostile input and on the
// detection cache's behaviour under concurrent identical requests.

package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// concurrencyWaitTimeout bounds how long a test waits for its concurrent
// callers to reach the expected state. It is generous so a loaded CI runner
// under -race does not fail the test; a correct run finishes in milliseconds.
const concurrencyWaitTimeout = time.Minute

// defaultDetectionPageSize is the page size GetDetections uses when the request
// carries no numResults (defaultNumResults in the detections package).
const defaultDetectionPageSize = 100

// searchNotesExactMock returns a mockSetup function that expects exactly one
// SearchNotes call carrying term unchanged, with the default first page. The
// detections handler trims the term and passes it on without escaping.
func searchNotesExactMock(term string) func(*mock.Mock) {
	return func(m *mock.Mock) {
		m.On("SearchNotes", term, false, defaultDetectionPageSize, 0).
			Return([]datastore.Note{}, int64(0), nil).Once()
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
				return controller.detections.GetDetection(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Setup all possible method calls
				m.On("Get", mock.Anything).Return(datastore.Note{}, errors.NewStd("not found"))
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
				"search":    "<script>alert('XSS')</script>",
			},
			handler: func(c echo.Context) error {
				return controller.detections.GetDetections(c)
			},
			mockSetup:      searchNotesExactMock("<script>alert('XSS')</script>"),
			expectedStatus: http.StatusOK,
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
				return controller.detections.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// No mock calls expected as validation should fail early
			},
			expectedStatus: http.StatusBadRequest,                // Expect 400 now
			expectedError:  "Invalid detection query parameters", // Generic safe error message
		},
		{
			name:   "JSON injection in review body",
			method: http.MethodPost,
			path:   "/api/v2/detections/1/review",
			body:   `{"verified": "correct", "comment": "}\n{\"malicious\":true"}`,
			handler: func(c echo.Context) error {
				return controller.detections.ReviewDetection(c)
			},
			mockSetup:      reviewDetectionMock("1"),
			expectedStatus: http.StatusOK,
		},
		// New security abuse test cases
		{
			name:   "Command injection attempt",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"search":    "bird; rm -rf /",
			},
			handler: func(c echo.Context) error {
				return controller.detections.GetDetections(c)
			},
			mockSetup:      searchNotesExactMock("bird; rm -rf /"),
			expectedStatus: http.StatusOK,
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
				return controller.detections.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Only mock what's actually being called
				m.On("SearchNotes", "", false, defaultDetectionPageSize, 0).Return([]datastore.Note{}, int64(0), nil)
			},
			expectedStatus: http.StatusOK,
		},
		{
			name:   "Malformed JSON payload",
			method: http.MethodPost,
			path:   "/api/v2/detections/1/review",
			body:   `{"verified": "correct", "comment": "test"`, // Missing closing brace
			handler: func(c echo.Context) error {
				return controller.detections.ReviewDetection(c)
			},
			mockSetup: func(m *mock.Mock) {
				// Need to mock Get since it's called before JSON validation
				m.On("Get", "1").Return(datastore.Note{ID: 1, Locked: false}, nil)
				m.On("IsNoteLocked", "1").Return(false, nil)
			},
			expectedStatus: http.StatusBadRequest,
			expectedError:  "Invalid request format",
		},
		{
			name:   "NUL byte in search text",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"search":    "bird\u0000.mp3", // NUL byte in the search text
			},
			handler: func(c echo.Context) error {
				return controller.detections.GetDetections(c)
			},
			mockSetup:      searchNotesExactMock("bird\x00.mp3"),
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
				return controller.detections.GetDetections(c)
			},
			mockSetup: func(m *mock.Mock) {
				// No mock calls should be needed as validation should fail first
			},
			// Expect Bad Request because numResults is negative
			expectedStatus: http.StatusBadRequest,
			// The specific error message depends on which validation fails first
			// Based on the code, numResults validation comes first
			expectedError: "Invalid detection query parameters",
		},
		// Advanced XSS test cases
		{
			name:   "XSS with attribute injection",
			method: http.MethodPost,
			path:   "/api/v2/detections/1/review",
			body:   `{"verified": "correct", "comment": "\" onmouseover=\"alert(1)"}`,
			handler: func(c echo.Context) error {
				return controller.detections.ReviewDetection(c)
			},
			mockSetup:      reviewDetectionMock("1"),
			expectedStatus: http.StatusOK,
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

	// Number of concurrent requests to simulate
	concurrentRequests := 50

	// Concurrent identical requests share one datastore load: a caller either
	// joins the load in flight or hits the cache once it is stored, so the
	// datastore is queried exactly once.
	// The datastore call blocks until every caller has missed the cache, so the
	// requests overlap instead of running one after another.
	release := make(chan struct{})
	var releaseOnce sync.Once
	t.Cleanup(func() { releaseOnce.Do(func() { close(release) }) })
	mockDS.EXPECT().
		SearchNotes(mock.Anything, mock.Anything, mock.Anything, mock.Anything).
		RunAndReturn(func(string, bool, int, int) ([]datastore.Note, int64, error) {
			<-release
			return []datastore.Note{}, 0, nil
		}).
		Once()

	// Create a wait group to synchronize goroutines
	// Go 1.25: Using WaitGroup.Go() for automatic Add/Done management
	var wg sync.WaitGroup

	// Create channels to collect results
	responseTimesChan := make(chan time.Duration, concurrentRequests)
	statusCodesChan := make(chan int, concurrentRequests)
	handlerErrsChan := make(chan error, concurrentRequests)

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
			handlerErrsChan <- controller.detections.GetDetections(c)

			// Record response time
			responseTime := time.Since(startTime)
			responseTimesChan <- responseTime
			statusCodesChan <- rec.Code
		})
	}

	require.Eventually(t, func() bool { return controller.DetectionCache.Stats().Misses == uint64(concurrentRequests) },
		concurrencyWaitTimeout, time.Millisecond, "every request reaches the cache before the load finishes")
	releaseOnce.Do(func() { close(release) })

	// Wait for all requests to complete
	wg.Wait()
	close(responseTimesChan)
	close(statusCodesChan)
	close(handlerErrsChan)

	for err := range handlerErrsChan {
		require.NoError(t, err, "GetDetections failed")
	}

	// Collect results
	var totalResponseTime time.Duration
	successCount := 0
	rateLimitedCount := 0

	for code := range statusCodesChan {
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
	if controller.Settings.Load() != nil && controller.Settings.Load().WebServer.Debug {
		// In debug mode, we can log that rate limiting should be tested in production
		t.Log("Note: Rate limiting should be verified in production environment")
	}

	// Every request must be answered with success or a rate-limit response: the
	// cache never turns a request away with any other status.
	assert.Equal(t, concurrentRequests, successCount+rateLimitedCount, "Not all requests were served or rate limited")
}
