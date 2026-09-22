// mqtt_source_topic_test.go - Tests for the per-source detection topic that
// Home Assistant discovery sensors read (GitHub #4349).
package processor

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// newSourceTopicTestAction builds an MqttAction for a detection from sourceID
// with HA discovery set to haEnabled.
func newSourceTopicTestAction(t *testing.T, client *MockMQTTClient, sourceID string, haEnabled bool) *MqttAction {
	t.Helper()

	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic
	settings.Realtime.MQTT.HomeAssistant.Enabled = haEnabled

	det := testDetection()
	det.Result.AudioSource.ID = sourceID
	det.Result.AudioSource.DisplayName = "Backyard Microphone"

	detectionCtx := &DetectionContext{}
	detectionCtx.NoteID.Store(7)

	return &MqttAction{
		Settings:     settings,
		Result:       det.Result,
		MqttClient:   client,
		EventTracker: NewEventTracker(testEventTrackerInterval),
		DetectionCtx: detectionCtx,
	}
}

// discoveredSpeciesStateTopic runs the real HA discovery publisher for source
// and returns the state_topic of its Last Species sensor.
func discoveredSpeciesStateTopic(t *testing.T, source datastore.AudioSource) string {
	t.Helper()

	recorder := NewMockMQTTClient()
	settings := &conf.Settings{}
	publisher := mqtt.NewDiscoveryPublisher(recorder, &mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       testMQTTTopic,
		DeviceName:      "BirdNET-Go",
		NodeID:          "test-node",
		Version:         "test",
	})
	require.NoError(t, publisher.PublishDiscovery(t.Context(), []datastore.AudioSource{source}, settings))

	for _, msg := range recorder.GetPublishedMessages() {
		if !strings.HasSuffix(msg.topic, "_species/config") {
			continue
		}
		var payload mqtt.DiscoveryPayload
		require.NoError(t, json.Unmarshal([]byte(msg.payload), &payload))
		return payload.StateTopic
	}
	require.FailNow(t, "discovery published no Last Species sensor")
	return ""
}

// TestMqttAction_Execute_PublishesToDiscoveredSourceTopic verifies the end to
// end contract behind GitHub #4349: with HA discovery enabled, a detection is
// published to the exact topic the source's discovered sensors read, with the
// same payload as the shared topic.
func TestMqttAction_Execute_PublishesToDiscoveredSourceTopic(t *testing.T) {
	t.Parallel()

	const sourceID = "rtsp_65c31a0b"
	stateTopic := discoveredSpeciesStateTopic(t, datastore.AudioSource{ID: sourceID, DisplayName: "Backyard Microphone"})
	require.NotEqual(t, testMQTTTopic, stateTopic, "discovered sensor must not read the shared topic")

	client := NewMockMQTTClient()
	action := newSourceTopicTestAction(t, client, sourceID, true)

	require.NoError(t, action.Execute(t.Context(), nil))

	msgs := client.GetPublishedMessages()
	require.Len(t, msgs, 2, "expected one shared and one per-source publish")
	assert.Equal(t, testMQTTTopic, msgs[0].topic, "shared topic keeps receiving every detection")
	assert.Equal(t, stateTopic, msgs[1].topic, "per-source publish must hit the discovered state topic")
	assert.JSONEq(t, msgs[0].payload, msgs[1].payload, "per-source payload must match the shared one")

	var jsonMap map[string]any
	require.NoError(t, json.Unmarshal([]byte(msgs[1].payload), &jsonMap))
	assert.Equal(t, sourceID, jsonMap["sourceId"])
}

// TestMqttAction_Execute_SourceTopicSkipped verifies the cases that publish
// only to the shared topic.
func TestMqttAction_Execute_SourceTopicSkipped(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name      string
		sourceID  string
		haEnabled bool
	}{
		// Without discovery nothing reads the per-source topic, so installs that
		// do not use HA see no extra broker traffic.
		{name: "HA discovery disabled", sourceID: "rtsp_65c31a0b", haEnabled: false},
		// No discovered sensor can match a detection without a source.
		{name: "empty source ID", sourceID: "", haEnabled: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			client := NewMockMQTTClient()
			action := newSourceTopicTestAction(t, client, tt.sourceID, tt.haEnabled)

			require.NoError(t, action.Execute(t.Context(), nil))

			msgs := client.GetPublishedMessages()
			require.Len(t, msgs, 1)
			assert.Equal(t, testMQTTTopic, msgs[0].topic)
		})
	}
}

// TestMqttAction_Execute_SourceTopicFailure_NonFatal verifies that a failed
// per-source publish does not fail the action. The detection already reached
// the shared topic; failing would make a retry publish it there twice.
func TestMqttAction_Execute_SourceTopicFailure_NonFatal(t *testing.T) {
	t.Parallel()

	const sourceID = "rtsp_65c31a0b"
	client := NewMockMQTTClient()
	client.SetTopicError(mqtt.SourceDetectionTopic(testMQTTTopic, sourceID), errors.New("broker rejected publish"))
	action := newSourceTopicTestAction(t, client, sourceID, true)

	require.NoError(t, action.Execute(t.Context(), nil))

	assert.Equal(t, 2, client.GetPublishCalls(), "both publishes must be attempted")
	msgs := client.GetPublishedMessages()
	require.Len(t, msgs, 1)
	assert.Equal(t, testMQTTTopic, msgs[0].topic)
}
