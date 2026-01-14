// source_registry_test.go - Unit tests for audio source registry
package myaudio

import (
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants to avoid goconst warnings
const testLocalStreamURL = "rtsp://test.local/stream"

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
	registry := newTestRegistry()

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
	registry := newTestRegistry()

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

	// Verify we have exactly 10 sources
	sources := registry.ListSources()
	assert.Len(t, sources, 10, "Expected exactly 10 sources")
}

func TestBackwardCompatibility(t *testing.T) {
	registry := GetRegistry()

	// Test that GetOrCreateSource works correctly
	testURL := testLocalStreamURL

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
	registry := newTestRegistry()

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

// TestMultiProtocolValidation tests URL validation for all supported stream protocols
func TestMultiProtocolValidation(t *testing.T) {
	registry := newTestRegistry()

	testCases := []struct {
		name        string
		url         string
		sourceType  SourceType
		shouldPass  bool
		description string
	}{
		// RTSP protocol tests
		{
			name:        "RTSP basic URL",
			url:         "rtsp://192.168.1.100/stream",
			sourceType:  SourceTypeRTSP,
			shouldPass:  true,
			description: "Basic RTSP URL without credentials",
		},
		{
			name:        "RTSP with credentials",
			url:         "rtsp://admin:password@192.168.1.100/stream",
			sourceType:  SourceTypeRTSP,
			shouldPass:  true,
			description: "RTSP URL with username and password",
		},
		{
			name:        "RTSPS secure",
			url:         "rtsps://secure.camera.local/stream",
			sourceType:  SourceTypeRTSP,
			shouldPass:  true,
			description: "Secure RTSP URL",
		},

		// HTTP protocol tests
		{
			name:        "HTTP basic URL",
			url:         "http://stream.example.com/live",
			sourceType:  SourceTypeHTTP,
			shouldPass:  true,
			description: "Basic HTTP stream URL",
		},
		{
			name:        "HTTPS secure URL",
			url:         "https://stream.example.com/live",
			sourceType:  SourceTypeHTTP,
			shouldPass:  true,
			description: "Secure HTTPS stream URL",
		},
		{
			name:        "HTTP with auth",
			url:         "http://user:pass@stream.example.com/live",
			sourceType:  SourceTypeHTTP,
			shouldPass:  true,
			description: "HTTP URL with credentials",
		},

		// HLS protocol tests
		{
			name:        "HLS m3u8 playlist",
			url:         "https://cdn.example.com/playlist.m3u8",
			sourceType:  SourceTypeHLS,
			shouldPass:  true,
			description: "HLS playlist URL",
		},
		{
			name:        "HLS with query token",
			url:         "https://cdn.example.com/playlist.m3u8?token=abc123",
			sourceType:  SourceTypeHLS,
			shouldPass:  true,
			description: "HLS URL with authentication token",
		},
		{
			name:        "HLS with multiple params",
			url:         "https://cdn.example.com/playlist.m3u8?token=abc&expires=123456",
			sourceType:  SourceTypeHLS,
			shouldPass:  true,
			description: "HLS URL with multiple query parameters",
		},

		// RTMP protocol tests
		{
			name:        "RTMP basic URL",
			url:         "rtmp://live.example.com/app/streamkey",
			sourceType:  SourceTypeRTMP,
			shouldPass:  true,
			description: "Basic RTMP URL",
		},
		{
			name:        "RTMPS secure URL",
			url:         "rtmps://live.example.com/app/streamkey",
			sourceType:  SourceTypeRTMP,
			shouldPass:  true,
			description: "Secure RTMP URL",
		},
		{
			name:        "RTMP with credentials",
			url:         "rtmp://user:pass@live.example.com/app/streamkey",
			sourceType:  SourceTypeRTMP,
			shouldPass:  true,
			description: "RTMP URL with credentials",
		},

		// UDP protocol tests
		{
			name:        "UDP multicast",
			url:         "udp://239.0.0.1:1234",
			sourceType:  SourceTypeUDP,
			shouldPass:  true,
			description: "UDP multicast URL",
		},
		{
			name:        "UDP with options",
			url:         "udp://@239.0.0.1:1234?pkt_size=1316",
			sourceType:  SourceTypeUDP,
			shouldPass:  true,
			description: "UDP URL with FFmpeg options",
		},

		// Invalid cases
		{
			name:        "Empty URL",
			url:         "",
			sourceType:  SourceTypeRTSP,
			shouldPass:  false,
			description: "Empty URL should be rejected",
		},
		{
			name:        "Wrong scheme for RTSP type",
			url:         "http://example.com/stream",
			sourceType:  SourceTypeRTSP,
			shouldPass:  false,
			description: "HTTP URL should be rejected when RTSP type is specified",
		},
		{
			name:        "Wrong scheme for RTMP type",
			url:         "rtsp://example.com/stream",
			sourceType:  SourceTypeRTMP,
			shouldPass:  false,
			description: "RTSP URL should be rejected when RTMP type is specified",
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			config := SourceConfig{
				ID:          fmt.Sprintf("test_%s", tc.sourceType),
				DisplayName: fmt.Sprintf("Test %s", tc.sourceType),
				Type:        tc.sourceType,
			}

			_, err := registry.RegisterSource(tc.url, config)

			if tc.shouldPass {
				assert.NoError(t, err, tc.description)
			} else {
				assert.Error(t, err, tc.description)
			}
		})
	}
}

