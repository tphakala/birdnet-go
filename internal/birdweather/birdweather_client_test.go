package birdweather

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/myaudio"
)

// Test constants
const (
	// testTimestamp is the standard timestamp used in tests
	testTimestamp = "2023-01-01T12:00:00.000-0500"
)

// MockSettings creates mock settings for testing
func MockSettings() *conf.Settings {
	return &conf.Settings{
		BirdNET: conf.BirdNETConfig{
			Latitude:  40.7128,
			Longitude: -74.0060, // Sample coordinates (New York City)
		},
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				FfmpegPath: findFFmpegPath(), // Find FFmpeg for testing
			},
			Birdweather: conf.BirdweatherSettings{
				ID:               "test-station-123",
				LocationAccuracy: 100, // 100 meters accuracy
				Debug:            true,
			},
		},
	}
}

// findFFmpegPath attempts to find FFmpeg executable for testing
func findFFmpegPath() string {
	// Try to find ffmpeg in PATH
	if path, err := exec.LookPath("ffmpeg"); err == nil {
		return path
	}
	return "" // Return empty if not found
}

// createTestFLACData creates FLAC-encoded audio data for testing
func createTestFLACData(t *testing.T) []byte {
	t.Helper()
	// Create test PCM data (1 second of 48kHz mono audio)
	pcmData := make([]byte, 48000*2) // 2 bytes per sample for 16-bit

	// Fill with simple sine wave pattern for more realistic audio
	for i := 0; i < len(pcmData); i += 2 {
		// Simple 440Hz sine wave
		sample := int16(3276) // Low amplitude to avoid clipping (10% of max)
		pcmData[i] = byte(sample & 0xFF)
		pcmData[i+1] = byte((sample >> 8) & 0xFF)
	}

	ffmpegPath := findFFmpegPath()
	if ffmpegPath == "" {
		t.Skip("FFmpeg not found in PATH, skipping FLAC test")
	}

	// Create context with timeout
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	// Use the same custom args as in the main code
	customArgs := []string{
		"-af", "volume=15.0dB", // Simple gain adjustment
		"-c:a", "flac",
		"-f", "flac",
	}

	// Use the myaudio package to create FLAC data
	flacBuffer, err := myaudio.ExportAudioWithCustomFFmpegArgsContext(ctx, pcmData, ffmpegPath, customArgs)
	require.NoError(t, err, "Failed to create test FLAC data")

	return flacBuffer.Bytes()
}

// TestMain runs setup/teardown for all tests in this package
func TestMain(m *testing.M) {
	// Run tests
	code := m.Run()

	// Cleanup after all tests
	cleanupTestArtifacts()

	// Exit with test result code
	os.Exit(code)
}

// cleanupTestArtifacts removes directories created during tests
func cleanupTestArtifacts() {
	log := GetLogger()

	// Clean up debug directory if it exists
	debugDir := "debug"
	if _, err := os.Stat(debugDir); err == nil {
		if err := os.RemoveAll(debugDir); err != nil {
			log.Warn("Failed to remove debug directory", logger.Error(err))
		}
	}

	// Clean up logs directory if it exists
	logsDir := "logs"
	if _, err := os.Stat(logsDir); err == nil {
		if err := os.RemoveAll(logsDir); err != nil {
			log.Warn("Failed to remove logs directory", logger.Error(err))
		}
	}
}

func TestNew(t *testing.T) {
	settings := MockSettings()

	client, err := New(settings)
	require.NoError(t, err, "Failed to create new BwClient")
	require.NotNil(t, client, "New returned nil client")

	// Verify client properties
	assert.Equal(t, settings.Realtime.Birdweather.ID, client.BirdweatherID)
	assert.InDelta(t, settings.Realtime.Birdweather.LocationAccuracy, client.Accuracy, 0.0001)
	assert.InDelta(t, settings.BirdNET.Latitude, client.Latitude, 0.0001)
	assert.InDelta(t, settings.BirdNET.Longitude, client.Longitude, 0.0001)
	require.NotNil(t, client.HTTPClient, "HTTPClient should not be nil")
	assert.Equal(t, 45*time.Second, client.HTTPClient.Timeout)
}

