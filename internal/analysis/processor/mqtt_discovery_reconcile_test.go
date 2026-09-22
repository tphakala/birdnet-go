// mqtt_discovery_reconcile_test.go - Tests for the discovery record and cleanup
// diff that removes entities and retained state for sources that disappear
// (GitHub #4349 follow-up).
package processor

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/conf/conftest"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

// finalRetainedState collapses an ordered message log into the last payload seen
// per topic, which is the retained state a subscriber would end up with.
func finalRetainedState(msgs []publishedMessage) map[string]string {
	state := make(map[string]string, len(msgs))
	for _, m := range msgs {
		state[m.topic] = m.payload
	}
	return state
}

// registerDiscoveryTestSource registers an RTSP source with the given display
// name and returns its generated ID.
func registerDiscoveryTestSource(t *testing.T, reg *audiocore.SourceRegistry, connection, displayName string) string {
	t.Helper()
	src, err := reg.Register(&audiocore.SourceConfig{
		Type:             audiocore.SourceTypeRTSP,
		ConnectionString: connection,
		DisplayName:      displayName,
		SampleRate:       48000,
		BitDepth:         16,
		Channels:         1,
	})
	require.NoError(t, err)
	return src.ID
}

func newDiscoveryTestProcessor(t *testing.T, reg *audiocore.SourceRegistry) (*Processor, *conf.Settings) {
	t.Helper()
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic
	settings.Realtime.MQTT.HomeAssistant.Enabled = true
	settings.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
	settings.Main.Name = "node"

	p := &Processor{Settings: settings}
	p.registry = reg
	return p, settings
}

// TestPublishHomeAssistantDiscovery_RemovesDepartedSource verifies that after
// publishing A+B then A only, source B's config topics and per-source state
// topics receive empty retained publishes (cleanup), while A stays published.
func TestPublishHomeAssistantDiscovery_RemovesDepartedSource(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	idA := registerDiscoveryTestSource(t, reg, "rtsp://alpha/stream", "Alpha")
	idB := registerDiscoveryTestSource(t, reg, "rtsp://bravo/stream", "Bravo")

	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)

	// Publish A + B.
	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	// Remove B and republish (A only).
	require.NoError(t, reg.Unregister(idB))
	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	state := finalRetainedState(client.GetPublishedMessages())

	// B's entity key is "Bravo" (sole source in its group). Its config topics and
	// state topics must all end empty (removed).
	const bKey = "Bravo"
	for _, sensorType := range mqtt.AllSensorTypes {
		topic := "homeassistant/sensor/node/node_" + bKey + "_" + sensorType + "/config"
		require.Contains(t, state, topic, "B config topic %s never published", topic)
		assert.Empty(t, state[topic], "B config topic %s must be emptied", topic)
	}
	bDetection := mqtt.SourceDetectionTopic(testMQTTTopic, idB)
	bSoundLevel := mqtt.SourceSoundLevelTopic(testMQTTTopic, idB)
	require.Contains(t, state, bDetection)
	assert.Empty(t, state[bDetection], "B detection state topic must be emptied")
	require.Contains(t, state, bSoundLevel)
	assert.Empty(t, state[bSoundLevel], "B sound level state topic must be emptied")

	// A stays published: its species config is non-empty.
	aSpecies := "homeassistant/sensor/node/node_Alpha_species/config"
	require.Contains(t, state, aSpecies)
	assert.NotEmpty(t, state[aSpecies], "A species config must remain published")

	// The record now holds A only.
	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	require.NotNil(t, rec)
	require.Len(t, rec.sources, 1)
	assert.Equal(t, idA, rec.sources[0].ID)
}

// TestPublishHomeAssistantDiscovery_RemovesEverythingWhenNoSources verifies that
// publishing A then republishing with zero sources removes A's entities, A's
// state topics, and the bridge, and clears the record.
func TestPublishHomeAssistantDiscovery_RemovesEverythingWhenNoSources(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	idA := registerDiscoveryTestSource(t, reg, "rtsp://alpha/stream", "Alpha")

	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)

	// Publish A.
	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	// Remove A and republish with zero sources.
	require.NoError(t, reg.Unregister(idA))
	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	state := finalRetainedState(client.GetPublishedMessages())

	// Bridge removed.
	const bridgeTopic = "homeassistant/binary_sensor/node/status/config"
	require.Contains(t, state, bridgeTopic)
	assert.Empty(t, state[bridgeTopic], "bridge config must be emptied")

	// A's config and state topics removed.
	aSpecies := "homeassistant/sensor/node/node_Alpha_species/config"
	require.Contains(t, state, aSpecies)
	assert.Empty(t, state[aSpecies], "A species config must be emptied")
	aDetection := mqtt.SourceDetectionTopic(testMQTTTopic, idA)
	require.Contains(t, state, aDetection)
	assert.Empty(t, state[aDetection], "A detection state topic must be emptied")

	// Record cleared.
	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	assert.Nil(t, rec, "record must be cleared once all sources are gone")
}

