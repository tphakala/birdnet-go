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
	"sync"
	"testing"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/audiocore/ffmpeg"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
	"github.com/tphakala/birdnet-go/internal/securefs"
	"gorm.io/gorm"
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
	controller.Settings.Load().Realtime.Audio.Export.Path = tempDir

	// Inject passthrough auth middleware so authenticated routes (e.g. clip extraction)
	// can be registered and tested without a real auth service
	WithAuthMiddleware(passthroughMiddleware())(controller)

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
	mockDS.On("GetNoteClipPath", "999").Return("", gorm.ErrRecordNotFound)
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

			// Setup mock to return this filename — use numeric IDs since the handler
			// validates that :id is a valid number
			audioID := fmt.Sprintf("%d", i+1)
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

	// Simulate raw spectrogram (default behavior) using lg size (1026px)
	rawSpectrogramFilename := "test_raw_param_1026px.png"
	rawSpectrogramPath := filepath.Join(tempDir, rawSpectrogramFilename)
	err = os.WriteFile(rawSpectrogramPath, []byte("id raw spectrogram"), 0o600)
	require.NoError(t, err)

	// Simulate spectrogram with legend
	legendSpectrogramFilename := "test_raw_param_1026px-legend.png"
	legendSpectrogramPath := filepath.Join(tempDir, legendSpectrogramFilename)
	err = os.WriteFile(legendSpectrogramPath, []byte("id legend spectrogram"), 0o600)
	require.NoError(t, err)

	// Setup mock data store
	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "123").Return(testFilename, nil)
	mockDS.On("GetNoteModelType", "123").Return("bird", nil)
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
			// Create request using lg size (default)
			url := "/api/v2/spectrogram/123?size=lg"
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

