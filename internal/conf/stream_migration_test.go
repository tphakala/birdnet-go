package conf

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// Test constants to avoid goconst warnings
const testTransportTCP = "tcp"

func TestSettings_MigrateRTSPConfig(t *testing.T) {
	tests := []struct {
		name           string
		settings       Settings
		expectMigrated bool
		expectedCount  int
		expectedNames  []string
	}{
		{
			name: "migrate legacy URLs to streams",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{"rtsp://192.168.1.10/stream", "rtsp://192.168.1.20/stream"},
						Transport: "tcp",
					},
				},
			},
			expectMigrated: true,
			expectedCount:  2,
			expectedNames:  []string{"Stream 1", "Stream 2"},
		},
		{
			name: "skip migration if streams already exist",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						Streams: []StreamConfig{
							{Name: "Existing", URL: "rtsp://192.168.1.10/stream", Type: StreamTypeRTSP},
						},
						URLs: []string{"rtsp://old.url/stream"}, // Should be ignored
					},
				},
			},
			expectMigrated: false,
			expectedCount:  1,
			expectedNames:  []string{"Existing"},
		},
		{
			name: "skip migration if no legacy URLs",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs: []string{},
					},
				},
			},
			expectMigrated: false,
			expectedCount:  0,
		},
		{
			name: "use default transport if not specified",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{"rtsp://192.168.1.10/stream"},
						Transport: "", // Empty should default to tcp
					},
				},
			},
			expectMigrated: true,
			expectedCount:  1,
		},
		{
			name: "preserve udp transport from legacy",
			settings: Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{"rtsp://192.168.1.10/stream"},
						Transport: "udp",
					},
				},
			},
			expectMigrated: true,
			expectedCount:  1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			migrated := tt.settings.MigrateRTSPConfig()

			assert.Equal(t, tt.expectMigrated, migrated)
			assert.Len(t, tt.settings.Realtime.RTSP.Streams, tt.expectedCount)

			if tt.expectMigrated {
				// Verify legacy fields are cleared
				assert.Empty(t, tt.settings.Realtime.RTSP.URLs)
				assert.Empty(t, tt.settings.Realtime.RTSP.Transport)
			}

			// Verify names if expected
			for i, expectedName := range tt.expectedNames {
				require.Greater(t, len(tt.settings.Realtime.RTSP.Streams), i)
				assert.Equal(t, expectedName, tt.settings.Realtime.RTSP.Streams[i].Name)
			}
		})
	}
}

func TestSettings_MigrateRTSPConfig_StreamProperties(t *testing.T) {
	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				URLs:      []string{"rtsp://user:pass@192.168.1.10:554/stream1"},
				Transport: "udp",
			},
		},
	}

	migrated := settings.MigrateRTSPConfig()
	require.True(t, migrated)
	require.Len(t, settings.Realtime.RTSP.Streams, 1)

	stream := settings.Realtime.RTSP.Streams[0]
	assert.Equal(t, "Stream 1", stream.Name)
	assert.Equal(t, "rtsp://user:pass@192.168.1.10:554/stream1", stream.URL)
	assert.Equal(t, StreamTypeRTSP, stream.Type)
	assert.Equal(t, "udp", stream.Transport)
}