// TestPublishHomeAssistantDiscovery_StartupRaceSkips verifies that with no
// sources ever registered and no prior record, publishing is a silent no-op that
// leaves nothing behind (the GitHub #2948 startup race).
func TestPublishHomeAssistantDiscovery_StartupRaceSkips(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)

	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	assert.Empty(t, client.GetPublishedMessages(), "startup race must publish nothing")
	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	assert.Nil(t, rec)
}

// TestSetRegistry_SourceRemovedSchedulesPublish verifies that a SourceRemoved
// event schedules a discovery republish so departed entities get cleaned up.
func TestSetRegistry_SourceRemovedSchedulesPublish(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	id := registerDiscoveryTestSource(t, reg, "rtsp://alpha/stream", "Alpha")

	p := &Processor{}
	p.SetRegistry(reg)

	// Removing a source must arm the debounce timer.
	require.NoError(t, reg.Unregister(id))

	p.discoveryDebounceMu.Lock()
	timer := p.discoveryDebounce
	if timer != nil {
		timer.Stop() // prevent the background publish from firing during the test
	}
	p.discoveryDebounceMu.Unlock()
	assert.NotNil(t, timer, "SourceRemoved must schedule a debounced discovery publish")
}

// newRetireTestProcessor builds a processor with a pre-set discovery record and a
// connected mock client, as if a prior publish had already happened.
func newRetireTestProcessor(t *testing.T, sources []datastore.AudioSource, cfg *mqtt.DiscoveryConfig, broker string) (*Processor, *MockMQTTClient) {
	t.Helper()
	client := NewMockMQTTClient()
	p := &Processor{}
	p.SetMQTTClient(client)
	p.haDiscoveryRecord = &haDiscoveryRecord{config: *cfg, broker: broker, sources: sources}
	return p, client
}

// TestRetireHomeAssistantDiscovery verifies the reconfigure-time retirement:
// removal fires (under the OLD topics) when HA discovery is turned off or the
// discovery identity changes, and is a no-op when nothing changed or MQTT is
// being disabled entirely.
func TestRetireHomeAssistantDiscovery(t *testing.T) {
	t.Parallel()

	oldCfg := mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "node",
		Version:         "v",
	}
	const oldBroker = "tcp://old:1883"
	sources := []datastore.AudioSource{{ID: "rtsp_a", DisplayName: "Alpha"}}

	newSettings := func(mutate func(*conf.Settings)) *conf.Settings {
		s := &conf.Settings{}
		s.Main.Name = "node"
		s.Realtime.MQTT.Enabled = true
		s.Realtime.MQTT.Topic = "birdnet"
		s.Realtime.MQTT.Broker = oldBroker
		s.Realtime.MQTT.HomeAssistant.Enabled = true
		s.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
		if mutate != nil {
			mutate(s)
		}
		return s
	}

	t.Run("HA disabled removes under old topics and clears record", func(t *testing.T) {
		p, client := newRetireTestProcessor(t, sources, &oldCfg, oldBroker)
		next := newSettings(func(s *conf.Settings) { s.Realtime.MQTT.HomeAssistant.Enabled = false })

		p.RetireHomeAssistantDiscovery(t.Context(), next)

		state := finalRetainedState(client.GetPublishedMessages())
		require.Contains(t, state, "homeassistant/binary_sensor/node/status/config")
		assert.Empty(t, state["homeassistant/binary_sensor/node/status/config"], "bridge must be removed")
		require.Contains(t, state, mqtt.SourceDetectionTopic("birdnet", "rtsp_a"))
		assert.Empty(t, state[mqtt.SourceDetectionTopic("birdnet", "rtsp_a")])

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.Nil(t, rec, "record must be cleared after retirement")
	})

	t.Run("base topic change removes under old base", func(t *testing.T) {
		p, client := newRetireTestProcessor(t, sources, &oldCfg, oldBroker)
		next := newSettings(func(s *conf.Settings) { s.Realtime.MQTT.Topic = "newbase" })

		p.RetireHomeAssistantDiscovery(t.Context(), next)

		state := finalRetainedState(client.GetPublishedMessages())
		assert.Contains(t, state, mqtt.SourceDetectionTopic("birdnet", "rtsp_a"), "removal must use the OLD base topic")
		assert.NotContains(t, state, mqtt.SourceDetectionTopic("newbase", "rtsp_a"), "removal must not use the NEW base topic")

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.Nil(t, rec)
	})

	t.Run("no change does not publish and keeps record", func(t *testing.T) {
		p, client := newRetireTestProcessor(t, sources, &oldCfg, oldBroker)
		next := newSettings(nil)

		p.RetireHomeAssistantDiscovery(t.Context(), next)

		assert.Zero(t, client.GetPublishCalls(), "unchanged identity must not publish")
		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.NotNil(t, rec, "record must be kept when nothing changed")
	})

	t.Run("MQTT disabled does not publish and keeps record", func(t *testing.T) {
		p, client := newRetireTestProcessor(t, sources, &oldCfg, oldBroker)
		next := newSettings(func(s *conf.Settings) { s.Realtime.MQTT.Enabled = false })

		p.RetireHomeAssistantDiscovery(t.Context(), next)

		assert.Zero(t, client.GetPublishCalls(), "disabling MQTT must not publish removals")
		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.NotNil(t, rec, "record must be kept when MQTT is disabled entirely")
	})
}

