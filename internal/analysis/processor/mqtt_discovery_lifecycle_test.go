// mqtt_discovery_lifecycle_test.go: tests for how Home Assistant discovery is
// (re)published and removed over the life of the MQTT connection (GitHub #4349
// follow-ups).
package processor

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

const lifecycleTestBaseTopic = "birdnet"

// newLifecycleSettings returns settings with MQTT enabled and HA discovery set
// to haEnabled.
func newLifecycleSettings(haEnabled bool) *conf.Settings {
	s := &conf.Settings{}
	s.Main.Name = "node"
	s.Realtime.MQTT.Enabled = true
	s.Realtime.MQTT.Topic = lifecycleTestBaseTopic
	s.Realtime.MQTT.HomeAssistant.Enabled = haEnabled
	s.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
	s.Realtime.MQTT.HomeAssistant.DeviceName = "BirdNET-Go"
	return s
}

// useLiveSettings publishes live as the global settings snapshot for the test
// and restores the previous snapshot afterwards. Tests using it must not run in
// parallel.
func useLiveSettings(t *testing.T, live *conf.Settings) {
	t.Helper()
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	conftest.SetTestSettings(live)
}

// newLifecycleProcessor returns a processor whose registry holds one RTSP
// source per display name.
func newLifecycleProcessor(t *testing.T, displayNames ...string) *Processor {
	t.Helper()
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	for i, name := range displayNames {
		_, err := reg.Register(&audiocore.SourceConfig{
			Type:             audiocore.SourceTypeRTSP,
			ConnectionString: "rtsp://192.0.2.1/stream" + string(rune('a'+i)),
			DisplayName:      name,
			SampleRate:       48000,
			BitDepth:         16,
			Channels:         1,
		})
		require.NoError(t, err)
	}
	p := &Processor{}
	p.registry = reg
	return p
}

// soundLevelConfigPayload returns the last payload published to a sound level
// sensor config topic, and whether one was published.
func soundLevelConfigPayload(msgs []publishedMessage) (string, bool) {
	payload, found := "", false
	for _, m := range msgs {
		if strings.HasSuffix(m.topic, "_sound_level/config") {
			payload, found = m.payload, true
		}
	}
	return payload, found
}

// TestOnConnectHandler_UsesLiveSettings verifies that the HA discovery OnConnect
// handler, which fires on every reconnect, publishes with the settings in force
// when it fires rather than the ones captured at registration.
func TestOnConnectHandler_UsesLiveSettings(t *testing.T) {
	t.Run("HA discovery turned off since registration publishes nothing", func(t *testing.T) {
		registered := newLifecycleSettings(true)
		useLiveSettings(t, newLifecycleSettings(false))

		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		p.RegisterHomeAssistantDiscovery(client, registered)
		handler := client.OnConnectHandler()
		require.NotNil(t, handler)

		handler()

		assert.Empty(t, client.GetPublishedMessages(), "no discovery may be published once HA discovery is off")
	})

	t.Run("sound level enabled since registration publishes the sensor", func(t *testing.T) {
		registered := newLifecycleSettings(true) // sound level monitoring off
		live := newLifecycleSettings(true)
		live.Realtime.Audio.SoundLevel.Enabled = true
		useLiveSettings(t, live)

		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		p.RegisterHomeAssistantDiscovery(client, registered)
		client.OnConnectHandler()()

		payload, found := soundLevelConfigPayload(client.GetPublishedMessages())
		require.True(t, found, "sound level config topic must be published")
		assert.NotEmpty(t, payload, "live settings enable sound level, so the sensor config must not be a removal")
	})

	t.Run("identity stays that of the client's settings", func(t *testing.T) {
		registered := newLifecycleSettings(true)
		live := newLifecycleSettings(true)
		live.Realtime.MQTT.Topic = "moved"
		live.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "otherprefix"
		useLiveSettings(t, live)

		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		p.RegisterHomeAssistantDiscovery(client, registered)
		client.OnConnectHandler()()

		msgs := client.GetPublishedMessages()
		require.NotEmpty(t, msgs)
		for _, m := range msgs {
			assert.False(t, strings.HasPrefix(m.topic, "otherprefix/"),
				"discovery must stay under the client's prefix, got %s", m.topic)
			assert.False(t, strings.HasPrefix(m.topic, "moved/"),
				"per-source topics must stay under the client's base topic, got %s", m.topic)
			assert.NotContains(t, m.payload, `"moved`,
				"sensor state topics must stay under the client's base topic")
		}
	})
}

// TestRefreshHomeAssistantDiscovery_Debounced verifies that a refresh only arms
// the debounce timer: nothing is published on the caller's goroutine (the
// control monitor must never block on the broker), and the debounced publish
// uses live settings, so a sound level toggle adds the Sound Level sensor.
func TestRefreshHomeAssistantDiscovery_Debounced(t *testing.T) {
	live := newLifecycleSettings(true)
	live.Realtime.Audio.SoundLevel.Enabled = true
	useLiveSettings(t, live)

	p := newLifecycleProcessor(t, "Backyard")
	client := NewMockMQTTClient()
	p.SetMQTTClient(client)

	p.RefreshHomeAssistantDiscovery()
	t.Cleanup(func() {
		p.discoveryDebounceMu.Lock()
		defer p.discoveryDebounceMu.Unlock()
		if p.discoveryDebounce != nil {
			p.discoveryDebounce.Stop()
		}
	})

	assert.Empty(t, client.GetPublishedMessages(), "refresh must not publish synchronously")
	p.discoveryDebounceMu.Lock()
	armed := p.discoveryDebounce != nil
	p.discoveryDebounceMu.Unlock()
	assert.True(t, armed, "refresh must arm the debounce timer")

	// Run what the timer runs, without waiting for it.
	p.publishDiscoveryIfReady()
	payload, found := soundLevelConfigPayload(client.GetPublishedMessages())
	require.True(t, found, "the debounced publish must include the sound level sensor config")
	assert.NotEmpty(t, payload, "sound level is on, so the sensor config must not be a removal")
}

// TestCleanupDefaultDiscovery_KeepsBridge verifies the legacy "default" source
// cleanup removes that source's sensors but never publishes a removal for the
// shared bridge config, which would make the bridge entity flap once per start.
func TestCleanupDefaultDiscovery_KeepsBridge(t *testing.T) {
	t.Parallel()

	client := NewMockMQTTClient()
	publisher := mqtt.NewDiscoveryPublisher(client, &mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       lifecycleTestBaseTopic,
		DeviceName:      "BirdNET-Go",
		NodeID:          "node",
	})

	cleanupDefaultDiscovery(t.Context(), publisher)

	msgs := client.GetPublishedMessages()
	require.NotEmpty(t, msgs, "the legacy source must be cleaned up")
	removedLegacySpecies := false
	for _, m := range msgs {
		assert.NotEqual(t, "homeassistant/binary_sensor/node/status/config", m.topic,
			"cleanup must not remove the bridge")
		if m.topic == "homeassistant/sensor/node/node_Default_species/config" {
			removedLegacySpecies = m.payload == "" && m.retain
		}
	}
	assert.True(t, removedLegacySpecies, "the legacy source's species config must get a retained removal")
}