// TestSpeciesImageNotFound_Returns404 verifies that image endpoints return HTTP 404
// (not 500) when the image provider has no image for a species.
// Regression test for GitHub issue #2201.
func TestSpeciesImageNotFound_Returns404(t *testing.T) {
	t.Attr("component", "media")
	t.Attr("type", "regression")
	t.Attr("issue", "2201")

	// Setup test environment — the default mock provider returns images for any species
	e, _, controller := setupTestEnvironment(t)

	// Replace the mock provider with one that returns ErrImageNotFound for unknown species
	notFoundProvider := &TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
		},
	}
	controller.BirdImageCache.SetImageProvider(notFoundProvider)

	tests := []struct {
		name    string
		handler func(echo.Context) error
		setup   func(*echo.Echo) echo.Context
	}{
		{
			name:    "GetSpeciesImageInfo returns 404 for missing species image",
			handler: controller.GetSpeciesImageInfo,
			setup: func(e *echo.Echo) echo.Context {
				req := httptest.NewRequest(http.MethodGet, "/api/v2/media/species-image/info?name=Nonexistus+fictus", http.NoBody)
				rec := httptest.NewRecorder()
				return e.NewContext(req, rec)
			},
		},
		{
			name:    "ServeSpeciesImageProxy returns 404 for missing species image",
			handler: controller.ServeSpeciesImageProxy,
			setup: func(e *echo.Echo) echo.Context {
				req := httptest.NewRequest(http.MethodGet, "/api/v2/media/image/Nonexistus%20fictus", http.NoBody)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				c.SetParamNames("scientific_name")
				c.SetParamValues("Nonexistus fictus")
				return c
			},
		},
		{
			name:    "GetSpeciesImage returns 404 for missing species image",
			handler: controller.GetSpeciesImage,
			setup: func(e *echo.Echo) echo.Context {
				req := httptest.NewRequest(http.MethodGet, "/api/v2/media/species-image?name=Nonexistus+fictus", http.NoBody)
				rec := httptest.NewRecorder()
				return e.NewContext(req, rec)
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := tt.setup(e)
			_ = tt.handler(ctx)

			rec := ctx.Response().Writer.(*httptest.ResponseRecorder)
			assert.Equal(t, http.StatusNotFound, rec.Code,
				"Expected 404 Not Found for missing species image, got %d", rec.Code)
			assert.Contains(t, rec.Body.String(), "Image not found",
				"Response body should indicate image was not found")
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

// TestServeAudioClipWaitsForEncoding verifies that when an audio file is being
// encoded (temp file exists), the server waits for the final file to appear
// instead of immediately returning 503.
// TestIsExportTempFor verifies the in-progress-encoding temp matcher accepts the
// export temp formats and rejects unrelated sidecar temps that merely share the
// clip's prefix and the ".temp" suffix (GitHub #3323).
func TestIsExportTempFor(t *testing.T) {
	t.Parallel()
	const base = "turdus_merula_84p_20260531T084138Z.m4a"
	tests := []struct {
		name string
		file string
		want bool
	}{
		{"unique format", base + ".12345.1" + ffmpeg.TempExt, true},
		{"unique format large seq", base + ".987.4096" + ffmpeg.TempExt, true},
		{"pre-fix legacy format", base + ffmpeg.TempExt, true},
		{"final clip", base, false},
		{"final clip other ext", base + ".part", false},
		{"sidecar spectrogram temp", base + ".png.12345.1" + ffmpeg.TempExt, false},
		{"different clip", "other_99p_20260531T084138Z.m4a.12345.1" + ffmpeg.TempExt, false},
		{"non-integer pid", base + ".abc.1" + ffmpeg.TempExt, false},
		{"non-integer seq", base + ".12345.x" + ffmpeg.TempExt, false},
		{"missing seq", base + ".12345" + ffmpeg.TempExt, false},
		{"extra middle segment", base + ".12345.1.2" + ffmpeg.TempExt, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isExportTempFor(tt.file, base))
		})
	}
}

func TestServeAudioClipWaitsForEncoding(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	audioFilename := "encoding-test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	// Mirror the per-export unique temp name "<clip>.<pid>.<seq>.temp" the real
	// exporters write, so this exercises the directory-scan detection in
	// isAudioBeingEncoded rather than a fixed name that production never produces.
	tempFilePath := audioFilePath + ".99999.1" + ffmpeg.TempExt

	// Create the temp file to simulate in-progress encoding
	err := os.WriteFile(tempFilePath, []byte("temp encoding data"), 0o600)
	require.NoError(t, err)

	// Simulate the file appearing after a short delay (encoding completes).
	// Use an error channel to propagate failures from the background goroutine.
	errChan := make(chan error, 1)
	go func() {
		time.Sleep(500 * time.Millisecond)
		// Create the final audio file
		createErr := createTestAudioFile(t, audioFilePath)
		if createErr != nil {
			errChan <- createErr
			return
		}
		// Remove the temp file to simulate FFmpeg completing
		os.Remove(tempFilePath) //nolint:errcheck // best-effort cleanup in test
		errChan <- nil
	}()
	t.Cleanup(func() {
		if bgErr := <-errChan; bgErr != nil {
			t.Errorf("Background goroutine failed: %v", bgErr)
		}
	})

	// Make the request — should wait for the file and serve it
	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/media/audio/:filename")
	ctx.SetParamNames("filename")
	ctx.SetParamValues(audioFilename)

	handlerErr := controller.ServeAudioClip(ctx)

	// Should succeed (200) instead of 503
	if handlerErr != nil {
		// If there's an error, it should NOT be 503
		if httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr); ok {
			assert.NotEqual(t, http.StatusServiceUnavailable, httpErr.Code,
				"Should not return 503 when file appears within timeout")
		}
	} else {
		assert.Equal(t, http.StatusOK, rec.Code,
			"Should serve the file successfully after waiting for encoding")
	}
}

