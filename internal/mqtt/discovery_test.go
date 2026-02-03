// discovery_test.go: Tests for Home Assistant MQTT auto-discovery implementation.
package mqtt

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
)

// TestSanitizeID verifies the ID sanitization function for MQTT topics and HA entity IDs.
func TestSanitizeID(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		input    string
		expected string
	}{
		// Valid inputs - should pass through
		{"Simple alphanumeric", "test123", "test123"},
		{"With underscore", "test_id", "test_id"},
		{"With hyphen", "test-id", "test-id"},
		{"Mixed valid chars", "Test_ID-123", "Test_ID-123"},

		// Characters that need replacement
		{"Spaces to underscore", "test id", "test_id"},
		{"Colons to underscore", "hw:0,0", "hw_0_0"},
		{"Multiple special chars", "test@#$%id", "test_id"},
		{"Path separators", "path/to/source", "path_to_source"},
		{"Dots to underscore", "device.name", "device_name"},

		// Consecutive underscores
		{"Double underscore cleanup", "test__id", "test_id"},
		{"Triple underscore cleanup", "test___id", "test_id"},
		{"Multiple specials become one", "test@@@id", "test_id"},

		// Edge cases
		{"Leading special chars", "@@@test", "test"},
		{"Trailing special chars", "test@@@", "test"},
		{"All special chars", "@#$%", "unknown"},
		{"Empty string", "", "unknown"},
		{"Single underscore", "_", "unknown"},
		{"Only spaces", "   ", "unknown"},

		// Real-world examples
		{"ALSA device", "hw:0,0 USB Audio", "hw_0_0_USB_Audio"},
		{"PulseAudio sink", "alsa_output.pci-0000_00_1f.3", "alsa_output_pci-0000_00_1f_3"},
		{"Network stream", "http://192.168.1.1:8080/stream", "http_192_168_1_1_8080_stream"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			result := SanitizeID(tt.input)
			assert.Equal(t, tt.expected, result, "SanitizeID(%q) result mismatch", tt.input)
		})
	}
}

// mockPublisher implements a mock MQTT client for testing discovery publishing.
type mockPublisher struct {
	publishedMessages map[string]string // topic -> payload
	publishError      error
}

// Ensure mockPublisher implements the Client interface at compile time
var _ Client = (*mockPublisher)(nil)

func newMockPublisher() *mockPublisher {
	return &mockPublisher{
		publishedMessages: make(map[string]string),
	}
}

func (m *mockPublisher) Connect(_ context.Context) error            { return nil }
func (m *mockPublisher) Disconnect()                                {}
func (m *mockPublisher) IsConnected() bool                          { return true }
func (m *mockPublisher) Publish(_ context.Context, _, _ string) error { return nil }
func (m *mockPublisher) SetControlChannel(_ chan string)            {}
func (m *mockPublisher) TestConnection(_ context.Context, _ chan<- TestResult) {}
func (m *mockPublisher) RegisterOnConnectHandler(_ OnConnectHandler) {}

func (m *mockPublisher) PublishWithRetain(_ context.Context, topic, data string, _ bool) error {
	if m.publishError != nil {
		return m.publishError
	}
	m.publishedMessages[topic] = data
	return nil
}

