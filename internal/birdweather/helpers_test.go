package birdweather

import (
	"bytes"
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/errors"
)

// TestParseSoundscapeResponse tests the JSON response parsing helper
func TestParseSoundscapeResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name           string
		responseBody   []byte
		maskedURL      string
		statusCode     int
		expectedID     string
		wantErr        bool
		errContains    string
	}{
		{
			name: "valid successful response",
			responseBody: []byte(`{
				"success": true,
				"soundscape": {
					"id": 12345,
					"stationId": 100,
					"timestamp": "2023-01-01T12:00:00.000Z",
					"url": null,
					"filesize": 48000,
					"extension": "wav",
					"duration": 3.0
				}
			}`),
			maskedURL:  "https://app.birdweather.com/api/v1/stations/***/soundscapes",
			statusCode: 201,
			expectedID: "12345",
			wantErr:    false,
		},
		{
			name: "response with success false",
			responseBody: []byte(`{
				"success": false,
				"soundscape": {
					"id": 0
				}
			}`),
			maskedURL:   "https://app.birdweather.com/api/v1/stations/***/soundscapes",
			statusCode:  201,
			wantErr:     true,
			errContains: "upload failed",
		},
		{
			name:         "invalid JSON",
			responseBody: []byte(`{invalid json`),
			maskedURL:    "https://app.birdweather.com/api/v1/stations/***/soundscapes",
			statusCode:   200,
			wantErr:      true,
			errContains:  "failed to decode JSON",
		},
		{
			name:         "HTML error response",
			responseBody: []byte(`<html><head><title>502 Bad Gateway</title></head><body>Error</body></html>`),
			maskedURL:    "https://app.birdweather.com/api/v1/stations/***/soundscapes",
			statusCode:   200,
			wantErr:      true,
			errContains:  "HTML",
		},
		{
			name: "large soundscape ID",
			responseBody: []byte(`{
				"success": true,
				"soundscape": {
					"id": 999999999,
					"stationId": 100,
					"timestamp": "2023-01-01T12:00:00.000Z",
					"url": "https://example.com/audio.wav",
					"filesize": 48000,
					"extension": "flac",
					"duration": 3.0
				}
			}`),
			maskedURL:  "https://app.birdweather.com/api/v1/stations/***/soundscapes",
			statusCode: 201,
			expectedID: "999999999",
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			soundscapeID, err := parseSoundscapeResponse(tt.responseBody, tt.maskedURL, tt.statusCode)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			require.NoError(t, err)
			assert.Equal(t, tt.expectedID, soundscapeID)
		})
	}
}

// TestTrackOperationTiming tests the deferred timing helper
func TestTrackOperationTiming(t *testing.T) {
	t.Parallel()

	t.Run("successful operation adds timing info", func(t *testing.T) {
		t.Parallel()

		var err error
		startTime := time.Now()

		// Simulate operation
		cleanup := trackOperationTiming(&err, "test_operation", startTime, "key1", "value1")
		time.Sleep(10 * time.Millisecond) // Small delay to ensure measurable duration
		cleanup()

		// err should remain nil for successful operation
		assert.NoError(t, err)
	})

	t.Run("failed operation with enhanced error gets timing added", func(t *testing.T) {
		t.Parallel()

		// Create an enhanced error as an error interface type
		var err error = errors.New(assert.AnError).
			Component("test").
			Category(errors.CategoryGeneric).
			Build()
		startTime := time.Now()

		cleanup := trackOperationTiming(&err, "test_operation", startTime)
		time.Sleep(10 * time.Millisecond)
		cleanup()

		require.Error(t, err)
		// Check that timing was added to enhanced error
		var enhancedErr *errors.EnhancedError
		if errors.As(err, &enhancedErr) {
			assert.Contains(t, enhancedErr.Context, "operation_duration_ms")
			assert.Contains(t, enhancedErr.Context, "operation")
		}
	})

	t.Run("failed operation with plain error gets wrapped", func(t *testing.T) {
		t.Parallel()

		err := fmt.Errorf("plain test error")
		startTime := time.Now()

		cleanup := trackOperationTiming(&err, "wrap_test", startTime)
		cleanup()

		require.Error(t, err)
		// The error should now be an EnhancedError
		var enhancedErr *errors.EnhancedError
		assert.True(t, errors.As(err, &enhancedErr), "error should be wrapped as EnhancedError")
	})
}

