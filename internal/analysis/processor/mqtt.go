// mqtt.go: MQTT-related functionality for the processor
package processor

import (
	"context"
	stderrors "errors"
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
	// haDiscoveryRetireTimeout bounds the removal publishes issued when retiring
	// HA discovery during a reconfigure through the still-connected old client.
	haDiscoveryRetireTimeout = 10 * time.Second
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
// This sentinel error is intentionally a plain stderrors.New value so it has
// no telemetry category attached. Wrapping layers must preserve it with
// errors.Is-compatible wrapping (fmt.Errorf("...: %w", err) or the internal
// errors builder's New() which chains cause) so callers can detect and
// silently drop publishes while the broker is unreachable.
var ErrMQTTClientNotReady = stderrors.New("MQTT client not ready")

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
	// reachable. Subsequent attempts on that topic are silent: the sentinel is
	// all the caller needs to decide to skip.
	//
	// Keyed on the topic rather than guarded once per process, because the line
	// names the topic and a process-wide guard would report a topic the operator
	// may have since changed away from: the topic derives from the
	// hot-reloadable Realtime.MQTT.Topic setting. The key space is bounded by
	// how many distinct topics the configuration has held, not by traffic.
	//
	// The per-source sound level topic (mqtt.SourceSoundLevelTopic) publishes
	// through here too while HA discovery is on, so the key space also grows by
	// one entry per audio source. That is still bounded: source count is small
	// and stable. (The per-source detection topic does not add keys here; it
	// publishes via the MQTT client directly, not through PublishMQTT.)
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

	// Register Home Assistant discovery handler if enabled
	if settings.Realtime.MQTT.HomeAssistant.Enabled {
		p.registerHomeAssistantDiscovery(mqttClient, settings)
	}

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
func (p *Processor) RegisterHomeAssistantDiscovery(client mqtt.Client, settings *conf.Settings) {
	if client == nil || settings == nil {
		return
	}
	if !settings.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}
	p.registerHomeAssistantDiscovery(client, settings)
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

		// Read the live settings so a settings change after registration takes
		// effect (the handler may fire on a reconnect long after registration).
		// Fall back to the captured settings if no live snapshot is published.
		liveSettings := p.currentSettings()
		if liveSettings == nil {
			liveSettings = settings
		}

		// HA discovery may have been turned off since this handler was registered.
		if !liveSettings.Realtime.MQTT.HomeAssistant.Enabled {
			log.Debug("HA discovery disabled since handler registration, skipping publish")
			return
		}

		log.Info("MQTT connected, publishing Home Assistant discovery messages")

		ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
		defer cancel()

		if err := p.publishHomeAssistantDiscovery(ctx, client, liveSettings); err != nil {
			log.Error("Failed to publish Home Assistant discovery",
				logger.Error(err))
		}
	})

	log.Info("Home Assistant discovery handler registered",
		logger.String("discovery_prefix", haSettings.DiscoveryPrefix),
		logger.String("device_name", haSettings.DeviceName))
}

// haDiscoveryRecord captures the last successfully published Home Assistant
// discovery: the config it was published under, the broker, and the source list.
// It lets the next publish (and RetireHomeAssistantDiscovery) remove entities and
// retained state for sources that disappeared or whose entity key changed.
type haDiscoveryRecord struct {
	config  mqtt.DiscoveryConfig
	broker  string
	sources []datastore.AudioSource
}

