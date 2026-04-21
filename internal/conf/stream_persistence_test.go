package conf

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/spf13/viper"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"gopkg.in/yaml.v3"
)

// TestStreamConfig_YAML_RoundTrip tests that StreamConfig correctly marshals/unmarshals to YAML
func TestStreamConfig_YAML_RoundTrip(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name   string
		stream StreamConfig
	}{
		{
			name: "basic RTSP stream",
			stream: StreamConfig{
				Name:      "Front Yard",
				URL:       "rtsp://192.168.1.10/stream",
				Enabled:   true,
				Type:      StreamTypeRTSP,
				Transport: "tcp",
			},
		},
		{
			name: "unicode stream name",
			stream: StreamConfig{
				Name:      "Caméra Jardin 🌳",
				URL:       "rtsp://192.168.1.20/cam",
				Enabled:   true,
				Type:      StreamTypeRTSP,
				Transport: "udp",
			},
		},
		{
			name: "URL with query parameters",
			stream: StreamConfig{
				Name:    "Complex URL",
				URL:     "http://server.local/stream?auth=abc123&format=h264&quality=high",
				Enabled: true,
				Type:    StreamTypeHTTP,
			},
		},
		{
			name: "IPv6 address URL",
			stream: StreamConfig{
				Name:      "IPv6 Camera",
				URL:       "rtsp://[2001:db8::1]:554/stream",
				Enabled:   true,
				Type:      StreamTypeRTSP,
				Transport: "tcp",
			},
		},
		{
			name: "URL with special characters",
			stream: StreamConfig{
				Name:    "Special Chars",
				URL:     "rtmp://server.com/app/stream-name_v2.0?key=abc%20def&token=123%26456",
				Enabled: true,
				Type:    StreamTypeRTMP,
			},
		},
		{
			name: "empty transport field",
			stream: StreamConfig{
				Name:      "No Transport",
				URL:       "rtsp://192.168.1.30/cam",
				Enabled:   true,
				Type:      StreamTypeRTSP,
				Transport: "", // Should remain empty after round-trip
			},
		},
		{
			name: "URL with embedded credentials",
			stream: StreamConfig{
				Name:      "Credential URL",
				URL:       "rtsp://admin:p@ssw0rd!#$@192.168.1.40:554/stream",
				Enabled:   true,
				Type:      StreamTypeRTSP,
				Transport: "tcp",
			},
		},
		{
			name: "HLS with HTTPS",
			stream: StreamConfig{
				Name:    "Secure HLS",
				URL:     "https://cdn.example.com/live/playlist.m3u8?token=xyz",
				Enabled: true,
				Type:    StreamTypeHLS,
			},
		},
		{
			name: "UDP with multicast",
			stream: StreamConfig{
				Name:    "Multicast Feed",
				URL:     "udp://@239.0.0.1:1234?pkt_size=1316",
				Enabled: true,
				Type:    StreamTypeUDP,
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			// Marshal to YAML
			data, err := yaml.Marshal(&tt.stream)
			require.NoError(t, err, "Failed to marshal StreamConfig to YAML")

			// Unmarshal back
			var result StreamConfig
			err = yaml.Unmarshal(data, &result)
			require.NoError(t, err, "Failed to unmarshal StreamConfig from YAML")

			// Verify all fields preserved
			assert.Equal(t, tt.stream.Name, result.Name, "Name mismatch")
			assert.Equal(t, tt.stream.URL, result.URL, "URL mismatch")
			assert.Equal(t, tt.stream.IsEnabled(), result.IsEnabled(), "Enabled mismatch")
			assert.Equal(t, tt.stream.Type, result.Type, "Type mismatch")
			assert.Equal(t, tt.stream.Transport, result.Transport, "Transport mismatch")
		})
	}
}