// TestSourceStatsMultiProtocol tests that GetSourceStats correctly counts all protocol types
func TestSourceStatsMultiProtocol(t *testing.T) {
	registry := newTestRegistry()

	// Register sources of each type
	registry.GetOrCreateSource("rtsp://cam1.local/stream", SourceTypeRTSP)
	registry.GetOrCreateSource("rtsp://cam2.local/stream", SourceTypeRTSP)
	registry.GetOrCreateSource("http://stream.example.com/live", SourceTypeHTTP)
	registry.GetOrCreateSource("https://cdn.example.com/playlist.m3u8", SourceTypeHLS)
	registry.GetOrCreateSource("rtmp://live.example.com/app/stream", SourceTypeRTMP)
	registry.GetOrCreateSource("udp://239.0.0.1:1234", SourceTypeUDP)
	registry.GetOrCreateSource("hw:1,0", SourceTypeAudioCard)

	stats := registry.GetSourceStats()

	assert.Equal(t, 7, stats.Total, "Expected 7 total sources")
	assert.Equal(t, 2, stats.RTSP, "Expected 2 RTSP sources")
	assert.Equal(t, 1, stats.HTTP, "Expected 1 HTTP source")
	assert.Equal(t, 1, stats.HLS, "Expected 1 HLS source")
	assert.Equal(t, 1, stats.RTMP, "Expected 1 RTMP source")
	assert.Equal(t, 1, stats.UDP, "Expected 1 UDP source")
	assert.Equal(t, 1, stats.Device, "Expected 1 device source")
}

// TestGetSourceByConnection tests the lookup-only method
func TestGetSourceByConnection(t *testing.T) {
	registry := newTestRegistry()

	// Register a source first
	testURL := testLocalStreamURL
	source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, source)

	t.Run("finds existing source", func(t *testing.T) {
		found, exists := registry.GetSourceByConnection(testURL)
		assert.True(t, exists, "Should find existing source")
		assert.Equal(t, source.ID, found.ID, "Should return the same source")
	})

	t.Run("returns false for non-existent source", func(t *testing.T) {
		found, exists := registry.GetSourceByConnection("rtsp://nonexistent.local/stream")
		assert.False(t, exists, "Should not find non-existent source")
		assert.Nil(t, found, "Found should be nil for non-existent source")
	})
}

// TestUnregisterDoesNotCreateOrphanSources verifies that attempting to look up
// a non-existent source for unregistration does not create orphan entries
func TestUnregisterDoesNotCreateOrphanSources(t *testing.T) {
	registry := newTestRegistry()

	// Get initial source count
	initialSources := registry.ListSources()
	initialCount := len(initialSources)

	// Attempt to look up a source that was never registered
	// This simulates what happens during unregistration
	source, exists := registry.GetSourceByConnection("rtsp://never-registered.local/stream")

	assert.False(t, exists, "Should not find non-existent source")
	assert.Nil(t, source, "Source should be nil")

	// Verify no new sources were created
	finalSources := registry.ListSources()
	assert.Len(t, finalSources, initialCount,
		"Looking up non-existent source should not create new entries")
}

