// mqtt.go: MQTT-related functionality for the processor
package processor

import (
	"context"
	"slices"
	"time"

	"github.com/tphakala/birdnet-go/internal/audiocore"
	"github.com/tphakala/birdnet-go/internal/conf"
	"github.com/tphakala/birdnet-go/internal/datastore"
	"github.com/tphakala/birdnet-go/internal/errors"
	"github.com/tphakala/birdnet-go/internal/logger"
	"github.com/tphakala/birdnet-go/internal/mqtt"
)

const (
	// mqttConnectionTimeout is the timeout for MQTT connection attempts
	mqttConnectionTimeout = 30 * time.Second
	// discoveryPublishTimeout is the timeout for publishing discovery messages
	discoveryPublishTimeout = 30 * time.Second
	// discoveryDebounceDuration is the delay after the last source registration
	// before HA discovery is republished. Coalesces rapid startup registrations.
	discoveryDebounceDuration = 3 * time.Second
	// haDiscoveryRetireTimeout bounds removing HA discovery when it is turned off.
	haDiscoveryRetireTimeout = 10 * time.Second
	// haPendingRemovalsCap bounds queued HA entity removals (one per deleted or
	// renamed source) while the broker is unreachable.
	haPendingRemovalsCap = 256
)

// ErrMQTTClientNotReady is returned by PublishMQTT whenever the MQTT client
// reference is nil, regardless of whether MQTT is enabled in settings. In
// practice this happens when MQTT is configured but:
//   - initializeMQTT() failed to create the client (invalid broker address, TLS
//     material that will not load, etc.)
//   - The client is between DisconnectMQTTClient() and SetMQTTClient() during
//     runtime reconfiguration.
//
// A client that failed only to *connect* is retained with its reconnect loop
// armed, so it does not surface here: those publishes are suppressed inside the
// mqtt package while it reconnects.
//
// Callers in streaming publish paths (sound level, etc.) should check for
// this sentinel with errors.Is and treat it as a graceful no-op to avoid
// flooding telemetry with identical "client not available" events while
// the broker is unreachable. The detection pipeline already skips MQTT
// action creation when the client is nil (see processor.go), so this
// sentinel is primarily for streaming (non-detection) publishers.
//
// This sentinel error is intentionally a plain errors.NewStd value so it has
// no telemetry category attached. Wrapping layers must preserve it with
// errors.Is-compatible wrapping (fmt.Errorf("...: %w", err) or the internal
// errors builder's New() which chains cause) so callers can detect and
// silently drop publishes while the broker is unreachable.
var ErrMQTTClientNotReady = errors.NewStd("MQTT client not ready")

// GetMQTTClient safely returns the current MQTT client
func (p *Processor) GetMQTTClient() mqtt.Client {
	p.mqttMutex.RLock()
	defer p.mqttMutex.RUnlock()
	return p.MqttClient
}

// SetMQTTClient safely sets a new MQTT client
func (p *Processor) SetMQTTClient(client mqtt.Client) {
	p.mqttMutex.Lock()
	defer p.mqttMutex.Unlock()
	p.MqttClient = client
}

// DisconnectMQTTClient safely disconnects and removes the MQTT client
func (p *Processor) DisconnectMQTTClient() {
	p.mqttMutex.Lock()
	client := p.MqttClient
	p.MqttClient = nil
	p.mqttMutex.Unlock()

	if client != nil {
		client.Disconnect()
	}
}

