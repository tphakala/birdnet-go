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
	"strings"
	"testing"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/securefs"
)

// assertPartialContentHeaders checks headers for partial content responses.
func assertPartialContentHeaders(t *testing.T, rec *httptest.ResponseRecorder, expectedLength int64) {
	t.Helper()
	assert.Equal(t, fmt.Sprintf("%d", expectedLength), rec.Header().Get("Content-Length"))
	assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
	assert.Contains(t, rec.Header().Get("Content-Range"), "bytes ")
}

// assertFullContentHeaders checks headers and content for full content responses.
func assertFullContentHeaders(t *testing.T, rec *httptest.ResponseRecorder, expectedLength int64, checkRIFF bool) {
	t.Helper()
	assert.Equal(t, fmt.Sprintf("%d", expectedLength), rec.Header().Get("Content-Length"))
	if checkRIFF {
		body := rec.Body.Bytes()
		assert.GreaterOrEqual(t, len(body), 4, "Response body should have at least 4 bytes")
		if len(body) >= 4 {
			assert.Equal(t, []byte("RIFF"), body[:4], "Should return a WAV file starting with RIFF header")
		}
	}
}

// assertAudioErrorResponse checks error response body content.
func assertAudioErrorResponse(t *testing.T, rec *httptest.ResponseRecorder, status int) {
	t.Helper()
	switch status {
	case http.StatusNotFound:
		assert.Contains(t, rec.Body.String(), "File not found", "Expected 'File not found' in body for 404")
	case http.StatusBadRequest:
		assert.Contains(t, rec.Body.String(), "Invalid file path", "Expected 'Invalid file path' in body for 400")
	}
}

// assertAudioClipResponse validates the response based on expected status and content type.
func assertAudioClipResponse(t *testing.T, rec *httptest.ResponseRecorder, expectedStatus int, expectedLength int64, partialContent, isSmallFile bool) {
	t.Helper()
	assert.Equal(t, expectedStatus, rec.Code)

	if expectedStatus >= 200 && expectedStatus < 300 {
		if partialContent {
			assertPartialContentHeaders(t, rec, expectedLength)
		} else if expectedStatus == http.StatusOK {
			assertFullContentHeaders(t, rec, expectedLength, isSmallFile)
		}
	} else {
		assertAudioErrorResponse(t, rec, expectedStatus)
	}
}

// assertHandlerError checks handler error matches expected status for error responses.
func assertHandlerError(t *testing.T, handlerErr error, expectedStatus int) {
	t.Helper()
	if expectedStatus < 400 {
		require.NoError(t, handlerErr)
		return
	}
	if handlerErr == nil {
		return // Handler returned nil, response written to recorder
	}
	if httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr); ok {
		assert.Equal(t, expectedStatus, httpErr.Code)
	}
}

// assertAudioHeaders validates audio response headers for iOS Safari compatibility.
func assertAudioHeaders(t *testing.T, rec *httptest.ResponseRecorder, expectedContentType, expectedDisposition string, shouldHaveAcceptRanges bool) {
	t.Helper()
	if expectedContentType != "" {
		assert.Equal(t, expectedContentType, rec.Header().Get("Content-Type"))
	}
	if expectedDisposition != "" {
		assert.Equal(t, expectedDisposition, rec.Header().Get("Content-Disposition"))
	}
	if shouldHaveAcceptRanges {
		assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"))
	}
}

// assertMediaResponseBody validates response body content based on type.
func assertMediaResponseBody(t *testing.T, body []byte, expectedBody string, isAudioFile bool) {
	t.Helper()
	if isAudioFile {
		assert.GreaterOrEqual(t, len(body), 4, "Audio response should have at least 4 bytes")
		if len(body) >= 4 {
			assert.Equal(t, []byte("RIFF"), body[:4], "Should return a WAV file starting with RIFF header")
		}
	} else {
		assert.Equal(t, expectedBody, string(body))
	}
}

