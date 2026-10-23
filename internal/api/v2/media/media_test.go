// media_test.go: Package api provides tests for API v2 media endpoints.

package media

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/api/middleware"
	"github.com/tphakala/birdnet-go/internal/api/v2/apitest"
	"github.com/tphakala/birdnet-go/internal/audiocore/audiotemp"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/datastore/mocks"
	"github.com/tphakala/birdnet-go/internal/diskmanager"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
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

const testAudioAsyncTimeout = 5 * time.Second

func requireAudioWaitStarted(t *testing.T, waitStarted <-chan struct{}, handlerErr <-chan error) {
	t.Helper()

	select {
	case <-waitStarted:
		return
	case err := <-handlerErr:
		require.Failf(t, "handler returned before entering audio wait", "handler error: %v", err)
	case <-time.After(testAudioAsyncTimeout):
		require.Fail(t, "handler did not enter audio wait before timeout")
	}
}

func requireAudioHandlerSuccess(t *testing.T, handlerErr <-chan error) {
	t.Helper()

	select {
	case err := <-handlerErr:
		require.NoError(t, err)
	case <-time.After(testAudioAsyncTimeout):
		require.Fail(t, "audio handler did not complete before timeout")
	}
}

// Rate and silent-clip size of the default audio fixture: 0.1 s of 16-bit mono,
// which with the 44-byte WAV header is 8864 bytes.
const (
	testAudioRate     = 44100
	testAudioDataSize = testAudioRate / 10 * 2
)

// createLargeWAVFile creates a WAV file whose data chunk is a dataSize-byte
// 0..255 ramp, for testing partial content responses.
func createLargeWAVFile(t *testing.T, path string, dataSize int) {
	t.Helper()
	ramp := make([]byte, dataSize)
	for i := range ramp {
		ramp[i] = byte(i % 256)
	}
	writePCM16WAV(t, path, testAudioRate, ramp)
}

// createTestAudioFile creates a valid, silent WAV file of testAudioDataSize PCM
// bytes that meets the minimum size requirements.
func createTestAudioFile(t *testing.T, path string) {
	t.Helper()
	writePCM16WAV(t, path, testAudioRate, make([]byte, testAudioDataSize))
}

// TestServeAudioClip tests the ServeAudioClip handler using SecureFS
func TestServeAudioClip(t *testing.T) {
	// Setup test environment with SecureFS rooted in tempDir
	e, _, tempDir := setupMediaTestEnvironment(t)

	// Create a small test audio file within the secure root
	smallFilename := "small.wav"
	smallFilePath := filepath.Join(tempDir, smallFilename)
	createTestAudioFile(t, smallFilePath)

	// Create a large test audio file (over 1MB)
	largeFilename := "large.wav"
	largeFilePath := filepath.Join(tempDir, largeFilename)
	createLargeWAVFile(t, largeFilePath, 1100*1024)

	standardClipFilename := "corvus_corax_74p_20260724T132919Z.wav"
	createTestAudioFile(t, filepath.Join(tempDir, standardClipFilename))
	extendedClipFilename := "strix_uralensis_85p_20260724T231500Z_86s.wav"
	createTestAudioFile(t, filepath.Join(tempDir, extendedClipFilename))
	spacedFilename := "my clip.wav"
	createTestAudioFile(t, filepath.Join(tempDir, spacedFilename))
	cyrillicFilename := "тест.wav"
	createTestAudioFile(t, filepath.Join(tempDir, cyrillicFilename))
	umlautFilename := "Prüfung.wav"
	createTestAudioFile(t, filepath.Join(tempDir, umlautFilename))

	// Test cases
	testCases := []struct {
		name                string
		filename            string // Filename relative to SecureFS root
		rangeHeader         string
		expectedStatus      int
		expectedLength      int64 // Use int64 for http.ServeContent
		partialContent      bool
		expectedDisposition string
		wantBody            string // substring of the error body, checked when set
	}{
		{
			name:                "Small file - full content",
			filename:            smallFilename,
			rangeHeader:         "",
			expectedStatus:      http.StatusOK,
			expectedLength:      8864, // WAV header (44) + audio data (8820)
			partialContent:      false,
			expectedDisposition: "inline; filename*=UTF-8''small.wav",
		},
		{
			name:                "Large file - full content",
			filename:            largeFilename,
			rangeHeader:         "",
			expectedStatus:      http.StatusOK,
			expectedLength:      1100*1024 + 44, // Data size + WAV header
			partialContent:      false,
			expectedDisposition: "inline; filename*=UTF-8''large.wav",
		},
		{
			name:                "Large file - partial content",
			filename:            largeFilename,
			rangeHeader:         "bytes=100-199",
			expectedStatus:      http.StatusPartialContent,
			expectedLength:      100,
			partialContent:      true,
			expectedDisposition: "inline; filename*=UTF-8''large.wav",
		},
		{
			name:                "Standard clip filename omits misleading UTC marker",
			filename:            standardClipFilename,
			expectedStatus:      http.StatusOK,
			expectedLength:      8864,
			expectedDisposition: "inline; filename*=UTF-8''corvus_corax_74p_20260724T132919.wav",
		},
		{
			name:                "Extended clip filename omits misleading UTC marker",
			filename:            extendedClipFilename,
			expectedStatus:      http.StatusOK,
			expectedLength:      8864,
			expectedDisposition: "inline; filename*=UTF-8''strix_uralensis_85p_20260724T231500_86s.wav",
		},
		{
			name:                "Filename spaces use RFC 5987 encoding",
			filename:            spacedFilename,
			expectedStatus:      http.StatusOK,
			expectedLength:      8864,
			expectedDisposition: "inline; filename*=UTF-8''my%20clip.wav",
		},
		{
			name:                "Cyrillic filename is percent-encoded UTF-8",
			filename:            cyrillicFilename,
			expectedStatus:      http.StatusOK,
			expectedLength:      8864,
			expectedDisposition: "inline; filename*=UTF-8''%D1%82%D0%B5%D1%81%D1%82.wav",
		},
		{
			name:                "Umlaut filename is percent-encoded UTF-8",
			filename:            umlautFilename,
			expectedStatus:      http.StatusOK,
			expectedLength:      8864,
			expectedDisposition: "inline; filename*=UTF-8''Pr%C3%BCfung.wav",
		},
		{
			name:           "Non-existent file",
			filename:       "nonexistent.mp3",
			rangeHeader:    "",
			expectedStatus: http.StatusNotFound,
			expectedLength: 0,
			partialContent: false,
			wantBody:       "File not found",
		},
		{
			name:           "Invalid filename (traversal attempt)",
			filename:       "../../../etc/passwd",
			rangeHeader:    "",
			expectedStatus: http.StatusBadRequest, // SecureFS correctly detects and blocks traversal attempts
			expectedLength: 0,
			partialContent: false,
			wantBody:       "Invalid file path",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			targetURL := "/api/v2/media/audio/" + strings.ReplaceAll(tc.filename, " ", "%20")
			req := httptest.NewRequest(http.MethodGet, targetURL, http.NoBody)
			if tc.rangeHeader != "" {
				req.Header.Set("Range", tc.rangeHeader)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)

			isSmallFile := tc.filename == smallFilename
			assertAudioClipResponse(t, rec, tc.expectedStatus, tc.expectedLength, tc.partialContent, isSmallFile)
			if tc.wantBody != "" {
				assert.Contains(t, rec.Body.String(), tc.wantBody)
			}
			assert.Equal(t, tc.expectedDisposition, rec.Header().Get("Content-Disposition"))
		})
	}
}