// PublishMQTT safely publishes a message using the MQTT client if available.
// Does NOT pre-check IsConnected() to avoid TOCTOU race (GitHub #2397).
//
// When the MQTT client reference is nil (initializeMQTT() could not create the
// client, or the client is between DisconnectMQTTClient() and SetMQTTClient()),
// this returns ErrMQTTClientNotReady. Streaming publishers that run on a timer
// (sound level publisher, etc.) should check for this sentinel with
// errors.Is and silently skip to avoid flooding telemetry. See
// internal/analysis/sound_level.go for the canonical caller pattern.
//
// The returned error is intentionally NOT tagged with a telemetry category:
// the sentinel is filtered at the caller, but if it leaks through, it still
// has no CategoryMQTTPublish tag so it will not be reported to Sentry.
func (p *Processor) PublishMQTT(ctx context.Context, topic, payload string) error {
	p.mqttMutex.RLock()
	client := p.MqttClient
	p.mqttMutex.RUnlock()

	if client != nil {
		return client.Publish(ctx, topic, payload)
	}
	// Emit one warn log per topic so operators learn MQTT is configured but not
	// reachable. Subsequent attempts on that topic are silent; the sentinel is
	// all the caller needs to decide to skip.
	//
	// Keyed on the topic rather than guarded once per process, because the line
	// names the topic and a process-wide guard would report a topic the operator
	// may have since changed away from: the topic derives from the
	// hot-reloadable Realtime.MQTT.Topic setting. The key space is bounded by
	// how many distinct topics the configuration has held, not by traffic.
	p.mqttNotReadyWarnLogged.do(topic, func() {
		GetLogger().Warn(
			"MQTT publish suppressed: client not ready (further suppressed publishes on this topic are silent)",
			logger.String("topic", topic),
			logger.String("operation", "publish_mqtt_not_ready"))
	})
	return ErrMQTTClientNotReady
}

// initializeMQTT initializes the MQTT client if enabled in settings
func (p *Processor) initializeMQTT(settings *conf.Settings) {
	if settings == nil {
		return
	}
	if !settings.Realtime.MQTT.Enabled {
		return
	}

	log := GetLogger()
	// Create a new MQTT client using the settings and metrics
	mqttClient, err := mqtt.NewClient(settings, p.Metrics)
	if err != nil {
		log.Error("failed to create MQTT client", logger.Error(err))
		_ = errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryMQTTConnection).
			Context("operation", "mqtt_client_create").
			Build()
		return
	}

	// Register the Home Assistant handlers (discovery only when enabled; the
	// retire handler always, see RegisterHomeAssistantDiscovery).
	p.RegisterHomeAssistantDiscovery(mqttClient, settings)

	// Create a context with a timeout for the connection attempt
	ctx, cancel := context.WithTimeout(context.Background(), mqttConnectionTimeout)
	defer cancel() // Ensure the cancel function is called to release resources

	// Attempt to connect to the MQTT broker. A failure here is not fatal: MQTT is
	// an optional integration, and at boot the broker is commonly unreachable for
	// a few seconds while the network comes up. Retain the client and arm its
	// reconnect loop so it recovers on its own; discarding it here would leave
	// MQTT dead until the next restart.
	//
	// Logged at warn rather than error to match onConnectionLost: a recoverable
	// connection failure is not a fault worth paging on. Connect already reported
	// this failure to telemetry once inside the mqtt package, so no error is built
	// here; building one would duplicate the Sentry event for a single startup
	// outage, which is the noise this warn-level, retry-on-failure path avoids.
	if err := mqttClient.Connect(ctx); err != nil {
		log.Warn("failed to connect to MQTT broker, retrying in background",
			logger.Error(err))
		mqttClient.StartReconnectLoop()
	}

	p.SetMQTTClient(mqttClient)
}

// RegisterHomeAssistantDiscovery registers the OnConnect handler for Home Assistant discovery.
// This is called during MQTT initialization and after MQTT reconfiguration.
//
// The retire handler is registered even when HA discovery is disabled: if this
// process published discovery and HA discovery was then turned off while the
// broker was unreachable, the first successful (re)connect removes the entities.
func (p *Processor) RegisterHomeAssistantDiscovery(client mqtt.Client, settings *conf.Settings) {
	if client == nil || settings == nil {
		return
	}
	p.registerHomeAssistantRetire(client)
	if !settings.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}
	p.registerHomeAssistantDiscovery(client, settings)
}