// TestServeAudioClipReturns503AfterTimeout verifies that the server returns 503
// with Retry-After header when encoding doesn't complete within the timeout.
func TestServeAudioClipReturns503AfterTimeout(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	audioFilename := "slow-encoding.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	// Mirror the per-export unique temp name "<clip>.<pid>.<seq>.temp" the real
	// exporters write, so this exercises the directory-scan detection in
	// isAudioBeingEncoded rather than a fixed name that production never produces.
	tempFilePath := audioFilePath + ".99999.1" + ffmpeg.TempExt

	// Create only the temp file — final file never appears
	err := os.WriteFile(tempFilePath, []byte("temp encoding data"), 0o600)
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/media/audio/:filename")
	ctx.SetParamNames("filename")
	ctx.SetParamValues(audioFilename)

	handlerErr := controller.ServeAudioClip(ctx)

	// Should return 503 with Retry-After header.
	// HandleError may write the response directly (handlerErr == nil) or return an HTTPError.
	if handlerErr != nil {
		httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr)
		require.True(t, ok, "Error should be an HTTPError")
		assert.Equal(t, http.StatusServiceUnavailable, httpErr.Code,
			"Should return 503 when encoding doesn't complete within timeout")
	} else {
		assert.Equal(t, http.StatusServiceUnavailable, rec.Code,
			"Should return 503 when encoding doesn't complete within timeout")
	}
	assert.Equal(t, audioRetryAfterSeconds, rec.Header().Get("Retry-After"),
		"Should include Retry-After header")
}

// TestServeAudioClipNoTempFileReturns404 verifies that a missing file without
// a temp file returns 404 (not 503).
func TestServeAudioClipNoTempFileReturns404(t *testing.T) {
	e, controller, _ := setupMediaTestEnvironment(t)

	audioFilename := "nonexistent.wav"

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/media/audio/:filename")
	ctx.SetParamNames("filename")
	ctx.SetParamValues(audioFilename)

	handlerErr := controller.ServeAudioClip(ctx)

	// Should return 404, not 503
	if handlerErr != nil {
		if httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr); ok {
			assert.Equal(t, http.StatusNotFound, httpErr.Code,
				"Should return 404 when no temp file exists")
		}
	} else {
		assert.Equal(t, http.StatusNotFound, rec.Code,
			"Should return 404 when no temp file exists")
	}
}

// TestServeAudioClipGraceWaitServesFile verifies that when an audio file
// appears shortly after the initial request (no temp file visible), the
// grace period wait catches it instead of returning 404. This covers the
// race condition from issue #2355 where the detection DB record is committed
// but FFmpeg hasn't created the temp file yet.
func TestServeAudioClipGraceWaitServesFile(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	audioFilename := "grace-wait-test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)

	// No temp file — only the final file appears after a short delay.
	// This simulates the race window where FFmpeg hasn't started yet.
	// Delay is derived from audioGracePeriod to stay aligned with production timing.
	var wg sync.WaitGroup
	wg.Go(func() {
		time.Sleep(audioGracePeriod / 2)
		if err := createTestAudioFile(t, audioFilePath); err != nil {
			t.Errorf("Background goroutine failed: %v", err)
		}
	})
	t.Cleanup(wg.Wait)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/media/audio/:filename")
	ctx.SetParamNames("filename")
	ctx.SetParamValues(audioFilename)

	handlerErr := controller.ServeAudioClip(ctx)

	// Should succeed — grace wait should pick up the file
	if handlerErr != nil {
		if httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr); ok {
			assert.NotEqual(t, http.StatusNotFound, httpErr.Code,
				"Should not return 404 when file appears within grace period")
		}
	} else {
		assert.Equal(t, http.StatusOK, rec.Code,
			"Should serve the file successfully after grace wait")
	}
}

