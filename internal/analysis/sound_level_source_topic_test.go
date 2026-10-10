// sound_level_source_topic_test.go: tests for the per-source sound level topic
// that the Home Assistant discovery sound level sensor reads (GitHub #4349).
package analysis

import (
	"context"
	"encoding/json"
	"strings"
	"sync"
	"testing"

	"github.com/prometheus/client_golang/prometheus/testutil"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/analysis/processor"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/mqtt"
	"github.com/tphakala/birdnet-go/internal/observability"
)

// TestPublishSoundLevelToSourceTopic_RecordsFailureMetric verifies that a
// non-sentinel per-source publish failure records a source_topic_error metric,
// while the ErrMQTTClientNotReady sentinel records no error metric (it is a
// graceful no-op, same as the shared-topic path).
func TestPublishSoundLevelToSourceTopic_RecordsFailureMetric(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	const baseTopic = "birdnet/test"
	data := makeValidSoundLevelData()
	sourceTopic := mqtt.SourceSoundLevelTopic(baseTopic, data.Source)

	tests := []struct {
		name       string
		failErr    error
		wantErrors int
	}{
		{name: "non-sentinel failure records source_topic_error", failErr: errors.NewStd("broker rejected"), wantErrors: 1},
		{name: "client-not-ready sentinel records no error", failErr: processor.ErrMQTTClientNotReady, wantErrors: 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := conf.GetSettings()
			t.Cleanup(func() { conftest.SetTestSettings(prev) })

			metricsObj, err := observability.NewMetrics()
			require.NoError(t, err)

			proc := &processor.Processor{Settings: &conf.Settings{}, Metrics: metricsObj}
			proc.SetMQTTClient(&mockMQTTClient{
				connected: true,
				publishFunc: func(_ context.Context, topic, _ string) error {
					if topic == sourceTopic {
						return tt.failErr
					}
					return nil
				},
			})

			settings := &conf.Settings{}
			settings.Realtime.MQTT.Enabled = true
			settings.Realtime.MQTT.Topic = baseTopic
			settings.Realtime.MQTT.HomeAssistant.Enabled = true
			conftest.SetTestSettings(settings)

			require.NoError(t, publishSoundLevelToMQTT(data, proc))

			// Assert the exact value of the specific labelled series, not just how
			// many series exist, so a double count or a wrong-label increment is
			// caught.
			got := testutil.ToFloat64(metricsObj.SoundLevel.SoundLevelPublishingErrorsVec().
				WithLabelValues(data.Source, data.Name, "mqtt", "source_topic_error"))
			assert.InDelta(t, float64(tt.wantErrors), got, 0, "source_topic_error metric value")
		})
	}
}

func TestPublishSoundLevelToMQTT_PerSourceTopic(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	const baseTopic = "birdnet/test"

	tests := []struct {
		name       string
		haEnabled  bool
		wantTopics []string
	}{
		{
			name:      "HA discovery enabled publishes shared and per-source",
			haEnabled: true,
			wantTopics: []string{
				mqtt.SoundLevelTopic(baseTopic),
				mqtt.SourceSoundLevelTopic(baseTopic, makeValidSoundLevelData().Source),
			},
		},
		{
			name:       "HA discovery disabled publishes shared only",
			haEnabled:  false,
			wantTopics: []string{mqtt.SoundLevelTopic(baseTopic)},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := conf.GetSettings()
			t.Cleanup(func() { conftest.SetTestSettings(prev) })

			var mu sync.Mutex
			var topics, payloads []string
			proc := createMockProcessor(func(_ context.Context, topic, payload string) error {
				mu.Lock()
				defer mu.Unlock()
				topics = append(topics, topic)
				payloads = append(payloads, payload)
				return nil
			})

			settings := &conf.Settings{}
			settings.Realtime.MQTT.Enabled = true
			settings.Realtime.MQTT.Topic = baseTopic
			settings.Realtime.MQTT.HomeAssistant.Enabled = tt.haEnabled
			conftest.SetTestSettings(settings)

			require.NoError(t, publishSoundLevelToMQTT(makeValidSoundLevelData(), proc))

			mu.Lock()
			defer mu.Unlock()
			require.Equal(t, tt.wantTopics, topics)
			for _, p := range payloads[1:] {
				assert.JSONEq(t, payloads[0], p, "per-source payload must match the shared one")
			}
		})
	}
}