// registerHomeAssistantRetire registers an OnConnect handler that retires HA
// discovery when it is disabled in the live settings. It is a no-op unless this
// process published discovery, so installs that never enabled HA see nothing.
func (p *Processor) registerHomeAssistantRetire(client mqtt.Client) {
	client.RegisterOnConnectHandler(func() {
		if !client.IsConnected() {
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), haDiscoveryRetireTimeout)
		defer cancel()
		p.RetireHomeAssistantDiscovery(ctx, client, p.currentSettings())
	})
}

// RetireHomeAssistantDiscovery removes the HA entities this process published
// when next has MQTT enabled but HA discovery disabled: the bridge, every
// current source's sensors, and their retained per-source state, all under the
// identity they were published with. Removal goes through client, the caller's
// connected client (the reconfigure path passes the old client before it is
// disconnected; the OnConnect retire handler passes its own).
//
// The published identity is forgotten only after every removal succeeded, so a
// failure or a disconnected client leaves it for the next attempt (the retire
// handler on the next connect). A source missing from the registry at that
// moment (mid-restart) keeps its entities; that is rare and matches the
// behavior before retirement existed.
func (p *Processor) RetireHomeAssistantDiscovery(ctx context.Context, client mqtt.Client, next *conf.Settings) {
	if next == nil || !next.Realtime.MQTT.Enabled || next.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}

	p.haDiscoveryMu.Lock()
	defer p.haDiscoveryMu.Unlock()

	published := p.haPublishedConfig
	if published == nil {
		return
	}
	log := GetLogger()
	if client == nil || !client.IsConnected() {
		log.Debug("HA discovery retirement deferred: MQTT client not connected",
			logger.String("operation", "ha_discovery_retire"))
		return
	}

	removeCtx, cancel := context.WithTimeout(ctx, haDiscoveryRetireTimeout)
	defer cancel()

	publisher := mqtt.NewDiscoveryPublisher(client, published)
	err := publisher.RemoveDiscovery(removeCtx, p.getAudioSourcesForDiscovery())
	// Removals queued for sources deleted just before discovery was turned off
	// are no longer performed by a publish, so retirement performs them. Every
	// candidate key goes, since all live entities are being removed anyway.
	pending := pinHARemovals(p.takeHAPendingRemovals(), published)
	var failed []haPendingRemoval
	for _, item := range pending {
		itemPublisher := mqtt.NewDiscoveryPublisher(client, item.config)
		var errs []error
		for _, key := range mqtt.SourceEntityKeyCandidates(item.source) {
			if rmErr := itemPublisher.RemoveSourceConfigs(removeCtx, key); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
		if !item.configOnly {
			if rmErr := itemPublisher.RemoveSourceState(removeCtx, item.source); rmErr != nil {
				errs = append(errs, rmErr)
			}
		}
		if len(errs) > 0 {
			failed = append(failed, item)
			err = errors.Join(append([]error{err}, errs...)...)
		}
	}
	p.requeueHAPendingRemovals(failed)
	if err != nil {
		log.Warn("failed to remove HA discovery after it was disabled, will retry on next connect",
			logger.Error(err),
			logger.String("operation", "ha_discovery_retire"))
		return
	}
	p.haPublishedConfig = nil
	log.Info("removed HA discovery entities after HA discovery was disabled",
		logger.String("operation", "ha_discovery_retire"))
}