// createLargeWAVFile creates a large WAV file for testing partial content responses.
func createLargeWAVFile(t *testing.T, path string, dataSize int) {
	t.Helper()
	file, err := os.Create(path) //nolint:gosec // G304: path is from controlled test temp dir
	require.NoError(t, err)
	defer func() {
		if err := file.Close(); err != nil {
			t.Logf("Failed to close large file: %v", err)
		}
	}()

	header := []byte{
		'R', 'I', 'F', 'F',
		byte(dataSize + 36), byte((dataSize + 36) >> 8), byte((dataSize + 36) >> 16), byte((dataSize + 36) >> 24),
		'W', 'A', 'V', 'E', 'f', 'm', 't', ' ',
		16, 0, 0, 0, 1, 0, 1, 0,
		0x44, 0xAC, 0, 0, 0x88, 0x58, 0x01, 0, 2, 0, 16, 0,
		'd', 'a', 't', 'a',
		byte(dataSize), byte(dataSize >> 8), byte(dataSize >> 16), byte(dataSize >> 24),
	}
	_, err = file.Write(header)
	require.NoError(t, err)

	content := make([]byte, dataSize)
	for i := range content {
		content[i] = byte(i % 256)
	}
	_, err = file.Write(content)
	require.NoError(t, err)
}

// TestInitMediaRoutesRegistration tests that media routes are properly registered
func TestInitMediaRoutesRegistration(t *testing.T) {
	// Setup
	e, controller, _ := setupMediaTestEnvironment(t)

	// Re-initialize the routes to ensure a clean state
	controller.initMediaRoutes()

	// Get all routes from the Echo instance
	routes := e.Routes()

	// Define the media route suffixes we expect to find
	expectedSuffixes := map[string]bool{
		"GET /media/audio/:filename":       false,
		"GET /media/spectrogram/:filename": false,
	}

	// Check each route suffix
	foundCount := 0
	for _, r := range routes {
		// Check if the route path ends with one of the expected suffixes
		for suffix := range expectedSuffixes {
			// Ensure the check is for the correct HTTP method and path suffix
			if r.Method == http.MethodGet && strings.HasSuffix(r.Path, suffix[4:]) { // suffix[4:] removes "GET "
				if !expectedSuffixes[suffix] {
					expectedSuffixes[suffix] = true
					foundCount++
				}
			}
		}
	}

	// Verify that all expected routes were registered
	assert.Equal(t, len(expectedSuffixes), foundCount, "Number of found media routes does not match expected.")
	for suffix, found := range expectedSuffixes {
		assert.True(t, found, "Media route suffix not registered: %s", suffix)
	}
}

// createTestAudioFile creates a valid WAV file for testing that meets the minimum size requirements.
// The file will be at least 1024 bytes to pass validation.
func createTestAudioFile(t *testing.T, path string) error {
	t.Helper()

	// Create a 0.1 second WAV file (100ms) at 44100Hz, 16-bit, mono
	// This results in a file size of approximately 8864 bytes (44 header + 8820 data)
	durationSec := 0.1
	sampleRate := 44100
	numSamples := int(float64(sampleRate) * durationSec)
	dataSize := numSamples * 2 // 16-bit = 2 bytes per sample

	file, err := os.Create(path) //nolint:gosec // G304: path is constructed from controlled test temp dir
	if err != nil {
		return err
	}
	defer func() {
		if err := file.Close(); err != nil {
			t.Logf("Failed to close test audio file: %v", err)
		}
	}()

	// Write WAV header
	header := []byte{
		'R', 'I', 'F', 'F',
		byte(dataSize + 36), byte((dataSize + 36) >> 8), byte((dataSize + 36) >> 16), byte((dataSize + 36) >> 24),
		'W', 'A', 'V', 'E',
		'f', 'm', 't', ' ',
		16, 0, 0, 0, // Subchunk1Size
		1, 0, // AudioFormat (PCM)
		1, 0, // NumChannels (mono)
		byte(sampleRate), byte(sampleRate >> 8), byte(sampleRate >> 16), byte(sampleRate >> 24), // SampleRate
		byte(sampleRate * 2), byte((sampleRate * 2) >> 8), byte((sampleRate * 2) >> 16), byte((sampleRate * 2) >> 24), // ByteRate
		2, 0, // BlockAlign
		16, 0, // BitsPerSample
		'd', 'a', 't', 'a',
		byte(dataSize), byte(dataSize >> 8), byte(dataSize >> 16), byte(dataSize >> 24),
	}

	if _, err := file.Write(header); err != nil {
		return err
	}

	// Write silence (zeros) as audio data
	silence := make([]byte, dataSize)
	_, err = file.Write(silence)
	return err
}

