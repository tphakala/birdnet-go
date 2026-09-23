// mqtt_discovery_lifecycle_test.go: tests for how Home Assistant discovery is
// (re)published and removed over the life of the MQTT connection (GitHub #4349
// follow-ups).
package processor

import (
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
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

// soundLevelConfigPayload returns the last payload published to the sound level
// sensor config topic of the "Backyard" source used by these tests, and whether
// one was published. It matches the exact topic: other sound level config
// topics (e.g. the suffixed-key cleanup) must not be mistaken for it.
func soundLevelConfigPayload(msgs []publishedMessage) (string, bool) {
	const topic = "homeassistant/sensor/node/node_Backyard_sound_level/config"
	payload, found := "", false
	for _, m := range msgs {
		if m.topic == topic {
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

// publishForRetireTest publishes discovery for p through client under settings
// and fails the test on error, returning the published message count.
func publishForRetireTest(t *testing.T, p *Processor, client *MockMQTTClient, settings *conf.Settings) int {
	t.Helper()
	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))
	return len(client.GetPublishedMessages())
}

// TestRetireHomeAssistantDiscovery covers removing HA entities when HA discovery
// is turned off: removal happens under the identity that was published, every
// removal is retained, and the published identity is forgotten only after a
// complete removal.
func TestRetireHomeAssistantDiscovery(t *testing.T) {
	t.Run("removes everything under the published identity", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		before := publishForRetireTest(t, p, client, published)

		next := newLifecycleSettings(false)
		next.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "otherprefix" // must not matter
		useLiveSettings(t, next)
		p.RetireHomeAssistantDiscovery(t.Context(), client, next)

		removals := client.GetPublishedMessages()[before:]
		require.NotEmpty(t, removals)
		topics := make(map[string]bool, len(removals))
		for _, m := range removals {
			assert.Empty(t, m.payload, "retire publishes only removals, got payload on %s", m.topic)
			assert.True(t, m.retain, "removal of %s must be retained", m.topic)
			assert.False(t, strings.HasPrefix(m.topic, "otherprefix/"), "removal must use the published prefix")
			topics[m.topic] = true
		}
		assert.True(t, topics["homeassistant/binary_sensor/node/status/config"], "bridge must be removed")
		assert.True(t, topics["homeassistant/sensor/node/node_Backyard_species/config"], "source sensors must be removed")
		p.haDiscoveryMu.Lock()
		assert.Nil(t, p.haPublishedConfig, "identity is forgotten after a complete removal")
		p.haDiscoveryMu.Unlock()
	})

	noOps := []struct {
		name    string
		publish bool
		next    func() *conf.Settings
	}{
		{name: "nothing published in this process", publish: false, next: func() *conf.Settings { return newLifecycleSettings(false) }},
		{name: "HA discovery still enabled", publish: true, next: func() *conf.Settings { return newLifecycleSettings(true) }},
		{name: "MQTT disabled entirely", publish: true, next: func() *conf.Settings {
			s := newLifecycleSettings(false)
			s.Realtime.MQTT.Enabled = false
			return s
		}},
	}
	for _, tt := range noOps {
		t.Run("no-op: "+tt.name, func(t *testing.T) {
			published := newLifecycleSettings(true)
			useLiveSettings(t, published)
			p := newLifecycleProcessor(t, "Backyard")
			client := NewMockMQTTClient()
			before := 0
			if tt.publish {
				before = publishForRetireTest(t, p, client, published)
			}
			p.RetireHomeAssistantDiscovery(t.Context(), client, tt.next())
			assert.Len(t, client.GetPublishedMessages(), before, "retire must publish nothing")
		})
	}

	t.Run("disconnected client keeps the identity, next connect retires", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newLifecycleProcessor(t, "Backyard")
		oldClient := NewMockMQTTClient()
		publishForRetireTest(t, p, oldClient, published)

		next := newLifecycleSettings(false)
		useLiveSettings(t, next)
		oldClient.SetConnected(false)
		p.RetireHomeAssistantDiscovery(t.Context(), oldClient, next)
		p.haDiscoveryMu.Lock()
		assert.NotNil(t, p.haPublishedConfig, "identity must be kept when removal was impossible")
		p.haDiscoveryMu.Unlock()

		// The new client registers handlers while HA discovery is off; its
		// retire handler performs the removal on connect.
		newClient := NewMockMQTTClient()
		p.RegisterHomeAssistantDiscovery(newClient, next)
		handler := newClient.OnConnectHandler()
		require.NotNil(t, handler, "a retire handler must be registered even with HA discovery off")
		handler()

		assert.NotEmpty(t, newClient.GetPublishedMessages(), "the retire handler must remove the entities")
		p.haDiscoveryMu.Lock()
		assert.Nil(t, p.haPublishedConfig)
		p.haDiscoveryMu.Unlock()
	})

	t.Run("failed removal keeps the identity", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		publishForRetireTest(t, p, client, published)

		next := newLifecycleSettings(false)
		useLiveSettings(t, next)
		client.SetTopicError("homeassistant/binary_sensor/node/status/config", assert.AnError)
		p.RetireHomeAssistantDiscovery(t.Context(), client, next)

		p.haDiscoveryMu.Lock()
		assert.NotNil(t, p.haPublishedConfig, "identity must be kept after a partial removal")
		p.haDiscoveryMu.Unlock()
	})

	t.Run("a publish queued before the switch does not recreate entities", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, newLifecycleSettings(false)) // HA already off when it runs
		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, published))
		assert.Empty(t, client.GetPublishedMessages())
	})
}