func TestSettings_MigrateRTSPConfig_MixedTypes(t *testing.T) {
	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				URLs: []string{
					"rtsp://192.168.1.10/stream",
					"http://192.168.1.50:8000/audio",
					"https://camera.local/live/playlist.m3u8",
					"rtmp://192.168.1.60/live/birdcam",
					"udp://192.168.1.5:1234",
				},
				Transport: "tcp",
			},
		},
	}

	migrated := settings.MigrateRTSPConfig()
	require.True(t, migrated)
	require.Len(t, settings.Realtime.RTSP.Streams, 5)

	// RTSP stream - should have transport
	assert.Equal(t, "Stream 1", settings.Realtime.RTSP.Streams[0].Name)
	assert.Equal(t, StreamTypeRTSP, settings.Realtime.RTSP.Streams[0].Type)
	assert.Equal(t, "tcp", settings.Realtime.RTSP.Streams[0].Transport)

	// HTTP stream - should NOT have transport
	assert.Equal(t, "Stream 2", settings.Realtime.RTSP.Streams[1].Name)
	assert.Equal(t, StreamTypeHTTP, settings.Realtime.RTSP.Streams[1].Type)
	assert.Empty(t, settings.Realtime.RTSP.Streams[1].Transport)

	// HLS stream (detected by .m3u8) - should NOT have transport
	assert.Equal(t, "Stream 3", settings.Realtime.RTSP.Streams[2].Name)
	assert.Equal(t, StreamTypeHLS, settings.Realtime.RTSP.Streams[2].Type)
	assert.Empty(t, settings.Realtime.RTSP.Streams[2].Transport)

	// RTMP stream - should have transport
	assert.Equal(t, "Stream 4", settings.Realtime.RTSP.Streams[3].Name)
	assert.Equal(t, StreamTypeRTMP, settings.Realtime.RTSP.Streams[3].Type)
	assert.Equal(t, "tcp", settings.Realtime.RTSP.Streams[3].Transport)

	// UDP stream - should NOT have transport
	assert.Equal(t, "Stream 5", settings.Realtime.RTSP.Streams[4].Name)
	assert.Equal(t, StreamTypeUDP, settings.Realtime.RTSP.Streams[4].Type)
	assert.Empty(t, settings.Realtime.RTSP.Streams[4].Transport)
}

func TestInferStreamType(t *testing.T) {
	tests := []struct {
		url      string
		expected string
	}{
		{"rtsp://192.168.1.10/stream", StreamTypeRTSP},
		{"RTSP://192.168.1.10/stream", StreamTypeRTSP},
		{"rtsps://secure.cam/stream", StreamTypeRTSP},
		{"http://192.168.1.50/audio", StreamTypeHTTP},
		{"https://secure.server/stream", StreamTypeHTTP},
		{"http://server/live/playlist.m3u8", StreamTypeHLS},
		{"https://cdn.example.com/stream.m3u8", StreamTypeHLS},
		{"rtmp://192.168.1.60/live", StreamTypeRTMP},
		{"rtmps://secure.rtmp/live", StreamTypeRTMP},
		{"udp://192.168.1.5:1234", StreamTypeUDP},
		{"rtp://192.168.1.5:5004", StreamTypeUDP},
		{"unknown://something", StreamTypeRTSP}, // Default
		{"no-scheme-url", StreamTypeRTSP},       // Default
	}

	for _, tt := range tests {
		t.Run(tt.url, func(t *testing.T) {
			t.Parallel()
			result := inferStreamType(tt.url)
			assert.Equal(t, tt.expected, result)
		})
	}
}