// TestServeSpectrogram tests the ServeSpectrogram handler using SecureFS,
// including on-demand rendering by the built-in renderer.
func TestServeSpectrogram(t *testing.T) {
	// Setup test environment with SecureFS rooted in tempDir
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file within the secure root
	audioFilename := "audio.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	createTestAudioFile(t, audioFilePath)

	// --- Simulate Spectrogram Generation (by creating the expected file) ---
	// This allows testing the "file exists" path without rendering.
	// Default is raw=true, so the filename format is audio_800px.png
	spectrogramFilename := "audio_800px.png"
	spectrogramFilePath := filepath.Join(tempDir, spectrogramFilename)
	spectrogramContent := "simulated spectrogram content"
	err := os.WriteFile(spectrogramFilePath, []byte(spectrogramContent), 0o600)
	require.NoError(t, err)

	// Test cases
	testCases := []struct {
		name           string
		filename       string // Filename relative to SecureFS root
		width          string
		expectedStatus int
		expectedBody   string
		expectPNG      bool // body is a freshly rendered PNG, not a fixed string
	}{
		{
			name:           "Spectrogram generated/exists",
			filename:       audioFilename,
			width:          "800",
			expectedStatus: http.StatusOK,
			expectedBody:   spectrogramContent,
		},
		{
			name:           "Spectrogram renders on demand (file doesn't exist initially)",
			filename:       audioFilename,
			width:          "1200", // Different width means different file
			expectedStatus: http.StatusOK,
			expectPNG:      true,
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
				if tc.expectPNG {
					assert.True(t, bytes.HasPrefix(rec.Body.Bytes(), []byte(pngSignature)), "body must be a PNG")
				} else {
					assert.Equal(t, tc.expectedBody, rec.Body.String())
				}
				assert.Equal(t, "image/png", rec.Header().Get("Content-Type"))
			}
		})
	}
}

// setupMediaTestEnvironment builds a media Handler wired to a test core (with an
// isolated SecureFS export root) and registers the media routes. It returns the
// echo instance, the handler, and the SecureFS root directory tests write their
// fixture files into. A passthrough auth middleware is injected so the
// authenticated ID-based routes (clip extraction / audio processing) register and
// can be exercised without a real auth service.
//
// opts are appended to the core options. Tests that call t.Parallel() must pass
// apitest.WithoutSettingsPublish(): publishing replaces the process-global
// settings snapshot and restores the previous one in Cleanup, so parallel
// publishers restore each other's snapshots.
func setupMediaTestEnvironment(t *testing.T, opts ...apitest.CoreOption) (*echo.Echo, *Handler, string) {
	t.Helper()

	e := echo.New()
	core := apitest.NewCore(t, append([]apitest.CoreOption{apitest.WithEcho(e)}, opts...)...)
	// Passthrough auth so the authenticated clip/process routes register and run
	// without a real auth service.
	core.AuthMiddleware = passthroughMiddleware()

	h := New(core)
	h.RegisterRoutes(core.Group)

	// The media SecureFS is rooted at the per-test export path apitest.NewCore
	// provisions (a t.TempDir()); tests create their fixture files under it.
	return e, h, core.SFS.BaseDir()
}

