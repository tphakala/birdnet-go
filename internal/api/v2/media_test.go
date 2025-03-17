// media_test.go: Package api provides tests for API v2 media endpoints.

package api

import (
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
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

// TestValidateMediaPath tests the validateMediaPath function
func TestValidateMediaPath(t *testing.T) {
	// Create a temporary directory for testing
	tempDir, err := os.MkdirTemp("", "media_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Setup controller
	_, _, controller := setupTestEnvironment(t)

	// Create a non-existent path for testing directory creation
	nonExistentPath := filepath.Join(tempDir, "non_existent_dir")
	// Ensure it doesn't exist before the test
	_ = os.RemoveAll(nonExistentPath)

	// Test cases
	testCases := []struct {
		name             string
		exportPath       string
		filename         string
		expectedError    string
		checkDirCreation bool
	}{
		{
			name:             "Valid path",
			exportPath:       tempDir,
			filename:         "audio.mp3",
			expectedError:    "",
			checkDirCreation: false,
		},
		{
			name:             "Empty filename",
			exportPath:       tempDir,
			filename:         "",
			expectedError:    "empty filename",
			checkDirCreation: false,
		},
		{
			name:             "Invalid characters in filename",
			exportPath:       tempDir,
			filename:         "file/../with/invalid/chars.mp3",
			expectedError:    "invalid filename characters",
			checkDirCreation: false,
		},
		{
			name:             "Path traversal attempt",
			exportPath:       tempDir,
			filename:         "../../../etc/passwd",
			expectedError:    "invalid filename characters",
			checkDirCreation: false,
		},
		{
			name:             "Path traversal with encoded characters",
			exportPath:       tempDir,
			filename:         "%2e%2e%2f%2e%2e%2fetc%2fpasswd",
			expectedError:    "invalid filename characters",
			checkDirCreation: false,
		},
		{
			name:             "Non-existent export path should be created",
			exportPath:       nonExistentPath,
			filename:         "audio.mp3",
			expectedError:    "",
			checkDirCreation: true,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			path, err := controller.validateMediaPath(tc.exportPath, tc.filename)

			if tc.expectedError != "" {
				assert.Error(t, err)
				assert.Contains(t, err.Error(), tc.expectedError)
			} else {
				assert.NoError(t, err)
				expectedPath := filepath.Join(tc.exportPath, tc.filename)
				assert.Equal(t, expectedPath, path)

				// Check if directory was created when expected
				if tc.checkDirCreation {
					dirInfo, statErr := os.Stat(tc.exportPath)
					assert.NoError(t, statErr, "Export directory should have been created")
					assert.True(t, dirInfo.IsDir(), "Export path should be a directory")
				}
			}
		})
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

// TestServeAudioClip tests the ServeAudioClip handler
func TestServeAudioClip(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "audio_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a small test audio file
	smallFile := filepath.Join(tempDir, "small.mp3")
	err = os.WriteFile(smallFile, []byte("small audio file content"), 0o644)
	require.NoError(t, err)

	// Create a large test audio file (slightly over 1MB to trigger range handling)
	largeFile := filepath.Join(tempDir, "large.mp3")
	largeContent := make([]byte, 1100*1024) // 1.1 MB
	for i := range largeContent {
		largeContent[i] = byte(i % 256) // Fill with some pattern
	}
	err = os.WriteFile(largeFile, largeContent, 0o644)
	require.NoError(t, err)

	// Setup controller with the temp directory
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Realtime.Audio.Export.Path = tempDir

	// Test cases
	testCases := []struct {
		name           string
		filename       string
		rangeHeader    string
		expectedStatus int
		expectedLength int
		partialContent bool
	}{
		{
			name:           "Small file - full content",
			filename:       "small.mp3",
			rangeHeader:    "",
			expectedStatus: http.StatusOK,
			expectedLength: len("small audio file content"),
			partialContent: false,
		},
		{
			name:           "Large file - full content",
			filename:       "large.mp3",
			rangeHeader:    "",
			expectedStatus: http.StatusOK,
			expectedLength: 1100 * 1024,
			partialContent: false,
		},
		{
			name:           "Large file - partial content",
			filename:       "large.mp3",
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
			name:           "Invalid filename",
			filename:       "../../../etc/passwd",
			rangeHeader:    "",
			expectedStatus: http.StatusBadRequest,
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
			c.SetParamValues(tc.filename)

			// Call handler
			_ = controller.ServeAudioClip(c)

			// Check response status
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// For success cases, check response content
			if tc.expectedStatus == http.StatusOK || tc.expectedStatus == http.StatusPartialContent {
				// Check if we have the expected content length
				if tc.partialContent {
					assert.Equal(t, fmt.Sprintf("%d", tc.expectedLength), rec.Header().Get("Content-Length"))
					assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
					assert.Contains(t, rec.Header().Get("Content-Range"), "bytes ")
				} else if tc.expectedStatus == http.StatusOK && tc.filename == "small.mp3" {
					// For small files served directly, verify content
					assert.Equal(t, "small audio file content", rec.Body.String())
				}
			}
		})
	}
}

// TestServeSpectrogram tests the ServeSpectrogram handler
func TestServeSpectrogram(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "spectrogram_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test audio file
	audioFile := filepath.Join(tempDir, "audio.mp3")
	err = os.WriteFile(audioFile, []byte("audio file content"), 0o644)
	require.NoError(t, err)

	// Create a test spectrogram file
	spectrogramFile := filepath.Join(tempDir, "audio_800.png")
	err = os.WriteFile(spectrogramFile, []byte("spectrogram image content"), 0o644)
	require.NoError(t, err)

	// Setup controller with the temp directory
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Realtime.Audio.Export.Path = tempDir

	// Test cases
	testCases := []struct {
		name           string
		filename       string
		width          string
		expectedStatus int
		expectedResult string
	}{
		{
			name:           "Existing spectrogram",
			filename:       "audio.mp3",
			width:          "800",
			expectedStatus: http.StatusOK,
			expectedResult: "spectrogram image content",
		},
		{
			name:           "Non-existent spectrogram - should try to generate",
			filename:       "audio.mp3",
			width:          "1200",
			expectedStatus: http.StatusInternalServerError, // Generation is unimplemented
			expectedResult: "",
		},
		{
			name:           "Non-existent audio file",
			filename:       "nonexistent.mp3",
			width:          "800",
			expectedStatus: http.StatusNotFound,
			expectedResult: "",
		},
		{
			name:           "Invalid filename",
			filename:       "../../../etc/passwd",
			width:          "800",
			expectedStatus: http.StatusBadRequest,
			expectedResult: "",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/spectrogram/"+tc.filename, http.NoBody)
			if tc.width != "" {
				req.URL.RawQuery = fmt.Sprintf("width=%s", tc.width)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(tc.filename)

			// Call handler
			_ = controller.ServeSpectrogram(c)

			// Check response status
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// For success cases, check response content
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedResult, rec.Body.String())
			}
		})
	}
}