// registerLifecycleSource adds an RTSP source with a unique connection string
// and returns its registry snapshot as a datastore.AudioSource.
func registerLifecycleSource(t *testing.T, p *Processor, conn, name string) datastore.AudioSource {
	t.Helper()
	src, err := p.registry.Register(&audiocore.SourceConfig{
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: conn,
		DisplayName:      name,
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	})
	require.NoError(t, err)
	return datastore.AudioSource{ID: src.ID, DisplayName: src.DisplayName}
}

// newEmptyLifecycleProcessor returns a processor with an empty registry.
func newEmptyLifecycleProcessor() *Processor {
	p := &Processor{}
	p.registry = audiocore.NewSourceRegistry(audiocore.GetLogger())
	return p
}

// lastPayloads maps every topic in msgs to its last payload.
func lastPayloads(msgs []publishedMessage) map[string]publishedMessage {
	out := make(map[string]publishedMessage, len(msgs))
	for _, m := range msgs {
		out[m.topic] = m
	}
	return out
}

// assertRemoved asserts topic received a retained empty payload as its last
// publish. It requires the topic to have been published at all: a map lookup
// of a never-published topic yields an empty payload and would pass vacuously.
func assertRemoved(t *testing.T, last map[string]publishedMessage, topic string) {
	t.Helper()
	m, ok := last[topic]
	require.True(t, ok, "expected a removal on %s, but nothing was published there", topic)
	assert.Empty(t, m.payload, "%s must be removed", topic)
	assert.True(t, m.retain, "removal of %s must be retained", topic)
}

func speciesConfigTopic(key string) string {
	return "homeassistant/sensor/node/node_" + key + "_species/config"
}

// stopDebounce stops the processor's debounce timer at test end so a queued
// refresh does not publish after the test.
func stopDebounce(t *testing.T, p *Processor) {
	t.Helper()
	t.Cleanup(func() {
		p.discoveryDebounceMu.Lock()
		defer p.discoveryDebounceMu.Unlock()
		if p.discoveryDebounce != nil {
			p.discoveryDebounce.Stop()
		}
	})
}