// TestDiscoveryPayloadStructure verifies the discovery payload JSON structure.
func TestDiscoveryPayloadStructure(t *testing.T) {
	t.Parallel()

	payload := DiscoveryPayload{
		Name:              "Last Species",
		UniqueID:          "birdnet_go_test_default_species",
		StateTopic:        "birdnet/detections",
		ValueTemplate:     "{{ value_json.CommonName }}",
		Icon:              "mdi:bird",
		AvailabilityTopic: "birdnet/status",
		Device: DiscoveryDevice{
			Identifiers:  []string{"birdnet_go_test_default"},
			Name:         "BirdNET-Go Default",
			Manufacturer: "BirdNET-Go",
			Model:        "Audio Analyzer",
			SWVersion:    "1.0.0",
			ViaDevice:    "birdnet_go_test_bridge",
		},
		Origin: &DiscoveryOrigin{
			Name:       "BirdNET-Go",
			SWVersion:  "1.0.0",
			SupportURL: "https://github.com/tphakala/birdnet-go",
		},
	}

	// Marshal to JSON
	data, err := json.Marshal(payload)
	require.NoError(t, err, "Failed to marshal discovery payload")

	// Unmarshal back to verify structure
	var result map[string]any
	err = json.Unmarshal(data, &result)
	require.NoError(t, err, "Failed to unmarshal discovery payload")

	// Verify required fields
	assert.Equal(t, "Last Species", result["name"])
	assert.Equal(t, "birdnet_go_test_default_species", result["unique_id"])
	assert.Equal(t, "birdnet/detections", result["state_topic"])
	assert.Equal(t, "{{ value_json.CommonName }}", result["value_template"])
	assert.Equal(t, "mdi:bird", result["icon"])
	assert.Equal(t, "birdnet/status", result["availability_topic"])

	// Verify device info
	device, ok := result["device"].(map[string]any)
	require.True(t, ok, "Device should be a map")
	assert.Equal(t, "BirdNET-Go Default", device["name"])
	assert.Equal(t, "BirdNET-Go", device["manufacturer"])
	assert.Equal(t, "Audio Analyzer", device["model"])
	assert.Equal(t, "1.0.0", device["sw_version"])
	assert.Equal(t, "birdnet_go_test_bridge", device["via_device"])

	// Verify origin info
	origin, ok := result["origin"].(map[string]any)
	require.True(t, ok, "Origin should be a map")
	assert.Equal(t, "BirdNET-Go", origin["name"])
	assert.Equal(t, "1.0.0", origin["sw_version"])
	assert.Equal(t, "https://github.com/tphakala/birdnet-go", origin["support_url"])
}

// TestPublishBridgeDiscovery verifies the bridge device discovery message.
func TestPublishBridgeDiscovery(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	err := publisher.publishBridgeDiscovery(ctx)
	require.NoError(t, err, "Failed to publish bridge discovery")

	// Verify the topic
	expectedTopic := "homeassistant/binary_sensor/test-node/status/config"
	assert.Contains(t, mock.publishedMessages, expectedTopic, "Bridge discovery topic not found")

	// Parse and verify the payload
	var payload DiscoveryPayload
	err = json.Unmarshal([]byte(mock.publishedMessages[expectedTopic]), &payload)
	require.NoError(t, err, "Failed to parse bridge discovery payload")

	assert.Equal(t, "Status", payload.Name)
	assert.Equal(t, "birdnet_go_test-node_bridge_status", payload.UniqueID)
	assert.Equal(t, "birdnet/status", payload.StateTopic)
	assert.Equal(t, "connectivity", payload.DeviceClass)
	assert.Equal(t, "diagnostic", payload.EntityCategory)
	assert.Equal(t, StatusPayloadOnline, payload.PayloadOn)
	assert.Equal(t, StatusPayloadOffline, payload.PayloadOff)
	// PayloadAvailable/PayloadNotAvailable are intentionally not set
	// since bridge has no AvailabilityTopic - these would be ignored by HA
	assert.Empty(t, payload.PayloadAvailable)
	assert.Empty(t, payload.PayloadNotAvailable)

	// Verify device info
	assert.Equal(t, "BirdNET-Go", payload.Device.Name)
	assert.Equal(t, "BirdNET-Go", payload.Device.Manufacturer)
	assert.Equal(t, "Bridge", payload.Device.Model)
	assert.Equal(t, "1.0.0", payload.Device.SWVersion)
	assert.Contains(t, payload.Device.Identifiers, "birdnet_go_test-node_bridge")
}