// TestPublishSoundLevelToMQTT_PublishFailures exercises the failure paths with HA
// discovery enabled: a per-source publish failure is non-fatal (the shared topic
// already got the reading), the ErrMQTTClientNotReady sentinel is dropped
// silently, and a shared-topic failure returns an error before the per-source
// topic is attempted.
func TestPublishSoundLevelToMQTT_PublishFailures(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	const baseTopic = "birdnet/test"
	sharedTopic := mqtt.SoundLevelTopic(baseTopic)
	sourceTopic := mqtt.SourceSoundLevelTopic(baseTopic, makeValidSoundLevelData().Source)

	tests := []struct {
		name          string
		failTopic     string
		failErr       error
		wantErr       bool
		wantAttempted []string
	}{
		{
			name:          "per-source publish error is non-fatal",
			failTopic:     sourceTopic,
			failErr:       errors.NewStd("broker rejected"),
			wantErr:       false,
			wantAttempted: []string{sharedTopic, sourceTopic},
		},
		{
			name:          "per-source client-not-ready is dropped silently",
			failTopic:     sourceTopic,
			failErr:       processor.ErrMQTTClientNotReady,
			wantErr:       false,
			wantAttempted: []string{sharedTopic, sourceTopic},
		},
		{
			name:          "shared publish error skips the per-source topic",
			failTopic:     sharedTopic,
			failErr:       errors.NewStd("broker down"),
			wantErr:       true,
			wantAttempted: []string{sharedTopic},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			prev := conf.GetSettings()
			t.Cleanup(func() { conftest.SetTestSettings(prev) })

			var mu sync.Mutex
			var attempted []string
			proc := createMockProcessor(func(_ context.Context, topic, _ string) error {
				mu.Lock()
				defer mu.Unlock()
				attempted = append(attempted, topic)
				if topic == tt.failTopic {
					return tt.failErr
				}
				return nil
			})

			settings := &conf.Settings{}
			settings.Realtime.MQTT.Enabled = true
			settings.Realtime.MQTT.Topic = baseTopic
			settings.Realtime.MQTT.HomeAssistant.Enabled = true
			conftest.SetTestSettings(settings)

			err := publishSoundLevelToMQTT(makeValidSoundLevelData(), proc)
			if tt.wantErr {
				require.Error(t, err)
			} else {
				require.NoError(t, err)
			}

			mu.Lock()
			defer mu.Unlock()
			assert.Equal(t, tt.wantAttempted, attempted, "shared topic must be attempted first")
		})
	}
}

// TestSoundLevelPerSourceTopic_MatchesDiscoveredStateTopic is an end-to-end
// parity check: the topic the sound level publisher writes to must be exactly the
// state_topic the real HA discovery publisher points the Sound Level sensor at.
func TestSoundLevelPerSourceTopic_MatchesDiscoveredStateTopic(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	const baseTopic = "birdnet/test"
	data := makeValidSoundLevelData()

	// 1. Run the real discovery publisher (sound level enabled) and read the Sound
	//    Level sensor's state_topic.
	var mu sync.Mutex
	discovered := make(map[string]string)
	discoveryClient := &mockMQTTClient{connected: true, publishFunc: func(_ context.Context, topic, payload string) error {
		mu.Lock()
		defer mu.Unlock()
		discovered[topic] = payload
		return nil
	}}
	discoverySettings := &conf.Settings{}
	discoverySettings.Realtime.Audio.SoundLevel.Enabled = true
	publisher := mqtt.NewDiscoveryPublisher(discoveryClient, &mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       baseTopic,
		DeviceName:      "BirdNET-Go",
		NodeID:          "node",
		Version:         "test",
	})
	source := datastore.AudioSource{ID: data.Source, DisplayName: data.Name}
	require.NoError(t, publisher.PublishDiscovery(t.Context(), []datastore.AudioSource{source}, discoverySettings))

	var soundLevelStateTopic string
	mu.Lock()
	for topic, payload := range discovered {
		if strings.HasSuffix(topic, "_sound_level/config") {
			var p mqtt.DiscoveryPayload
			require.NoError(t, json.Unmarshal([]byte(payload), &p))
			soundLevelStateTopic = p.StateTopic
		}
	}
	mu.Unlock()
	require.NotEmpty(t, soundLevelStateTopic, "discovery must publish a Sound Level sensor")

	// 2. Run the publisher and capture the per-source publish topic.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	var topics []string
	proc := createMockProcessor(func(_ context.Context, topic, _ string) error {
		mu.Lock()
		defer mu.Unlock()
		topics = append(topics, topic)
		return nil
	})
	runtimeSettings := &conf.Settings{}
	runtimeSettings.Realtime.MQTT.Enabled = true
	runtimeSettings.Realtime.MQTT.Topic = baseTopic
	runtimeSettings.Realtime.MQTT.HomeAssistant.Enabled = true
	conftest.SetTestSettings(runtimeSettings)

	require.NoError(t, publishSoundLevelToMQTT(data, proc))

	mu.Lock()
	defer mu.Unlock()
	require.Len(t, topics, 2, "expected one shared and one per-source publish")
	assert.Equal(t, soundLevelStateTopic, topics[1],
		"per-source sound level publish must hit the discovered state topic")
}