// TestForgetHomeAssistantSource covers removing the HA entities of streams
// deleted or renamed in settings. Removals run on the next discovery publish,
// computed from the live sources alone (no record of what was published).
func TestForgetHomeAssistantSource(t *testing.T) {
	t.Run("deleted stream: configs and state removed, others untouched", func(t *testing.T) {
		settings := newLifecycleSettings(true)
		useLiveSettings(t, settings)
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)

		require.NoError(t, p.registry.Unregister(garden.ID))
		p.ForgetHomeAssistantSource(garden)
		assert.Empty(t, client.GetPublishedMessages(), "forget must not publish on the caller's goroutine")
		p.publishDiscoveryIfReady()

		last := lastPayloads(client.GetPublishedMessages())
		for _, topic := range []string{
			speciesConfigTopic("Garden"),
			"birdnet/sources/" + garden.ID,
			"birdnet/sources/" + garden.ID + "/soundlevel",
		} {
			assertRemoved(t, last, topic)
		}
		assert.NotEmpty(t, last[speciesConfigTopic("Porch")].payload, "a live source must keep its entities")
	})

	t.Run("URL edited, same name: the new source keeps the entities", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		old := registerLifecycleSource(t, p, "rtsp://192.0.2.1/old", "Garden")
		// Production order: the new source is registered before the old one is removed.
		replacement := registerLifecycleSource(t, p, "rtsp://192.0.2.1/new", "Garden")
		require.NoError(t, p.registry.Unregister(old.ID))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)

		p.ForgetHomeAssistantSource(old)
		p.publishDiscoveryIfReady()

		msgs := client.GetPublishedMessages()
		for _, m := range msgs {
			if m.topic == speciesConfigTopic("Garden") {
				assert.NotEmpty(t, m.payload, "the live replacement's config must never be emptied")
			}
		}
		last := lastPayloads(msgs)
		assertRemoved(t, last, "birdnet/sources/"+old.ID)
		assert.NotContains(t, last, "birdnet/sources/"+replacement.ID, "the replacement's state is not touched")
	})

	t.Run("rename: old name removed, state kept", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		src := registerLifecycleSource(t, p, "rtsp://192.0.2.1/cam", "Garden")
		require.True(t, p.registry.UpdateDisplayName(src.ID, "Yard"))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)

		p.ForgetHomeAssistantEntityName(src) // src still carries the old name
		p.publishDiscoveryIfReady()

		last := lastPayloads(client.GetPublishedMessages())
		assertRemoved(t, last, speciesConfigTopic("Garden"))
		assert.NotEmpty(t, last[speciesConfigTopic("Yard")].payload, "the source is published under its new name")
		assert.NotContains(t, last, "birdnet/sources/"+src.ID, "a renamed live source keeps its retained state")
	})

	t.Run("collision winner deleted: survivor promoted, its suffixed configs removed", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		a := registerLifecycleSource(t, p, "rtsp://192.0.2.1/a", "Mic")
		b := registerLifecycleSource(t, p, "rtsp://192.0.2.1/b", "Mic")
		winner, survivor := a, b
		if b.ID < a.ID {
			winner, survivor = b, a
		}
		require.NoError(t, p.registry.Unregister(winner.ID))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)

		p.ForgetHomeAssistantSource(winner)
		p.publishDiscoveryIfReady()

		last := lastPayloads(client.GetPublishedMessages())
		assert.NotEmpty(t, last[speciesConfigTopic("Mic")].payload, "the survivor now owns the plain key")
		assertRemoved(t, last, speciesConfigTopic("Mic_"+survivor.ID))
	})

	t.Run("last stream deleted: removed without touching the bridge", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		only := registerLifecycleSource(t, p, "rtsp://192.0.2.1/only", "Garden")
		require.NoError(t, p.registry.Unregister(only.ID))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)

		p.ForgetHomeAssistantSource(only)
		p.publishDiscoveryIfReady()

		last := lastPayloads(client.GetPublishedMessages())
		assertRemoved(t, last, speciesConfigTopic("Garden"))
		assert.NotContains(t, last, "homeassistant/binary_sensor/node/status/config", "the bridge is left alone")
	})

	t.Run("re-added under the same raw ID: nothing removed", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		src := registerLifecycleSource(t, p, "rtsp://192.0.2.1/cam", "Garden")
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)

		p.ForgetHomeAssistantSource(src) // queued, but the source is live again
		p.publishDiscoveryIfReady()

		// (Its sound level state is cleared by the publish itself because sound
		// level monitoring is off in these settings; that is unrelated.)
		last := lastPayloads(client.GetPublishedMessages())
		assert.NotEmpty(t, last[speciesConfigTopic("Garden")].payload, "a live source keeps its config")
		assert.NotContains(t, last, "birdnet/sources/"+src.ID, "a live source keeps its detection state")
	})

	t.Run("broker down: kept queued, performed on the next publish", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		require.NoError(t, p.registry.Unregister(garden.ID))
		client := NewMockMQTTClient()
		client.SetConnected(false)
		p.SetMQTTClient(client)

		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady()
		assert.Empty(t, client.GetPublishedMessages())

		client.SetConnected(true)
		p.publishDiscoveryIfReady()
		assertRemoved(t, lastPayloads(client.GetPublishedMessages()), speciesConfigTopic("Garden"))
	})

	t.Run("failed removal is retried", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		require.NoError(t, p.registry.Unregister(garden.ID))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		client.SetTopicError(speciesConfigTopic("Garden"), assert.AnError)

		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady()
		p.haPendingMu.Lock()
		assert.Len(t, p.haPendingRemovals, 1, "a failed removal must stay queued")
		p.haPendingMu.Unlock()

		client.SetTopicError(speciesConfigTopic("Garden"), nil)
		p.publishDiscoveryIfReady()
		assertRemoved(t, lastPayloads(client.GetPublishedMessages()), speciesConfigTopic("Garden"))
		p.haPendingMu.Lock()
		assert.Empty(t, p.haPendingRemovals)
		p.haPendingMu.Unlock()
	})

	t.Run("HA discovery off: nothing queued", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(false))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		p.ForgetHomeAssistantSource(datastore.AudioSource{ID: "rtsp_x", DisplayName: "Garden"})
		p.haPendingMu.Lock()
		assert.Empty(t, p.haPendingRemovals)
		p.haPendingMu.Unlock()
	})

	t.Run("retire performs queued removals", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, published))

		require.NoError(t, p.registry.Unregister(garden.ID))
		p.ForgetHomeAssistantSource(garden) // queued, then HA is turned off before any publish
		next := newLifecycleSettings(false)
		useLiveSettings(t, next)
		p.RetireHomeAssistantDiscovery(t.Context(), client, next)

		last := lastPayloads(client.GetPublishedMessages())
		assertRemoved(t, last, speciesConfigTopic("Garden"))
		assertRemoved(t, last, "birdnet/sources/"+garden.ID)
		p.haPendingMu.Lock()
		assert.Empty(t, p.haPendingRemovals)
		p.haPendingMu.Unlock()
	})
}