// publishHomeAssistantDiscovery publishes Home Assistant discovery messages.
// This is the shared implementation used by both the OnConnect handler and manual trigger.
// Skips publishing when no audio sources have EVER been registered (startup race),
// but removes everything previously published once the last source disappears.
func (p *Processor) publishHomeAssistantDiscovery(ctx context.Context, client mqtt.Client, settings *conf.Settings) error {
	haSettings := settings.Realtime.MQTT.HomeAssistant
	discoveryConfig := mqtt.DiscoveryConfig{
		DiscoveryPrefix: haSettings.DiscoveryPrefix,
		BaseTopic:       settings.Realtime.MQTT.Topic,
		DeviceName:      haSettings.DeviceName,
		NodeID:          settings.Main.Name,
		Version:         settings.Version,
	}
	broker := settings.Realtime.MQTT.Broker

	publisher := mqtt.NewDiscoveryPublisher(client, &discoveryConfig)
	sources := p.getAudioSourcesForDiscovery()

	// Serialize discovery reconciliation so concurrent triggers (OnConnect, the
	// debounce timer, and the manual trigger) cannot interleave publishes and
	// removals or corrupt the last-published record.
	p.haDiscoveryMu.Lock()
	defer p.haDiscoveryMu.Unlock()

	if len(sources) == 0 {
		// No sources registered now. If we published before, every source has gone
		// away: remove everything we published (entities, retained state, bridge)
		// under the OLD config and clear the record. If we never published, this is
		// the startup race (GitHub #2948), so skip silently and leave nothing.
		prev := p.haDiscoveryRecord
		if prev == nil {
			GetLogger().Debug("skipping HA discovery publish, no audio sources registered yet",
				logger.String("operation", "ha_discovery_skip"))
			return nil
		}
		prevPublisher := mqtt.NewDiscoveryPublisher(client, &prev.config)
		if err := prevPublisher.RemoveDiscovery(ctx, prev.sources); err != nil {
			GetLogger().Error("failed to remove HA discovery after all sources removed",
				logger.Error(err),
				logger.String("operation", "ha_discovery_remove_all"))
			// Keep the record so a later trigger can retry the removal.
			return err
		}
		p.haDiscoveryRecord = nil
		return nil
	}

	p.defaultDiscoveryCleanup.Do(func() {
		cleanupDefaultDiscovery(ctx, publisher)
	})

	if err := publisher.PublishDiscovery(ctx, sources, settings); err != nil {
		return err
	}

	// Publish succeeded: remove entities/state for sources that disappeared or
	// whose entity key changed since the last publish, then store the new record.
	p.reconcileRemovedDiscovery(ctx, publisher, sources)
	p.haDiscoveryRecord = &haDiscoveryRecord{
		config:  discoveryConfig,
		broker:  broker,
		sources: sources,
	}
	return nil
}

// reconcileRemovedDiscovery removes discovery entities and retained state for
// sources that were in the previous record but are gone from newSources, and
// removes the orphaned config topics for sources still present whose entity key
// changed. Must be called with haDiscoveryMu held, after a successful publish of
// newSources under the current publisher.
func (p *Processor) reconcileRemovedDiscovery(ctx context.Context, publisher *mqtt.Publisher, newSources []datastore.AudioSource) {
	prev := p.haDiscoveryRecord
	if prev == nil {
		return
	}

	oldKeys := mqtt.SourceEntityKeys(prev.sources)
	newKeys := mqtt.SourceEntityKeys(newSources)

	present := make(map[string]struct{}, len(newSources))
	for _, s := range newSources {
		present[s.ID] = struct{}{}
	}

	for _, oldSource := range prev.sources {
		oldKey := oldKeys[oldSource.ID]
		if _, stillPresent := present[oldSource.ID]; !stillPresent {
			// Source removed: clear its entities and retained per-source state.
			if err := publisher.RemoveSourceDiscovery(ctx, oldSource, oldKey); err != nil {
				GetLogger().Warn("failed to remove discovery for departed source",
					logger.String("source_id", oldSource.ID),
					logger.Error(err),
					logger.String("operation", "ha_discovery_remove_source"))
			}
			continue
		}
		// Source still present: if its entity key changed, the configs under the
		// old key are orphaned. Clear ONLY those configs; the state topics (keyed
		// by the unchanged raw source ID) are still being published to, so wiping
		// them would drop a live source's retained state.
		if newKeys[oldSource.ID] != oldKey {
			if err := publisher.RemoveSourceConfigs(ctx, oldKey); err != nil {
				GetLogger().Warn("failed to remove stale discovery configs for rekeyed source",
					logger.String("source_id", oldSource.ID),
					logger.Error(err),
					logger.String("operation", "ha_discovery_remove_stale_configs"))
			}
		}
	}
}