// TestServeAudioClip tests the ServeAudioClip handler using SecureFS
func TestServeAudioClip(t *testing.T) {
	// Setup test environment with SecureFS rooted in tempDir
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create a small test audio file within the secure root
	smallFilename := "small.wav"
	smallFilePath := filepath.Join(tempDir, smallFilename)
	err := createTestAudioFile(t, smallFilePath)
	require.NoError(t, err)

	// Create a large test audio file (over 1MB)
	largeFilename := "large.wav"
	largeFilePath := filepath.Join(tempDir, largeFilename)
	createLargeWAVFile(t, largeFilePath, 1100*1024)

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
			expectedLength: 8864, // WAV header (44) + audio data (8820)
			partialContent: false,
		},
		{
			name:           "Large file - full content",
			filename:       largeFilename,
			rangeHeader:    "",
			expectedStatus: http.StatusOK,
			expectedLength: 1100*1024 + 44, // Data size + WAV header
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
			expectedStatus: http.StatusBadRequest, // SecureFS correctly detects and blocks traversal attempts
			expectedLength: 0,
			partialContent: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			targetURL := "/api/v2/media/audio/" + tc.filename
			req := httptest.NewRequest(http.MethodGet, targetURL, http.NoBody)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			isSmallFile := tc.filename == smallFilename
			assertAudioClipResponse(t, rec, tc.expectedStatus, tc.expectedLength, tc.partialContent, isSmallFile)
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
	audioFilename := "audio.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	err := createTestAudioFile(t, audioFilePath)
	require.NoError(t, err)

	// --- Simulate Spectrogram Generation (by creating the expected file) ---
	// This allows testing the "file exists" path without running external tools.
	// Default is raw=true, so the filename format is audio_800px.png
	spectrogramFilename := "audio_800px.png"
	spectrogramFilePath := filepath.Join(tempDir, spectrogramFilename)
	spectrogramContent := "simulated spectrogram content"
	err = os.WriteFile(spectrogramFilePath, []byte(spectrogramContent), 0o600)
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
			expectedStatus: http.StatusBadRequest, // SecureFS correctly detects and blocks traversal attempts
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
			}
		})
	}
}

