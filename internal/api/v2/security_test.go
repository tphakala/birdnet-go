// security_test.go: Package api provides security tests for API v2 endpoints.
// This file focuses on input validation against hostile input.

package api

import (
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/mock"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
)

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

// TestInputValidation sends hostile input to the detection and review handlers and checks the status each answers with and, for search terms, that the text reaches the datastore unchanged.
func TestInputValidation(t *testing.T) {
	// Setup
	e, mockDS, controller := setupTestEnvironment(t)

	// Search terms that must reach the datastore exactly as sent.
	const (
		scriptTagTerm = "<script>alert('XSS')</script>"
		shellTerm     = "bird; rm -rf /"
		nulByteTerm   = "bird\x00.mp3"
	)

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
				"search":    scriptTagTerm,
			},
			handler: func(c echo.Context) error {
				return controller.detections.GetDetections(c)
			},
			mockSetup:      searchNotesExactMock(scriptTagTerm),
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
		{
			name:   "Command injection attempt",
			method: http.MethodGet,
			path:   "/api/v2/detections",
			queryParams: map[string]string{
				"queryType": "search",
				"search":    shellTerm,
			},
			handler: func(c echo.Context) error {
				return controller.detections.GetDetections(c)
			},
			mockSetup:      searchNotesExactMock(shellTerm),
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
			mockSetup:      searchNotesExactMock(""),
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
				"search":    nulByteTerm,
			},
			handler: func(c echo.Context) error {
				return controller.detections.GetDetections(c)
			},
			mockSetup:      searchNotesExactMock(nulByteTerm),
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