// TestSettings_MigrateRTSPConfig_EdgeCases tests migration robustness with messy legacy data
func TestSettings_MigrateRTSPConfig_EdgeCases(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		urls          []string
		transport     string
		expectedCount int
		expectedURLs  []string // Expected URLs after migration (trimmed)
		description   string
	}{
		{
			name:          "whitespace trimming",
			urls:          []string{"  rtsp://192.168.1.1/stream  ", "\trtsp://192.168.1.2/cam\n"},
			transport:     "tcp",
			expectedCount: 2,
			expectedURLs:  []string{"rtsp://192.168.1.1/stream", "rtsp://192.168.1.2/cam"},
			description:   "URLs with leading/trailing whitespace should be trimmed",
		},
		{
			name:          "empty strings filtered",
			urls:          []string{"rtsp://cam1/stream", "", "rtsp://cam2/stream", "   ", "rtsp://cam3/stream"},
			transport:     "tcp",
			expectedCount: 3,
			expectedURLs:  []string{"rtsp://cam1/stream", "rtsp://cam2/stream", "rtsp://cam3/stream"},
			description:   "Empty strings and whitespace-only entries should be skipped",
		},
		{
			name:          "embedded credentials preserved",
			urls:          []string{"rtsp://user:p@ss#word!@192.168.1.1/stream"},
			transport:     "tcp",
			expectedCount: 1,
			expectedURLs:  []string{"rtsp://user:p@ss#word!@192.168.1.1/stream"},
			description:   "Complex credentials with special characters should be preserved exactly",
		},
		{
			name:          "URL encoding preserved",
			urls:          []string{"http://server.com/stream?auth=abc%20def&token=123%26456"},
			transport:     "",
			expectedCount: 1,
			expectedURLs:  []string{"http://server.com/stream?auth=abc%20def&token=123%26456"},
			description:   "URL-encoded characters should be preserved without modification",
		},
		{
			name:          "IPv6 addresses preserved",
			urls:          []string{"rtsp://[2001:db8::1]:554/stream", "rtsp://[::1]/local"},
			transport:     "tcp",
			expectedCount: 2,
			expectedURLs:  []string{"rtsp://[2001:db8::1]:554/stream", "rtsp://[::1]/local"},
			description:   "IPv6 addresses in brackets should be preserved correctly",
		},
		{
			name:          "long URLs preserved",
			urls:          []string{"http://very-long-domain-name-that-exceeds-normal-lengths.example.com:8080/very/long/path/to/streaming/endpoint?param1=value1&param2=value2&param3=value3&token=abc123def456ghi789jkl012mno345"},
			transport:     "",
			expectedCount: 1,
			expectedURLs:  []string{"http://very-long-domain-name-that-exceeds-normal-lengths.example.com:8080/very/long/path/to/streaming/endpoint?param1=value1&param2=value2&param3=value3&token=abc123def456ghi789jkl012mno345"},
			description:   "Long URLs should be preserved without truncation",
		},
		{
			name:          "multicast UDP addresses",
			urls:          []string{"udp://@239.255.0.1:1234", "rtp://@224.0.1.100:5004?buffer_size=65535"},
			transport:     "",
			expectedCount: 2,
			expectedURLs:  []string{"udp://@239.255.0.1:1234", "rtp://@224.0.1.100:5004?buffer_size=65535"},
			description:   "Multicast addresses with @ prefix should be preserved",
		},
		{
			name:          "single valid URL among garbage",
			urls:          []string{"", "   ", "\t\n", "rtsp://valid.cam/stream", "", "  "},
			transport:     "tcp",
			expectedCount: 1,
			expectedURLs:  []string{"rtsp://valid.cam/stream"},
			description:   "Single valid URL should be extracted from list with empty/whitespace entries",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			settings := Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      tt.urls,
						Transport: tt.transport,
					},
				},
			}

			migrated := settings.MigrateRTSPConfig()

			if tt.expectedCount > 0 {
				require.True(t, migrated, "Migration should occur: %s", tt.description)
				require.Len(t, settings.Realtime.RTSP.Streams, tt.expectedCount, "Stream count mismatch: %s", tt.description)

				// Verify URLs are preserved exactly (after trimming)
				for i, expectedURL := range tt.expectedURLs {
					assert.Equal(t, expectedURL, settings.Realtime.RTSP.Streams[i].URL,
						"Stream %d URL mismatch: %s", i, tt.description)
				}

				// Verify legacy fields are cleared
				assert.Empty(t, settings.Realtime.RTSP.URLs, "Legacy URLs should be cleared")
			} else {
				require.False(t, migrated, "Migration should not occur when no valid URLs")
			}
		})
	}
}

// TestSettings_MigrateRTSPConfig_NoDataLoss ensures NO user configuration is lost during migration
func TestSettings_MigrateRTSPConfig_NoDataLoss(t *testing.T) {
	t.Parallel()

	// Original legacy URLs - these represent actual user configurations that MUST be preserved
	originalURLs := []string{
		"rtsp://admin:secretpass123@192.168.1.100:554/h264/main/av_stream",
		"http://icecast.local:8000/bird-audio?nocache=true",
		"https://cdn.birdcam.net/live/playlist.m3u8?token=abc123&user=tester",
		"rtmp://streaming.server.com/live/birdnet-stream",
		"udp://@239.0.0.1:1234?pkt_size=1316",
		"rtsps://secure-camera.home/stream",
	}
	originalTransport := testTransportTCP

	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				URLs:      make([]string, len(originalURLs)),
				Transport: originalTransport,
			},
		},
	}
	copy(settings.Realtime.RTSP.URLs, originalURLs)

	// Perform migration
	migrated := settings.MigrateRTSPConfig()
	require.True(t, migrated, "Migration should have occurred")

	// CRITICAL: Verify no URLs were lost
	require.Len(t, settings.Realtime.RTSP.Streams, len(originalURLs),
		"CRITICAL: URL count changed during migration - data loss detected!")

	// Verify each URL is preserved EXACTLY
	for i, originalURL := range originalURLs {
		stream := settings.Realtime.RTSP.Streams[i]

		assert.Equal(t, originalURL, stream.URL,
			"CRITICAL: URL %d was modified during migration!\nOriginal: %s\nMigrated: %s",
			i, originalURL, stream.URL)

		// Verify auto-generated name format
		assert.Equal(t, "Stream "+string(rune('1'+i)), stream.Name,
			"Stream %d should have auto-generated name", i)
	}

	// Verify transport preservation for RTSP/RTMP types
	for i, stream := range settings.Realtime.RTSP.Streams {
		if stream.Type == StreamTypeRTSP || stream.Type == StreamTypeRTMP {
			assert.Equal(t, originalTransport, stream.Transport,
				"Stream %d (%s): Transport should be preserved for RTSP/RTMP types",
				i, stream.Name)
		}
	}
}