// TestBuildStyleSuffix tests that spectrogram style and dynamic range are correctly
// encoded in the filename suffix to prevent serving stale cached spectrograms.
func TestBuildStyleSuffix(t *testing.T) {
	tests := []struct {
		name         string
		style        string
		dynamicRange string
		expected     string
	}{
		{
			name:         "default style and default DR produce no suffix",
			style:        "default",
			dynamicRange: "100",
			expected:     "",
		},
		{
			name:         "empty style and empty DR produce no suffix",
			style:        "",
			dynamicRange: "",
			expected:     "",
		},
		{
			name:         "default style with empty DR produces no suffix",
			style:        "default",
			dynamicRange: "",
			expected:     "",
		},
		{
			name:         "scientific_dark style with default DR",
			style:        "scientific_dark",
			dynamicRange: "100",
			expected:     "-scientific_dark",
		},
		{
			name:         "high_contrast_dark style with default DR",
			style:        "high_contrast_dark",
			dynamicRange: "100",
			expected:     "-high_contrast_dark",
		},
		{
			name:         "scientific style with default DR",
			style:        "scientific",
			dynamicRange: "100",
			expected:     "-scientific",
		},
		{
			name:         "default style with non-default DR",
			style:        "default",
			dynamicRange: "80",
			expected:     "-dr80",
		},
		{
			name:         "scientific_dark style with non-default DR",
			style:        "scientific_dark",
			dynamicRange: "120",
			expected:     "-scientific_dark-dr120",
		},
		{
			name:         "empty style with non-default DR",
			style:        "",
			dynamicRange: "80",
			expected:     "-dr80",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := buildStyleSuffix(tt.style, tt.dynamicRange)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestBuildSpectrogramPathsWithStyle tests that spectrogram paths include the visual
// style in the filename, preventing stale cached spectrograms from being served
// after the user changes the spectrogram style setting.
func TestBuildSpectrogramPathsWithStyle(t *testing.T) {
	tests := []struct {
		name             string
		relAudioPath     string
		width            int
		raw              bool
		style            string
		dynamicRange     string
		expectedFilename string
	}{
		{
			name:             "default style raw",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              true,
			style:            "default",
			dynamicRange:     "100",
			expectedFilename: "bird_1026px.png",
		},
		{
			name:             "default style with legend",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              false,
			style:            "default",
			dynamicRange:     "100",
			expectedFilename: "bird_1026px-legend.png",
		},
		{
			name:             "scientific_dark style raw",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              true,
			style:            "scientific_dark",
			dynamicRange:     "100",
			expectedFilename: "bird_1026px-scientific_dark.png",
		},
		{
			name:             "scientific_dark style with legend",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              false,
			style:            "scientific_dark",
			dynamicRange:     "100",
			expectedFilename: "bird_1026px-scientific_dark-legend.png",
		},
		{
			name:             "high_contrast_dark with non-default DR raw",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            514,
			raw:              true,
			style:            "high_contrast_dark",
			dynamicRange:     "80",
			expectedFilename: "bird_514px-high_contrast_dark-dr80.png",
		},
		{
			name:             "empty style and DR produce backward-compatible filename",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              true,
			style:            "",
			dynamicRange:     "",
			expectedFilename: "bird_1026px.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, filename, fullPath := buildSpectrogramPaths(tt.relAudioPath, tt.width, tt.raw, tt.style, tt.dynamicRange)
			assert.Equal(t, tt.expectedFilename, filename)
			assert.Equal(t, filepath.Join("clips", "2025", "01", tt.expectedFilename), fullPath)
		})
	}
}

// TestAudioRangeWithGzipMiddleware_EndToEnd exercises the exact regression chain
// from #2709 (gzip middleware added) to #2846 (HTTP 416 on audio playback).
// It creates a full Echo router with the production gzip middleware AND SecureFS,
// then sends requests carrying both Accept-Encoding: gzip and Range headers.
// The gzip skipper must detect either the media route or the Range header and
// leave the response uncompressed so http.ServeContent can handle byte ranges.
func TestAudioRangeWithGzipMiddleware_EndToEnd(t *testing.T) {
	t.Parallel()
	t.Attr("component", "media")
	t.Attr("type", "regression")
	t.Attr("issue", "2846")

	// Constants for the test WAV file dimensions
	const (
		wavDataSize   = 8820 // 0.1s at 44100Hz, 16-bit mono
		wavHeaderSize = 44   // standard RIFF/WAV header
		wavTotalSize  = wavDataSize + wavHeaderSize
	)

	// Arrange: set up environment with gzip middleware on the Echo instance.
	// Echo middleware added via e.Use() applies to all routes at request time,
	// so routes registered by setupMediaTestEnvironment are already covered.
	e, _, tempDir := setupMediaTestEnvironment(t)
	e.Use(middleware.NewGzip())

	audioFilename := "gzip-range-test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	err := createTestAudioFile(t, audioFilePath)
	require.NoError(t, err)

	audioURL := "/api/v2/media/audio/" + audioFilename

	t.Run("range request with gzip accept-encoding returns 206 without compression", func(t *testing.T) {
		// Act: send a request with both Range and Accept-Encoding: gzip
		req := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
		req.Header.Set("Range", "bytes=0-99")
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusPartialContent, rec.Code,
			"Range request must return 206 Partial Content")
		assert.Empty(t, rec.Header().Get("Content-Encoding"),
			"Response must NOT be gzipped when Range is present")
		assert.Contains(t, rec.Header().Get("Content-Range"), "bytes 0-99/",
			"Content-Range header must reflect the requested byte range")
		assert.Equal(t, "100", rec.Header().Get("Content-Length"),
			"Content-Length must equal the number of bytes in the range")
	})

	t.Run("full request with gzip accept-encoding returns 200 without compression", func(t *testing.T) {
		// Media routes are excluded from gzip even without Range headers.
		req := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusOK, rec.Code)
		assert.Empty(t, rec.Header().Get("Content-Encoding"),
			"Media route must never be gzipped")
		assert.Equal(t, fmt.Sprintf("%d", wavTotalSize), rec.Header().Get("Content-Length"),
			"Content-Length must equal the full file size")
	})

	t.Run("suffix range with gzip accept-encoding returns 206", func(t *testing.T) {
		// Suffix range: last 200 bytes
		const suffixLen = 200
		req := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
		req.Header.Set("Range", fmt.Sprintf("bytes=-%d", suffixLen))
		req.Header.Set("Accept-Encoding", "gzip")
		rec := httptest.NewRecorder()
		e.ServeHTTP(rec, req)

		// Assert
		assert.Equal(t, http.StatusPartialContent, rec.Code)
		assert.Empty(t, rec.Header().Get("Content-Encoding"),
			"Response must not be gzipped for suffix range")
		expectedStart := wavTotalSize - suffixLen
		expectedRange := fmt.Sprintf("bytes %d-%d/%d", expectedStart, wavTotalSize-1, wavTotalSize)
		assert.Equal(t, expectedRange, rec.Header().Get("Content-Range"),
			"Content-Range must cover the last %d bytes", suffixLen)
	})
}