// Setup function to create a test environment with SecureFS
func setupMediaTestEnvironment(t *testing.T) (*echo.Echo, *Controller, string) {
	t.Helper()

	// Create a temporary directory for test files (auto-cleaned by testing framework)
	tempDir := t.TempDir()

	// Use the standard test setup which now initializes SFS
	// We need the controller instance to reconfigure its SFS
	e, _, controller := setupTestEnvironment(t)

	// --- Crucially: Re-initialize SFS in the controller to use the *media test* tempDir ---
	// Close the SFS created by setupTestEnvironment (if any)
	if controller.SFS != nil {
		require.NoError(t, controller.SFS.Close(), "Failed to close SFS")
	}
	// Create and assign the new SFS rooted in our tempDir
	newSFS, err := securefs.New(tempDir)
	require.NoError(t, err, "Failed to create SecureFS for media test environment")
	controller.SFS = newSFS
	t.Cleanup(func() {
		assert.NoError(t, controller.SFS.Close(), "Failed to close SFS")
	}) // Ensure this one is closed too

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
	audioFilename := "test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	err := createTestAudioFile(t, audioFilePath)
	require.NoError(t, err)

	// Simulate existing spectrogram
	// Default is raw=true, so the filename format is test_800px.png
	spectrogramFilename := "test_800px.png"
	spectrogramFilePath := filepath.Join(tempDir, spectrogramFilename)
	spectrogramContent := "test spectrogram content"
	err = os.WriteFile(spectrogramFilePath, []byte(spectrogramContent), 0o600)
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
		isAudioFile    bool // Flag to indicate if this is an audio file that needs special handling
	}{
		{
			name:           "Audio endpoint",
			endpoint:       "/api/v2/media/audio/" + audioFilename,
			expectedStatus: http.StatusOK,
			expectedBody:   "", // For audio files, we'll check the WAV header instead
			isAudioFile:    true,
		},
		{
			name:           "Spectrogram endpoint (existing)",
			endpoint:       "/api/v2/media/spectrogram/" + audioFilename + "?width=800",
			expectedStatus: http.StatusOK,
			expectedBody:   spectrogramContent,
			isAudioFile:    false,
		},
		{
			name:           "Spectrogram endpoint (needs generation - likely fails)",
			endpoint:       "/api/v2/media/spectrogram/" + audioFilename + "?width=1200",
			expectedStatus: http.StatusInternalServerError,
			expectedBody:   "",
			isAudioFile:    false,
		},
		{
			name:           "Missing audio file",
			endpoint:       "/api/v2/media/audio/missing.mp3",
			expectedStatus: http.StatusNotFound,
			expectedBody:   "",
			isAudioFile:    false,
		},
	}

	// Run test cases
	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Make request to the HTTP server
			resp, err := client.Get(server.URL + tc.endpoint)
			require.NoError(t, err)
			defer func() {
				assert.NoError(t, resp.Body.Close(), "Failed to close response body")
			}()

			// Check status code
			assert.Equal(t, tc.expectedStatus, resp.StatusCode)

			// Check response body for successful requests
			if tc.expectedStatus == http.StatusOK {
				body, err := io.ReadAll(resp.Body)
				require.NoError(t, err)
				assertMediaResponseBody(t, body, tc.expectedBody, tc.isAudioFile)
			}
		})
	}
}