// TestSettings_MigrateRTSPConfig_ExistingStreamsNotModified ensures existing streams are never touched
func TestSettings_MigrateRTSPConfig_ExistingStreamsNotModified(t *testing.T) {
	t.Parallel()

	existingStreams := []StreamConfig{
		{Name: "My Camera 1", URL: "rtsp://192.168.1.10/cam", Type: StreamTypeRTSP, Transport: "udp"},
		{Name: "Icecast Feed", URL: "http://server:8000/audio", Type: StreamTypeHTTP},
		{Name: "HLS Stream", URL: "https://cdn.example.com/live.m3u8", Type: StreamTypeHLS},
	}

	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				Streams: make([]StreamConfig, len(existingStreams)),
				URLs:    []string{"rtsp://should-be-ignored.com/stream"}, // Legacy URLs should be ignored
			},
		},
	}
	copy(settings.Realtime.RTSP.Streams, existingStreams)

	// Attempt migration
	migrated := settings.MigrateRTSPConfig()
	require.False(t, migrated, "Migration should NOT occur when streams already exist")

	// Verify existing streams are UNCHANGED
	require.Len(t, settings.Realtime.RTSP.Streams, len(existingStreams),
		"Existing stream count should not change")

	for i, original := range existingStreams {
		actual := settings.Realtime.RTSP.Streams[i]
		assert.Equal(t, original.Name, actual.Name, "Stream %d name changed!", i)
		assert.Equal(t, original.URL, actual.URL, "Stream %d URL changed!", i)
		assert.Equal(t, original.Type, actual.Type, "Stream %d type changed!", i)
		assert.Equal(t, original.Transport, actual.Transport, "Stream %d transport changed!", i)
	}
}

// TestSettings_MigrateRTSPConfig_Idempotent ensures migration can be safely called multiple times
func TestSettings_MigrateRTSPConfig_Idempotent(t *testing.T) {
	t.Parallel()

	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				URLs:      []string{"rtsp://192.168.1.1/cam1", "rtsp://192.168.1.2/cam2"},
				Transport: "tcp",
			},
		},
	}

	// First migration
	migrated1 := settings.MigrateRTSPConfig()
	require.True(t, migrated1, "First migration should occur")
	firstStreams := make([]StreamConfig, len(settings.Realtime.RTSP.Streams))
	copy(firstStreams, settings.Realtime.RTSP.Streams)

	// Second migration attempt (should be no-op)
	migrated2 := settings.MigrateRTSPConfig()
	require.False(t, migrated2, "Second migration should not occur (already migrated)")

	// Verify streams unchanged after second call
	require.Len(t, settings.Realtime.RTSP.Streams, len(firstStreams),
		"Stream count should not change on second migration call")

	for i, first := range firstStreams {
		actual := settings.Realtime.RTSP.Streams[i]
		assert.Equal(t, first, actual, "Stream %d was modified on second migration call", i)
	}

	// Third migration attempt
	migrated3 := settings.MigrateRTSPConfig()
	require.False(t, migrated3, "Third migration should not occur")
}