// TestServeAudio_AlternateExtensionFallback verifies the extension-agnostic
// recovery on the audio serve path: when the requested clip name's exact file is
// missing but a sibling with a different known audio extension exists (a rare
// clip_name/on-disk drift), the actual file is served and the response is labeled
// by the file served, not by the requested extension.
func TestServeAudio_AlternateExtensionFallback(t *testing.T) {
	e, _, tempDir := setupMediaTestEnvironment(t)

	// The real file on disk is FLAC (createTestAudioFile writes valid audio bytes;
	// the serve path only cares about the extension for labeling).
	createTestAudioFile(t, filepath.Join(tempDir, "drifted.flac"))

	server := httptest.NewServer(e)
	defer server.Close()

	// The request (mirroring a stored clip_name) names .mp3, which does not exist.
	resp, err := server.Client().Get(server.URL + "/api/v2/media/audio/drifted.mp3")
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	assert.Equal(t, http.StatusOK, resp.StatusCode,
		"the actual on-disk file must be served instead of a 404")
	assert.Equal(t, MimeTypeFLAC, resp.Header.Get(echo.HeaderContentType),
		"Content-Type must follow the file served, not the requested extension")
	body, readErr := io.ReadAll(resp.Body)
	require.NoError(t, readErr)
	assert.NotEmpty(t, body, "the served alternate file must have a non-empty body")

	// A genuinely absent clip (no sibling in any known audio format) still 404s.
	resp2, err := server.Client().Get(server.URL + "/api/v2/media/audio/nonexistent.mp3")
	require.NoError(t, err)
	defer func() { _ = resp2.Body.Close() }()
	assert.Equal(t, http.StatusNotFound, resp2.StatusCode,
		"a clip with no on-disk file in any format must still 404")
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
	createTestAudioFile(t, secureFilePath)

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

	// Closed and suffix slices are covered by TestServeAudioClip, the gzip
	// end-to-end test and TestAudioConcurrentRangeRequests; these rows pin the
	// full body, an open-ended range and the two kinds of unsatisfiable range.
	testCases := []struct {
		name                 string
		rangeHeader          string
		expectedStatus       int
		expectedBody         []byte // checked for 200 and 206
		expectedContentRange string // checked when set
	}{
		{name: "No range header", expectedStatus: http.StatusOK, expectedBody: fileContent},
		{
			name:                 "Range: bytes=924-",
			rangeHeader:          "bytes=924-",
			expectedStatus:       http.StatusPartialContent,
			expectedBody:         fileContent[924:],
			expectedContentRange: "bytes 924-1023/1024",
		},
		{
			name:           "Invalid range format (bytes=invalid)",
			rangeHeader:    "bytes=invalid",
			expectedStatus: http.StatusRequestedRangeNotSatisfiable,
		},
		{
			name:                 "Invalid range (out of bounds)",
			rangeHeader:          "bytes=2000-",
			expectedStatus:       http.StatusRequestedRangeNotSatisfiable,
			expectedContentRange: "bytes */1024", // an out-of-bounds 416 reports the current size
		},
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

			require.NoError(t, controller.ServeAudioClip(c))
			assert.Equal(t, tc.expectedStatus, rec.Code)
			if tc.expectedBody != nil {
				assert.Equal(t, tc.expectedBody, rec.Body.Bytes())
			} else {
				// http.ServeContent answers with a short error text, so the body is
				// not empty; what a 416 must never carry is the file itself.
				const probeLen = 64
				assert.False(t, bytes.Contains(rec.Body.Bytes(), fileContent[:probeLen]),
					"416 responses should not include the file body")
			}
			if tc.expectedContentRange != "" {
				assert.Equal(t, tc.expectedContentRange, rec.Header().Get("Content-Range"))
			}
		})
	}
}

// TestParseRawParameter pins every spelling of the raw query parameter. The
// handler tests below only check that the parsed value reaches the file name.
func TestParseRawParameter(t *testing.T) {
	t.Parallel()

	tests := []struct {
		in   string
		want bool
	}{
		{"", true}, {"true", true}, {"false", false}, {"1", true}, {"0", false},
		{"t", true}, {"f", false}, {"TRUE", true}, {"False", false},
		{"yes", true}, {"YES", true}, {"no", false}, {"No", false},
		{"on", true}, {"off", false},
		{"invalid", true}, // unknown values keep the backward-compatible default
	}
	for _, tt := range tests {
		t.Run(tt.in, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, parseRawParameter(tt.in))
		})
	}
}