// TestCheckAuthenticationStatus tests the HTTP status code evaluation helper
func TestCheckAuthenticationStatus(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		statusCode  int
		wantErr     bool
		errContains string
	}{
		{
			name:       "200 OK",
			statusCode: http.StatusOK,
			wantErr:    false,
		},
		{
			name:       "201 Created",
			statusCode: http.StatusCreated,
			wantErr:    false,
		},
		{
			name:        "401 Unauthorized",
			statusCode:  http.StatusUnauthorized,
			wantErr:     true,
			errContains: "invalid station ID",
		},
		{
			name:        "403 Forbidden",
			statusCode:  http.StatusForbidden,
			wantErr:     true,
			errContains: "invalid station ID",
		},
		{
			name:        "404 Not Found",
			statusCode:  http.StatusNotFound,
			wantErr:     true,
			errContains: "station not found",
		},
		{
			name:        "500 Internal Server Error",
			statusCode:  http.StatusInternalServerError,
			wantErr:     true,
			errContains: "status code 500",
		},
		{
			name:        "502 Bad Gateway",
			statusCode:  http.StatusBadGateway,
			wantErr:     true,
			errContains: "status code 502",
		},
		{
			name:        "503 Service Unavailable",
			statusCode:  http.StatusServiceUnavailable,
			wantErr:     true,
			errContains: "status code 503",
		},
		{
			name:       "399 below client error threshold",
			statusCode: 399,
			wantErr:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := checkAuthenticationStatus(tt.statusCode)

			if tt.wantErr {
				require.Error(t, err)
				if tt.errContains != "" {
					assert.Contains(t, err.Error(), tt.errContains)
				}
				return
			}

			assert.NoError(t, err)
		})
	}
}

// TestParseDouble tests the safe float parsing helper
func TestParseDouble(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		input        string
		defaultValue float64
		expected     float64
	}{
		{
			name:         "valid positive float",
			input:        "3.14159",
			defaultValue: 0.0,
			expected:     3.14159,
		},
		{
			name:         "valid negative float",
			input:        "-23.5",
			defaultValue: 0.0,
			expected:     -23.5,
		},
		{
			name:         "valid integer",
			input:        "42",
			defaultValue: 0.0,
			expected:     42.0,
		},
		{
			name:         "whitespace trimmed",
			input:        "  -70.0  ",
			defaultValue: -100.0,
			expected:     -70.0,
		},
		{
			name:         "empty string returns default",
			input:        "",
			defaultValue: -70.0,
			expected:     -70.0,
		},
		{
			name:         "invalid string returns default",
			input:        "not-a-number",
			defaultValue: -70.0,
			expected:     -70.0,
		},
		{
			name:         "unparseable special value returns default",
			input:        "NaN-invalid",
			defaultValue: -70.0,
			expected:     -70.0,
		},
		{
			name:         "zero",
			input:        "0",
			defaultValue: -1.0,
			expected:     0.0,
		},
		{
			name:         "scientific notation",
			input:        "1.5e-3",
			defaultValue: 0.0,
			expected:     0.0015,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := parseDouble(tt.input, tt.defaultValue)
			assert.InDelta(t, tt.expected, result, 0.000001)
		})
	}
}