// RetireHomeAssistantDiscovery removes previously published HA discovery when a
// reconfigure makes it stale, using the still-connected OLD client and must be
// called BEFORE the old client is disconnected. It fires when HA discovery is
// being turned off (while MQTT stays enabled) or when the discovery identity
// (discovery prefix, base topic, node ID, or broker) changed since the last
// publish, so entities and retained state are removed under the OLD topics
// rather than orphaned. It is a no-op when nothing was ever published, when MQTT
// is being disabled entirely (entities go unavailable via the LWT status topic,
// as before), or when the identity is unchanged and HA discovery stays enabled.
func (p *Processor) RetireHomeAssistantDiscovery(ctx context.Context, next *conf.Settings) {
	if next == nil {
		return
	}

	p.haDiscoveryMu.Lock()
	defer p.haDiscoveryMu.Unlock()

	prev := p.haDiscoveryRecord
	if prev == nil {
		return // nothing was ever published, so nothing to retire
	}

	// MQTT disabled entirely: leave the entities to go unavailable through the LWT
	// status topic, same as before. Keep the record so a later re-enable can still
	// reconcile against it.
	if !next.Realtime.MQTT.Enabled {
		return
	}

	haDisabled := !next.Realtime.MQTT.HomeAssistant.Enabled
	identityChanged := prev.config.DiscoveryPrefix != next.Realtime.MQTT.HomeAssistant.DiscoveryPrefix ||
		prev.config.BaseTopic != next.Realtime.MQTT.Topic ||
		prev.config.NodeID != next.Main.Name ||
		prev.broker != next.Realtime.MQTT.Broker

	if !haDisabled && !identityChanged {
		return // still enabled under the same identity: the next publish reconciles
	}

	client := p.GetMQTTClient()
	if client == nil || !client.IsConnected() {
		// No connected client to publish the removal through. Keep the record so a
		// later publish or retire can still clean up.
		return
	}

	removeCtx, cancel := context.WithTimeout(ctx, haDiscoveryRetireTimeout)
	defer cancel()

	// Remove under the RECORDED config (old prefix/base/node), not the new one.
	publisher := mqtt.NewDiscoveryPublisher(client, &prev.config)
	if err := publisher.RemoveDiscovery(removeCtx, prev.sources); err != nil {
		GetLogger().Error("failed to retire HA discovery on reconfigure",
			logger.Error(err),
			logger.String("operation", "ha_discovery_retire"))
		return // keep the record so a later attempt can retry
	}
	p.haDiscoveryRecord = nil
}

// cleanupDefaultDiscovery removes stale HA discovery entries for the
// hardcoded "default" source that older versions published before the
// source registry was populated. These entries never matched real
// detection payloads and left HA sensors stuck at "Unknown".
func cleanupDefaultDiscovery(ctx context.Context, publisher *mqtt.Publisher) {
	defaultSources := []datastore.AudioSource{
		{ID: "default", DisplayName: "Default"},
	}
	if err := publisher.RemoveDiscovery(ctx, defaultSources); err != nil {
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
		case audiocore.SourceAdded, audiocore.SourceReconfigured, audiocore.SourceRemoved:
			// A source appeared, was reconfigured, or was removed: republish so new
			// entities appear and a departed source's entities and retained state
			// are cleaned up by the diff in publishHomeAssistantDiscovery.
			p.scheduleDiscoveryPublish()
		case audiocore.SourceStateChanged:
			// Runtime state (running/stopped) does not change discovery entities.
		default:
			// Unknown event types do not affect discovery.
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

// RepublishHomeAssistantDiscovery re-publishes HA discovery if MQTT and HA
// discovery are enabled and the client is connected. Safe to call at any time
// (it silently returns when preconditions are not met). Used when a setting that
// changes the discovered entity set toggles at runtime, e.g. sound level
// monitoring: combined with the per-source Sound Level sensor removal in the
// discovery publisher, this adds or removes that sensor immediately.
func (p *Processor) RepublishHomeAssistantDiscovery() {
	p.publishDiscoveryIfReady()
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
	log.Info("publishing HA discovery after source registration",
		logger.String("operation", "ha_discovery_source_event"))

	ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
	defer cancel()

	if err := p.publishHomeAssistantDiscovery(ctx, client, settings); err != nil {
		log.Error("failed to publish HA discovery after source registration",
			logger.Error(err),
			logger.String("operation", "ha_discovery_source_event"))
	}
}

// Registry returns the source registry, or nil if not set.
func (p *Processor) Registry() *audiocore.SourceRegistry {
	p.registryMu.RLock()
	defer p.registryMu.RUnlock()
	return p.registry
}
