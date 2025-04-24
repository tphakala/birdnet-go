// media_test.go: Package api provides tests for API v2 media endpoints.

package api

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/httpcontroller/securefs"
)

// TestInitMediaRoutesRegistration tests that media routes are properly registered
func TestInitMediaRoutesRegistration(t *testing.T) {
	// Setup
	e, _, controller := setupTestEnvironment(t)

	// Re-initialize the routes to ensure a clean state
	controller.initMediaRoutes()

	// Get all routes from the Echo instance
	routes := e.Routes()

	// Define the media routes we expect to find
	expectedRoutes := map[string]bool{
		"GET /api/v2/media/audio/:filename":       false,
		"GET /api/v2/media/spectrogram/:filename": false,
	}

	// Check each route
	for _, r := range routes {
		routePath := r.Method + " " + r.Path
		if _, exists := expectedRoutes[routePath]; exists {
			expectedRoutes[routePath] = true
		}
	}

	// Verify that all expected routes were registered
	for route, found := range expectedRoutes {
		assert.True(t, found, "Media route not registered: %s", route)
	}
}

// TestParseRange tests the parseRange function
func TestParseRange(t *testing.T) {
	// Test file size
	size := int64(1000)

	// Test cases
	testCases := []struct {
		name          string
		rangeHeader   string
		expectedRange []httpRange
		expectedError string
	}{
		{
			name:        "Valid range: bytes=0-499",
			rangeHeader: "bytes=0-499",
			expectedRange: []httpRange{
				{start: 0, length: 500},
			},
			expectedError: "",
		},
		{
			name:        "Valid range: bytes=500-999",
			rangeHeader: "bytes=500-999",
			expectedRange: []httpRange{
				{start: 500, length: 500},
			},
			expectedError: "",
		},
		{
			name:        "Valid range: bytes=500-",
			rangeHeader: "bytes=500-",
			expectedRange: []httpRange{
				{start: 500, length: 500},
			},
			expectedError: "",
		},
		{
			name:        "Valid range: bytes=-500",
			rangeHeader: "bytes=-500",
			expectedRange: []httpRange{
				{start: 500, length: 500},
			},
			expectedError: "",
		},
		{
			name:          "Invalid range: no bytes= prefix",
			rangeHeader:   "0-499",
			expectedRange: nil,
			expectedError: "invalid range header format",
		},
		{
			name:          "Invalid range: empty",
			rangeHeader:   "bytes=",
			expectedRange: nil,
			expectedError: "no valid ranges found",
		},
		{
			name:          "Invalid range: bytes=abc-def",
			rangeHeader:   "bytes=abc-def",
			expectedRange: nil,
			expectedError: "invalid range format",
		},
		{
			name:          "Invalid range: bytes=1000-1500",
			rangeHeader:   "bytes=1000-1500",
			expectedRange: nil,
			expectedError: "no valid ranges found",
		},
		{
			name:        "Invalid range: bytes=-2000",
			rangeHeader: "bytes=-2000",
			expectedRange: []httpRange{
				{start: 0, length: 1000},
			},
			expectedError: "",
		},
		{
			name:        "Multiple ranges",
			rangeHeader: "bytes=0-499,600-699",
			expectedRange: []httpRange{
				{start: 0, length: 500},
				{start: 600, length: 100},
			},
			expectedError: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			ranges, err := parseRange(tc.rangeHeader, size)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedRange, ranges)
			}
		})
	}
}

// TestGetContentType tests the getContentType function
func TestGetContentType(t *testing.T) {
	testCases := []struct {
		filename     string
		expectedType string
	}{
		{"test.mp3", "audio/mpeg"},
		{"test.wav", "audio/wav"},
		{"test.ogg", "audio/ogg"},
		{"test.flac", "audio/flac"},
		{"test.MP3", "audio/mpeg"},
		{"test.unknown", "application/octet-stream"},
		{"test", "application/octet-stream"},
	}

	for _, tc := range testCases {
		t.Run(tc.filename, func(t *testing.T) {
			contentType := getContentType(tc.filename)
			assert.Equal(t, tc.expectedType, contentType)
		})
	}
}