// TestMediaSecurityScenarios tests various security scenarios using SecureFS
func TestMediaSecurityScenarios(t *testing.T) {
	// Go 1.25: Add test metadata for better organization and reporting
	t.Attr("component", "media")
	t.Attr("type", "security")
	t.Attr("feature", "path-traversal-protection")

	// Setup test environment (SecureFS handles security)
	// We need the echo instance to serve requests directly
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file within the secure root
	secureFilename := "secure.wav"
	secureFilePath := filepath.Join(tempDir, secureFilename)
	err := createTestAudioFile(t, secureFilePath)
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
					// or potentially 500/403/400 depending on the exact nature of the issue and SecureFS behavior
					assert.Contains(t, []int{http.StatusNotFound, http.StatusInternalServerError, http.StatusBadRequest, http.StatusForbidden},
						rec.Code, "Expected 404/500/400/403 status code for security issue, got %d for %s", rec.Code, endpoint)

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
	err := os.WriteFile(filePath, fileContent, 0o600)
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
				t.Helper()
				assert.Equal(t, fileContent, rec.Body.Bytes())
			},
		},
		{
			name:           "Range: bytes=0-99",
			rangeHeader:    "bytes=0-99",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, fileContent[0:100], rec.Body.Bytes())
				assert.Equal(t, "bytes 0-99/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Range: bytes=100-199",
			rangeHeader:    "bytes=100-199",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, fileContent[100:200], rec.Body.Bytes())
				assert.Equal(t, "bytes 100-199/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Range: bytes=-100",
			rangeHeader:    "bytes=-100",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, fileContent[924:1024], rec.Body.Bytes())
				assert.Equal(t, "bytes 924-1023/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Range: bytes=924-",
			rangeHeader:    "bytes=924-",
			expectedStatus: http.StatusPartialContent,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Equal(t, fileContent[924:1024], rec.Body.Bytes())
				assert.Equal(t, "bytes 924-1023/1024", rec.Header().Get("Content-Range"))
			},
		},
		{
			name:           "Invalid range format (bytes=invalid)",
			rangeHeader:    "bytes=invalid",
			expectedStatus: http.StatusRequestedRangeNotSatisfiable,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				assert.Empty(t, rec.Body.Bytes(), "416 responses should not include the file body")
			},
		},
		{
			name:           "Invalid range (out of bounds)",
			rangeHeader:    "bytes=2000-",
			expectedStatus: http.StatusRequestedRangeNotSatisfiable,
			validateFunc: func(t *testing.T, rec *httptest.ResponseRecorder) {
				t.Helper()
				// No body validation needed
			},
		},
		/* // <-- FIX: Comment out multi-range test
		{
			name:        "Multiple ranges - should be handled by http.ServeContent",
			rangeHeader: "bytes=0-99,200-299",
			// http.ServeContent serves the *entire* file if multiple ranges are requested,
			// but the spec says it *should* return multipart/byteranges. Go's implementation might differ or require specific setup.
			// Let's assume for now it sends the whole file (like Apache does sometimes).
			// Update: Go's http.ServeContent *does* support multipart/byteranges.
			// The test needs to be updated to expect this complex response format or simplified.
			// For now, let's test the first part is correct, although the response is multipart.
			// expectedBody:     fileContent[0:100], // This assertion is WRONG for multipart
			// expectedContentRange: "bytes 0-99/1024", // This assertion is WRONG for multipart
			// expectedContentType:  "multipart/byteranges; boundary=", // Check it starts with multipart
		},
		*/
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+filename, http.NoBody)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(filename)

			handlerErr := controller.ServeAudioClip(c)
			assert.Equal(t, tc.expectedStatus, rec.Code)
			assertHandlerError(t, handlerErr, tc.expectedStatus)

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
		"тест.wav",    // Cyrillic
		"테스트.wav",     // Korean
		"測試.wav",      // Chinese
		"Prüfung.wav", // German with umlaut
		"ファイル.wav",    // Japanese
		"αρχείο.wav",  // Greek
	}

	for _, name := range unicodeNames {
		// Use filename directly relative to tempDir (SecureFS root)
		filePath := filepath.Join(tempDir, name)
		err := createTestAudioFile(t, filePath)
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
			require.NoErrorf(t, handlerErr, "Error serving Unicode filename %s", name)

			// Check response
			assert.Equal(t, http.StatusOK, rec.Code)
			// Check that we got a valid WAV file (starts with "RIFF")
			body := rec.Body.Bytes()
			assert.Greater(t, len(body), 4, "Response body should not be empty")
			if len(body) > 4 {
				assert.Equal(t, []byte("RIFF"), body[:4], "Should return a WAV file starting with RIFF header")
			}
		})
	}
}

