// source_registry_test.go - Unit tests for audio source registry
package myaudio

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestSourceRegistration(t *testing.T) {
	registry := newTestRegistry()

	// Test RTSP source registration
	rtspURL := "rtsp://admin:password@192.168.1.100/stream"
	config := SourceConfig{
		ID:          "test_cam",
		DisplayName: "Test Camera",
		Type:        SourceTypeRTSP,
	}

	source, err := registry.RegisterSource(rtspURL, config)
	require.NoError(t, err, "Failed to register RTSP source")

	// Verify source properties
	assert.Equal(t, "test_cam", source.ID)
	assert.Equal(t, "Test Camera", source.DisplayName)
	assert.Equal(t, SourceTypeRTSP, source.Type)

	connStr, err := source.GetConnectionString()
	require.NoError(t, err, "Failed to get connection string")
	assert.Equal(t, rtspURL, connStr)

	// Verify safe string doesn't contain credentials
	assert.NotContains(t, source.SafeString, "password", "Safe string should not contain credentials")
}

func TestRTSPValidationWithQueryParameters(t *testing.T) {
	registry := newTestRegistry()

	testCases := []struct {
		name        string
		rtspURL     string
		shouldPass  bool
		description string
	}{
		{
			name:        "RTSP URL with ampersand in query params",
			rtspURL:     "rtsp://USER:PASS@192.168.1.100:554/cam/realmonitor?channel=1&subtype=0",
			shouldPass:  true,
			description: "Should allow ampersands in query parameters",
		},
		{
			name:        "RTSP URL with multiple query params",
			rtspURL:     "rtsp://admin:password@192.168.1.100/stream?quality=high&framerate=30&resolution=1080p",
			shouldPass:  true,
			description: "Should allow multiple query parameters with ampersands",
		},
		{
			name:        "Basic RTSP URL without query params",
			rtspURL:     "rtsp://192.168.1.100/stream",
			shouldPass:  true,
			description: "Should allow basic RTSP URLs",
		},
		{
			name:        "RTSP URL with credentials and query params",
			rtspURL:     "rtsp://user:pass@192.168.1.100:8554/live.sdp?transport=tcp&unicast=true",
			shouldPass:  true,
			description: "Should allow credentials with query parameters",
		},
		{
			name:        "Empty RTSP URL",
			rtspURL:     "",
			shouldPass:  false,
			description: "Should reject empty URLs",
		},
		{
			name:        "Invalid scheme",
			rtspURL:     "http://192.168.1.100/stream",
			shouldPass:  false,
			description: "Should reject non-RTSP schemes",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := SourceConfig{
				ID:          "test_rtsp",
				DisplayName: "Test RTSP",
				Type:        SourceTypeRTSP,
			}

			_, err := registry.RegisterSource(tc.rtspURL, config)

			if tc.shouldPass {
				assert.NoError(t, err, tc.description)
			} else {
				assert.Error(t, err, tc.description)
			}
		})
	}
}

func TestRTSPCredentialSanitization(t *testing.T) {
	registry := GetRegistry()

	testCases := []struct {
		name       string
		input      string
		shouldHide bool
	}{
		{
			name:       "RTSP with credentials",
			input:      "rtsp://admin:secret123@192.168.1.100/stream",
			shouldHide: true,
		},
		{
			name:       "RTSP without credentials",
			input:      "rtsp://192.168.1.100/stream",
			shouldHide: false,
		},
		{
			name:       "Audio device",
			input:      "hw:1,0",
			shouldHide: false,
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			// Detect the actual source type
			sourceType := SourceTypeRTSP
			if strings.HasPrefix(tc.input, "hw:") {
				sourceType = SourceTypeAudioCard
			}
			source := registry.GetOrCreateSource(tc.input, sourceType)
			require.NotNil(t, source, "Failed to create source for %s", tc.input)

			if tc.shouldHide {
				// Should not contain credentials in safe string
				assert.NotContains(t, source.SafeString, "secret123", "Safe string should not contain password")
				assert.NotContains(t, source.SafeString, "admin:secret123", "Safe string should not contain credentials")
			}

			// Original connection string should always be preserved
			connStr, err := source.GetConnectionString()
			require.NoError(t, err, "Failed to get connection string")
			assert.Equal(t, tc.input, connStr, "Original connection string not preserved")
		})
	}
}

func TestSourceIDGeneration(t *testing.T) {
	registry := newTestRegistry()

	// Test auto-generated IDs
	source1 := registry.GetOrCreateSource("rtsp://cam1.local/stream", SourceTypeRTSP)
	source2 := registry.GetOrCreateSource("rtsp://cam2.local/stream", SourceTypeRTSP)

	assert.NotEqual(t, source1.ID, source2.ID, "Generated IDs should be unique")

	// IDs should follow the pattern
	assert.True(t, strings.HasPrefix(source1.ID, "rtsp_"), "RTSP source ID should start with 'rtsp_'")
}

func TestConcurrentSourceAccess(t *testing.T) {
	registry := GetRegistry()

	// Test concurrent registration
	done := make(chan bool, 10)

	for i := range 10 {
		go func(id int) {
			source := registry.GetOrCreateSource(
				fmt.Sprintf("rtsp://cam%d.local/stream", id),
				SourceTypeRTSP,
			)
			assert.NotNil(t, source, "Failed to create source %d", id)
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for range 10 {
		<-done
	}

	// Verify we have 10 sources
	sources := registry.ListSources()
	assert.GreaterOrEqual(t, len(sources), 10, "Expected at least 10 sources")
}

func TestBackwardCompatibility(t *testing.T) {
	// Test that GetOrCreateSource works correctly
	testURL := "rtsp://test.local/stream"

	// This should auto-register the source
	source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, source, "GetOrCreateSource returned nil")

	// Should return a source with an ID, not the original URL
	assert.NotEqual(t, testURL, source.ID, "Source should have generated ID, not original URL")

	// Second call should return the same source
	source2 := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, source2)
	assert.Equal(t, source.ID, source2.ID, "GetOrCreateSource should be idempotent")
}

func TestSourceMetricsUpdate(t *testing.T) {
	registry := GetRegistry()

	source := registry.GetOrCreateSource("rtsp://metrics.test/stream", SourceTypeRTSP)
	initialBytes := source.TotalBytes
	initialErrors := source.ErrorCount

	// Update metrics
	registry.UpdateSourceMetrics(source.ID, 1024, false)
	registry.UpdateSourceMetrics(source.ID, 2048, true)

	// Verify updates
	updatedSource, exists := registry.GetSourceByID(source.ID)
	require.True(t, exists)
	assert.Equal(t, initialBytes+1024+2048, updatedSource.TotalBytes, "Expected total bytes to be updated")
	assert.Equal(t, initialErrors+1, updatedSource.ErrorCount, "Expected error count to be incremented")
}

func TestSourceStats(t *testing.T) {
	registry := newTestRegistry()

	// Create sources of different types
	registry.GetOrCreateSource("rtsp://cam1.local/stream", SourceTypeRTSP)
	registry.GetOrCreateSource("rtsp://cam2.local/stream", SourceTypeRTSP)
	registry.GetOrCreateSource("hw:1,0", SourceTypeAudioCard)

	stats := registry.GetSourceStats()

	assert.Equal(t, 3, stats.Total, "Expected 3 total sources")
	assert.Equal(t, 2, stats.RTSP, "Expected 2 RTSP sources")
	assert.Equal(t, 1, stats.Device, "Expected 1 device source")
}
