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

// publishedSource records one source as it was published: the source itself and
// the entity key its device identifier, unique_ids, and config topics used.
type publishedSource struct {
	source    datastore.AudioSource
	entityKey string
}

// haDiscoveryRecord captures everything published since the last full retire: the
// config it was published under and, per raw source ID, the source and the entity
// key it was published with. It lets the next publish, RetireHomeAssistantDiscovery,
// and ForgetHomeAssistantSource remove entities and retained state under the exact
// topics they were published on. Broker is intentionally NOT part of the record:
// a broker change points BirdNET-Go at a different server whose retained topics we
// cannot touch, so it is not a discovery-identity change.
type haDiscoveryRecord struct {
	config    mqtt.DiscoveryConfig
	published map[string]publishedSource // keyed by raw source ID
}

// sourcesAndKeys reconstructs the recorded sources and their entity keys, for
// passing to the *WithKeys publisher APIs.
func (r *haDiscoveryRecord) sourcesAndKeys() (sources []datastore.AudioSource, keys map[string]string) {
	sources = make([]datastore.AudioSource, 0, len(r.published))
	keys = make(map[string]string, len(r.published))
	for id, ps := range r.published {
		sources = append(sources, ps.source)
		keys[id] = ps.entityKey
	}
	return sources, keys
}

// keys returns the recorded raw-source-ID -> entity-key assignment, used as the
// "previous" input to SourceEntityKeys so incumbency is preserved across edits.
func (r *haDiscoveryRecord) keys() map[string]string {
	keys := make(map[string]string, len(r.published))
	for id, ps := range r.published {
		keys[id] = ps.entityKey
	}
	return keys
}

// errHADiscoveryClientNotConnected is returned internally when a removal or
// republish cannot proceed because the MQTT client is not connected. The record
// is kept so a later trigger can retry once the client reconnects.
var errHADiscoveryClientNotConnected = stderrors.New("MQTT client not connected for HA discovery")