// TestAudioRangeConsistencyAcrossSequentialRequests simulates Chrome's aggressive
// range request pattern. Chrome records the Content-Length from an initial response
// and then uses it to build subsequent Range headers. When a file's state changes
// between requests (e.g., it is replaced with a smaller file), Chrome's cached
// content-length becomes stale and the follow-up range request asks for bytes
// beyond the actual file size, resulting in HTTP 416.
func TestAudioRangeConsistencyAcrossSequentialRequests(t *testing.T) {
	t.Parallel()
	t.Attr("component", "media")
	t.Attr("type", "regression")
	t.Attr("issue", "2846")

	// Constants for file sizes
	const (
		largeDataSize = 50000
		largeFileSize = largeDataSize + 44 // WAV header is 44 bytes
		smallDataSize = 10000
		smallFileSize = smallDataSize + 44
	)

	// Arrange
	e, _, tempDir := setupMediaTestEnvironment(t)
	filename := "sequential-range-test.wav"
	filePath := filepath.Join(tempDir, filename)
	audioURL := "/api/v2/media/audio/" + filename

	// Step 1: Create a large WAV file and make an initial request without Range
	createLargeWAVFile(t, filePath, largeDataSize)

	initialReq := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
	initialRec := httptest.NewRecorder()
	e.ServeHTTP(initialRec, initialReq)

	require.Equal(t, http.StatusOK, initialRec.Code,
		"Initial request must succeed with 200 OK")
	recordedContentLength := initialRec.Header().Get("Content-Length")
	assert.Equal(t, fmt.Sprintf("%d", largeFileSize), recordedContentLength,
		"Content-Length must match the large file size")

	// Step 2: Send a follow-up range request using the recorded Content-Length
	// (simulating Chrome's behavior of requesting bytes=0-{contentLength-1})
	rangeEnd := largeFileSize - 1
	chromeRangeHeader := fmt.Sprintf("bytes=0-%d", rangeEnd)

	followUpReq := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
	followUpReq.Header.Set("Range", chromeRangeHeader)
	followUpRec := httptest.NewRecorder()
	e.ServeHTTP(followUpRec, followUpReq)

	assert.Equal(t, http.StatusPartialContent, followUpRec.Code,
		"Follow-up range request covering the full file must return 206")
	expectedRange := fmt.Sprintf("bytes 0-%d/%d", rangeEnd, largeFileSize)
	assert.Equal(t, expectedRange, followUpRec.Header().Get("Content-Range"),
		"Content-Range must span the entire file")

	// Step 3: Replace the file with a smaller one (simulates a file being
	// re-exported at a different length, or a different clip replacing it)
	err := os.Remove(filePath)
	require.NoError(t, err, "Must be able to remove the original file")
	createLargeWAVFile(t, filePath, smallDataSize)

	// Step 4: Send a range request using the OLD (larger) content-length.
	// The range now extends beyond the new file, so the server must reject
	// it with 416 Range Not Satisfiable.
	staleRangeHeader := fmt.Sprintf("bytes=%d-%d", smallFileSize, rangeEnd)
	staleReq := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
	staleReq.Header.Set("Range", staleRangeHeader)
	staleRec := httptest.NewRecorder()
	e.ServeHTTP(staleRec, staleReq)

	assert.Equal(t, http.StatusRequestedRangeNotSatisfiable, staleRec.Code,
		"Range request beyond file size must return 416")
	assert.Contains(t, staleRec.Header().Get("Content-Range"),
		fmt.Sprintf("bytes */%d", smallFileSize),
		"416 response must include Content-Range with the current file size")
}