// TestForgetHomeAssistantSource_Queue covers the queue's bounds and the
// identity removals are performed under: duplicates collapse, the queue is
// capped while the broker is unreachable, and removals target the identity the
// entities were published under.
func TestForgetHomeAssistantSource_Queue(t *testing.T) {
	t.Run("duplicates collapse and the queue is capped", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		src := datastore.AudioSource{ID: "rtsp_dup", DisplayName: "Garden"}
		p.ForgetHomeAssistantSource(src)
		p.ForgetHomeAssistantSource(src)
		p.haPendingMu.Lock()
		assert.Len(t, p.haPendingRemovals, 1, "the same removal is queued once")
		p.haPendingMu.Unlock()

		for i := range haPendingRemovalsCap + 10 {
			p.ForgetHomeAssistantSource(datastore.AudioSource{ID: "rtsp_" + strconv.Itoa(i), DisplayName: "Cam"})
		}
		p.haPendingMu.Lock()
		assert.Len(t, p.haPendingRemovals, haPendingRemovalsCap)
		p.haPendingMu.Unlock()
	})

	t.Run("failed publish keeps the taken removals queued", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		require.NoError(t, p.registry.Unregister(garden.ID))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		client.SetTopicError(speciesConfigTopic("Porch"), assert.AnError) // live source publish fails

		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady()

		p.haPendingMu.Lock()
		assert.Len(t, p.haPendingRemovals, 1, "removals taken by a failed publish must be requeued")
		p.haPendingMu.Unlock()
	})

	t.Run("a request repeated after a failed attempt is kept once", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		require.NoError(t, p.registry.Unregister(garden.ID))
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		client.SetTopicError(speciesConfigTopic("Garden"), assert.AnError)

		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady() // fails, requeued pinned to its identity
		// The same request again sits unpinned next to the pinned retry.
		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady() // fails again

		p.haPendingMu.Lock()
		assert.Len(t, p.haPendingRemovals, 1, "the pinned retry and the repeated request are one removal")
		p.haPendingMu.Unlock()
	})

	t.Run("requeued removals go first and are capped, oldest dropped", func(t *testing.T) {
		useLiveSettings(t, newLifecycleSettings(true))
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		// Queued while the failed attempt was in flight, so newer than the retries.
		newer := datastore.AudioSource{ID: "rtsp_newer", DisplayName: "Newer"}
		p.ForgetHomeAssistantSource(newer)
		items := make([]haPendingRemoval, haPendingRemovalsCap+5)
		for i := range items {
			items[i] = haPendingRemoval{source: datastore.AudioSource{ID: "rtsp_" + strconv.Itoa(i), DisplayName: "Cam"}}
		}
		p.requeueHAPendingRemovals(items)

		p.haPendingMu.Lock()
		defer p.haPendingMu.Unlock()
		require.Len(t, p.haPendingRemovals, haPendingRemovalsCap)
		assert.Equal(t, "rtsp_6", p.haPendingRemovals[0].source.ID, "the oldest retries are dropped")
		assert.Equal(t, newer, p.haPendingRemovals[len(p.haPendingRemovals)-1].source,
			"a request newer than the retries is kept, behind them")
	})

	t.Run("retire performs a repeated removal once", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, published))
		require.NoError(t, p.registry.Unregister(garden.ID))

		// A pinned retry and the same request repeated, unpinned.
		p.haDiscoveryMu.Lock()
		pinnedConfig := *p.haPublishedConfig
		p.haDiscoveryMu.Unlock()
		p.haPendingMu.Lock()
		p.haPendingRemovals = []haPendingRemoval{{source: garden, config: &pinnedConfig}, {source: garden}}
		p.haPendingMu.Unlock()

		client.SetTopicError(speciesConfigTopic("Garden"), assert.AnError)
		next := newLifecycleSettings(false)
		useLiveSettings(t, next)
		p.RetireHomeAssistantDiscovery(t.Context(), client, next)

		p.haPendingMu.Lock()
		defer p.haPendingMu.Unlock()
		assert.Len(t, p.haPendingRemovals, 1, "the pinned retry and the repeated request are one removal")
	})

	t.Run("a retried removal keeps the identity of its first attempt", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, published))

		// Same save: prefix changes and Garden is deleted; the removal fails once.
		moved := newLifecycleSettings(true)
		moved.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "otherprefix"
		useLiveSettings(t, moved)
		require.NoError(t, p.registry.Unregister(garden.ID))
		client.SetTopicError(speciesConfigTopic("Garden"), assert.AnError)
		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady() // publishes under otherprefix, removal fails

		client.SetTopicError(speciesConfigTopic("Garden"), nil)
		p.publishDiscoveryIfReady() // retry must still target the old prefix

		assertRemoved(t, lastPayloads(client.GetPublishedMessages()), speciesConfigTopic("Garden"))
	})

	t.Run("removals use the identity the entities were published under", func(t *testing.T) {
		published := newLifecycleSettings(true)
		useLiveSettings(t, published)
		p := newEmptyLifecycleProcessor()
		stopDebounce(t, p)
		garden := registerLifecycleSource(t, p, "rtsp://192.0.2.1/garden", "Garden")
		registerLifecycleSource(t, p, "rtsp://192.0.2.1/porch", "Porch")
		client := NewMockMQTTClient()
		p.SetMQTTClient(client)
		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, published))

		// One settings save both changes the prefix and deletes Garden.
		moved := newLifecycleSettings(true)
		moved.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "otherprefix"
		useLiveSettings(t, moved)
		require.NoError(t, p.registry.Unregister(garden.ID))
		p.ForgetHomeAssistantSource(garden)
		p.publishDiscoveryIfReady()

		assertRemoved(t, lastPayloads(client.GetPublishedMessages()), speciesConfigTopic("Garden"))
	})
}

