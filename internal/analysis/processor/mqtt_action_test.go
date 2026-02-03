// mqtt_action_test.go - Tests for MqttAction.Execute()
//
// These tests verify that MqttAction correctly:
// - Reads detection ID from DetectionContext
// - Generates correct JSON payload with all fields
// - Includes sourceId for Home Assistant filtering
package processor

import (
	"context"
	"encoding/json"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/detection"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// MockMQTTClient captures published messages for testing.
type MockMQTTClient struct {
	mu               sync.Mutex
	connected        bool
	publishedTopic   string
	publishedPayload string
	publishErr       error
	connectErr       error
	publishCalls     int
}

// NewMockMQTTClient creates a new mock MQTT client.
func NewMockMQTTClient() *MockMQTTClient {
	return &MockMQTTClient{connected: true}
}

func (m *MockMQTTClient) Connect(_ context.Context) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.connectErr != nil {
		return m.connectErr
	}
	m.connected = true
	return nil
}

func (m *MockMQTTClient) Publish(_ context.Context, topic, payload string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishCalls++
	if m.publishErr != nil {
		return m.publishErr
	}
	m.publishedTopic = topic
	m.publishedPayload = payload
	return nil
}

func (m *MockMQTTClient) PublishWithRetain(ctx context.Context, topic, payload string, _ bool) error {
	return m.Publish(ctx, topic, payload)
}

func (m *MockMQTTClient) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *MockMQTTClient) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = false
}

func (m *MockMQTTClient) TestConnection(_ context.Context, _ chan<- mqtt.TestResult) {}
func (m *MockMQTTClient) SetControlChannel(_ chan string)                            {}
func (m *MockMQTTClient) RegisterOnConnectHandler(_ mqtt.OnConnectHandler)           {}

// GetPublishedPayload returns the last published payload.
func (m *MockMQTTClient) GetPublishedPayload() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishedPayload
}

// GetPublishedTopic returns the last published topic.
func (m *MockMQTTClient) GetPublishedTopic() string {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishedTopic
}

// SetConnected sets the connection state.
func (m *MockMQTTClient) SetConnected(connected bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.connected = connected
}

// SetPublishError sets an error to be returned on Publish.
func (m *MockMQTTClient) SetPublishError(err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishErr = err
}

// GetPublishCalls returns the number of Publish calls.
func (m *MockMQTTClient) GetPublishCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.publishCalls
}

// Compile-time check that MockMQTTClient implements mqtt.Client
var _ mqtt.Client = (*MockMQTTClient)(nil)

// testMQTTTopic is the default topic used in MQTT tests.
const testMQTTTopic = "birdnet/detections"

// TestMqttAction_Execute_UsesDetectionContextID verifies that MqttAction
// reads the detection ID from DetectionContext and includes it in the payload.
func TestMqttAction_Execute_UsesDetectionContextID(t *testing.T) {
	t.Parallel()

	// Setup
	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create detection
	det := testDetection()

	// Create DetectionContext with pre-set ID (simulating DatabaseAction having run)
	detectionCtx := &DetectionContext{}
	expectedID := uint64(42)
	detectionCtx.NoteID.Store(expectedID)

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	// Execute
	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "MqttAction.Execute() should not return error")

	// Parse the published payload
	payload := mockClient.GetPublishedPayload()
	require.NotEmpty(t, payload, "Should have published a payload")

	var jsonMap map[string]any
	err = json.Unmarshal([]byte(payload), &jsonMap)
	require.NoError(t, err, "Payload should be valid JSON")

	// Verify detectionId is present and correct
	detectionID, ok := jsonMap["detectionId"].(float64) // JSON numbers are float64
	require.True(t, ok, "detectionId field should be present")
	assert.InDelta(t, float64(expectedID), detectionID, 0.001,
		"detectionId should match DetectionContext.NoteID")
}