// TestPublishSourceDiscovery verifies source device discovery messages.
func TestPublishSourceDiscovery(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	source := datastore.AudioSource{
		ID:          "hw:0,0",
		DisplayName: "USB Microphone",
	}

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				SoundLevel: conf.SoundLevelSettings{
					Enabled: true,
				},
			},
		},
	}

	err := publisher.publishSourceDiscovery(ctx, source, settings)
	require.NoError(t, err, "Failed to publish source discovery")

	// Expected topics for the source sensors
	// Note: When DisplayName is set, it's used for entity IDs instead of source.ID
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.DisplayName) // Use DisplayName since it's set
	baseTopicPrefix := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID

	expectedTopics := []string{
		baseTopicPrefix + "_species/config",
		baseTopicPrefix + "_confidence/config",
		baseTopicPrefix + "_scientific_name/config",
		baseTopicPrefix + "_sound_level/config",
	}

	for _, topic := range expectedTopics {
		assert.Contains(t, mock.publishedMessages, topic, "Expected topic not found: %s", topic)
	}

	// Verify species sensor payload
	speciesTopic := baseTopicPrefix + "_species/config"
	var speciesPayload DiscoveryPayload
	err = json.Unmarshal([]byte(mock.publishedMessages[speciesTopic]), &speciesPayload)
	require.NoError(t, err, "Failed to parse species discovery payload")

	assert.Equal(t, "Last Species", speciesPayload.Name)
	assert.Equal(t, "mdi:bird", speciesPayload.Icon)
	assert.Equal(t, "birdnet", speciesPayload.StateTopic)
	assert.Contains(t, speciesPayload.ValueTemplate, "hw:0,0") // Original source ID in template

	// Verify device has via_device pointing to bridge
	assert.Equal(t, "birdnet_go_test-node_bridge", speciesPayload.Device.ViaDevice)
	assert.Equal(t, "BirdNET-Go USB Microphone", speciesPayload.Device.Name)
}

// TestPublishSourceDiscoveryWithoutSoundLevel verifies sound level sensor is not published when disabled.
func TestPublishSourceDiscoveryWithoutSoundLevel(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	source := datastore.AudioSource{
		ID:          "default",
		DisplayName: "Default",
	}

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				SoundLevel: conf.SoundLevelSettings{
					Enabled: false, // Sound level disabled
				},
			},
		},
	}

	err := publisher.publishSourceDiscovery(ctx, source, settings)
	require.NoError(t, err, "Failed to publish source discovery")

	// Sound level topic should NOT be present
	// Note: When DisplayName is set, it's used for entity IDs instead of source.ID
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.DisplayName) // Use DisplayName since it's set
	soundLevelTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_sound_level/config"

	assert.NotContains(t, mock.publishedMessages, soundLevelTopic, "Sound level topic should not be published when disabled")

	// Other sensors should still be present
	speciesTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_species/config"
	assert.Contains(t, mock.publishedMessages, speciesTopic, "Species topic should be published")
}

// TestRemoveDiscovery verifies that discovery entries can be removed.
func TestRemoveDiscovery(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	sources := []datastore.AudioSource{
		{ID: "source1", DisplayName: "Source 1"},
		{ID: "source2", DisplayName: "Source 2"},
	}

	err := publisher.RemoveDiscovery(ctx, sources)
	require.NoError(t, err, "Failed to remove discovery")

	// Verify bridge removal (empty payload)
	bridgeTopic := "homeassistant/binary_sensor/test-node/status/config"
	assert.Contains(t, mock.publishedMessages, bridgeTopic)
	assert.Empty(t, mock.publishedMessages[bridgeTopic], "Bridge removal should publish empty payload")

	// Verify sensor removal for each source
	// Note: When DisplayName is set, it's used for entity IDs instead of source.ID
	nodeID := SanitizeID(config.NodeID)
	for _, source := range sources {
		sourceID := SanitizeID(source.DisplayName) // Use DisplayName since it's set
		for _, sensorType := range AllSensorTypes {
			objectID := nodeID + "_" + sourceID + "_" + sensorType
			topic := "homeassistant/sensor/" + nodeID + "/" + objectID + "/config"
			assert.Contains(t, mock.publishedMessages, topic, "Expected removal topic not found: %s", topic)
			assert.Empty(t, mock.publishedMessages[topic], "Removal should publish empty payload")
		}
	}
}

// TestDiscoveryConfigDefaults verifies default configuration values.
func TestDiscoveryConfigDefaults(t *testing.T) {
	t.Parallel()

	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "my-node",
		Version:         "1.2.3",
	}

	assert.Equal(t, "homeassistant", config.DiscoveryPrefix)
	assert.Equal(t, "birdnet", config.BaseTopic)
	assert.Equal(t, "BirdNET-Go", config.DeviceName)
	assert.Equal(t, "my-node", config.NodeID)
	assert.Equal(t, "1.2.3", config.Version)
}