// TestGenerateSpectrogram tests the generateSpectrogram function
func TestGenerateSpectrogram(t *testing.T) {
	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "spectrogram_gen_test")
	require.NoError(t, err)
	defer os.RemoveAll(tempDir)

	// Create a test audio file
	audioFile := filepath.Join(tempDir, "audio.mp3")
	err = os.WriteFile(audioFile, []byte("audio file content"), 0o644)
	require.NoError(t, err)

	// Setup controller with the temp directory
	_, _, controller := setupTestEnvironment(t)
	controller.Settings.Realtime.Audio.Export.Path = tempDir

	// Test the function
	spectrogramPath, err := controller.generateSpectrogram(audioFile, 800)

	// Expect an error since generation is not implemented
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "not implemented yet")

	// The path should still be valid even though generation fails
	assert.Contains(t, spectrogramPath, "audio_800.png")
}

// Setup function to create a test environment with a file server
func setupMediaTestEnvironment(t *testing.T) (*echo.Echo, *Controller, string) {
	t.Helper()

	// Create a temporary directory for test files
	tempDir, err := os.MkdirTemp("", "media_env_test")
	require.NoError(t, err)
	t.Cleanup(func() { os.RemoveAll(tempDir) })

	// Setup controller
	e, _, controller := setupTestEnvironment(t)
	controller.Settings.Realtime.Audio.Export.Path = tempDir

	// Initialize routes
	controller.initMediaRoutes()

	return e, controller, tempDir
}