// TestServeSpectrogramRawParameter checks that the filename route passes the
// parsed raw parameter to the file lookup: raw selects the plain PNG, raw=false
// the legend PNG. The spellings are covered by TestParseRawParameter.
func TestServeSpectrogramRawParameter(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file within the secure root
	audioFilename := "rawtest.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	createTestAudioFile(t, audioFilePath)

	// Simulate raw spectrogram (default behavior)
	rawSpectrogramFilename := "rawtest_800px.png"
	rawSpectrogramPath := filepath.Join(tempDir, rawSpectrogramFilename)
	err := os.WriteFile(rawSpectrogramPath, []byte("raw spectrogram"), 0o600)
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
			name:           "Explicit raw=false",
			rawParam:       "false",
			expectedStatus: http.StatusOK,
			expectedBody:   "legend spectrogram",
			description:    "Should generate spectrogram with legend",
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
	testFilename := "turdus_migratorius_88p_20240115T143045Z.wav"
	downloadFilename := "turdus_migratorius_88p_20240115T143045.wav"
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
			expectedDisposition:    fmt.Sprintf("inline; filename*=UTF-8''%s", downloadFilename),
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

func TestContentDispositionFilename(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		filename string
		expected string
	}{
		{
			name:     "standard local timestamp",
			filename: "corvus_corax_74p_20260724T132919Z.m4a",
			expected: "corvus_corax_74p_20260724T132919.m4a",
		},
		{
			name:     "extended capture duration",
			filename: "strix_uralensis_85p_20260724T231500Z_86s.wav",
			expected: "strix_uralensis_85p_20260724T231500_86s.wav",
		},
		{
			name:     "timestamp already has no zone marker",
			filename: "corvus_corax_74p_20260724T132919.m4a",
			expected: "corvus_corax_74p_20260724T132919.m4a",
		},
		{
			name:     "unrecognized filename preserves legitimate Z",
			filename: "recordingZ.m4a",
			expected: "recordingZ.m4a",
		},
		{
			name:     "custom UTC timestamp is unchanged",
			filename: "recording_20240115T143045Z.wav",
			expected: "recording_20240115T143045Z.wav",
		},
		{
			name:     "invalid timestamp is unchanged",
			filename: "corvus_corax_74p_20261399T999999Z.m4a",
			expected: "corvus_corax_74p_20261399T999999Z.m4a",
		},
	}

	for i := range tests {
		t.Run(tests[i].name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tests[i].expected, contentDispositionFilename(tests[i].filename))
		})
	}
}

func TestTranslateAudioServeErrorClearsHeadersBeforeCommittedJSON(t *testing.T) {
	e, controller, _ := setupMediaTestEnvironment(t)
	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/test.wav", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	ctx.Response().Header().Set(echo.HeaderContentType, MimeTypeWAV)
	ctx.Response().Header().Set(echo.HeaderContentDisposition, "inline; filename*=UTF-8''test.wav")
	ctx.Response().Header().Set(headerAcceptRanges, acceptRangesBytes)

	require.NoError(t, controller.translateAudioServeError(ctx, errors.NewStd("forced serve failure"), "Failed to serve audio"))
	assert.Equal(t, http.StatusInternalServerError, rec.Code)
	assert.Contains(t, rec.Header().Get(echo.HeaderContentType), echo.MIMEApplicationJSON)
	assert.Empty(t, rec.Header().Get(echo.HeaderContentDisposition))
	assert.Empty(t, rec.Header().Get(headerAcceptRanges))
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

			// Setup mock to return this filename - use numeric IDs since the handler
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

// TestServeSpectrogramByIDRawParameter checks that the ID route passes the parsed raw
// parameter to the file lookup. The spellings are covered by TestParseRawParameter.
func TestServeSpectrogramByIDRawParameter(t *testing.T) {
	// Setup test environment
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Create a test audio file
	testFilename := "test_raw_param.wav"
	filePath := filepath.Join(tempDir, testFilename)
	createTestAudioFile(t, filePath)

	// Simulate raw spectrogram (default behavior) using lg size (1026px)
	rawSpectrogramFilename := "test_raw_param_1026px.png"
	rawSpectrogramPath := filepath.Join(tempDir, rawSpectrogramFilename)
	err := os.WriteFile(rawSpectrogramPath, []byte("id raw spectrogram"), 0o600)
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
			name:           "Explicit raw=false",
			rawParam:       "false",
			expectedStatus: http.StatusOK,
			expectedBody:   "id legend spectrogram",
		},
		{
			// A spelling strconv.ParseBool rejects, so only the full parser maps it to false.
			name:           "Raw=no",
			rawParam:       "no",
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
//
// The negative cache entry is primed up front because the handlers no longer consult
// a provider on the request goroutine: a species nothing is known about yet answers
// 503 "not yet" (see TestSpeciesImageColdMiss_ReturnsPendingNot500) and only becomes
// a 404 once the lookup has actually concluded there is no image.
func TestSpeciesImageNotFound_Returns404(t *testing.T) {
	t.Attr("component", "media")
	t.Attr("type", "regression")
	t.Attr("issue", "2201")

	// Setup test environment - the default mock provider returns images for any species
	e := echo.New()
	controller := New(apitest.NewCore(t, apitest.WithEcho(e)))

	// Replace the mock provider with one that returns ErrImageNotFound for unknown species
	notFoundProvider := &apitest.TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
		},
	}
	controller.BirdImageCache.SetImageProvider(notFoundProvider)

	// Resolve the species once so the negative entry exists, making the handler
	// assertions below independent of background prefetch timing.
	_, err := controller.BirdImageCache.Get("Nonexistus fictus")
	require.ErrorIs(t, err, imageprovider.ErrImageNotFound)

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

// TestSpeciesImageColdMiss_ReturnsPendingNot500 pins the cold-miss contract that the
// whole de-blocking change rests on.
//
// A species nothing is cached for must answer immediately with 503 plus a Retry-After
// and, critically, Cache-Control: no-store. An <img> error event carries no status
// code, so no-store is the only thing that tells a retrying browser this failure is
// transient: a cacheable 404 is served from its own HTTP cache without a network hop,
// while this response is re-requested and can succeed once the background fetch lands.
//
// It must never be a 500. apicore reports every status >= 500 to Sentry, and a cold
// dashboard requests dozens of uncached thumbnails at once.
func TestSpeciesImageColdMiss_ReturnsPendingNot500(t *testing.T) {
	t.Attr("component", "media")

	e := echo.New()
	controller := New(apitest.NewCore(t, apitest.WithEcho(e)))

	// A provider that never returns lets the assertion prove the handler did not wait
	// for it: if the fetch were still on the request path, this test would hang.
	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	controller.BirdImageCache.SetImageProvider(&apitest.TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			<-blocked
			return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/image/Coldus%20missus", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("scientific_name")
	ctx.SetParamValues("Coldus missus")

	require.NoError(t, controller.ServeSpeciesImageProxy(ctx))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, strconv.Itoa(ImagePendingRetryAfterSeconds), rec.Header().Get("Retry-After"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"),
		"a pending image must not be cached, or the client retry never reaches the server")
	assert.Empty(t, rec.Header().Get("Location"),
		"the proxy is a hard boundary and must never redirect to the upstream image host")
}

// TestSpeciesImageColdMiss_ConcurrentRequestsAllReturnPromptly is the shape that
// actually reproduced the reported freeze: a dashboard asking for the same uncached
// species from several connections at once.
//
// Every one of them must answer immediately. Queueing them behind the first request's
// fetch is what exhausted the browser's per-host connection budget and starved
// unrelated API calls and the SSE stream.
func TestSpeciesImageColdMiss_ConcurrentRequestsAllReturnPromptly(t *testing.T) {
	t.Attr("component", "media")

	e := echo.New()
	controller := New(apitest.NewCore(t, apitest.WithEcho(e)))

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	controller.BirdImageCache.SetImageProvider(&apitest.TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			<-blocked
			return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
		},
	})

	const concurrency = 10
	codes := make(chan int, concurrency)
	var wg sync.WaitGroup
	for range concurrency {
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, "/api/v2/media/image/Coldus%20missus", http.NoBody)
			rec := httptest.NewRecorder()
			ctx := e.NewContext(req, rec)
			ctx.SetParamNames("scientific_name")
			ctx.SetParamValues("Coldus missus")
			_ = controller.ServeSpeciesImageProxy(ctx)
			codes <- rec.Code
		})
	}

	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("concurrent cold-miss requests queued behind the provider fetch")
	}

	close(codes)
	for code := range codes {
		assert.Equal(t, http.StatusServiceUnavailable, code)
	}

	// Prefetch deduplication is asserted where it lives, in
	// TestPrefetchAsync_DeduplicatesBySpecies. What this test owns is the handler
	// contract: every concurrent request answers immediately instead of queueing
	// behind one fetch, which is what exhausted the browser's connection budget.
}