// TestPublishDiscoveryErrorHandling verifies error handling during discovery publishing.
func TestPublishDiscoveryErrorHandling(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	mock.publishError = errors.New("mock publish error")

	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "error-test",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	sources := []datastore.AudioSource{
		{ID: "source1", DisplayName: "Source 1"},
	}

	settings := &conf.Settings{}

	// PublishDiscovery should return an error when bridge fails to publish
	err := publisher.PublishDiscovery(ctx, sources, settings)
	require.Error(t, err, "Expected error when publishing fails")
	assert.Contains(t, err.Error(), "mock publish error", "Error should contain original error message")
}

// TestPublishDiscoveryMultipleSources verifies discovery for multiple audio sources.
func TestPublishDiscoveryMultipleSources(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "multi-source",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	sources := []datastore.AudioSource{
		{ID: "usb-mic", DisplayName: "USB Microphone"},
		{ID: "built-in", DisplayName: "Built-in Audio"},
		{ID: "network", DisplayName: "Network Stream"},
	}

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				SoundLevel: conf.SoundLevelSettings{
					Enabled: false,
				},
			},
		},
	}

	err := publisher.PublishDiscovery(ctx, sources, settings)
	require.NoError(t, err, "Failed to publish discovery")

	// Should have bridge + 3 sensors per source (species, confidence, scientific_name)
	// Bridge: 1 topic
	// 3 sources * 3 sensors = 9 topics
	// Total: 10 topics
	assert.Len(t, mock.publishedMessages, 10, "Expected 10 discovery messages")

	// Verify each source has its sensors
	// Note: When DisplayName is set, it's used for entity IDs instead of source.ID
	nodeID := SanitizeID(config.NodeID)
	for _, source := range sources {
		sourceID := SanitizeID(source.DisplayName) // Use DisplayName since it's set
		speciesTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_species/config"
		assert.Contains(t, mock.publishedMessages, speciesTopic, "Species topic not found for source %s", source.DisplayName)
	}
}

// =============================================================================
// SOUND LEVEL VALUE TEMPLATE TESTS
// =============================================================================
//
// These tests verify that the sound level value template uses the correct
// octave band key format. The keys must match what formatBandKey() produces
// in internal/myaudio/soundlevel.go:
//   - Frequencies < 1000 Hz: "%.1f_Hz" (e.g., "500.0_Hz")
//   - Frequencies >= 1000 Hz: "%.1f_kHz" (e.g., "1.0_kHz" for 1000 Hz)
//
// The 1000 Hz band is commonly used for sound level monitoring, and its key
// is "1.0_kHz", NOT "1000".
// =============================================================================

// TestSoundLevelValueTemplate_UsesCorrectBandKeyFormat verifies that the sound
// level discovery message uses the correct octave band key format in its value
// template. The 1000 Hz band key is "1.0_kHz" (as produced by formatBandKey).
//
// This test catches the bug where the template used '1000' instead of '1.0_kHz',
// causing Home Assistant to never find the sound level data.
func TestSoundLevelValueTemplate_UsesCorrectBandKeyFormat(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	source := datastore.AudioSource{
		ID:          "test-mic",
		DisplayName: "Test Microphone",
	}

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				SoundLevel: conf.SoundLevelSettings{
					Enabled: true,
				},
			},
		},
	}

	err := publisher.publishSourceDiscovery(ctx, source, settings)
	require.NoError(t, err, "Failed to publish source discovery")

	// Get the sound level discovery payload
	// Note: When DisplayName is set, it's used for entity IDs instead of source.ID
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.DisplayName) // Use DisplayName since it's set
	soundLevelTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_sound_level/config"

	require.Contains(t, mock.publishedMessages, soundLevelTopic, "Sound level topic not found")

	var payload DiscoveryPayload
	err = json.Unmarshal([]byte(mock.publishedMessages[soundLevelTopic]), &payload)
	require.NoError(t, err, "Failed to parse sound level discovery payload")

	// ==========================================================================
	// CRITICAL ASSERTION: The value template MUST use '1.0_kHz' NOT '1000'
	// ==========================================================================
	// The octave band keys are formatted by formatBandKey() in soundlevel.go:
	// - 1000 Hz becomes "1.0_kHz" (since 1000 >= 1000, it uses kHz format)
	// - The template must match this format to extract data correctly
	//
	// WRONG: {{ value_json.b['1000'].m ... }}
	// RIGHT: {{ value_json.b['1.0_kHz'].m ... }}
	// ==========================================================================

	assert.Contains(t, payload.ValueTemplate, "1.0_kHz",
		"SOUND LEVEL BUG: Value template must use '1.0_kHz' band key, not '1000'. "+
			"The formatBandKey() function formats 1000 Hz as '1.0_kHz'. "+
			"Got template: %s", payload.ValueTemplate)

	assert.NotContains(t, payload.ValueTemplate, "['1000']",
		"SOUND LEVEL BUG: Value template uses incorrect band key '1000'. "+
			"Should be '1.0_kHz' to match formatBandKey() output. "+
			"Got template: %s", payload.ValueTemplate)

	// Also verify the template structure is correct overall
	assert.Contains(t, payload.ValueTemplate, "value_json.b",
		"Value template should access bands via value_json.b")
	assert.Contains(t, payload.ValueTemplate, ".m",
		"Value template should access mean dB via .m")
	assert.Contains(t, payload.ValueTemplate, "value_json.src",
		"Value template should filter by source ID via value_json.src")
}

