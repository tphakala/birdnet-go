// composite_action_integration_test.go - Full integration tests for CompositeAction
//
// These tests verify the complete data flow from DatabaseAction through
// MQTT and SSE actions, ensuring DetectionContext properly propagates IDs.
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
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/imageprovider"
)

// TestCompositeAction_DatabaseToSSE_IDPropagation verifies that the detection ID
// flows from DatabaseAction to SSEAction via DetectionContext.
func TestCompositeAction_DatabaseToSSE_IDPropagation(t *testing.T) {
	t.Parallel()

	// Setup shared components
	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create shared DetectionContext for ID propagation
	detectionCtx := &DetectionContext{}

	// Create detection
	det := testDetection()

	// Track what SSE receives
	var sseReceivedNoteID uint
	var sseReceivedMu sync.Mutex
	sseBroadcaster := func(note *datastore.Note, _ *imageprovider.BirdImage) error {
		sseReceivedMu.Lock()
		defer sseReceivedMu.Unlock()
		sseReceivedNoteID = note.ID
		return nil
	}

	// Create actions with shared context
	dbAction := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	sseAction := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: sseBroadcaster,
	}

	// Create CompositeAction: Database first, then SSE
	composite := &CompositeAction{
		Actions:     []Action{dbAction, sseAction},
		Description: "Database save then SSE broadcast",
	}

	// Execute
	err := composite.Execute(context.Background(), det)
	require.NoError(t, err, "CompositeAction should succeed")

	// Verify database saved with assigned ID
	savedNote := mockDs.GetLastSavedNote()
	require.NotNil(t, savedNote)
	assert.Equal(t, uint(1), savedNote.ID, "First save should get ID 1")

	// Verify DetectionContext was updated
	assert.Equal(t, uint64(1), detectionCtx.NoteID.Load(),
		"DetectionContext should contain database ID")

	// Verify SSE received the correct ID
	sseReceivedMu.Lock()
	receivedID := sseReceivedNoteID
	sseReceivedMu.Unlock()
	assert.Equal(t, uint(1), receivedID,
		"SSE should receive the same ID from DetectionContext")
}

// TestCompositeAction_DatabaseToMQTT_IDPropagation verifies that the detection ID
// flows from DatabaseAction to MqttAction via DetectionContext.
func TestCompositeAction_DatabaseToMQTT_IDPropagation(t *testing.T) {
	t.Parallel()

	// Setup shared components
	mockDs := NewActionMockDatastore()
	mockMqtt := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Topic = testMQTTTopic
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create shared DetectionContext
	detectionCtx := &DetectionContext{}

	det := testDetection()

	// Create actions with shared context
	dbAction := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	mqttAction := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockMqtt,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	// Create CompositeAction: Database first, then MQTT
	composite := &CompositeAction{
		Actions:     []Action{dbAction, mqttAction},
		Description: "Database save then MQTT publish",
	}

	// Execute
	err := composite.Execute(context.Background(), det)
	require.NoError(t, err)

	// Verify database saved
	savedNote := mockDs.GetLastSavedNote()
	require.NotNil(t, savedNote)
	assert.Equal(t, uint(1), savedNote.ID)

	// Verify MQTT payload contains the database ID
	payload := mockMqtt.GetPublishedPayload()
	require.NotEmpty(t, payload)

	var jsonMap map[string]any
	err = json.Unmarshal([]byte(payload), &jsonMap)
	require.NoError(t, err)

	detectionID, ok := jsonMap["detectionId"].(float64)
	require.True(t, ok, "detectionId should be present in MQTT payload")
	assert.InDelta(t, float64(1), detectionID, 0.001,
		"MQTT payload should contain database ID")
}

// TestCompositeAction_FullPipeline_DatabaseMQTTSSE verifies the complete
// detection pipeline with all three actions.
func TestCompositeAction_FullPipeline_DatabaseMQTTSSE(t *testing.T) {
	t.Parallel()

	// Setup shared components
	mockDs := NewActionMockDatastore()
	mockMqtt := NewMockMQTTClient()
	mockSSE := NewMockSSEBroadcaster()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Topic = testMQTTTopic
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create shared DetectionContext
	detectionCtx := &DetectionContext{}

	// Create detection with specific species
	det := testDetectionWithSpecies("Great Tit", "Parus major", 0.92)

	// Create all actions with shared context
	dbAction := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs,
		Result:       det.Result,
		Results:      det.Results,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	mqttAction := &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   mockMqtt,
		EventTracker: eventTracker,
		DetectionCtx: detectionCtx,
	}

	sseAction := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: mockSSE.BroadcastFunc(),
	}

	// Create CompositeAction: Database -> MQTT -> SSE
	composite := &CompositeAction{
		Actions:     []Action{dbAction, mqttAction, sseAction},
		Description: "Full detection pipeline",
	}

	// Execute
	err := composite.Execute(context.Background(), det)
	require.NoError(t, err)

	// Verify all actions executed
	savedNote := mockDs.GetLastSavedNote()
	require.NotNil(t, savedNote, "Database should have saved note")
	assert.Equal(t, "Great Tit", savedNote.CommonName)

	require.NotEmpty(t, mockMqtt.GetPublishedPayload(), "MQTT should have published")

	sseNote := mockSSE.GetLastNote()
	require.NotNil(t, sseNote, "SSE should have broadcasted")

	// Verify all actions received the same ID
	expectedID := uint(1)
	assert.Equal(t, expectedID, savedNote.ID, "Database note ID")

	var mqttPayload map[string]any
	err = json.Unmarshal([]byte(mockMqtt.GetPublishedPayload()), &mqttPayload)
	require.NoError(t, err)
	detectionID, ok := mqttPayload["detectionId"].(float64)
	require.True(t, ok, "detectionId should be float64")
	assert.InDelta(t, float64(expectedID), detectionID, 0.001,
		"MQTT payload detection ID")

	assert.Equal(t, expectedID, sseNote.ID, "SSE note ID")
}