// TestCredentialSanitizationMultiProtocol tests credential removal for all protocol types
func TestCredentialSanitizationMultiProtocol(t *testing.T) {
	registry := newTestRegistry()

	testCases := []struct {
		name         string
		url          string
		sourceType   SourceType
		shouldRemove []string // Strings that should NOT appear in SafeString
	}{
		{
			name:         "RTSP credentials",
			url:          "rtsp://admin:secret123@192.168.1.100/stream",
			sourceType:   SourceTypeRTSP,
			shouldRemove: []string{"admin", "secret123", "admin:secret123"},
		},
		{
			name:         "HTTP basic auth",
			url:          "http://user:password@stream.example.com/live",
			sourceType:   SourceTypeHTTP,
			shouldRemove: []string{"user", "password", "user:password"},
		},
		{
			name:         "HTTPS basic auth",
			url:          "https://apikey:secretkey@cdn.example.com/playlist.m3u8",
			sourceType:   SourceTypeHLS,
			shouldRemove: []string{"apikey", "secretkey"},
		},
		{
			name:         "RTMP credentials",
			url:          "rtmp://broadcaster:streampass@live.example.com/app/key",
			sourceType:   SourceTypeRTMP,
			shouldRemove: []string{"broadcaster", "streampass"},
		},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			source := registry.GetOrCreateSource(tc.url, tc.sourceType)
			require.NotNil(t, source)

			for _, sensitive := range tc.shouldRemove {
				assert.NotContains(t, source.SafeString, sensitive,
					"SafeString should not contain '%s'", sensitive)
			}

			// Original connection string should be preserved
			connStr, err := source.GetConnectionString()
			require.NoError(t, err)
			assert.Equal(t, tc.url, connStr, "Original URL should be preserved")
		})
	}
}

// TestSourceIDPrefixesByType verifies that auto-generated IDs use correct prefixes
func TestSourceIDPrefixesByType(t *testing.T) {
	registry := newTestRegistry()

	testCases := []struct {
		url        string
		sourceType SourceType
		prefix     string
	}{
		{"rtsp://cam.local/stream", SourceTypeRTSP, "rtsp_"},
		{"http://stream.example.com/live", SourceTypeHTTP, "http_"},
		{"https://cdn.example.com/playlist.m3u8", SourceTypeHLS, "hls_"},
		{"rtmp://live.example.com/app/stream", SourceTypeRTMP, "rtmp_"},
		{"udp://239.0.0.1:1234", SourceTypeUDP, "udp_"},
		{"hw:1,0", SourceTypeAudioCard, "audio_card_"},
	}

	for _, tc := range testCases {
		t.Run(string(tc.sourceType), func(t *testing.T) {
			source := registry.GetOrCreateSource(tc.url, tc.sourceType)
			require.NotNil(t, source)
			assert.True(t, strings.HasPrefix(source.ID, tc.prefix),
				"Source ID %q should start with %q", source.ID, tc.prefix)
		})
	}
}

// TestRemoveSource tests the source removal functionality
func TestRemoveSource(t *testing.T) {
	registry := newTestRegistry()

	// Create a source
	testURL := testLocalStreamURL
	source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, source)
	sourceID := source.ID

	t.Run("removes existing source", func(t *testing.T) {
		// Verify source exists
		_, exists := registry.GetSourceByID(sourceID)
		assert.True(t, exists, "Source should exist before removal")

		// Remove the source
		err := registry.RemoveSource(sourceID)
		require.NoError(t, err, "Should remove source without error")

		// Verify source is gone
		_, exists = registry.GetSourceByID(sourceID)
		assert.False(t, exists, "Source should not exist after removal")

		// Verify connection mapping is also removed
		_, exists = registry.GetSourceByConnection(testURL)
		assert.False(t, exists, "Connection mapping should be removed")
	})

	t.Run("returns error for non-existent source", func(t *testing.T) {
		err := registry.RemoveSource("non_existent_id")
		require.Error(t, err, "Should return error for non-existent source")
		assert.ErrorIs(t, err, ErrSourceNotFound)
	})
}

// TestRemoveSourceByConnection tests removal by connection string
func TestRemoveSourceByConnection(t *testing.T) {
	registry := newTestRegistry()

	testURL := "rtsp://remove-by-conn.local/stream"
	source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, source)

	t.Run("removes source by connection string", func(t *testing.T) {
		err := registry.RemoveSourceByConnection(testURL)
		require.NoError(t, err, "Should remove source by connection")

		// Verify source is gone
		_, exists := registry.GetSourceByConnection(testURL)
		assert.False(t, exists, "Source should be removed")
	})

	t.Run("returns error for non-existent connection", func(t *testing.T) {
		err := registry.RemoveSourceByConnection("rtsp://never-existed.local/stream")
		assert.Error(t, err, "Should return error for non-existent connection")
	})
}