// registerHomeAssistantDiscovery registers the OnConnect handler for Home Assistant discovery.
func (p *Processor) registerHomeAssistantDiscovery(client mqtt.Client, settings *conf.Settings) {
	log := GetLogger()
	haSettings := settings.Realtime.MQTT.HomeAssistant

	// Register the OnConnect handler. The handler runs in a goroutine
	// (client.go onConnect), so the client may disconnect between the
	// callback firing and the goroutine executing.
	client.RegisterOnConnectHandler(func() {
		if !client.IsConnected() {
			log.Debug("MQTT client disconnected before HA discovery handler executed, skipping")
			return
		}

		// The handler outlives the settings it was registered with (it fires on
		// every reconnect). Routing and identity (base topic, discovery prefix,
		// node name) stay those of the settings this client was created with,
		// because a change to them builds a new client and handler. Feature
		// flags are read live: HA discovery may have been turned off since, and
		// sound level monitoring decides which sensors are published.
		live := p.currentSettings()
		if live == nil {
			live = settings
		}
		if !live.Realtime.MQTT.HomeAssistant.Enabled {
			log.Debug("HA discovery disabled since handler registration, skipping")
			return
		}
		effective := conf.CloneSettings(settings)
		effective.Realtime.Audio.SoundLevel = live.Realtime.Audio.SoundLevel

		log.Info("MQTT connected, publishing Home Assistant discovery messages")

		ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
		defer cancel()

		if err := p.publishHomeAssistantDiscovery(ctx, client, effective); err != nil {
			log.Error("Failed to publish Home Assistant discovery",
				logger.Error(err))
		}
	})

	log.Info("Home Assistant discovery handler registered",
		logger.String("discovery_prefix", haSettings.DiscoveryPrefix),
		logger.String("device_name", haSettings.DeviceName))
}

// publishHomeAssistantDiscovery publishes Home Assistant discovery messages.
// This is the shared implementation used by both the OnConnect handler and manual trigger.
// Skips publishing when no audio sources are registered yet (startup race).
func (p *Processor) publishHomeAssistantDiscovery(ctx context.Context, client mqtt.Client, settings *conf.Settings) error {
	p.haDiscoveryMu.Lock()
	defer p.haDiscoveryMu.Unlock()

	// A publish queued before HA discovery was turned off (debounce timer, an
	// OnConnect that raced the settings save) must not recreate retired entities.
	if live := p.currentSettings(); live != nil && !live.Realtime.MQTT.HomeAssistant.Enabled {
		GetLogger().Debug("skipping HA discovery publish, HA discovery is disabled",
			logger.String("operation", "ha_discovery_skip"))
		return nil
	}

	haSettings := settings.Realtime.MQTT.HomeAssistant
	discoveryConfig := mqtt.DiscoveryConfig{
		DiscoveryPrefix: haSettings.DiscoveryPrefix,
		BaseTopic:       settings.Realtime.MQTT.Topic,
		DeviceName:      haSettings.DeviceName,
		NodeID:          settings.Main.Name,
		Version:         settings.Version,
	}

	publisher := mqtt.NewDiscoveryPublisher(client, &discoveryConfig)

	// Queued removals are taken BEFORE the live snapshot. A source is
	// unregistered before its removal is queued, so anything taken here is
	// absent from the snapshot unless it was genuinely re-added; anything queued
	// after this point arms a new debounce and is handled by the next publish.
	pending := p.takeHAPendingRemovals()
	// Removals target the identity the entities were published under, which a
	// settings save may have changed together with the stream deletion.
	removalConfig := &discoveryConfig
	if p.haPublishedConfig != nil {
		removalConfig = p.haPublishedConfig
	}

	sources := p.getAudioSourcesForDiscovery()

	if len(sources) == 0 {
		// Queued removals are explicit user intent (a deleted stream may have
		// been the last one), so perform them even with nothing to publish.
		p.processHAPendingRemovals(ctx, client, removalConfig, pending, nil)
		GetLogger().Debug("skipping HA discovery publish, no audio sources registered yet",
			logger.String("operation", "ha_discovery_skip"))
		return nil
	}

	p.defaultDiscoveryCleanup.Do(func() {
		cleanupDefaultDiscovery(ctx, publisher)
	})

	if err := publisher.PublishDiscovery(ctx, sources, settings); err != nil {
		p.requeueHAPendingRemovals(pending)
		return err
	}
	p.haPublishedConfig = &discoveryConfig
	p.clearLegacyStatusTopic(ctx, client, settings.Realtime.MQTT.Topic)
	removePromotedSuffixConfigs(ctx, publisher, sources)
	p.processHAPendingRemovals(ctx, client, removalConfig, pending, sources)
	return nil
}