// TestServeSpeciesImageProxy_NeverRedirects asserts the D2 boundary for a species that
// IS resolved but whose bytes are not on disk. This used to 302 to the provider's own
// URL, which handed clients an address whose availability this process does not
// control and which API clients (unlike browsers) were refused outright.
func TestServeSpeciesImageProxy_NeverRedirects(t *testing.T) {
	t.Attr("component", "media")

	e := echo.New()
	controller := New(apitest.NewCore(t, apitest.WithEcho(e)))
	controller.BirdImageCache.SetImageProvider(&apitest.TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			return imageprovider.BirdImage{
				URL:            "https://upload.wikimedia.org/does-not-matter.jpg",
				ScientificName: scientificName,
				SourceProvider: "wikimedia",
			}, nil
		},
	})

	// Resolve the metadata so the handler takes the "cached, but no bytes" branch.
	_, err := controller.BirdImageCache.Get("Turdus merula")
	require.NoError(t, err)

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/image/Turdus%20merula", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("scientific_name")
	ctx.SetParamValues("Turdus merula")

	require.NoError(t, controller.ServeSpeciesImageProxy(ctx))

	// Pin the positive contract, not just "not a 302": NotEqual(302) would also pass
	// for a 200 or a 500, so it cannot tell the boundary being held from the handler
	// having failed some other way.
	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.Empty(t, rec.Header().Get("Location"),
		"the proxy must never hand the client the upstream image host")
	assert.NotContains(t, rec.Body.String(), "upload.wikimedia.org",
		"the upstream URL must not leak to the client")
}