// TestRTSPSettings_YAML_RoundTrip tests the full RTSPSettings structure round-trip
func TestRTSPSettings_YAML_RoundTrip(t *testing.T) {
	t.Parallel()

	original := RTSPSettings{
		Streams: []StreamConfig{
			{Name: "Stream 1", URL: "rtsp://192.168.1.10/cam1", Enabled: true, Type: StreamTypeRTSP, Transport: "tcp"},
			{Name: "Stream 2", URL: "http://192.168.1.20:8000/audio", Enabled: true, Type: StreamTypeHTTP},
			{Name: "Stream 3", URL: "udp://239.0.0.1:5004", Enabled: false, Type: StreamTypeUDP},
		},
	}

	// Marshal to YAML
	data, err := yaml.Marshal(&original)
	require.NoError(t, err)

	// Unmarshal back
	var result RTSPSettings
	err = yaml.Unmarshal(data, &result)
	require.NoError(t, err)

	// Verify stream count
	require.Len(t, result.Streams, len(original.Streams), "Stream count mismatch")

	// Verify each stream preserved exactly
	for i := range original.Streams {
		assert.Equal(t, original.Streams[i].Name, result.Streams[i].Name, "Stream %d Name mismatch", i)
		assert.Equal(t, original.Streams[i].URL, result.Streams[i].URL, "Stream %d URL mismatch", i)
		assert.Equal(t, original.Streams[i].IsEnabled(), result.Streams[i].IsEnabled(), "Stream %d Enabled mismatch", i)
		assert.Equal(t, original.Streams[i].Type, result.Streams[i].Type, "Stream %d Type mismatch", i)
		assert.Equal(t, original.Streams[i].Transport, result.Streams[i].Transport, "Stream %d Transport mismatch", i)
	}
}

// TestStreamConfig_FilePersistence tests actual file write/read operations
func TestStreamConfig_FilePersistence(t *testing.T) {
	t.Parallel()

	// Create temp directory
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "config_test.yaml")

	// Build test configuration with various stream types
	original := struct {
		RTSP RTSPSettings `yaml:"rtsp"`
	}{
		RTSP: RTSPSettings{
			Streams: []StreamConfig{
				{
					Name:      "Unicode Test 日本語 🐦",
					URL:       "rtsp://user:pass@192.168.1.100:554/stream",
					Enabled:   true,
					Type:      StreamTypeRTSP,
					Transport: "tcp",
				},
				{
					Name:    "Query Params & Ampersand",
					URL:     "http://example.com/stream?a=1&b=2&c=test%20value",
					Enabled: true,
					Type:    StreamTypeHTTP,
				},
				{
					Name:      "Secure RTMP",
					URL:       "rtmps://live.twitch.tv/app/streamkey",
					Enabled:   true,
					Type:      StreamTypeRTMP,
					Transport: "tcp",
				},
			},
		},
	}

	// Write to file
	data, err := yaml.Marshal(&original)
	require.NoError(t, err)
	err = os.WriteFile(configPath, data, 0o600)
	require.NoError(t, err)

	// Read back from file
	readData, err := os.ReadFile(configPath) //nolint:gosec // G304 - test file path from t.TempDir()
	require.NoError(t, err)

	var loaded struct {
		RTSP RTSPSettings `yaml:"rtsp"`
	}
	err = yaml.Unmarshal(readData, &loaded)
	require.NoError(t, err)

	// Verify complete preservation
	require.Len(t, loaded.RTSP.Streams, len(original.RTSP.Streams), "Stream count changed after file persistence")

	for i, orig := range original.RTSP.Streams {
		loaded := loaded.RTSP.Streams[i]
		assert.Equal(t, orig.Name, loaded.Name, "Stream %d: Name was modified", i)
		assert.Equal(t, orig.URL, loaded.URL, "Stream %d: URL was modified", i)
		assert.Equal(t, orig.IsEnabled(), loaded.IsEnabled(), "Stream %d: Enabled was modified", i)
		assert.Equal(t, orig.Type, loaded.Type, "Stream %d: Type was modified", i)
		assert.Equal(t, orig.Transport, loaded.Transport, "Stream %d: Transport was modified", i)
	}
}

// TestStreamConfig_NoDataLoss_AllFieldsPreserved ensures no fields are silently dropped
func TestStreamConfig_NoDataLoss_AllFieldsPreserved(t *testing.T) {
	t.Parallel()

	// Comprehensive stream with all fields populated
	original := StreamConfig{
		Name:      "Complete Stream Config",
		URL:       "rtsp://admin:secret123@camera.local:554/h264/main/av_stream",
		Enabled:   false,
		Type:      StreamTypeRTSP,
		Transport: "udp",
	}

	// Round-trip through YAML
	data, err := yaml.Marshal(&original)
	require.NoError(t, err)

	var result StreamConfig
	err = yaml.Unmarshal(data, &result)
	require.NoError(t, err)

	// Use reflection-style comparison to catch any added fields
	assert.Equal(t, original, result, "StreamConfig fields were lost during YAML round-trip")
}

