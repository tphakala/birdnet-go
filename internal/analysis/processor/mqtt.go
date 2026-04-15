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
)

// ErrMQTTClientNotReady is returned by PublishMQTT whenever the MQTT client
// reference is nil, regardless of whether MQTT is enabled in settings. In
// practice this happens when MQTT is configured but:
//   - initializeMQTT() failed to connect at startup (broker unreachable,
//     auth failure, TLS issue, etc.)
//   - The client is between DisconnectMQTTClient() and SetMQTTClient() during
//     runtime reconfiguration.
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
// When the MQTT client reference is nil (initializeMQTT() failed at startup,
// or the client is between DisconnectMQTTClient() and SetMQTTClient()), this
// returns ErrMQTTClientNotReady. Streaming publishers that run on a timer
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
	// Emit a single warn log per process lifetime so operators learn MQTT is
	// configured but not reachable. Subsequent attempts are silent — the
	// sentinel is all the caller needs to decide to skip.
	p.mqttNotReadyWarnOnce.Do(func() {
		GetLogger().Warn(
			"MQTT publish suppressed: client not ready (further suppressed publishes are silent)",
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

	// Attempt to connect to the MQTT broker
	if err := mqttClient.Connect(ctx); err != nil {
		log.Error("failed to connect to MQTT broker", logger.Error(err))
		_ = errors.New(err).
			Component("analysis.processor").
			Category(errors.CategoryMQTTConnection).
			Context("operation", "mqtt_connect").
			Build()
		return
	}

	// Set the client only if connection was successful
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

	// Register the OnConnect handler
	client.RegisterOnConnectHandler(func() {
		log.Info("MQTT connected, publishing Home Assistant discovery messages")

		// Create a context for publishing
		ctx, cancel := context.WithTimeout(context.Background(), discoveryPublishTimeout)
		defer cancel()

		// Publish discovery messages using the helper
		if err := p.publishHomeAssistantDiscovery(ctx, client, settings); err != nil {
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
func (p *Processor) publishHomeAssistantDiscovery(ctx context.Context, client mqtt.Client, settings *conf.Settings) error {
	haSettings := settings.Realtime.MQTT.HomeAssistant
	discoveryConfig := mqtt.DiscoveryConfig{
		DiscoveryPrefix: haSettings.DiscoveryPrefix,
		BaseTopic:       settings.Realtime.MQTT.Topic,
		DeviceName:      haSettings.DeviceName,
		NodeID:          settings.Main.Name,
		Version:         settings.Version,
	}

	publisher := mqtt.NewDiscoveryPublisher(client, &discoveryConfig)
	sources := p.getAudioSourcesForDiscovery()

	return publisher.PublishDiscovery(ctx, sources, settings)
}

// TriggerHomeAssistantDiscovery manually triggers Home Assistant discovery messages.
// This can be called from the API to force republishing of discovery messages.
func (p *Processor) TriggerHomeAssistantDiscovery(ctx context.Context) error {
	log := GetLogger()

	// Guard against nil settings during startup/teardown
	if p.Settings == nil {
		return errors.Newf("settings not initialized").
			Component("analysis.processor").
			Category(errors.CategoryConfiguration).
			Context("operation", "trigger_ha_discovery").
			Build()
	}

	// Check if MQTT is enabled and Home Assistant discovery is enabled
	if !p.Settings.Realtime.MQTT.Enabled {
		return errors.Newf("MQTT is not enabled").
			Component("analysis.processor").
			Category(errors.CategoryConfiguration).
			Context("operation", "trigger_ha_discovery").
			Build()
	}
	if !p.Settings.Realtime.MQTT.HomeAssistant.Enabled {
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

	if err := p.publishHomeAssistantDiscovery(publishCtx, client, p.Settings); err != nil {
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
func (p *Processor) getAudioSourcesForDiscovery() []datastore.AudioSource {
	p.registryMu.RLock()
	registry := p.registry
	p.registryMu.RUnlock()

	if registry == nil {
		// Fallback when registry is not yet injected
		return []datastore.AudioSource{{
			ID:          "default",
			DisplayName: "Default",
		}}
	}

	registrySources := registry.List()

	// Convert audiocore.AudioSource to datastore.AudioSource
	sources := make([]datastore.AudioSource, 0, len(registrySources))
	for _, src := range registrySources {
		sources = append(sources, datastore.AudioSource{
			ID:          src.ID,
			SafeString:  src.SafeString,
			DisplayName: src.DisplayName,
		})
	}

	// If no sources registered yet, create a default source
	if len(sources) == 0 {
		sources = append(sources, datastore.AudioSource{
			ID:          "default",
			DisplayName: "Default",
		})
	}

	return sources
}

// SetRegistry sets the source registry for audio source lookups.
func (p *Processor) SetRegistry(r *audiocore.SourceRegistry) {
	p.registryMu.Lock()
	defer p.registryMu.Unlock()
	p.registry = r
}

// Registry returns the source registry, or nil if not set.
func (p *Processor) Registry() *audiocore.SourceRegistry {
	p.registryMu.RLock()
	defer p.registryMu.RUnlock()
	return p.registry
}