// TestGetSpeciesImageInfo_ColdMissReturnsPendingJSON covers the metadata endpoint's
// half of the same contract. It differs from the image endpoint deliberately: this one
// is consumed by fetch() rather than an <img>, so it must return a parseable body for
// a client that reads every response as JSON.
func TestGetSpeciesImageInfo_ColdMissReturnsPendingJSON(t *testing.T) {
	t.Attr("component", "media")

	e := echo.New()
	controller := New(apitest.NewCore(t, apitest.WithEcho(e)))

	blocked := make(chan struct{})
	t.Cleanup(func() { close(blocked) })
	controller.BirdImageCache.SetImageProvider(&apitest.TestImageProvider{
		FetchFunc: func(scientificName string) (imageprovider.BirdImage, error) {
			<-blocked
			return imageprovider.BirdImage{}, imageprovider.ErrImageNotFound
		},
	})

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/species-image/info?name=Coldus+missus", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)

	require.NoError(t, controller.GetSpeciesImageInfo(ctx))

	assert.Equal(t, http.StatusServiceUnavailable, rec.Code)
	assert.Equal(t, strconv.Itoa(ImagePendingRetryAfterSeconds), rec.Header().Get("Retry-After"))
	assert.Equal(t, "no-store", rec.Header().Get("Cache-Control"))
	assert.NotEmpty(t, rec.Body.String(), "a JSON endpoint must not answer with an empty body")
	var body map[string]any
	require.NoError(t, json.Unmarshal(rec.Body.Bytes(), &body))
}

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
		{"unique format", base + ".12345.1" + audiotemp.Ext, true},
		{"unique format large seq", base + ".987.4096" + audiotemp.Ext, true},
		{"pre-fix legacy format", base + audiotemp.Ext, true},
		{"final clip", base, false},
		{"final clip other ext", base + ".part", false},
		{"sidecar spectrogram temp", base + ".png.12345.1" + audiotemp.Ext, false},
		{"different clip", "other_99p_20260531T084138Z.m4a.12345.1" + audiotemp.Ext, false},
		{"non-integer pid", base + ".abc.1" + audiotemp.Ext, false},
		{"non-integer seq", base + ".12345.x" + audiotemp.Ext, false},
		{"missing seq", base + ".12345" + audiotemp.Ext, false},
		{"extra middle segment", base + ".12345.1.2" + audiotemp.Ext, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isExportTempFor(tt.file, base))
		})
	}
}

// TestServeAudioClipWaitsForEncoding verifies that when an audio file is being
// encoded (temp file exists), the server waits for the final file to appear
// instead of immediately returning 503.
func TestServeAudioClipWaitsForEncoding(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)
	controller.audioWaitTimeoutOverride = testAudioAsyncTimeout

	audioFilename := "encoding-test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	// Mirror the per-export unique temp name "<clip>.<pid>.<seq>.temp" the real
	// exporters write, so this exercises the directory-scan detection in
	// isAudioBeingEncoded rather than a fixed name that production never produces.
	tempFilePath := audioFilePath + ".99999.1" + audiotemp.Ext
	require.NoError(t, os.WriteFile(tempFilePath, []byte("temp encoding data"), 0o600))

	waitStarted := make(chan struct{})
	controller.audioWaitStartedHook = func() {
		close(waitStarted)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/media/audio/:filename")
	ctx.SetParamNames("filename")
	ctx.SetParamValues(audioFilename)

	handlerErr := make(chan error, 1)
	go func() {
		handlerErr <- controller.ServeAudioClip(ctx)
	}()

	requireAudioWaitStarted(t, waitStarted, handlerErr)
	createTestAudioFile(t, audioFilePath)
	require.NoError(t, os.Remove(tempFilePath))
	requireAudioHandlerSuccess(t, handlerErr)
	assert.Equal(t, http.StatusOK, rec.Code,
		"Should serve the file successfully after waiting for encoding")
}

// TestServeAudioByIDWaitsForEncodingPreservesHeaders verifies that the ID route
// keeps its audio metadata when the initial 404 is recovered by a successful
// encoding wait and retry.
func TestServeAudioByIDWaitsForEncodingPreservesHeaders(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)
	controller.audioWaitTimeoutOverride = testAudioAsyncTimeout

	audioFilename := "turdus_migratorius_88p_20260724T143045Z.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	tempFilePath := audioFilePath + ".99999.1" + audiotemp.Ext
	require.NoError(t, os.WriteFile(tempFilePath, []byte("temp encoding data"), 0o600))

	mockDS := mocks.NewMockInterface(t)
	mockDS.On("GetNoteClipPath", "42").Return(audioFilename, nil)
	mockDS.On("Get", "42").Return(datastore.Note{ID: 42, EndTime: time.Now()}, nil).Maybe()
	controller.DS = mockDS

	waitStarted := make(chan struct{})
	controller.audioWaitStartedHook = func() {
		close(waitStarted)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/42", http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetParamNames("id")
	ctx.SetParamValues("42")

	handlerErr := make(chan error, 1)
	go func() {
		handlerErr <- controller.ServeAudioByID(ctx)
	}()

	requireAudioWaitStarted(t, waitStarted, handlerErr)
	createTestAudioFile(t, audioFilePath)
	require.NoError(t, os.Remove(tempFilePath))
	requireAudioHandlerSuccess(t, handlerErr)
	assert.Equal(t, http.StatusOK, rec.Code)
	assertAudioHeaders(t, rec, MimeTypeWAV,
		"inline; filename*=UTF-8''turdus_migratorius_88p_20260724T143045.wav", true)
}

// TestServeAudioClipReturns503AfterTimeout verifies that the server returns 503
// with Retry-After header when encoding doesn't complete within the timeout.
func TestServeAudioClipReturns503AfterTimeout(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	// Inject a short server-side wait so the 503-after-timeout path is exercised
	// quickly. The final file never appears, so the wait deterministically expires
	// regardless of the timeout value; the override only bounds the wait (default
	// would be audioWaitTimeout).
	controller.audioWaitTimeoutOverride = testFailFastTimeout

	audioFilename := "slow-encoding.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	// Mirror the per-export unique temp name "<clip>.<pid>.<seq>.temp" the real
	// exporters write, so this exercises the directory-scan detection in
	// isAudioBeingEncoded rather than a fixed name that production never produces.
	tempFilePath := audioFilePath + ".99999.1" + audiotemp.Ext

	// Create only the temp file; the final file never appears.
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
	assert.Empty(t, rec.Header().Get("Content-Disposition"),
		"JSON error response should not advertise an audio filename")
}

