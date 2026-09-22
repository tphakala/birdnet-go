// mqtt_discovery_reconcile_test.go - Tests for the HA discovery record and the
// reconcile/retire/forget lifecycle (GitHub #4349 follow-up, round 3).
package processor

import (
	"errors"
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

// countPublishesTo returns how many times topic was published in the ordered log.
func countPublishesTo(msgs []publishedMessage, topic string) int {
	n := 0
	for _, m := range msgs {
		if m.topic == topic {
			n++
		}
	}
	return n
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

// discoveryTestSettings builds a settings snapshot with MQTT + HA discovery on.
func discoveryTestSettings() *conf.Settings {
	settings := &conf.Settings{}
	settings.Realtime.MQTT.Enabled = true
	settings.Realtime.MQTT.Topic = testMQTTTopic
	settings.Realtime.MQTT.HomeAssistant.Enabled = true
	settings.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
	settings.Main.Name = "node"
	return settings
}

// discoveryConfigFrom builds the DiscoveryConfig that publishHomeAssistantDiscovery
// would derive from settings, for pre-seeding a record.
func discoveryConfigFrom(settings *conf.Settings) mqtt.DiscoveryConfig {
	return mqtt.DiscoveryConfig{
		DiscoveryPrefix: settings.Realtime.MQTT.HomeAssistant.DiscoveryPrefix,
		BaseTopic:       settings.Realtime.MQTT.Topic,
		DeviceName:      settings.Realtime.MQTT.HomeAssistant.DeviceName,
		NodeID:          settings.Main.Name,
		Version:         settings.Version,
	}
}

// newDiscoveryTestProcessor builds a processor whose registry is reg and installs
// settings as the live global snapshot (publishHomeAssistantDiscovery reads the
// live settings under the lock). Tests using it must NOT be parallel.
func newDiscoveryTestProcessor(t *testing.T, reg *audiocore.SourceRegistry) (*Processor, *conf.Settings) {
	t.Helper()
	settings := discoveryTestSettings()
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	conftest.SetTestSettings(settings)

	p := &Processor{Settings: settings}
	p.registry = reg
	return p, settings
}

// TestPublishHomeAssistantDiscovery_StartupRaceSkips verifies that with no
// sources ever registered and no prior record, publishing is a silent no-op that
// leaves nothing behind (the GitHub #2948 startup race).
func TestPublishHomeAssistantDiscovery_StartupRaceSkips(t *testing.T) {
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

// TestPublishHomeAssistantDiscovery_TransientEmptyRegistryNoRemoval verifies that
// publishing with zero sources AFTER a record already exists removes nothing (a
// transiently empty registry during a restart must not tear down entities). This
// is the restored pre-round-2 behavior.
func TestPublishHomeAssistantDiscovery_TransientEmptyRegistryNoRemoval(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)
	p.SetMQTTClient(client)

	// A record exists from an earlier publish, but the registry is momentarily empty.
	a := datastore.AudioSource{ID: "rtsp_a", DisplayName: "Alpha"}
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config:    discoveryConfigFrom(settings),
		published: map[string]publishedSource{a.ID: {source: a, entityKey: "Alpha"}},
	}

	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	assert.Zero(t, client.GetPublishCalls(), "a transient empty registry must publish nothing (no teardown)")
	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	require.NotNil(t, rec, "record must be kept when the registry is transiently empty")
	assert.Contains(t, rec.published, a.ID)
}

// TestPublishHomeAssistantDiscovery_URLEditedSameName covers the case where a
// source's RTSP URL is edited (new raw ID, same display name): the new source is
// published under the shared key, the old (now absent) source's retained state is
// NOT cleared by the publish, and ForgetHomeAssistantSource on the old ID clears
// only its state, leaving the shared configs intact because the live source owns
// the key.
func TestPublishHomeAssistantDiscovery_URLEditedSameName(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	idC := registerDiscoveryTestSource(t, reg, "rtsp://garden/new", "Garden")

	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)
	p.SetMQTTClient(client)

	// Prior record: old source A held the "Garden" key.
	const oldID = "rtsp_old"
	oldA := datastore.AudioSource{ID: oldID, DisplayName: "Garden"}
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config:    discoveryConfigFrom(settings),
		published: map[string]publishedSource{oldID: {source: oldA, entityKey: "Garden"}},
	}

	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	state := finalRetainedState(client.GetPublishedMessages())
	const gardenSpecies = "homeassistant/sensor/node/node_Garden_species/config"
	require.Contains(t, state, gardenSpecies)
	assert.NotEmpty(t, state[gardenSpecies], "the live source must be published under the Garden key")

	// The absent old source's retained state must NOT be cleared by the publish.
	oldDetection := mqtt.SourceDetectionTopic(testMQTTTopic, oldID)
	assert.NotContains(t, state, oldDetection, "publish must not clear an absent source's retained state")

	// Config-driven deletion of the old source: clears its state, but the shared
	// Garden configs survive because the live source C owns that key.
	p.ForgetHomeAssistantSource(oldID)
	state = finalRetainedState(client.GetPublishedMessages())
	require.Contains(t, state, oldDetection)
	assert.Empty(t, state[oldDetection], "ForgetHomeAssistantSource must clear the deleted source's retained state")
	assert.NotEmpty(t, state[gardenSpecies], "Garden configs must survive because live source C owns the key")

	// The live source C is still recorded under the Garden key.
	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	require.NotNil(t, rec)
	require.Contains(t, rec.published, idC)
	assert.Equal(t, "Garden", rec.published[idC].entityKey)
}