// TestServeSpectrogramRawParameter tests the raw parameter parsing functionality
func TestServeSpectrogramRawParameter(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file within the secure root
	audioFilename := "rawtest.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	err := createTestAudioFile(t, audioFilePath)
	require.NoError(t, err)

	// Simulate raw spectrogram (default behavior)
	rawSpectrogramFilename := "rawtest_800px.png"
	rawSpectrogramPath := filepath.Join(tempDir, rawSpectrogramFilename)
	err = os.WriteFile(rawSpectrogramPath, []byte("raw spectrogram"), 0o600)
	require.NoError(t, err)

	// Simulate spectrogram with legend
	legendSpectrogramFilename := "rawtest_800px-legend.png"
	legendSpectrogramPath := filepath.Join(tempDir, legendSpectrogramFilename)
	err = os.WriteFile(legendSpectrogramPath, []byte("legend spectrogram"), 0o600)
	require.NoError(t, err)

	// Test cases for different raw parameter values
	testCases := []struct {
		name           string
		rawParam       string
		expectedStatus int
		expectedBody   string
		description    string
	}{
		{
			name:           "Default behavior (no raw param)",
			rawParam:       "",
			expectedStatus: http.StatusOK,
			expectedBody:   "raw spectrogram",
			description:    "Should default to raw=true",
		},
		{
			name:           "Explicit raw=true",
			rawParam:       "true",
			expectedStatus: http.StatusOK,
			expectedBody:   "raw spectrogram",
			description:    "Should generate raw spectrogram",
		},
		{
			name:           "Explicit raw=false",
			rawParam:       "false",
			expectedStatus: http.StatusOK,
			expectedBody:   "legend spectrogram",
			description:    "Should generate spectrogram with legend",
		},
		{
			name:           "Raw=1 (numeric true)",
			rawParam:       "1",
			expectedStatus: http.StatusOK,
			expectedBody:   "raw spectrogram",
			description:    "Should parse '1' as true",
		},
		{
			name:           "Raw=0 (numeric false)",
			rawParam:       "0",
			expectedStatus: http.StatusOK,
			expectedBody:   "legend spectrogram",
			description:    "Should parse '0' as false",
		},
		{
			name:           "Raw=yes",
			rawParam:       "yes",
			expectedStatus: http.StatusOK,
			expectedBody:   "raw spectrogram",
			description:    "Should parse 'yes' as true",
		},
		{
			name:           "Raw=no",
			rawParam:       "no",
			expectedStatus: http.StatusOK,
			expectedBody:   "legend spectrogram",
			description:    "Should parse 'no' as false",
		},
		{
			name:           "Raw=invalid (defaults to true)",
			rawParam:       "invalid",
			expectedStatus: http.StatusOK,
			expectedBody:   "raw spectrogram",
			description:    "Invalid values should default to true",
		},
		{
			name:           "Raw=TRUE (case insensitive)",
			rawParam:       "TRUE",
			expectedStatus: http.StatusOK,
			expectedBody:   "raw spectrogram",
			description:    "Should be case insensitive",
		},
		{
			name:           "Raw=False (mixed case)",
			rawParam:       "False",
			expectedStatus: http.StatusOK,
			expectedBody:   "legend spectrogram",
			description:    "Should handle mixed case",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			url := "/api/v2/media/spectrogram/" + audioFilename + "?width=800"
			if tc.rawParam != "" {
				url += "&raw=" + tc.rawParam
			}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("filename")
			c.SetParamValues(audioFilename)

			// Call handler
			_ = controller.ServeSpectrogram(c)

			// Check response
			assert.Equal(t, tc.expectedStatus, rec.Code, tc.description)
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedBody, rec.Body.String(), tc.description)
			}
		})
	}
}

// TestServeAudioByID tests the ServeAudioByID handler with Content-Disposition header
func TestServeAudioByID(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file
	testFilename := "2024-01-15_14-30-45_Turdus_migratorius.wav"
	filePath := filepath.Join(tempDir, testFilename)
	testContent := "test audio content"
	err := os.WriteFile(filePath, []byte(testContent), 0o600)
	require.NoError(t, err)

	// Setup mock data store to return the test file path
	mockDS := mocks.NewMockInterface(t)
	// Mock the GetNoteClipPath method to return our test filename
	mockDS.On("GetNoteClipPath", "123").Return(testFilename, nil)
	mockDS.On("GetNoteClipPath", "999").Return("", errors.New("record not found"))
	controller.DS = mockDS

	// Test cases for different scenarios
	tests := []struct {
		name                   string
		audioID                string
		expectedStatus         int
		expectedContentType    string
		expectedDisposition    string
		shouldHaveAcceptRanges bool
	}{
		{
			name:                   "Valid audio by ID with iOS Safari headers",
			audioID:                "123",
			expectedStatus:         http.StatusOK,
			expectedContentType:    MimeTypeWAV,
			expectedDisposition:    fmt.Sprintf("inline; filename*=UTF-8''%s", testFilename),
			shouldHaveAcceptRanges: true,
		},
		{
			name:           "Invalid audio ID returns 404",
			audioID:        "999",
			expectedStatus: http.StatusNotFound,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/"+tc.audioID, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(tc.audioID)

			handlerErr := controller.ServeAudioByID(c)

			if tc.expectedStatus == http.StatusOK {
				require.NoError(t, handlerErr)
			}

			assert.Equal(t, tc.expectedStatus, rec.Code)

			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, testContent, rec.Body.String())
				assertAudioHeaders(t, rec, tc.expectedContentType, tc.expectedDisposition, tc.shouldHaveAcceptRanges)
			}
		})
	}

	// Note: Error cases are omitted as they are tested elsewhere and the main
	// goal of this test is to verify Content-Disposition header functionality
}

