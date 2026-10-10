// mqtt_action_test.go - Tests for MqttAction.Execute()
//
// These tests verify that MqttAction correctly:
// - Reads detection ID from DetectionContext
// - Generates correct JSON payload with all fields
// - Includes sourceId (the audio source ID) in the payload
package processor

import (
	"context"
	"encoding/json"
	"fmt"
	"slices"
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
	reconnectLoops   int
	disconnectCalls  int
	// messages records every successful publish in order.
	messages []publishedMessage
	// topicErrs fails publishes to specific topics only.
	topicErrs map[string]error
	// onConnectHandler stores the last handler registered via RegisterOnConnectHandler.
	onConnectHandler mqtt.OnConnectHandler
}

// publishedMessage is one publish captured by MockMQTTClient.
type publishedMessage struct {
	topic   string
	payload string
	retain  bool // true only for PublishWithRetain(..., true)
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

func (m *MockMQTTClient) Publish(ctx context.Context, topic, payload string) error {
	return m.publish(ctx, topic, payload, false)
}

func (m *MockMQTTClient) publish(ctx context.Context, topic, payload string, retain bool) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.publishCalls++
	if err := ctx.Err(); err != nil {
		return err
	}
	if m.publishErr != nil {
		return m.publishErr
	}
	if err := m.topicErrs[topic]; err != nil {
		return err
	}
	m.publishedTopic = topic
	m.publishedPayload = payload
	m.messages = append(m.messages, publishedMessage{topic: topic, payload: payload, retain: retain})
	return nil
}

func (m *MockMQTTClient) PublishWithRetain(ctx context.Context, topic, payload string, retain bool) error {
	return m.publish(ctx, topic, payload, retain)
}

func (m *MockMQTTClient) IsConnected() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.connected
}

func (m *MockMQTTClient) Disconnect() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.disconnectCalls++
	m.connected = false
}

func (m *MockMQTTClient) StartReconnectLoop() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.reconnectLoops++
}

func (m *MockMQTTClient) TestConnection(_ context.Context, _ chan<- mqtt.TestResult) {}
func (m *MockMQTTClient) SetControlChannel(_ chan string)                            {}

// RegisterOnConnectHandler stores the handler so tests can invoke it directly.
func (m *MockMQTTClient) RegisterOnConnectHandler(h mqtt.OnConnectHandler) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.onConnectHandler = h
}

// OnConnectHandler returns the last handler registered, or nil.
func (m *MockMQTTClient) OnConnectHandler() mqtt.OnConnectHandler {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.onConnectHandler
}

// ReconnectLoopStarts returns how many times StartReconnectLoop was called.
func (m *MockMQTTClient) ReconnectLoopStarts() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.reconnectLoops
}

// DisconnectCalls returns how many times Disconnect was called.
func (m *MockMQTTClient) DisconnectCalls() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.disconnectCalls
}

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

// SetTopicError makes publishes to topic fail with err; other topics succeed.
func (m *MockMQTTClient) SetTopicError(topic string, err error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.topicErrs == nil {
		m.topicErrs = make(map[string]error)
	}
	m.topicErrs[topic] = err
}

// GetPublishedMessages returns a copy of every successful publish, in order.
func (m *MockMQTTClient) GetPublishedMessages() []publishedMessage {
	m.mu.Lock()
	defer m.mu.Unlock()
	return slices.Clone(m.messages)
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
	settings.Realtime.MQTT.Enabled = true
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
	settings.Realtime.MQTT.Enabled = true
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
	detectionCtx.ClipSaved.Store(true) // Simulate successful audio export

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

// TestMqttAction_Execute_SourceID verifies that the sourceId field carries the
// audio source ID.
func TestMqttAction_Execute_SourceID(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Enabled = true
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
		"sourceId should match AudioSource.ID; HA discovery keys the per-source topic on it")
}

// TestMqttAction_Execute_TransientError_NonFatal verifies that transient connection
// errors (EOF, not connected) are absorbed by MqttAction and do NOT fail the action.
// This is the key behavioral change for GitHub #2397: the detection is safe in the
// database, so a missed MQTT notification is not data loss.
func TestMqttAction_Execute_TransientError_NonFatal(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	mockClient.SetPublishError(fmt.Errorf("not connected to MQTT broker"))

	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Enabled = true
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

	// Transient connection errors should return nil (non-fatal)
	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "Transient connection error should be non-fatal (detection is safe in DB)")
}