// TestSettings_MigrateRTSPConfig_TypeInferenceAccuracy tests type inference correctness
func TestSettings_MigrateRTSPConfig_TypeInferenceAccuracy(t *testing.T) {
	t.Parallel()

	tests := []struct {
		url          string
		expectedType string
		description  string
	}{
		// RTSP variants
		{"rtsp://192.168.1.1/stream", StreamTypeRTSP, "Standard RTSP"},
		{"RTSP://192.168.1.1/stream", StreamTypeRTSP, "Uppercase RTSP"},
		{"rtsps://192.168.1.1/stream", StreamTypeRTSP, "Secure RTSPS"},
		{"RtSp://192.168.1.1/stream", StreamTypeRTSP, "Mixed case RTSP"},

		// HTTP variants
		{"http://192.168.1.1/audio", StreamTypeHTTP, "Standard HTTP"},
		{"HTTP://192.168.1.1/audio", StreamTypeHTTP, "Uppercase HTTP"},
		{"https://192.168.1.1/audio", StreamTypeHTTP, "Secure HTTPS"},

		// HLS detection (HTTP with .m3u8)
		{"http://server/live.m3u8", StreamTypeHLS, "HLS with HTTP"},
		{"https://server/live.m3u8", StreamTypeHLS, "HLS with HTTPS"},
		{"http://server/path/playlist.M3U8", StreamTypeHLS, "HLS with uppercase extension"},
		{"https://cdn.example.com/stream/index.m3u8?token=abc", StreamTypeHLS, "HLS with query params"},

		// RTMP variants
		{"rtmp://server/app/stream", StreamTypeRTMP, "Standard RTMP"},
		{"RTMP://server/app/stream", StreamTypeRTMP, "Uppercase RTMP"},
		{"rtmps://server/app/stream", StreamTypeRTMP, "Secure RTMPS"},

		// UDP/RTP variants
		{"udp://192.168.1.1:1234", StreamTypeUDP, "Standard UDP"},
		{"UDP://192.168.1.1:1234", StreamTypeUDP, "Uppercase UDP"},
		{"rtp://192.168.1.1:5004", StreamTypeUDP, "RTP (treated as UDP)"},
		{"RTP://239.0.0.1:5004", StreamTypeUDP, "Uppercase RTP multicast"},

		// Edge cases - default to RTSP
		{"ftp://server/file", StreamTypeRTSP, "Unsupported protocol defaults to RTSP"},
		{"unknown://server/stream", StreamTypeRTSP, "Unknown protocol defaults to RTSP"},
		{"192.168.1.1/stream", StreamTypeRTSP, "No scheme defaults to RTSP"},
	}

	for _, tt := range tests {
		t.Run(tt.description, func(t *testing.T) {
			t.Parallel()

			settings := Settings{
				Realtime: RealtimeSettings{
					RTSP: RTSPSettings{
						URLs:      []string{tt.url},
						Transport: "tcp",
					},
				},
			}

			migrated := settings.MigrateRTSPConfig()
			require.True(t, migrated)
			require.Len(t, settings.Realtime.RTSP.Streams, 1)

			assert.Equal(t, tt.expectedType, settings.Realtime.RTSP.Streams[0].Type,
				"Type inference failed for %s", tt.description)
		})
	}
}

// TestSettings_MigrateRTSPConfig_TransportAssignment verifies transport is only set for appropriate types
func TestSettings_MigrateRTSPConfig_TransportAssignment(t *testing.T) {
	t.Parallel()

	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: RTSPSettings{
				URLs: []string{
					"rtsp://cam1/stream",    // Should get transport
					"rtsps://cam2/stream",   // Should get transport
					"http://audio/stream",   // Should NOT get transport
					"https://hls/live.m3u8", // Should NOT get transport (HLS)
					"rtmp://live/stream",    // Should get transport
					"rtmps://live/stream",   // Should get transport
					"udp://239.0.0.1:1234",  // Should NOT get transport
					"rtp://239.0.0.1:5004",  // Should NOT get transport
				},
				Transport: "udp",
			},
		},
	}

	migrated := settings.MigrateRTSPConfig()
	require.True(t, migrated)

	// Map expected transport assignment
	expectedTransports := map[int]string{
		0: "udp", // RTSP
		1: "udp", // RTSPS
		2: "",    // HTTP
		3: "",    // HLS
		4: "udp", // RTMP
		5: "udp", // RTMPS
		6: "",    // UDP
		7: "",    // RTP
	}

	for i, stream := range settings.Realtime.RTSP.Streams {
		expected := expectedTransports[i]
		assert.Equal(t, expected, stream.Transport,
			"Stream %d (%s, type=%s): unexpected transport value",
			i, stream.URL, stream.Type)
	}
}