// publishHomeAssistantDiscovery publishes Home Assistant discovery messages.
// This is the shared implementation used by both the OnConnect handler and manual
// trigger. It takes haDiscoveryMu FIRST, then reads the live settings and the
// source snapshot under the lock so a stale caller snapshot cannot resurrect or
// remove the wrong sources. When no source is currently registered (startup race
// or a transiently empty registry), it skips silently and removes nothing.
func (p *Processor) publishHomeAssistantDiscovery(ctx context.Context, client mqtt.Client, settings *conf.Settings) error {
	// Serialize discovery reconciliation so concurrent triggers (OnConnect, the
	// debounce timer, and the manual trigger) cannot interleave publishes and
	// removals or corrupt the record. Snapshots are taken under this lock.
	p.haDiscoveryMu.Lock()
	defer p.haDiscoveryMu.Unlock()

	// Prefer the live settings read inside the lock: a caller that captured its
	// settings before HA discovery was turned off must not resurrect entities that
	// a concurrent retire just removed.
	if live := p.currentSettings(); live != nil {
		settings = live
	}
	if settings == nil {
		return nil
	}
	if !settings.Realtime.MQTT.Enabled || !settings.Realtime.MQTT.HomeAssistant.Enabled {
		// HA discovery is disabled in the live settings: publishing now would
		// re-create entities the retire path is responsible for removing.
		return nil
	}

	sources := p.getAudioSourcesForDiscovery()
	if len(sources) == 0 {
		// No source registered right now. This is either the startup race
		// (GitHub #2948) or a registry that transiently emptied while a source
		// restarts. Skip silently and remove nothing: a source that comes back in a
		// few seconds keeps its entities and retained state.
		GetLogger().Debug("skipping HA discovery publish, no audio sources registered",
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

	prev := p.haDiscoveryRecord

	// Identity drift: a changed discovery prefix or node ID moves every entity to a
	// new set of config topics, so remove the old ones first; a base-topic-only
	// change reuses the same config topics but different state topics.
	if prev != nil {
		if err := p.reconcileIdentityDrift(ctx, client, prev, &discoveryConfig); err != nil {
			return err
		}
		prev = p.haDiscoveryRecord // may have been reset to nil by a prefix/node change
	}

	// Compute entity keys, preserving incumbency from the surviving record so an
	// edited RTSP URL (new raw ID, same name) or a new same-name source cannot
	// steal a live source's key and move its HA history.
	var previousKeys map[string]string
	if prev != nil {
		previousKeys = prev.keys()
	}
	entityKeys := mqtt.SourceEntityKeys(sources, previousKeys)

	publisher := mqtt.NewDiscoveryPublisher(client, &discoveryConfig)
	p.defaultDiscoveryCleanup.Do(func() {
		cleanupDefaultDiscovery(ctx, publisher)
	})

	if err := publisher.PublishDiscoveryWithKeys(ctx, sources, entityKeys, settings); err != nil {
		return err
	}

	// On a trailing-slash base topic, older versions published the LWT/status to
	// "<raw>//status". Clear that once so it does not linger retained.
	p.clearLegacyStatusTopicOnce(ctx, client, &discoveryConfig)

	// For sources present in BOTH the record and the new set whose key changed,
	// the configs under the old key are orphaned. Remove them, but never delete a
	// key that a live source now owns. Never touch sources that are in the record
	// but absent from the snapshot (they may be restarting).
	p.removeRekeyedConfigs(ctx, publisher, client, prev, sources, entityKeys)

	// Update the record: keep entries for sources absent from the snapshot, merge
	// the new assignments, and store the config just published under.
	p.haDiscoveryRecord = mergeDiscoveryRecord(prev, sources, entityKeys, &discoveryConfig)
	return nil
}

// reconcileIdentityDrift handles a discovery-identity change detected at publish
// time. A prefix or node-ID change moves every entity to new config topics, so it
// removes everything under the RECORDED config and resets the record (only on a
// fully successful removal). A base-topic-only change keeps the same config topics
// (the republish overwrites them in place) but changes the state topics, so it
// clears the retained per-source state and status under the OLD base and keeps the
// record. Must be called with haDiscoveryMu held.
func (p *Processor) reconcileIdentityDrift(ctx context.Context, client mqtt.Client, prev *haDiscoveryRecord, next *mqtt.DiscoveryConfig) error {
	prefixChanged := prev.config.DiscoveryPrefix != next.DiscoveryPrefix
	nodeChanged := mqtt.SanitizeID(prev.config.NodeID) != mqtt.SanitizeID(next.NodeID)
	baseChanged := mqtt.NormalizeBaseTopic(prev.config.BaseTopic) != mqtt.NormalizeBaseTopic(next.BaseTopic)

	if prefixChanged || nodeChanged {
		if !client.IsConnected() {
			return errHADiscoveryClientNotConnected // keep record, retry next publish
		}
		recPublisher := mqtt.NewDiscoveryPublisher(client, &prev.config)
		recSources, recKeys := prev.sourcesAndKeys()
		if err := recPublisher.RemoveDiscoveryWithKeys(ctx, recSources, recKeys); err != nil {
			GetLogger().Error("failed to remove HA discovery under the old identity",
				logger.Error(err),
				logger.String("operation", "ha_discovery_identity_remove"))
			return err // keep record so the next publish retries
		}
		p.haDiscoveryRecord = nil
		return nil
	}

	if baseChanged {
		if !client.IsConnected() {
			return errHADiscoveryClientNotConnected
		}
		if err := p.clearOldBaseState(ctx, client, prev); err != nil {
			GetLogger().Warn("failed to clear retained state under the old base topic",
				logger.Error(err),
				logger.String("operation", "ha_discovery_base_change"))
			return err // keep record so the next publish retries
		}
	}
	return nil
}

// clearOldBaseState empties the retained per-source state topics and the status
// topic under the record's OLD base topic, used when only the base topic changed.
func (p *Processor) clearOldBaseState(ctx context.Context, client mqtt.Client, prev *haDiscoveryRecord) error {
	oldBase := prev.config.BaseTopic
	var errs []error
	for _, ps := range prev.published {
		for _, topic := range []string{
			mqtt.SourceDetectionTopic(oldBase, ps.source.ID),
			mqtt.SourceSoundLevelTopic(oldBase, ps.source.ID),
		} {
			if err := client.PublishWithRetain(ctx, topic, "", true); err != nil {
				errs = append(errs, err)
			}
		}
	}
	if err := client.PublishWithRetain(ctx, mqtt.StatusTopic(oldBase), "", true); err != nil {
		errs = append(errs, err)
	}
	return errors.Join(errs...)
}

// removeRekeyedConfigs removes orphaned config topics for sources that are in
// both the record and the new set but whose entity key changed, unless the old
// key is now owned by a live source. Sources absent from the snapshot are never
// touched. A removal is only attempted while the client is connected. Must be
// called with haDiscoveryMu held.
func (p *Processor) removeRekeyedConfigs(ctx context.Context, publisher *mqtt.Publisher, client mqtt.Client, prev *haDiscoveryRecord, sources []datastore.AudioSource, entityKeys map[string]string) {
	if prev == nil || !client.IsConnected() {
		return
	}
	newKeyValues := make(map[string]struct{}, len(entityKeys))
	for _, k := range entityKeys {
		newKeyValues[k] = struct{}{}
	}
	present := make(map[string]struct{}, len(sources))
	for _, s := range sources {
		present[s.ID] = struct{}{}
	}
	for id, ps := range prev.published {
		if _, stillHere := present[id]; !stillHere {
			continue // absent from snapshot: may be restarting, never remove
		}
		oldKey := ps.entityKey
		newKey := entityKeys[id]
		if newKey == oldKey {
			continue
		}
		if _, owned := newKeyValues[oldKey]; owned {
			continue // a live source now owns the old key; never delete it
		}
		if err := publisher.RemoveSourceConfigs(ctx, oldKey); err != nil {
			GetLogger().Warn("failed to remove stale discovery configs for rekeyed source",
				logger.String("source_id", id),
				logger.Error(err),
				logger.String("operation", "ha_discovery_remove_stale_configs"))
		}
	}
}

// mergeDiscoveryRecord builds the new record after a successful publish: entries
// for sources absent from the snapshot are carried over (they may be restarting,
// so a later full retire or config-driven removal still cleans them up), then the
// freshly published sources overwrite/add their entries.
func mergeDiscoveryRecord(prev *haDiscoveryRecord, sources []datastore.AudioSource, entityKeys map[string]string, config *mqtt.DiscoveryConfig) *haDiscoveryRecord {
	published := make(map[string]publishedSource, len(sources))
	if prev != nil {
		present := make(map[string]struct{}, len(sources))
		for _, s := range sources {
			present[s.ID] = struct{}{}
		}
		for id, ps := range prev.published {
			if _, here := present[id]; !here {
				published[id] = ps
			}
		}
	}
	for _, s := range sources {
		published[s.ID] = publishedSource{source: s, entityKey: entityKeys[s.ID]}
	}
	return &haDiscoveryRecord{config: *config, published: published}
}

// clearLegacyStatusTopicOnce clears the pre-fix LWT/status topic "<raw>//status"
// once per process, but only when the configured base topic carries a trailing
// slash (so the raw form differs from mqtt.StatusTopic). On a base without a
// trailing slash there is no legacy topic to clean, so it is a no-op.
func (p *Processor) clearLegacyStatusTopicOnce(ctx context.Context, client mqtt.Client, config *mqtt.DiscoveryConfig) {
	legacyStatus := config.BaseTopic + "/status"
	if legacyStatus == mqtt.StatusTopic(config.BaseTopic) {
		return // no trailing slash: the raw and normalized status topics match
	}
	p.legacyStatusCleanup.Do(func() {
		if err := client.PublishWithRetain(ctx, legacyStatus, "", true); err != nil {
			GetLogger().Debug("failed to clear legacy status topic",
				logger.String("topic", legacyStatus),
				logger.Error(err),
				logger.String("operation", "ha_discovery_legacy_status"))
		}
	})
}

// RetireHomeAssistantDiscovery removes previously published HA discovery when the
// user turns HA discovery OFF while MQTT stays enabled. It uses the currently
// connected client to publish the removals under the RECORDED config and, on a
// fully successful removal, clears the record.
//
// It is a no-op when nothing was ever published, when MQTT is being disabled
// entirely (entities go unavailable via the LWT status topic, as before), or when
// HA discovery stays enabled (a discovery-identity change is handled at the next
// publish, not here). If no connected client is available the record is kept so a
// later call (e.g. the second call handleReconfigureMQTT makes once the new client
// connects) can still clean up.
//
// The record is copied under haDiscoveryMu and the removal publishes run WITHOUT
// the lock (bounded by haDiscoveryRetireTimeout), so a slow broker cannot stall
// other discovery work; the record is then cleared only if it is still the one we
// removed, so a concurrent publish that replaced it is not lost.
func (p *Processor) RetireHomeAssistantDiscovery(ctx context.Context, next *conf.Settings) {
	if next == nil {
		return
	}
	// Only retire on an HA-discovery-off transition while MQTT stays enabled.
	if !next.Realtime.MQTT.Enabled || next.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}

	p.haDiscoveryMu.Lock()
	prev := p.haDiscoveryRecord
	p.haDiscoveryMu.Unlock()
	if prev == nil {
		return // nothing was ever published, so nothing to retire
	}

	client := p.GetMQTTClient()
	if client == nil || !client.IsConnected() {
		// No connected client to publish the removal through. Keep the record so a
		// later retire (after the new client connects) can still clean up.
		return
	}

	removeCtx, cancel := context.WithTimeout(ctx, haDiscoveryRetireTimeout)
	defer cancel()

	// Remove under the RECORDED config (old prefix/base/node), not the new one.
	publisher := mqtt.NewDiscoveryPublisher(client, &prev.config)
	sources, keys := prev.sourcesAndKeys()
	if err := publisher.RemoveDiscoveryWithKeys(removeCtx, sources, keys); err != nil {
		GetLogger().Error("failed to retire HA discovery on reconfigure",
			logger.Error(err),
			logger.String("operation", "ha_discovery_retire"))
		return // keep the record so a later attempt can retry
	}

	// Clear the record only if a concurrent publish did not replace it while we
	// published without the lock.
	p.haDiscoveryMu.Lock()
	if p.haDiscoveryRecord == prev {
		p.haDiscoveryRecord = nil
	}
	p.haDiscoveryMu.Unlock()
}

// ForgetHomeAssistantSource removes one source's HA discovery when the user
// deletes that stream in settings (config-driven removal). It is called from the
// audio pipeline's reconfigure path when a source is dropped from the config.
//
// Under haDiscoveryMu, if the source is in the record and HA discovery is enabled
// and the client is connected, it clears the source's retained per-source state
// topics and removes its config topics under the recorded key, then drops it from
// the record. The config topics are removed ONLY IF no other source in the current
// registry snapshot (excluding this ID) is assigned that key by the record, so a
// source sharing a disambiguated base key does not lose its entities. When the
// client is disconnected nothing is removed and the record is kept, so a later
// retire can clean up.
func (p *Processor) ForgetHomeAssistantSource(sourceID string) {
	p.haDiscoveryMu.Lock()
	defer p.haDiscoveryMu.Unlock()

	prev := p.haDiscoveryRecord
	if prev == nil {
		return
	}
	ps, ok := prev.published[sourceID]
	if !ok {
		return // never published for this source
	}

	settings := p.currentSettings()
	if settings == nil || !settings.Realtime.MQTT.Enabled || !settings.Realtime.MQTT.HomeAssistant.Enabled {
		return
	}

	client := p.GetMQTTClient()
	if client == nil || !client.IsConnected() {
		GetLogger().Debug("HA discovery source removal deferred: MQTT client not connected",
			logger.String("source_id", sourceID),
			logger.String("operation", "ha_discovery_forget_source"))
		return // keep the record so a later retire can clean up
	}

	ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
	defer cancel()

	// Does another live source own this source's entity key by the record? If so,
	// the shared config topics belong to that live source and must not be removed.
	keyOwnedByOther := false
	for _, s := range p.getAudioSourcesForDiscovery() {
		if s.ID == sourceID {
			continue
		}
		if other, recorded := prev.published[s.ID]; recorded && other.entityKey == ps.entityKey {
			keyOwnedByOther = true
			break
		}
	}

	publisher := mqtt.NewDiscoveryPublisher(client, &prev.config)
	var errs []error
	if !keyOwnedByOther {
		if err := publisher.RemoveSourceConfigs(ctx, ps.entityKey); err != nil {
			errs = append(errs, err)
		}
	}
	if err := p.clearSourceStateTopics(ctx, client, &prev.config, ps.source); err != nil {
		errs = append(errs, err)
	}
	if err := errors.Join(errs...); err != nil {
		GetLogger().Warn("failed to remove HA discovery for deleted source",
			logger.String("source_id", sourceID),
			logger.Error(err),
			logger.String("operation", "ha_discovery_forget_source"))
		return // keep the record so a later retire can retry
	}

	delete(prev.published, sourceID)
	if len(prev.published) == 0 {
		p.haDiscoveryRecord = nil
	}
}

// clearSourceStateTopics empties one source's retained per-source detection and
// sound level state topics under config.BaseTopic.
func (p *Processor) clearSourceStateTopics(ctx context.Context, client mqtt.Client, config *mqtt.DiscoveryConfig, source datastore.AudioSource) error {
	var errs []error
	for _, topic := range []string{
		mqtt.SourceDetectionTopic(config.BaseTopic, source.ID),
		mqtt.SourceSoundLevelTopic(config.BaseTopic, source.ID),
	} {
		if err := client.PublishWithRetain(ctx, topic, "", true); err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// cleanupDefaultDiscovery removes stale HA discovery entries for the hardcoded
// "default" source that older versions published before the source registry was
// populated. These entries never matched real detection payloads and left HA
// sensors stuck at "Unknown". Only the "default" source's config topics and its
// per-source state topics are cleared; the shared bridge is left in place so a
// live install does not flap its Status entity on every startup.
func cleanupDefaultDiscovery(ctx context.Context, publisher *mqtt.Publisher) {
	defaultSource := datastore.AudioSource{ID: "default", DisplayName: "Default"}
	entityKey := mqtt.SourceEntityKeys([]datastore.AudioSource{defaultSource}, nil)[defaultSource.ID]
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
			// A source appeared or was reconfigured: republish so its entities
			// appear or update. A SourceRemoved event is deliberately NOT handled
			// here: the registry empties or drops a source transiently during a
			// restart (watchdog escalation, quiet hours, USB unplug), and treating
			// that as a deletion would wipe entities and retained state for a source
			// that comes back seconds later. A genuine config-driven deletion is
			// handled explicitly via ForgetHomeAssistantSource.
			p.scheduleDiscoveryPublish()
		case audiocore.SourceRemoved, audiocore.SourceStateChanged:
			// SourceRemoved may be a transient restart; SourceStateChanged is just
			// runtime running/stopped. Neither changes the discovered entity set.
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

// RepublishHomeAssistantDiscovery schedules a debounced HA discovery republish.
// It returns immediately (the publish runs asynchronously after the debounce
// window) so it never blocks the control monitor on a slow or unreachable broker.
// Used when a setting that changes the discovered entity set toggles at runtime,
// e.g. sound level monitoring: combined with the per-source Sound Level sensor
// removal in the discovery publisher, this adds or removes that sensor on the next
// debounced publish.
func (p *Processor) RepublishHomeAssistantDiscovery() {
	p.scheduleDiscoveryPublish()
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
	log.Info("publishing HA discovery (debounced)",
		logger.String("operation", "ha_discovery_debounced_publish"))

	ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
	defer cancel()

	if err := p.publishHomeAssistantDiscovery(ctx, client, settings); err != nil {
		log.Error("failed to publish HA discovery (debounced)",
			logger.Error(err),
			logger.String("operation", "ha_discovery_debounced_publish"))
	}
}

// Registry returns the source registry, or nil if not set.
func (p *Processor) Registry() *audiocore.SourceRegistry {
	p.registryMu.RLock()
	defer p.registryMu.RUnlock()
	return p.registry
}