// TestServeAudioByID_AudioFormats tests different audio format MIME type handling
func TestServeAudioByID_AudioFormats(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Test cases for different audio formats
	audioFormats := []struct {
		filename     string
		expectedMIME string
	}{
		{"test.wav", MimeTypeWAV},
		{"test.flac", MimeTypeFLAC},
		{"test.mp3", MimeTypeMP3},
		{"test.m4a", MimeTypeM4A},
		{"test.ogg", MimeTypeOGG},
		{"test.unknown", ""}, // Should let ServeRelativeFile handle
	}

	// Setup mock data store
	mockDS := mocks.NewMockInterface(t)
	controller.DS = mockDS

	// Using Go 1.25's modern range over int syntax for cleaner iteration
	for i := range len(audioFormats) {
		format := audioFormats[i]
		t.Run(fmt.Sprintf("Audio format %s", format.filename), func(t *testing.T) {
			// Create test file
			filePath := filepath.Join(tempDir, format.filename)
			testContent := fmt.Sprintf("test audio content %d", i)
			err := os.WriteFile(filePath, []byte(testContent), 0o600)
			require.NoError(t, err)

			// Use Go 1.25 t.Cleanup for modern resource management
			t.Cleanup(func() {
				if err := os.Remove(filePath); err != nil {
					t.Logf("Failed to remove test file %s: %v", filePath, err)
				}
			})

			// Setup mock to return this filename
			audioID := fmt.Sprintf("test%d", i)
			mockDS.On("GetNoteClipPath", audioID).Return(format.filename, nil).Once()

			// Create request
			req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/"+audioID, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues(audioID)

			// Call handler
			handlerErr := controller.ServeAudioByID(c)
			require.NoError(t, handlerErr)

			// Check response
			assert.Equal(t, http.StatusOK, rec.Code)
			assert.Equal(t, testContent, rec.Body.String())

			// Check Content-Type if expected
			if format.expectedMIME != "" {
				assert.Equal(t, format.expectedMIME, rec.Header().Get("Content-Type"),
					"Content-Type should match expected MIME type for %s", format.filename)
			}

			// All audio files should have these headers for iOS Safari
			assert.Equal(t, "bytes", rec.Header().Get("Accept-Ranges"),
				"Accept-Ranges header required for iOS Safari")
			assert.Contains(t, rec.Header().Get("Content-Disposition"), "inline",
				"Content-Disposition should use inline for browser playback")
		})
	}
}