// TestServeAudioClip tests the ServeAudioClip handler using SecureFS
func TestServeAudioClip(t *testing.T) {
	// Setup test environment with SecureFS rooted in tempDir
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a small test audio file within the secure root
	smallFilename := "small.mp3"
	smallFilePath := filepath.Join(tempDir, smallFilename)
	err := os.WriteFile(smallFilePath, []byte("small audio file content"), 0o644)
	require.NoError(t, err)

	// Create a large test audio file (over 1MB)
	largeFilename := "large.mp3"
	largeFilePath := filepath.Join(tempDir, largeFilename)
	largeContent := make([]byte, 1100*1024) // 1.1 MB
	for i := range largeContent {
		largeContent[i] = byte(i % 256)
	}
	err = os.WriteFile(largeFilePath, largeContent, 0o644)
	require.NoError(t, err)

	// Test cases
	testCases := []struct {
		name           string
		filename       string // Filename relative to SecureFS root
		rangeHeader    string
		expectedStatus int
		expectedLength int64 // Use int64 for http.ServeContent
		partialContent bool
	}{
		{
			name:           "Small file - full content",
			filename:       smallFilename,
			rangeHeader:    "",
			expectedStatus: http.StatusOK,
			expectedLength: int64(len("small audio file content")),
			partialContent: false,
		},
		{
			name:           "Large file - full content",
			filename:       largeFilename,
			rangeHeader:    "",
			expectedStatus: http.StatusOK,
			expectedLength: 1100 * 1024,
			partialContent: false,
		},
		{
			name:           "Large file - partial content",
			filename:       largeFilename,
			rangeHeader:    "bytes=100-199",
			expectedStatus: http.StatusPartialContent,
			expectedLength: 100,
			partialContent: true,
		},
		{
			name:           "Non-existent file",
			filename:       "nonexistent.mp3",
			rangeHeader:    "",
			expectedStatus: http.StatusNotFound,
			expectedLength: 0,
			partialContent: false,
		},
		{
			name:           "Invalid filename (traversal attempt)",
			filename:       "../../../etc/passwd",
			rangeHeader:    "",
			expectedStatus: http.StatusNotFound, // SecureFS prevents access, looks like not found
			expectedLength: 0,
			partialContent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+tc.filename, http.NoBody)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(tc.filename) // Pass the relative filename

			// Call handler (now uses SecureFS.ServeFile implicitly)
			handlerErr := controller.ServeAudioClip(c)

			// Check response status code directly from recorder
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Handle expected errors from the handler itself (e.g., validation errors)
			if tc.expectedStatus >= 400 {
				// If ServeFile returns an error that echo converts to HTTPError
				if handlerErr != nil {
					assert.Error(t, handlerErr)
					// Use errors.As for robust error checking
					var httpErr *echo.HTTPError
					if errors.As(handlerErr, &httpErr) {
						assert.Equal(t, tc.expectedStatus, httpErr.Code)
					} // else, it might be an error that didn't translate to HTTP status, check rec.Code
				} // If handlerErr is nil, the error was handled internally (like NotFound)
			} else {
				// No error expected from the handler on success
				assert.NoError(t, handlerErr)
			}

			// For success cases, check headers and content
			if tc.expectedStatus == http.StatusOK || tc.expectedStatus == http.StatusPartialContent {
				if tc.partialContent {
					assert.Equal(t, fmt.Sprintf("%d", tc.expectedLength), rec.Header().Get("Content-Length"))
					assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
					assert.Contains(t, rec.Header().Get("Content-Range"), "bytes ")
				} else if tc.expectedStatus == http.StatusOK {
					// http.ServeContent sets Content-Length for full responses too
					assert.Equal(t, fmt.Sprintf("%d", tc.expectedLength), rec.Header().Get("Content-Length"))
					// Content verification for small file
					if tc.filename == smallFilename {
						assert.Equal(t, "small audio file content", rec.Body.String())
					}
				}
			}
		})
	}
}

