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
	ctx := context.Background()

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
	assert.Equal(t, "online", payload.PayloadAvailable)
	assert.Equal(t, "offline", payload.PayloadNotAvailable)

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
	ctx := context.Background()

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
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.ID)
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
	ctx := context.Background()

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
	nodeID := SanitizeID(config.NodeID)
	sourceID := SanitizeID(source.ID)
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
	ctx := context.Background()

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
	nodeID := SanitizeID(config.NodeID)
	for _, source := range sources {
		sourceID := SanitizeID(source.ID)
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
	ctx := context.Background()

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
	ctx := context.Background()

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
	nodeID := SanitizeID(config.NodeID)
	for _, source := range sources {
		sourceID := SanitizeID(source.ID)
		speciesTopic := "homeassistant/sensor/" + nodeID + "/" + nodeID + "_" + sourceID + "_species/config"
		assert.Contains(t, mock.publishedMessages, speciesTopic, "Species topic not found for source %s", source.ID)
	}
}
