// composite_action_integration_test.go - Full integration tests for CompositeAction
//
// These tests verify the complete data flow from DatabaseAction through
// MQTT and SSE actions, ensuring DetectionContext properly propagates IDs.
package processor

import (
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
	err := composite.Execute(t.Context(), det)
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
	err := composite.Execute(t.Context(), det)
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

// TestCompositeAction_RepoPath_DatabaseToMQTT_IDPropagation verifies that the
// detection ID flows correctly when using the DetectionRepository path (production).
// This mirrors TestCompositeAction_DatabaseToMQTT_IDPropagation but sets the Repo
// field on DatabaseAction, which is how the Processor configures it in production.
// The legacy tests only exercise the Ds (legacy) path — this test ensures the Repo
// path also propagates the ID through DetectionContext to MQTT.
func TestCompositeAction_RepoPath_DatabaseToMQTT_IDPropagation(t *testing.T) {
	t.Parallel()

	// Setup shared components
	mockDs := NewActionMockDatastore()
	mockMqtt := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Topic = testMQTTTopic
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create a real DetectionRepository wrapping the mock datastore,
	// matching the production setup in processor.go line 381
	repo := datastore.NewDetectionRepository(mockDs, nil)

	// Create shared DetectionContext
	detectionCtx := &DetectionContext{}

	det := testDetection()

	// Create DatabaseAction with BOTH Ds and Repo set (production configuration).
	// When Repo is non-nil, the Repo path is taken (lines 89-103 of actions_database.go).
	dbAction := &DatabaseAction{
		Settings:     settings,
		Ds:           mockDs, // Legacy fallback (not used when Repo is set)
		Repo:         repo,   // Production path — DetectionRepository
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
		Description: "Repo path: Database save then MQTT publish",
	}

	// Execute
	err := composite.Execute(t.Context(), det)
	require.NoError(t, err)

	// Verify database saved with assigned ID
	savedNote := mockDs.GetLastSavedNote()
	require.NotNil(t, savedNote)
	assert.Equal(t, uint(1), savedNote.ID, "First save should get ID 1")

	// Verify DetectionContext was updated with the database ID
	assert.Equal(t, uint64(1), detectionCtx.NoteID.Load(),
		"DetectionContext should contain database ID from Repo path")

	// Verify MQTT payload contains the database ID
	payload := mockMqtt.GetPublishedPayload()
	require.NotEmpty(t, payload)

	var jsonMap map[string]any
	err = json.Unmarshal([]byte(payload), &jsonMap)
	require.NoError(t, err)

	detectionID, ok := jsonMap["detectionId"].(float64)
	require.True(t, ok, "detectionId should be present in MQTT payload")
	assert.InDelta(t, float64(1), detectionID, 0.001,
		"MQTT payload should contain database ID from Repo path")

	// Also verify the embedded Note.ID in the JSON payload
	noteID, ok := jsonMap["ID"].(float64)
	require.True(t, ok, "Note ID should be present in MQTT payload")
	assert.InDelta(t, float64(1), noteID, 0.001,
		"Embedded Note.ID should match database ID")
}

// TestCompositeAction_RepoPath_MultipleDetections verifies that multiple
// detections get unique sequential IDs when using the Repo path (production).
func TestCompositeAction_RepoPath_MultipleDetections(t *testing.T) {
	t.Parallel()

	mockDs := NewActionMockDatastore()
	mockMqtt := NewMockMQTTClient()
	settings := &conf.Settings{Debug: true}
	settings.Realtime.MQTT.Topic = testMQTTTopic
	eventTracker := NewEventTracker(testEventTrackerInterval)

	// Create a real DetectionRepository wrapping the mock datastore
	repo := datastore.NewDetectionRepository(mockDs, nil)

	species := []struct {
		common     string
		scientific string
	}{
		{"Eurasian Pygmy Owl", "Glaucidium passerinum"},
		{"Great Tit", "Parus major"},
		{"Eurasian Blue Tit", "Cyanistes caeruleus"},
	}

	for i, s := range species {
		detectionCtx := &DetectionContext{} // Fresh context per detection
		det := testDetectionWithSpecies(s.common, s.scientific, 0.95)

		dbAction := &DatabaseAction{
			Settings:     settings,
			Ds:           mockDs,
			Repo:         repo, // Production path
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

		composite := &CompositeAction{
			Actions:     []Action{dbAction, mqttAction},
			Description: "Repo path: multi-detection test",
		}

		err := composite.Execute(t.Context(), det)
		require.NoError(t, err, "Detection %d (%s) should succeed", i, s.common)

		// Verify DetectionContext has the correct ID
		expectedID := uint64(i + 1)
		assert.Equal(t, expectedID, detectionCtx.NoteID.Load(),
			"Detection %d should have ID %d", i, expectedID)

		// Verify MQTT payload
		payload := mockMqtt.GetPublishedPayload()
		require.NotEmpty(t, payload)

		var jsonMap map[string]any
		err = json.Unmarshal([]byte(payload), &jsonMap)
		require.NoError(t, err)

		detectionID, ok := jsonMap["detectionId"].(float64)
		require.True(t, ok)
		assert.InDelta(t, float64(expectedID), detectionID, 0.001,
			"MQTT detection %d should have ID %d", i, expectedID)
	}
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
	err := composite.Execute(t.Context(), det)
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

	err := composite.Execute(t.Context(), nil)
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

		err := composite.Execute(t.Context(), det)
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