// TestCleanupInactiveSources tests the inactive source cleanup functionality
func TestCleanupInactiveSources(t *testing.T) {
	registry := newTestRegistry()

	// Create sources with different states
	activeSource := registry.GetOrCreateSource("rtsp://active.local/stream", SourceTypeRTSP)
	require.NotNil(t, activeSource)
	activeSource.IsActive = true
	activeSource.LastSeen = time.Now()

	inactiveRecentSource := registry.GetOrCreateSource("rtsp://inactive-recent.local/stream", SourceTypeRTSP)
	require.NotNil(t, inactiveRecentSource)
	inactiveRecentSource.IsActive = false
	inactiveRecentSource.LastSeen = time.Now() // Recent but inactive

	inactiveOldSource := registry.GetOrCreateSource("rtsp://inactive-old.local/stream", SourceTypeRTSP)
	require.NotNil(t, inactiveOldSource)
	inactiveOldSource.IsActive = false
	inactiveOldSource.LastSeen = time.Now().Add(-2 * time.Hour) // Old and inactive

	t.Run("only removes old inactive sources", func(t *testing.T) {
		// Should only remove sources inactive for more than 1 hour
		removed := registry.CleanupInactiveSources(1 * time.Hour)

		assert.Equal(t, 1, removed, "Should remove exactly 1 source")

		// Active source should still exist
		_, exists := registry.GetSourceByID(activeSource.ID)
		assert.True(t, exists, "Active source should not be removed")

		// Recently inactive source should still exist
		_, exists = registry.GetSourceByID(inactiveRecentSource.ID)
		assert.True(t, exists, "Recently inactive source should not be removed")

		// Old inactive source should be removed
		_, exists = registry.GetSourceByID(inactiveOldSource.ID)
		assert.False(t, exists, "Old inactive source should be removed")
	})

	t.Run("returns zero when nothing to clean", func(t *testing.T) {
		// Create a fresh registry with only active sources
		freshRegistry := newTestRegistry()
		src := freshRegistry.GetOrCreateSource("rtsp://fresh.local/stream", SourceTypeRTSP)
		src.IsActive = true
		src.LastSeen = time.Now()

		removed := freshRegistry.CleanupInactiveSources(1 * time.Hour)
		assert.Equal(t, 0, removed, "Should not remove any active sources")
	})
}

// TestSourceLifecycle tests the full lifecycle of a source
func TestSourceLifecycle(t *testing.T) {
	registry := newTestRegistry()

	testURL := "rtsp://lifecycle.local/stream"

	// Step 1: Create source
	source := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, source, "Should create source")
	sourceID := source.ID

	// Step 2: Update metrics
	registry.UpdateSourceMetrics(sourceID, 1024, false)
	registry.UpdateSourceMetrics(sourceID, 2048, true) // With error

	updatedSource, exists := registry.GetSourceByID(sourceID)
	require.True(t, exists)
	assert.Equal(t, int64(3072), updatedSource.TotalBytes, "Should track total bytes")
	assert.Equal(t, 1, updatedSource.ErrorCount, "Should track error count")

	// Step 3: Mark as active (directly on the source)
	updatedSource.IsActive = true
	updatedSource.LastSeen = time.Now()

	// Step 4: Mark as inactive
	updatedSource.IsActive = false

	// Step 5: Remove source
	err := registry.RemoveSource(sourceID)
	require.NoError(t, err, "Should remove source")

	// Step 6: Verify complete cleanup
	_, exists = registry.GetSourceByID(sourceID)
	assert.False(t, exists, "Source should be completely removed")

	_, exists = registry.GetSourceByConnection(testURL)
	assert.False(t, exists, "Connection mapping should be removed")

	// Verify we can re-register the same URL
	newSource := registry.GetOrCreateSource(testURL, SourceTypeRTSP)
	require.NotNil(t, newSource, "Should be able to re-register URL")
	assert.NotEqual(t, sourceID, newSource.ID, "New source should have different ID")
}