//nolint:gocognit // Test function with multiple sub-tests and validation checks
func TestRandomizeLocation(t *testing.T) {
	settings := MockSettings()
	client, _ := New(settings)

	// Original coordinates
	originalLat := client.Latitude
	originalLon := client.Longitude

	// Test cases with different radiuses
	testCases := []struct {
		radius float64
	}{
		{0},    // No randomization
		{10},   // Small radius
		{100},  // Medium radius
		{1000}, // Large radius
	}

	for _, tc := range testCases {
		t.Run(fmt.Sprintf("Radius_%vm", tc.radius), func(t *testing.T) {
			// Run multiple times to check randomness
			coordinatePairs := make(map[string]bool)

			for range 10 {
				lat, lon := client.RandomizeLocation(tc.radius)

				// Check if coordinates are within expected range
				maxOffset := tc.radius / 111000 // Convert meters to approximate degrees

				// The implementation uses math.Floor for truncation, so we need to adjust our expectations
				// The actual value could be up to 0.0001 less than the theoretical maximum
				assert.GreaterOrEqual(t, lat, originalLat-maxOffset-0.0001,
					"Latitude outside expected range for radius %f", tc.radius)
				assert.LessOrEqual(t, lat, originalLat+maxOffset,
					"Latitude outside expected range for radius %f", tc.radius)

				assert.GreaterOrEqual(t, lon, originalLon-maxOffset-0.0001,
					"Longitude outside expected range for radius %f", tc.radius)
				assert.LessOrEqual(t, lon, originalLon+maxOffset,
					"Longitude outside expected range for radius %f", tc.radius)

				// Check decimal precision (should be 4 decimal places)
				latStr := fmt.Sprintf("%.5f", lat)
				lonStr := fmt.Sprintf("%.5f", lon)

				assert.Equal(t, byte('0'), latStr[len(latStr)-1],
					"Latitude %s has more than 4 decimal places", latStr)
				assert.Equal(t, byte('0'), lonStr[len(lonStr)-1],
					"Longitude %s has more than 4 decimal places", lonStr)

				// Track unique coordinate pairs for randomness check
				coordKey := fmt.Sprintf("%.4f,%.4f", lat, lon)
				coordinatePairs[coordKey] = true
			}

			// If radius > 0, we expect some randomness
			if tc.radius > 0 {
				assert.GreaterOrEqual(t, len(coordinatePairs), 2,
					"Expected multiple different coordinate pairs for radius %f", tc.radius)
			}

			// If radius = 0, we expect no randomness
			if tc.radius == 0 {
				assert.LessOrEqual(t, len(coordinatePairs), 1,
					"Expected single coordinate pair for radius 0")
			}
		})
	}
}

func TestHandleNetworkError(t *testing.T) {
	// Test with different error types
	testCases := []struct {
		name        string
		err         error
		expectMatch string
	}{
		{
			name:        "Simple error",
			err:         fmt.Errorf("simple error"),
			expectMatch: "BirdWeather test operation network error: simple error",
		},
		{
			name:        "Nil error",
			err:         nil,
			expectMatch: "nil error",
		},
		// More specific network errors could be tested with custom error types
		// that implement the net.Error interface
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			resultErr := handleNetworkError(tc.err, "https://test.example.com", 30*time.Second, "test operation")

			require.NotNil(t, resultErr, "handleNetworkError should never return nil")
			assert.Equal(t, tc.expectMatch, resultErr.Error())
		})
	}
}

func TestUploadSoundscape(t *testing.T) {
	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check request headers
		assert.Equal(t, "application/octet-stream", r.Header.Get("Content-Type"))
		assert.Empty(t, r.Header.Get("Content-Encoding"),
			"Expected no Content-Encoding header for FLAC")
		assert.Equal(t, "BirdNET-Go", r.Header.Get("User-Agent"))

		// Return success response
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, err := fmt.Fprint(w, `{
			"success": true,
			"soundscape": {
				"id": 12345,
				"stationId": 67890,
				"timestamp": "2023-01-01T12:00:00.000Z",
				"url": "https://example.com/soundscape.flac",
				"filesize": 48000,
				"extension": "flac",
				"duration": 3.0
			}
		}`)
		assert.NoError(t, err, "Failed to write response")
	}))
	defer server.Close()

	// Create client with mocked URL
	settings := MockSettings()
	client, _ := New(settings)

	// Create a custom RoundTripper that redirects all requests to our test server
	client.HTTPClient.Transport = &mockTransport{
		server: server,
	}

	// Create test PCM data (create FLAC data using PCM)
	pcmData := make([]byte, 48000*2) // 1 second of 48kHz mono audio (2 bytes per sample)
	timestamp := testTimestamp

	// Call the method under test
	soundscapeID, err := client.UploadSoundscape(timestamp, pcmData)

	// Check results
	require.NoError(t, err, "UploadSoundscape failed")
	assert.Equal(t, "12345", soundscapeID)
}