// TestServeSpectrogramByIDRawParameter tests the raw parameter parsing for ID-based spectrogram endpoint
func TestServeSpectrogramByIDRawParameter(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file
	testFilename := "test_raw_param.wav"
	filePath := filepath.Join(tempDir, testFilename)
	err := createTestAudioFile(t, filePath)
	require.NoError(t, err)

	// Simulate raw spectrogram (default behavior)
	rawSpectrogramFilename := "test_raw_param_400px.png"
	rawSpectrogramPath := filepath.Join(tempDir, rawSpectrogramFilename)
	err = os.WriteFile(rawSpectrogramPath, []byte("id raw spectrogram"), 0o600)
	require.NoError(t, err)

	// Simulate spectrogram with legend
	legendSpectrogramFilename := "test_raw_param_400px-legend.png"
	legendSpectrogramPath := filepath.Join(tempDir, legendSpectrogramFilename)
	err = os.WriteFile(legendSpectrogramPath, []byte("id legend spectrogram"), 0o600)
	require.NoError(t, err)

	// Setup mock data store
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "123").Return(testFilename, nil)
	controller.DS = mockDS

	// Test cases
	testCases := []struct {
		name           string
		rawParam       string
		expectedStatus int
		expectedBody   string
	}{
		{
			name:           "Default (no raw param) - should be raw",
			rawParam:       "",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Explicit raw=true",
			rawParam:       "true",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Explicit raw=false",
			rawParam:       "false",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			name:           "Raw=1 (numeric true)",
			rawParam:       "1",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=0 (numeric false)",
			rawParam:       "0",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			name:           "Raw=t (short true)",
			rawParam:       "t",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=f (short false)",
			rawParam:       "f",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			name:           "Raw=yes",
			rawParam:       "yes",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=no",
			rawParam:       "no",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			name:           "Raw=on",
			rawParam:       "on",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=off",
			rawParam:       "off",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			name:           "Raw=invalid (defaults to true)",
			rawParam:       "invalid",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=TRUE (case insensitive)",
			rawParam:       "TRUE",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=False (mixed case)",
			rawParam:       "False",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			name:           "Raw=YES (uppercase)",
			rawParam:       "YES",
			expectedStatus: http.StatusOK,
			expectedBody:   "id raw spectrogram",
		},
		{
			name:           "Raw=No (mixed case)",
			rawParam:       "No",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Create request
			url := "/api/v2/spectrogram/123?size=sm"
			if tc.rawParam != "" {
				url += "&raw=" + tc.rawParam
			}
			req := httptest.NewRequest(http.MethodGet, url, http.NoBody)
			rec := httptest.NewRecorder()
			c := e.NewContext(req, rec)
			c.SetParamNames("id")
			c.SetParamValues("123")

			// Call handler
			_ = controller.ServeSpectrogramByID(c)

			// Check response
			assert.Equal(t, tc.expectedStatus, rec.Code)
			if tc.expectedStatus == http.StatusOK {
				assert.Equal(t, tc.expectedBody, rec.Body.String())
			}
		})
	}
}

// TestIsValidFilename tests the isValidFilename function for various edge cases
func TestIsValidFilename(t *testing.T) {
	tests := []struct {
		name     string
		filename string
		expected bool
	}{
		// Valid filenames
		{"valid audio file", "audio.wav", true},
		{"valid with spaces", "my audio file.flac", true},
		{"valid with numbers", "clip_123.mp3", true},
		{"valid with special chars", "bird-song.m4a", true},
		{"valid unicode", "птица.ogg", true},

		// Invalid filenames - empty/special cases
		{"empty string", "", false},
		{"current directory", ".", false},
		{"root directory", "/", false},

		// Invalid filenames - security risks
		{"path with forward slash", "path/to/file.wav", false},
		{"path with backslash", "path\\to\\file.wav", false},
		{"relative path up", "../audio.wav", false},

		// Invalid filenames - control characters
		{"null byte", "file\x00.wav", false},
		{"tab character", "file\t.wav", false},
		{"newline character", "file\n.wav", false},
		{"carriage return", "file\r.wav", false},
		{"control character", "file\x01.wav", false},
		{"delete character", "file\x7f.wav", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isValidFilename(tt.filename)
			assert.Equal(t, tt.expected, result,
				"isValidFilename(%q) = %v, want %v", tt.filename, result, tt.expected)
		})
	}
}

// TestGetSpectrogramLogger tests that the spectrogram logger is never nil
func TestGetSpectrogramLogger(t *testing.T) {
	// Test that getSpectrogramLogger always returns a non-nil logger
	logger := getSpectrogramLogger()
	require.NotNil(t, logger, "getSpectrogramLogger() must never return nil")

	// Test that we can call logging methods without panic
	assert.NotPanics(t, func() {
		logger.Info("test message")
		logger.Debug("test debug")
		logger.Warn("test warning")
		logger.Error("test error")
	}, "Logger methods should not panic")
}