// concurrentRangeResult holds the outcome of a single concurrent range request.
type concurrentRangeResult struct {
	statusCode int
	err        error
}

// TestAudioConcurrentRangeRequests verifies that the audio endpoint handles
// simultaneous range requests to the same file without panics, data corruption,
// or races. Each response must be one of 200 (full content), 206 (partial), or
// 416 (range not satisfiable). Results are collected via a channel so all
// assertions run in the main goroutine (required by testify).
func TestAudioConcurrentRangeRequests(t *testing.T) {
	t.Parallel()
	t.Attr("component", "media")
	t.Attr("type", "concurrency")
	t.Attr("issue", "2846")

	const (
		concurrentWorkers = 20
		fileDataSize      = 100000
		fileTotalSize     = fileDataSize + 44 // WAV header
	)

	// Accepted HTTP status codes for a valid range request response
	acceptableStatuses := []int{
		http.StatusOK,
		http.StatusPartialContent,
		http.StatusRequestedRangeNotSatisfiable,
	}

	// Arrange
	e, _, tempDir := setupMediaTestEnvironment(t)
	filename := "concurrent-range-test.wav"
	filePath := filepath.Join(tempDir, filename)
	createLargeWAVFile(t, filePath, fileDataSize)

	audioURL := "/api/v2/media/audio/" + filename

	// Diverse range headers that exercise different code paths
	rangeHeaders := []string{
		"",              // full content (no range)
		"bytes=0-99",    // first 100 bytes
		"bytes=100-199", // middle chunk
		"bytes=-100",    // last 100 bytes
		"bytes=0-",      // from start to end
		fmt.Sprintf("bytes=0-%d", fileTotalSize-1),  // entire file as range
		fmt.Sprintf("bytes=%d-", fileTotalSize-100), // near the end
		"bytes=0-0",     // single byte
		"bytes=999999-", // out of bounds (expect 416)
		"bytes=invalid", // malformed (expect 416)
	}

	// Act: launch concurrent workers
	results := make(chan concurrentRangeResult, concurrentWorkers)
	var wg sync.WaitGroup

	for i := range concurrentWorkers {
		rangeHeader := rangeHeaders[i%len(rangeHeaders)]
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
			if rangeHeader != "" {
				req.Header.Set("Range", rangeHeader)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			results <- concurrentRangeResult{
				statusCode: rec.Code,
				err:        nil,
			}
		})
	}

	// Wait for all workers then close the results channel
	wg.Wait()
	close(results)

	// Assert: check each result in the main goroutine
	resultCount := 0
	for res := range results {
		resultCount++
		require.NoError(t, res.err, "Concurrent request must not return an error")
		assert.Contains(t, acceptableStatuses, res.statusCode,
			"Response status %d is not in the accepted set %v", res.statusCode, acceptableStatuses)
	}
	assert.Equal(t, concurrentWorkers, resultCount,
		"Must receive a result from every concurrent worker")
}