// TestIsHTMLResponse tests the HTML content-type detection
func TestIsHTMLResponse(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		contentType string
		expected    bool
	}{
		{
			name:        "text/html",
			contentType: "text/html",
			expected:    true,
		},
		{
			name:        "text/html with charset",
			contentType: "text/html; charset=utf-8",
			expected:    true,
		},
		{
			name:        "TEXT/HTML uppercase",
			contentType: "TEXT/HTML",
			expected:    true,
		},
		{
			name:        "application/json",
			contentType: "application/json",
			expected:    false,
		},
		{
			name:        "application/octet-stream",
			contentType: "application/octet-stream",
			expected:    false,
		},
		{
			name:        "empty content type",
			contentType: "",
			expected:    false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			resp := &http.Response{
				Header: http.Header{
					"Content-Type": []string{tt.contentType},
				},
			}

			result := isHTMLResponse(resp)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestExtractHTMLError tests the HTML error extraction helper
func TestExtractHTMLError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		htmlContent string
		shouldMatch string
	}{
		{
			name:        "extract title from error page",
			htmlContent: "<html><head><title>502 Bad Gateway</title></head><body>Error</body></html>",
			shouldMatch: "502 Bad Gateway",
		},
		{
			name:        "extract error pattern",
			htmlContent: "<html><body><p>There was an error processing your request</p></body></html>",
			shouldMatch: "error",
		},
		{
			name:        "service unavailable",
			htmlContent: "<html><body><h1>Service Unavailable</h1></body></html>",
			shouldMatch: "Service Unavailable",
		},
		{
			name:        "gateway timeout",
			htmlContent: "<html><body>Gateway Timeout - The server did not respond in time</body></html>",
			shouldMatch: "Gateway Timeout",
		},
		{
			name:        "not found error",
			htmlContent: "<html><body>The requested resource was not found</body></html>",
			shouldMatch: "not found",
		},
		{
			name:        "unauthorized",
			htmlContent: "<html><body>Unauthorized access denied</body></html>",
			shouldMatch: "Unauthorized",
		},
		{
			name:        "generic html without specific error",
			htmlContent: "<html><body><p>Welcome to our website</p></body></html>",
			shouldMatch: "Unexpected HTML response",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := extractHTMLError(tt.htmlContent)
			assert.Contains(t, result, tt.shouldMatch)
		})
	}
}

