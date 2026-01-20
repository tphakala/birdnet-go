package processor

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/jobqueue"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// MockMqttClientWithCapture captures published messages for testing
type MockMqttClientWithCapture struct {
	Connected      bool
	PublishedTopic string
	PublishedData  string
	PublishError   error
}

func (m *MockMqttClientWithCapture) Connect(_ context.Context) error {
	m.Connected = true
	return nil
}

func (m *MockMqttClientWithCapture) Disconnect() {
	m.Connected = false
}

func (m *MockMqttClientWithCapture) IsConnected() bool {
	return m.Connected
}

func (m *MockMqttClientWithCapture) Publish(_ context.Context, topic, data string) error {
	m.PublishedTopic = topic
	m.PublishedData = data
	return m.PublishError
}

func (m *MockMqttClientWithCapture) SetControlChannel(_ chan string) {
	// Not needed for test
}

func (m *MockMqttClientWithCapture) TestConnection(_ context.Context, _ chan<- mqtt.TestResult) {
	// Not needed for test
}

func (m *MockMqttClientWithCapture) PublishWithRetain(_ context.Context, topic, data string, _ bool) error {
	m.PublishedTopic = topic
	m.PublishedData = data
	return m.PublishError
}

func (m *MockMqttClientWithCapture) RegisterOnConnectHandler(_ mqtt.OnConnectHandler) {
	// Not needed for test
}

func TestMqttAction_IncludesOccurrence(t *testing.T) {
	now := time.Date(2024, 1, 15, 12, 0, 0, 0, time.UTC)

	// Create test Result (single source of truth)
	testResult := detection.Result{
		Timestamp: now,
		Species: detection.Species{
			CommonName:     "American Robin",
			ScientificName: "Turdus migratorius",
		},
		Confidence: 0.95,
		ClipName:   "test_clip.wav",
		Occurrence: 0.75, // This should appear in MQTT message
		AudioSource: detection.AudioSource{
			ID:          "test-source",
			SafeString:  "test-source",
			DisplayName: "Test Source",
		},
	}

	// Legacy Note kept for backward compatibility during transition
	testNote := datastore.Note{
		CommonName:     "American Robin",
		ScientificName: "Turdus migratorius",
		Confidence:     0.95,
		ClipName:       "test_clip.wav",
		Date:           "2024-01-15",
		Time:           "12:00:00",
		Occurrence:     0.75,
		Source:         testAudioSource(),
	}

	// Create mock MQTT client to capture published data
	mockClient := &MockMqttClientWithCapture{
		Connected: true,
	}

	// Create event tracker that allows everything
	eventTracker := NewEventTracker(60 * time.Second)

	// Create test settings
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Topic:   "birdnet/detections",
			},
		},
		Debug: false,
	}

	// Create retry config (disabled for test)
	retryConfig := jobqueue.RetryConfig{
		Enabled:      false,
		MaxRetries:   0,
		InitialDelay: 0,
		MaxDelay:     0,
		Multiplier:   0,
	}

	// Create MQTT action
	action := &MqttAction{
		Settings:       settings,
		Result:         testResult, // Domain model (single source of truth)
		Note:           testNote,   // Deprecated: kept temporarily
		BirdImageCache: nil,        // No image cache for this test
		MqttClient:     mockClient,
		EventTracker:   eventTracker,
		RetryConfig:    retryConfig,
	}

	// Execute the action
	err := action.Execute(nil)
	require.NoError(t, err)

	// Verify the message was published
	assert.Equal(t, "birdnet/detections", mockClient.PublishedTopic)
	assert.NotEmpty(t, mockClient.PublishedData)

	// Debug: print the actual published data
	t.Logf("Published MQTT data: %s", mockClient.PublishedData)

	// Parse the published JSON to verify occurrence is included
	// The structure is NoteWithBirdImage which embeds Note directly
	var publishedData map[string]any
	err = json.Unmarshal([]byte(mockClient.PublishedData), &publishedData)
	require.NoError(t, err, "Failed to parse published JSON")

	// Check for occurrence field directly in the root level
	occurrence, hasOccurrence := publishedData["occurrence"].(float64)
	assert.True(t, hasOccurrence, "Occurrence field should be present in MQTT message")
	assert.InDelta(t, 0.75, occurrence, 0.001, "Occurrence value should match")

	// Verify other fields (note the capitalized field names from Go structs)
	assert.Equal(t, "American Robin", publishedData["CommonName"])
	assert.Equal(t, "Turdus migratorius", publishedData["ScientificName"])
	confidence, _ := publishedData["Confidence"].(float64)
	assert.InDelta(t, 0.95, confidence, 0.001)
}