// TestClearLegacyStatusTopic verifies the retained status message an older
// version left under "<base>//status" (base topic with a trailing slash) is
// cleared once, retried after a failure, and never published for a base topic
// without a trailing slash.
func TestClearLegacyStatusTopic(t *testing.T) {
	publishWithBase := func(t *testing.T, client *MockMQTTClient, p *Processor, base string) {
		t.Helper()
		s := newLifecycleSettings(true)
		s.Realtime.MQTT.Topic = base
		useLiveSettings(t, s)
		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, s))
	}
	count := func(client *MockMQTTClient, topic string) int {
		n := 0
		for _, m := range client.GetPublishedMessages() {
			if m.topic == topic {
				n++
			}
		}
		return n
	}

	t.Run("trailing slash: cleared once, retained", func(t *testing.T) {
		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		publishWithBase(t, client, p, "birdnet/")
		publishWithBase(t, client, p, "birdnet/")

		assert.Equal(t, 1, count(client, "birdnet//status"), "the legacy topic is cleared exactly once")
		assertRemoved(t, lastPayloads(client.GetPublishedMessages()), "birdnet//status")
	})

	t.Run("no trailing slash: nothing published", func(t *testing.T) {
		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		publishWithBase(t, client, p, "birdnet")

		for _, m := range client.GetPublishedMessages() {
			assert.NotEqual(t, "birdnet/status", m.topic, "the live status topic must not be cleared")
		}
	})

	t.Run("failed clear is retried", func(t *testing.T) {
		p := newLifecycleProcessor(t, "Backyard")
		client := NewMockMQTTClient()
		client.SetTopicError("birdnet//status", assert.AnError)
		publishWithBase(t, client, p, "birdnet/")
		client.SetTopicError("birdnet//status", nil)
		publishWithBase(t, client, p, "birdnet/")

		assertRemoved(t, lastPayloads(client.GetPublishedMessages()), "birdnet//status")
	})
}