// TestMaskURLForLoggingVariants tests additional URL masking scenarios
func TestMaskURLForLoggingVariants(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		url           string
		birdweatherID string
		expected      string
	}{
		{
			name:          "mask station ID in URL",
			url:           "https://app.birdweather.com/api/v1/stations/abc123xyz/soundscapes",
			birdweatherID: "abc123xyz",
			expected:      "https://app.birdweather.com/api/v1/stations/***/soundscapes",
		},
		{
			name:          "empty birdweather ID returns original",
			url:           "https://app.birdweather.com/api/v1/stations/abc123xyz/soundscapes",
			birdweatherID: "",
			expected:      "https://app.birdweather.com/api/v1/stations/abc123xyz/soundscapes",
		},
		{
			name:          "ID not in URL returns original",
			url:           "https://app.birdweather.com/api/v1/info",
			birdweatherID: "abc123xyz",
			expected:      "https://app.birdweather.com/api/v1/info",
		},
		{
			name:          "multiple occurrences all masked",
			url:           "https://example.com/secret123/path/secret123",
			birdweatherID: "secret123",
			expected:      "https://example.com/***/path/***",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := maskURLForLogging(tt.url, tt.birdweatherID)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestLogFLACEncodingError tests that the FLAC error logger handles different error types
func TestLogFLACEncodingError(t *testing.T) {
	t.Parallel()

	// This test verifies the function doesn't panic for various error types
	tests := []struct {
		name string
		err  error
	}{
		{
			name: "context canceled",
			err:  context.Canceled,
		},
		{
			name: "context deadline exceeded",
			err:  context.DeadlineExceeded,
		},
		{
			name: "generic error",
			err:  assert.AnError,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Verify no panic occurs
			assert.NotPanics(t, func() {
				logFLACEncodingError(tt.err)
			})
		})
	}
}

// TestIsDNSError tests the DNS error detection helper
func TestIsDNSErrorExtended(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		errString string
		expected  bool
	}{
		{
			name:      "no such host",
			errString: "dial tcp: lookup example.com: no such host",
			expected:  true,
		},
		{
			name:      "lookup keyword present",
			errString: "lookup failed for hostname",
			expected:  true,
		},
		{
			name:      "connection refused is not DNS",
			errString: "connection refused",
			expected:  false,
		},
		{
			name:      "timeout without lookup is not DNS",
			errString: "i/o timeout",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			err := &testError{msg: tt.errString}
			result := isDNSError(err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsNetworkError tests the network error detection helper
func TestIsNetworkError(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		errString string
		expected  bool
	}{
		{
			name:      "connection refused",
			errString: "dial tcp: connection refused",
			expected:  true,
		},
		{
			name:      "i/o timeout",
			errString: "i/o timeout",
			expected:  true,
		},
		{
			name:      "network unreachable",
			errString: "network is unreachable",
			expected:  true,
		},
		{
			name:      "connection reset by peer",
			errString: "connection reset by peer",
			expected:  true,
		},
		{
			name:      "connection closed",
			errString: "use of closed network connection",
			expected:  true,
		},
		{
			name:      "generic application error",
			errString: "invalid input parameter",
			expected:  false,
		},
		{
			name:      "nil error",
			errString: "",
			expected:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			var err error
			if tt.errString != "" {
				err = &testError{msg: tt.errString}
			}
			result := isNetworkError(err)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestIsDNSTimeout tests the DNS timeout detection helper
func TestIsDNSTimeoutExtended(t *testing.T) {
	t.Parallel()

	t.Run("nil error returns false", func(t *testing.T) {
		t.Parallel()
		assert.False(t, isDNSTimeout(nil))
	})

	t.Run("context deadline exceeded returns true", func(t *testing.T) {
		t.Parallel()
		assert.True(t, isDNSTimeout(context.DeadlineExceeded))
	})

	t.Run("context canceled returns false", func(t *testing.T) {
		t.Parallel()
		// Context canceled is not a timeout
		assert.False(t, isDNSTimeout(context.Canceled))
	})
}

// TestReplaceHostWithIP tests the URL host replacement helper
func TestReplaceHostWithIP(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		url      string
		ip       string
		expected string
	}{
		{
			name:     "replace hostname with IP",
			url:      "https://app.birdweather.com/api/v1/info",
			ip:       "104.26.10.123",
			expected: "https://104.26.10.123/api/v1/info",
		},
		{
			name:     "preserve port in URL",
			url:      "https://example.com:8443/path",
			ip:       "192.168.1.1",
			expected: "https://192.168.1.1:8443/path",
		},
		{
			name:     "invalid URL returns original",
			url:      "://invalid",
			ip:       "1.2.3.4",
			expected: "://invalid",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			result := replaceHostWithIP(tt.url, tt.ip)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSaveBufferToFile tests the debug file saving helper
func TestSaveBufferToFile(t *testing.T) {
	t.Parallel()

	t.Run("saves FLAC file with metadata", func(t *testing.T) {
		t.Parallel()

		// Create temp directory for test
		tmpDir := t.TempDir()
		filename := filepath.Join(tmpDir, "test_audio.flac")

		// Create test buffer with some data
		testData := []byte("fake FLAC audio data for testing")
		buffer := bytes.NewBuffer(testData)

		startTime := time.Date(2024, 1, 15, 10, 30, 0, 0, time.UTC)
		endTime := startTime.Add(3 * time.Second)

		// Call the function
		err := saveBufferToFile(buffer, filename, startTime, endTime)
		require.NoError(t, err)

		// Verify audio file was created
		assert.FileExists(t, filename)

		// Verify audio file content
		content, err := os.ReadFile(filename) //nolint:gosec // G304: test reads file from t.TempDir()
		require.NoError(t, err)
		assert.Equal(t, testData, content)

		// Verify metadata file was created
		metaFilename := filepath.Join(tmpDir, "test_audio.txt")
		assert.FileExists(t, metaFilename)

		// Verify metadata content
		metaContent, err := os.ReadFile(metaFilename) //nolint:gosec // G304: test reads file from t.TempDir()
		require.NoError(t, err)
		metaStr := string(metaContent)

		assert.Contains(t, metaStr, "File: test_audio.flac")
		assert.Contains(t, metaStr, "Format: .flac")
		assert.Contains(t, metaStr, "Duration: 3s")
		assert.Contains(t, metaStr, "Sample Rate: 48000 Hz")
		// FLAC files should NOT have WAV-specific metadata
		assert.NotContains(t, metaStr, "Estimated PCM Data Size")
	})

	t.Run("returns error for nil buffer", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		filename := filepath.Join(tmpDir, "test.flac")
		startTime := time.Now()
		endTime := startTime.Add(time.Second)

		err := saveBufferToFile(nil, filename, startTime, endTime)
		require.Error(t, err)
		assert.Contains(t, err.Error(), "buffer is nil")
	})

	t.Run("creates directory if not exists", func(t *testing.T) {
		t.Parallel()

		tmpDir := t.TempDir()
		nestedDir := filepath.Join(tmpDir, "nested", "deep", "dir")
		filename := filepath.Join(nestedDir, "test.flac")

		buffer := bytes.NewBuffer([]byte("test data"))
		startTime := time.Now()
		endTime := startTime.Add(time.Second)

		err := saveBufferToFile(buffer, filename, startTime, endTime)
		require.NoError(t, err)
		assert.FileExists(t, filename)
	})
}

// TestGenerateTestPCMData tests the test PCM data generation helper
func TestGenerateTestPCMData(t *testing.T) {
	t.Parallel()

	data := generateTestPCMData()

	// Expected size: 48000 samples/sec / 2 (0.5 sec) * 2 bytes/sample = 48000 bytes
	expectedSize := testAudioSampleRate / testAudioDurationFraction * testAudioBytesPerSample
	assert.Len(t, data, expectedSize)

	// Data should be all zeros (silence)
	for i, b := range data {
		if b != 0 {
			t.Errorf("Expected silence (0) at index %d, got %d", i, b)
			break
		}
	}
}

// TestCloseResponseBody tests the response body close helper
func TestCloseResponseBody(t *testing.T) {
	t.Parallel()

	t.Run("closes response body without error", func(t *testing.T) {
		t.Parallel()

		// Create a mock response with a body
		resp := &http.Response{
			Body: http.NoBody,
		}

		// Should not panic and should handle gracefully
		closeResponseBody(resp)
	})

	t.Run("handles nil response gracefully", func(t *testing.T) {
		t.Parallel()

		// Should not panic with nil response
		closeResponseBody(nil)
	})
}

// TestNewSecureHTTPClient tests the secure HTTP client factory helper
func TestNewSecureHTTPClient(t *testing.T) {
	t.Parallel()

	t.Run("creates client with specified timeout", func(t *testing.T) {
		t.Parallel()

		timeout := 30 * time.Second
		client := newSecureHTTPClient(timeout)

		require.NotNil(t, client)
		assert.Equal(t, timeout, client.Timeout)
	})

	t.Run("has TLS transport configured", func(t *testing.T) {
		t.Parallel()

		client := newSecureHTTPClient(15 * time.Second)

		require.NotNil(t, client.Transport)
		transport, ok := client.Transport.(*http.Transport)
		require.True(t, ok, "Transport should be *http.Transport")
		require.NotNil(t, transport.TLSClientConfig)
		assert.False(t, transport.TLSClientConfig.InsecureSkipVerify)
	})
}