// TestIsRecentClipCompletion covers the recency predicate shared with the
// reconcile crawler: a zero completion time is treated as recent (fail-safe),
// times inside the window are recent, and times older than the window are not.
func TestIsRecentClipCompletion(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name    string
		endTime time.Time
		want    bool
	}{
		{name: "zero time is recent (unknown, fail-safe)", endTime: time.Time{}, want: true},
		{name: "just now is recent", endTime: time.Now(), want: true},
		{name: "inside window is recent", endTime: time.Now().Add(-diskmanager.ClipRecencyWindow / 2), want: true},
		{name: "older than window is not recent", endTime: time.Now().Add(-2 * diskmanager.ClipRecencyWindow), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			assert.Equal(t, tt.want, isRecentClipCompletion(tt.endTime))
		})
	}
}

// TestServeAudioByID_GraceSkippedForOldDetections verifies the defense-in-depth
// grace-poll skip: a 404 for a detection whose capture completed long ago returns
// at once (no grace wait), while a recent detection still waits out the grace
// period and then answers 503 with Retry-After. It runs under synctest, so the
// elapsed time is the exact fake-clock duration: zero for the old detection and
// audioGracePeriod for the recent one.
func TestServeAudioByID_GraceSkippedForOldDetections(t *testing.T) {
	// clipPath resolves but the file does not exist on disk, forcing the 404 path.
	const missingClip = "2024/01/ghost_95p_20240101T000000Z.wav"

	tests := []struct {
		name        string
		age         time.Duration // how long before now the capture completed
		wantStatus  int
		wantElapsed time.Duration
	}{
		{
			name:        "old detection skips grace and 404s immediately",
			age:         time.Hour,
			wantStatus:  http.StatusNotFound,
			wantElapsed: 0,
		},
		{
			name:        "recent detection waits the grace period and answers 503 while the export may be queued",
			age:         0,
			wantStatus:  http.StatusServiceUnavailable,
			wantElapsed: audioGracePeriod,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			e, controller, _ := setupMediaTestEnvironment(t)

			synctest.Test(t, func(t *testing.T) {
				// The end time is taken inside the bubble: the bubble clock starts at
				// 2000-01-01 UTC (testing/synctest docs), so a time.Now() taken outside
				// it would lie decades in the bubble's future and every row would count
				// as recent.
				mockDS := mocks.NewMockInterface(t)
				mockDS.On("GetNoteClipPath", "42").Return(missingClip, nil)
				mockDS.On("Get", "42").Return(datastore.Note{ID: 42, EndTime: time.Now().Add(-tc.age)}, nil).Maybe()
				controller.DS = mockDS

				req := httptest.NewRequest(http.MethodGet, "/api/v2/audio/42", http.NoBody)
				rec := httptest.NewRecorder()
				c := e.NewContext(req, rec)
				c.SetParamNames("id")
				c.SetParamValues("42")

				start := time.Now()
				handlerErr := controller.ServeAudioByID(c)
				elapsed := time.Since(start)

				// A missing clip resolves to an echo.HTTPError (the framework turns it
				// into the response); a pending answer is written directly.
				code := rec.Code
				if httpErr, ok := errors.AsType[*echo.HTTPError](handlerErr); ok {
					code = httpErr.Code
				}
				assert.Equal(t, tc.wantStatus, code)
				assert.Equal(t, tc.wantElapsed, elapsed)
				if tc.wantStatus == http.StatusServiceUnavailable {
					require.NoError(t, handlerErr)
					assert.NotEmpty(t, rec.Header().Get(echo.HeaderRetryAfter), "503 must carry Retry-After")
				}
			})
		})
	}
}