// TestAudioETagInvalidatesStaleCache validates the fix for #2846. The root cause
// is browser cache poisoning: before the gzip fix (#2709), Chrome cached audio
// with compressed Content-Length. After the fix, http.ServeContent returns 304
// for If-Modified-Since matches, ignoring Range headers entirely. Chrome then
// uses its stale cache with a wrong Content-Length, causing 416 on subsequent
// range requests. The fix adds an ETag incorporating file size so that stale
// cached entries (with a different size from the gzipped era) are invalidated.
func TestAudioETagInvalidatesStaleCache(t *testing.T) {
	t.Parallel()
	t.Attr("component", "media")
	t.Attr("type", "regression")
	t.Attr("issue", "2846")

	e, controller, tempDir := setupMediaTestEnvironment(t)

	const filename = "etag-cache-test.wav"
	const dataSize = 2048
	filePath := filepath.Join(tempDir, filename)
	createLargeWAVFile(t, filePath, dataSize)

	totalSize := int64(dataSize + 44) // WAV header is 44 bytes

	// Step 1: Initial request, capture ETag and Last-Modified
	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+filename, http.NoBody)
	rec := httptest.NewRecorder()
	c := e.NewContext(req, rec)
	c.SetParamNames("filename")
	c.SetParamValues(filename)

	err := controller.ServeAudioClip(c)
	require.NoError(t, err)
	require.Equal(t, http.StatusOK, rec.Code)

	etag := rec.Header().Get("ETag")
	lastModified := rec.Header().Get("Last-Modified")
	require.NotEmpty(t, etag, "Response must include an ETag header")
	require.NotEmpty(t, lastModified, "Response must include a Last-Modified header")
	t.Logf("Initial: ETag=%s Last-Modified=%s Content-Length=%d", etag, lastModified, totalSize)

	// Step 2: Conditional request with matching If-None-Match returns 304
	req2 := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+filename, http.NoBody)
	req2.Header.Set("If-None-Match", etag)
	rec2 := httptest.NewRecorder()
	c2 := e.NewContext(req2, rec2)
	c2.SetParamNames("filename")
	c2.SetParamValues(filename)

	err = controller.ServeAudioClip(c2)
	require.NoError(t, err)
	assert.Equal(t, http.StatusNotModified, rec2.Code, "Matching ETag should return 304")

	// Step 3: Conditional request with a STALE ETag (simulating a cached gzipped
	// response that had a different file size) should return 200 with fresh content.
	staleETag := `"cafebabe-deadbeef"`
	req3 := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+filename, http.NoBody)
	req3.Header.Set("If-None-Match", staleETag)
	rec3 := httptest.NewRecorder()
	c3 := e.NewContext(req3, rec3)
	c3.SetParamNames("filename")
	c3.SetParamValues(filename)

	err = controller.ServeAudioClip(c3)
	require.NoError(t, err)
	assert.Equal(t, http.StatusOK, rec3.Code, "Stale ETag should get 200, not 304")
	assert.Equal(t, totalSize, int64(rec3.Body.Len()), "Fresh response should contain full file")

	// Step 4: Conditional request with If-None-Match + Range (Chrome's actual
	// pattern). With matching ETag, this should return 304 (use cache). With
	// stale ETag, this should return 206 (fresh partial content).
	req4 := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+filename, http.NoBody)
	req4.Header.Set("If-None-Match", staleETag)
	req4.Header.Set("Range", "bytes=0-99")
	rec4 := httptest.NewRecorder()
	c4 := e.NewContext(req4, rec4)
	c4.SetParamNames("filename")
	c4.SetParamValues(filename)

	err = controller.ServeAudioClip(c4)
	require.NoError(t, err)
	assert.Equal(t, http.StatusPartialContent, rec4.Code,
		"Stale ETag + Range should return 206 (fresh partial content), not 304")
	assert.Equal(t, 100, rec4.Body.Len(), "Partial content should be 100 bytes")
}