// TestServeSpectrogram tests the ServeSpectrogram handler using SecureFS
// Note: This test verifies the handler logic calls SecureFS, but does not
// guarantee actual spectrogram generation works if tools are missing.
func TestServeSpectrogram(t *testing.T) {
	// Setup test environment with SecureFS rooted in tempDir
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file within the secure root
	audioFilename := "audio.mp3"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	err := os.WriteFile(audioFilePath, []byte("audio file content"), 0o644)
	require.NoError(t, err)

	// --- Simulate Spectrogram Generation (by creating the expected file) ---
	// This allows testing the "file exists" path without running external tools.
	spectrogramFilename := "audio_800.png"
	spectrogramFilePath := filepath.Join(tempDir, spectrogramFilename)
	spectrogramContent := "simulated spectrogram content"
	err = os.WriteFile(spectrogramFilePath, []byte(spectrogramContent), 0o644)
	require.NoError(t, err)

	// Test cases
	testCases := []struct {
		name           string
		filename       string // Filename relative to SecureFS root
		width          string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "Spectrogram generated/exists",
			filename:       audioFilename,
			width:          "800",
			expectedStatus: http.StatusOK,
			expectedBody:   spectrogramContent,
		},
		{
			name:     "Spectrogram needs generation (file doesn't exist initially)",
			filename: audioFilename,
			width:    "1200", // Different width means different file
			// Expect error because external tools likely won't run in test
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "",
		},
		{
			name:           "Non-existent audio file",
			filename:       "nonexistent.mp3",
			width:          "800",
			expectedStatus: http.StatusNotFound, // SecureFS.GenerateSpectrogram should return ErrNotExist
			expectedBody:   "",
		},
		{
			name:           "Invalid filename (traversal attempt)",
			filename:       "../../../etc/passwd",
			width:          "800",
			expectedStatus: http.StatusNotFound, // SecureFS prevents access
			expectedBody:   "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			url := "/api/v2/media/spectrogram/" + tc.filename
			if tc.width != "" {
				url += "?width=" + tc.width
			}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(tc.filename)

			// Call handler (uses SecureFS implicitly)
			_ = controller.ServeSpectrogram(c) // Ignore handler error for now, check status code

			// Check response status
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// For success cases, check response content
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedBody, rec.Body.String())
				assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
				assert.Contains(t, rec.Header().Get("Content-Disposition"), "inline; filename=\"audio_800.png\"")
			}
		})
	}
}

// Setup function to create a test environment with SecureFS
func setupMediaTestEnvironment(t *testing.T) (*echo.Echo, *Controller, string) {
	t.Helper()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "media_env_test")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	// Use the standard test setup which now initializes SFS
	// We need the controller instance to reconfigure its SFS
	e, _, controller := setupTestEnvironment(t)

	// --- Crucially: Re-initialize SFS in the controller to use the *media test* tempDir ---
	// Close the SFS created by setupTestEnvironment (if any)
	if controller.SFS != nil {
		controller.SFS.Close()
	}
	// Create and assign the new SFS rooted in our tempDir
	newSFS, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS for media test environment")
	controller.SFS = newSFS
	t.Cleanup(func() { controller.SFS.Close() }) // Ensure this one is closed too

	// Assign the tempDir to settings just in case any *other* part relies on it
	// (though SecureFS should make this less necessary)
	controller.Settings.Realtime.Audio.Export.Path = tempDir

	// Initialize media routes on the controller instance that has the correct SFS
	controller.initMediaRoutes()

	// Return the Echo instance, the *correctly configured* controller, and the tempDir path
	return e, controller, tempDir
}