// TestServeAudioClipGraceWaitServesFile verifies that when an audio file
// appears shortly after the initial request (no temp file visible), the
// grace period wait catches it instead of returning 404. This covers the
// race condition from issue #2355 where the detection DB record is committed
// but the export hasn't created its temp file yet.
func TestServeAudioClipGraceWaitServesFile(t *testing.T) {
	e, controller, tempDir := setupMediaTestEnvironment(t)

	audioFilename := "grace-wait-test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)

	waitStarted := make(chan struct{})
	controller.audioWaitStartedHook = func() {
		close(waitStarted)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v2/media/audio/"+audioFilename, http.NoBody)
	rec := httptest.NewRecorder()
	ctx := e.NewContext(req, rec)
	ctx.SetPath("/api/v2/media/audio/:filename")
	ctx.SetParamNames("filename")
	ctx.SetParamValues(audioFilename)

	handlerErr := make(chan error, 1)
	go func() {
		handlerErr <- controller.ServeAudioClip(ctx)
	}()

	requireAudioWaitStarted(t, waitStarted, handlerErr)
	createTestAudioFile(t, audioFilePath)
	requireAudioHandlerSuccess(t, handlerErr)
	assert.Equal(t, http.StatusOK, rec.Code,
		"Should serve the file successfully after grace wait")
	assert.Equal(t, "inline; filename*=UTF-8''grace-wait-test.wav", rec.Header().Get(echo.HeaderContentDisposition))
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
		freqSuffix       string
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
			name:             "bat profile suffix raw",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              true,
			style:            "default",
			dynamicRange:     "100",
			freqSuffix:       "bat",
			expectedFilename: "bird_1026px-bat.png",
		},
		{
			name:             "bat profile suffix with legend and style",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            1026,
			raw:              false,
			style:            "scientific_dark",
			dynamicRange:     "100",
			freqSuffix:       "bat",
			expectedFilename: "bird_1026px-scientific_dark-bat-legend.png",
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
			name:             "high_contrast_dark with non-default DR raw",
			relAudioPath:     "clips/2025/01/bird.wav",
			width:            514,
			raw:              true,
			style:            "high_contrast_dark",
			dynamicRange:     "80",
			expectedFilename: "bird_514px-high_contrast_dark-dr80.png",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, _, filename, fullPath := buildSpectrogramPaths(tt.relAudioPath, tt.width, tt.raw, tt.style, tt.dynamicRange, tt.freqSuffix)
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
	e, _, tempDir := setupMediaTestEnvironment(t, apitest.WithoutSettingsPublish())
	e.Use(middleware.NewGzip())

	audioFilename := "gzip-range-test.wav"
	audioFilePath := filepath.Join(tempDir, audioFilename)
	createTestAudioFile(t, audioFilePath)

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
	e, _, tempDir := setupMediaTestEnvironment(t, apitest.WithoutSettingsPublish())
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

	// Step 2: Replace the file with a smaller one (simulates a file being
	// re-exported at a different length, or a different clip replacing it)
	err := os.Remove(filePath)
	require.NoError(t, err, "Must be able to remove the original file")
	createLargeWAVFile(t, filePath, smallDataSize)

	// Step 3: Send a range request built from the OLD (larger) content-length.
	// The range now extends beyond the new file, so the server must reject
	// it with 416 Range Not Satisfiable.
	staleRangeHeader := fmt.Sprintf("bytes=%d-%d", smallFileSize, largeFileSize-1)
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

// TestAudioConcurrentRangeRequests verifies that the audio endpoint handles
// simultaneous range requests to the same file without data corruption or
// races. Each response must carry the exact status for its Range header and, for
// 2xx responses, exactly the bytes of the requested slice of the file. Results
// are collected via a channel so all assertions run in the main goroutine
// (required by testify). Run under -race, it also fails on unsynchronized state
// shared between requests.
func TestAudioConcurrentRangeRequests(t *testing.T) {
	t.Parallel()
	t.Attr("component", "media")
	t.Attr("type", "concurrency")
	t.Attr("issue", "2846")

	const (
		concurrentWorkers = 20
		fileDataSize      = 100000
	)

	// Arrange
	e, _, tempDir := setupMediaTestEnvironment(t, apitest.WithoutSettingsPublish())
	filename := "concurrent-range-test.wav"
	filePath := filepath.Join(tempDir, filename)
	createLargeWAVFile(t, filePath, fileDataSize)
	// The file as written is the oracle for every expected body.
	fileBytes, err := os.ReadFile(filePath) //nolint:gosec // G304: path is under t.TempDir()
	require.NoError(t, err)

	audioURL := "/api/v2/media/audio/" + filename

	// Diverse range headers that exercise different code paths. wantBody is nil
	// for 416 rows, whose body is an error text and not part of the contract.
	rangeCases := []struct {
		rangeHeader string
		wantStatus  int
		wantBody    []byte
	}{
		{"", http.StatusOK, fileBytes},
		{"bytes=0-99", http.StatusPartialContent, fileBytes[:100]},
		{"bytes=100-199", http.StatusPartialContent, fileBytes[100:200]},
		{"bytes=-100", http.StatusPartialContent, fileBytes[len(fileBytes)-100:]},
		{"bytes=0-0", http.StatusPartialContent, fileBytes[:1]},
		{"bytes=999999-", http.StatusRequestedRangeNotSatisfiable, nil},
		{"bytes=invalid", http.StatusRequestedRangeNotSatisfiable, nil},
	}

	type result struct {
		caseIdx int
		code    int
		body    []byte
	}

	// Act: launch concurrent workers
	results := make(chan result, concurrentWorkers)
	var wg sync.WaitGroup

	for i := range concurrentWorkers {
		caseIdx := i % len(rangeCases)
		wg.Go(func() {
			req := httptest.NewRequest(http.MethodGet, audioURL, http.NoBody)
			if h := rangeCases[caseIdx].rangeHeader; h != "" {
				req.Header.Set("Range", h)
			}
			rec := httptest.NewRecorder()
			e.ServeHTTP(rec, req)
			results <- result{caseIdx: caseIdx, code: rec.Code, body: rec.Body.Bytes()}
		})
	}

	// Wait for all workers then close the results channel
	wg.Wait()
	close(results)

	// Assert: check each result in the main goroutine
	resultCount := 0
	for res := range results {
		resultCount++
		tc := rangeCases[res.caseIdx]
		assert.Equal(t, tc.wantStatus, res.code, "status for Range %q", tc.rangeHeader)
		if tc.wantBody != nil {
			assert.Equal(t, tc.wantBody, res.body, "body for Range %q", tc.rangeHeader)
		}
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

	e, controller, tempDir := setupMediaTestEnvironment(t, apitest.WithoutSettingsPublish())

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
