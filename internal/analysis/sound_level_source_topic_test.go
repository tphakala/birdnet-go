// sound_level_source_topic_test.go: tests for the per-source sound level topic
// that the Home Assistant discovery sound level sensor reads (GitHub #4349).
package analysis

import (
	"context"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

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
			assert.Equal(t, tt.wantTopics, topics)
			for _, p := range payloads[1:] {
				assert.JSONEq(t, payloads[0], p, "per-source payload must match the shared one")
			}
		})
	}
}