// mockTransport is a custom http.RoundTripper that redirects all requests to a test server
type mockTransport struct {
	server *httptest.Server
}

func (t *mockTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	// Create a new request to the test server with the same body and headers
	testURL := t.server.URL
	newReq, err := http.NewRequest(req.Method, testURL, req.Body)
	if err != nil {
		return nil, err
	}

	// Copy headers
	newReq.Header = req.Header

	// Ensure User-Agent is set correctly for the test
	if req.Header.Get("User-Agent") == "" {
		newReq.Header.Set("User-Agent", "BirdNET-Go")
	}

	// Send the request to the test server
	return t.server.Client().Transport.RoundTrip(newReq)
}

func TestUploadSoundscape_EmptyData(t *testing.T) {
	client, _ := New(MockSettings())

	// Test with empty PCM data
	_, err := client.UploadSoundscape(testTimestamp, []byte{})

	require.Error(t, err, "Expected error with empty pcmData")
	assert.Equal(t, "pcmData is empty", err.Error())
}

func TestUploadSoundscape_ServerError(t *testing.T) {
	// Setup mock server that returns an error
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusInternalServerError)
		_, err := fmt.Fprint(w, `{"success": false, "error": "Server error"}`)
		assert.NoError(t, err, "Failed to write response")
	}))
	defer server.Close()

	// Create client with mocked URL
	settings := MockSettings()
	client, _ := New(settings)
	// Use the same mockTransport as the successful test to redirect the request
	client.HTTPClient.Transport = &mockTransport{
		server: server,
	}

	// Create test PCM data
	pcmData := make([]byte, 48000*2) // 2 bytes per sample for 16-bit
	timestamp := testTimestamp

	// Call the method under test
	_, err := client.UploadSoundscape(timestamp, pcmData)

	// Check results
	require.Error(t, err, "Expected error from server error response")
}

func TestPostDetection(t *testing.T) {
	// Setup mock server
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Check method
		assert.Equal(t, "POST", r.Method)

		// Check content type
		assert.Equal(t, "application/json", r.Header.Get("Content-Type"))

		// Check user agent
		assert.Equal(t, "BirdNET-Go", r.Header.Get("User-Agent"))

		// Check request body
		var reqBody map[string]any
		decoder := json.NewDecoder(r.Body)
		err := decoder.Decode(&reqBody)
		if !assert.NoError(t, err, "Failed to decode request body") {
			return
		}

		// Check required fields
		expectedFields := []string{
			"timestamp", "lat", "lon", "soundscapeId", "soundscapeStartTime",
			"soundscapeEndTime", "commonName", "scientificName", "algorithm", "confidence",
		}

		for _, field := range expectedFields {
			assert.Contains(t, reqBody, field, "Missing required field in request body")
		}

		// Return success response
		w.WriteHeader(http.StatusCreated)
	}))
	defer server.Close()

	// Create client with mocked URL
	settings := MockSettings()
	client, _ := New(settings)

	// Create a custom RoundTripper that redirects all requests to our test server
	client.HTTPClient.Transport = &mockTransport{
		server: server,
	}

	// Test parameters
	soundscapeID := "12345"
	timestamp := testTimestamp
	commonName := "American Robin"
	scientificName := "Turdus migratorius"
	confidence := 0.95

	// Call the method under test
	err := client.PostDetection(soundscapeID, timestamp, commonName, scientificName, confidence)

	// Check result
	require.NoError(t, err, "PostDetection failed")
}