// TestRegisterHomeAssistantDiscovery_HandlerUsesLiveSettings verifies that the
// registered OnConnect handler reads the live settings, not the settings
// captured at registration: it skips publishing when HA discovery has since been
// disabled, and publishes under the live config when it is still enabled.
func TestRegisterHomeAssistantDiscovery_HandlerUsesLiveSettings(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	newCaptured := func() *conf.Settings {
		s := &conf.Settings{}
		s.Main.Name = "node"
		s.Realtime.MQTT.Enabled = true
		s.Realtime.MQTT.Topic = "birdnet"
		s.Realtime.MQTT.HomeAssistant.Enabled = true
		s.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
		return s
	}

	t.Run("skips when HA discovery disabled in live settings", func(t *testing.T) {
		prev := conf.GetSettings()
		t.Cleanup(func() { conftest.SetTestSettings(prev) })

		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		registerDiscoveryTestSource(t, reg, "rtsp://a/stream", "Alpha")

		captured := newCaptured()
		p := &Processor{Settings: captured}
		p.registry = reg

		client := NewMockMQTTClient()
		p.registerHomeAssistantDiscovery(client, captured)

		handler := client.OnConnectHandler()
		require.NotNil(t, handler, "OnConnect handler must be registered")

		// Live settings: HA discovery now disabled.
		live := newCaptured()
		live.Realtime.MQTT.HomeAssistant.Enabled = false
		conftest.SetTestSettings(live)

		handler()

		assert.Zero(t, client.GetPublishCalls(), "handler must skip when live HA discovery is disabled")
	})

	t.Run("publishes under live settings", func(t *testing.T) {
		prev := conf.GetSettings()
		t.Cleanup(func() { conftest.SetTestSettings(prev) })

		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		registerDiscoveryTestSource(t, reg, "rtsp://a/stream", "Alpha")

		captured := newCaptured() // base "birdnet"
		p := &Processor{Settings: captured}
		p.registry = reg

		client := NewMockMQTTClient()
		p.registerHomeAssistantDiscovery(client, captured)

		handler := client.OnConnectHandler()
		require.NotNil(t, handler)

		// Live settings: HA still enabled but a different base topic.
		live := newCaptured()
		live.Realtime.MQTT.Topic = "livebase"
		conftest.SetTestSettings(live)

		handler()

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		require.NotNil(t, rec, "handler must publish when HA discovery is enabled")
		assert.Equal(t, "livebase", rec.config.BaseTopic, "handler must publish under the LIVE base topic, not the captured one")
	})
}

// TestRepublishHomeAssistantDiscovery_TogglesSoundLevelSensor verifies the
// runtime entry point used when sound level monitoring toggles: republishing
// removes the per-source Sound Level sensor when disabled and adds it when
// enabled (combined with the discovery publisher's B8 behavior).
func TestRepublishHomeAssistantDiscovery_TogglesSoundLevelSensor(t *testing.T) {
	// Not parallel: conftest.SetTestSettings mutates package-global state.
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	registerDiscoveryTestSource(t, reg, "rtsp://a/stream", "Alpha")

	client := NewMockMQTTClient()
	p := &Processor{}
	p.registry = reg
	p.SetMQTTClient(client)

	settings := func(soundLevel bool) *conf.Settings {
		s := &conf.Settings{}
		s.Main.Name = "node"
		s.Realtime.MQTT.Enabled = true
		s.Realtime.MQTT.Topic = "birdnet"
		s.Realtime.MQTT.HomeAssistant.Enabled = true
		s.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
		s.Realtime.Audio.SoundLevel.Enabled = soundLevel
		return s
	}

	const soundLevelConfig = "homeassistant/sensor/node/node_Alpha_sound_level/config"

	// Sound level OFF: republish removes the sensor (empty retained config).
	conftest.SetTestSettings(settings(false))
	p.RepublishHomeAssistantDiscovery()
	state := finalRetainedState(client.GetPublishedMessages())
	require.Contains(t, state, soundLevelConfig)
	assert.Empty(t, state[soundLevelConfig], "sound level sensor must be removed while monitoring is off")

	// Sound level ON: republish adds the sensor (non-empty config).
	conftest.SetTestSettings(settings(true))
	p.RepublishHomeAssistantDiscovery()
	state = finalRetainedState(client.GetPublishedMessages())
	assert.NotEmpty(t, state[soundLevelConfig], "sound level sensor must be added when monitoring is enabled")
}