// TestMqttAction_Execute_EOFError_NonFatal verifies that EOF errors during publish
// are treated as transient and do not fail the action (GitHub #2397).
func TestMqttAction_Execute_EOFError_NonFatal(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	mockClient.SetPublishError(fmt.Errorf("connection lost: EOF"))

	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)
	det := testDetection()

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "EOF error should be non-fatal (GitHub #2397)")
}

// TestMqttAction_Execute_PublishesToConfiguredTopic verifies that the message
// is published to the configured topic.
func TestMqttAction_Execute_PublishesToConfiguredTopic(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{
		Debug: true,
	}
	settings.Realtime.MQTT.Enabled = true
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
	settings.Realtime.MQTT.Enabled = true
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
	settings.Realtime.MQTT.Enabled = true
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

// TestMqttAction_Execute_DisabledAfterCreation verifies that MqttAction exits
// silently when MQTT is disabled via settings after the action was created.
// This supports hot-reload: disabling MQTT in the UI takes effect immediately.
func TestMqttAction_Execute_DisabledAfterCreation(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)
	det := testDetection()

	action := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockClient,
		EventTracker: eventTracker,
	}

	// Simulate runtime hot-reload toggle after action creation.
	settings.Realtime.MQTT.Enabled = false

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "Should silently return nil when MQTT is disabled")
	assert.Equal(t, 0, mockClient.GetPublishCalls(), "Should not attempt publish when disabled")
}

// TestMqttAction_Execute_ClearsClipNameWhenExportFailed verifies that MqttAction
// clears ClipName in the MQTT payload when audio export did not succeed.
// This prevents reporting phantom filenames for clips that don't exist (GitHub #107).
func TestMqttAction_Execute_IncludesClipNameEvenBeforeExport(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	det.Result.ClipName = "2024/01/parus_major_95p_20240115T120000Z.wav"

	// DetectionContext with ClipSaved=false (default) - audio export hasn't run yet.
	// MQTT should still include ClipName because audio export runs independently
	// and consumers should handle missing files gracefully.
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

	assert.Equal(t, "2024/01/parus_major_95p_20240115T120000Z.wav", jsonMap["ClipName"],
		"ClipName should always be included; audio export runs independently")
}

// TestMqttAction_Execute_PreservesClipNameWhenExportSucceeded verifies that MqttAction
// preserves ClipName in the MQTT payload when audio export succeeded.
func TestMqttAction_Execute_PreservesClipNameWhenExportSucceeded(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)

	det := testDetection()
	det.Result.ClipName = "2024/01/parus_major_95p_20240115T120000Z.wav"

	// DetectionContext with ClipSaved=true simulates successful export
	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)
	detectionCtx.ClipSaved.Store(true)

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

	assert.Equal(t, "2024/01/parus_major_95p_20240115T120000Z.wav", jsonMap["ClipName"],
		"ClipName should be preserved when audio export succeeded")
}

// mockSpeciesTimeSource is a controllable speciesDetectionTimeSource for tests.
type mockSpeciesTimeSource struct {
	first    *time.Time
	last     *time.Time
	err      error
	honorCtx bool // when set, block until the query context is done and return ctx.Err() (models a slow query hitting the timeout/cancellation)

	calls       int
	gotName     string
	gotBefore   time.Time
	gotDeadline bool // whether the received context carried the per-query timeout deadline
}

func (m *mockSpeciesTimeSource) GetSpeciesFirstAndLastDetectionTimeBefore(ctx context.Context, scientificName string, before time.Time) (first, last *time.Time, err error) {
	m.calls++
	m.gotName = scientificName
	m.gotBefore = before
	_, m.gotDeadline = ctx.Deadline()
	if m.honorCtx {
		<-ctx.Done()
		return nil, nil, ctx.Err()
	}
	return m.first, m.last, m.err
}

// TestMqttAction_Execute_SpeciesDetectionTimes_Populated verifies that the
// payload carries the species' first-ever and most-recent previous detection
// times when the datastore provides them.
func TestMqttAction_Execute_SpeciesDetectionTimes_Populated(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)
	det := testDetection()

	firstSeen := time.Date(2024, 1, 10, 8, 30, 0, 0, time.UTC)
	lastSeen := time.Date(2024, 1, 14, 18, 45, 0, 0, time.UTC)
	source := &mockSpeciesTimeSource{first: &firstSeen, last: &lastSeen}

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:          settings,
		Result:            det.Result,
		MqttClient:        mockClient,
		EventTracker:      eventTracker,
		DetectionCtx:      detectionCtx,
		SpeciesTimeSource: source,
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	assert.Equal(t, 1, source.calls, "species time query should run exactly once")
	assert.True(t, source.gotDeadline,
		"query must run under the per-detection timeout (deadline-bounded context)")
	assert.Equal(t, det.Result.Species.ScientificName, source.gotName,
		"query must use the detection's scientific name")
	assert.Equal(t, det.Result.Timestamp, source.gotBefore,
		"bound must be the current detection timestamp (strict before)")

	var jsonMap map[string]any
	require.NoError(t, json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap))

	assert.Equal(t, firstSeen.Format(time.RFC3339), jsonMap["speciesFirstDetectedAt"])
	assert.Equal(t, lastSeen.Format(time.RFC3339), jsonMap["speciesLastDetectedAt"])
}