// clearLegacyStatusTopic empties the retained status message an older version
// left on "<base>/status" built from the raw base topic. For a base topic with a
// trailing slash that was "<base>//status", while the status (LWT) topic is now
// built from the trimmed base, so the old retained "online"/"offline" would stay
// on the broker forever. Nothing is published when the two topics coincide. A
// failed clear is retried on the next discovery publish. Must be called with
// haDiscoveryMu held.
func (p *Processor) clearLegacyStatusTopic(ctx context.Context, client mqtt.Client, baseTopic string) {
	legacy := baseTopic + "/status"
	if legacy == mqtt.StatusTopic(baseTopic) || legacy == p.haLegacyStatusCleared {
		return
	}
	if err := client.PublishWithRetain(ctx, legacy, "", true); err != nil {
		GetLogger().Debug("failed to clear legacy status topic",
			logger.String("topic", legacy),
			logger.Error(err),
			logger.String("operation", "ha_discovery_legacy_status"))
		return
	}
	p.haLegacyStatusCleared = legacy
}

// haPendingRemoval is one queued HA entity removal. configOnly marks a rename:
// the source is still live under its raw ID, so only the discovery configs under
// its previous name are removed, never its per-source state.
type haPendingRemoval struct {
	source     datastore.AudioSource
	configOnly bool
	// config pins the identity a failed removal was attempted under, so a retry
	// after the published identity changed still targets the right topics. nil
	// until the first attempt.
	config *mqtt.DiscoveryConfig
}

// ForgetHomeAssistantSource queues removal of a source deleted from the
// configuration: its discovery configs (under every entity key it can have
// held that no live source owns) and its retained per-source state. The work
// runs on the next debounced discovery publish, never on the caller's
// goroutine, so it is safe to call while holding audio pipeline locks.
// Nothing is queued unless MQTT is enabled. HA discovery may already be off:
// a save that turns it off while the broker is down defers retirement to the
// next connection, and the source deleted in that same save is no longer
// registered then, so only the queue still names it. The retire handler
// performs the queued removals; with nothing to retire they wait for the next
// publish, where a removal of entities that no longer exist is harmless.
func (p *Processor) ForgetHomeAssistantSource(source datastore.AudioSource) {
	p.queueHAPendingRemoval(source, false)
}

// ForgetHomeAssistantEntityName queues removal of the discovery configs a
// source had under its previous display name after a rename. previous carries
// the source's raw ID and its OLD display name. The live source is republished
// under its new name, so its per-source state is kept.
func (p *Processor) ForgetHomeAssistantEntityName(previous datastore.AudioSource) {
	p.queueHAPendingRemoval(previous, true)
}

func (p *Processor) queueHAPendingRemoval(source datastore.AudioSource, configOnly bool) {
	settings := p.currentSettings()
	if settings == nil || !settings.Realtime.MQTT.Enabled {
		return
	}
	item := haPendingRemoval{source: source, configOnly: configOnly}
	p.haPendingMu.Lock()
	if !slices.Contains(p.haPendingRemovals, item) {
		if len(p.haPendingRemovals) >= haPendingRemovalsCap {
			// The broker has been unreachable through many settings changes;
			// drop the oldest request rather than grow without bound.
			p.haPendingRemovals = p.haPendingRemovals[1:]
			GetLogger().Warn("HA entity removal queue full, dropping the oldest request",
				logger.Int("cap", haPendingRemovalsCap),
				logger.String("operation", "ha_discovery_forget_source"))
		}
		p.haPendingRemovals = append(p.haPendingRemovals, item)
	}
	p.haPendingMu.Unlock()
	p.scheduleDiscoveryPublish()
}

// takeHAPendingRemovals empties the queue and returns what was in it.
func (p *Processor) takeHAPendingRemovals() []haPendingRemoval {
	p.haPendingMu.Lock()
	defer p.haPendingMu.Unlock()
	items := p.haPendingRemovals
	p.haPendingRemovals = nil
	return items
}