// =============================================================================
// DEVICE NAMING TESTS
// =============================================================================
//
// These tests verify that device names remain reasonably short even when
// source IDs are long (e.g., RTSP streams with UUID-based IDs or full URLs).
//
// Issue: Users reported that RTSP stream names become "unimaginably long"
// when the full source ID is used as the display name.
//
// Solution: When DisplayName is empty, use a truncated/friendly version
// of the source ID to keep sensor names manageable in Home Assistant.
// =============================================================================

// TestDeviceNaming_ShortDisplayNameWhenIDIsLong verifies that device names
// remain short even when source IDs are long (e.g., RTSP streams).
//
// When DisplayName is empty and source.ID is long, the discovery should:
// 1. Use a truncated/friendly version of the ID
// 2. Keep the total display name portion under maxDisplayNameLength
func TestDeviceNaming_ShortDisplayNameWhenIDIsLong(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	// Simulate an RTSP source with a long ID and NO DisplayName
	// This is the problematic case - without DisplayName, the full ID gets used
	longSourceID := "rtsp_a1b2c3d4-e5f6-7890-abcd-ef1234567890_stream_high_quality"
	source := datastore.AudioSource{
		ID:          longSourceID,
		DisplayName: "", // Empty - this is the problem case
	}

	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			Audio: conf.AudioSettings{
				SoundLevel: conf.SoundLevelSettings{
					Enabled: false,
				},
			},
		},
	}

	err := publisher.publishSourceDiscovery(ctx, source, settings)
	require.NoError(t, err, "Failed to publish source discovery")

	// Get the species sensor discovery payload to check device name
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.ID)
	speciesTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_species/config"

	require.Contains(t, mock.publishedMessages, speciesTopic, "Species topic not found")

	var payload DiscoveryPayload
	err = json.Unmarshal([]byte(mock.publishedMessages[speciesTopic]), &payload)
	require.NoError(t, err, "Failed to parse species discovery payload")

	// ==========================================================================
	// CRITICAL: Device name should be reasonably short
	// ==========================================================================
	// The device name format is: "{DeviceName} {DisplayName}"
	// When DisplayName is empty, we fall back to source.ID
	// But if source.ID is very long, we should truncate/shorten it
	//
	// Bad: "BirdNET-Go rtsp_a1b2c3d4-e5f6-7890-abcd-ef1234567890_stream_high_quality"
	// Good: "BirdNET-Go RTSP Stream 1" or "BirdNET-Go rtsp_a1b2c3d4"
	// ==========================================================================

	// Extract the display name portion (everything after "BirdNET-Go ")
	deviceName := payload.Device.Name
	prefix := config.DeviceName + " "
	require.Greater(t, len(deviceName), len(prefix), "Device name should have a display name portion")

	displayNamePortion := deviceName[len(prefix):]

	assert.LessOrEqual(t, len(displayNamePortion), maxDisplayNameLength,
		"DEVICE NAMING BUG: Display name portion is too long (%d chars). "+
			"When DisplayName is empty, should use a shortened version of source.ID. "+
			"Got device name: %q, display portion: %q",
		len(displayNamePortion), deviceName, displayNamePortion)

	// Also verify the full device name doesn't exceed a reasonable total length
	maxTotalDeviceNameLength := len(config.DeviceName) + 1 + maxDisplayNameLength
	assert.LessOrEqual(t, len(deviceName), maxTotalDeviceNameLength,
		"DEVICE NAMING BUG: Total device name is too long (%d chars). "+
			"Maximum should be %d. Got: %q",
		len(deviceName), maxTotalDeviceNameLength, deviceName)
}