// TestPublishHomeAssistantDiscovery_CollisionWinnerDepartsKeepsSurvivor verifies
// that when the source that held the plain base key of a colliding pair departs
// (a restart, so it is only absent from the snapshot), the survivor keeps its own
// suffixed key and nothing is removed.
func TestPublishHomeAssistantDiscovery_CollisionWinnerDepartsKeepsSurvivor(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	idB := registerDiscoveryTestSource(t, reg, "rtsp://mic/b", "Mic")

	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)
	p.SetMQTTClient(client)

	// Prior record: A held the plain "Mic" key, B held the suffixed key.
	const aID = "rtsp_0000" // sorts before any generated RTSP id
	bKey := "Mic_" + mqtt.SanitizeID(idB)
	aSrc := datastore.AudioSource{ID: aID, DisplayName: "Mic"}
	bSrc := datastore.AudioSource{ID: idB, DisplayName: "Mic"}
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config: discoveryConfigFrom(settings),
		published: map[string]publishedSource{
			aID: {source: aSrc, entityKey: "Mic"},
			idB: {source: bSrc, entityKey: bKey},
		},
	}

	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	state := finalRetainedState(client.GetPublishedMessages())

	// Survivor B keeps its suffixed key: its active sensors are published non-empty.
	for _, sensor := range []string{"species", "confidence", "scientific_name"} {
		topic := "homeassistant/sensor/node/node_" + bKey + "_" + sensor + "/config"
		require.Contains(t, state, topic)
		assert.NotEmpty(t, state[topic], "survivor must keep its own key: %s", topic)
	}

	// B did NOT take over the plain "Mic" key (that would move its HA history).
	assert.NotContains(t, state, "homeassistant/sensor/node/node_Mic_species/config",
		"survivor must not switch to the departed source's plain key")

	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	require.NotNil(t, rec)
	assert.Equal(t, bKey, rec.published[idB].entityKey, "survivor keeps its recorded key")
}