// TestMqttAction_Execute_SpeciesDetectionTimes_QueryError_NonFatal verifies that
// a datastore failure degrades to null fields instead of failing the publish
// (transient errors are non-fatal, GitHub #2397).
func TestMqttAction_Execute_SpeciesDetectionTimes_QueryError_NonFatal(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)
	det := testDetection()

	source := &mockSpeciesTimeSource{err: fmt.Errorf("database unavailable")}

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:          settings,
		Result:            det.Result,
		MqttClient:        mockClient,
		EventTracker:      eventTracker,
		DetectionCtx:      detectionCtx,
		SpeciesTimeSource: source,
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err, "species time query failure must not fail the action")
	assert.Equal(t, 1, mockClient.GetPublishCalls(), "payload must still be published")

	var jsonMap map[string]any
	require.NoError(t, json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap))

	_, present := jsonMap["speciesFirstDetectedAt"]
	assert.True(t, present, "speciesFirstDetectedAt key must still be present")
	assert.Nil(t, jsonMap["speciesFirstDetectedAt"], "speciesFirstDetectedAt must be null on query error")
	assert.Nil(t, jsonMap["speciesLastDetectedAt"], "speciesLastDetectedAt must be null on query error")
}

// TestMqttAction_Execute_SpeciesDetectionTimes_ContextCanceled_Silent verifies
// that a canceled query context (an expected shutdown interruption) degrades to
// null fields without failing the publish, and exercises the context.Canceled
// suppression branch that keeps per-detection logs quiet on stop. The query
// context is derived from the action context via the per-query timeout, so an
// already-canceled action context reaches the query as context.Canceled.
func TestMqttAction_Execute_SpeciesDetectionTimes_ContextCanceled_Silent(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic

	eventTracker := NewEventTracker(testEventTrackerInterval)
	det := testDetection()

	// honorCtx makes the query block until its context is done and return
	// ctx.Err(); with an already-canceled parent that is context.Canceled.
	source := &mockSpeciesTimeSource{honorCtx: true}

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(1)

	action := &MqttAction{
		Settings:          settings,
		Result:            det.Result,
		MqttClient:        mockClient,
		EventTracker:      eventTracker,
		DetectionCtx:      detectionCtx,
		SpeciesTimeSource: source,
	}

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // canceled before Execute: the query context is already done

	err := action.Execute(ctx, nil)
	require.NoError(t, err, "a canceled query context must not fail the action")
	assert.Equal(t, 1, source.calls, "species time query should run exactly once")
	assert.True(t, source.gotDeadline,
		"query must still run under the per-detection timeout deadline")
	assert.Equal(t, 1, mockClient.GetPublishCalls(), "payload must still be published")

	var jsonMap map[string]any
	require.NoError(t, json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap))

	_, present := jsonMap["speciesFirstDetectedAt"]
	assert.True(t, present, "speciesFirstDetectedAt key must still be present")
	assert.Nil(t, jsonMap["speciesFirstDetectedAt"], "speciesFirstDetectedAt must be null on canceled query")
	assert.Nil(t, jsonMap["speciesLastDetectedAt"], "speciesLastDetectedAt must be null on canceled query")
}

// TestMqttAction_Execute_SpeciesDetectionTimes_NilSource verifies that the
// payload carries explicit nulls when no datastore is wired (DB disabled).
func TestMqttAction_Execute_SpeciesDetectionTimes_NilSource(t *testing.T) {
	t.Parallel()

	mockClient := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Enabled = true
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
		// SpeciesTimeSource intentionally nil
	}

	err := action.Execute(t.Context(), nil)
	require.NoError(t, err)

	var jsonMap map[string]any
	require.NoError(t, json.Unmarshal([]byte(mockClient.GetPublishedPayload()), &jsonMap))

	_, present := jsonMap["speciesFirstDetectedAt"]
	assert.True(t, present, "speciesFirstDetectedAt key must always be present (no omitempty)")
	assert.Nil(t, jsonMap["speciesFirstDetectedAt"])
	assert.Nil(t, jsonMap["speciesLastDetectedAt"])
}