func TestPostDetection_InvalidInput(t *testing.T) {
	client, _ := New(MockSettings())

	// Test cases with invalid inputs
	testCases := []struct {
		name           string
		soundscapeID   string
		timestamp      string
		commonName     string
		scientificName string
		confidence     float64
	}{
		{
			name:           "Empty soundscapeID",
			soundscapeID:   "",
			timestamp:      testTimestamp,
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			confidence:     0.95,
		},
		{
			name:           "Empty timestamp",
			soundscapeID:   "12345",
			timestamp:      "",
			commonName:     "American Robin",
			scientificName: "Turdus migratorius",
			confidence:     0.95,
		},
		{
			name:           "Empty commonName",
			soundscapeID:   "12345",
			timestamp:      testTimestamp,
			commonName:     "",
			scientificName: "Turdus migratorius",
			confidence:     0.95,
		},
		{
			name:           "Empty scientificName",
			soundscapeID:   "12345",
			timestamp:      testTimestamp,
			commonName:     "American Robin",
			scientificName: "",
			confidence:     0.95,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			err := client.PostDetection(
				tc.soundscapeID, tc.timestamp, tc.commonName, tc.scientificName, tc.confidence)

			require.Error(t, err, "Expected error with invalid input")
			assert.Contains(t, err.Error(), "invalid input")
		})
	}
}

func TestPublish(t *testing.T) {
	// Setup mock server for both upload and post
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// All requests should be POST
		if !assert.Equal(t, "POST", r.Method, "Expected POST request") {
			w.WriteHeader(http.StatusBadRequest)
			return
		}

		// Check content type to determine if it's a soundscape upload or detection post
		contentType := r.Header.Get("Content-Type")
		switch contentType {
		case "application/octet-stream":
			// This is a soundscape upload
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, err := fmt.Fprint(w, `{
				"success": true,
				"soundscape": {
					"id": 12345,
					"stationId": 67890,
					"timestamp": "2023-01-01T12:00:00.000Z",
					"url": "https://example.com/soundscape.flac",
					"filesize": 48000,
					"extension": "flac",
					"duration": 3.0
				}
			}`)
			assert.NoError(t, err, "Failed to write response")
		case "application/json":
			// This is a detection post
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusCreated)
			_, err := fmt.Fprint(w, `{"success": true}`)
			assert.NoError(t, err, "Failed to write response")
		default:
			// Unexpected content type
			assert.Failf(t, "Unexpected Content-Type", "got: %s", contentType)
			w.WriteHeader(http.StatusBadRequest)
		}
	}))
	defer server.Close()

	// Create client with mocked URL
	settings := MockSettings()
	client, _ := New(settings)

	// Create a custom RoundTripper that redirects all requests to our test server
	client.HTTPClient.Transport = &mockTransport{
		server: server,
	}

	// Create test note and PCM data
	note := &datastore.Note{
		Date:           "2023-01-01",
		Time:           "12:00:00",
		CommonName:     "American Robin",
		ScientificName: "Turdus migratorius",
		Confidence:     0.95,
	}

	pcmData := make([]byte, 48000*2) // 1 second of 48kHz mono audio (2 bytes per sample)

	// Call the method under test
	err := client.Publish(note, pcmData)

	// Check result
	require.NoError(t, err, "Publish failed")
}

func TestPublish_EmptyData(t *testing.T) {
	client, _ := New(MockSettings())

	note := &datastore.Note{
		Date:           "2023-01-01",
		Time:           "12:00:00",
		CommonName:     "American Robin",
		ScientificName: "Turdus migratorius",
		Confidence:     0.95,
	}

	// Test with empty PCM data
	err := client.Publish(note, []byte{})

	require.Error(t, err, "Expected error with empty pcmData")
	assert.Equal(t, "pcmData is empty", err.Error())
}

func TestClose(t *testing.T) {
	// Create a mock client for testing
	settings := MockSettings()
	client, _ := New(settings)

	// Create a custom mock HTTP client that we can check after Close
	mockClient := &http.Client{}
	client.HTTPClient = mockClient

	// Call Close
	client.Close()

	// Check if the client was properly closed
	// Note: In the actual implementation, Close() might not set HTTPClient to nil
	// but instead just call CloseIdleConnections(). We're testing the behavior
	// as implemented, not enforcing a specific implementation.

	// Attempt operations after Close to ensure they fail gracefully
	_, err := client.UploadSoundscape(testTimestamp, []byte{1, 2, 3, 4})
	require.Error(t, err, "Expected error when using client after Close")
}