// TestPublishHomeAssistantDiscovery_NewSameNameSmallerIDJoins verifies that when a
// new same-name source with a lexicographically smaller raw ID joins an incumbent,
// the incumbent keeps the plain base key and the newcomer is suffixed, with no
// removal of the plain key.
func TestPublishHomeAssistantDiscovery_NewSameNameSmallerIDJoins(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	id1 := registerDiscoveryTestSource(t, reg, "rtsp://mic/one", "Mic")
	id2 := registerDiscoveryTestSource(t, reg, "rtsp://mic/two", "Mic")

	// Make the incumbent the lexicographically LARGER id, so without incumbency the
	// (smaller) newcomer would win the plain base key.
	incumbentID, newcomerID := id1, id2
	if incumbentID < newcomerID {
		incumbentID, newcomerID = newcomerID, incumbentID
	}
	require.Less(t, newcomerID, incumbentID)

	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)
	p.SetMQTTClient(client)

	incSrc := datastore.AudioSource{ID: incumbentID, DisplayName: "Mic"}
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config:    discoveryConfigFrom(settings),
		published: map[string]publishedSource{incumbentID: {source: incSrc, entityKey: "Mic"}},
	}

	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

	state := finalRetainedState(client.GetPublishedMessages())

	// Incumbent keeps the plain "Mic" key.
	assert.NotEmpty(t, state["homeassistant/sensor/node/node_Mic_species/config"],
		"incumbent must keep the plain base key")
	// Newcomer (smaller id) is suffixed, not given the plain key.
	newcomerSpecies := "homeassistant/sensor/node/node_Mic_" + mqtt.SanitizeID(newcomerID) + "_species/config"
	require.Contains(t, state, newcomerSpecies)
	assert.NotEmpty(t, state[newcomerSpecies], "newcomer must get a suffixed key even though its id sorts first")

	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	require.NotNil(t, rec)
	assert.Equal(t, "Mic", rec.published[incumbentID].entityKey)
	assert.Equal(t, "Mic_"+mqtt.SanitizeID(newcomerID), rec.published[newcomerID].entityKey)
}

// TestPublishHomeAssistantDiscovery_IdentityDrift covers the identity-change paths
// handled at publish time.
func TestPublishHomeAssistantDiscovery_IdentityDrift(t *testing.T) {
	t.Run("prefix change removes under old prefix then republishes under new", func(t *testing.T) {
		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		idA := registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

		client := NewMockMQTTClient()
		p, settings := newDiscoveryTestProcessor(t, reg)
		p.SetMQTTClient(client)

		oldCfg := discoveryConfigFrom(settings) // prefix homeassistant
		aSrc := datastore.AudioSource{ID: idA, DisplayName: "Alpha"}
		p.haDiscoveryRecord = &haDiscoveryRecord{
			config:    oldCfg,
			published: map[string]publishedSource{idA: {source: aSrc, entityKey: "Alpha"}},
		}
		settings.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "ha2"
		conftest.SetTestSettings(settings)

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

		state := finalRetainedState(client.GetPublishedMessages())
		require.Contains(t, state, "homeassistant/sensor/node/node_Alpha_species/config")
		assert.Empty(t, state["homeassistant/sensor/node/node_Alpha_species/config"], "old-prefix config must be removed")
		require.Contains(t, state, "homeassistant/binary_sensor/node/status/config")
		assert.Empty(t, state["homeassistant/binary_sensor/node/status/config"], "old-prefix bridge must be removed")
		assert.NotEmpty(t, state["ha2/sensor/node/node_Alpha_species/config"], "entities must be republished under the new prefix")

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		require.NotNil(t, rec)
		assert.Equal(t, "ha2", rec.config.DiscoveryPrefix)
	})

	t.Run("node change removes under old node then republishes under new", func(t *testing.T) {
		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		idA := registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

		client := NewMockMQTTClient()
		p, settings := newDiscoveryTestProcessor(t, reg)
		p.SetMQTTClient(client)

		aSrc := datastore.AudioSource{ID: idA, DisplayName: "Alpha"}
		p.haDiscoveryRecord = &haDiscoveryRecord{
			config:    discoveryConfigFrom(settings), // node "node"
			published: map[string]publishedSource{idA: {source: aSrc, entityKey: "Alpha"}},
		}
		settings.Main.Name = "node2"
		conftest.SetTestSettings(settings)

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

		state := finalRetainedState(client.GetPublishedMessages())
		require.Contains(t, state, "homeassistant/binary_sensor/node/status/config")
		assert.Empty(t, state["homeassistant/binary_sensor/node/status/config"], "old-node bridge must be removed")
		assert.NotEmpty(t, state["homeassistant/binary_sensor/node2/status/config"], "bridge must be republished under the new node")

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		require.NotNil(t, rec)
		assert.Equal(t, "node2", rec.config.NodeID)
	})

	t.Run("base-only change clears old base state, keeps configs", func(t *testing.T) {
		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		idA := registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

		client := NewMockMQTTClient()
		p, settings := newDiscoveryTestProcessor(t, reg) // base testMQTTTopic
		p.SetMQTTClient(client)

		oldCfg := discoveryConfigFrom(settings)
		aSrc := datastore.AudioSource{ID: idA, DisplayName: "Alpha"}
		p.haDiscoveryRecord = &haDiscoveryRecord{
			config:    oldCfg,
			published: map[string]publishedSource{idA: {source: aSrc, entityKey: "Alpha"}},
		}
		settings.Realtime.MQTT.Topic = "newbase"
		conftest.SetTestSettings(settings)

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

		state := finalRetainedState(client.GetPublishedMessages())
		// Old base per-source state and status cleared.
		oldDetection := mqtt.SourceDetectionTopic(testMQTTTopic, idA)
		require.Contains(t, state, oldDetection)
		assert.Empty(t, state[oldDetection], "old-base per-source state must be cleared")
		require.Contains(t, state, mqtt.StatusTopic(testMQTTTopic))
		assert.Empty(t, state[mqtt.StatusTopic(testMQTTTopic)], "old-base status must be cleared")
		// Config topics are NOT removed (they do not depend on the base topic).
		assert.NotEmpty(t, state["homeassistant/sensor/node/node_Alpha_species/config"], "configs must not be removed on a base-only change")

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		require.NotNil(t, rec)
		assert.Equal(t, "newbase", rec.config.BaseTopic)
	})

	t.Run("broker change is not an identity change: nothing removed", func(t *testing.T) {
		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		idA := registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

		client := NewMockMQTTClient()
		p, settings := newDiscoveryTestProcessor(t, reg)
		p.SetMQTTClient(client)
		settings.Realtime.MQTT.Broker = "tcp://old:1883"
		conftest.SetTestSettings(settings)

		aSrc := datastore.AudioSource{ID: idA, DisplayName: "Alpha"}
		p.haDiscoveryRecord = &haDiscoveryRecord{
			config:    discoveryConfigFrom(settings),
			published: map[string]publishedSource{idA: {source: aSrc, entityKey: "Alpha"}},
		}
		settings.Realtime.MQTT.Broker = "tcp://new:1883"
		conftest.SetTestSettings(settings)

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

		state := finalRetainedState(client.GetPublishedMessages())
		// A broker-only change never clears the status topic or removes the bridge.
		assert.NotContains(t, state, mqtt.StatusTopic(testMQTTTopic), "broker change must not clear the status topic")
		assert.NotEmpty(t, state["homeassistant/binary_sensor/node/status/config"], "broker change must not remove the bridge")
		assert.NotEmpty(t, state["homeassistant/sensor/node/node_Alpha_species/config"], "broker change must not remove configs")
	})
}

