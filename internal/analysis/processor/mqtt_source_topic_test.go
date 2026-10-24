// mqtt_source_topic_test.go - Tests for the per-source detection topic that
// Home Assistant discovery sensors read (GitHub #4349).
package processor

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/alerting"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
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

// TestMqttAction_Execute_SourceTopicSkippedWhenStepExpired verifies that when
// the composite step context is already spent, the shared publish (which runs on
// its own background timeout) still delivers, but the per-source republish is not
// even attempted: publishSourceDetection checks the step context first and returns
// without publishing when it is already done, so no doomed publish is started. The
// action still returns nil (a missed per-source publish is non-fatal).
func TestMqttAction_Execute_SourceTopicSkippedWhenStepExpired(t *testing.T) {
	t.Parallel()

	const sourceID = "rtsp_65c31a0b"
	client := NewMockMQTTClient()
	action := newSourceTopicTestAction(t, client, sourceID, true)

	ctx, cancel := context.WithCancel(t.Context())
	cancel() // Simulate a step whose budget is already spent.

	require.NoError(t, action.Execute(ctx, nil))

	assert.Equal(t, 1, client.GetPublishCalls(), "only the shared publish is attempted; the per-source one is skipped")
	msgs := client.GetPublishedMessages()
	require.Len(t, msgs, 1, "only the shared publish should be delivered")
	assert.Equal(t, testMQTTTopic, msgs[0].topic, "shared publish uses its own background timeout")
}

// TestMqttAction_SourceTopicFailure_Alerting verifies that a non-transient
// per-source publish failure raises the same MQTT-publish-failed alert the
// shared path raises, while a transient connection failure stays a warning only.
// The shared publish succeeds here, so its own alert never fires; only the
// per-source failure can raise one.
func TestMqttAction_SourceTopicFailure_Alerting(t *testing.T) {
	// Not parallel: SetGlobalBus mutates a package-global singleton.
	const (
		sourceID     = "rtsp_65c31a0b"
		testBroker   = "tcp://broker.example:1883"
		barrierEvent = "test.barrier"
	)

	tests := []struct {
		name      string
		failErr   error
		wantAlert bool
	}{
		{name: "non-transient failure raises alert", failErr: errors.NewStd("broker rejected publish"), wantAlert: true},
		{name: "transient failure does not alert", failErr: errors.NewStd("connection lost"), wantAlert: false},
		{name: "deadline exceeded does not alert", failErr: context.DeadlineExceeded, wantAlert: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prevBus := alerting.GetGlobalBus()
			bus := alerting.NewAlertEventBus(nil)
			alerting.SetGlobalBus(bus)
			t.Cleanup(func() {
				bus.Stop()
				alerting.SetGlobalBus(prevBus)
			})

			var mu sync.Mutex
			var events []*alerting.AlertEvent
			bus.Subscribe(func(e *alerting.AlertEvent) {
				mu.Lock()
				defer mu.Unlock()
				events = append(events, e)
			})

			client := NewMockMQTTClient()
			client.SetTopicError(mqtt.SourceDetectionTopic(testMQTTTopic, sourceID), tt.failErr)
			action := newSourceTopicTestAction(t, client, sourceID, true)
			action.Settings.Realtime.MQTT.Broker = testBroker

			require.NoError(t, action.Execute(t.Context(), nil))

			// FIFO barrier: the bus delivers in order on a single worker, so once
			// the sentinel arrives any alert Execute raised has already arrived.
			bus.Publish(&alerting.AlertEvent{ObjectType: alerting.ObjectTypeIntegration, EventName: barrierEvent})
			require.Eventually(t, func() bool {
				mu.Lock()
				defer mu.Unlock()
				return slices.ContainsFunc(events, func(e *alerting.AlertEvent) bool { return e.EventName == barrierEvent })
			}, time.Second, 5*time.Millisecond)

			mu.Lock()
			defer mu.Unlock()
			var mqttAlerts []*alerting.AlertEvent
			for _, e := range events {
				if e.EventName == alerting.EventMQTTPublishFailed {
					mqttAlerts = append(mqttAlerts, e)
				}
			}
			if tt.wantAlert {
				require.Len(t, mqttAlerts, 1, "non-transient failure must raise exactly one alert")
				assert.Equal(t, testBroker, mqttAlerts[0].Properties[alerting.PropertyBroker])
				assert.Contains(t, mqttAlerts[0].Properties[alerting.PropertyError], "broker rejected publish",
					"alert must carry the underlying publish error message")
			} else {
				assert.Empty(t, mqttAlerts, "transient failure must not raise an alert")
			}
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
	client.SetTopicError(mqtt.SourceDetectionTopic(testMQTTTopic, sourceID), errors.NewStd("broker rejected publish"))
	action := newSourceTopicTestAction(t, client, sourceID, true)

	require.NoError(t, action.Execute(t.Context(), nil))

	assert.Equal(t, 2, client.GetPublishCalls(), "both publishes must be attempted")
	msgs := client.GetPublishedMessages()
	require.Len(t, msgs, 1)
	assert.Equal(t, testMQTTTopic, msgs[0].topic)
}

// TestMqttAction_Execute_TrailingSlashBase_MatchesDiscoveredTopic verifies the
// per-source detection publish hits exactly the discovered species state_topic
// even when the base topic carries a trailing slash ("birdnet/"), so a
// trailing-slash base never desynchronizes the publisher from discovery.
func TestMqttAction_Execute_TrailingSlashBase_MatchesDiscoveredTopic(t *testing.T) {
	t.Parallel()

	const base = "birdnet/"
	const sourceID = "rtsp_65c31a0b"
	source := datastore.AudioSource{ID: sourceID, DisplayName: "Backyard Microphone"}

	// Discover the species state topic for the trailing-slash base.
	recorder := NewMockMQTTClient()
	publisher := mqtt.NewDiscoveryPublisher(recorder, &mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       base,
		DeviceName:      "BirdNET-Go",
		NodeID:          "node",
		Version:         "test",
	})
	require.NoError(t, publisher.PublishDiscovery(t.Context(), []datastore.AudioSource{source}, &conf.Settings{}))

	var stateTopic string
	for _, msg := range recorder.GetPublishedMessages() {
		if !strings.HasSuffix(msg.topic, "_species/config") {
			continue
		}
		var payload mqtt.DiscoveryPayload
		require.NoError(t, json.Unmarshal([]byte(msg.payload), &payload))
		stateTopic = payload.StateTopic
	}
	require.NotEmpty(t, stateTopic, "discovery must publish a species sensor")

	// Run the action against the same trailing-slash base.
	client := NewMockMQTTClient()
	action := newSourceTopicTestAction(t, client, sourceID, true)
	action.Settings.Realtime.MQTT.Topic = base

	require.NoError(t, action.Execute(t.Context(), nil))

	msgs := client.GetPublishedMessages()
	require.Len(t, msgs, 2, "expected one shared and one per-source publish")
	assert.Equal(t, stateTopic, msgs[1].topic,
		"per-source publish must hit the discovered state topic for a trailing-slash base")
}