// TestLegacyConfigFields_Preserved ensures legacy fields don't interfere with new structure
func TestLegacyConfigFields_Preserved(t *testing.T) {
	t.Parallel()

	// YAML with both new and legacy fields (simulating transitional config)
	yamlContent := `
streams:
  - name: "Modern Stream"
    url: "rtsp://192.168.1.10/stream"
    type: "rtsp"
    transport: "tcp"
urls:
  - "rtsp://legacy.url/stream"
transport: "udp"
`

	var rtsp RTSPSettings
	err := yaml.Unmarshal([]byte(yamlContent), &rtsp)
	require.NoError(t, err)

	// New format takes precedence but legacy should still be loadable
	assert.Len(t, rtsp.Streams, 1, "New streams should be loaded")
	assert.Equal(t, "Modern Stream", rtsp.Streams[0].Name)

	// Legacy fields should also be populated (before migration clears them)
	assert.Len(t, rtsp.URLs, 1, "Legacy URLs should be preserved")
	assert.Equal(t, "udp", rtsp.Transport, "Legacy Transport should be preserved")
}

func TestStreamConfig_IsEnabled(t *testing.T) {
	t.Parallel()

	assert.True(t, (&StreamConfig{Enabled: true}).IsEnabled())
	assert.False(t, (&StreamConfig{Enabled: false}).IsEnabled())
}

func TestNormalizeRTSPStreamEnabledDefaults(t *testing.T) {
	t.Parallel()

	normalized, migrated := normalizeRTSPStreamEnabledDefaults([]any{
		map[string]any{
			"name": "Legacy Stream",
			"url":  "rtsp://192.168.1.10/stream",
			"type": StreamTypeRTSP,
		},
		map[string]any{
			"name":    "Disabled Stream",
			"url":     "rtsp://192.168.1.20/stream",
			"enabled": false,
			"type":    StreamTypeRTSP,
		},
		"not-a-stream-map",
	})

	require.True(t, migrated, "legacy enabled omissions should be materialized before unmarshal")
	require.Len(t, normalized, 3)

	first, ok := normalized[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, first["enabled"])

	second, ok := normalized[1].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, false, second["enabled"])

	assert.Equal(t, "not-a-stream-map", normalized[2])
}

func TestNormalizeRTSPStreamEnabledDefaults_NoChangesNeeded(t *testing.T) {
	t.Parallel()

	normalized, migrated := normalizeRTSPStreamEnabledDefaults([]any{
		map[string]any{
			"name":    "Explicit Stream",
			"url":     "rtsp://192.168.1.10/stream",
			"enabled": true,
			"type":    StreamTypeRTSP,
		},
	})

	assert.False(t, migrated)
	assert.Nil(t, normalized)
}

func TestNormalizeRTSPStreamEnabledDefaults_NilValue(t *testing.T) {
	t.Parallel()

	normalized, migrated := normalizeRTSPStreamEnabledDefaults([]any{
		map[string]any{
			"name":    "Null Enabled",
			"url":     "rtsp://192.168.1.10/stream",
			"enabled": nil,
			"type":    StreamTypeRTSP,
		},
	})

	require.True(t, migrated, "nil enabled value should be treated as missing")
	require.Len(t, normalized, 1)
	first, ok := normalized[0].(map[string]any)
	require.True(t, ok)
	assert.Equal(t, true, first["enabled"])
}

func TestNormalizeRTSPStreamEnabledDefaults_NonSliceInput(t *testing.T) {
	t.Parallel()

	normalized, migrated := normalizeRTSPStreamEnabledDefaults(map[string]any{
		"name": "not-a-slice",
		"url":  "rtsp://192.168.1.10/stream",
	})

	assert.False(t, migrated)
	assert.Nil(t, normalized)
}

func TestLoad_MigratesMissingStreamEnabledToTrue(t *testing.T) {
	configPath := filepath.Join(t.TempDir(), "config.yaml")
	configYAML := `
realtime:
  rtsp:
    streams:
      - name: "Legacy Stream"
        url: "rtsp://192.168.1.10/stream"
        type: "rtsp"
        transport: "tcp"
      - name: "Explicitly Disabled"
        url: "rtsp://192.168.1.20/stream"
        enabled: false
        type: "rtsp"
        transport: "tcp"
`
	err := os.WriteFile(configPath, []byte(configYAML), 0o600)
	require.NoError(t, err)

	oldPath := ConfigPath
	oldSettings := GetSettings()
	t.Cleanup(func() {
		ConfigPath = oldPath
		viper.Reset()
		SetTestSettings(oldSettings)
	})

	ConfigPath = configPath

	settings, err := Load()
	require.NoError(t, err)
	require.Len(t, settings.Realtime.RTSP.Streams, 2)
	assert.True(t, settings.Realtime.RTSP.Streams[0].Enabled)
	assert.False(t, settings.Realtime.RTSP.Streams[1].Enabled)

	persisted, err := os.ReadFile(configPath)
	require.NoError(t, err)
	assert.Contains(t, string(persisted), "enabled: true")
	assert.Contains(t, string(persisted), "enabled: false")
}