// TestMqttAction_Execute_PayloadContainsAllFields verifies that the MQTT
// payload contains all expected fields from the Result.
func TestMqttAction_Execute_PayloadContainsAllFields(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create detection with all fields populated
	now := time.Now()
	det := Detections{
		Result: detection.Result{
			Timestamp:  now,
			SourceNode: "test-node",
			AudioSource: detection.AudioSource{
				ID:          "rtsp://camera1",
				SafeString:  "camera1",
				DisplayName: "Camera 1",
			},
			BeginTime: now,
			EndTime:   now.Add(15 * time.Second),
			Species: detection.Species{
				ScientificName: "Turdus migratorius",
				CommonName:     "American Robin",
				Code:           "amerob",
			},
			Confidence:     0.95,
			Latitude:       42.0,
			Longitude:      -71.0,
			ClipName:       "test_clip.wav",
			ProcessingTime: 100 * time.Millisecond,
			Occurrence:     0.85,
			Model:          detection.DefaultModelInfo(),
		},
	}

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(123)

	action := &MqttAction{
		Settings:       settings,
		Result:         det.Result,
		MqttClient:     mockClient,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		BirdImageCache: nil, // Not needed for this test
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	// Parse payload
	var jsonMap map[string]any
	err = json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap)
	require.NoError(t, err)

	// Verify critical fields are present (matches contract test expectations)
	assert.Equal(t, "American Robin", jsonMap["CommonName"])
	assert.Equal(t, "Turdus migratorius", jsonMap["ScientificName"])
	confidence, ok := jsonMap["Confidence"].(float64)
	require.True(t, ok, "Confidence should be float64")
	assert.InDelta(t, 0.95, confidence, 0.001)
	assert.Equal(t, "test_clip.wav", jsonMap["ClipName"])
	detectionID, ok := jsonMap["detectionId"].(float64)
	require.True(t, ok, "detectionId should be float64")
	assert.InDelta(t, float64(123), detectionID, 0.001)
	assert.Equal(t, "rtsp://camera1", jsonMap["sourceId"])

	// Verify Date and Time format
	assert.Regexp(t, `^\d{4}-\d{2}-\d{2}$`, jsonMap["Date"], "Date should be YYYY-MM-DD")
	assert.Regexp(t, `^\d{2}:\d{2}:\d{2}$`, jsonMap["Time"], "Time should be HH:MM:SS")
}

// TestMqttAction_Execute_SourceID verifies that the sourceId field is included
// for Home Assistant device filtering.
func TestMqttAction_Execute_SourceID(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	// Set specific source ID
	det.Result.AudioSource.ID = "microphone-backyard"
	det.Result.AudioSource.DisplayName = "Backyard Microphone"

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	var jsonMap map[string]any
	err = json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap)
	require.NoError(t, err)

	assert.Equal(t, "microphone-backyard", jsonMap["sourceId"],
		"sourceId should match AudioSource.ID for HA filtering")
}

// TestMqttAction_Execute_NotConnected verifies that MqttAction returns error
// when not connected.
func TestMqttAction_Execute_NotConnected(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	mockClient.SetConnected(false)

	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	err := action.Execute(t.Context(), nil)
	require.Error(t, err, "Should return error when not connected")
	assert.Contains(t, err.Error(), "MQTT client not connected", "Error should indicate connection issue")
}

// TestMqttAction_Execute_PublishesToConfiguredTopic verifies that the message
// is published to the configured topic.
func TestMqttAction_Execute_PublishesToConfiguredTopic(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = "homeassistant/sensor/birdnet/state"

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	assert.Equal(t, "homeassistant/sensor/birdnet/state", mockClient.GetPublishedTopic(),
		"Should publish to configured topic")
}

// TestMqttAction_Execute_WithoutDetectionContext verifies that MqttAction works
// even without DetectionContext (detectionId will be 0).
func TestMqttAction_Execute_WithoutDetectionContext(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
		DetectionCtx: nil, // No DetectionContext
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "Should succeed without DetectionContext")

	var jsonMap map[string]any
	err = json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap)
	require.NoError(t, err)

	// detectionId should be 0 when no DetectionContext
	detectionID, ok := jsonMap["detectionId"].(float64)
	require.True(t, ok, "detectionId should be float64")
	assert.InDelta(t, float64(0), detectionID, 0.001,
		"detectionId should be 0 without DetectionContext")
}

// TestMqttAction_Execute_EmptyTopic verifies that MqttAction returns error
// when topic is not configured.
func TestMqttAction_Execute_EmptyTopic(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Topic = "" // Empty topic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	err := action.Execute(t.Context(), nil)
	require.Error(t, err, "Should return error for empty topic")
	assert.Contains(t, err.Error(), "MQTT topic is not specified", "Error should mention topic")
}