// pinHARemovals pins every removal not yet attempted to config, the identity
// its entities were published under, and drops duplicates. A request repeated
// after an earlier attempt failed is queued unpinned next to the pinned retry;
// once both are pinned to the same identity they are the same removal, so it
// is performed (and requeued) only once. The same source pinned to different
// identities stays separate, since each identity has its own topics.
func pinHARemovals(items []haPendingRemoval, config *mqtt.DiscoveryConfig) []haPendingRemoval {
	type removalIdentity struct {
		source     datastore.AudioSource
		configOnly bool
		config     mqtt.DiscoveryConfig
	}
	seen := make(map[removalIdentity]struct{}, len(items))
	pinned := make([]haPendingRemoval, 0, len(items))
	for _, item := range items {
		if item.config == nil {
			item.config = config
		}
		id := removalIdentity{source: item.source, configOnly: item.configOnly, config: *item.config}
		if _, dup := seen[id]; dup {
			continue
		}
		seen[id] = struct{}{}
		pinned = append(pinned, item)
	}
	return pinned
}

// requeueHAPendingRemovals puts removals that failed back at the front of the
// queue: they were taken before anything queued during the attempt, so they are
// the oldest requests and the first to go when the cap is exceeded.
func (p *Processor) requeueHAPendingRemovals(items []haPendingRemoval) {
	if len(items) == 0 {
		return
	}
	p.haPendingMu.Lock()
	defer p.haPendingMu.Unlock()
	p.haPendingRemovals = append(slices.Clone(items), p.haPendingRemovals...)
	if excess := len(p.haPendingRemovals) - haPendingRemovalsCap; excess > 0 {
		// Same bound as queueHAPendingRemoval: drop the oldest requests.
		p.haPendingRemovals = p.haPendingRemovals[excess:]
		GetLogger().Warn("HA entity removal queue full, dropping the oldest requests",
			logger.Int("cap", haPendingRemovalsCap),
			logger.Int("dropped", excess),
			logger.String("operation", "ha_discovery_forget_source"))
	}
}

// processHAPendingRemovals performs the taken queued removals after discovery
// for the live sources was published. A candidate key owned by a live source is never
// removed (an edited stream URL keeps its name and key under a new raw ID), and
// a deleted source whose raw ID is live again keeps everything. Failed items are
// requeued. Must be called with haDiscoveryMu held.
func (p *Processor) processHAPendingRemovals(ctx context.Context, client mqtt.Client, config *mqtt.DiscoveryConfig, items []haPendingRemoval, live []datastore.AudioSource) {
	if len(items) == 0 {
		return
	}

	liveIDs := make(map[string]struct{}, len(live))
	for _, src := range live {
		liveIDs[src.ID] = struct{}{}
	}
	liveKeys := make(map[string]struct{}, len(live))
	for _, key := range mqtt.SourceEntityKeys(live) {
		liveKeys[key] = struct{}{}
	}

	var failed []haPendingRemoval
	for _, item := range pinHARemovals(items, config) {
		if _, isLive := liveIDs[item.source.ID]; isLive && !item.configOnly {
			continue // re-added under the same raw ID: nothing to remove
		}
		publisher := mqtt.NewDiscoveryPublisher(client, item.config)
		var errs []error
		for _, key := range mqtt.SourceEntityKeyCandidates(item.source) {
			if _, owned := liveKeys[key]; owned {
				continue
			}
			if err := publisher.RemoveSourceConfigs(ctx, key); err != nil {
				errs = append(errs, err)
			}
		}
		if !item.configOnly {
			if err := publisher.RemoveSourceState(ctx, item.source); err != nil {
				errs = append(errs, err)
			}
		}
		if err := errors.Join(errs...); err != nil {
			GetLogger().Warn("failed to remove HA entities of a removed or renamed source, will retry",
				logger.String("source_id", item.source.ID),
				logger.Error(err),
				logger.String("operation", "ha_discovery_forget_source"))
			failed = append(failed, item)
		}
	}
	p.requeueHAPendingRemovals(failed)
}