// TestMediaEndpointsIntegration tests the media endpoints in an integrated way
func TestMediaEndpointsIntegration(t *testing.T) {
	// Setup test environment (already configures SecureFS)
	// We need the echo instance for the test server
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create test files within the SecureFS root
	audioFilename := "test.mp3"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	err := os.WriteFile(audioFilePath, []byte("test audio content"), 0o644)
	require.NoError(t, err)

	// Simulate existing spectrogram
	spectrogramFilename := "test_800.png"
	spectrogramFilePath := filepath.Join(tempDir, spectrogramFilename)
	spectrogramContent := "test spectrogram content"
	err = os.WriteFile(spectrogramFilePath, []byte(spectrogramContent), 0o644)
	require.NoError(t, err)

	// Create a real HTTP server using the Echo instance from setup
	server := httptest.NewServer(e)
	defer server.Close()

	client := server.Client()

	// Test cases
	testCases := []struct {
		name           string
		endpoint       string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "Audio endpoint",
			endpoint:       "/api/v2/media/audio/" + audioFilename,
			expectedStatus: http.StatusOK,
			expectedBody:   "test audio content",
		},
		{
			name:           "Spectrogram endpoint (existing)",
			endpoint:       "/api/v2/media/spectrogram/" + audioFilename + "?width=800",
			expectedStatus: http.StatusOK,
			expectedBody:   spectrogramContent,
		},
		{
			name:           "Spectrogram endpoint (needs generation - likely fails)",
			endpoint:       "/api/v2/media/spectrogram/" + audioFilename + "?width=1200",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "",
		},
		{
			name:           "Missing audio file",
			endpoint:       "/api/v2/media/audio/missing.mp3",
			expectedStatus: http.StatusNotFound,
			expectedBody:   "",
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Make request to the HTTP server
			resp, err := client.Get(server.URL + tc.endpoint)
			assert.NoError(t, err)
			defer resp.Body.Close()

			// Check status code
			assert.Equal(t, tc.expectedStatus, resp.StatusCode)

			// Check response body for successful requests
			if tc.expectedStatus == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				assert.NoError(t, err)
				assert.Equal(t, tc.expectedBody, string(body))
			}
		})
	}
}

// TestMediaSecurityScenarios tests various security scenarios using SecureFS
func TestMediaSecurityScenarios(t *testing.T) {
	// Setup test environment (SecureFS handles security)
	// We need the echo instance to serve requests directly
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file within the secure root
	secureFilename := "secure.mp3"
	secureFilePath := filepath.Join(tempDir, secureFilename)
	err := os.WriteFile(secureFilePath, []byte("secure audio content"), 0o644)
	require.NoError(t, err)

	// --- No need to create sensitive file outside tempDir, SecureFS prevents access ---

	// Path traversal attempts
	// SecureFS should prevent access outside its root
	traversalAttempts := []string{
		"../../../etc/passwd",
		"..%2F..%2F..%2Fetc%2Fpasswd", // URL encoded
		"secure.mp3/../../../etc/passwd",
		// Filenames SecureFS might reject inherently (depending on OS, less likely with os.Root)
		"secure.mp3%00.jpg",          // Null byte
		"CON",                        // Windows reserved name
		"LPT1",                       // Windows reserved name
		"secure.mp3:Zone.Identifier", // Windows Alternate Data Stream (less likely relevant)
	}

	// Test each path traversal attempt
	for _, attempt := range traversalAttempts {
		t.Run("Path traversal: "+attempt, func(t *testing.T) {
			// Test against both endpoints
			endpoints := []string{
				"/api/v2/media/audio/" + attempt,
				"/api/v2/media/spectrogram/" + attempt + "?width=800",
			}

			for _, endpoint := range endpoints {
				t.Run(endpoint, func(t *testing.T) {
					req := httptest.NewRequest(http.MethodGet, endpoint, http.NoBody)
					rec := httptest.NewRecorder()
					// We use ServeHTTP directly to bypass echo's built-in path cleaning for testing raw paths
					e.ServeHTTP(rec, req)

					// SecureFS prevents access, usually resulting in a 404 Not Found
					// or potentially 500 if SecureFS returns an unexpected validation error
					assert.True(t, rec.Code == http.StatusNotFound || rec.Code == http.StatusInternalServerError || rec.Code == http.StatusBadRequest,
						"Expected 404/500/400 status code for security issue, got %d for %s", rec.Code, endpoint)

					// Response should not contain sensitive content (though we didn't create one)
					assert.NotContains(t, rec.Body.String(), "root:")
				})
			}
		})
	}
}