func TestMqttAction_OmitsOccurrenceWhenZero(t *testing.T) {
	now := time.Date(2024, 1, 15, 14, 0, 0, 0, time.UTC)

	// Create test Result (single source of truth)
	testResult := detection.Result{
		Timestamp: now,
		Species: detection.Species{
			CommonName:     "House Sparrow",
			ScientificName: "Passer domesticus",
		},
		Confidence: 0.85,
		ClipName:   "test_clip2.wav",
		Occurrence: 0.0, // Zero occurrence should be omitted
		AudioSource: detection.AudioSource{
			ID:          "test-source",
			SafeString:  "test-source",
			DisplayName: "Test Source",
		},
	}

	// Legacy Note kept for backward compatibility during transition
	testNote := datastore.Note{
		CommonName:     "House Sparrow",
		ScientificName: "Passer domesticus",
		Confidence:     0.85,
		ClipName:       "test_clip2.wav",
		Date:           "2024-01-15",
		Time:           "14:00:00",
		Occurrence:     0.0,
		Source:         testAudioSource(),
	}

	// Create mock MQTT client to capture published data
	mockClient := &MockMqttClientWithCapture{
		Connected: true,
	}

	// Create event tracker that allows everything
	eventTracker := NewEventTracker(60 * time.Second)

	// Create test settings
	settings := &conf.Settings{
		Realtime: conf.RealtimeSettings{
			MQTT: conf.MQTTSettings{
				Enabled: true,
				Topic:   "birdnet/detections",
			},
		},
		Debug: false,
	}

	// Create retry config (disabled for test)
	retryConfig := jobqueue.RetryConfig{
		Enabled:      false,
		MaxRetries:   0,
		InitialDelay: 0,
		MaxDelay:     0,
		Multiplier:   0,
	}

	// Create MQTT action
	action := &MqttAction{
		Settings:       settings,
		Result:         testResult, // Domain model (single source of truth)
		Note:           testNote,   // Deprecated: kept temporarily
		BirdImageCache: nil,
		MqttClient:     mockClient,
		EventTracker:   eventTracker,
		RetryConfig:    retryConfig,
	}

	// Execute the action
	err := action.Execute(nil)
	require.NoError(t, err)

	// Verify the message was published
	assert.Equal(t, "birdnet/detections", mockClient.PublishedTopic)
	assert.NotEmpty(t, mockClient.PublishedData)

	// Parse the published JSON to verify occurrence is omitted when zero
	var publishedData map[string]any
	err = json.Unmarshal([]byte(mockClient.PublishedData), &publishedData)
	require.NoError(t, err, "Failed to parse published JSON")

	// Check that occurrence field is not present (omitempty should exclude it when zero)
	_, hasOccurrence := publishedData["occurrence"]
	assert.False(t, hasOccurrence, "Occurrence field should be omitted when value is zero")

	// Verify other fields are still present (note the capitalized field names)
	assert.Equal(t, "House Sparrow", publishedData["CommonName"])
	assert.Equal(t, "Passer domesticus", publishedData["ScientificName"])
	confidence, _ := publishedData["Confidence"].(float64)
	assert.InDelta(t, 0.85, confidence, 0.001)
}