// removePromotedSuffixConfigs empties the suffixed discovery configs of every
// source that now holds its plain entity key. When the winner of a same-name
// group is deleted, the survivor is promoted from base+"_"+ID to base and its
// old suffixed configs would otherwise stay in HA as a duplicate device. A key
// another live source owns is skipped (a source whose display name sanitizes to
// exactly this suffixed key would hold it as its plain key); for a source that
// was never suffixed this is a no-op removal of topics that do not exist. Failures are only logged, since this
// runs on every discovery publish.
func removePromotedSuffixConfigs(ctx context.Context, publisher *mqtt.Publisher, sources []datastore.AudioSource) {
	keys := mqtt.SourceEntityKeys(sources)
	owned := make(map[string]struct{}, len(keys))
	for _, key := range keys {
		owned[key] = struct{}{}
	}
	for _, src := range sources {
		candidates := mqtt.SourceEntityKeyCandidates(src)
		plain, suffixed := candidates[0], candidates[1]
		if keys[src.ID] != plain {
			continue
		}
		if _, taken := owned[suffixed]; taken {
			continue
		}
		if err := publisher.RemoveSourceConfigs(ctx, suffixed); err != nil {
			GetLogger().Debug("failed to remove stale suffixed HA discovery configs",
				logger.String("source_id", src.ID),
				logger.Error(err),
				logger.String("operation", "ha_discovery_promotion_cleanup"))
		}
	}
}

// cleanupDefaultDiscovery removes stale HA discovery entries for the
// hardcoded "default" source that older versions published before the
// source registry was populated. These entries never matched real
// detection payloads and left HA sensors stuck at "Unknown".
//
// Only the legacy source's sensors and per-source state are removed. The bridge
// is shared with the real sources and is republished right after this runs, so
// removing it here would make its entity flap on every process start.
func cleanupDefaultDiscovery(ctx context.Context, publisher *mqtt.Publisher) {
	defaultSource := datastore.AudioSource{ID: "default", DisplayName: "Default"}
	entityKey := mqtt.SourceEntityKeys([]datastore.AudioSource{defaultSource})[defaultSource.ID]
	if err := publisher.RemoveSourceDiscovery(ctx, defaultSource, entityKey); err != nil {
		GetLogger().Debug("failed to clean up stale default discovery entries",
			logger.Error(err),
			logger.String("operation", "ha_discovery_cleanup_default"))
	}
}

// TriggerHomeAssistantDiscovery manually triggers Home Assistant discovery messages.
// This can be called from the API to force republishing of discovery messages.
func (p *Processor) TriggerHomeAssistantDiscovery(ctx context.Context) error {
	log := GetLogger()
	settings := p.currentSettings()

	// Guard against nil settings during startup/teardown
	if settings == nil {
		return errors.Newf("settings not initialized").
			Component("analysis.processor").
			Category(errors.CategoryConfiguration).
			Context("operation", "trigger_ha_discovery").
			Build()
	}

	// Check if MQTT is enabled and Home Assistant discovery is enabled
	if !settings.Realtime.MQTT.Enabled {
		return errors.Newf("MQTT is not enabled").
			Component("analysis.processor").
			Category(errors.CategoryConfiguration).
			Context("operation", "trigger_ha_discovery").
			Build()
	}
	if !settings.Realtime.MQTT.HomeAssistant.Enabled {
		return errors.Newf("home assistant discovery is not enabled").
			Component("analysis.processor").
			Category(errors.CategoryConfiguration).
			Context("operation", "trigger_ha_discovery").
			Build()
	}

	// Get the MQTT client
	client := p.GetMQTTClient()
	if client == nil || !client.IsConnected() {
		return errors.Newf("MQTT client not connected").
			Component("analysis.processor").
			Category(errors.CategoryMQTTConnection).
			Context("operation", "trigger_ha_discovery").
			Build()
	}

	log.Info("Manually triggering Home Assistant discovery")

	// Publish discovery messages with timeout
	publishCtx, cancel := context.WithTimeout(ctx, discoveryPublishTimeout)
	defer cancel()

	if err := p.publishHomeAssistantDiscovery(publishCtx, client, settings); err != nil {
		log.Error("Failed to publish Home Assistant discovery", logger.Error(err))
		return errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryMQTTPublish).
			Context("operation", "publish_ha_discovery").
			Build()
	}

	return nil
}