// TestRangeHeaderHandling tests how the server handles various Range header formats with SecureFS
func TestRangeHeaderHandling(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test file (1KB)
	filename := "rangetest.mp3"
	filePath := filepath.Join(tempDir, filename)
	fileContent := make([]byte, 1024)
	for i := range fileContent {
		fileContent[i] = byte(i % 256)
	}
	err := os.WriteFile(filePath, fileContent, 0o644)
	require.NoError(t, err)

	// Test cases for range headers (same as before, but now served via SecureFS -> http.ServeContent)
	testCases := []struct {
		name           string
		rangeHeader    string
		expectedStatus int
		validateFunc   func(*testing.T, *httptest.ResponseRecorder)
	}{
		{
			name:           "No range header",
			rangeHeader:    "",
			expectedStatus: http.StatusOK,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent, rec.Body.Bytes())
			},
		},
		{
			name:           "Range: bytes=0-99",
			rangeHeader:    "bytes=0-99",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent[0:100], rec.Body.Bytes())
				assert.Equal(t, "bytes 0-99/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Range: bytes=100-199",
			rangeHeader:    "bytes=100-199",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent[100:200], rec.Body.Bytes())
				assert.Equal(t, "bytes 100-199/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Range: bytes=-100",
			rangeHeader:    "bytes=-100",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent[924:1024], rec.Body.Bytes())
				assert.Equal(t, "bytes 924-1023/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Range: bytes=924-",
			rangeHeader:    "bytes=924-",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent[924:1024], rec.Body.Bytes())
				assert.Equal(t, "bytes 924-1023/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:        "Invalid range format (bytes=invalid)",
			rangeHeader: "bytes=invalid",
			// http.ServeContent might return OK or PartialContent depending on parsing
			// Let's expect OK as it likely ignores the invalid header
			expectedStatus: http.StatusOK,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent, rec.Body.Bytes())
			},
		},
		{
			name:           "Invalid range (out of bounds)",
			rangeHeader:    "bytes=2000-",
			expectedStatus: http.StatusRequestedRangeNotSatisfiable,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				// No body validation needed
			},
		},
		{
			name:        "Multiple ranges - should be handled by http.ServeContent",
			rangeHeader: "bytes=0-99,200-299",
			// http.ServeContent typically serves only the *first* valid range if multiple are requested.
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				assert.Equal(t, fileContent[0:100], rec.Body.Bytes())
				assert.Equal(t, "bytes 0-99/1024", rec.Header().Get("Content-Range"))
				// Ensure Content-Type is NOT multipart
				assert.NotContains(t, rec.Header().Get("Content-Type"), "multipart")
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+filename, http.NoBody)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(filename)

			// Call handler (which uses SecureFS.ServeFile -> http.ServeContent)
			handlerErr := controller.ServeAudioClip(c)

			// Check status code directly from recorder
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Handle potential handler errors (less likely now with http.ServeContent)
			if tc.expectedStatus >= 400 {
				if handlerErr != nil {
					assert.Error(t, handlerErr)
					// Use errors.As for robust error checking
					var httpErr *echo.HTTPError
					if errors.As(handlerErr, &httpErr) {
						assert.Equal(t, tc.expectedStatus, httpErr.Code)
					}
				}
			} else {
				// Expect no error from the handler itself on success
				assert.NoError(t, handlerErr)
			}

			// Run validation function for successful responses
			if rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent {
				tc.validateFunc(t, rec)
			}
		})
	}
}

// TestServeAudioClipWithUnicodeFilenames tests handling of Unicode filenames with SecureFS
func TestServeAudioClipWithUnicodeFilenames(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create test files with Unicode filenames
	unicodeNames := []string{
		"тест.mp3",    // Cyrillic
		"테스트.mp3",     // Korean
		"測試.mp3",      // Chinese
		"Prüfung.mp3", // German with umlaut
		"ファイル.mp3",    // Japanese
		"αρχείο.mp3",  // Greek
	}

	for _, name := range unicodeNames {
		// Use filename directly relative to tempDir (SecureFS root)
		filePath := filepath.Join(tempDir, name)
		err := os.WriteFile(filePath, []byte("unicode audio content"), 0o644)
		require.NoError(t, err)
	}

	// Test each Unicode filename
	for _, name := range unicodeNames {
		t.Run("Unicode filename: "+name, func(t *testing.T) {
			// Create request
			// Need to URL-encode the filename for the request path
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+name, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			// Echo automatically decodes path parameters, so pass the raw name here
			c.SetParamValues(name)

			// Call handler (uses SecureFS.ServeFile)
			handlerErr := controller.ServeAudioClip(c)

			// Check for handler error
			if handlerErr != nil {
				t.Fatalf("Error serving Unicode filename %s: %v", name, handlerErr)
			}

			// Check response
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "unicode audio content", rec.Body.String())
		})
	}
}