// TestDeviceNaming_PreservesShortDisplayName verifies that short DisplayNames
// are preserved as-is without modification.
func TestDeviceNaming_PreservesShortDisplayName(t *testing.T) {
	t.Parallel()

	mock := newMockPublisher()
	config := DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "1.0.0",
	}

	publisher := NewDiscoveryPublisher(mock, &config)
	ctx := t.Context()

	// Source with a nice short DisplayName - should be used as-is
	source := datastore.AudioSource{
		ID:          "rtsp_a1b2c3d4-e5f6-7890-abcd-ef1234567890",
		DisplayName: "Backyard Camera", // Nice short name
	}

	settings := &conf.Settings{}

	err := publisher.publishSourceDiscovery(ctx, source, settings)
	require.NoError(t, err, "Failed to publish source discovery")

	// Note: When DisplayName is set, it's used for entity IDs instead of source.ID
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.DisplayName) // Use DisplayName since it's set
	speciesTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_species/config"

	var payload DiscoveryPayload
	err = json.Unmarshal([]byte(mock.publishedMessages[speciesTopic]), &payload)
	require.NoError(t, err, "Failed to parse discovery payload")

	// DisplayName should be preserved exactly in the device name
	assert.Equal(t, "BirdNET-Go Backyard Camera", payload.Device.Name,
		"Device name should use the provided DisplayName exactly")
}

// TestShortenDisplayName tests the shortenDisplayName helper function directly
func TestShortenDisplayName(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "Short name passes through unchanged",
			input:    "Backyard Camera",
			expected: "Backyard Camera",
		},
		{
			name:     "Exactly 32 chars passes through",
			input:    "12345678901234567890123456789012", // 32 chars
			expected: "12345678901234567890123456789012",
		},
		{
			name:     "RTSP UUID truncated to 13 chars",
			input:    "rtsp_a1b2c3d4-e5f6-7890-abcd-ef1234567890",
			expected: "rtsp_a1b2c3d4",
		},
		{
			name:     "Long string with underscores truncates at boundary",
			input:    "camera_stream_front_yard_high_quality_feed",
			expected: "camera_stream_front_yard_high",
		},
		{
			name:     "Long string with hyphens truncates at boundary",
			input:    "front-yard-camera-stream-high-quality-feed",
			expected: "front-yard-camera-stream-high",
		},
		{
			name:     "Long string without boundaries just truncates",
			input:    "abcdefghijklmnopqrstuvwxyz1234567890abcd",
			expected: "abcdefghijklmnopqrstuvwxyz123456",
		},
		{
			name:     "Empty string returns empty",
			input:    "",
			expected: "",
		},
		{
			name:     "UTF-8 multi-byte characters handled safely",
			input:    "日本語カメラ_ストリーム_フロントヤードの庭_高画質ストリーミング配信", // 34 runes
			expected: "日本語カメラ_ストリーム_フロントヤードの庭",                         // truncated at _ (22 runes)
		},
		{
			name:     "Mixed ASCII and UTF-8 truncates correctly",
			input:    "Café_Microphone_Stream_日本語_テスト_追加テキスト文字列", // 41 runes
			expected: "Café_Microphone_Stream_日本語_テスト",                    // truncated at _ (30 runes)
		},
		{
			name:     "Short UTF-8 string passes through unchanged",
			input:    "Mikrofon_Gärten",
			expected: "Mikrofon_Gärten",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := shortenDisplayName(tt.input)
			assert.Equal(t, tt.expected, result)
			// Check rune length (not byte length) for UTF-8 safety
			assert.LessOrEqual(t, len([]rune(result)), maxDisplayNameLength,
				"Result rune count should never exceed maxDisplayNameLength")
		})
	}
}