// TestBackwardCompatibility_LegacyOnlyConfig tests loading a pure legacy config format
func TestBackwardCompatibility_LegacyOnlyConfig(t *testing.T) {
	t.Parallel()

	// Simulating a config from before the migration (only has urls and transport)
	legacyYAML := `
urls:
  - "rtsp://192.168.1.10/stream1"
  - "rtsp://192.168.1.20/stream2"
  - "http://192.168.1.30:8000/audio"
transport: "tcp"
`

	var rtsp RTSPSettings
	err := yaml.Unmarshal([]byte(legacyYAML), &rtsp)
	require.NoError(t, err)

	// Streams should be empty (not yet migrated)
	assert.Empty(t, rtsp.Streams, "Streams should be empty in legacy format")

	// Legacy fields should be loaded
	require.Len(t, rtsp.URLs, 3, "All legacy URLs should be loaded")
	assert.Equal(t, "rtsp://192.168.1.10/stream1", rtsp.URLs[0])
	assert.Equal(t, "rtsp://192.168.1.20/stream2", rtsp.URLs[1])
	assert.Equal(t, "http://192.168.1.30:8000/audio", rtsp.URLs[2])
	assert.Equal(t, "tcp", rtsp.Transport)
}

// TestBackwardCompatibility_NewFormatOnly tests loading a new format config (no legacy fields)
func TestBackwardCompatibility_NewFormatOnly(t *testing.T) {
	t.Parallel()

	// Modern config format (only streams, no legacy urls/transport)
	modernYAML := `
streams:
  - name: "Front Camera"
    url: "rtsp://192.168.1.10/stream"
    type: "rtsp"
    transport: "tcp"
  - name: "Back Camera"
    url: "rtsp://192.168.1.20/stream"
    type: "rtsp"
    transport: "udp"
`

	var rtsp RTSPSettings
	err := yaml.Unmarshal([]byte(modernYAML), &rtsp)
	require.NoError(t, err)

	// Streams should be properly loaded
	require.Len(t, rtsp.Streams, 2, "All streams should be loaded")
	assert.Equal(t, "Front Camera", rtsp.Streams[0].Name)
	assert.Equal(t, "rtsp://192.168.1.10/stream", rtsp.Streams[0].URL)
	assert.Equal(t, StreamTypeRTSP, rtsp.Streams[0].Type)
	assert.Equal(t, "tcp", rtsp.Streams[0].Transport)

	assert.Equal(t, "Back Camera", rtsp.Streams[1].Name)
	assert.Equal(t, "udp", rtsp.Streams[1].Transport)

	// Legacy fields should be empty
	assert.Empty(t, rtsp.URLs, "Legacy URLs should be empty in new format")
	assert.Empty(t, rtsp.Transport, "Legacy Transport should be empty in new format")
}