// TestCompositeAction_Integration_AudioExportFailedFlag verifies that AudioExportFailed
// flag is properly propagated from DatabaseAction to SSEAction in a full pipeline.
func TestCompositeAction_Integration_AudioExportFailedFlag(t *testing.T) {
	t.Parallel()

	// Setup
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	detectionCtx := &DetectionContext{}

	// Track if SSE skipped audio wait
	var sseSkippedWait bool
	sseBroadcaster := func(_ *datastore.Note, _ *imageprovider.BirdImage) error {
		// If we get here quickly, audio wait was skipped
		sseSkippedWait = true
		return nil
	}

	det := testDetection()
	// Set a clip name that would trigger audio wait
	det.Result.ClipName = "/nonexistent/clip.wav"

	// Simulate database action that sets AudioExportFailed
	dbAction := &SimpleAction{
		name: "Mock Database Action",
		onExecute: func() {
			// Simulate database save assigning ID
			detectionCtx.NoteID.Store(42)
			// Simulate audio export failure
			detectionCtx.AudioExportFailed.Store(true)
		},
	}

	sseAction := &SSEAction{
		Settings:       settings,
		Result:         det.Result,
		EventTracker:   eventTracker,
		DetectionCtx:   detectionCtx,
		SSEBroadcaster: sseBroadcaster,
	}

	composite := &CompositeAction{
		Actions:     []Action{dbAction, sseAction},
		Description: "Test AudioExportFailed propagation",
	}

	startTime := time.Now()
	err := composite.Execute(context.Background(), det)
	duration := time.Since(startTime)

	require.NoError(t, err)

	// Verify audio wait was skipped (should be fast, not 5+ seconds)
	assert.Less(t, duration, 500*time.Millisecond,
		"SSE should skip audio wait when AudioExportFailed is set")

	assert.True(t, sseSkippedWait, "SSE broadcaster should have been called")
}

// TestCompositeAction_SequentialExecution verifies that actions execute
// in order, not concurrently.
func TestCompositeAction_SequentialExecution(t *testing.T) {
	t.Parallel()

	executionOrder := make([]string, 0, 3)
	executionMu := sync.Mutex{}

	action1 := &SimpleAction{
		name:         "First Action",
		executeDelay: 50 * time.Millisecond,
		onExecute: func() {
			executionMu.Lock()
			executionOrder = append(executionOrder, "first")
			executionMu.Unlock()
		},
	}

	action2 := &SimpleAction{
		name:         "Second Action",
		executeDelay: 50 * time.Millisecond,
		onExecute: func() {
			executionMu.Lock()
			executionOrder = append(executionOrder, "second")
			executionMu.Unlock()
		},
	}

	action3 := &SimpleAction{
		name:         "Third Action",
		executeDelay: 50 * time.Millisecond,
		onExecute: func() {
			executionMu.Lock()
			executionOrder = append(executionOrder, "third")
			executionMu.Unlock()
		},
	}

	composite := &CompositeAction{
		Actions:     []Action{action1, action2, action3},
		Description: "Sequential execution test",
	}

	err := composite.Execute(context.Background(), nil)
	require.NoError(t, err)

	executionMu.Lock()
	defer executionMu.Unlock()

	require.Len(t, executionOrder, 3)
	assert.Equal(t, "first", executionOrder[0])
	assert.Equal(t, "second", executionOrder[1])
	assert.Equal(t, "third", executionOrder[2])
}

// TestCompositeAction_MultipleDetections verifies that multiple detections
// get unique sequential IDs.
func TestCompositeAction_MultipleDetections(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	settings := &conf.Settings{Debug: true}
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Process multiple detections
	species := []struct {
		common     string
		scientific string
	}{
		{"American Robin", "Turdus migratorius"},
		{"Great Tit", "Parus major"},
		{"Blue Jay", "Cyanocitta cristata"},
	}

	for i, s := range species {
		detectionCtx := &DetectionContext{} // Fresh context for each detection
		det := testDetectionWithSpecies(s.common, s.scientific, 0.9)

		dbAction := &DatabaseAction{
			Settings:     settings,
			Ds:           mockDs,
			Result:       det.Result,
			Results:      det.Results,
			EventTracker: eventTracker,
			DetectionCtx: detectionCtx,
		}

		composite := &CompositeAction{
			Actions:     []Action{dbAction},
			Description: "Multi-detection test",
		}

		err := composite.Execute(context.Background(), det)
		require.NoError(t, err, "Detection %d should succeed", i)
	}

	// Verify all detections saved with unique IDs
	savedNotes := mockDs.GetSavedNotes()
	require.Len(t, savedNotes, 3)

	assert.Equal(t, uint(1), savedNotes[0].ID)
	assert.Equal(t, uint(2), savedNotes[1].ID)
	assert.Equal(t, uint(3), savedNotes[2].ID)

	assert.Equal(t, "American Robin", savedNotes[0].CommonName)
	assert.Equal(t, "Great Tit", savedNotes[1].CommonName)
	assert.Equal(t, "Blue Jay", savedNotes[2].CommonName)
}