// getAudioSourcesForDiscovery retrieves audio sources from the registry for HA discovery.
// Returns nil when the registry is not yet injected or has no sources registered,
// signaling that discovery should be deferred until sources are available.
func (p *Processor) getAudioSourcesForDiscovery() []datastore.AudioSource {
	p.registryMu.RLock()
	registry := p.registry
	p.registryMu.RUnlock()

	if registry == nil {
		return nil
	}

	registrySources := registry.List()

	sources := make([]datastore.AudioSource, 0, len(registrySources))
	for _, src := range registrySources {
		sources = append(sources, datastore.AudioSource{
			ID:          src.ID,
			SafeString:  src.SafeString,
			DisplayName: src.DisplayName,
		})
	}

	return sources
}

// SetRegistry sets the source registry for audio source lookups and registers
// a listener that triggers debounced HA discovery re-publish when sources are
// added or reconfigured. This ensures discovery is published with correct source
// IDs even when MQTT connects before sources are registered at startup.
func (p *Processor) SetRegistry(r *audiocore.SourceRegistry) {
	p.registryMu.Lock()
	defer p.registryMu.Unlock()

	if p.registry == r {
		return
	}
	p.registry = r

	if r == nil {
		return
	}

	r.AddListener(func(event audiocore.SourceEvent) {
		switch event.Type {
		case audiocore.SourceAdded, audiocore.SourceReconfigured:
			p.scheduleDiscoveryPublish()
		default: // SourceRemoved and SourceStateChanged don't affect discovery
		}
	})
}

// scheduleDiscoveryPublish starts or resets a debounce timer that publishes
// HA discovery after discoveryDebounceDuration of inactivity. Called by the
// source registry listener when sources are added or reconfigured.
func (p *Processor) scheduleDiscoveryPublish() {
	p.discoveryDebounceMu.Lock()
	defer p.discoveryDebounceMu.Unlock()

	if p.discoveryDebounce != nil {
		p.discoveryDebounce.Stop()
	}
	p.discoveryDebounce = time.AfterFunc(discoveryDebounceDuration, func() {
		p.publishDiscoveryIfReady()
	})
}

// publishDiscoveryIfReady publishes HA discovery if MQTT is enabled, connected,
// and HA discovery is configured. Safe to call at any time; silently returns
// when preconditions are not met.
func (p *Processor) publishDiscoveryIfReady() {
	settings := p.currentSettings()
	if settings == nil {
		return
	}
	if !settings.Realtime.MQTT.Enabled || !settings.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}

	client := p.GetMQTTClient()
	if client == nil || !client.IsConnected() {
		return
	}

	log := GetLogger()
	log.Info("publishing HA discovery (debounced refresh)",
		logger.String("operation", "ha_discovery_refresh"))

	ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
	defer cancel()

	if err := p.publishHomeAssistantDiscovery(ctx, client, settings); err != nil {
		log.Error("failed to publish HA discovery (debounced refresh)",
			logger.Error(err),
			logger.String("operation", "ha_discovery_refresh"))
	}
}

// RefreshHomeAssistantDiscovery schedules a debounced republish of HA discovery,
// for settings that change which sensors exist (sound level monitoring) without
// reconnecting MQTT. It only arms the debounce timer, so it never blocks the
// caller; the publish runs later on the timer goroutine and is skipped unless
// MQTT and HA discovery are enabled and the client is connected.
func (p *Processor) RefreshHomeAssistantDiscovery() {
	p.scheduleDiscoveryPublish()
}

// Registry returns the source registry, or nil if not set.
func (p *Processor) Registry() *audiocore.SourceRegistry {
	p.registryMu.RLock()
	defer p.registryMu.RUnlock()
	return p.registry
}