// TestBackwardCompatibility_MigrationFromFile simulates end-to-end legacy file migration
func TestBackwardCompatibility_MigrationFromFile(t *testing.T) {
	t.Parallel()

	// Create a realistic legacy config file
	tmpDir := t.TempDir()
	legacyConfigPath := filepath.Join(tmpDir, "legacy_rtsp.yaml")

	legacyContent := `# Legacy BirdNET-Go RTSP configuration
# This format was used before v1.x
urls:
  - "rtsp://admin:pass123@192.168.1.100:554/h264/main/av_stream"
  - "http://icecast.local:8000/birdsong"
  - "https://cdn.example.com/live/playlist.m3u8"
transport: "tcp"
`

	err := os.WriteFile(legacyConfigPath, []byte(legacyContent), 0o600)
	require.NoError(t, err)

	// Read the legacy file
	data, err := os.ReadFile(legacyConfigPath) //nolint:gosec // G304 - test file path from t.TempDir()
	require.NoError(t, err)

	var rtsp RTSPSettings
	err = yaml.Unmarshal(data, &rtsp)
	require.NoError(t, err)

	// Verify legacy data is loaded
	require.Len(t, rtsp.URLs, 3, "Legacy URLs should be loaded from file")
	assert.Equal(t, "tcp", rtsp.Transport)

	// Create a Settings wrapper and run migration
	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: rtsp,
		},
	}

	migrated := settings.MigrateRTSPConfig()
	require.True(t, migrated, "Migration should occur")

	// Verify migration results
	require.Len(t, settings.Realtime.RTSP.Streams, 3, "All URLs should be migrated to streams")

	// Stream 1: RTSP with credentials
	assert.Equal(t, "Stream 1", settings.Realtime.RTSP.Streams[0].Name)
	assert.Equal(t, "rtsp://admin:pass123@192.168.1.100:554/h264/main/av_stream", settings.Realtime.RTSP.Streams[0].URL)
	assert.Equal(t, StreamTypeRTSP, settings.Realtime.RTSP.Streams[0].Type)
	assert.Equal(t, "tcp", settings.Realtime.RTSP.Streams[0].Transport)

	// Stream 2: HTTP (no transport)
	assert.Equal(t, "Stream 2", settings.Realtime.RTSP.Streams[1].Name)
	assert.Equal(t, StreamTypeHTTP, settings.Realtime.RTSP.Streams[1].Type)
	assert.Empty(t, settings.Realtime.RTSP.Streams[1].Transport, "HTTP streams should not have transport")

	// Stream 3: HLS (detected from .m3u8)
	assert.Equal(t, "Stream 3", settings.Realtime.RTSP.Streams[2].Name)
	assert.Equal(t, StreamTypeHLS, settings.Realtime.RTSP.Streams[2].Type)
	assert.Empty(t, settings.Realtime.RTSP.Streams[2].Transport, "HLS streams should not have transport")

	// Legacy fields should be cleared
	assert.Empty(t, settings.Realtime.RTSP.URLs)
	assert.Empty(t, settings.Realtime.RTSP.Transport)

	// Now save the migrated config and verify it can be reloaded
	migratedData, err := yaml.Marshal(&settings.Realtime.RTSP)
	require.NoError(t, err)

	var reloaded RTSPSettings
	err = yaml.Unmarshal(migratedData, &reloaded)
	require.NoError(t, err)

	// Verify reloaded config matches
	require.Len(t, reloaded.Streams, 3)
	for i, original := range settings.Realtime.RTSP.Streams {
		assert.Equal(t, original.Name, reloaded.Streams[i].Name)
		assert.Equal(t, original.URL, reloaded.Streams[i].URL)
		assert.Equal(t, original.Type, reloaded.Streams[i].Type)
		assert.Equal(t, original.Transport, reloaded.Streams[i].Transport)
	}
}

// TestBackwardCompatibility_EmptyConfig tests handling of completely empty config
func TestBackwardCompatibility_EmptyConfig(t *testing.T) {
	t.Parallel()

	// Empty YAML
	emptyYAML := ``

	var rtsp RTSPSettings
	err := yaml.Unmarshal([]byte(emptyYAML), &rtsp)
	require.NoError(t, err)

	assert.Empty(t, rtsp.Streams)
	assert.Empty(t, rtsp.URLs)
	assert.Empty(t, rtsp.Transport)

	// Migration should not occur (no data)
	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: rtsp,
		},
	}

	migrated := settings.MigrateRTSPConfig()
	assert.False(t, migrated, "No migration should occur for empty config")
}

// TestBackwardCompatibility_MixedFormatPriority tests that new format takes priority
func TestBackwardCompatibility_MixedFormatPriority(t *testing.T) {
	t.Parallel()

	// Config with both old and new format (edge case during upgrade)
	mixedYAML := `
streams:
  - name: "Primary Camera"
    url: "rtsp://192.168.1.10/main"
    type: "rtsp"
    transport: "tcp"
urls:
  - "rtsp://192.168.1.20/legacy"
  - "rtsp://192.168.1.30/legacy2"
transport: "udp"
`

	var rtsp RTSPSettings
	err := yaml.Unmarshal([]byte(mixedYAML), &rtsp)
	require.NoError(t, err)

	// Both should be loaded
	assert.Len(t, rtsp.Streams, 1, "New format streams should be loaded")
	assert.Len(t, rtsp.URLs, 2, "Legacy URLs should also be present")

	// Migration should NOT occur (streams already exist)
	settings := Settings{
		Realtime: RealtimeSettings{
			RTSP: rtsp,
		},
	}

	migrated := settings.MigrateRTSPConfig()
	assert.False(t, migrated, "Migration should NOT occur when streams already exist")

	// Streams should remain unchanged
	assert.Len(t, settings.Realtime.RTSP.Streams, 1)
	assert.Equal(t, "Primary Camera", settings.Realtime.RTSP.Streams[0].Name)

	// Legacy fields are NOT modified when migration is skipped
	assert.Len(t, settings.Realtime.RTSP.URLs, 2, "Legacy URLs should remain when migration skipped")
}