// TestPublishHomeAssistantDiscovery_SnapshotInsideLock verifies that the live
// settings read under the lock win over a stale caller snapshot: if HA discovery
// was disabled after the caller captured its settings, publishing does nothing.
func TestPublishHomeAssistantDiscovery_SnapshotInsideLock(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

	client := NewMockMQTTClient()
	p := &Processor{}
	p.registry = reg
	p.SetMQTTClient(client)

	// Caller captured HA-enabled settings...
	captured := discoveryTestSettings()
	// ...but the live settings have since disabled HA discovery.
	live := discoveryTestSettings()
	live.Realtime.MQTT.HomeAssistant.Enabled = false
	prev := conf.GetSettings()
	t.Cleanup(func() { conftest.SetTestSettings(prev) })
	conftest.SetTestSettings(live)

	require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, captured))

	assert.Zero(t, client.GetPublishCalls(), "a stale enabled snapshot must not publish when live settings disable HA discovery")
}

// TestPublishHomeAssistantDiscovery_LegacyStatusTopic verifies R11: on a
// trailing-slash base the pre-fix "<raw>//status" topic is cleared exactly once,
// and on a base with no trailing slash nothing is published to the status topic.
func TestPublishHomeAssistantDiscovery_LegacyStatusTopic(t *testing.T) {
	t.Run("trailing slash base clears legacy status once", func(t *testing.T) {
		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

		client := NewMockMQTTClient()
		p, settings := newDiscoveryTestProcessor(t, reg)
		settings.Realtime.MQTT.Topic = "birdnet/"
		conftest.SetTestSettings(settings)

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))
		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

		msgs := client.GetPublishedMessages()
		assert.Equal(t, 1, countPublishesTo(msgs, "birdnet//status"),
			"legacy status topic must be cleared exactly once across publishes")
	})

	t.Run("no trailing slash base never clears the status topic", func(t *testing.T) {
		reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
		registerDiscoveryTestSource(t, reg, "rtsp://alpha", "Alpha")

		client := NewMockMQTTClient()
		p, settings := newDiscoveryTestProcessor(t, reg)
		settings.Realtime.MQTT.Topic = "birdnet"
		conftest.SetTestSettings(settings)

		require.NoError(t, p.publishHomeAssistantDiscovery(t.Context(), client, settings))

		state := finalRetainedState(client.GetPublishedMessages())
		assert.NotContains(t, state, "birdnet/status", "no legacy status clear when the base has no trailing slash")
	})
}