// TestMediaEndpointsIntegration tests the media endpoints in an integrated way
func TestMediaEndpointsIntegration(t *testing.T) {
	// Setup test environment
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create test files
	audioFile := filepath.Join(tempDir, "test.mp3")
	err := os.WriteFile(audioFile, []byte("test audio content"), 0o644)
	require.NoError(t, err)

	spectrogramFile := filepath.Join(tempDir, "test_800.png")
	err = os.WriteFile(spectrogramFile, []byte("test spectrogram content"), 0o644)
	require.NoError(t, err)

	// Create a real HTTP server and client for better integration testing
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
			endpoint:       "/api/v2/media/audio/test.mp3",
			expectedStatus: http.StatusOK,
			expectedBody:   "test audio content",
		},
		{
			name:           "Spectrogram endpoint",
			endpoint:       "/api/v2/media/spectrogram/test.mp3?width=800",
			expectedStatus: http.StatusOK,
			expectedBody:   "test spectrogram content",
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

// TestMediaSecurityScenarios tests various security scenarios for media endpoints
func TestMediaSecurityScenarios(t *testing.T) {
	// Setup test environment
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create test files
	audioFile := filepath.Join(tempDir, "secure.mp3")
	err := os.WriteFile(audioFile, []byte("secure audio content"), 0o644)
	require.NoError(t, err)

	// Create a "sensitive" file outside the media directory
	sensitiveDir, err := os.MkdirTemp("", "sensitive")
	require.NoError(t, err)
	defer os.RemoveAll(sensitiveDir)

	sensitiveFile := filepath.Join(sensitiveDir, "sensitive.txt")
	err = os.WriteFile(sensitiveFile, []byte("sensitive content"), 0o644)
	require.NoError(t, err)

	// Path traversal attempts - URL safe versions
	// Note: We need to be careful with semicolons and other special characters
	// that might be misinterpreted by the HTTP request parser
	traversalAttempts := []string{
		"../../../etc/passwd",
		"..%2F..%2F..%2Fetc%2Fpasswd",
		"secure.mp3/../../../etc/passwd",
		"secure.mp3%00.jpg",     // Null byte injection
		"secure.mp3%3Bls%20-la", // Command injection with URL-encoded semicolon and space
		"secure.mp3%5C",         // Backslash (URL-encoded)
		"secure.mp3%22",         // Quote (URL-encoded)
		"secure.mp3%27",         // Single quote (URL-encoded)
		"secure.mp3%24PATH",     // Environment variable (URL-encoded dollar sign)
	}

	// Test each path traversal attempt
	for _, attempt := range traversalAttempts {
		t.Run("Path traversal: "+attempt, func(t *testing.T) {
			// Test against both endpoints
			endpoints := []string{
				"/api/v2/media/audio/" + attempt,
				"/api/v2/media/spectrogram/" + attempt,
			}

			for _, endpoint := range endpoints {
				req := httptest.NewRequest(http.MethodGet, endpoint, http.NoBody)
				rec := httptest.NewRecorder()
				e.ServeHTTP(rec, req)

				// Should return 400 Bad Request for invalid filenames
				assert.True(t, rec.Code == http.StatusBadRequest || rec.Code == http.StatusNotFound,
					"Expected 400 or 404 status code for security issue, got %d for %s", rec.Code, endpoint)

				// Response should not contain any sensitive content
				assert.NotContains(t, rec.Body.String(), "sensitive content")
				assert.NotContains(t, rec.Body.String(), "root:")
			}
		})
	}
}

// TestRangeHeaderHandling tests how the server handles various Range header formats
func TestRangeHeaderHandling(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test file (1KB)
	testFile := filepath.Join(tempDir, "rangetest.mp3")
	fileContent := make([]byte, 1024)
	for i := range fileContent {
		fileContent[i] = byte(i % 256)
	}
	err := os.WriteFile(testFile, fileContent, 0o644)
	require.NoError(t, err)

	// Test cases for range headers
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
			name:           "Invalid range format",
			rangeHeader:    "bytes=invalid",
			expectedStatus: http.StatusRequestedRangeNotSatisfiable,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				// No content validation for 416 response
			},
		},
		{
			name:           "Multiple ranges - multipart response",
			rangeHeader:    "bytes=0-99,200-299",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				// For multipart responses, we need to check:
				// 1. Content-Type should be multipart/byteranges
				contentType := rec.Header().Get("Content-Type")
				assert.True(t, strings.Contains(contentType, "multipart/byteranges") ||
					strings.Contains(contentType, "multipart/mixed"),
					"Expected multipart content type, got: %s", contentType)

				// 2. Body should contain both ranges
				body := rec.Body.String()
				// Check for first range data
				assert.Contains(t, body, "Content-Range: bytes 0-99/1024")
				// Check for second range data
				assert.Contains(t, body, "Content-Range: bytes 200-299/1024")

				// 3. Body should contain actual range data
				// Convert the first few bytes of the first range to string for easier comparison
				firstRangeStart := string([]byte{0, 1, 2, 3, 4, 5})
				assert.Contains(t, body, firstRangeStart)

				// And a few bytes from the second range
				secondRangeStart := string([]byte{200 % 256, 201 % 256, 202 % 256})
				assert.Contains(t, body, secondRangeStart)
			},
		},
		{
			name:           "Multiple ranges with different order",
			rangeHeader:    "bytes=300-399,0-99",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				// For multipart responses, we need to check:
				// 1. Content-Type should be multipart/byteranges
				contentType := rec.Header().Get("Content-Type")
				assert.True(t, strings.Contains(contentType, "multipart/byteranges") ||
					strings.Contains(contentType, "multipart/mixed"),
					"Expected multipart content type, got: %s", contentType)

				// 2. Body should contain both ranges
				body := rec.Body.String()
				// Check for first range data
				assert.Contains(t, body, "Content-Range: bytes 300-399/1024")
				// Check for second range data
				assert.Contains(t, body, "Content-Range: bytes 0-99/1024")

				// 3. Body should contain actual range data
				// First range (300-399) should contain bytes with values 44, 45, 46...
				firstRangeStart := string([]byte{44, 45, 46})
				assert.Contains(t, body, firstRangeStart)

				// Second range (0-99) should contain 0, 1, 2...
				secondRangeStart := string([]byte{0, 1, 2})
				assert.Contains(t, body, secondRangeStart)
			},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/rangetest.mp3", http.NoBody)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues("rangetest.mp3")

			// Call handler
			err := controller.ServeAudioClip(c)
			require.NoError(t, err)

			// Check status code
			assert.Equal(t, tc.expectedStatus, rec.Code)

			// Run validation function for successful responses
			if rec.Code == http.StatusOK || rec.Code == http.StatusPartialContent {
				tc.validateFunc(t, rec)
			}
		})
	}
}

// TestServeAudioClipWithUnicodeFilenames tests handling of Unicode filenames
func TestServeAudioClipWithUnicodeFilenames(t *testing.T) {
	// Skip this test if Unicode filenames aren't enabled in safeFilenamePattern
	if !strings.Contains(safeFilenamePattern.String(), "\\p{L}") {
		t.Skip("Unicode filename pattern not enabled, skipping test")
	}

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
		filePath := filepath.Join(tempDir, name)
		err := os.WriteFile(filePath, []byte("unicode audio content"), 0o644)
		require.NoError(t, err)
	}

	// Test each Unicode filename
	for _, name := range unicodeNames {
		t.Run("Unicode filename: "+name, func(t *testing.T) {
			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+name, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(name)

			// Call handler
			err := controller.ServeAudioClip(c)

			// This might fail if Unicode filenames aren't supported by the OS or pattern
			if err != nil {
				t.Logf("Error handling Unicode filename %s: %v", name, err)
				return
			}

			// Check response
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, "unicode audio content", rec.Body.String())
		})
	}
}