// newRetireTestProcessor builds a processor with a pre-set discovery record and a
// connected mock client, as if a prior publish had already happened.
func newRetireTestProcessor(t *testing.T, sources []datastore.AudioSource, cfg *mqtt.DiscoveryConfig) (*Processor, *MockMQTTClient) {
	t.Helper()
	client := NewMockMQTTClient()
	p := &Processor{}
	p.SetMQTTClient(client)
	keys := mqtt.SourceEntityKeys(sources, nil)
	published := make(map[string]publishedSource, len(sources))
	for _, s := range sources {
		published[s.ID] = publishedSource{source: s, entityKey: keys[s.ID]}
	}
	p.haDiscoveryRecord = &haDiscoveryRecord{config: *cfg, published: published}
	return p, client
}

// TestRetireHomeAssistantDiscovery verifies the reconfigure-time retirement, which
// fires only when HA discovery is turned OFF while MQTT stays enabled.
func TestRetireHomeAssistantDiscovery(t *testing.T) {
	t.Parallel()

	oldCfg := mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "node",
		Version:         "v",
	}
	sources := []datastore.AudioSource{{ID: "rtsp_a", DisplayName: "Alpha"}}

	newSettings := func(mutate func(*conf.Settings)) *conf.Settings {
		s := &conf.Settings{}
		s.Main.Name = "node"
		s.Realtime.MQTT.Enabled = true
		s.Realtime.MQTT.Topic = "birdnet"
		s.Realtime.MQTT.HomeAssistant.Enabled = false // HA discovery being turned off
		s.Realtime.MQTT.HomeAssistant.DiscoveryPrefix = "homeassistant"
		if mutate != nil {
			mutate(s)
		}
		return s
	}

	t.Run("HA disabled removes under old topics and clears record", func(t *testing.T) {
		t.Parallel()
		p, client := newRetireTestProcessor(t, sources, &oldCfg)

		p.RetireHomeAssistantDiscovery(t.Context(), newSettings(nil))

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

	t.Run("HA still enabled is a no-op and keeps record", func(t *testing.T) {
		t.Parallel()
		p, client := newRetireTestProcessor(t, sources, &oldCfg)

		p.RetireHomeAssistantDiscovery(t.Context(), newSettings(func(s *conf.Settings) {
			s.Realtime.MQTT.HomeAssistant.Enabled = true
		}))

		assert.Zero(t, client.GetPublishCalls(), "must not retire while HA discovery stays enabled")
		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.NotNil(t, rec, "record must be kept when HA discovery stays enabled")
	})

	t.Run("MQTT disabled entirely does not publish and keeps record", func(t *testing.T) {
		t.Parallel()
		p, client := newRetireTestProcessor(t, sources, &oldCfg)

		p.RetireHomeAssistantDiscovery(t.Context(), newSettings(func(s *conf.Settings) {
			s.Realtime.MQTT.Enabled = false
		}))

		assert.Zero(t, client.GetPublishCalls(), "disabling MQTT must not publish removals")
		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.NotNil(t, rec, "record must be kept when MQTT is disabled entirely")
	})

	t.Run("disconnected client keeps record then a reconnect retire removes", func(t *testing.T) {
		t.Parallel()
		p, client := newRetireTestProcessor(t, sources, &oldCfg)
		client.SetConnected(false)

		p.RetireHomeAssistantDiscovery(t.Context(), newSettings(nil))
		assert.Zero(t, client.GetPublishCalls(), "no connected client: nothing published")
		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		require.NotNil(t, rec, "record must be kept while the client is disconnected")

		// The new client connects to the same broker; the second retire cleans up.
		client.SetConnected(true)
		p.RetireHomeAssistantDiscovery(t.Context(), newSettings(nil))
		assert.Positive(t, client.GetPublishCalls(), "the reconnect retire must publish the removals")
		p.haDiscoveryMu.Lock()
		rec = p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		assert.Nil(t, rec, "record must be cleared once the reconnect retire succeeds")
	})

	t.Run("removal error keeps the record", func(t *testing.T) {
		t.Parallel()
		p, client := newRetireTestProcessor(t, sources, &oldCfg)
		client.SetTopicError("homeassistant/sensor/node/node_Alpha_species/config", errors.New("broker rejected publish"))

		p.RetireHomeAssistantDiscovery(t.Context(), newSettings(nil))

		p.haDiscoveryMu.Lock()
		rec := p.haDiscoveryRecord
		p.haDiscoveryMu.Unlock()
		require.NotNil(t, rec, "a failed removal must keep the record so a later retire can retry")
	})
}

// TestSetRegistry_SourceAddedSchedulesPublish verifies that a SourceAdded event
// arms the debounced discovery publish.
func TestSetRegistry_SourceAddedSchedulesPublish(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	p := &Processor{}
	p.SetRegistry(reg)

	registerDiscoveryTestSource(t, reg, "rtsp://alpha/stream", "Alpha")

	p.discoveryDebounceMu.Lock()
	timer := p.discoveryDebounce
	if timer != nil {
		timer.Stop()
	}
	p.discoveryDebounceMu.Unlock()
	assert.NotNil(t, timer, "SourceAdded must schedule a debounced discovery publish")
}

// TestSetRegistry_SourceRemovedDoesNotSchedule verifies that a SourceRemoved event
// (which may be a transient restart) does NOT schedule a discovery publish.
func TestSetRegistry_SourceRemovedDoesNotSchedule(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	// Register BEFORE the listener is installed, so only SourceRemoved reaches it.
	id := registerDiscoveryTestSource(t, reg, "rtsp://alpha/stream", "Alpha")

	p := &Processor{}
	p.SetRegistry(reg)

	require.NoError(t, reg.Unregister(id))

	p.discoveryDebounceMu.Lock()
	timer := p.discoveryDebounce
	if timer != nil {
		timer.Stop()
	}
	p.discoveryDebounceMu.Unlock()
	assert.Nil(t, timer, "SourceRemoved must NOT schedule a discovery publish")
}

// TestSetRegistry_SourceStateChangedDoesNotSchedule verifies that a runtime
// state change does NOT schedule a discovery publish.
func TestSetRegistry_SourceStateChangedDoesNotSchedule(t *testing.T) {
	t.Parallel()

	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	id := registerDiscoveryTestSource(t, reg, "rtsp://alpha/stream", "Alpha")

	p := &Processor{}
	p.SetRegistry(reg)

	require.NoError(t, reg.UpdateState(id, audiocore.SourceRunning))

	p.discoveryDebounceMu.Lock()
	timer := p.discoveryDebounce
	if timer != nil {
		timer.Stop()
	}
	p.discoveryDebounceMu.Unlock()
	assert.Nil(t, timer, "SourceStateChanged must NOT schedule a discovery publish")
}

// TestRepublishHomeAssistantDiscovery_Schedules verifies R8: the runtime republish
// entry point schedules a debounced publish instead of publishing synchronously,
// so it never blocks the control monitor.
func TestRepublishHomeAssistantDiscovery_Schedules(t *testing.T) {
	t.Parallel()

	p := &Processor{}
	p.RepublishHomeAssistantDiscovery()

	p.discoveryDebounceMu.Lock()
	timer := p.discoveryDebounce
	if timer != nil {
		timer.Stop()
	}
	p.discoveryDebounceMu.Unlock()
	assert.NotNil(t, timer, "RepublishHomeAssistantDiscovery must schedule a debounced publish, not publish synchronously")
}

// TestCleanupDefaultDiscovery_DoesNotRemoveBridge verifies R9: cleaning up the
// legacy "default" source removes only its config and state topics, never the
// shared bridge (which would flap the Status entity on every startup).
func TestCleanupDefaultDiscovery_DoesNotRemoveBridge(t *testing.T) {
	t.Parallel()

	client := NewMockMQTTClient()
	publisher := mqtt.NewDiscoveryPublisher(client, &mqtt.DiscoveryConfig{
		DiscoveryPrefix: "homeassistant",
		BaseTopic:       "birdnet",
		DeviceName:      "BirdNET-Go",
		NodeID:          "node",
		Version:         "v",
	})

	cleanupDefaultDiscovery(t.Context(), publisher)

	state := finalRetainedState(client.GetPublishedMessages())
	assert.NotContains(t, state, "homeassistant/binary_sensor/node/status/config",
		"cleanup of the legacy default source must not touch the bridge")
	assert.Contains(t, state, "homeassistant/sensor/node/node_Default_species/config",
		"cleanup must still empty the legacy default source's config topics")
	assert.Empty(t, state["homeassistant/sensor/node/node_Default_species/config"],
		"the legacy default source's config topic must be emptied")
}

// TestRegisterHomeAssistantDiscovery_HandlerUsesLiveSettings verifies that the
// registered OnConnect handler reads the live settings, not the settings captured
// at registration: it skips publishing when HA discovery has since been disabled,
// and publishes under the live config when it is still enabled.
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

// TestForgetHomeAssistantSource_RemovesSoleSource verifies that deleting the only
// source removes its config and state topics and clears the record.
func TestForgetHomeAssistantSource_RemovesSoleSource(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	client := NewMockMQTTClient()
	p, settings := newDiscoveryTestProcessor(t, reg)
	p.SetMQTTClient(client)

	a := datastore.AudioSource{ID: "rtsp_a", DisplayName: "Alpha"}
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config:    discoveryConfigFrom(settings),
		published: map[string]publishedSource{a.ID: {source: a, entityKey: "Alpha"}},
	}

	p.ForgetHomeAssistantSource(a.ID)

	state := finalRetainedState(client.GetPublishedMessages())
	require.Contains(t, state, "homeassistant/sensor/node/node_Alpha_species/config")
	assert.Empty(t, state["homeassistant/sensor/node/node_Alpha_species/config"], "deleted source's config must be removed")
	require.Contains(t, state, mqtt.SourceDetectionTopic(testMQTTTopic, a.ID))
	assert.Empty(t, state[mqtt.SourceDetectionTopic(testMQTTTopic, a.ID)], "deleted source's state must be cleared")

	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	assert.Nil(t, rec, "record must be cleared once its last source is forgotten")
}

// TestForgetHomeAssistantSource_DisconnectedKeepsRecord verifies that a deletion
// while the client is disconnected removes nothing and keeps the record.
func TestForgetHomeAssistantSource_DisconnectedKeepsRecord(t *testing.T) {
	reg := audiocore.NewSourceRegistry(audiocore.GetLogger())
	client := NewMockMQTTClient()
	client.SetConnected(false)
	p, settings := newDiscoveryTestProcessor(t, reg)
	p.SetMQTTClient(client)

	a := datastore.AudioSource{ID: "rtsp_a", DisplayName: "Alpha"}
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config:    discoveryConfigFrom(settings),
		published: map[string]publishedSource{a.ID: {source: a, entityKey: "Alpha"}},
	}

	p.ForgetHomeAssistantSource(a.ID)

	assert.Zero(t, client.GetPublishCalls(), "a disconnected client must not publish removals")
	p.haDiscoveryMu.Lock()
	rec := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	require.NotNil(t, rec, "record must be kept while the client is disconnected")
	assert.Contains(t, rec.published, a.ID)
}
